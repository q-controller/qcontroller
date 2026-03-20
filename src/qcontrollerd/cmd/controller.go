package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	servicesv1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qcontroller/src/pkg/events"
	"github.com/q-controller/qcontroller/src/pkg/images"
	"github.com/q-controller/qcontroller/src/pkg/protos"
	"github.com/q-controller/qcontroller/src/pkg/utils"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var controllerCmd = &cobra.Command{
	Use:   "controller",
	Short: "Starts QEMU instances controller",
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath, configPathErr := cmd.Flags().GetString("config")
		if configPathErr != nil {
			return fmt.Errorf("failed to get config: %w", configPathErr)
		}

		config := &settingsv1.ControllerConfig{}
		if unmarshalErr := utils.Unmarshal(config, configPath); unmarshalErr != nil {
			return fmt.Errorf("wrong config file format: %w", unmarshalErr)
		}
		slog.Debug("Read config", "config", config)

		lis, lisErr := net.Listen("tcp", fmt.Sprintf(":%d", config.Port))
		if lisErr != nil {
			return fmt.Errorf("failed to listen: %w", lisErr)
		}

		s := grpc.NewServer()

		servicesv1.RegisterEventServiceServer(s, protos.NewEventServer())

		eventPublisher, eventPublisherErr := events.NewEventPublisher(context.Background(), lis.Addr().String())
		if eventPublisherErr != nil {
			return fmt.Errorf("failed to create event publisher: %w", eventPublisherErr)
		}

		var localImages images.ImageClient
		if config.FileRegistryEndpoint != "" {
			fileRegistryConn, frErr := grpc.NewClient(config.FileRegistryEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if frErr != nil {
				return fmt.Errorf("failed to connect to file registry: %w", frErr)
			}
			var imgErr error
			localImages, imgErr = images.CreateImageClient(servicesv1.NewFileRegistryServiceClient(fileRegistryConn))
			if imgErr != nil {
				return fmt.Errorf("failed to create image client: %w", imgErr)
			}
		}

		contr, contrErr := protos.NewController(config, eventPublisher, localImages)
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
