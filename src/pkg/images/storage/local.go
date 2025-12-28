package storage

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/q-controller/qcontroller/src/pkg/utils"
)

// LocalFilesystemBackend implements StorageBackend for local filesystem
type LocalFilesystemBackend struct {
	root string
	db   *badger.DB
	mu   sync.RWMutex
}

func NewLocalFilesystemBackend(root string) (*LocalFilesystemBackend, error) {
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}

	dbPath := filepath.Join(root, "images_badger")
	opts := badger.DefaultOptions(dbPath)
	opts.Logger = nil // Disable badger logging
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	return &LocalFilesystemBackend{
		root: root,
		db:   db,
	}, nil
}

func (b *LocalFilesystemBackend) Store(imageID string, data io.Reader) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	hash := utils.Hash(imageID)
	filePath := filepath.Join(b.root, hash)

	file, err := os.Create(filepath.Clean(filePath))
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	size, err := io.Copy(file, data)
	if err != nil {
		return fmt.Errorf("failed to write data: %w", err)
	}

	metadata := &ImageMetadata{
		ImageID:    imageID,
		Hash:       hash,
		Size:       size,
		UploadedAt: time.Now(),
	}

	return b.db.Update(func(txn *badger.Txn) error {
		data, err := json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}
		return txn.Set([]byte(imageID), data)
	})
}

func (b *LocalFilesystemBackend) Retrieve(imageID string) (io.ReadCloser, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// Get metadata to find the file
	var metadata ImageMetadata
	err := b.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(imageID))
		if err != nil {
			if err == badger.ErrKeyNotFound {
				return fmt.Errorf("image not found: %s", imageID)
			}
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &metadata)
		})
	})
	if err != nil {
		return nil, err
	}

	filePath := filepath.Join(b.root, metadata.Hash)
	file, err := os.Open(filepath.Clean(filePath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("image file not found: %s", imageID)
		}
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	return file, nil
}

func (b *LocalFilesystemBackend) Remove(imageID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Get metadata first to find the file
	var metadata ImageMetadata
	err := b.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(imageID))
		if err != nil {
			if err == badger.ErrKeyNotFound {
				return nil // Image doesn't exist, consider it already removed
			}
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &metadata)
		})
	})
	if err != nil {
		return fmt.Errorf("failed to get metadata: %w", err)
	}

	if metadata.Hash != "" {
		// Remove file
		filePath := filepath.Join(b.root, metadata.Hash)
		if err := os.Remove(filepath.Clean(filePath)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove file: %w", err)
		}
	}

	// Remove metadata from database
	return b.db.Update(func(txn *badger.Txn) error {
		return txn.Delete([]byte(imageID))
	})
}

func (b *LocalFilesystemBackend) Exists(imageID string) (bool, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var exists bool
	err := b.db.View(func(txn *badger.Txn) error {
		_, err := txn.Get([]byte(imageID))
		if err == badger.ErrKeyNotFound {
			exists = false
			return nil
		}
		if err != nil {
			return err
		}
		exists = true
		return nil
	})
	return exists, err
}

// LocalFilesystemBackend methods for the new interface
func (b *LocalFilesystemBackend) GetMetadata(imageID string) (*ImageMetadata, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var metadata ImageMetadata
	err := b.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(imageID))
		if err != nil {
			if err == badger.ErrKeyNotFound {
				return fmt.Errorf("image not found: %s", imageID)
			}
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &metadata)
		})
	})
	if err != nil {
		return nil, err
	}

	return &metadata, nil
}

func (b *LocalFilesystemBackend) List() ([]string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var imageIDs []string
	err := b.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false // Only need keys
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			imageIDs = append(imageIDs, string(item.Key()))
		}
		return nil
	})
	return imageIDs, err
}

// Close closes the database connection
func (b *LocalFilesystemBackend) Close() error {
	if b.db != nil {
		return b.db.Close()
	}
	return nil
}
