package images

import (
	"context"
	"mime/multipart"

	imageservice "github.com/q-controller/qcontroller/src/generated/oapi"
)

type ImageClient interface {
	Upload(ctx context.Context, name string, file multipart.File) error
	Download(ctx context.Context, id, path string) (retErr error)
	Remove(ctx context.Context, id string) error
	List(ctx context.Context) ([]*imageservice.ImageInfo, error)
}
