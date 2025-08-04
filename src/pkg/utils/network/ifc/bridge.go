//go:build linux
// +build linux

package ifc

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"slices"
	"syscall"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
)

var ErrLinkExists = errors.New("link already exists")

func getFirstUsableIP(ipNet *net.IPNet) net.IP {
	if ipNet == nil {
		return nil
	}
	// Start with the network address
	firstIP := ipNet.IP

	// Create a copy to avoid modifying the original
	firstUsable := make(net.IP, len(firstIP))
	copy(firstUsable, firstIP)

	for i := len(firstUsable) - 1; i >= 0; i-- {
		firstUsable[i]++
		if firstUsable[i] != 0 {
			break
		}
	}

	return firstUsable
}

func CreateBridge(name string, subnet string, disableTxOffloading bool) error {
	_, ipnet, ipErr := net.ParseCIDR(subnet)
	if ipErr != nil {
		return fmt.Errorf("failed to parse subnet %s: %v", subnet, ipErr)
	}

	ip := getFirstUsableIP(ipnet)
	if ip == nil {
		return fmt.Errorf("failed to get first usable IP")
	}

	// Define the bridge attributes
	bridgeAttrs := netlink.NewLinkAttrs()
	bridgeAttrs.Name = name

	myBridge := &netlink.Bridge{
		LinkAttrs: bridgeAttrs,
	}

	// Add the bridge to the system
	linkAddErr := netlink.LinkAdd(myBridge)
	if linkAddErr != nil {
		if errors.Is(linkAddErr, syscall.EEXIST) {
			slog.Debug("Link already exists")
			addresses, addrErr := netlink.AddrList(myBridge, nl.FAMILY_V4)
			if addrErr != nil {
				return fmt.Errorf("failed to list interface addresses: %w", addrErr)
			}
			if ok := slices.ContainsFunc(addresses, func(addr netlink.Addr) bool {
				return addr.Equal(netlink.Addr{
					IPNet: &net.IPNet{
						IP:   ip,
						Mask: ipnet.Mask,
					},
				})
			}); ok {
				return nil
			}
		} else {
			return fmt.Errorf("failed to add bridge %s: %v", bridgeAttrs.Name, linkAddErr)
		}
	}

	if addrErr := netlink.AddrAdd(myBridge, &netlink.Addr{
		IPNet: &net.IPNet{
			IP:   ip,
			Mask: ipnet.Mask,
		},
	}); addrErr != nil {
		if linkDelErr := netlink.LinkDel(myBridge); linkDelErr != nil {
			return fmt.Errorf("failed to set ip: %v, failed to delete link: %v", addrErr, linkDelErr)
		}
		return fmt.Errorf("failed to set ip: %v", addrErr)
	}

	if err := netlink.LinkSetUp(myBridge); err != nil {
		if linkDelErr := netlink.LinkDel(myBridge); linkDelErr != nil {
			return fmt.Errorf("failed to bring bridge %s up: %v, failed to delete link: %v", bridgeAttrs.Name, err, linkDelErr)
		}
		return fmt.Errorf("failed to bring bridge %s up: %v", bridgeAttrs.Name, err)
	}

	slog.Debug("successfully created bridge", "name", bridgeAttrs.Name)

	if disableTxOffloading {
		cmd := exec.Command("ethtool", "-K", name, "tx", "off")
		return cmd.Run()
	}

	return nil
}

func CreateTap(name string, bridgeName string) error {
	link, linkErr := netlink.LinkByName(bridgeName)
	if linkErr != nil {
		return fmt.Errorf("failed to get bridge %s: %v", bridgeName, linkErr)
	}
	if bridge, ok := link.(*netlink.Bridge); ok {
		tap := &netlink.Tuntap{
			Mode: netlink.TUNTAP_MODE_TAP,
			LinkAttrs: netlink.LinkAttrs{
				Name:        name,
				MasterIndex: bridge.Index,
			},
		}

		_, err := netlink.LinkByName(name)
		if err != nil {
			if _, ok := err.(netlink.LinkNotFoundError); !ok {
				return fmt.Errorf("unexpected error checking link: %v", err)
			}
			if linkAddErr := netlink.LinkAdd(tap); linkAddErr != nil {
				return fmt.Errorf("failed to add tap device %s: %v", name, linkAddErr)
			}
		}

		tapLink, err := netlink.LinkByName(name)
		if err != nil {
			return fmt.Errorf("failed to get tap %s: %v", name, err)
		}
		if err := netlink.LinkSetUp(tapLink); err != nil {
			if linkDelErr := netlink.LinkDel(tapLink); linkDelErr != nil {
				return fmt.Errorf("failed to bring tap %s up: %v, failed to delete tap: %v", name, err, linkDelErr)
			}
			return fmt.Errorf("failed to bring tap %s up: %v", name, err)
		}

		slog.Debug("successfully added tap", "tap", name, "bridge", bridgeName)

		return nil
	}

	return fmt.Errorf("the link %s is not a bridge", bridgeName)
}

func DeleteLink(name string) error {
	link, linkErr := netlink.LinkByName(name)
	if linkErr != nil {
		return linkErr
	}

	if delErr := netlink.LinkDel(link); delErr != nil {
		return delErr
	}

	slog.Debug("successfully deleted link", "name", name)
	return nil
}
