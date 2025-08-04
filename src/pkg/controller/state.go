package controller

import v1 "github.com/krjakbrjak/qcontroller/src/generated/vm/statemachine/v1"

type State interface {
	Update(instance *v1.Instance) (*v1.Instance, error)
	Get(id string) (*v1.Instance, error)
	List() ([]*v1.Instance, error)
	Remove(id string) error
}
