package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	v1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qcontroller/src/pkg/images"
	"github.com/q-controller/qcontroller/src/pkg/utils"
	qUtils "github.com/q-controller/qcontroller/src/qcontrollerd/cmd/utils"
	"github.com/spf13/cobra"
	httpSwagger "github.com/swaggo/http-swagger/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func createFileRegistryClient(endpoint string) (v1.FileRegistryServiceClient, error) {
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("did not connect: %w", err)
	}

	return v1.NewFileRegistryServiceClient(conn), nil
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

		imageHandler, imageHandlerErr := images.CreateHandler(imageClient)
		if imageHandlerErr != nil {
			return fmt.Errorf("failed to create image handler: %w", imageHandlerErr)
		}

		if err := mux.HandlePath("POST", qUtils.PathPrefix, imageHandler.Post); err != nil {
			slog.Warn("Adding endpoint to upload VM images failed", "error", err)
		}

		if err := mux.HandlePath("DELETE", qUtils.PathPrefix+"/{imageId}", imageHandler.Delete); err != nil {
			slog.Warn("Adding endpoint to delete VM images failed", "error", err)
		}

		if err := mux.HandlePath("GET", qUtils.PathPrefix, imageHandler.Get); err != nil {
			slog.Warn("Adding endpoint to list VM images failed", "error", err)
		}

		opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
		err := v1.RegisterControllerServiceHandlerFromEndpoint(ctx, mux, config.ControllerEndpoint, opts)
		if err != nil {
			return err
		}

		httpMux := http.NewServeMux()
		httpMux.Handle("/", mux)

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
