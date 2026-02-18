package network

import (
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
)

// GetBridgeInterface returns the network interface to use for bridged networking.
// On Linux, this always returns empty string as the tap interface is managed differently.
func GetBridgeInterface(config *settingsv1.QemuConfig) (string, error) {
	return "", nil
}
