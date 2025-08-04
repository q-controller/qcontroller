package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	v1 "github.com/krjakbrjak/qcontroller/src/generated/services/v1"
	settingsv1 "github.com/krjakbrjak/qcontroller/src/generated/settings/v1"
	"github.com/krjakbrjak/qcontroller/src/pkg/utils"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

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
