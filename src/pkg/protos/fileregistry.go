package protos

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	servicesv1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	"github.com/q-controller/qcontroller/src/pkg/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const chunkSize = 1024 * 64 // 64 KB

type FileRegistry struct {
	servicesv1.UnimplementedFileRegistryServiceServer
	root         string
	tempDir      string
	metadataFile string
	mu           sync.RWMutex
}

type ImageMetadata struct {
	ImageID string `json:"image_id"`
	Hash    string `json:"hash"`
}

func (f *FileRegistry) UploadImage(stream grpc.ClientStreamingServer[servicesv1.UploadImageRequest, servicesv1.UploadImageResponse]) error {
	var fileID string
	var file *os.File
	hasher := sha256.New()

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

			// Finalize and verify hash
			if closeErr := file.Close(); closeErr != nil {
				return status.Errorf(codes.Internal, "failed to close file: %v", closeErr)
			}

			hash := utils.Hash(fileID)
			finalPath := fmt.Sprintf("%s/%s", f.root, hash)
			if err := utils.CopyFile(tmpFilePath, finalPath); err != nil {
				return err
			}

			// Save metadata mapping
			if err := f.saveImageMetadata(fileID, hash); err != nil {
				// Cleanup the file if metadata save fails
				if rmErr := os.Remove(finalPath); rmErr != nil {
					slog.Error("failed to cleanup file after metadata save failure", "error", rmErr)
				}
				return status.Errorf(codes.Internal, "failed to save metadata: %v", err)
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
					return status.Errorf(codes.Internal, "Image with the same id is currently being uploaded")
				}
				return status.Errorf(codes.Internal, "failed to create temp file: %v", err)
			}
			file = tmpFile
			tmpFilePath = tmpFile.Name()
		}

		// Write to file and hash
		if _, err := file.Write(req.Chunk); err != nil {
			return status.Errorf(codes.Internal, "failed to write to temp file: %v", err)
		}
		if _, err := hasher.Write(req.Chunk); err != nil {
			return status.Errorf(codes.Internal, "failed to hash chunk: %v", err)
		}
	}
}

func (f *FileRegistry) DownloadImage(req *servicesv1.DownloadImageRequest, stream grpc.ServerStreamingServer[servicesv1.DownloadImageResponse]) error {
	imagePath := fmt.Sprintf("%s/%s", f.root, utils.Hash(req.ImageId))
	_, statErr := os.Stat(imagePath)
	if os.IsNotExist(statErr) && utils.IsHTTP(req.ImageId) {
		if downloadErr := utils.DownloadFile(req.ImageId, imagePath); downloadErr != nil {
			return downloadErr
		}
	} else if statErr != nil {
		return statErr
	}

	file, err := os.Open(imagePath)
	if err != nil {
		return fmt.Errorf("failed to open image %q: %w", req.ImageId, err)
	}

	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			slog.Error("failed to close file", "error", err)
		}
	}()

	buffer := make([]byte, chunkSize)
	for {
		n, err := file.Read(buffer)
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("error reading file: %w", err)
		}

		resp := &servicesv1.DownloadImageResponse{
			Chunk: buffer[:n],
		}

		if sendErr := stream.Send(resp); sendErr != nil {
			return fmt.Errorf("failed to send chunk: %w", sendErr)
		}
	}

	return nil
}

func (f *FileRegistry) RemoveImage(ctx context.Context, req *servicesv1.RemoveImageRequest) (*servicesv1.RemoveImageResponse, error) {
	hash, exists := f.getImageHash(req.ImageId)
	if !exists {
		return &servicesv1.RemoveImageResponse{
			Removed: false,
		}, nil
	}

	imagePath := fmt.Sprintf("%s/%s", f.root, hash)
	if err := os.Remove(imagePath); err != nil {
		if os.IsNotExist(err) {
			return &servicesv1.RemoveImageResponse{
				Removed: false,
			}, nil
		}
		return nil, fmt.Errorf("failed to remove image %q: %w", req.ImageId, err)
	}

	// Remove from metadata
	if err := f.removeImageMetadata(req.ImageId); err != nil {
		slog.Warn("failed to remove metadata for image", "image_id", req.ImageId, "error", err)
	}

	return &servicesv1.RemoveImageResponse{
		Removed: true,
	}, nil
}

func (f *FileRegistry) ListImages(ctx context.Context, req *servicesv1.ListImagesRequest) (*servicesv1.ListImagesResponse, error) {
	imageIDs, err := f.getAllImageIDs()
	if err != nil {
		return nil, fmt.Errorf("failed to get image list: %w", err)
	}

	return &servicesv1.ListImagesResponse{
		Images: imageIDs,
	}, nil
}

func NewFileRegistry(root string) (servicesv1.FileRegistryServiceServer, error) {
	path, pathErr := os.MkdirTemp("", "fileregistry-*")
	if pathErr != nil {
		return nil, pathErr
	}

	if err := os.MkdirAll(root, 0777); err != nil {
		return nil, err
	}

	metadataFile := filepath.Join(root, "metadata.json")
	return &FileRegistry{
		root:         root,
		tempDir:      path,
		metadataFile: metadataFile,
	}, nil
}

// Helper methods for metadata management

func (f *FileRegistry) loadMetadata() (map[string]string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	data, err := os.ReadFile(f.metadataFile)
	if os.IsNotExist(err) {
		return make(map[string]string), nil
	}
	if err != nil {
		return nil, err
	}

	var metadata []ImageMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, err
	}

	mapping := make(map[string]string)
	for _, item := range metadata {
		mapping[item.ImageID] = item.Hash
	}
	return mapping, nil
}

func (f *FileRegistry) saveMetadata(mapping map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	metadata := make([]ImageMetadata, 0, len(mapping))
	for imageID, hash := range mapping {
		metadata = append(metadata, ImageMetadata{
			ImageID: imageID,
			Hash:    hash,
		})
	}

	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(f.metadataFile, data, 0644)
}

func (f *FileRegistry) saveImageMetadata(imageID, hash string) error {
	mapping, err := f.loadMetadata()
	if err != nil {
		return err
	}

	mapping[imageID] = hash
	return f.saveMetadata(mapping)
}

func (f *FileRegistry) removeImageMetadata(imageID string) error {
	mapping, err := f.loadMetadata()
	if err != nil {
		return err
	}

	delete(mapping, imageID)
	return f.saveMetadata(mapping)
}

func (f *FileRegistry) getImageHash(imageID string) (string, bool) {
	mapping, err := f.loadMetadata()
	if err != nil {
		return "", false
	}

	hash, exists := mapping[imageID]
	return hash, exists
}

func (f *FileRegistry) getAllImageIDs() ([]string, error) {
	mapping, err := f.loadMetadata()
	if err != nil {
		return nil, err
	}

	imageIDs := make([]string, 0, len(mapping))
	for imageID := range mapping {
		imageIDs = append(imageIDs, imageID)
	}
	return imageIDs, nil
}
