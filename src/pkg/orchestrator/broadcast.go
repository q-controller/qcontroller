package orchestrator

import (
	"context"

	orchestratorv1 "github.com/q-controller/qcontroller/src/generated/services/orchestrator/v1"
)

// Subscription is a handle to receive broadcast events.
type Subscription struct {
	ch chan *orchestratorv1.Event
}

// Events returns a receive-only channel of events.
func (s *Subscription) Events() <-chan *orchestratorv1.Event {
	return s.ch
}

// Broadcaster fans out orchestrator events to WebSocket clients.
type Broadcaster struct {
	eventCh chan *orchestratorv1.Event
	subCh   chan *Subscription
	unsubCh chan *Subscription
}

func NewBroadcaster(bufferSize int) *Broadcaster {
	return &Broadcaster{
		eventCh: make(chan *orchestratorv1.Event, bufferSize),
		subCh:   make(chan *Subscription, bufferSize),
		unsubCh: make(chan *Subscription, bufferSize),
	}
}

func (b *Broadcaster) Run(ctx context.Context) {
	subs := map[*Subscription]struct{}{}
	for {
		select {
		case <-ctx.Done():
			for s := range subs {
				close(s.ch)
			}
			return
		case s := <-b.subCh:
			subs[s] = struct{}{}
		case s := <-b.unsubCh:
			delete(subs, s)
			close(s.ch)
		case event := <-b.eventCh:
			for s := range subs {
				select {
				case s.ch <- event:
				default:
				}
			}
		}
	}
}

// Send broadcasts an event to all subscribers. Non-blocking — drops if buffer is full.
func (b *Broadcaster) Send(event *orchestratorv1.Event) {
	select {
	case b.eventCh <- event:
	default:
	}
}

// Subscribe registers a new client.
func (b *Broadcaster) Subscribe() *Subscription {
	s := &Subscription{ch: make(chan *orchestratorv1.Event, 100)}
	b.subCh <- s
	return s
}

// Unsubscribe removes a client. The subscription's channel will be closed.
func (b *Broadcaster) Unsubscribe(s *Subscription) {
	b.unsubCh <- s
}
