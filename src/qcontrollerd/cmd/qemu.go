package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	servicesv1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qcontroller/src/pkg/protos"
	"github.com/q-controller/qcontroller/src/pkg/qemu/process"
	"github.com/q-controller/qcontroller/src/pkg/utils"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
)

var qemuCmd = &cobra.Command{
	Use:   "qemu",
	Short: "Starts QEMU client",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if os.Geteuid() != 0 {
			return errors.New("this command must be run as root")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath, configPathErr := cmd.Flags().GetString("config")
		if configPathErr != nil {
			return fmt.Errorf("failed to get config: %w", configPathErr)
		}

		config := &settingsv1.QemuConfig{}
		if unmarshalErr := utils.Unmarshal(config, configPath); unmarshalErr != nil {
			return unmarshalErr
		}
		slog.Debug("Read config", "config", config)

		lis, lisErr := net.Listen("tcp", fmt.Sprintf(":%d", config.Port))
		if lisErr != nil {
			return fmt.Errorf("failed to listen: %w", lisErr)
		}
		defer func() {
			if err := lis.Close(); err != nil {
				slog.Error("Failed to close the listener", "error", err)
			}
		}()

		s := grpc.NewServer()
		defer func() {
			// Create a deadline for graceful shutdown
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer shutdownCancel()

			// Give ongoing operations a chance to complete
			done := make(chan struct{})
			go func() {
				s.GracefulStop() // Stop accepting new requests, let existing ones finish
				close(done)
			}()

			// Wait for graceful shutdown or timeout
			select {
			case <-shutdownCtx.Done():
				slog.Warn("Graceful shutdown timed out, forcing stop")
				s.Stop()
			case <-done:
				slog.Info("Graceful shutdown completed")
			}
		}()

		monitor, monitorErr := process.NewInstanceMonitor()
		if monitorErr != nil {
			return monitorErr
		}
		defer func() {
			if err := monitor.Close(); err != nil {
				slog.Error("Error closing monitor", "error", err)
			}
		}()

		reg, regErr := protos.NewQemuService(monitor, config)
		if regErr != nil {
			return fmt.Errorf("failed to create server %w", regErr)
		}
		servicesv1.RegisterQemuServiceServer(s, reg)

		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)

		// Server error channel
		errCh := make(chan error, 1)

		// Start server in background
		go func() {
			if err := s.Serve(lis); err != nil {
				errCh <- err
			}
		}()

		// Wait for signal or error
		select {
		case <-stop:
			slog.Info("Shutting down gRPC server due to signal...")
		case err := <-errCh:
			return err
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(qemuCmd)

	qemuCmd.Flags().StringP("config", "c", "", "Path to qemu's config file")
	if err := qemuCmd.MarkFlagRequired("config"); err != nil {
		panic(fmt.Errorf("failed to mark flag `config` as required: %w", err))
	}
}
