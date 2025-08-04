package cmd

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"log"
	"log/slog"
	"mime/multipart"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	v1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qcontroller/src/pkg/utils"
	"github.com/spf13/cobra"
	httpSwagger "github.com/swaggo/http-swagger/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gopkg.in/yaml.v3"
)

//go:embed docs/openapi.yaml
var openAPISpecs string

type uploader struct {
	cli v1.FileRegistryServiceClient
}

func (u *uploader) Upload(name string, file multipart.File) error {
	stream, streamErr := u.cli.UploadImage(context.Background())
	if streamErr != nil {
		return streamErr
	}

	const chunkSize = 64 * 1024 // 4KB chunks
	var totalBytes int64
	buffer := make([]byte, chunkSize)
	for {
		n, err := file.Read(buffer)
		if err != nil && err != io.EOF {
			return fmt.Errorf("failed to read file: %v", err)
		}
		if n == 0 {
			// No more data to read
			break
		}

		totalBytes += int64(n)
		if sendErr := stream.Send(&v1.UploadImageRequest{
			ImageId: name,
			Chunk:   buffer[:n],
		}); sendErr != nil {
			return sendErr
		}
	}

	_, respErr := stream.CloseAndRecv()
	if respErr != nil {
		return respErr
	}
	return nil
}

func createUploader(endpoint string) (*uploader, error) {
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("did not connect: %w", err)
	}

	return &uploader{
		cli: v1.NewFileRegistryServiceClient(conn),
	}, nil
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

		uploader, uploaderErr := createUploader(config.ControllerEndpoint)
		if uploaderErr != nil {
			return uploaderErr
		}

		ctx := context.Background()
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		mux := runtime.NewServeMux()

		if config.ExposeSwaggerUi {
			if specsErr := mux.HandlePath("GET", "/openapi.yaml", func(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
				var spec map[string]interface{}
				if err := yaml.Unmarshal([]byte(openAPISpecs), &spec); err != nil {
					log.Fatalf("Failed to parse OpenAPI spec: %v", err)
				}

				tag := "ImageService"
				if existingTags, ok := spec["tags"].([]string); ok {
					// Avoid duplicates
					found := false
					for _, t := range existingTags {
						if t == tag {
							found = true
							break
						}
					}
					if !found {
						spec["tags"] = append(existingTags, tag)
					}
				} else {
					// Create new tags array
					spec["tags"] = []string{tag}
				}

				newPath := map[string]interface{}{
					"post": map[string]interface{}{
						"tags":        []string{tag},
						"summary":     "Upload image",
						"description": "Uploads an image file with an associated ID",
						"requestBody": map[string]interface{}{
							"required": true,
							"content": map[string]interface{}{
								"multipart/form-data": map[string]interface{}{
									"schema": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"id": map[string]interface{}{
												"type": "string",
											},
											"file": map[string]interface{}{
												"type":   "string",
												"format": "binary",
											},
										},
										"required": []string{"id", "file"},
									},
								},
							},
						},
						"responses": map[string]interface{}{
							"200": map[string]interface{}{
								"description": "Upload successful",
							},
							"400": map[string]interface{}{
								"description": "Bad request",
							},
							"500": map[string]interface{}{
								"description": "Internal server error",
							},
						},
					},
				}

				if paths, ok := spec["paths"].(map[string]interface{}); ok {
					paths["/v1/images"] = newPath
				}
				if bytes, err := yaml.Marshal(spec); err != nil {
					if _, err := w.Write([]byte(openAPISpecs)); err != nil {
						slog.Warn("Failed to write response", "error", err)
					}
				} else {
					if _, err := w.Write(bytes); err != nil {
						slog.Warn("Failed to write response", "error", err)
					}
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

		if err := mux.HandlePath("POST", "/v1/images", func(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
			if r.Method != http.MethodPost {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}

			err := r.ParseMultipartForm(10 << 20)
			if err != nil {
				http.Error(w, fmt.Sprintf("failed to parse form: %s", err.Error()), http.StatusBadRequest)
				return
			}

			id := r.FormValue("id")
			if id == "" {
				http.Error(w, "Missing id parameter", http.StatusBadRequest)
				return
			}

			file, _, err := r.FormFile("file")
			if err != nil {
				http.Error(w, "Failed to retrieve file: "+err.Error(), http.StatusBadRequest)
				return
			}

			defer func() {
				if err := file.Close(); err != nil {
					slog.Warn("Failed to close file", "error", err)
				}
			}()

			if uploadErr := uploader.Upload(id, file); uploadErr != nil {
				http.Error(w, "Failed to upload file: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}); err != nil {
			slog.Warn("Adding endpoint to upload VM images failed", "error", err)
		}

		opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
		err := v1.RegisterControllerServiceHandlerFromEndpoint(ctx, mux, config.ControllerEndpoint, opts)
		if err != nil {
			return err
		}

		// Start HTTP server (and proxy calls to gRPC server endpoint)
		return http.ListenAndServe(fmt.Sprintf(":%d", config.Port), mux)
	},
}

func init() {
	rootCmd.AddCommand(gwCmd)

	gwCmd.Flags().StringP("config", "c", "", "Path to gateway's config file")
	if err := gwCmd.MarkFlagRequired("config"); err != nil {
		panic(fmt.Errorf("failed to mark flag `config` as required: %w", err))
	}
}
