//go:build linux

package protos

import (
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qemu-client/pkg/qemu"
)

// buildPlatformConfig creates platform-specific configuration for Linux.
func buildPlatformConfig(config *settingsv1.QemuConfig) (*qemu.PlatformConfig, error) {
	// Linux uses tap networking with VM ID as interface name
	// No additional configuration needed
	return &qemu.PlatformConfig{
		Network: &qemu.LinuxNetworkConfig{},
	}, nil
}
