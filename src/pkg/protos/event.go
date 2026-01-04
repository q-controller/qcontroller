package protos

import (
	"io"
	"sync"

	v1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	"google.golang.org/grpc"
)

type EventServer struct {
	v1.UnimplementedEventServiceServer

	mu          sync.RWMutex
	subscribers map[grpc.ServerStreamingServer[v1.SubscribeResponse]]struct{}
}

func NewEventServer() *EventServer {
	return &EventServer{
		subscribers: make(map[grpc.ServerStreamingServer[v1.SubscribeResponse]]struct{}),
	}
}

func (s *EventServer) Subscribe(req *v1.SubscribeRequest, srv grpc.ServerStreamingServer[v1.SubscribeResponse]) error {
	// Register subscriber
	s.mu.Lock()
	s.subscribers[srv] = struct{}{}
	s.mu.Unlock()

	// Wait until the client disconnects
	<-srv.Context().Done()

	// Unregister subscriber
	s.mu.Lock()
	delete(s.subscribers, srv)
	s.mu.Unlock()

	return nil
}

func (s *EventServer) Publish(srv grpc.ClientStreamingServer[v1.PublishRequest, v1.PublishResponse]) error {
	for {
		msg, recvErr := srv.Recv()
		if recvErr != nil {
			if recvErr == io.EOF {
				break
			}
			return recvErr
		}

		// Broadcast to all subscribers
		s.mu.RLock()
		// Make a copy to avoid holding lock during sends
		subs := make([]grpc.ServerStreamingServer[v1.SubscribeResponse], 0, len(s.subscribers))
		for sub := range s.subscribers {
			subs = append(subs, sub)
		}
		s.mu.RUnlock()

		// Track failed subscribers to remove them in batch
		var failedSubs []grpc.ServerStreamingServer[v1.SubscribeResponse]
		for _, sub := range subs {
			if err := sub.Send(&v1.SubscribeResponse{
				Update: msg.Update,
			}); err != nil {
				failedSubs = append(failedSubs, sub)
			}
		}

		// Remove failed subscribers in batch
		if len(failedSubs) > 0 {
			s.mu.Lock()
			for _, sub := range failedSubs {
				delete(s.subscribers, sub)
			}
			s.mu.Unlock()
		}
	}

	// Send an empty response
	if sendErr := srv.SendAndClose(&v1.PublishResponse{}); sendErr != nil {
		return sendErr
	}

	return nil
}
