package images

import (
	"context"
	"mime/multipart"

	v1 "github.com/q-controller/qcontroller/src/generated/services/v1"
)

type ImageClient interface {
	Upload(ctx context.Context, name string, file multipart.File) error
	Download(ctx context.Context, id, path string) (retErr error)
	Remove(ctx context.Context, id string) error
	List(ctx context.Context) ([]*v1.VMImage, error)
}
