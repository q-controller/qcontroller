package vm

import (
	"context"

	servicesv1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	vmv1 "github.com/q-controller/qcontroller/src/generated/vm/statemachine/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// remoteNodeManager implements NodeManager by delegating to a remote ControllerService.
type remoteNodeManager struct {
	name     string
	endpoint string
}

func newRemoteNodeManager(name, endpoint string) NodeManager {
	return &remoteNodeManager{
		name:     name,
		endpoint: endpoint,
	}
}

func (n *remoteNodeManager) Endpoint() string {
	return n.endpoint
}

func (n *remoteNodeManager) dial() (*grpc.ClientConn, error) {
	return grpc.NewClient(n.endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
}

func (n *remoteNodeManager) Create(ctx context.Context, id, imageId string,
	cpus, memory, disk uint32, cloudInit *vmv1.CloudInit) error {
	conn, err := n.dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = servicesv1.NewControllerServiceClient(conn).Create(ctx, &servicesv1.CreateRequest{
		Name:      id,
		Image:     imageId,
		Vm:        &settingsv1.VM{Cpus: cpus, Memory: memory, Disk: disk},
		CloudInit: cloudInit,
	})
	return err
}

func (n *remoteNodeManager) Start(ctx context.Context, name string) error {
	conn, err := n.dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = servicesv1.NewControllerServiceClient(conn).Start(ctx, &servicesv1.StartRequest{Name: name})
	return err
}

func (n *remoteNodeManager) Stop(ctx context.Context, name string, force bool) error {
	conn, err := n.dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = servicesv1.NewControllerServiceClient(conn).Stop(ctx, &servicesv1.StopRequest{Name: name, Force: force})
	return err
}

func (n *remoteNodeManager) Remove(ctx context.Context, name string) error {
	conn, err := n.dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = servicesv1.NewControllerServiceClient(conn).Remove(ctx, &servicesv1.RemoveRequest{Name: name})
	return err
}

func (n *remoteNodeManager) Info(ctx context.Context, name string) ([]*servicesv1.Info, error) {
	conn, err := n.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	resp, err := servicesv1.NewControllerServiceClient(conn).Info(ctx, &servicesv1.InfoRequest{Name: name})
	if err != nil {
		return nil, err
	}
	return resp.Info, nil
}
