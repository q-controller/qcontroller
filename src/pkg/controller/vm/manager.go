package vm

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	controllerv1 "github.com/q-controller/qcontroller/src/generated/services/controller/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	vmv1 "github.com/q-controller/qcontroller/src/generated/vm/statemachine/v1"
	"github.com/q-controller/qcontroller/src/pkg/controller"
	"github.com/q-controller/qcontroller/src/pkg/events"
	"github.com/q-controller/qcontroller/src/pkg/node"
)

// Manager manages VMs on the local node.
//
// State is updated via a polling loop that queries QemuService.
// Info() reads from the EventPublisher's cache.
// Mutations go directly to the local QemuService.
type Manager struct {
	nm     node.Manager
	ctx    context.Context
	cancel context.CancelFunc

	eventsPublisher *events.Publisher
}

func newManager(local *settingsv1.Node, state controller.State, eventPublisher *events.Publisher) (*Manager, error) {
	if local == nil {
		return nil, fmt.Errorf("local node must be configured")
	}

	nm, nmErr := newLocalNodeManager(local.Name, local.Endpoint, state)
	if nmErr != nil {
		return nil, nmErr
	}

	ctx, cancel := context.WithCancel(context.Background())

	manager := &Manager{
		nm:              nm,
		ctx:             ctx,
		cancel:          cancel,
		eventsPublisher: eventPublisher,
	}

	manager.startPollingLoop()

	return manager, nil
}

func (m *Manager) Create(ctx context.Context, id, imageId string,
	cpus uint32, memory, disk uint32, cloudInit *vmv1.CloudInit) (string, error) {
	if err := m.nm.Create(ctx, id, imageId, cpus, memory, disk, cloudInit); err != nil {
		return "", err
	}

	_ = m.eventsPublisher.VMUpdated(&controllerv1.Info{
		Name: id,
		Spec: &controllerv1.VMSpec{
			Image: imageId,
			Vm:    &settingsv1.VM{Cpus: cpus, Memory: memory, Disk: disk},
		},
		Status: &controllerv1.VMStatus{
			State: vmv1.State_STATE_STOPPED.String(),
		},
	})

	return id, nil
}

func (m *Manager) Start(_ context.Context, id string) error {
	go func() {
		if err := m.nm.Start(m.ctx, id); err != nil {
			slog.Error("Start failed", "id", id, "error", err)
			_ = m.eventsPublisher.PublishError(fmt.Sprintf("failed to start: %v", err), id)
		}
	}()
	return nil
}

func (m *Manager) Stop(ctx context.Context, id string, force bool) error {
	return m.nm.Stop(ctx, id, force)
}

func (m *Manager) Remove(ctx context.Context, id string) error {
	if removeErr := m.nm.Remove(ctx, id); removeErr != nil {
		return removeErr
	}

	if eventErr := m.eventsPublisher.VMRemoved(id); eventErr != nil {
		slog.Warn("Failed to publish VM removal event", "id", id, "error", eventErr)
	}

	return nil
}

func (m *Manager) Info(ctx context.Context, id string) ([]*controllerv1.Info, error) {
	return m.nm.Info(ctx, id)
}

func (m *Manager) Close() {
	m.cancel()
}

var singleton *Manager
var once sync.Once

func CreateManager(local *settingsv1.Node, state controller.State, eventPublisher *events.Publisher) *Manager {
	once.Do(func() {
		mgr, mgrErr := newManager(local, state, eventPublisher)
		if mgrErr != nil {
			slog.Error("failed to create VM manager", "error", mgrErr)
		}
		singleton = mgr
	})
	return singleton
}

const localPollInterval = 3 * time.Second

func (m *Manager) startPollingLoop() {
	go func() {
		m.poll()

		ticker := time.NewTicker(localPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
				m.poll()
			}
		}
	}()
}

func (m *Manager) poll() {
	infos, err := m.nm.Info(m.ctx, "")
	if err != nil {
		slog.Debug("Failed to poll local node", "error", err)
		return
	}

	current := make(map[string]bool, len(infos))
	for _, info := range infos {
		current[info.Name] = true
	}

	// Detect VMs that disappeared.
	for _, cached := range m.eventsPublisher.GetAll() {
		if !current[cached.Name] {
			if eventErr := m.eventsPublisher.VMRemoved(cached.Name); eventErr != nil {
				slog.Warn("Failed to publish VM removal event", "id", cached.Name, "error", eventErr)
			}
		}
	}

	for _, info := range infos {
		if eventErr := m.eventsPublisher.VMUpdated(info); eventErr != nil {
			slog.Warn("Failed to publish VM info event", "id", info.Name, "error", eventErr)
		}
	}
}
