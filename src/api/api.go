package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	securejoin "github.com/cyphar/filepath-securejoin"
	pb "github.com/datti-to/signal-cli-grpc-api/proto"
	"github.com/datti-to/signal-cli-grpc-api/utils"
	"github.com/gabriel-vasile/mimetype"
	uuid "github.com/gofrs/uuid"
	"github.com/golang/protobuf/ptypes/empty"
	"github.com/h2non/filetype"
	log "github.com/sirupsen/logrus"
	"github.com/skip2/go-qrcode"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type SignalCliGroupEntry struct {
	Name              string   `json:"name"`
	Id                string   `json:"id"`
	IsMember          bool     `json:"isMember"`
	IsBlocked         bool     `json:"isBlocked"`
	Members           []string `json:"members"`
	PendingMembers    []string `json:"pendingMembers"`
	RequestingMembers []string `json:"requestingMembers"`
	GroupInviteLink   string   `json:"groupInviteLink"`
}

const signalCliV2GroupError = "Cannot create a V2 group as self does not have a versioned profile"
const groupPrefix = "group."

func convertInternalGroupIdToGroupId(internalId string) string {
	return groupPrefix + base64.StdEncoding.EncodeToString([]byte(internalId))
}

func convertGroupIdToInternalGroupId(id string) (string, error) {
	groupIdWithoutPrefix := strings.TrimPrefix(id, groupPrefix)
	internalGroupId, err := base64.StdEncoding.DecodeString(groupIdWithoutPrefix)
	if err != nil {
		return "", errors.New("invalid group id")
	}

	return string(internalGroupId), err
}

func getStringInBetween(str string, start string, end string) (result string) {
	i := strings.Index(str, start)
	if i == -1 {
		return
	}
	i += len(start)
	j := strings.Index(str[i:], end)
	if j == -1 {
		return
	}
	return str[i : i+j]
}

func cleanupTmpFiles(paths []string) {
	for _, path := range paths {
		os.Remove(path)
	}
}

func getContainerId() (string, error) {
	data, err := ioutil.ReadFile("/proc/1/cpuset")
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return "", errors.New("couldn't get docker container id (empty)")
	}
	containerId := strings.Replace(lines[0], "/docker/", "", -1)
	return containerId, nil
}

func send(attachmentTmpDir string, signalCliConfig string, number string, message string,
	recipients []string, base64Attachments []string, isGroup bool) (*pb.SendResponse, error) {
	cmd := []string{"--config", signalCliConfig, "-u", number, "send"}

	if len(recipients) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Please specify at least one recipient")
	}

	if !isGroup {
		cmd = append(cmd, recipients...)
	} else {
		if len(recipients) > 1 {
			return nil, status.Error(codes.InvalidArgument, "More than one recipient is currently not allowed")
		}

		groupId, err := base64.StdEncoding.DecodeString(recipients[0])
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "More than one recipient is currently not allowed")
		}

		cmd = append(cmd, []string{"-g", string(groupId)}...)
	}

	attachmentTmpPaths := []string{}
	for _, base64Attachment := range base64Attachments {
		u, err := uuid.NewV4()
		if err != nil {
			return nil, status.Error(codes.Unknown, err.Error())
		}

		dec, err := base64.StdEncoding.DecodeString(base64Attachment)
		if err != nil {
			return nil, status.Error(codes.Unknown, err.Error())
		}

		mimeType := mimetype.Detect(dec)

		attachmentTmpPath := attachmentTmpDir + u.String() + mimeType.Extension()
		attachmentTmpPaths = append(attachmentTmpPaths, attachmentTmpPath)

		f, err := os.Create(attachmentTmpPath)
		if err != nil {
			return nil, status.Error(codes.Unknown, err.Error())
		}
		defer f.Close()

		if _, err := f.Write(dec); err != nil {
			cleanupTmpFiles(attachmentTmpPaths)
			return nil, status.Error(codes.Unknown, err.Error())
		}
		if err := f.Sync(); err != nil {
			cleanupTmpFiles(attachmentTmpPaths)
			return nil, status.Error(codes.Unknown, err.Error())
		}
		f.Close()
	}

	if len(attachmentTmpPaths) > 0 {
		cmd = append(cmd, "-a")
		cmd = append(cmd, attachmentTmpPaths...)
	}

	resp, err := runSignalCli(true, cmd, message)
	if err != nil {
		cleanupTmpFiles(attachmentTmpPaths)
		if strings.Contains(err.Error(), signalCliV2GroupError) {
			return nil, status.Error(codes.InvalidArgument, "Cannot send message to group - please first update your profile.")
		}
		return nil, status.Error(codes.Unknown, err.Error())
	}

	cleanupTmpFiles(attachmentTmpPaths)

	t, err := strconv.ParseInt(strings.TrimSuffix(resp, "\n"), 10, 64)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &pb.SendResponse{Timestamp: &timestamppb.Timestamp{Seconds: int64(t)}}, nil
}

func parseWhitespaceDelimitedKeyValueStringList(in string, keys []string) []map[string]string {
	l := []map[string]string{}
	lines := strings.Split(in, "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}

		m := make(map[string]string)

		temp := line
		for i, key := range keys {
			if i == 0 {
				continue
			}

			idx := strings.Index(temp, " "+key+": ")
			pair := temp[:idx]
			value := strings.TrimPrefix(pair, key+": ")
			temp = strings.TrimLeft(temp[idx:], " "+key+": ")

			m[keys[i-1]] = value
		}
		m[keys[len(keys)-1]] = temp

		l = append(l, m)
	}
	return l
}

func getGroups(number string, signalCliConfig string) ([]*pb.GetGroupResponse, error) {
	groupEntries := []*pb.GetGroupResponse{}

	out, err := runSignalCli(true, []string{"--config", signalCliConfig, "--output", "json", "-u", number, "listGroups", "-d"}, "")
	if err != nil {
		return groupEntries, err
	}

	var signalCliGroupEntries []SignalCliGroupEntry

	err = json.Unmarshal([]byte(out), &signalCliGroupEntries)
	if err != nil {
		return groupEntries, err
	}

	for _, signalCliGroupEntry := range signalCliGroupEntries {
		var groupEntry pb.GetGroupResponse
		groupEntry.InternalId = signalCliGroupEntry.Id
		groupEntry.Name = signalCliGroupEntry.Name
		groupEntry.Id = convertInternalGroupIdToGroupId(signalCliGroupEntry.Id)
		groupEntry.Blocked = signalCliGroupEntry.IsBlocked
		groupEntry.Members = signalCliGroupEntry.Members
		groupEntry.PendingRequests = signalCliGroupEntry.PendingMembers
		groupEntry.PendingInvites = signalCliGroupEntry.RequestingMembers
		groupEntry.InviteLink = signalCliGroupEntry.GroupInviteLink

		groupEntries = append(groupEntries, &groupEntry)
	}

	return groupEntries, nil
}

func runSignalCli(wait bool, args []string, stdin string) (string, error) {
	containerId, err := getContainerId()

	log.Debug("If you want to run this command manually, run the following steps on your host system:")
	if err == nil {
		log.Debug("*) docker exec -it ", containerId, " /bin/bash")
	} else {
		log.Debug("*) docker exec -it <container id> /bin/bash")
	}

	signalCliBinary := "signal-cli"
	if utils.GetEnv("USE_NATIVE", "0") == "1" {
		if utils.GetEnv("SUPPORTS_NATIVE", "0") == "1" {
			signalCliBinary = "signal-cli-native"
		} else {
			log.Error("signal-cli-native is not support on this system...falling back to signal-cli")
			signalCliBinary = "signal-cli"
		}
	}

	fullCmd := ""
	if stdin != "" {
		fullCmd += "echo '" + stdin + "' | "
	}
	fullCmd += signalCliBinary + " " + strings.Join(args, " ")

	log.Debug("*) su signal-api")
	log.Debug("*) ", fullCmd)

	cmdTimeout, err := utils.GetIntEnv("SIGNAL_CLI_CMD_TIMEOUT", 60)
	if err != nil {
		log.Error("Env variable 'SIGNAL_CLI_CMD_TIMEOUT' contains an invalid timeout...falling back to default timeout (60 seconds)")
		cmdTimeout = 60
	}

	cmd := exec.Command(signalCliBinary, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	if wait {
		var errBuffer bytes.Buffer
		var outBuffer bytes.Buffer
		cmd.Stderr = &errBuffer
		cmd.Stdout = &outBuffer

		err := cmd.Start()
		if err != nil {
			return "", err
		}

		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
		}()
		select {
		case <-time.After(time.Duration(cmdTimeout) * time.Second):
			err := cmd.Process.Kill()
			if err != nil {
				return "", err
			}
			return "", errors.New("process killed as timeout reached")
		case err := <-done:
			if err != nil {
				return "", errors.New(errBuffer.String())
			}
		}

		return outBuffer.String(), nil
	} else {
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return "", err
		}
		cmd.Start()
		buf := bufio.NewReader(stdout) // Notice that this is not in a loop
		line, _, _ := buf.ReadLine()
		return string(line), nil
	}
}

type SignalService struct {
	pb.UnimplementedSignalServiceServer
	signalCliConfig  string
	attachmentTmpDir string
	avatarTmpDir     string
}

func NewSignalService(signalCliConfig string, attachmentTmpDir string, avatarTmpDir string) *SignalService {
	return &SignalService{
		signalCliConfig:  signalCliConfig,
		attachmentTmpDir: attachmentTmpDir,
		avatarTmpDir:     avatarTmpDir,
	}
}

func (s *SignalService) About(ctx context.Context, _ *emptypb.Empty) (*pb.AboutResponse, error) {

	return &pb.AboutResponse{
		Build:    2,
		Versions: []string{"v1", "v2"},
	}, nil
}

func (s *SignalService) GetConfiguration(ctx context.Context, _ *empty.Empty) (*pb.GetConfigurationResponse, error) {
	logLevel := ""
	if log.GetLevel() == log.DebugLevel {
		logLevel = "debug"
	} else if log.GetLevel() == log.InfoLevel {
		logLevel = "info"
	} else if log.GetLevel() == log.WarnLevel {
		logLevel = "warn"
	}

	return &pb.GetConfigurationResponse{
		Logging: &pb.Logging{
			Level: logLevel,
		},
	}, nil
}

func (s *SignalService) SetConfiguration(ctx context.Context, in *pb.SetConfigurationRequest) (*empty.Empty, error) {
	if in.Logging.Level != "" {
		if in.Logging.Level == "debug" {
			log.SetLevel(log.DebugLevel)
		} else if in.Logging.Level == "info" {
			log.SetLevel(log.InfoLevel)
		} else if in.Logging.Level == "warn" {
			log.SetLevel(log.WarnLevel)
		} else {
			return nil, status.Error(codes.InvalidArgument, "Couldn't set log level - invalid log level")
		}
	}

	return &empty.Empty{}, nil
}

func (s *SignalService) Health(ctx context.Context, _ *empty.Empty) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (s *SignalService) RegisterNumber(ctx context.Context, in *pb.RegisterNumberRequest) (*empty.Empty, error) {
	if in.Number == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number")
	}

	command := []string{"--config", s.signalCliConfig, "-u", in.Number, "register"}

	if in.UseVoice {
		command = append(command, "--voice")
	}

	if in.Captcha != "" {
		command = append(command, []string{"--captcha", in.Captcha}...)
	}

	_, err := runSignalCli(true, command, "")
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	return &empty.Empty{}, nil
}

func (s *SignalService) VerifyRegisteredNumber(ctx context.Context, in *pb.VerifyRegisteredNumberRequest) (*empty.Empty, error) {
	if in.Number == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number")
	}

	if in.Token == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a verification code")
	}

	cmd := []string{"--config", s.signalCliConfig, "-u", in.Number, "verify", in.Token}
	if in.Pin != "" {
		cmd = append(cmd, "--pin")
		cmd = append(cmd, in.Pin)
	}
	_, err := runSignalCli(true, cmd, "")
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}
	return &empty.Empty{}, nil
}

func (s *SignalService) Send(ctx context.Context, in *pb.SendRequest) (*pb.SendResponse, error) {

	base64Attachments := []string{}
	if in.Base64Attachment != "" {
		base64Attachments = append(base64Attachments, in.Base64Attachment)
	}
	resp, err := send(s.attachmentTmpDir, s.signalCliConfig, in.Number, in.Message, in.Recipients, base64Attachments, in.IsGroup)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func (s *SignalService) Receive(ctx context.Context, in *pb.ReceiveRequest) (*pb.ReceiveResponse, error) {
	if in.Timeout == "" {
		in.Timeout = "1"
	}
	command := []string{"--config", s.signalCliConfig, "--output", "json", "-u", in.Number, "receive", "-t", in.Timeout}

	out, err := runSignalCli(true, command, "")
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	out = strings.Trim(out, "\n")
	lines := strings.Split(out, "\n")

	return &pb.ReceiveResponse{
		Messages: lines,
	}, nil
}

func (s *SignalService) CreateGroup(ctx context.Context, in *pb.CreateGroupRequest) (*pb.CreateGroupResponse, error) {
	if in.Number == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number")
	}

	if in.Permissions.AddMembers != "" && !utils.StringInSlice(in.Permissions.AddMembers, []string{"every-member", "only-admins"}) {
		return nil, status.Error(codes.InvalidArgument, "Invalid edit group permissions provided - only 'every-member' and 'only-admins' allowed!")
	}

	if in.Permissions.EditGroup != "" && !utils.StringInSlice(in.Permissions.EditGroup, []string{"every-member", "only-admins"}) {
		return nil, status.Error(codes.InvalidArgument, "Invalid add members permission provided - only 'every-member' and 'only-admins' allowed!")
	}

	if in.GroupLink != "" && !utils.StringInSlice(in.GroupLink, []string{"enabled", "enabled-with-approval", "disabled"}) {
		return nil, status.Error(codes.InvalidArgument, "Invalid group link provided - only 'enabled', 'enabled-with-approval' and 'disabled' allowed!")
	}

	cmd := []string{"--config", s.signalCliConfig, "-u", in.Number, "updateGroup", "-n", in.Name, "-m"}
	cmd = append(cmd, in.Members...)

	if in.Permissions.AddMembers != "" {
		cmd = append(cmd, []string{"--set-permissions-add-member", in.Permissions.AddMembers}...)
	}

	if in.Permissions.EditGroup != "" {
		cmd = append(cmd, []string{"--set-permission-edit-details", in.Permissions.EditGroup}...)
	}

	if in.GroupLink != "" {
		cmd = append(cmd, []string{"--link", in.GroupLink}...)
	}

	if in.Description != "" {
		cmd = append(cmd, []string{"--description", in.Description}...)
	}

	out, err := runSignalCli(true, cmd, "")
	if err != nil {
		if strings.Contains(err.Error(), signalCliV2GroupError) {
			return nil, status.Error(codes.FailedPrecondition, "Cannot create group - please first update your profile.")
		} else {
			return nil, status.Error(codes.Unknown, err.Error())
		}
	}

	internalGroupId := getStringInBetween(out, `"`, `"`)
	return &pb.CreateGroupResponse{
		Id: convertInternalGroupIdToGroupId(internalGroupId),
	}, nil
}

func (s *SignalService) GetGroups(ctx context.Context, in *pb.GetGroupsRequest) (*pb.GetGroupsResponse, error) {
	if in.Number == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number")
	}

	groups, err := getGroups(in.Number, s.signalCliConfig)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	return &pb.GetGroupsResponse{
		Groups: groups,
	}, nil
}

func (s *SignalService) GetGroup(ctx context.Context, in *pb.GroupRequest) (*pb.GetGroupResponse, error) {
	if in.Number == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number")
	}

	if in.Groupid == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a group id")
	}

	groups, err := getGroups(in.Number, s.signalCliConfig)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	for _, group := range groups {
		if group.Id == in.Groupid {
			return group, nil
		}
	}

	return nil, status.Error(codes.NotFound, "No group with that id found")
}

func BlockGroup(s *SignalService, in *pb.GroupActionRequest) error {
	if in.Number == "" {
		return status.Error(codes.InvalidArgument, "Please provide a number")
	}

	if in.Groupid == "" {
		return status.Error(codes.InvalidArgument, "Please provide a group id")
	}

	internalGroupId, err := convertGroupIdToInternalGroupId(in.Groupid)
	if err != nil {
		return status.Error(codes.InvalidArgument, "Please provide a number")
	}

	_, err = runSignalCli(true, []string{"--config", s.signalCliConfig, "-u", in.Number, "block", "-g", internalGroupId}, "")
	if err != nil {
		return status.Error(codes.Unknown, err.Error())
	}

	return nil
}

func (s *SignalService) GroupAction(ctx context.Context, in *pb.GroupActionRequest) (*empty.Empty, error) {
	m := make(map[int32]string)
	m[int32(pb.GroupActionRequest_DELETE)] = "quitGroup"
	m[int32(pb.GroupActionRequest_BLOCK)] = "block"
	m[int32(pb.GroupActionRequest_JOIN)] = "updateGroup"
	m[int32(pb.GroupActionRequest_QUIT)] = "quitGroup"

	if in.Number == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number")
	}

	if in.Groupid == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a group id")
	}

	internalGroupId, err := convertGroupIdToInternalGroupId(in.Groupid)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number")
	}

	_, err = runSignalCli(true, []string{"--config", s.signalCliConfig, "-u", in.Number, m[int32(in.Action)], "-g", internalGroupId}, "")
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	return &empty.Empty{}, nil
}

func (s *SignalService) GetQrCodeLink(ctx context.Context, in *pb.GetQrCodeLinkRequest) (*pb.GetQrCodeLinkResponse, error) {
	if in.DeviceName == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a name for the device")
	}

	command := []string{"--config", s.signalCliConfig, "link", "-n", in.DeviceName}

	tsdeviceLink, err := runSignalCli(false, command, "")
	if err != nil {
		log.Error("Couldn't create QR code: ", err.Error())
		return nil, status.Error(codes.Internal, "Couldn't create QR code: "+err.Error())
	}

	q, err := qrcode.New(string(tsdeviceLink), qrcode.Medium)
	if err != nil {
		log.Error("Couldn't create QR code: ", err.Error())
		return nil, status.Error(codes.Internal, "Couldn't create QR code: "+err.Error())
	}

	q.DisableBorder = false
	var png []byte
	png, err = q.PNG(256)
	if err != nil {
		log.Error("Couldn't create QR code: ", err.Error())
		return nil, status.Error(codes.Internal, "Couldn't create QR code: "+err.Error())
	}

	return &pb.GetQrCodeLinkResponse{
		Image: png,
	}, nil
}

func (s *SignalService) GetAttachments(ctx context.Context, _ *empty.Empty) (*pb.GetAttachmentsResponse, error) {
	files := []string{}
	err := filepath.Walk(s.signalCliConfig+"/attachments/", func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}
		files = append(files, filepath.Base(path))
		return nil
	})

	if err != nil {
		return nil, status.Error(codes.Internal, "Couldn't get list of attachments: "+err.Error())
	}

	return &pb.GetAttachmentsResponse{
		Attachments: files,
	}, nil
}

func (s *SignalService) RemoveAttachment(ctx context.Context, in *pb.RemoveAttachmentRequest) (*empty.Empty, error) {
	path, err := securejoin.SecureJoin(s.signalCliConfig+"/attachments/", in.Attachment)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "Please provide a valid attachment name")
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, status.Error(codes.NotFound, "No attachment with that name found")
	}
	err = os.Remove(path)
	if err != nil {
		return nil, status.Error(codes.Internal, "Couldn't delete attachment - please try again later")

	}

	return &empty.Empty{}, nil
}

func (s *SignalService) ServeAttachment(ctx context.Context, in *pb.ServeAttachmentRequest) (*pb.ServeAttachmentResponse, error) {
	path, err := securejoin.SecureJoin(s.signalCliConfig+"/attachments/", in.Attachment)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "Please provide a valid attachment name")
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, status.Error(codes.NotFound, "No attachment with that name found")

	}

	imgBytes, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, status.Error(codes.Internal, "Couldn't read attachment - please try again later")

	}

	return &pb.ServeAttachmentResponse{
		Attachment: imgBytes,
	}, nil
}

func (s *SignalService) UpdateProfile(ctx context.Context, in *pb.UpdateProfileRequest) (*empty.Empty, error) {
	if in.Number == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number")
	}

	if in.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a profile name")
	}
	cmd := []string{"--config", s.signalCliConfig, "-u", in.Number, "updateProfile", "--name", in.Name}

	avatarTmpPaths := []string{}
	if in.Base64Avatar == "" {
		cmd = append(cmd, "--remove-avatar")
	} else {
		u, err := uuid.NewV4()
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}

		avatarBytes, err := base64.StdEncoding.DecodeString(in.Base64Avatar)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "Couldn't decode base64 encoded avatar")
		}

		fType, err := filetype.Get(avatarBytes)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}

		avatarTmpPath := s.avatarTmpDir + u.String() + "." + fType.Extension

		f, err := os.Create(avatarTmpPath)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		defer f.Close()

		if _, err := f.Write(avatarBytes); err != nil {
			cleanupTmpFiles(avatarTmpPaths)
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		if err := f.Sync(); err != nil {
			cleanupTmpFiles(avatarTmpPaths)
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		f.Close()

		cmd = append(cmd, []string{"--avatar", avatarTmpPath}...)
		avatarTmpPaths = append(avatarTmpPaths, avatarTmpPath)
	}

	_, err := runSignalCli(true, cmd, "")
	if err != nil {
		cleanupTmpFiles(avatarTmpPaths)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	cleanupTmpFiles(avatarTmpPaths)
	return &empty.Empty{}, nil
}

func (s *SignalService) ListIdentities(ctx context.Context, in *pb.ListIdentitiesRequest) (*pb.ListIdentitiesResponse, error) {
	if in.Number == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number")
	}

	out, err := runSignalCli(true, []string{"--config", s.signalCliConfig, "-u", in.Number, "listIdentities"}, "")
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())

	}

	identityEntries := []*pb.ListIdentitiesResponse_ListIdentityResponse{}
	keyValuePairs := parseWhitespaceDelimitedKeyValueStringList(out, []string{"NumberAndTrustStatus", "Added", "Fingerprint", "Safety Number"})
	for _, keyValuePair := range keyValuePairs {
		numberAndTrustStatus := keyValuePair["NumberAndTrustStatus"]
		numberAndTrustStatusSplitted := strings.Split(numberAndTrustStatus, ":")

		identityEntry := &pb.ListIdentitiesResponse_ListIdentityResponse{Number: strings.Trim(numberAndTrustStatusSplitted[0], " "),
			Status:       strings.Trim(numberAndTrustStatusSplitted[1], " "),
			Added:        keyValuePair["Added"],
			Fingerprint:  strings.Trim(keyValuePair["Fingerprint"], " "),
			SafetyNumber: strings.Trim(keyValuePair["Safety Number"], " "),
		}
		identityEntries = append(identityEntries, identityEntry)
	}

	return &pb.ListIdentitiesResponse{
		Identities: identityEntries,
	}, nil
}

func (s *SignalService) TrustIdentity(ctx context.Context, in *pb.TrustIdentityRequest) (*empty.Empty, error) {
	if in.Number == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number")
	}

	if in.NumberToTrust == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number to trust")
	}

	if in.VerifiedSafetyNumber == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a verified safety number")
	}

	cmd := []string{"--config", s.signalCliConfig, "-u", in.Number, "trust", in.NumberToTrust, "--verified-safety-number", in.VerifiedSafetyNumber}
	_, err := runSignalCli(true, cmd, "")
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &empty.Empty{}, nil
}

func (s *SignalService) SendV2(ctx context.Context, in *pb.SendV2Request) (*pb.SendResponse, error) {
	if len(in.Recipients) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Please provide at least one recipient")
	}

	groups := []string{}
	recipients := []string{}

	for _, recipient := range in.Recipients {
		if strings.HasPrefix(recipient, groupPrefix) {
			groups = append(groups, strings.TrimPrefix(recipient, groupPrefix))
		} else {
			recipients = append(recipients, recipient)
		}
	}

	if len(recipients) > 0 && len(groups) > 0 {
		return nil, status.Error(codes.InvalidArgument, "Signal Messenger Groups and phone numbers cannot be specified together in one request! Please split them up into multiple REST API calls.")
	}

	if len(groups) > 1 {
		return nil, status.Error(codes.InvalidArgument, "A signal message cannot be sent to more than one group at once! Please use multiple REST API calls for that.")
	}

	for _, group := range groups {
		_, err := send(s.attachmentTmpDir, s.signalCliConfig, in.Number, in.Message, []string{group}, in.Base64Attachments, true)
		if err != nil {
			return nil, err
		}
	}
	if len(groups) > 0 {
		return &pb.SendResponse{Timestamp: timestamppb.Now()}, nil
	}

	if len(recipients) > 0 {
		resp, err := send(s.attachmentTmpDir, s.signalCliConfig, in.Number, in.Message, recipients, in.Base64Attachments, false)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}

	return nil, status.Error(codes.Unknown, "This should not happen.")
}
