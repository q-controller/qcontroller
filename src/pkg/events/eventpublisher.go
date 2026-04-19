package events

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	controllerv1 "github.com/q-controller/qcontroller/src/generated/services/controller/v1"
	eventv1 "github.com/q-controller/qcontroller/src/generated/services/event/v1"
	fileregistryv1 "github.com/q-controller/qcontroller/src/generated/services/fileregistry/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qcontroller/src/pkg/grpcutil"
	"google.golang.org/protobuf/proto"
)

type Publisher struct {
	ch    chan<- *eventv1.PublishRequest
	done  atomic.Bool
	mu    sync.Mutex
	cache map[string]*controllerv1.Info
}

func (p *Publisher) VMUpdated(info *controllerv1.Info) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	prev, exists := p.cache[info.Name]
	if exists && proto.Equal(prev, info) {
		slog.Debug("Skipping duplicate VM event", "vm", info.Name, "state", info.GetStatus().GetState(), "ips", info.GetStatus().GetRuntimeInfo().GetIpaddresses())
		return nil // No change, skip send
	}
	if exists {
		slog.Debug("VM info changed", "vm", info.Name, "state", info.GetStatus().GetState(), "ips", info.GetStatus().GetRuntimeInfo().GetIpaddresses(), "prev_ips", prev.GetStatus().GetRuntimeInfo().GetIpaddresses())
	} else {
		slog.Info("First VM event", "vm", info.Name, "state", info.GetStatus().GetState(), "ips", info.GetStatus().GetRuntimeInfo().GetIpaddresses())
	}
	p.cache[info.Name] = proto.Clone(info).(*controllerv1.Info)
	update := &eventv1.Update{
		Timestamp: time.Now().Unix(),
		Payload: &eventv1.Update_VmEvent{
			VmEvent: &eventv1.VMEvent{
				Info: info,
				Type: eventv1.VMEvent_EVENT_TYPE_UPDATED,
			},
		},
	}
	return p.publish(&eventv1.PublishRequest{Update: update})
}

func (p *Publisher) VMRemoved(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.cache, id)
	update := &eventv1.Update{
		Timestamp: time.Now().Unix(),
		Payload: &eventv1.Update_VmEvent{
			VmEvent: &eventv1.VMEvent{
				Info: &controllerv1.Info{
					Name: id,
				},
				Type: eventv1.VMEvent_EVENT_TYPE_REMOVED,
			},
		},
	}
	return p.publish(&eventv1.PublishRequest{Update: update})
}

// GetAll returns a snapshot of all cached VM infos.
func (p *Publisher) GetAll() []*controllerv1.Info {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*controllerv1.Info, 0, len(p.cache))
	for _, info := range p.cache {
		out = append(out, info)
	}
	return out
}

// Get returns the cached info for a specific VM, or nil if not found.
func (p *Publisher) Get(name string) *controllerv1.Info {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cache[name]
}

func (p *Publisher) PublishImageUpdate(image *fileregistryv1.VMImage, eventType eventv1.ImageEvent_EventType) error {
	update := &eventv1.Update{
		Timestamp: time.Now().Unix(),
		Payload: &eventv1.Update_ImageEvent{
			ImageEvent: &eventv1.ImageEvent{
				Image: image,
				Type:  eventType,
			},
		},
	}
	return p.publish(&eventv1.PublishRequest{Update: update})
}

func (p *Publisher) PublishProgress(resource, message string, percent int32) error {
	update := &eventv1.Update{
		Timestamp: time.Now().Unix(),
		Payload: &eventv1.Update_ProgressEvent{
			ProgressEvent: &eventv1.ProgressEvent{
				Resource: resource,
				Message:  message,
				Percent:  percent,
			},
		},
	}
	return p.publish(&eventv1.PublishRequest{Update: update})
}

func (p *Publisher) PublishError(message, resource string) error {
	update := &eventv1.Update{
		Timestamp: time.Now().Unix(),
		Payload: &eventv1.Update_ErrorEvent{
			ErrorEvent: &eventv1.ErrorEvent{
				Message:  message,
				Resource: resource,
			},
		},
	}
	return p.publish(&eventv1.PublishRequest{Update: update})
}

func (p *Publisher) publish(req *eventv1.PublishRequest) (err error) {
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

func NewEventPublisher(ctx context.Context, endpoint string, tlsCfg *settingsv1.TLSConfig) (*Publisher, error) {
	conn, err := grpcutil.Dial(endpoint, grpcutil.WithTLS(tlsCfg))
	if err != nil {
		return nil, fmt.Errorf("did not connect: %w", err)
	}

	events := make(chan *eventv1.PublishRequest, 100)
	publisher := &Publisher{ch: events, cache: make(map[string]*controllerv1.Info)}

	go func() {
		defer func() {
			publisher.done.Store(true)
			close(events)
			_ = conn.Close()
		}()

		cli := eventv1.NewEventServiceClient(conn)

		for {
			// Establish stream with retry
			var stream eventv1.EventService_PublishClient
			for {
				var streamErr error
				stream, streamErr = cli.Publish(ctx)
				if streamErr == nil {
					break
				}
				slog.Debug("Event service not ready, retrying", "error", streamErr)
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
				}
			}

			// Send events until stream breaks
			streamBroken := false
			for !streamBroken {
				select {
				case <-ctx.Done():
					_ = stream.CloseSend()
					return
				case event, ok := <-events:
					if !ok {
						_ = stream.CloseSend()
						return
					}
					if sendErr := stream.Send(event); sendErr != nil {
						slog.Warn("Event stream broken, reconnecting", "error", sendErr)
						streamBroken = true
					}
				}
			}

			// Back off before reconnecting
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()

	return publisher, nil
}
