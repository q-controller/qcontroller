package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	authv1 "github.com/q-controller/qcontroller/src/generated/services/auth/v1"
	fileregistryv1 "github.com/q-controller/qcontroller/src/generated/services/fileregistry/v1"
	orchestratorv1 "github.com/q-controller/qcontroller/src/generated/services/orchestrator/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qcontroller/src/pkg/auth"
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

		// gRPC-gateway: REST API (in-process, no network hop). The
		// ForwardSetCookie option lets handlers (currently AuthService.Logout)
		// set/clear browser cookies via grpc.SendHeader.
		mux := runtime.NewServeMux(runtime.WithForwardResponseOption(auth.ForwardSetCookie))
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
		httpMux.Handle("/", mux)
		httpMux.HandleFunc("/ui/", frontend.Handler("/ui/"))

		var verifiers []auth.Verifier
		var oidcV *auth.OIDCVerifier
		allowedOrigin := ""
		if config.Auth != nil && len(config.Auth.Issuers) > 0 {
			secretPath := filepath.Join(filepath.Dir(configPath), "session.key")
			secret, secretErr := auth.LoadOrCreateSecret(secretPath)
			if secretErr != nil {
				return fmt.Errorf("load session secret: %w", secretErr)
			}
			var oidcErr error
			oidcV, oidcErr = auth.NewOIDCVerifier(ctx, config.Auth, secret)
			if oidcErr != nil {
				return fmt.Errorf("init oidc verifier: %w", oidcErr)
			}
			oidcV.RegisterRoutes(httpMux)
			verifiers = append(verifiers, oidcV)
			// Origin is scheme+host only — strip any path/query from
			// external_url (e.g. behind a reverse proxy at /app) so the
			// WebSocket origin check actually matches.
			if u, err := url.Parse(config.Auth.ExternalUrl); err == nil && u.Scheme != "" && u.Host != "" {
				allowedOrigin = u.Scheme + "://" + u.Host
			}
		}
		// AuthService (GetConfig/GetMe/Logout) goes via the gRPC-gateway, same
		// as OrchestratorService. /auth/login and /auth/callback stay on
		// httpMux above because they're HTTP-redirect handlers, not JSON RPCs.
		if err := authv1.RegisterAuthServiceHandlerServer(ctx, mux, auth.NewAuthServer(oidcV)); err != nil {
			return fmt.Errorf("failed to register auth gateway: %w", err)
		}
		httpMux.HandleFunc("/ws", orchWsHandler(bc, allowedOrigin))
		httpMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ok\n"))
		})
		// Redirect /ui (no trailing slash) to /ui/ so the SPA loads either way.
		httpMux.HandleFunc("/ui", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/ui/", http.StatusMovedPermanently)
		})

		exempt := append(auth.PublicPaths(), "/ui/", "/ui", "/healthz")
		var inner http.Handler = httpMux
		if len(verifiers) > 0 {
			inner = auth.RequireCSRFHeader(inner)
		}
		if oidcV != nil {
			inner = oidcV.Renewal(inner)
		}
		externalURL := ""
		if config.Auth != nil {
			externalURL = config.Auth.ExternalUrl
		}
		handler := auth.SecurityHeaders(externalURL)(auth.Middleware(verifiers, exempt...)(inner))

		httpServer := &http.Server{
			Addr:    fmt.Sprintf(":%d", config.Port),
			Handler: handler,
		}

		go func() {
			<-ctx.Done()
			_ = httpServer.Close()
		}()

		slog.Info("Orchestrator listening", "port", config.Port, "nodes", len(config.Nodes), "tls", config.Tls != nil)
		var servErr error
		if config.Tls != nil {
			servErr = httpServer.ListenAndServeTLS(config.Tls.Cert, config.Tls.Key)
		} else {
			servErr = httpServer.ListenAndServe()
		}
		if servErr != nil && servErr != http.ErrServerClosed {
			return servErr
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

func orchWsHandler(bc *orchestrator.Broadcaster, allowedOrigin string) http.HandlerFunc {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			if allowedOrigin == "" {
				return true
			}
			return r.Header.Get("Origin") == allowedOrigin
		},
	}
	return func(w http.ResponseWriter, r *http.Request) {
		conn, connErr := upgrader.Upgrade(w, r, nil)
		if connErr != nil {
			slog.ErrorContext(r.Context(), "Failed to upgrade WebSocket", "error", connErr)
			return
		}
		defer func() { _ = conn.Close() }()

		// Read initial message (mirrors the gateway protocol).
		_, _, readErr := conn.ReadMessage()
		if readErr != nil {
			slog.WarnContext(r.Context(), "Failed to read initial WebSocket message", "error", readErr)
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
					slog.ErrorContext(r.Context(), "Failed to marshal event", "error", err)
					return
				}
				if err := conn.WriteMessage(websocket.BinaryMessage, b); err != nil {
					slog.WarnContext(r.Context(), "Failed to write WebSocket message", "error", err)
					return
				}
			}
		}
	}
}
