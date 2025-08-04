package cmd

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"

	servicesv1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qcontroller/src/pkg/protos"
	"github.com/q-controller/qcontroller/src/pkg/utils"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

func readConfig(path string) (*settingsv1.ControllerConfig, error) {
	value := &settingsv1.ControllerConfig{}
	if unmarshalErr := utils.Unmarshal(value, path); unmarshalErr != nil {
		return nil, unmarshalErr
	}

	config := getDefaultConfig()
	proto.Merge(config, value)

	if config.Cache == nil {
		return nil, fmt.Errorf("failed to read config: image cache is not set")
	}

	return config, nil
}

func getDefaultConfig() *settingsv1.ControllerConfig {
	return &settingsv1.ControllerConfig{
		Port: 80000,
	}
}

type localFetcher struct {
	fileRegistry servicesv1.FileRegistryServiceServer
}

type downloadStream struct {
	grpc.ServerStream
	file *os.File
}

func (m *downloadStream) Send(resp *servicesv1.DownloadImageResponse) error {
	_, err := m.file.Write(resp.Chunk)
	return err
}

func (l *localFetcher) Get(id, path string) (retErr error) {
	file, fileErr := os.Create(path)
	if fileErr != nil {
		return fileErr
	}

	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			if retErr == nil {
				// Return close error if no other error
				retErr = closeErr
			} else {
				retErr = errors.Join(retErr, closeErr)
			}
		}
	}()

	stream := &downloadStream{file: file}
	return l.fileRegistry.DownloadImage(&servicesv1.DownloadImageRequest{
		ImageId: id,
	}, stream)
}

var controllerCmd = &cobra.Command{
	Use:   "controller",
	Short: "Starts QEMU instances controller",
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath, configPathErr := cmd.Flags().GetString("config")
		if configPathErr != nil {
			return fmt.Errorf("failed to get config: %w", configPathErr)
		}

		config, configErr := readConfig(configPath)
		if configErr != nil {
			return fmt.Errorf("wrong config file format: %w", configErr)
		}
		slog.Debug("Read config", "config", config)

		lis, lisErr := net.Listen("tcp", fmt.Sprintf(":%d", config.Port))
		if lisErr != nil {
			return fmt.Errorf("failed to listen: %w", lisErr)
		}

		s := grpc.NewServer()

		reg, regErr := protos.NewFileRegistry(filepath.Join(config.Root, config.Cache.Root))
		if regErr != nil {
			return fmt.Errorf("failed to create server %w", regErr)
		}
		servicesv1.RegisterFileRegistryServiceServer(s, reg)

		contr, contrErr := protos.NewController(config, &localFetcher{
			fileRegistry: reg,
		})
		if contrErr != nil {
			return fmt.Errorf("failed to create server %w", contrErr)
		}
		servicesv1.RegisterControllerServiceServer(s, contr)

		if servErr := s.Serve(lis); servErr != nil {
			return fmt.Errorf("failed to serve: %w", servErr)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(controllerCmd)

	controllerCmd.Flags().StringP("config", "c", "", "Path to controller's config file")
	if err := controllerCmd.MarkFlagRequired("config"); err != nil {
		panic(fmt.Errorf("failed to mark flag `config` as required: %w", err))
	}
}
