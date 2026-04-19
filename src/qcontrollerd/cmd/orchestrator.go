package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	fileregistryv1 "github.com/q-controller/qcontroller/src/generated/services/fileregistry/v1"
	orchestratorv1 "github.com/q-controller/qcontroller/src/generated/services/orchestrator/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qcontroller/src/pkg/frontend"
	"github.com/q-controller/qcontroller/src/pkg/grpcutil"
	"github.com/q-controller/qcontroller/src/pkg/images"
	"github.com/q-controller/qcontroller/src/pkg/orchestrator"
	"github.com/q-controller/qcontroller/src/pkg/utils"
	qUtils "github.com/q-controller/qcontroller/src/qcontrollerd/cmd/utils"
	"github.com/spf13/cobra"
	httpSwagger "github.com/swaggo/http-swagger/v2"
	"google.golang.org/protobuf/proto"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

var orchestratorCmd = &cobra.Command{
	Use:   "orchestrator",
	Short: "Starts the multi-node orchestrator with HTTP gateway",
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath, configPathErr := cmd.Flags().GetString("config")
		if configPathErr != nil {
			return fmt.Errorf("failed to get config: %w", configPathErr)
		}

		config := &settingsv1.OrchestratorConfig{}
		if unmarshalErr := utils.Unmarshal(config, configPath); unmarshalErr != nil {
			return unmarshalErr
		}
		slog.Debug("Read config", "config", config)

		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		// Broadcaster: node subscribers write events, WebSocket clients read them.
		bc := orchestrator.NewBroadcaster(100)
		go bc.Run(ctx)

		// Connect to the orchestrator's own file registry.
		frConn, frErr := grpcutil.Dial(config.FileRegistryEndpoint, grpcutil.WithTLS(config.FileRegistryTls))
		if frErr != nil {
			return fmt.Errorf("failed to connect to file registry: %w", frErr)
		}
		defer func() { _ = frConn.Close() }()

		imageClient, imgErr := images.CreateImageClient(fileregistryv1.NewFileRegistryServiceClient(frConn))
		if imgErr != nil {
			return fmt.Errorf("failed to create image client: %w", imgErr)
		}

		// Create OrchestratorService server.
		orchServer, orchErr := orchestrator.NewServer(config.Nodes, imageClient, bc)
		if orchErr != nil {
			return fmt.Errorf("failed to create orchestrator server: %w", orchErr)
		}
		defer orchServer.Close()

		// Subscribe to each node's EventService.
		for _, n := range config.Nodes {
			go subscribeToNodeEvents(ctx, n, bc)
		}

		// gRPC-gateway: REST API (in-process, no network hop).
		mux := runtime.NewServeMux()
		if err := orchestratorv1.RegisterOrchestratorServiceHandlerServer(ctx, mux, orchServer); err != nil {
			return fmt.Errorf("failed to register orchestrator gateway: %w", err)
		}

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

		httpMux := http.NewServeMux()
		_ = images.CreateHandler(imageClient, httpMux)
		httpMux.HandleFunc("/ws", orchWsHandler(bc))
		httpMux.Handle("/", mux)
		httpMux.HandleFunc("/ui/", frontend.Handler("/ui/"))

		httpServer := &http.Server{
			Addr:    fmt.Sprintf(":%d", config.Port),
			Handler: httpMux,
		}

		go func() {
			<-ctx.Done()
			_ = httpServer.Close()
		}()

		slog.Info("Orchestrator listening", "port", config.Port, "nodes", len(config.Nodes))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(orchestratorCmd)

	orchestratorCmd.Flags().StringP("config", "c", "", "Path to orchestrator's config file")
	if err := orchestratorCmd.MarkFlagRequired("config"); err != nil {
		panic(fmt.Errorf("failed to mark flag `config` as required: %w", err))
	}
}

func orchWsHandler(bc *orchestrator.Broadcaster) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, connErr := upgrader.Upgrade(w, r, nil)
		if connErr != nil {
			slog.Error("Failed to upgrade WebSocket", "error", connErr)
			return
		}
		defer func() { _ = conn.Close() }()

		// Read initial message (mirrors the gateway protocol).
		_, _, readErr := conn.ReadMessage()
		if readErr != nil {
			slog.Warn("Failed to read initial WebSocket message", "error", readErr)
			return
		}

		sub := bc.Subscribe()
		defer bc.Unsubscribe(sub)

		for {
			select {
			case <-r.Context().Done():
				return
			case event, ok := <-sub.Events():
				if !ok {
					return
				}
				b, err := proto.Marshal(event)
				if err != nil {
					slog.Error("Failed to marshal event", "error", err)
					return
				}
				if err := conn.WriteMessage(websocket.BinaryMessage, b); err != nil {
					slog.Warn("Failed to write WebSocket message", "error", err)
					return
				}
			}
		}
	}
}
