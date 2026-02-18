package network

import (
	"fmt"
	"os/exec"
	"strings"
)

type defaultNetworkManager struct{}

func (m *defaultNetworkManager) Close() {}

func (m *defaultNetworkManager) CreateInterface(interfaceName string) error {
	return nil
}

func (m *defaultNetworkManager) RemoveInterface(interfaceName string) error {
	return nil
}

func NewNetworkManager(bridgeName, subnet string) (NetworkManager, error) {
	return &defaultNetworkManager{}, nil
}

func GetDefaultInterface() (string, error) {
	out, err := exec.Command("route", "get", "default").Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "interface:") {
			fields := strings.Fields(line)
			if len(fields) == 2 {
				return strings.TrimSpace(fields[1]), nil
			}
		}
	}
	return "", fmt.Errorf("default interface not found")
}
