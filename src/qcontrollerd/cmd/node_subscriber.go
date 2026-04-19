package cmd

import (
	"context"
	"io"
	"log/slog"
	"time"

	controllerv1 "github.com/q-controller/qcontroller/src/generated/services/controller/v1"
	eventv1 "github.com/q-controller/qcontroller/src/generated/services/event/v1"
	orchestratorv1 "github.com/q-controller/qcontroller/src/generated/services/orchestrator/v1"
	"github.com/q-controller/qcontroller/src/pkg/orchestrator"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func subscribeToNodeEvents(ctx context.Context, nodeName, controllerEndpoint, eventsEndpoint string, bc *orchestrator.Broadcaster) {
	controllerConn, controllerConnErr := grpc.NewClient(controllerEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if controllerConnErr != nil {
		slog.Error("Failed to connect to controller", "node", nodeName, "error", controllerConnErr)
		return
	}
	defer func() { _ = controllerConn.Close() }()

	eventsConn, eventsConnErr := grpc.NewClient(eventsEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if eventsConnErr != nil {
		slog.Error("Failed to connect to event service", "node", nodeName, "error", eventsConnErr)
		return
	}
	defer func() { _ = eventsConn.Close() }()

	eventCli := eventv1.NewEventServiceClient(eventsConn)
	controllerCli := controllerv1.NewControllerServiceClient(controllerConn)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Seed initial state before subscribing to events.
		seedNodeState(ctx, nodeName, controllerCli, bc)

		stream, streamErr := eventCli.Subscribe(ctx, &eventv1.SubscribeRequest{})
		if streamErr != nil {
			slog.Debug("Failed to subscribe to node events, retrying", "node", nodeName, "error", streamErr)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}

		slog.Info("Subscribed to node events", "node", nodeName, "endpoint", eventsEndpoint)

		for {
			resp, recvErr := stream.Recv()
			if recvErr != nil {
				if recvErr != io.EOF {
					slog.Warn("Node event stream lost, reconnecting", "node", nodeName, "error", recvErr)
				}
				break
			}

			update := resp.GetUpdate()
			if update == nil {
				continue
			}

			bc.Send(&orchestratorv1.Event{
				Node:   nodeName,
				Update: update,
			})
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func seedNodeState(ctx context.Context, nodeName string, cli controllerv1.ControllerServiceClient, bc *orchestrator.Broadcaster) {
	resp, err := cli.Info(ctx, &controllerv1.InfoRequest{})
	if err != nil {
		slog.Debug("Failed to seed node state", "node", nodeName, "error", err)
		return
	}

	for _, info := range resp.Info {
		bc.Send(&orchestratorv1.Event{
			Node: nodeName,
			Update: &eventv1.Update{
				Payload: &eventv1.Update_VmEvent{
					VmEvent: &eventv1.VMEvent{
						Info: info,
						Type: eventv1.VMEvent_EVENT_TYPE_UPDATED,
					},
				},
			},
		})
	}
}
