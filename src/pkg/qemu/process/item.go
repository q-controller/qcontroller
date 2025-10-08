package process

import (
	"fmt"

	servicesv1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	"github.com/q-controller/qemu-client/pkg/qemu"
)

type Item struct {
	Instance *qemu.Instance

	subscribers map[chan<- *servicesv1.Event]struct{}
	add         chan chan<- *servicesv1.Event
}

func (item *Item) Subscribe(ch chan<- *servicesv1.Event) {
	item.add <- ch
}

func CreateItem(id string, instance *qemu.Instance) (*Item, error) {
	item := &Item{
		Instance:    instance,
		subscribers: make(map[chan<- *servicesv1.Event]struct{}),
		add:         make(chan chan<- *servicesv1.Event),
	}

	go func() {
	Loop:
		for {
			select {
			case ch, ok := <-item.add:
				if !ok {
					panic(fmt.Sprintf("item.add channel unexpectedly closed for instance %s", id))
				}
				item.subscribers[ch] = struct{}{}
				ch <- &servicesv1.Event{
					Id: id,
					EventKind: &servicesv1.Event_Status{
						Status: &servicesv1.Status{
							Running: true,
						},
					},
				}
			case <-item.Instance.Done:
				break Loop
			}
		}

		stop := &servicesv1.Event{
			Id: id,
			EventKind: &servicesv1.Event_Status{
				Status: &servicesv1.Status{
					Running: false,
				},
			},
		}
		for ch := range item.subscribers {
			select {
			case ch <- stop:
			default:
				// Drop message if subscriber is slow
			}
		}

		item.subscribers = nil
	}()

	return item, nil
}
