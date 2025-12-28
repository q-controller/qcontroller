package storage

import (
	"io"
	"time"
)

// StorageBackend abstracts the underlying storage mechanism
type StorageBackend interface {
	Store(imageID string, data io.Reader) error
	Retrieve(imageID string) (io.ReadCloser, error)
	Remove(imageID string) error
	Exists(imageID string) (bool, error)
	GetMetadata(imageID string) (*ImageMetadata, error)
	List() ([]string, error)
}

type ImageMetadata struct {
	ImageID    string    `json:"image_id"`
	Hash       string    `json:"hash"`
	Size       int64     `json:"size"`
	UploadedAt time.Time `json:"uploaded_at"`
}
