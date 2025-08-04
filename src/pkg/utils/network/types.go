package network

type NetworkManager interface {
	Close()
	CreateInterface(interfaceName string) error
	RemoveInterface(interfaceName string) error
}

type Event interface {
	isEvent()
}

type DefaultInterfaceChanged struct {
	NewInterface string
	OldInterface string
}

func (e *DefaultInterfaceChanged) isEvent() {}
