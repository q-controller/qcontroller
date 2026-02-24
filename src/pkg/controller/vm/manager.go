package vm

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	servicesv1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	vmv1 "github.com/q-controller/qcontroller/src/generated/vm/statemachine/v1"
	"github.com/q-controller/qcontroller/src/pkg/controller"
	"github.com/q-controller/qcontroller/src/pkg/events"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// Manager orchestrates VM operations across local and remote nodes.
//
// State is updated asynchronously:
//   - Local node: polling loop queries QemuService and publishes events
//   - Remote nodes: subscribes to each node's EventService
//
// Info() reads from the EventPublisher's cache — zero network calls.
// Mutations (Create/Start/Stop/Remove) call the target node directly.
type Manager struct {
	mutex     sync.RWMutex
	localNode string                 // local node name (empty if no local node)
	nodes     map[string]NodeManager // node name → manager
	vmIndex   map[string]string      // VM ID → node name (for routing mutations)
	ctx       context.Context
	cancel    context.CancelFunc

	eventsPublisher *events.Publisher
}

func newManager(local *settingsv1.Node, remotes []*settingsv1.Node, state controller.State, eventPublisher *events.Publisher) (*Manager, error) {
	nodes := make(map[string]NodeManager)

	var localName string
	if local != nil {
		nm, nmErr := newLocalNodeManager(local.Name, local.Endpoint, state)
		if nmErr != nil {
			return nil, nmErr
		}
		nodes[local.Name] = nm
		localName = local.Name
	}
	for _, remote := range remotes {
		nodes[remote.Name] = newRemoteNodeManager(remote.Name, remote.Endpoint)
	}

	ctx, cancel := context.WithCancel(context.Background())

	manager := Manager{
		localNode:       localName,
		nodes:           nodes,
		vmIndex:         make(map[string]string),
		ctx:             ctx,
		cancel:          cancel,
		eventsPublisher: eventPublisher,
	}

	// Local node: poll QemuService (local, fast).
	if local != nil {
		manager.startLocalPollingLoop()
	}

	// Remote nodes: subscribe to each node's EventService.
	for _, remote := range remotes {
		go manager.subscribeToNode(remote.Name, remote.Endpoint)
	}

	return &manager, nil
}

func (m *Manager) Create(id, imageId string,
	cpus uint32, memory, disk uint32, cloudInit *vmv1.CloudInit, node string) (string, error) {
	m.mutex.RLock()
	if node == "" {
		if m.localNode == "" {
			m.mutex.RUnlock()
			return "", fmt.Errorf("no local node configured")
		}
		node = m.localNode
	}
	nm, ok := m.nodes[node]
	m.mutex.RUnlock()
	if !ok {
		return "", fmt.Errorf("node %s not found", node)
	}

	if err := nm.Create(m.ctx, id, imageId, cpus, memory, disk, cloudInit); err != nil {
		return "", err
	}

	qualifiedName := m.qualifyName(node, id)

	m.mutex.Lock()
	m.vmIndex[qualifiedName] = node
	m.mutex.Unlock()

	// Seed the cache so the VM appears in the UI immediately after creation.
	_ = m.eventsPublisher.VMUpdated(&servicesv1.Info{
		Name:    qualifiedName,
		State:   vmv1.State_STATE_STOPPED.String(),
		ImageId: imageId,
		Node:    node,
		Details: &settingsv1.VM{Cpus: cpus, Memory: memory, Disk: disk},
	})

	return qualifiedName, nil
}

func (m *Manager) Start(_ context.Context, id string) error {
	m.mutex.RLock()
	vmName, nm, err := m.resolveNode(id)
	m.mutex.RUnlock()
	if err != nil {
		return err
	}

	go func() {
		if err := nm.Start(m.ctx, vmName); err != nil {
			slog.Error("Start failed", "id", id, "error", err)
			_ = m.eventsPublisher.PublishError(fmt.Sprintf("failed to start: %v", err), id)
		}
	}()

	return nil
}

func (m *Manager) Stop(ctx context.Context, id string, force bool) error {
	m.mutex.RLock()
	vmName, nm, err := m.resolveNode(id)
	m.mutex.RUnlock()
	if err != nil {
		return err
	}

	return nm.Stop(ctx, vmName, force)
}

func (m *Manager) Remove(ctx context.Context, id string) error {
	m.mutex.RLock()
	vmName, nm, err := m.resolveNode(id)
	m.mutex.RUnlock()
	if err != nil {
		return err
	}

	if removeErr := nm.Remove(ctx, vmName); removeErr != nil {
		return removeErr
	}

	m.mutex.Lock()
	delete(m.vmIndex, id)
	m.mutex.Unlock()

	if eventErr := m.eventsPublisher.VMRemoved(id); eventErr != nil {
		slog.Warn("Failed to publish VM removal event", "id", id, "error", eventErr)
	}

	return nil
}

// Info reads from the EventPublisher's cache. No network calls.
func (m *Manager) Info(_ context.Context, id string) ([]*servicesv1.Info, error) {
	if id != "" {
		info := m.eventsPublisher.Get(id)
		if info == nil {
			return nil, fmt.Errorf("instance %s not found", id)
		}
		return []*servicesv1.Info{info}, nil
	}
	return m.eventsPublisher.GetAll(), nil
}

func (m *Manager) ListNodes() []*settingsv1.Node {
	out := make([]*settingsv1.Node, 0, len(m.nodes))
	for name, nm := range m.nodes {
		out = append(out, &settingsv1.Node{Name: name, Endpoint: nm.Endpoint()})
	}
	return out
}

func (m *Manager) Close() {
	m.cancel()
}

// qualifyName returns "node:vmName" for remote nodes, plain "vmName" for local.
func (m *Manager) qualifyName(node, vmName string) string {
	if node == m.localNode {
		return vmName
	}
	return node + ":" + vmName
}

// resolveNode looks up a qualified ID and returns the plain VM name + node manager.
func (m *Manager) resolveNode(id string) (string, NodeManager, error) {
	node, ok := m.vmIndex[id]
	if !ok {
		return "", nil, fmt.Errorf("instance %s not found", id)
	}
	nm, nmOk := m.nodes[node]
	if !nmOk {
		return "", nil, fmt.Errorf("node %s not found", node)
	}
	// Extract plain VM name: strip "node:" prefix for remote VMs.
	vmName := id
	if prefix := node + ":"; strings.HasPrefix(id, prefix) {
		vmName = id[len(prefix):]
	}
	return vmName, nm, nil
}

var singleton *Manager
var once sync.Once

func CreateManager(local *settingsv1.Node, remotes []*settingsv1.Node, state controller.State, eventPublisher *events.Publisher) *Manager {
	once.Do(func() {
		mgr, mgrErr := newManager(local, remotes, state, eventPublisher)
		if mgrErr != nil {
			slog.Error("failed to create VM manager", "error", mgrErr)
		}
		singleton = mgr
	})
	return singleton
}

// --- Local node: polling loop ---

const localPollInterval = 3 * time.Second

func (m *Manager) startLocalPollingLoop() {
	go func() {
		m.pollLocalNode()

		ticker := time.NewTicker(localPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
				m.pollLocalNode()
			}
		}
	}()
}

func (m *Manager) pollLocalNode() {
	m.mutex.RLock()
	nm, ok := m.nodes[m.localNode]
	m.mutex.RUnlock()
	if !ok {
		return
	}

	infos, err := nm.Info(m.ctx, "")
	if err != nil {
		slog.Debug("Failed to poll local node", "node", m.localNode, "error", err)
		return
	}

	newLocal := make(map[string]bool, len(infos))
	for _, info := range infos {
		newLocal[info.Name] = true
	}

	m.mutex.Lock()
	for _, info := range infos {
		m.vmIndex[info.Name] = m.localNode
	}
	// Detect local VMs that disappeared.
	for vmName, node := range m.vmIndex {
		if node == m.localNode && !newLocal[vmName] {
			delete(m.vmIndex, vmName)
			if eventErr := m.eventsPublisher.VMRemoved(vmName); eventErr != nil {
				slog.Warn("Failed to publish VM removal event", "id", vmName, "error", eventErr)
			}
		}
	}
	m.mutex.Unlock()

	for _, info := range infos {
		if eventErr := m.eventsPublisher.VMUpdated(info); eventErr != nil {
			slog.Warn("Failed to publish VM info event", "id", info.Name, "error", eventErr)
		}
	}
}

// --- Remote nodes: event subscriptions ---

const subscribeRetryInterval = 2 * time.Second

func (m *Manager) subscribeToNode(nodeName, endpoint string) {
	for {
		select {
		case <-m.ctx.Done():
			return
		default:
		}

		err := m.runSubscription(nodeName, endpoint)
		if err != nil {
			slog.Warn("Event subscription lost, reconnecting", "node", nodeName, "error", err)
		}

		select {
		case <-m.ctx.Done():
			return
		case <-time.After(subscribeRetryInterval):
		}
	}
}

func (m *Manager) runSubscription(nodeName, endpoint string) error {
	conn, err := grpc.NewClient(endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		return fmt.Errorf("dial %s: %w", endpoint, err)
	}
	defer conn.Close()

	// Seed initial state before subscribing.
	// On reconnect, also reconcile: remove VMs that disappeared while disconnected.
	controllerCli := servicesv1.NewControllerServiceClient(conn)
	infoResp, infoErr := controllerCli.Info(m.ctx, &servicesv1.InfoRequest{})
	if infoErr != nil {
		return fmt.Errorf("initial info %s: %w", nodeName, infoErr)
	}

	currentVMs := make(map[string]bool, len(infoResp.Info))
	for _, info := range infoResp.Info {
		currentVMs[m.qualifyName(nodeName, info.Name)] = true
	}

	m.mutex.Lock()
	for vmName, owner := range m.vmIndex {
		if owner == nodeName && !currentVMs[vmName] {
			delete(m.vmIndex, vmName)
			if eventErr := m.eventsPublisher.VMRemoved(vmName); eventErr != nil {
				slog.Warn("Failed to remove stale VM on reconnect", "id", vmName, "error", eventErr)
			}
		}
	}
	for _, info := range infoResp.Info {
		m.vmIndex[m.qualifyName(nodeName, info.Name)] = nodeName
	}
	m.mutex.Unlock()

	for _, info := range infoResp.Info {
		info.Name = m.qualifyName(nodeName, info.Name)
		info.Node = nodeName
		if eventErr := m.eventsPublisher.VMUpdated(info); eventErr != nil {
			slog.Warn("Failed to publish initial VM info", "id", info.Name, "error", eventErr)
		}
	}

	stream, err := servicesv1.NewEventServiceClient(conn).Subscribe(m.ctx, &servicesv1.SubscribeRequest{})
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", nodeName, err)
	}

	slog.Info("Subscribed to node events", "node", nodeName, "endpoint", endpoint)

	for {
		resp, recvErr := stream.Recv()
		if recvErr != nil {
			return fmt.Errorf("recv %s: %w", nodeName, recvErr)
		}

		update := resp.GetUpdate()
		if update == nil {
			continue
		}

		vmEvent := update.GetVmEvent()
		if vmEvent == nil {
			continue
		}

		info := vmEvent.GetInfo()
		if info == nil {
			continue
		}

		qualifiedName := m.qualifyName(nodeName, info.Name)

		switch vmEvent.Type {
		case servicesv1.VMEvent_EVENT_TYPE_UPDATED:
			m.mutex.Lock()
			m.vmIndex[qualifiedName] = nodeName
			m.mutex.Unlock()

			info.Name = qualifiedName
			info.Node = nodeName
			if eventErr := m.eventsPublisher.VMUpdated(info); eventErr != nil {
				slog.Warn("Failed to forward VM event", "id", qualifiedName, "error", eventErr)
			}

		case servicesv1.VMEvent_EVENT_TYPE_REMOVED:
			m.mutex.Lock()
			delete(m.vmIndex, qualifiedName)
			m.mutex.Unlock()

			if eventErr := m.eventsPublisher.VMRemoved(qualifiedName); eventErr != nil {
				slog.Warn("Failed to forward VM removal event", "id", qualifiedName, "error", eventErr)
			}
		}
	}
}
