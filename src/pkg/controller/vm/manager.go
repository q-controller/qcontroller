package vm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	servicesv1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	vmv1 "github.com/q-controller/qcontroller/src/generated/vm/statemachine/v1"
	"github.com/q-controller/qcontroller/src/pkg/controller"
	"github.com/q-controller/qcontroller/src/pkg/controller/db"
	"github.com/q-controller/qcontroller/src/pkg/events"
	"github.com/q-controller/qcontroller/src/pkg/images"
	localUtils "github.com/q-controller/qcontroller/src/pkg/utils"
	"github.com/q-controller/qemu-client/pkg/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	cINSTANCES = "instances"
	cERRLOG    = "log.err"
	cOUTLOG    = "log.out"
)

// Manager holds a collection of VM instances.
type Manager struct {
	rootDir      string
	instancesDir string
	state        controller.State
	mutex        sync.RWMutex

	qemuConn *grpc.ClientConn
	qemuCh   chan *servicesv1.Event

	imageClient     images.ImageClient
	eventsPublisher *events.Publisher
}

// NewManager creates a new VM provisioner.
func newManager(rootDir string, qemuEndpoint string, state controller.State, imageClient images.ImageClient, eventPublisher *events.Publisher) (*Manager, error) {
	if _, statErr := os.Stat(rootDir); statErr != nil {
		return nil, statErr
	}

	instancesDir := filepath.Join(rootDir, cINSTANCES)
	if err := os.MkdirAll(instancesDir, 0777); err != nil {
		return nil, err
	}

	vms, vmsErr := state.List()
	if vmsErr != nil {
		return nil, vmsErr
	}

	conn, err := grpc.NewClient(qemuEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("did not connect: %w", err)
	}

	manager := Manager{
		state:           state,
		rootDir:         rootDir,
		instancesDir:    instancesDir,
		qemuCh:          make(chan *servicesv1.Event),
		qemuConn:        conn,
		imageClient:     imageClient,
		eventsPublisher: eventPublisher,
	}

	// This can occur only during startup. Ensure
	// the state is adjusted to allow VM restart.
	for _, inst := range vms {
		inst.State = vmv1.State_STATE_STOPPED

		if _, updateErr := state.Update(inst); updateErr != nil {
			return nil, updateErr
		}
	}

	manager.eventLoop()

	for _, inst := range vms {
		if inst.Pid != nil {
			if err := manager.Start(inst.Id); err != nil {
				slog.Warn("Failed to reattach to qemu instance", "error", err)
			}
		}
	}

	return &manager, nil
}

// NewVMInstance creates a new VM instance with a state machine.
func (m *Manager) Create(id, imageId string,
	cpus uint32, memory, disk uint32) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if _, instanceErr := m.state.Get(id); instanceErr == nil {
		return fmt.Errorf("instance %s already exists", id)
	}

	imageUrl := filepath.Join(m.instanceDir(id), "image")
	if downloadErr := m.imageClient.Download(context.Background(), imageId, imageUrl); downloadErr != nil {
		return downloadErr
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
		Path:   imageUrl,
		Id:     id,
		Hwaddr: &hwaddr,
		State:  vmv1.State_STATE_STOPPED,
	})
	if instanceErr != nil {
		return instanceErr
	}

	return nil
}

func (m *Manager) Stop(ctx context.Context, id string, force bool) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

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

	return os.RemoveAll(filepath.Join(m.instancesDir, id))
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
		if resp, infoErr := servicesv1.NewQemuServiceClient(m.qemuConn).Info(context.Background(), &servicesv1.QemuServiceInfoRequest{
			Ids: []string{inst.Id},
		}); infoErr == nil {
			for _, data := range resp.Info {
				info.Ipaddresses = data.Ipaddresses
			}
		}
		res = append(res, info)
	}

	return res, nil
}

// singleton and once implement a thread-safe singleton pattern for Manager.
// This ensures only one instance of Manager is created and shared across the application.
var singleton *Manager
var once sync.Once

func CreateManager(rootDir string, qemuEndpoint string, state controller.State, imageClient images.ImageClient, eventPublisher *events.Publisher) *Manager {
	once.Do(func() {
		mgr, mgrErr := newManager(rootDir, qemuEndpoint, state, imageClient, eventPublisher)
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
					if !data.Status.Running {
						inst.Pid = nil
						if _, instanceErr := m.state.Update(inst); instanceErr != nil {
							slog.Error("Failed to update state", "instance", event.Id, "error", instanceErr)
						}
					}
				case *servicesv1.Event_Pid:
					inst.Pid = &data.Pid.Id
					if _, instanceErr := m.state.Update(inst); instanceErr != nil {
						slog.Error("Failed to update state", "instance", event.Id, "error", instanceErr)
					}
				case *servicesv1.Event_Info:
					if eventErr := m.eventsPublisher.VMUpdated(
						&servicesv1.Info{
							Name:        inst.Id,
							State:       inst.State.String(),
							Ipaddresses: data.Info.Ipaddresses,
						},
					); eventErr != nil {
						slog.Warn("Failed to publish VM info event", "id", inst.Id, "error", eventErr)
					}
				}
			}
		}
		close(m.qemuCh)
	}()
}

func (m *Manager) startImpl(instance *vmv1.Instance) error {
	instanceDir := m.instanceDir(instance.Id)
	if err := os.MkdirAll(instanceDir, 0777); err != nil {
		return err
	}
	ErrFilePath := filepath.Join(instanceDir, cERRLOG)
	if err := localUtils.TouchFile(ErrFilePath); err != nil {
		return fmt.Errorf("failed to touch file: %v", err)
	}
	OutFilePath := filepath.Join(instanceDir, cOUTLOG)
	if err := localUtils.TouchFile(OutFilePath); err != nil {
		return fmt.Errorf("failed to touch file: %v", err)
	}
	startResp, startErr := servicesv1.NewQemuServiceClient(m.qemuConn).Start(context.Background(), &servicesv1.QemuServiceStartRequest{
		Config: &servicesv1.QemuConfig{
			Id:    instance.Id,
			Image: instance.Path,
			Hardware: &settingsv1.VM{
				Cpus:   instance.Hardware.Cpus,
				Memory: instance.Hardware.Memory,
				Disk:   instance.Hardware.Disk,
			},
			Network: &servicesv1.NetworkConfig{
				Mac: *instance.Hwaddr,
			},
			ErrFilePath: ErrFilePath,
			OutFilePath: OutFilePath,
		},
		Pid: instance.Pid,
	})
	if startErr != nil {
		return startErr
	}

	statusResp, statusErr := servicesv1.NewQemuServiceClient(m.qemuConn).Status(context.Background(), &servicesv1.QemuServiceStatusRequest{
		Id: instance.Id,
	})
	if statusErr != nil {
		return statusErr
	}

	go func(pid int32) {
		m.qemuCh <- &servicesv1.Event{
			Id: instance.Id,
			EventKind: &servicesv1.Event_Pid{
				Pid: &servicesv1.Pid{
					Id: pid,
				},
			},
		}

	STATUS_LOOP:
		for {
			resp, err := statusResp.Recv()
			if err == nil {
				m.qemuCh <- resp.Event
				switch data := resp.Event.EventKind.(type) {
				case *servicesv1.Event_Status:
					if !data.Status.Running {
						break STATUS_LOOP
					}
				}
			} else {
				break STATUS_LOOP
			}
		}
	}(startResp.Pid)

	return nil
}

func (m *Manager) instanceDir(id string) string {
	return filepath.Join(m.instancesDir, id)
}
