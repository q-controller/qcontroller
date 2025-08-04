package protos

import (
	"context"

	servicesv1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qcontroller/src/pkg/utils/network"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

type NetworkManagerServer struct {
	servicesv1.UnimplementedNetworkManagerServiceServer

	nm network.NetworkManager
}

func (s *NetworkManagerServer) CreateInterface(ctx context.Context,
	req *servicesv1.CreateInterfaceRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, s.nm.CreateInterface(req.Name)
}

func (s *NetworkManagerServer) RemoveInterface(ctx context.Context,
	req *servicesv1.RemoveInterfaceRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, s.nm.RemoveInterface(req.Name)
}

func NewNetworkManager(bridge *settingsv1.Bridge) (servicesv1.NetworkManagerServiceServer, error) {
	nm, nmErr := network.NewNetworkManager(bridge.Name, bridge.Subnet)
	if nmErr != nil {
		return nil, nmErr
	}

	server := &NetworkManagerServer{
		nm: nm,
	}

	return server, nil
}
