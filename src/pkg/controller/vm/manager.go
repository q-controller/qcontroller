package vm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	controllerv1 "github.com/q-controller/qcontroller/src/generated/services/controller/v1"
	eventv1 "github.com/q-controller/qcontroller/src/generated/services/event/v1"
	processv1 "github.com/q-controller/qcontroller/src/generated/services/process/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	vmv1 "github.com/q-controller/qcontroller/src/generated/vm/statemachine/v1"
	"github.com/q-controller/qcontroller/src/pkg/controller"
	"github.com/q-controller/qcontroller/src/pkg/events"
	"github.com/q-controller/qcontroller/src/pkg/node"
	"github.com/q-controller/qcontroller/src/pkg/utils"
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

func newManager(local *settingsv1.Node, state controller.State, eventPublisher *events.Publisher, qemuTLS *settingsv1.TLSConfig) (*Manager, error) {
	if local == nil {
		return nil, errors.New("local node must be configured")
	}

	nm, nmErr := newLocalNodeManager(local.Name, local.Endpoint, state, qemuTLS)
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

func (m *Manager) Create(ctx context.Context, id, imageID string,
	cpus uint32, memory, disk uint32, cloudInit *vmv1.CloudInit) (string, error) {
	if err := m.nm.Create(ctx, id, imageID, cpus, memory, disk, cloudInit); err != nil {
		return "", err
	}

	_ = m.eventsPublisher.VMUpdated(&controllerv1.Info{
		Name: id,
		Spec: &controllerv1.VMSpec{
			Image: imageID,
			Vm:    &settingsv1.VM{Cpus: cpus, Memory: memory, Disk: disk},
		},
		Status: &controllerv1.VMStatus{
			State: vmv1.State_STATE_STOPPED.String(),
		},
	})

	return id, nil
}

func (m *Manager) Start(ctx context.Context, id string) error {
	go func() {
		asyncCtx, cancel := utils.AsyncCtx(ctx, m.ctx.Done())
		defer cancel()
		if err := m.nm.Start(asyncCtx, id); err != nil {
			slog.ErrorContext(asyncCtx, "Start failed", "id", id, "error", err)
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

func (m *Manager) Logs(ctx context.Context, req *processv1.LogsRequest) (<-chan *processv1.LogsResponse, error) {
	return m.nm.Logs(ctx, req)
}

// Snapshot performs a snapshot operation on an instance. List is synchronous and
// returns the snapshots; Save/Load/Delete are fire-and-forget — they return
// immediately and report completion or failure over the event stream.
func (m *Manager) Snapshot(ctx context.Context, op processv1.SnapshotOp, id, tag string) ([]*processv1.Snapshot, error) {
	if op == processv1.SnapshotOp_SNAPSHOT_OP_LIST {
		return m.nm.Snapshot(ctx, op, id, tag)
	}

	go func() {
		asyncCtx, cancel := utils.AsyncCtx(ctx, m.ctx.Done())
		defer cancel()
		label := snapshotOpLabel(op)
		_ = m.eventsPublisher.PublishSnapshot(op, eventv1.SnapshotEvent_PHASE_STARTED, id, tag,
			fmt.Sprintf("snapshot %q: %s started", tag, label))
		if _, err := m.nm.Snapshot(asyncCtx, op, id, tag); err != nil {
			slog.ErrorContext(asyncCtx, "snapshot operation failed", "op", op.String(), "id", id, "tag", tag, "error", err)
			_ = m.eventsPublisher.PublishSnapshot(op, eventv1.SnapshotEvent_PHASE_FAILED, id, tag,
				fmt.Sprintf("failed to %s snapshot %q: %v", label, tag, err))
			return
		}
		_ = m.eventsPublisher.PublishSnapshot(op, eventv1.SnapshotEvent_PHASE_COMPLETED, id, tag,
			fmt.Sprintf("snapshot %q: %s completed", tag, label))
	}()
	return nil, nil
}

// snapshotOpLabel is the human wording used in event messages.
func snapshotOpLabel(op processv1.SnapshotOp) string {
	switch op {
	case processv1.SnapshotOp_SNAPSHOT_OP_SAVE:
		return "save"
	case processv1.SnapshotOp_SNAPSHOT_OP_LOAD:
		return "restore"
	case processv1.SnapshotOp_SNAPSHOT_OP_DELETE:
		return "delete"
	default:
		return op.String()
	}
}

func (m *Manager) Close() {
	m.cancel()
}

var singleton *Manager
var once sync.Once

func CreateManager(local *settingsv1.Node, state controller.State, eventPublisher *events.Publisher, qemuTLS *settingsv1.TLSConfig) *Manager {
	once.Do(func() {
		mgr, mgrErr := newManager(local, state, eventPublisher, qemuTLS)
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
