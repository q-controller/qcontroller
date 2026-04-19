package cmd

import (
	"fmt"
	"log/slog"
	"net"

	eventv1 "github.com/q-controller/qcontroller/src/generated/services/event/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qcontroller/src/pkg/protos"
	"github.com/q-controller/qcontroller/src/pkg/utils"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
)

var eventServiceCmd = &cobra.Command{
	Use:   "eventservice",
	Short: "Starts the event service (pub/sub hub for VM and image events)",
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath, configPathErr := cmd.Flags().GetString("config")
		if configPathErr != nil {
			return fmt.Errorf("failed to get config: %w", configPathErr)
		}

		config := &settingsv1.EventServiceConfig{}
		if unmarshalErr := utils.Unmarshal(config, configPath); unmarshalErr != nil {
			return fmt.Errorf("wrong config file format: %w", unmarshalErr)
		}
		slog.Debug("Read config", "config", config)

		lis, lisErr := net.Listen("tcp", fmt.Sprintf(":%d", config.Port))
		if lisErr != nil {
			return fmt.Errorf("failed to listen: %w", lisErr)
		}

		s := grpc.NewServer()
		eventv1.RegisterEventServiceServer(s, protos.NewEventServer())

		slog.Info("Event service listening", "port", config.Port)
		if servErr := s.Serve(lis); servErr != nil {
			return fmt.Errorf("failed to serve: %w", servErr)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(eventServiceCmd)

	eventServiceCmd.Flags().StringP("config", "c", "", "Path to event service's config file")
	if err := eventServiceCmd.MarkFlagRequired("config"); err != nil {
		panic(fmt.Errorf("failed to mark flag `config` as required: %w", err))
	}
}
