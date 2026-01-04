package events

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	v1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

type Publisher struct {
	ch    chan<- *v1.PublishRequest
	done  atomic.Bool
	mu    sync.Mutex
	cache map[string]*v1.Info
}

func (p *Publisher) VMUpdated(info *v1.Info) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	prev, exists := p.cache[info.Name]
	if exists && proto.Equal(prev, info) {
		return nil // No change, skip send
	}
	p.cache[info.Name] = proto.Clone(info).(*v1.Info)
	update := &v1.Update{
		Timestamp: time.Now().Unix(),
		Payload: &v1.Update_VmEvent{
			VmEvent: &v1.VMEvent{
				Info: info,
				Type: v1.VMEvent_EVENT_TYPE_UPDATED,
			},
		},
	}
	return p.publish(&v1.PublishRequest{Update: update})
}

func (p *Publisher) VMRemoved(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.cache, id)
	update := &v1.Update{
		Timestamp: time.Now().Unix(),
		Payload: &v1.Update_VmEvent{
			VmEvent: &v1.VMEvent{
				Info: &v1.Info{
					Name: id,
				},
				Type: v1.VMEvent_EVENT_TYPE_REMOVED,
			},
		},
	}
	return p.publish(&v1.PublishRequest{Update: update})
}

func (p *Publisher) PublishImageUpdate(image *v1.VMImage, eventType v1.ImageEvent_EventType) error {
	update := &v1.Update{
		Timestamp: time.Now().Unix(),
		Payload: &v1.Update_ImageEvent{
			ImageEvent: &v1.ImageEvent{
				Image: image,
				Type:  eventType,
			},
		},
	}
	return p.publish(&v1.PublishRequest{Update: update})
}

func (p *Publisher) PublishError(message, resource string) error {
	update := &v1.Update{
		Timestamp: time.Now().Unix(),
		Payload: &v1.Update_ErrorEvent{
			ErrorEvent: &v1.ErrorEvent{
				Message:  message,
				Resource: resource,
			},
		},
	}
	return p.publish(&v1.PublishRequest{Update: update})
}

func (p *Publisher) publish(req *v1.PublishRequest) (err error) {
	if p.done.Load() {
		return fmt.Errorf("publisher is closed")
	}

	select {
	case p.ch <- req:
		return nil
	default:
		return fmt.Errorf("event queue is full, dropping event")
	}
}

func NewEventPublisher(ctx context.Context, endpoint string) (*Publisher, error) {
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("did not connect: %w", err)
	}

	events := make(chan *v1.PublishRequest, 100)
	publisher := &Publisher{ch: events, cache: make(map[string]*v1.Info)}

	go func() {
		defer func() {
			publisher.done.Store(true)
			close(events)
			_ = conn.Close()
		}()

		cli := v1.NewEventServiceClient(conn)
		stream, streamErr := cli.Publish(ctx)
		if streamErr != nil {
			return
		}
		defer func() {
			_ = stream.CloseSend()
		}()

		for {
			select {
			case <-stream.Context().Done():
				for {
					select {
					case event, ok := <-events:
						if !ok {
							return
						}
						if sendErr := stream.Send(event); sendErr != nil {
							return
						}
					default:
						return
					}
				}
			case event, ok := <-events:
				if !ok {
					return
				}
				if sendErr := stream.Send(event); sendErr != nil {
					return
				}
			}
		}
	}()

	return publisher, nil
}
