package protos

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	servicesv1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	"github.com/q-controller/qcontroller/src/pkg/events"
	"github.com/q-controller/qcontroller/src/pkg/images/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const chunkSize = 1024 * 1024 // 1 MB

type FileRegistry struct {
	servicesv1.UnimplementedFileRegistryServiceServer
	tempDir          string
	storage          storage.StorageBackend
	eventPublisher   *events.Publisher
	upstreamEndpoint string
}

func (f *FileRegistry) UploadImage(stream grpc.ClientStreamingServer[servicesv1.UploadImageRequest, servicesv1.UploadImageResponse]) error {
	var fileID string
	var file *os.File

	tmpFilePath := ""
	defer func() {
		if tmpFilePath != "" {
			if rmErr := os.Remove(tmpFilePath); rmErr != nil {
				slog.Error("failed to cleanup", "error", rmErr)
			}
		}
	}()

	for {
		req, err := stream.Recv()
		if err == io.EOF {
			if file == nil {
				return status.Error(codes.InvalidArgument, "no data received")
			}

			// Finalize file
			if closeErr := file.Close(); closeErr != nil {
				return status.Errorf(codes.Internal, "failed to close file: %v", closeErr)
			}

			tmpFile, reopenErr := os.Open(tmpFilePath)
			if reopenErr != nil {
				return status.Errorf(codes.Internal, "failed to reopen temp file: %v", reopenErr)
			}
			defer func() { _ = tmpFile.Close() }()

			if err := f.storage.Store(fileID, tmpFile); err != nil {
				return status.Errorf(codes.Internal, "failed to store file: %v", err)
			}

			defer func() {
				if err := f.eventPublisher.PublishImageUpdate(&servicesv1.VMImage{
					ImageId: fileID,
				}, servicesv1.ImageEvent_EVENT_TYPE_UPLOADED); err != nil {
					slog.Error("failed to publish image uploaded event", "image_id", fileID, "error", err)
				}
			}()

			return stream.SendAndClose(&servicesv1.UploadImageResponse{
				ImageId: fileID,
			})
		}
		if err != nil {
			return err
		}

		// First chunk: extract metadata and create temp file
		if file == nil {
			if req.ImageId == "" {
				return status.Error(codes.InvalidArgument, "image_id must be provided in the first chunk")
			}
			fileID = req.ImageId

			tmpFile, tmpFileErr := os.OpenFile(filepath.Join(f.tempDir, fileID), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
			if tmpFileErr != nil {
				if os.IsExist(tmpFileErr) {
					return status.Errorf(codes.AlreadyExists, "Image with the same id is currently being uploaded")
				}
				return status.Errorf(codes.Internal, "failed to create temp file: %v", tmpFileErr)
			}
			file = tmpFile
			tmpFilePath = tmpFile.Name()
		}

		// Write to file
		if _, err := file.Write(req.Chunk); err != nil {
			return status.Errorf(codes.Internal, "failed to write to temp file: %v", err)
		}
	}
}

func (f *FileRegistry) DownloadImage(req *servicesv1.DownloadImageRequest, stream grpc.ServerStreamingServer[servicesv1.DownloadImageResponse]) error {
	exists, existsErr := f.storage.Exists(req.ImageId)
	if existsErr != nil {
		return status.Errorf(codes.Internal, "failed to check image existence: %v", existsErr)
	}

	if !exists {
		if f.upstreamEndpoint == "" {
			return status.Errorf(codes.NotFound, "image not found: %s", req.ImageId)
		}
		if err := f.fetchFromUpstream(stream.Context(), req.ImageId); err != nil {
			return status.Errorf(codes.Internal, "failed to fetch image from upstream: %v", err)
		}
	}

	file, err := f.storage.Retrieve(req.ImageId)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to retrieve image: %v", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			slog.Error("failed to close file", "error", closeErr)
		}
	}()

	buffer := make([]byte, chunkSize)
	for {
		n, err := file.Read(buffer)
		if err != nil {
			if err == io.EOF {
				break
			}
			return status.Errorf(codes.Internal, "error reading file: %v", err)
		}

		if sendErr := stream.Send(&servicesv1.DownloadImageResponse{Chunk: buffer[:n]}); sendErr != nil {
			return status.Errorf(codes.Internal, "failed to send chunk: %v", sendErr)
		}
	}

	return nil
}

func (f *FileRegistry) fetchFromUpstream(ctx context.Context, imageId string) error {
	slog.Info("Fetching image from upstream", "image", imageId, "upstream", f.upstreamEndpoint)

	conn, err := grpc.NewClient(f.upstreamEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial upstream: %w", err)
	}
	defer conn.Close()

	downloadStream, err := servicesv1.NewFileRegistryServiceClient(conn).DownloadImage(ctx, &servicesv1.DownloadImageRequest{ImageId: imageId})
	if err != nil {
		return fmt.Errorf("download from upstream: %w", err)
	}

	tmpFile, err := os.CreateTemp(f.tempDir, imageId+"-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	for {
		chunk, recvErr := downloadStream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			tmpFile.Close()
			return fmt.Errorf("recv chunk: %w", recvErr)
		}
		if _, writeErr := tmpFile.Write(chunk.Chunk); writeErr != nil {
			tmpFile.Close()
			return fmt.Errorf("write chunk: %w", writeErr)
		}
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	reopened, err := os.Open(tmpPath)
	if err != nil {
		return fmt.Errorf("reopen temp file: %w", err)
	}
	defer reopened.Close()

	if err := f.storage.Store(imageId, reopened); err != nil {
		return fmt.Errorf("store image: %w", err)
	}

	slog.Info("Image fetched from upstream", "image", imageId)
	return nil
}

func (f *FileRegistry) RemoveImage(ctx context.Context, req *servicesv1.RemoveImageRequest) (*servicesv1.RemoveImageResponse, error) {
	removeErr := f.storage.Remove(req.ImageId)
	if removeErr != nil {
		slog.Warn("failed to remove image", "image_id", req.ImageId, "error", removeErr)
		return &servicesv1.RemoveImageResponse{
			Removed: false,
		}, nil
	}

	defer func() {
		if err := f.eventPublisher.PublishImageUpdate(&servicesv1.VMImage{
			ImageId: req.ImageId,
		}, servicesv1.ImageEvent_EVENT_TYPE_REMOVED); err != nil {
			slog.Error("failed to publish image removal event", "image_id", req.ImageId, "error", err)
		}
	}()

	return &servicesv1.RemoveImageResponse{
		Removed: true,
	}, nil
}

func (f *FileRegistry) ListImages(ctx context.Context, req *servicesv1.ListImagesRequest) (*servicesv1.ListImagesResponse, error) {
	imageIDs, err := f.storage.List()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get image list: %v", err)
	}

	resp := &servicesv1.ListImagesResponse{
		Images: []*servicesv1.VMImage{},
	}
	for _, id := range imageIDs {
		metadata, metadataErr := f.storage.GetMetadata(id)
		if metadataErr != nil {
			slog.Warn("failed to get image metadata", "image_id", id, "error", metadataErr)
			continue
		}

		resp.Images = append(resp.Images, &servicesv1.VMImage{
			ImageId:    metadata.ImageID,
			Hash:       metadata.Hash,
			Size:       metadata.Size,
			UploadedAt: metadata.UploadedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	return resp, nil
}

func NewFileRegistry(root, eventsEndpoint, upstreamEndpoint string) (servicesv1.FileRegistryServiceServer, error) {
	// Create temp directory
	tempDir, pathErr := os.MkdirTemp("", "fileregistry-*")
	if pathErr != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", pathErr)
	}

	// Create storage backend
	storageDir := filepath.Join(root, "storage")
	storageBackend, storageErr := storage.NewLocalFilesystemBackend(storageDir)
	if storageErr != nil {
		return nil, fmt.Errorf("failed to create storage backend: %w", storageErr)
	}

	eventPublisher, eventPublisherErr := events.NewEventPublisher(context.Background(), eventsEndpoint)
	if eventPublisherErr != nil {
		return nil, fmt.Errorf("failed to create event publisher: %w", eventPublisherErr)
	}

	return &FileRegistry{
		tempDir:          tempDir,
		storage:          storageBackend,
		eventPublisher:   eventPublisher,
		upstreamEndpoint: upstreamEndpoint,
	}, nil
}
