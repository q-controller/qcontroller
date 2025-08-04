package protos

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	servicesv1 "github.com/krjakbrjak/qcontroller/src/generated/services/v1"
	"github.com/krjakbrjak/qcontroller/src/pkg/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const chunkSize = 1024 * 64 // 64 KB

type FileRegistry struct {
	servicesv1.UnimplementedFileRegistryServiceServer
	root    string
	tempDir string
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

			finalPath := fmt.Sprintf("%s/%s", f.root, utils.Hash(fileID))
			if err := utils.CopyFile(tmpFilePath, finalPath); err != nil {
				return err
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

func NewFileRegistry(root string) (servicesv1.FileRegistryServiceServer, error) {
	path, pathErr := os.MkdirTemp("", "fileregistry-*")
	if pathErr != nil {
		return nil, pathErr
	}

	if err := os.MkdirAll(root, 0777); err != nil {
		return nil, err
	}

	return &FileRegistry{root: root, tempDir: path}, nil
}
