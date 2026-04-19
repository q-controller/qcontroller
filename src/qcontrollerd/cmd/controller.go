package cmd

import (
	"fmt"
	"log/slog"
	"net"

	controllerv1 "github.com/q-controller/qcontroller/src/generated/services/controller/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qcontroller/src/pkg/events"
	"github.com/q-controller/qcontroller/src/pkg/grpcutil"
	"github.com/q-controller/qcontroller/src/pkg/protos"
	"github.com/q-controller/qcontroller/src/pkg/utils"
	"github.com/spf13/cobra"
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

		s, sErr := grpcutil.NewServer(grpcutil.WithTLS(config.Tls))
		if sErr != nil {
			return fmt.Errorf("failed to create grpc server: %w", sErr)
		}

		eventPublisher, eventPublisherErr := events.NewEventPublisher(cmd.Context(), config.EventsEndpoint, config.EventsTls)
		if eventPublisherErr != nil {
			return fmt.Errorf("failed to create event publisher: %w", eventPublisherErr)
		}

		contr, contrErr := protos.NewController(config, eventPublisher)
		if contrErr != nil {
			return fmt.Errorf("failed to create server %w", contrErr)
		}
		controllerv1.RegisterControllerServiceServer(s, contr)

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
