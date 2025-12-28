package storage_test

import (
	"os"
	"strings"
	"testing"

	"github.com/q-controller/qcontroller/src/pkg/images/storage"
)

func TestLocalFilesystemBackend(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "storage-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create backend
	backend, err := storage.NewLocalFilesystemBackend(tempDir)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}
	defer backend.Close() // Close database connection

	// Test data
	imageID := "test-image"
	testData := "Hello, World! This is test image data."

	// Test Store
	dataReader := strings.NewReader(testData)
	err = backend.Store(imageID, dataReader)
	if err != nil {
		t.Fatalf("Failed to store image: %v", err)
	}

	// Test Exists
	exists, err := backend.Exists(imageID)
	if err != nil {
		t.Fatalf("Failed to check existence: %v", err)
	}
	if !exists {
		t.Fatal("Image should exist after storing")
	}

	// Test Retrieve
	reader, err := backend.Retrieve(imageID)
	if err != nil {
		t.Fatalf("Failed to retrieve image: %v", err)
	}
	defer reader.Close()

	retrievedData := make([]byte, len(testData))
	n, err := reader.Read(retrievedData)
	if err != nil {
		t.Fatalf("Failed to read retrieved data: %v", err)
	}

	if string(retrievedData[:n]) != testData {
		t.Fatalf("Retrieved data doesn't match. Expected: %s, Got: %s", testData, string(retrievedData[:n]))
	}

	// Test List
	imageIDs, err := backend.List()
	if err != nil {
		t.Fatalf("Failed to list images: %v", err)
	}

	found := false
	for _, id := range imageIDs {
		if id == imageID {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("Image ID should be in the list")
	}

	// Test Remove
	err = backend.Remove(imageID)
	if err != nil {
		t.Fatalf("Failed to remove image: %v", err)
	}

	// Verify removal
	exists, err = backend.Exists(imageID)
	if err != nil {
		t.Fatalf("Failed to check existence after removal: %v", err)
	}
	if exists {
		t.Fatal("Image should not exist after removal")
	}
}

func TestStorageBackendDeduplication(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "storage-test-internal-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create backend
	backend, err := storage.NewLocalFilesystemBackend(tempDir)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}
	defer backend.Close()

	testData := "Same content"
	imageID1 := "image-1"
	imageID2 := "image-2"

	dataReader1 := strings.NewReader(testData)
	err = backend.Store(imageID1, dataReader1)
	if err != nil {
		t.Fatalf("Failed to store first image: %v", err)
	}

	dataReader2 := strings.NewReader(testData)
	err = backend.Store(imageID2, dataReader2)
	if err != nil {
		t.Fatalf("Failed to store second image: %v", err)
	}

	// Both should exist
	exists1, _ := backend.Exists(imageID1)
	exists2, _ := backend.Exists(imageID2)

	if !exists1 || !exists2 {
		t.Fatal("Both images should exist")
	}

	// Both can be retrieved independently
	reader1, err := backend.Retrieve(imageID1)
	if err != nil {
		t.Fatalf("Failed to retrieve first image: %v", err)
	}
	reader1.Close()

	reader2, err := backend.Retrieve(imageID2)
	if err != nil {
		t.Fatalf("Failed to retrieve second image: %v", err)
	}
	reader2.Close()
}
