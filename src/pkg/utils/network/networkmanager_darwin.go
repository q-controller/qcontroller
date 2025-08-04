package network

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
