package network

type NetworkManager interface {
	Close()
	CreateInterface(interfaceName string) error
	RemoveInterface(interfaceName string) error
}

type Event interface {
	isEvent()
}

type NetworkInterfaceAdded struct {
	Name string
}

type NetworkInterfaceRemoved struct {
	Name string
}

type DefaultInterfaceChanged struct {
	NewInterface string
	OldInterface string
}

func (e *NetworkInterfaceAdded) isEvent()   {}
func (e *NetworkInterfaceRemoved) isEvent() {}
func (e *DefaultInterfaceChanged) isEvent() {}
