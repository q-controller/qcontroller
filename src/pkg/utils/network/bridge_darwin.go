package network

import (
	"fmt"

	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
)

// GetBridgeInterface returns the network interface to use for bridged networking.
// Returns empty string for shared networking mode.
func GetBridgeInterface(config *settingsv1.QemuConfig) (string, error) {
	macosSettings := config.GetMacosSettings()
	if macosSettings == nil {
		return "", nil
	}

	switch macosSettings.Mode {
	case settingsv1.MacosSettings_MODE_BRIDGED:
		// Use specified interface or default
		if macosSettings.Bridged != nil && macosSettings.Bridged.Interface != "" {
			return macosSettings.Bridged.Interface, nil
		}
		// Get default interface for bridged mode
		defaultIface, err := GetDefaultInterface()
		if err != nil {
			return "", fmt.Errorf("failed to get default interface for bridged mode: %w", err)
		}
		return defaultIface, nil
	case settingsv1.MacosSettings_MODE_SHARED, settingsv1.MacosSettings_MODE_UNSPECIFIED:
		return "", nil
	default:
		return "", nil
	}
}
