package vm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	imageservice "github.com/q-controller/qcontroller/src/generated/oapi"
	cc "github.com/q-controller/qcontroller/src/generated/oapi/controllerclient"
	ic "github.com/q-controller/qcontroller/src/generated/oapi/imageclient"
	"github.com/q-controller/qcontroller/src/pkg/images"
)

type recordedProgress struct {
	resource string
	message  string
	percent  int32
}

type mockProgressReporter struct {
	mu     sync.Mutex
	events []recordedProgress
}

func (m *mockProgressReporter) PublishProgress(resource, message string, percent int32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, recordedProgress{resource, message, percent})
	return nil
}

func (m *mockProgressReporter) getEvents() []recordedProgress {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]recordedProgress, len(m.events))
	copy(out, m.events)
	return out
}

type mockLocalImageClient struct {
	imageData  []byte
	downloadFn func(ctx context.Context, id, path string) error
}

func (m *mockLocalImageClient) Upload(_ context.Context, _ string, _ multipart.File) error {
	return nil
}

func (m *mockLocalImageClient) Download(ctx context.Context, id, path string) error {
	if m.downloadFn != nil {
		return m.downloadFn(ctx, id, path)
	}
	return os.WriteFile(path, m.imageData, 0644)
}

func (m *mockLocalImageClient) Remove(_ context.Context, _ string) error { return nil }

func (m *mockLocalImageClient) List(_ context.Context) ([]*imageservice.ImageInfo, error) {
	return nil, nil
}

var _ images.ImageClient = (*mockLocalImageClient)(nil)

func ptr[T any](v T) *T { return &v }

func TestProgressWriter_ReportsPercentage(t *testing.T) {
	var buf bytes.Buffer
	reporter := &mockProgressReporter{}
	pw := &progressWriter{
		dst:       &buf,
		total:     100,
		resource:  "image:test",
		message:   "Pushing",
		publisher: reporter,
	}

	_, err := pw.Write(make([]byte, 50))
	require.NoError(t, err)
	assert.Equal(t, int32(50), pw.lastPct)

	_, err = pw.Write(make([]byte, 50))
	require.NoError(t, err)
	assert.Equal(t, int32(100), pw.lastPct)

	assert.Equal(t, 100, buf.Len())

	events := reporter.getEvents()
	require.NotEmpty(t, events)
	assert.Equal(t, int32(50), events[0].percent)
	assert.Equal(t, int32(100), events[len(events)-1].percent)
}

func TestProgressWriter_ThrottlesDuplicates(t *testing.T) {
	var buf bytes.Buffer
	reporter := &mockProgressReporter{}
	pw := &progressWriter{
		dst:       &buf,
		total:     1000,
		resource:  "image:test",
		message:   "Pushing",
		publisher: reporter,
	}

	for i := 0; i < 9; i++ {
		_, _ = pw.Write([]byte{0})
	}
	assert.Empty(t, reporter.getEvents(), "no events expected for 0%%")

	_, _ = pw.Write([]byte{0})
	events := reporter.getEvents()
	require.Len(t, events, 1)
	assert.Equal(t, int32(1), events[0].percent)
}

func TestProgressWriter_ZeroTotal(t *testing.T) {
	var buf bytes.Buffer
	reporter := &mockProgressReporter{}
	pw := &progressWriter{
		dst:       &buf,
		total:     0,
		resource:  "image:test",
		message:   "Pushing",
		publisher: reporter,
	}

	n, err := pw.Write([]byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Empty(t, reporter.getEvents())
}

func TestOapi2ProtoInfo_FullFields(t *testing.T) {
	src := &cc.ServicesV1Info{
		Name:    ptr("test-vm"),
		State:   ptr("STATE_RUNNING"),
		Hwaddr:  ptr("aa:bb:cc:dd:ee:ff"),
		Node:    ptr("node1"),
		ImageId: ptr("ubuntu-22.04"),
		Details: &cc.SettingsV1VM{
			Cpus:   ptr(uint32(4)),
			Memory: ptr(uint32(2048)),
			Disk:   ptr(uint32(40960)),
		},
		CloudInit: &cc.VmStatemachineV1CloudInit{
			Userdata:      ptr("#cloud-config"),
			NetworkConfig: ptr("version: 2"),
		},
		RuntimeInfo: &cc.VmRuntimeV1RuntimeInfo{
			Name:        ptr("test-vm"),
			Ipaddresses: &[]string{"10.0.0.1", "10.0.0.2"},
			MemoryStats: &cc.SettingsV1MemoryStats{
				TotalMemory:     ptr("4294967296"),
				AvailableMemory: ptr("2147483648"),
				FreeMemory:      ptr("1073741824"),
				DiskCaches:      ptr("536870912"),
			},
			DiskStats: &cc.SettingsV1DiskStats{
				TotalBytes: ptr("42949672960"),
				UsedBytes:  ptr("21474836480"),
			},
		},
	}

	info := oapi2ProtoInfo(src)

	assert.Equal(t, "test-vm", info.Name)
	assert.Equal(t, "STATE_RUNNING", info.State)
	assert.Equal(t, "aa:bb:cc:dd:ee:ff", info.Hwaddr)
	assert.Equal(t, "node1", info.Node)
	assert.Equal(t, "ubuntu-22.04", info.ImageId)

	require.NotNil(t, info.Details)
	assert.Equal(t, uint32(4), info.Details.Cpus)
	assert.Equal(t, uint32(2048), info.Details.Memory)
	assert.Equal(t, uint32(40960), info.Details.Disk)

	require.NotNil(t, info.CloudInit)
	assert.Equal(t, "#cloud-config", info.CloudInit.Userdata)
	assert.Equal(t, "version: 2", info.CloudInit.NetworkConfig)

	require.NotNil(t, info.RuntimeInfo)
	assert.Equal(t, []string{"10.0.0.1", "10.0.0.2"}, info.RuntimeInfo.Ipaddresses)

	require.NotNil(t, info.RuntimeInfo.MemoryStats)
	assert.Equal(t, uint64(4294967296), info.RuntimeInfo.MemoryStats.TotalMemory)

	require.NotNil(t, info.RuntimeInfo.DiskStats)
	assert.Equal(t, uint64(42949672960), info.RuntimeInfo.DiskStats.TotalBytes)
}

func TestOapi2ProtoInfo_NilFields(t *testing.T) {
	info := oapi2ProtoInfo(&cc.ServicesV1Info{})

	assert.Empty(t, info.Name)
	assert.Nil(t, info.Details)
	assert.Nil(t, info.CloudInit)
	assert.Nil(t, info.RuntimeInfo)
}

func TestOapi2ProtoInfo_PartialRuntimeInfo(t *testing.T) {
	info := oapi2ProtoInfo(&cc.ServicesV1Info{
		RuntimeInfo: &cc.VmRuntimeV1RuntimeInfo{
			Name: ptr("vm1"),
		},
	})

	require.NotNil(t, info.RuntimeInfo)
	assert.Equal(t, "vm1", info.RuntimeInfo.Name)
	assert.Empty(t, info.RuntimeInfo.Ipaddresses)
	assert.Nil(t, info.RuntimeInfo.MemoryStats)
	assert.Nil(t, info.RuntimeInfo.DiskStats)
}

func TestEnsureImage_SkipsWhenImageExists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/images" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ic.ImageListResponse{
				Images: &[]ic.ImageInfo{{ImageId: "existing-image", Hash: "abc", Size: 100}},
			})
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	client, _ := cc.NewClientWithResponses(server.URL)
	imgClient, _ := ic.NewClientWithResponses(server.URL)

	rm := &remoteNodeManager{
		name:            "test-node",
		endpoint:        server.URL,
		client:          client,
		imageClient:     imgClient,
		eventsPublisher: &mockProgressReporter{},
	}

	assert.NoError(t, rm.ensureImage(context.Background(), "existing-image"))
}

func TestEnsureImage_UploadsWhenMissing(t *testing.T) {
	var uploadReceived bool
	var uploadedImageId string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/images" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ic.ImageListResponse{Images: &[]ic.ImageInfo{}})

		case r.URL.Path == "/v1/images" && r.Method == http.MethodPost:
			uploadReceived = true
			mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			assert.Equal(t, "multipart/form-data", mediaType)
			reader := multipart.NewReader(r.Body, params["boundary"])
			for {
				part, err := reader.NextPart()
				if err == io.EOF {
					break
				}
				if part.FormName() == "id" {
					data, _ := io.ReadAll(part)
					uploadedImageId = string(data)
				}
			}
			w.WriteHeader(http.StatusOK)

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client, _ := cc.NewClientWithResponses(server.URL)
	imgClient, _ := ic.NewClientWithResponses(server.URL)

	rm := &remoteNodeManager{
		name:            "test-node",
		endpoint:        server.URL,
		client:          client,
		imageClient:     imgClient,
		localImages:     &mockLocalImageClient{imageData: []byte("fake-image-data")},
		eventsPublisher: &mockProgressReporter{},
	}

	require.NoError(t, rm.ensureImage(context.Background(), "new-image"))
	assert.True(t, uploadReceived, "expected upload request")
	assert.Equal(t, "new-image", uploadedImageId)
}

func TestEnsureImage_CancelledContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/images" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ic.ImageListResponse{Images: &[]ic.ImageInfo{}})
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	client, _ := cc.NewClientWithResponses(server.URL)
	imgClient, _ := ic.NewClientWithResponses(server.URL)

	rm := &remoteNodeManager{
		name:        "test-node",
		endpoint:    server.URL,
		client:      client,
		imageClient: imgClient,
		localImages: &mockLocalImageClient{
			downloadFn: func(ctx context.Context, _, _ string) error {
				<-ctx.Done()
				return ctx.Err()
			},
		},
		eventsPublisher: &mockProgressReporter{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	assert.Error(t, rm.ensureImage(ctx, "some-image"))
}
