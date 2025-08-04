package protos

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	servicesv1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	pkgController "github.com/q-controller/qcontroller/src/pkg/controller"
	"github.com/q-controller/qcontroller/src/pkg/controller/db"
	"github.com/q-controller/qcontroller/src/pkg/controller/vm"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

type Server struct {
	servicesv1.UnimplementedControllerServiceServer
	Config  *settingsv1.ControllerConfig
	manager *vm.Manager
}

func (s *Server) Start(ctx context.Context, request *servicesv1.StartRequest) (*emptypb.Empty, error) {
	if startErr := s.manager.Start(request.Name); startErr != nil {
		slog.Error("failed to start an instance", "error", startErr)
		return nil, status.Errorf(codes.Unknown, "failed to start a VM instance")
	}

	return &emptypb.Empty{}, nil
}

func (s *Server) Create(ctx context.Context, request *servicesv1.CreateRequest) (*emptypb.Empty, error) {
	if createErr := s.manager.Create(request.Name, request.Image,
		request.Vm.Cpus, request.Vm.Memory,
		request.Vm.Disk); createErr != nil {
		return nil, status.Errorf(codes.Internal, "method Launch failed: %v", createErr)
	}

	if request.Start {
		if startErr := s.manager.Start(request.Name); startErr != nil {
			slog.Error("failed to start an instance", "error", startErr)
			return nil, status.Errorf(codes.Unknown, "failed to start a VM instance")
		}
	}

	return &emptypb.Empty{}, nil
}

func (s *Server) Stop(ctx context.Context, request *servicesv1.StopRequest) (*emptypb.Empty, error) {
	if stopErr := s.manager.Stop(ctx, request.Name, request.Force); stopErr != nil {
		slog.Error("failed to stop an instance", "error", stopErr)
		return nil, status.Errorf(codes.Unknown, "failed to stop a VM instance")
	}

	return &emptypb.Empty{}, nil
}

func (s *Server) Remove(ctx context.Context, req *servicesv1.RemoveRequest) (*emptypb.Empty, error) {
	if removeErr := s.manager.Remove(ctx, req.Name); removeErr != nil {
		slog.Error("failed to remove an instance", "error", removeErr)
		return nil, status.Errorf(codes.Unknown, "failed to remove a VM instance")
	}

	return &emptypb.Empty{}, nil
}

func (s *Server) Info(ctx context.Context, request *servicesv1.InfoRequest) (*servicesv1.InfoResponse, error) {
	info, infoErr := s.manager.Info(request.Name)
	if infoErr != nil {
		return nil, status.Errorf(codes.Internal, "failed to retrieve info")
	}

	return &servicesv1.InfoResponse{
		Info: info,
	}, nil
}

func NewController(settings *settingsv1.ControllerConfig, fetcher pkgController.Fetcher) (servicesv1.ControllerServiceServer, error) {
	if mkdirErr := os.MkdirAll(filepath.Join(settings.Root, "db"), 0755); mkdirErr != nil {
		return nil, mkdirErr
	}

	state, stateErr := db.NewDatabase(filepath.Join(settings.Root, "db", "qcontroller.db"))
	if stateErr != nil {
		return nil, stateErr
	}

	manager := vm.CreateManager(settings.Root, settings.QemuEndpoint, fetcher, state)
	if manager == nil {
		return nil, fmt.Errorf("failed to create a manager")
	}

	server := &Server{
		Config:  settings,
		manager: manager,
	}

	return server, nil
}
