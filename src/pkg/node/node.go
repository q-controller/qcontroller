package node

import (
	"context"

	controllerv1 "github.com/q-controller/qcontroller/src/generated/services/controller/v1"
	vmv1 "github.com/q-controller/qcontroller/src/generated/vm/statemachine/v1"
)

// Manager handles VM operations on a single node.
type Manager interface {
	Endpoint() string
	Create(ctx context.Context, id, imageId string, cpus, memory, disk uint32, cloudInit *vmv1.CloudInit) error
	Start(ctx context.Context, name string) error
	Stop(ctx context.Context, name string, force bool) error
	Remove(ctx context.Context, name string) error
	Info(ctx context.Context, name string) ([]*controllerv1.Info, error)
}
