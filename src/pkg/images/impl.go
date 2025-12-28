package images

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"

	v1 "github.com/q-controller/qcontroller/src/generated/services/v1"
)

type imageClientImpl struct {
	cli v1.FileRegistryServiceClient
}

func (h *imageClientImpl) Upload(ctx context.Context, name string, file multipart.File) error {
	stream, streamErr := h.cli.UploadImage(ctx)
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

func (h *imageClientImpl) Download(ctx context.Context, id, path string) (retErr error) {
	file, fileErr := os.Create(path)
	if fileErr != nil {
		return fileErr
	}

	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			if retErr == nil {
				// Return close error if no other error
				retErr = closeErr
			} else {
				retErr = errors.Join(retErr, closeErr)
			}
		}
	}()

	client, clientErr := h.cli.DownloadImage(ctx, &v1.DownloadImageRequest{
		ImageId: id,
	})
	if clientErr != nil {
		return clientErr
	}

	for {
		resp, recvErr := client.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				break
			}
			return recvErr
		}

		if _, writeErr := file.Write(resp.Chunk); writeErr != nil {
			return writeErr
		}
	}

	return nil
}

func (h *imageClientImpl) Remove(ctx context.Context, id string) error {
	_, err := h.cli.RemoveImage(ctx, &v1.RemoveImageRequest{
		ImageId: id,
	})
	return err
}

func (h *imageClientImpl) List(ctx context.Context) ([]string, error) {
	resp, err := h.cli.ListImages(ctx, &v1.ListImagesRequest{})
	if err != nil {
		return nil, err
	}
	return resp.Images, nil
}

func CreateImageClient(cli v1.FileRegistryServiceClient) (ImageClient, error) {
	return &imageClientImpl{
		cli: cli,
	}, nil
}
