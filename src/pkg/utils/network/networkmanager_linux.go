package network

import (
	"errors"
	"log/slog"
	"net"
	"sync"

	"github.com/krjakbrjak/qcontroller/src/pkg/utils/network/firewall"
	"github.com/krjakbrjak/qcontroller/src/pkg/utils/network/ifc"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
)

var ErrNetworkDisconnected = errors.New("network disconnected")

type linuxNetworkManager struct {
	bridgeName string
	mu         sync.RWMutex
	taps       map[string]bool
	done       chan struct{}
	events     chan Event
}

func (m *linuxNetworkManager) Close() {
	m.done <- struct{}{}
}

// getDefaultInterface finds the default network interface
func getDefaultInterface() (string, error) {
	routes, err := netlink.RouteList(nil, nl.FAMILY_V4)
	if err != nil {
		return "", err
	}

	for _, route := range routes {
		if route.Dst.IP.IsUnspecified() && route.Dst.Mask.String() == net.CIDRMask(0, 32).String() {
			link, err := netlink.LinkByIndex(route.LinkIndex)
			if err != nil {
				continue
			}
			return link.Attrs().Name, nil
		}
	}
	return "", ErrNetworkDisconnected
}

func (m *linuxNetworkManager) CreateInterface(interfaceName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if tapErr := ifc.CreateTap(interfaceName, m.bridgeName); tapErr != nil && !errors.Is(tapErr, ifc.ErrLinkExists) {
		return tapErr
	}

	m.taps[interfaceName] = true
	m.events <- &NetworkInterfaceAdded{
		Name: interfaceName,
	}

	return nil
}

func (m *linuxNetworkManager) RemoveInterface(interfaceName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, interfaceExists := m.taps[interfaceName]; interfaceExists {
		delete(m.taps, interfaceName)

		m.events <- &NetworkInterfaceRemoved{
			Name: interfaceName,
		}

		return ifc.DeleteLink(interfaceName)
	}

	return nil
}

func NewNetworkManager(bridgeName, subnet string) (NetworkManager, error) {
	if bridgeErr := ifc.CreateBridge(bridgeName, subnet, true); bridgeErr != nil {
		return nil, bridgeErr
	}

	nm := &linuxNetworkManager{
		bridgeName: bridgeName,
		taps:       make(map[string]bool),
		done:       make(chan struct{}),
		events:     make(chan Event, 10),
	}

	currentDefaultInterface := ""
	go func() {
		for event := range nm.events {
			switch ev := event.(type) {
			case *NetworkInterfaceAdded:
				if tapErr := firewall.ConfigureTap("", currentDefaultInterface, ev.Name); tapErr != nil {
					slog.Error("could not configure tap", "tap", ev.Name, "error", tapErr)
				}
			case *NetworkInterfaceRemoved:
				if tapErr := firewall.ConfigureTap(currentDefaultInterface, "", ev.Name); tapErr != nil {
					slog.Error("could not configure tap", "tap", ev.Name, "error", tapErr)
				}
			case *DefaultInterfaceChanged:
				if firewallErr := firewall.ConfigureFirewall(ev.OldInterface,
					ev.NewInterface,
					subnet); firewallErr == nil {
					nm.mu.RLock()
					for tap := range nm.taps {
						if tapErr := firewall.ConfigureTap(ev.OldInterface,
							ev.NewInterface,
							tap); tapErr != nil {
							slog.Error("could not configure tap", "tap", tap, "error", tapErr)
						}
					}
					nm.mu.RUnlock()
				} else {
					slog.Error("could not configure firewall", "new interface", ev.NewInterface, "error", firewallErr)
				}
			}
		}
	}()

	updates := make(chan netlink.LinkUpdate)

	subscribeErr := netlink.LinkSubscribe(updates, nm.done)
	if subscribeErr != nil {
		return nil, subscribeErr
	}

	if defaultInterface, defaultInterfaceErr := getDefaultInterface(); defaultInterfaceErr == nil && defaultInterface != "" {
		nm.events <- &DefaultInterfaceChanged{
			NewInterface: defaultInterface,
			OldInterface: currentDefaultInterface,
		}
		currentDefaultInterface = defaultInterface
	} else {
		return nil, defaultInterfaceErr
	}

	go func() {
	outerloop:
		for {
			select {
			case <-updates:
				newDefaultInterface := ""
				if defaultInterface, defaultInterfaceErr := getDefaultInterface(); defaultInterfaceErr == nil {
					newDefaultInterface = defaultInterface
				}
				if currentDefaultInterface != newDefaultInterface {
					if newDefaultInterface == "" {
						slog.Debug("Got disconnected from the internet")
					} else {
						slog.Debug("New default interface was configured", "interface", newDefaultInterface)
					}
					nm.events <- &DefaultInterfaceChanged{
						NewInterface: newDefaultInterface,
						OldInterface: currentDefaultInterface,
					}
					currentDefaultInterface = newDefaultInterface
				}
			case <-nm.done:
				break outerloop
			}
		}
	}()

	return nm, nil
}
