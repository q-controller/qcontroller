package protos

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	servicesv1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	"github.com/q-controller/qcontroller/src/pkg/images/storage"
	"github.com/q-controller/qcontroller/src/pkg/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const chunkSize = 1024 * 64 // 64 KB

type FileRegistry struct {
	servicesv1.UnimplementedFileRegistryServiceServer
	tempDir string
	storage storage.StorageBackend
	mu      sync.RWMutex
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
			defer tmpFile.Close()

			if err := f.storage.Store(fileID, tmpFile); err != nil {
				return status.Errorf(codes.Internal, "failed to store file: %v", err)
			}

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
	// Check if image exists in storage
	exists, existsErr := f.storage.Exists(req.ImageId)
	if existsErr != nil {
		return status.Errorf(codes.Internal, "failed to check image existence: %v", existsErr)
	}

	if !exists {
		// If not found and looks like HTTP URL, try downloading
		if utils.IsHTTP(req.ImageId) {
			return f.downloadAndStoreImage(req.ImageId, stream)
		}
		return status.Errorf(codes.NotFound, "image not found: %s", req.ImageId)
	}

	// Retrieve file from storage
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

		resp := &servicesv1.DownloadImageResponse{
			Chunk: buffer[:n],
		}

		if sendErr := stream.Send(resp); sendErr != nil {
			return status.Errorf(codes.Internal, "failed to send chunk: %v", sendErr)
		}
	}

	return nil
}

func (f *FileRegistry) downloadAndStoreImage(imageURL string, stream grpc.ServerStreamingServer[servicesv1.DownloadImageResponse]) error {
	// Create a temporary file for downloading
	tmpFile, err := os.CreateTemp(f.tempDir, "download-*")
	if err != nil {
		return status.Errorf(codes.Internal, "failed to create temp file: %v", err)
	}
	defer func() {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
	}()

	// Download the image
	if err := utils.DownloadFile(imageURL, tmpFile.Name()); err != nil {
		return status.Errorf(codes.Internal, "failed to download image: %v", err)
	}

	// Store using storage backend (it handles hashing internally)
	tmpFile.Seek(0, 0) // Reset to beginning
	if err := f.storage.Store(imageURL, tmpFile); err != nil {
		return status.Errorf(codes.Internal, "failed to store downloaded image: %v", err)
	}

	// Now stream the file back
	tmpFile.Seek(0, 0) // Reset to beginning
	buffer := make([]byte, chunkSize)
	for {
		n, err := tmpFile.Read(buffer)
		if err != nil {
			if err == io.EOF {
				break
			}
			return status.Errorf(codes.Internal, "error reading downloaded file: %v", err)
		}

		resp := &servicesv1.DownloadImageResponse{
			Chunk: buffer[:n],
		}

		if sendErr := stream.Send(resp); sendErr != nil {
			return status.Errorf(codes.Internal, "failed to send chunk: %v", sendErr)
		}
	}

	return nil
}

func (f *FileRegistry) RemoveImage(ctx context.Context, req *servicesv1.RemoveImageRequest) (*servicesv1.RemoveImageResponse, error) {
	err := f.storage.Remove(req.ImageId)
	if err != nil {
		slog.Warn("failed to remove image", "image_id", req.ImageId, "error", err)
		return &servicesv1.RemoveImageResponse{
			Removed: false,
		}, nil
	}

	return &servicesv1.RemoveImageResponse{
		Removed: true,
	}, nil
}

func (f *FileRegistry) ListImages(ctx context.Context, req *servicesv1.ListImagesRequest) (*servicesv1.ListImagesResponse, error) {
	imageIDs, err := f.storage.List()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get image list: %v", err)
	}

	return &servicesv1.ListImagesResponse{
		Images: imageIDs,
	}, nil
}

func NewFileRegistry(root string) (servicesv1.FileRegistryServiceServer, error) {
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

	return &FileRegistry{
		tempDir: tempDir,
		storage: storageBackend,
	}, nil
}
