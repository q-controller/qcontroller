package vm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	servicesv1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	runtimev1 "github.com/q-controller/qcontroller/src/generated/vm/runtime/v1"
	vmv1 "github.com/q-controller/qcontroller/src/generated/vm/statemachine/v1"
	"github.com/q-controller/qcontroller/src/pkg/controller"
	"github.com/q-controller/qcontroller/src/pkg/controller/db"
	"github.com/q-controller/qemu-client/pkg/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// localNodeManager implements NodeManager for the local node.
// It wraps a QemuService gRPC client for VM operations and a local DB for state persistence.
type localNodeManager struct {
	name     string
	endpoint string
	state    controller.State
}

func newLocalNodeManager(name, endpoint string, state controller.State) (NodeManager, error) {
	nm := &localNodeManager{name: name, endpoint: endpoint, state: state}
	return nm, nil
}

func (n *localNodeManager) Endpoint() string {
	return n.endpoint
}

func (n *localNodeManager) dial() (*grpc.ClientConn, error) {
	return grpc.NewClient(n.endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
}

func (n *localNodeManager) qemuList(ctx context.Context) ([]string, error) {
	conn, err := n.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	resp, err := servicesv1.NewQemuServiceClient(conn).List(ctx, &servicesv1.QemuServiceListRequest{})
	if err != nil {
		return nil, err
	}
	return resp.Ids, nil
}

func (n *localNodeManager) Create(ctx context.Context, id, imageId string,
	cpus, memory, disk uint32, cloudInit *vmv1.CloudInit) error {

	if _, err := n.state.Get(id); err == nil {
		return fmt.Errorf("instance %s already exists", id)
	}

	hwaddr, hwaddrErr := utils.GenerateRandomMAC()
	if hwaddrErr != nil {
		return hwaddrErr
	}

	_, err := n.state.Update(&vmv1.Instance{
		Hardware: &settingsv1.VM{
			Cpus:   cpus,
			Memory: memory,
			Disk:   disk,
		},
		ImageId:   imageId,
		Id:        id,
		Hwaddr:    &hwaddr,
		State:     vmv1.State_STATE_STOPPED,
		Cloudinit: cloudInit,
		Node:      n.name,
	})
	return err
}

func (n *localNodeManager) Start(ctx context.Context, name string) error {
	inst, err := n.state.Get(name)
	if err != nil {
		return fmt.Errorf("instance %s not found: %w", name, err)
	}
	if inst.State != vmv1.State_STATE_STOPPED {
		return fmt.Errorf("instance %s is not stopped", name)
	}

	cloudInit := inst.Cloudinit
	if cloudInit == nil || cloudInit.NetworkConfig == "" {
		if inst.Hwaddr == nil {
			return fmt.Errorf("cannot generate cloud-init network config: instance %s has no MAC address", name)
		}
		if cloudInit == nil {
			cloudInit = &vmv1.CloudInit{}
		}
		cloudInit.NetworkConfig = fmt.Sprintf(
			"version: 2\nethernets:\n  id0:\n    match:\n      macaddress: %s\n    dhcp4: true\n    dhcp-identifier: mac\n",
			*inst.Hwaddr,
		)
	}

	n.setInstanceState(inst.Id, vmv1.State_STATE_STARTING)

	conn, dialErr := n.dial()
	if dialErr != nil {
		n.setInstanceState(inst.Id, vmv1.State_STATE_STOPPED)
		return dialErr
	}
	defer conn.Close()

	if _, startErr := servicesv1.NewQemuServiceClient(conn).Start(ctx, &servicesv1.QemuServiceStartRequest{
		Config: &servicesv1.QemuConfig{
			Id:      inst.Id,
			ImageId: inst.ImageId,
			Hardware: &settingsv1.VM{
				Cpus:   inst.Hardware.Cpus,
				Memory: inst.Hardware.Memory,
				Disk:   inst.Hardware.Disk,
			},
			Network: &servicesv1.NetworkConfig{
				Mac: *inst.Hwaddr,
			},
			CloudInit: cloudInit,
		},
	}); startErr != nil {
		n.setInstanceState(inst.Id, vmv1.State_STATE_STOPPED)
		return startErr
	}

	n.setInstanceState(inst.Id, vmv1.State_STATE_RUNNING)
	return nil
}

func (n *localNodeManager) Stop(ctx context.Context, name string, force bool) error {
	conn, err := n.dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = servicesv1.NewQemuServiceClient(conn).Stop(ctx, &servicesv1.QemuServiceStopRequest{Id: name, Force: force})
	return err
}

func (n *localNodeManager) Remove(ctx context.Context, name string) error {
	inst, getErr := n.state.Get(name)
	if getErr != nil {
		return nil // already gone
	}

	if inst.State != vmv1.State_STATE_STOPPED {
		return fmt.Errorf("instance %s is not stopped", name)
	}

	if err := n.state.Remove(name); err != nil {
		if errors.Is(err, db.ErrNoInstanceRemoved) {
			return nil
		}
		return err
	}

	// Clean up instance directory on QEMU side
	conn, dialErr := n.dial()
	if dialErr != nil {
		slog.Warn("Failed to connect to QEMU for cleanup", "id", name, "error", dialErr)
		return nil
	}
	defer conn.Close()
	if _, removeErr := servicesv1.NewQemuServiceClient(conn).Remove(ctx, &servicesv1.QemuServiceRemoveRequest{Id: name}); removeErr != nil {
		slog.Warn("Failed to remove instance from QEMU", "id", name, "error", removeErr)
	}

	return nil
}

func (n *localNodeManager) Info(ctx context.Context, name string) ([]*servicesv1.Info, error) {
	var instances []*vmv1.Instance
	if name == "" {
		listed, err := n.state.List()
		if err != nil {
			return nil, err
		}
		instances = listed

		// Reconcile state with QEMU
		n.reconcileInstances(ctx, instances)
	} else {
		inst, err := n.state.Get(name)
		if err != nil {
			return nil, err
		}
		instances = append(instances, inst)
	}

	// Collect running instance IDs for a single batched runtime query.
	var runningIDs []string
	res := make([]*servicesv1.Info, 0, len(instances))
	for _, inst := range instances {
		info := n.instanceToInfo(inst)
		if inst.State == vmv1.State_STATE_RUNNING {
			runningIDs = append(runningIDs, inst.Id)
		}
		res = append(res, info)
	}

	if len(runningIDs) > 0 {
		n.batchEnrichWithRuntime(ctx, res, runningIDs)
	}
	return res, nil
}

func (n *localNodeManager) instanceToInfo(inst *vmv1.Instance) *servicesv1.Info {
	info := &servicesv1.Info{
		Name:    inst.Id,
		State:   inst.State.String(),
		Details: inst.Hardware,
		Node:    inst.Node,
		ImageId: inst.ImageId,
	}
	if inst.Cloudinit != nil {
		info.CloudInit = inst.Cloudinit
	}
	if inst.Hwaddr != nil {
		info.Hwaddr = *inst.Hwaddr
	}
	return info
}

func (n *localNodeManager) batchEnrichWithRuntime(ctx context.Context, infos []*servicesv1.Info, runningIDs []string) {
	conn, err := n.dial()
	if err != nil {
		return
	}
	defer conn.Close()
	resp, err := servicesv1.NewQemuServiceClient(conn).Info(ctx, &servicesv1.QemuServiceInfoRequest{Ids: runningIDs})
	if err != nil {
		return
	}

	// Index runtime info by name for fast lookup.
	runtimeByName := make(map[string]*runtimev1.RuntimeInfo, len(resp.Info))
	for _, ri := range resp.Info {
		runtimeByName[ri.Name] = ri
	}
	for _, info := range infos {
		if ri, ok := runtimeByName[info.Name]; ok {
			info.RuntimeInfo = ri
		}
	}
}

func (n *localNodeManager) setInstanceState(id string, state vmv1.State) {
	inst, getErr := n.state.Get(id)
	if getErr != nil {
		return
	}
	if inst.State == state {
		return
	}
	inst.State = state
	if _, updateErr := n.state.Update(inst); updateErr != nil {
		slog.Error("Failed to update instance state", "id", id, "state", state, "error", updateErr)
	}
}

// reconcileInstances updates DB state to match QEMU reality during Info("") calls.
func (n *localNodeManager) reconcileInstances(ctx context.Context, instances []*vmv1.Instance) {
	ids, err := n.qemuList(ctx)
	if err != nil {
		return
	}
	running := make(map[string]bool, len(ids))
	for _, id := range ids {
		running[id] = true
	}
	for _, inst := range instances {
		if running[inst.Id] && inst.State != vmv1.State_STATE_RUNNING {
			inst.State = vmv1.State_STATE_RUNNING
			n.setInstanceState(inst.Id, vmv1.State_STATE_RUNNING)
		} else if !running[inst.Id] && inst.State == vmv1.State_STATE_RUNNING {
			inst.State = vmv1.State_STATE_STOPPED
			n.setInstanceState(inst.Id, vmv1.State_STATE_STOPPED)
		}
	}
}
