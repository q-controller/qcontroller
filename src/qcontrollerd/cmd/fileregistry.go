package cmd

import (
	"fmt"
	"log/slog"
	"net"
	"path/filepath"

	servicesv1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qcontroller/src/pkg/protos"
	"github.com/q-controller/qcontroller/src/pkg/utils"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
)

var fileRegistryCmd = &cobra.Command{
	Use:   "fileregistry",
	Short: "Starts the file registry service",
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath, configPathErr := cmd.Flags().GetString("config")
		if configPathErr != nil {
			return fmt.Errorf("failed to get config: %w", configPathErr)
		}

		config := &settingsv1.FileRegistryConfig{}
		if unmarshalErr := utils.Unmarshal(config, configPath); unmarshalErr != nil {
			return fmt.Errorf("wrong config file format: %w", unmarshalErr)
		}
		slog.Debug("Read config", "config", config)

		if config.Cache == nil {
			return fmt.Errorf("failed to read config: image cache is not set")
		}

		lis, lisErr := net.Listen("tcp", fmt.Sprintf(":%d", config.Port))
		if lisErr != nil {
			return fmt.Errorf("failed to listen: %w", lisErr)
		}

		s := grpc.NewServer()

		reg, regErr := protos.NewFileRegistry(
			filepath.Join(config.Root, config.Cache.Root),
			config.EventsEndpoint,
			config.UpstreamEndpoint,
		)
		if regErr != nil {
			return fmt.Errorf("failed to create file registry: %w", regErr)
		}
		servicesv1.RegisterFileRegistryServiceServer(s, reg)

		if servErr := s.Serve(lis); servErr != nil {
			return fmt.Errorf("failed to serve: %w", servErr)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(fileRegistryCmd)

	fileRegistryCmd.Flags().StringP("config", "c", "", "Path to file registry's config file")
	if err := fileRegistryCmd.MarkFlagRequired("config"); err != nil {
		panic(fmt.Errorf("failed to mark flag `config` as required: %w", err))
	}
}
