package orchestrator

import (
	"context"
	"mime/multipart"
	"os"
	"testing"

	imageservice "github.com/q-controller/qcontroller/src/generated/oapi"
	"github.com/q-controller/qcontroller/src/pkg/images"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockImageClient struct {
	images     []*imageservice.ImageInfo
	imageData  []byte
	uploaded   map[string]bool
	downloadFn func(ctx context.Context, id, path string) error
}

func newMockImageClient(imgs []*imageservice.ImageInfo, data []byte) *mockImageClient {
	return &mockImageClient{
		images:    imgs,
		imageData: data,
		uploaded:  make(map[string]bool),
	}
}

func (m *mockImageClient) Upload(_ context.Context, name string, _ multipart.File) error {
	m.uploaded[name] = true
	return nil
}

func (m *mockImageClient) Download(ctx context.Context, id, path string) error {
	if m.downloadFn != nil {
		return m.downloadFn(ctx, id, path)
	}
	return os.WriteFile(path, m.imageData, 0644)
}

func (m *mockImageClient) Remove(_ context.Context, _ string) error { return nil }

func (m *mockImageClient) List(_ context.Context) ([]*imageservice.ImageInfo, error) {
	return m.images, nil
}

var _ images.ImageClient = (*mockImageClient)(nil)

// newTestManager creates a remoteNodeManager with mock clients for testing.
func newTestManager(t *testing.T, localImages, nodeImages *mockImageClient) *remoteNodeManager {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	bc := NewBroadcaster(100)
	go bc.Run(ctx)
	return &remoteNodeManager{
		name:        "test-node",
		endpoint:    "localhost:0",
		localImages: localImages,
		nodeImages:  nodeImages,
		publisher:   bc,
	}
}

func TestEnsureImage_SkipsWhenImageExists(t *testing.T) {
	local := newMockImageClient(
		[]*imageservice.ImageInfo{{ImageId: "img1", Hash: "abc123"}},
		nil,
	)
	remote := newMockImageClient(
		[]*imageservice.ImageInfo{{ImageId: "img1", Hash: "abc123"}},
		nil,
	)

	rm := newTestManager(t, local, remote)
	assert.NoError(t, rm.ensureImage(context.Background(), "img1"))
	assert.Empty(t, remote.uploaded)
}

func TestEnsureImage_UploadsWhenMissing(t *testing.T) {
	local := newMockImageClient(
		[]*imageservice.ImageInfo{{ImageId: "img1", Hash: "abc123"}},
		[]byte("image-data"),
	)
	remote := newMockImageClient(nil, nil)

	rm := newTestManager(t, local, remote)
	require.NoError(t, rm.ensureImage(context.Background(), "img1"))
	assert.True(t, remote.uploaded["img1"])
}

func TestEnsureImage_UploadsWhenHashDiffers(t *testing.T) {
	local := newMockImageClient(
		[]*imageservice.ImageInfo{{ImageId: "img1", Hash: "new-hash"}},
		[]byte("new-data"),
	)
	remote := newMockImageClient(
		[]*imageservice.ImageInfo{{ImageId: "img1", Hash: "old-hash"}},
		nil,
	)

	rm := newTestManager(t, local, remote)
	require.NoError(t, rm.ensureImage(context.Background(), "img1"))
	assert.True(t, remote.uploaded["img1"])
}

func TestEnsureImage_ErrorWhenNotFoundLocally(t *testing.T) {
	local := newMockImageClient(nil, nil)
	remote := newMockImageClient(nil, nil)

	rm := newTestManager(t, local, remote)
	err := rm.ensureImage(context.Background(), "missing")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found locally")
}

func TestEnsureImage_CancelledContext(t *testing.T) {
	local := newMockImageClient(
		[]*imageservice.ImageInfo{{ImageId: "img1", Hash: "abc"}},
		nil,
	)
	local.downloadFn = func(ctx context.Context, _, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	}
	remote := newMockImageClient(nil, nil)

	rm := newTestManager(t, local, remote)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := rm.ensureImage(ctx, "img1")
	assert.Error(t, err)
}
