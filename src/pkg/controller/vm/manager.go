package vm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	servicesv1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	vmv1 "github.com/q-controller/qcontroller/src/generated/vm/statemachine/v1"
	"github.com/q-controller/qcontroller/src/pkg/controller"
	"github.com/q-controller/qcontroller/src/pkg/controller/db"
	"github.com/q-controller/qcontroller/src/pkg/events"
	"github.com/q-controller/qemu-client/pkg/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Manager holds a collection of VM instances.
type Manager struct {
	state    controller.State
	mutex    sync.RWMutex
	qemuConn *grpc.ClientConn
	qemuCh   chan *servicesv1.Event

	eventsPublisher *events.Publisher
}

// newManager creates a new VM provisioner.
func newManager(qemuEndpoint string, state controller.State, eventPublisher *events.Publisher) (*Manager, error) {
	vms, vmsErr := state.List()
	if vmsErr != nil {
		return nil, vmsErr
	}

	conn, connErr := grpc.NewClient(qemuEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if connErr != nil {
		return nil, fmt.Errorf("failed to connect to QEMU service at %s: %w", qemuEndpoint, connErr)
	}

	manager := Manager{
		state:           state,
		qemuConn:        conn,
		qemuCh:          make(chan *servicesv1.Event),
		eventsPublisher: eventPublisher,
	}

	// Reset all VMs to STOPPED on startup — QEMU service handles reattach.
	for _, inst := range vms {
		inst.State = vmv1.State_STATE_STOPPED
		if _, updateErr := state.Update(inst); updateErr != nil {
			return nil, updateErr
		}
	}

	manager.eventLoop()

	// Reconcile with QEMU service: discover running instances and start status streams.
	client := servicesv1.NewQemuServiceClient(conn)
	listResp, listErr := client.List(context.Background(), &servicesv1.QemuServiceListRequest{})
	if listErr != nil {
		slog.Warn("Failed to query QEMU service for running instances", "error", listErr)
	} else {
		for _, id := range listResp.Ids {
			if _, getErr := state.Get(id); getErr != nil {
				continue
			}
			if streamErr := manager.startStatusStream(id); streamErr != nil {
				slog.Warn("Failed to start status stream for instance", "id", id, "error", streamErr)
			}
		}
	}

	return &manager, nil
}

// Create creates a new VM instance.
func (m *Manager) Create(id, imageId string,
	cpus uint32, memory, disk uint32, cloudInit *vmv1.CloudInit) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if _, instanceErr := m.state.Get(id); instanceErr == nil {
		return fmt.Errorf("instance %s already exists", id)
	}

	hwaddr, hwaddrErr := utils.GenerateRandomMAC()
	if hwaddrErr != nil {
		return hwaddrErr
	}

	_, instanceErr := m.state.Update(&vmv1.Instance{
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
	})
	if instanceErr != nil {
		return instanceErr
	}

	return nil
}

func (m *Manager) Stop(ctx context.Context, id string, force bool) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if _, err := m.state.Get(id); err != nil {
		return fmt.Errorf("instance %s not found: %w", id, err)
	}

	if _, stopErr := servicesv1.NewQemuServiceClient(m.qemuConn).Stop(context.Background(), &servicesv1.QemuServiceStopRequest{
		Id:    id,
		Force: force,
	}); stopErr != nil {
		return stopErr
	}

	return nil
}

func (m *Manager) Remove(ctx context.Context, id string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if err := m.state.Remove(id); err != nil {
		if errors.Is(err, db.ErrNoInstanceRemoved) {
			return nil
		}
		return err
	}

	if eventErr := m.eventsPublisher.VMRemoved(id); eventErr != nil {
		slog.Warn("Failed to publish VM removal event", "id", id, "error", eventErr)
	}

	// Clean up instance directory on QEMU side
	if _, removeErr := servicesv1.NewQemuServiceClient(m.qemuConn).Remove(context.Background(), &servicesv1.QemuServiceRemoveRequest{
		Id: id,
	}); removeErr != nil {
		slog.Warn("Failed to remove instance from QEMU", "id", id, "error", removeErr)
	}

	return nil
}

func (m *Manager) Start(id string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if instance, instanceErr := m.state.Get(id); instanceErr == nil && instance.State == vmv1.State_STATE_STOPPED {
		return m.startImpl(instance)
	}

	return fmt.Errorf("instance %s does not exist or not Stopped", id)
}

func (m *Manager) Info(id string) ([]*servicesv1.Info, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	vms := make([]*vmv1.Instance, 0)

	if id == "" {
		listedVms, vmsErr := m.state.List()
		if vmsErr != nil {
			return nil, vmsErr
		}
		vms = listedVms
	} else {
		vm, vmErr := m.state.Get(id)
		if vmErr != nil {
			return nil, vmErr
		}
		vms = append(vms, vm)
	}

	res := []*servicesv1.Info{}
	for _, inst := range vms {
		info := &servicesv1.Info{
			Name:    inst.Id,
			State:   inst.State.String(),
			Details: inst.Hardware,
		}
		if inst.Cloudinit != nil {
			info.CloudInit = inst.Cloudinit
		}
		if inst.Hwaddr != nil {
			info.Hwaddr = *inst.Hwaddr
		}
		if resp, infoErr := servicesv1.NewQemuServiceClient(m.qemuConn).Info(context.Background(), &servicesv1.QemuServiceInfoRequest{
			Ids: []string{inst.Id},
		}); infoErr == nil {
			for _, data := range resp.Info {
				info.RuntimeInfo = data
			}
		}
		res = append(res, info)
	}

	return res, nil
}

// singleton and once implement a thread-safe singleton pattern for Manager.
var singleton *Manager
var once sync.Once

func CreateManager(qemuEndpoint string, state controller.State, eventPublisher *events.Publisher) *Manager {
	once.Do(func() {
		mgr, mgrErr := newManager(qemuEndpoint, state, eventPublisher)
		if mgrErr != nil {
			slog.Error("failed to create VM manager", "error", mgrErr)
		}
		singleton = mgr
	})
	return singleton
}

func (m *Manager) eventLoop() {
	go func() {
		for event := range m.qemuCh {
			if inst, instErr := m.state.Get(event.Id); instErr == nil {
				switch data := event.EventKind.(type) {
				case *servicesv1.Event_Status:
					newState := vmv1.State_STATE_RUNNING
					if !data.Status.Running {
						newState = vmv1.State_STATE_STOPPED
					}
					prevState := inst.State
					if prevState != newState {
						inst.State = newState
						if _, instanceErr := m.state.Update(inst); instanceErr != nil {
							slog.Error("Failed to update state", "instance", event.Id, "error", instanceErr)
						}
						if eventErr := m.eventsPublisher.VMUpdated(
							&servicesv1.Info{
								Name:  inst.Id,
								State: inst.State.String(),
							},
						); eventErr != nil {
							slog.Warn("Failed to publish VM update event", "id", inst.Id, "error", eventErr)
						}
					}
				case *servicesv1.Event_Info:
					info := &servicesv1.Info{
						Name:        inst.Id,
						State:       inst.State.String(),
						Details:     inst.Hardware,
						RuntimeInfo: data.Info,
					}
					if inst.Cloudinit != nil {
						info.CloudInit = inst.Cloudinit
					}
					if inst.Hwaddr != nil {
						info.Hwaddr = *inst.Hwaddr
					}
					if eventErr := m.eventsPublisher.VMUpdated(info); eventErr != nil {
						slog.Warn("Failed to publish VM info event", "id", inst.Id, "error", eventErr)
					}
				}
			}
		}
		close(m.qemuCh)
	}()
}

func (m *Manager) startStatusStream(id string) error {
	client := servicesv1.NewQemuServiceClient(m.qemuConn)
	statusResp, statusErr := client.Status(context.Background(), &servicesv1.QemuServiceStatusRequest{
		Id: id,
	})
	if statusErr != nil {
		return statusErr
	}

	go func() {
		slog.Info("Starting status loop goroutine", "instance", id)

	STATUS_LOOP:
		for {
			resp, err := statusResp.Recv()
			if err == nil {
				slog.Debug("Received status event", "instance", id, "event_type", fmt.Sprintf("%T", resp.Event.EventKind))
				m.qemuCh <- resp.Event
				switch data := resp.Event.EventKind.(type) {
				case *servicesv1.Event_Status:
					if !data.Status.Running {
						break STATUS_LOOP
					}
				}
			} else {
				m.qemuCh <- &servicesv1.Event{
					Id: id,
					EventKind: &servicesv1.Event_Status{
						Status: &servicesv1.Status{
							Running: false,
						},
					},
				}
				break STATUS_LOOP
			}
		}
	}()

	return nil
}

func (m *Manager) startImpl(instance *vmv1.Instance) error {
	cloudInit := instance.Cloudinit
	if cloudInit == nil || cloudInit.NetworkConfig == "" {
		if instance.Hwaddr == nil {
			return fmt.Errorf("cannot generate cloud-init network config: instance %s has no MAC address", instance.Id)
		}
		if cloudInit == nil {
			cloudInit = &vmv1.CloudInit{}
		}
		cloudInit.NetworkConfig = fmt.Sprintf(
			"version: 2\nethernets:\n  id0:\n    match:\n      macaddress: %s\n    dhcp4: true\n    dhcp-identifier: mac\n",
			*instance.Hwaddr,
		)
	}

	client := servicesv1.NewQemuServiceClient(m.qemuConn)
	if _, startErr := client.Start(context.Background(), &servicesv1.QemuServiceStartRequest{
		Config: &servicesv1.QemuConfig{
			Id:      instance.Id,
			ImageId: instance.ImageId,
			Hardware: &settingsv1.VM{
				Cpus:   instance.Hardware.Cpus,
				Memory: instance.Hardware.Memory,
				Disk:   instance.Hardware.Disk,
			},
			Network: &servicesv1.NetworkConfig{
				Mac: *instance.Hwaddr,
			},
			CloudInit: cloudInit,
		},
	}); startErr != nil {
		return startErr
	}

	return m.startStatusStream(instance.Id)
}
