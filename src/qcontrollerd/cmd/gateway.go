package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	v1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qcontroller/src/pkg/frontend"
	"github.com/q-controller/qcontroller/src/pkg/images"
	"github.com/q-controller/qcontroller/src/pkg/utils"
	qUtils "github.com/q-controller/qcontroller/src/qcontrollerd/cmd/utils"
	"github.com/spf13/cobra"
	httpSwagger "github.com/swaggo/http-swagger/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

func createFileRegistryClient(endpoint string) (v1.FileRegistryServiceClient, error) {
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("did not connect: %w", err)
	}

	return v1.NewFileRegistryServiceClient(conn), nil
}

func createEventClient(endpoint string) (v1.EventServiceClient, error) {
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("did not connect: %w", err)
	}

	return v1.NewEventServiceClient(conn), nil
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func wsHandler(grpcClient v1.EventServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, connErr := upgrader.Upgrade(w, r, nil)
		if connErr != nil {
			slog.Error("Failed to upgrade connection", "error", connErr)
			return
		}
		defer func() {
			_ = conn.Close()
		}()

		msgType, data, readErr := conn.ReadMessage()
		if readErr != nil || msgType != websocket.BinaryMessage {
			slog.Warn("Invalid WebSocket message", "error", readErr, "type", msgType)
			return
		}

		var req v1.SubscribeRequest
		if err := proto.Unmarshal(data, &req); err != nil {
			slog.Warn("Failed to unmarshal subscribe request", "error", err)
			return
		}

		// Create context with timeout
		ctx, cancel := context.WithTimeout(r.Context(), time.Hour)
		defer cancel()

		stream, err := grpcClient.Subscribe(ctx, &req)
		if err != nil {
			slog.Error("Failed to create subscription", "error", err)
			return
		}

		// Forward stream → websocket
		for {
			update, err := stream.Recv()
			if err != nil {
				if err != io.EOF {
					slog.Warn("Stream receive error", "error", err)
				}
				return
			}

			b, err := proto.Marshal(update)
			if err != nil {
				slog.Error("Failed to marshal update", "error", err)
				return
			}

			if err := conn.WriteMessage(websocket.BinaryMessage, b); err != nil {
				slog.Warn("Failed to write WebSocket message", "error", err)
				return
			}
		}
	}
}

var gwCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Starts HTTP gateway",
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath, configPathErr := cmd.Flags().GetString("config")
		if configPathErr != nil {
			return fmt.Errorf("failed to get config: %w", configPathErr)
		}

		config := &settingsv1.GatewayConfig{}
		if unmarshalErr := utils.Unmarshal(config, configPath); unmarshalErr != nil {
			return unmarshalErr
		}
		slog.Debug("Read config", "config", config)

		ctx := context.Background()
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		mux := runtime.NewServeMux()

		if config.ExposeSwaggerUi {
			if specsErr := mux.HandlePath("GET", "/openapi.yaml", func(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
				openAPISpecs, openAPISpecsErr := qUtils.GenerateOpenAPISpecs()
				if openAPISpecsErr != nil {
					slog.Warn("Failed to generate OpenAPI specs", "error", openAPISpecsErr)
					http.Error(w, "Failed to generate OpenAPI specs", http.StatusInternalServerError)
					return
				}
				if _, err := w.Write([]byte(openAPISpecs)); err != nil {
					slog.Warn("Failed to write response", "error", err)
				}
			}); specsErr == nil {
				if swaggerErr := mux.HandlePath("GET", "/v1/swagger/*", func(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
					httpSwagger.Handler(
						httpSwagger.URL("/openapi.yaml"),
						httpSwagger.Layout("BaseLayout"),
						httpSwagger.DefaultModelsExpandDepth(httpSwagger.HideModel),
					)(w, r)
				}); swaggerErr != nil {
					slog.Warn("Failed to register swagger endpoint", "error", swaggerErr)
				}
			} else {
				slog.Warn("Failed to register specs endpoint", "error", specsErr)
			}
		}

		fileRegistryClient, clientErr := createFileRegistryClient(config.ControllerEndpoint)
		if clientErr != nil {
			return fmt.Errorf("failed to create file registry client: %w", clientErr)
		}

		imageClient, imageClientErr := images.CreateImageClient(fileRegistryClient)
		if imageClientErr != nil {
			return fmt.Errorf("failed to create image client: %w", imageClientErr)
		}

		grpcEventClient, grpcEventClientErr := createEventClient(config.ControllerEndpoint)
		if grpcEventClientErr != nil {
			return fmt.Errorf("failed to create event client: %w", grpcEventClientErr)
		}

		opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
		err := v1.RegisterControllerServiceHandlerFromEndpoint(ctx, mux, config.ControllerEndpoint, opts)
		if err != nil {
			return err
		}

		httpMux := http.NewServeMux()
		_ = images.CreateHandler(imageClient, httpMux)
		httpMux.HandleFunc("/ws", wsHandler(grpcEventClient))

		// Mount gRPC gateway at root for all API endpoints
		httpMux.Handle("/", mux)

		// Serve frontend at specific path
		httpMux.HandleFunc("/ui/", frontend.Handler("/ui/"))

		return http.ListenAndServe(fmt.Sprintf(":%d", config.Port), httpMux)
	},
}

func init() {
	rootCmd.AddCommand(gwCmd)

	gwCmd.Flags().StringP("config", "c", "", "Path to gateway's config file")
	if err := gwCmd.MarkFlagRequired("config"); err != nil {
		panic(fmt.Errorf("failed to mark flag `config` as required: %w", err))
	}
}
