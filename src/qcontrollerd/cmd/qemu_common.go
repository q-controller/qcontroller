package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	servicesv1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qcontroller/src/pkg/images"
	"github.com/q-controller/qcontroller/src/pkg/protos"
	"github.com/q-controller/qcontroller/src/pkg/qemu/process"
	"github.com/q-controller/qcontroller/src/pkg/utils/network/ip"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func Entrypoint(config *settingsv1.QemuConfig, addressResolver ip.AddressResolver, stop <-chan struct{}) error {
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
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 1*time.Second)
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

	fileRegistryConn, fileRegistryConnErr := grpc.NewClient(config.FileRegistryEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if fileRegistryConnErr != nil {
		return fmt.Errorf("failed to connect to file registry: %w", fileRegistryConnErr)
	}
	defer fileRegistryConn.Close()

	fileRegistryClient := servicesv1.NewFileRegistryServiceClient(fileRegistryConn)
	imageClient, imageClientErr := images.CreateImageClient(fileRegistryClient)
	if imageClientErr != nil {
		return fmt.Errorf("failed to create image client: %w", imageClientErr)
	}

	reg, regErr := protos.NewQemuService(monitor, addressResolver, config, imageClient)
	if regErr != nil {
		return fmt.Errorf("failed to create server %w", regErr)
	}
	servicesv1.RegisterQemuServiceServer(s, reg)

	// Server error channel
	errCh := make(chan error, 1)

	// Start server in background
	go func() {
		if err := s.Serve(lis); err != nil {
			errCh <- err
		} else {
			errCh <- nil
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
}
