package network

import (
	"sync"

	"github.com/q-controller/network-utils/src/utils/network/ifc"
)

type linuxNetworkManager struct {
	bridgeName string
	mu         sync.RWMutex
	done       chan struct{}
	events     chan Event
}

func (m *linuxNetworkManager) Close() {
	m.done <- struct{}{}
}

func (m *linuxNetworkManager) CreateInterface(interfaceName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if tapErr := ifc.CreateTap(interfaceName, m.bridgeName); tapErr != nil {
		return tapErr
	}

	return nil
}

func (m *linuxNetworkManager) RemoveInterface(interfaceName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if tapErr := ifc.DeleteLink(interfaceName); tapErr != nil {
		return tapErr
	}

	return nil
}

func NewNetworkManager(bridgeName, gateway string) (NetworkManager, error) {
	if bridgeErr := ifc.CreateBridge(bridgeName, gateway, true); bridgeErr != nil {
		return nil, bridgeErr
	}

	nm := &linuxNetworkManager{
		bridgeName: bridgeName,
		done:       make(chan struct{}),
		events:     make(chan Event, 10),
	}

	return nm, nil
}
