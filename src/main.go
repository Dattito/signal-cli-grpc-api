package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/datti-to/signal-cli-grpc-api/api"
	"github.com/datti-to/signal-cli-grpc-api/proto"
	"github.com/datti-to/signal-cli-grpc-api/utils"
	"github.com/robfig/cron/v3"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

func main() {
	signalCliConfig := flag.String("signal-cli-config", "/home/.local/share/signal-cli/", "Config directory where signal-cli config is stored")
	attachmentTmpDir := flag.String("attachment-tmp-dir", "/tmp/", "Attachment tmp directory")
	avatarTmpDir := flag.String("avatar-tmp-dir", "/tmp/", "Avatar tmp directory")
	flag.Parse()

	log.Info("Started Signal Messenger gRPC API")

	supportsSignalCliNative := "0"
	if _, err := os.Stat("/usr/bin/signal-cli-native"); err == nil {
		supportsSignalCliNative = "1"
	}

	err := os.Setenv("SUPPORTS_NATIVE", supportsSignalCliNative)
	if err != nil {
		log.Fatal("Couldn't set env variable: ", err.Error())
	}

	signalService := api.NewSignalService(*signalCliConfig, *attachmentTmpDir, *avatarTmpDir)

	port := utils.GetEnv("PORT", "9090")
	if _, err := strconv.Atoi(port); err != nil {
		log.Fatal("Invalid PORT ", port, " set. PORT needs to be a number")
	}

	autoReceiveSchedule := utils.GetEnv("AUTO_RECEIVE_SCHEDULE", "")
	if autoReceiveSchedule != "" {
		p := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		schedule, err := p.Parse(autoReceiveSchedule)
		if err != nil {
			log.Fatal("AUTO_RECEIVE_SCHEDULE: Invalid schedule: ", err.Error())
		}

		c := cron.New()
		c.Schedule(schedule, cron.FuncJob(func() {
			err := filepath.Walk(*signalCliConfig, func(path string, info os.FileInfo, err error) error {
				filename := filepath.Base(path)
				if strings.HasPrefix(filename, "+") && info.Mode().IsRegular() {
					log.Debug("AUTO_RECEIVE_SCHEDULE: Calling receive for number ", filename)
					// resp, err := http.Get("http://127.0.0.1:" + port + "/v1/receive/" + filename)
					_, err := signalService.Receive(context.TODO(), &proto.ReceiveRequest{
						Number: filename,
					})
					if err != nil {
						log.Error("AUTO_RECEIVE_SCHEDULE: Couldn't call receive for number ", filename, ": ", err.Error())
					}
				}
				return nil
			})
			if err != nil {
				log.Fatal("AUTO_RECEIVE_SCHEDULE: Couldn't get registered numbers")
			}
		}))
		c.Start()
	}

	lis, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%s", port))
	if err != nil {
		panic(err)
	}

	var opts []grpc.ServerOption
	grpcServer := grpc.NewServer(opts...)

	proto.RegisterSignalServiceServer(grpcServer, signalService)

	if err := grpcServer.Serve(lis); err != nil {
		panic(err)
	}
}
