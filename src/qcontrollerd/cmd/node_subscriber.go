package cmd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	controllerv1 "github.com/q-controller/qcontroller/src/generated/services/controller/v1"
	eventv1 "github.com/q-controller/qcontroller/src/generated/services/event/v1"
	orchestratorv1 "github.com/q-controller/qcontroller/src/generated/services/orchestrator/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qcontroller/src/pkg/grpcutil"
	"github.com/q-controller/qcontroller/src/pkg/orchestrator"
)

func subscribeToNodeEvents(ctx context.Context, n *settingsv1.Node, bc *orchestrator.Broadcaster) {
	controllerConn, controllerConnErr := grpcutil.Dial(n.Endpoint, grpcutil.WithTLS(n.ControllerTls))
	if controllerConnErr != nil {
		slog.Error("Failed to connect to controller", "node", n.Name, "error", controllerConnErr)
		return
	}
	defer func() { _ = controllerConn.Close() }()

	eventsConn, eventsConnErr := grpcutil.Dial(n.EventsEndpoint, grpcutil.WithTLS(n.EventsTls))
	if eventsConnErr != nil {
		slog.Error("Failed to connect to event service", "node", n.Name, "error", eventsConnErr)
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
		seedNodeState(ctx, n.Name, controllerCli, bc)

		stream, streamErr := eventCli.Subscribe(ctx, &eventv1.SubscribeRequest{})
		if streamErr != nil {
			slog.Debug("Failed to subscribe to node events, retrying", "node", n.Name, "error", streamErr)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}

		slog.Info("Subscribed to node events", "node", n.Name, "endpoint", n.EventsEndpoint)

		for {
			resp, recvErr := stream.Recv()
			if recvErr != nil {
				if !errors.Is(recvErr, io.EOF) {
					slog.Warn("Node event stream lost, reconnecting", "node", n.Name, "error", recvErr)
				}
				break
			}

			update := resp.GetUpdate()
			if update == nil {
				continue
			}

			bc.Send(&orchestratorv1.Event{
				Node:   n.Name,
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
