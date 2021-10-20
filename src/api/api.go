package api

import (
	"context"
	"encoding/json"

	"github.com/golang/protobuf/ptypes/empty"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dattito/signal-cli-grpc-api/client"
	pb "github.com/dattito/signal-cli-grpc-api/proto"
	utils "github.com/dattito/signal-cli-grpc-api/utils"
)

type Api struct {
	pb.UnimplementedSignalServiceServer
	signalClient *client.SignalClient
}

func NewApi(signalClient *client.SignalClient) *Api {
	return &Api{
		signalClient: signalClient,
	}
}

func (a *Api) About(ctx context.Context, _ *emptypb.Empty) (*pb.AboutResponse, error) {

	b := a.signalClient.About()

	return &pb.AboutResponse{
		Build:                int32(b.BuildNr),
		SupportedApiVersions: b.SupportedApiVersions,
	}, nil
}

func (a *Api) RegisterNumber(ctx context.Context, in *pb.RegisterNumberRequest) (*empty.Empty, error) {
	if in.Number == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number")
	}

	err := a.signalClient.RegisterNumber(in.Number, in.UseVoice, in.Captcha)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	return &empty.Empty{}, nil
}

func (a *Api) VerifyRegisteredNumber(ctx context.Context, in *pb.VerifyRegisteredNumberRequest) (*empty.Empty, error) {
	if in.Number == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number")
	}

	if in.Token == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a verification code")
	}

	err := a.signalClient.VerifyRegisteredNumber(in.Number, in.Token, in.Pin)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}
	return &empty.Empty{}, nil
}

func (a *Api) Send(ctx context.Context, in *pb.SendRequest) (*pb.SendResponse, error) {

	base64Attachments := []string{}
	if in.Base64Attachment != "" {
		base64Attachments = append(base64Attachments, in.Base64Attachment)
	}

	timestamp, err := a.signalClient.SendV1(in.Number, in.Message, in.Recipients, base64Attachments, in.IsGroup)
	if err != nil {
		return nil, err
	}

	return &pb.SendResponse{
		Timestamp: &timestamppb.Timestamp{
			Seconds: timestamp.Timestamp,
		},
	}, nil
}

func (a *Api) SendV2(ctx context.Context, in *pb.SendV2Request) (*pb.SendResponse, error) {
	if len(in.Recipients) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Please provide at least one recipient")
	}

	if in.Number == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number")
	}

	timestamp, err := a.signalClient.SendV2(in.Number, in.Message, in.Recipients, in.Base64Attachments)
	if err != nil {
		return nil, err
	}

	return &pb.SendResponse{
		Timestamp: &timestamppb.Timestamp{
			Seconds: (*timestamp)[0].Timestamp,
		},
	}, nil
}

func (a *Api) Receive(ctx context.Context, in *pb.ReceiveRequest) (*pb.ReceiveResponse, error) {
	if in.Timeout == 0 {
		in.Timeout = 1
	}

	jsonStr, err := a.signalClient.Receive(in.Number, int64(in.Timeout))
	if err != nil {
		return nil, err
	}

	slice_messages := []string{}

	if err := json.Unmarshal([]byte(jsonStr), &slice_messages); err != nil {
		return nil, err
	}

	return &pb.ReceiveResponse{
		Messages: slice_messages,
	}, nil
}

func (a *Api) CreateGroup(ctx context.Context, in *pb.CreateGroupRequest) (*pb.CreateGroupResponse, error) {
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

	editGroupPermission := client.DefaultGroupPermission
	addMembersPermission := client.DefaultGroupPermission
	groupLinkState := client.DefaultGroupLinkState

	groupId, err := a.signalClient.CreateGroup(in.Number, in.Name, in.Members, in.Description, editGroupPermission, addMembersPermission, groupLinkState)
	if err != nil {
		return nil, err
	}

	return &pb.CreateGroupResponse{
		Id: groupId,
	}, nil
}

func (a *Api) GetGroups(ctx context.Context, in *pb.GetGroupsRequest) (*pb.GetGroupsResponse, error) {
	if in.Number == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number")
	}

	groups, err := a.signalClient.GetGroups(in.Number)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	grpc_groups := []*pb.GetGroupResponse{}

	for _, group := range groups {
		grpc_groups = append(grpc_groups, &pb.GetGroupResponse{
			Name:            group.Name,
			Id:              group.Id,
			InternalId:      group.InternalId,
			Members:         group.Members,
			Blocked:         group.Blocked,
			PendingInvites:  group.PendingInvites,
			PendingRequests: group.PendingRequests,
			InviteLink:      group.InviteLink,
		})
	}

	return &pb.GetGroupsResponse{
		Groups: grpc_groups,
	}, nil
}

func (a *Api) GetGroup(ctx context.Context, in *pb.GroupRequest) (*pb.GetGroupResponse, error) {
	if in.Number == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number")
	}

	if in.Groupid == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a group id")
	}

	group, err := a.signalClient.GetGroup(in.Number, in.Groupid)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	if group == nil {
		return nil, status.Error(codes.NotFound, "Group not found")
	}

	return &pb.GetGroupResponse{
		Name:            group.Name,
		Id:              group.Id,
		InternalId:      group.InternalId,
		Members:         group.Members,
		Blocked:         group.Blocked,
		PendingInvites:  group.PendingInvites,
		PendingRequests: group.PendingRequests,
		InviteLink:      group.InviteLink,
	}, nil
}

func (a *Api) DeleteGroup(ctx context.Context, in *pb.GroupRequest) (*empty.Empty, error) {
	if in.Number == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number")
	}

	if in.Groupid == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a group id")
	}

	groupId, err := client.ConvertGroupIdToInternalGroupId(in.Groupid)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	err = a.signalClient.DeleteGroup(in.Number, groupId)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	return &empty.Empty{}, nil
}

func (a *Api) GetQrCodeLink(ctx context.Context, in *pb.GetQrCodeLinkRequest) (*pb.GetQrCodeLinkResponse, error) {
	if in.DeviceName == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a name for the device")
	}

	png, err := a.signalClient.GetQrCodeLink(in.DeviceName)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	return &pb.GetQrCodeLinkResponse{
		Image: png,
	}, nil
}

func (a *Api) GetAttachments(ctx context.Context, _ *empty.Empty) (*pb.GetAttachmentsResponse, error) {
	files, err := a.signalClient.GetAttachments()
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	return &pb.GetAttachmentsResponse{
		Attachments: files,
	}, nil
}

func (a *Api) RemoveAttachment(ctx context.Context, in *pb.RemoveAttachmentRequest) (*empty.Empty, error) {
	err := a.signalClient.RemoveAttachment(in.Attachment)

	if err != nil {
		switch err.(type) {
		case *client.InvalidNameError:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case *client.NotFoundError:
			return nil, status.Error(codes.NotFound, err.Error())
		case *client.InternalError:
			return nil, status.Error(codes.Internal, err.Error())
		default:
			return nil, status.Error(codes.Unknown, err.Error())
		}
	}
	return &empty.Empty{}, nil
}

func (a *Api) ServeAttachment(ctx context.Context, in *pb.ServeAttachmentRequest) (*pb.ServeAttachmentResponse, error) {
	attachmentBytes, err := a.signalClient.GetAttachment(in.Attachment)

	if err != nil {
		switch err.(type) {
		case *client.InvalidNameError:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case *client.NotFoundError:
			return nil, status.Error(codes.NotFound, err.Error())
		case *client.InternalError:
			return nil, status.Error(codes.Internal, err.Error())
		default:
			return nil, status.Error(codes.Unknown, err.Error())
		}
	}

	return &pb.ServeAttachmentResponse{
		Attachment: attachmentBytes,
	}, nil
}

func (a *Api) UpdateProfile(ctx context.Context, in *pb.UpdateProfileRequest) (*empty.Empty, error) {
	if in.Number == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number")
	}

	if in.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a profile name")
	}

	err := a.signalClient.UpdateProfile(in.Number, in.Name, in.Base64Avatar)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	return &empty.Empty{}, nil
}

func (a *Api) Health(ctx context.Context, _ *empty.Empty) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (a *Api) ListIdentities(ctx context.Context, in *pb.ListIdentitiesRequest) (*pb.ListIdentitiesResponse, error) {
	if in.Number == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number")
	}

	identities, err := a.signalClient.ListIdentities(in.Number)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	grpc_identities := []*pb.ListIdentitiesResponse_ListIdentityResponse{}
	for _, identity := range *identities {
		grpc_identities = append(grpc_identities, &pb.ListIdentitiesResponse_ListIdentityResponse{
			Added:        identity.Added,
			Fingerprint:  identity.Fingerprint,
			Number:       identity.Number,
			SafetyNumber: identity.SafetyNumber,
			Status:       identity.Status,
		})
	}

	return &pb.ListIdentitiesResponse{
		Identities: grpc_identities,
	}, nil
}

func (a *Api) TrustIdentity(ctx context.Context, in *pb.TrustIdentityRequest) (*empty.Empty, error) {
	if in.Number == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number")
	}

	if in.NumberToTrust == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number to trust")
	}

	if in.VerifiedSafetyNumber == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a verified safety number")
	}

	err := a.signalClient.TrustIdentity(in.Number, in.NumberToTrust, in.VerifiedSafetyNumber)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	return &empty.Empty{}, nil
}

func (a *Api) SetConfiguration(ctx context.Context, in *pb.SetConfigurationRequest) (*empty.Empty, error) {
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

func (a *Api) GetConfiguration(ctx context.Context, _ *empty.Empty) (*pb.GetConfigurationResponse, error) {
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

func (a *Api) BlockGroup(ctx context.Context, in *pb.GroupRequest) (*empty.Empty, error) {
	if in.Number == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number")
	}

	if in.Groupid == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a group id")
	}

	err := a.signalClient.BlockGroup(in.Number, in.Groupid)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	return &empty.Empty{}, nil
}

func (a *Api) JoinGroup(ctx context.Context, in *pb.GroupRequest) (*empty.Empty, error) {
	if in.Number == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number")
	}

	if in.Groupid == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a group id")
	}

	err := a.signalClient.JoinGroup(in.Number, in.Groupid)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	return &empty.Empty{}, nil
}

func (a *Api) QuitGroup(ctx context.Context, in *pb.GroupRequest) (*empty.Empty, error) {
	if in.Number == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a number")
	}

	if in.Groupid == "" {
		return nil, status.Error(codes.InvalidArgument, "Please provide a group id")
	}

	err := a.signalClient.QuitGroup(in.Number, in.Groupid)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	return &empty.Empty{}, nil
}
