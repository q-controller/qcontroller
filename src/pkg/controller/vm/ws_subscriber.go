package vm

import (
	"context"
	"fmt"
	"net/url"

	"github.com/gorilla/websocket"
	servicesv1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	"google.golang.org/protobuf/proto"
)

// wsSubscriber wraps a gorilla/websocket connection to subscribe to a remote
// gateway's event stream at /ws. It uses the same binary protobuf protocol
// as the gateway's wsHandler: send a SubscribeRequest, then read
// SubscribeResponse messages in a loop.
type wsSubscriber struct {
	conn *websocket.Conn
}

// dialWebSocket connects to the remote gateway's /ws endpoint and sends
// the initial SubscribeRequest. The returned wsSubscriber is ready to
// receive events via Recv.
func dialWebSocket(ctx context.Context, httpEndpoint string) (*wsSubscriber, error) {
	wsURL, err := toWebSocketURL(httpEndpoint, "/ws")
	if err != nil {
		return nil, fmt.Errorf("build ws url: %w", err)
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("ws dial: %w", err)
	}

	// Send SubscribeRequest (empty, mirrors the gateway protocol).
	subReq := &servicesv1.SubscribeRequest{}
	reqBytes, err := proto.Marshal(subReq)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("marshal subscribe request: %w", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, reqBytes); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("send subscribe request: %w", err)
	}

	return &wsSubscriber{conn: conn}, nil
}

// Recv blocks until the next SubscribeResponse is received.
func (s *wsSubscriber) Recv() (*servicesv1.SubscribeResponse, error) {
	for {
		msgType, data, err := s.conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		if msgType != websocket.BinaryMessage {
			continue
		}
		var resp servicesv1.SubscribeResponse
		if err := proto.Unmarshal(data, &resp); err != nil {
			return nil, fmt.Errorf("unmarshal subscribe response: %w", err)
		}
		return &resp, nil
	}
}

// Close closes the underlying WebSocket connection.
func (s *wsSubscriber) Close() error {
	return s.conn.Close()
}

func toWebSocketURL(httpEndpoint, path string) (string, error) {
	u, err := url.Parse(httpEndpoint)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	u.Path = path
	return u.String(), nil
}
