package cmd

import (
	"errors"
	"fmt"
	"net"

	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
)

type LinuxConfig struct {
	Name      string
	BridgeIp  net.IP
	GatewayIp net.IP
	StartIp   net.IP
	EndIp     net.IP
	Subnet    *net.IPNet
}

func NewLinuxConfig(settings *settingsv1.LinuxSettings) (*LinuxConfig, error) {
	if settings == nil {
		return nil, errors.New("linux settings cannot be nil")
	}

	hostIp, hostNet, hostErr := net.ParseCIDR(settings.Network.GatewayIp)
	if hostErr != nil {
		return nil, fmt.Errorf("failed to parse host_ip %s: %w", settings.Network.GatewayIp, hostErr)
	}

	bridgeIp, bridgeNet, bridgeErr := net.ParseCIDR(settings.Network.BridgeIp)
	if bridgeErr != nil {
		return nil, fmt.Errorf("failed to parse bridge_ip %s: %w", settings.Network.BridgeIp, bridgeErr)
	}

	if bridgeNet.String() != hostNet.String() {
		return nil, fmt.Errorf("bridge (%s) and host (%s) subnets are not same", bridgeNet.String(), hostNet.String())
	}

	if bridgeIp.Equal(hostIp) {
		return nil, fmt.Errorf("bridge_ip (%s) cannot be same as host_ip (%s)", bridgeIp.String(), hostIp.String())
	}

	startIp, startNet, startErr := net.ParseCIDR(settings.Network.Dhcp.Start)
	if startErr != nil {
		return nil, fmt.Errorf("failed to parse dhcp_start %s: %w", settings.Network.Dhcp.Start, startErr)
	}

	endIp, endNet, endErr := net.ParseCIDR(settings.Network.Dhcp.End)
	if endErr != nil {
		return nil, fmt.Errorf("failed to parse dhcp_end %s: %w", settings.Network.Dhcp.End, endErr)
	}

	if startNet.String() != hostNet.String() {
		return nil, fmt.Errorf("dhcp_start (%s) and network (%s) subnets are not same", startNet.String(), hostNet.String())
	}

	if endNet.String() != hostNet.String() {
		return nil, fmt.Errorf("dhcp_end (%s) and network (%s) subnets are not same", endNet.String(), hostNet.String())
	}

	return &LinuxConfig{
		BridgeIp:  bridgeIp,
		GatewayIp: hostIp,
		Subnet:    hostNet,
		Name:      settings.Network.Name,
		StartIp:   startIp,
		EndIp:     endIp,
	}, nil
}
