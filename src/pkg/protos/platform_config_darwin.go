//go:build darwin

package protos

import (
	"fmt"
	"net"

	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qcontroller/src/pkg/utils/network"
	"github.com/q-controller/qemu-client/pkg/qemu"
)

// buildPlatformConfig creates platform-specific configuration for macOS.
func buildPlatformConfig(config *settingsv1.QemuConfig) (*qemu.PlatformConfig, error) {
	macosSettings := config.GetMacosSettings()
	if macosSettings == nil {
		return nil, fmt.Errorf("macOS settings required")
	}

	darwinNet := &qemu.DarwinNetworkConfig{}

	switch macosSettings.Mode {
	case settingsv1.MacosSettings_MODE_BRIDGED:
		bridgeInterface, bridgeErr := network.GetBridgeInterface(config)
		if bridgeErr != nil {
			return nil, fmt.Errorf("failed to get bridge interface: %w", bridgeErr)
		}
		darwinNet.Bridged = &qemu.VmnetBridged{
			Interface: bridgeInterface,
		}

	case settingsv1.MacosSettings_MODE_SHARED:
		if macosSettings.Shared == nil {
			return nil, fmt.Errorf("shared mode requires shared configuration")
		}
		_, ipNet, parseErr := net.ParseCIDR(macosSettings.Shared.Subnet)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid subnet in macOS settings: %w", parseErr)
		}
		darwinNet.Shared = &qemu.VmnetShared{
			StartAddress: macosSettings.Shared.StartAddress,
			EndAddress:   macosSettings.Shared.EndAddress,
			SubnetMask:   net.IP(ipNet.Mask).String(),
		}

	default:
		return nil, fmt.Errorf("unsupported macOS network mode: %v", macosSettings.Mode)
	}

	return &qemu.PlatformConfig{
		Network: darwinNet,
	}, nil
}
