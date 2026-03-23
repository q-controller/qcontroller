package vm

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	cc "github.com/q-controller/qcontroller/src/generated/oapi/controllerclient"
	ic "github.com/q-controller/qcontroller/src/generated/oapi/imageclient"
	servicesv1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	runtimev1 "github.com/q-controller/qcontroller/src/generated/vm/runtime/v1"
	vmv1 "github.com/q-controller/qcontroller/src/generated/vm/statemachine/v1"
	"github.com/q-controller/qcontroller/src/pkg/images"
	"github.com/q-controller/qcontroller/src/pkg/node"
)

// remoteNodeManager implements NodeManager by calling a remote gateway's REST API.
type remoteNodeManager struct {
	name            string
	endpoint        string
	client          *cc.ClientWithResponses
	imageClient     *ic.ClientWithResponses
	localImages     images.ImageClient
	eventsPublisher ProgressReporter
}

func newRemoteNodeManager(name, endpoint string, localImages images.ImageClient, eventsPublisher ProgressReporter) (node.Manager, error) {
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		return nil, fmt.Errorf("remote node %q endpoint must include a scheme (http:// or https://), got: %s", name, endpoint)
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	client, err := cc.NewClientWithResponses(endpoint, cc.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("failed to create controller client for %s: %w", name, err)
	}
	// Image uploads can be large, use a separate client without timeout.
	imgClient, err := ic.NewClientWithResponses(endpoint, ic.WithHTTPClient(&http.Client{}))
	if err != nil {
		return nil, fmt.Errorf("failed to create image client for %s: %w", name, err)
	}
	return &remoteNodeManager{
		name:            name,
		endpoint:        endpoint,
		client:          client,
		imageClient:     imgClient,
		localImages:     localImages,
		eventsPublisher: eventsPublisher,
	}, nil
}

func (n *remoteNodeManager) Endpoint() string {
	return n.endpoint
}

func (n *remoteNodeManager) Create(ctx context.Context, id, imageId string,
	cpus, memory, disk uint32, cloudInit *vmv1.CloudInit) error {
	if err := n.ensureImage(ctx, imageId); err != nil {
		return fmt.Errorf("ensure image on %s: %w", n.name, err)
	}

	req := cc.ServicesV1CreateRequest{
		Name: &id,
		Spec: &cc.ServicesV1VMSpec{
			Image: &imageId,
			Vm: &cc.SettingsV1VM{
				Cpus:   &cpus,
				Memory: &memory,
				Disk:   &disk,
			},
		},
	}
	if cloudInit != nil {
		req.Spec.CloudInit = &cc.VmStatemachineV1CloudInit{
			Userdata:      &cloudInit.Userdata,
			NetworkConfig: &cloudInit.NetworkConfig,
		}
	}
	resp, err := n.client.ControllerServiceCreateWithResponse(ctx, req)
	if err != nil {
		return fmt.Errorf("create on %s: %w", n.name, err)
	}
	if resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("create on %s: HTTP %d: %s", n.name, resp.StatusCode(), string(resp.Body))
	}
	return nil
}

// ensureImage checks if the remote gateway has the image with the same content
// hash. If not (missing or different hash), it downloads from the local file
// registry and uploads to the remote gateway.
func (n *remoteNodeManager) ensureImage(ctx context.Context, imageId string) error {
	// Get local image hash for comparison.
	localImgs, err := n.localImages.List(ctx)
	if err != nil {
		return fmt.Errorf("list local images: %w", err)
	}
	var localHash string
	for _, img := range localImgs {
		if img.ImageId == imageId {
			localHash = img.Hash
			break
		}
	}
	if localHash == "" {
		return fmt.Errorf("image %s not found locally", imageId)
	}

	// Check if remote already has the image with the same content hash.
	listResp, err := n.imageClient.GetV1ImagesWithResponse(ctx)
	if err != nil {
		return fmt.Errorf("list remote images: %w", err)
	}
	if listResp.StatusCode() != http.StatusOK {
		return fmt.Errorf("list remote images: HTTP %d", listResp.StatusCode())
	}
	if listResp.JSON200 != nil && listResp.JSON200.Images != nil {
		for _, img := range *listResp.JSON200.Images {
			if img.ImageId == imageId && img.Hash == localHash {
				return nil // same content, skip upload
			}
		}
	}

	progressResource := fmt.Sprintf("image:%s", imageId)
	progressMsg := fmt.Sprintf("Pushing image to %s", n.name)

	slog.Info("Pushing image to remote node", "image", imageId, "node", n.name)
	_ = n.eventsPublisher.PublishProgress(progressResource, progressMsg, 0)

	// Download from local file registry to a temp file.
	tmpFile, err := os.CreateTemp("", "image-push-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	if err := n.localImages.Download(ctx, imageId, tmpPath); err != nil {
		return fmt.Errorf("download image locally: %w", err)
	}

	// Get image size for progress reporting.
	fileStat, err := os.Stat(tmpPath)
	if err != nil {
		return fmt.Errorf("stat temp file: %w", err)
	}
	totalSize := fileStat.Size()

	// Upload to the remote gateway via multipart POST.
	// Use io.Pipe to stream the multipart body without buffering
	// the entire image in memory.
	file, err := os.Open(tmpPath)
	if err != nil {
		return fmt.Errorf("open temp file: %w", err)
	}
	defer func() { _ = file.Close() }()

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	// Write multipart body in a goroutine so the pipe reader
	// can be consumed concurrently by the HTTP client.
	go func() {
		var writeErr error
		defer func() { _ = pw.CloseWithError(writeErr) }()

		if writeErr = writer.WriteField("id", imageId); writeErr != nil {
			return
		}
		var part io.Writer
		part, writeErr = writer.CreateFormFile("file", imageId)
		if writeErr != nil {
			return
		}
		// Wrap with progress tracking.
		tracker := &progressWriter{
			dst:       part,
			total:     totalSize,
			resource:  progressResource,
			message:   progressMsg,
			publisher: n.eventsPublisher,
		}
		if _, writeErr = io.Copy(tracker, file); writeErr != nil {
			return
		}
		writeErr = writer.Close()
	}()

	uploadResp, err := n.imageClient.PostV1ImagesWithBodyWithResponse(ctx, writer.FormDataContentType(), pr)
	if err != nil {
		return fmt.Errorf("upload image: %w", err)
	}
	if uploadResp.StatusCode() != http.StatusOK {
		return fmt.Errorf("upload image: HTTP %d: %s", uploadResp.StatusCode(), string(uploadResp.Body))
	}

	slog.Info("Image pushed to remote node", "image", imageId, "node", n.name)
	_ = n.eventsPublisher.PublishProgress(progressResource, progressMsg, 100)
	return nil
}

func (n *remoteNodeManager) Start(ctx context.Context, name string) error {
	resp, err := n.client.ControllerServiceStartWithResponse(ctx, name, cc.ControllerServiceStartJSONRequestBody{})
	if err != nil {
		return fmt.Errorf("start on %s: %w", n.name, err)
	}
	if resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("start on %s: HTTP %d: %s", n.name, resp.StatusCode(), string(resp.Body))
	}
	return nil
}

func (n *remoteNodeManager) Stop(ctx context.Context, name string, force bool) error {
	resp, err := n.client.ControllerServiceStopWithResponse(ctx, name, cc.ControllerServiceStopJSONRequestBody{
		Force: &force,
	})
	if err != nil {
		return fmt.Errorf("stop on %s: %w", n.name, err)
	}
	if resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("stop on %s: HTTP %d: %s", n.name, resp.StatusCode(), string(resp.Body))
	}
	return nil
}

func (n *remoteNodeManager) Remove(ctx context.Context, name string) error {
	resp, err := n.client.ControllerServiceRemoveWithResponse(ctx, name)
	if err != nil {
		return fmt.Errorf("remove on %s: %w", n.name, err)
	}
	if resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("remove on %s: HTTP %d: %s", n.name, resp.StatusCode(), string(resp.Body))
	}
	return nil
}

func (n *remoteNodeManager) Info(ctx context.Context, name string) ([]*servicesv1.Info, error) {
	resp, err := n.client.ControllerServiceInfoWithResponse(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("info on %s: %w", n.name, err)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("info on %s: HTTP %d: %s", n.name, resp.StatusCode(), string(resp.Body))
	}
	if resp.JSON200 == nil || resp.JSON200.Info == nil {
		return nil, nil
	}
	result := make([]*servicesv1.Info, 0, len(*resp.JSON200.Info))
	for _, item := range *resp.JSON200.Info {
		result = append(result, oapi2ProtoInfo(&item))
	}
	return result, nil
}

// ProgressReporter publishes progress events during long-running operations.
type ProgressReporter interface {
	PublishProgress(resource, message string, percent int32) error
}

// progressWriter wraps a destination io.Writer and publishes progress events
// as data flows through it. It throttles updates to avoid flooding the event
// stream — a new event is published only when the percentage changes.
type progressWriter struct {
	dst       io.Writer
	total     int64
	written   int64
	lastPct   int32
	resource  string
	message   string
	publisher ProgressReporter
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.dst.Write(p)
	pw.written += int64(n)
	if pw.total > 0 {
		pct := int32(pw.written * 100 / pw.total)
		if pct != pw.lastPct {
			pw.lastPct = pct
			_ = pw.publisher.PublishProgress(pw.resource, pw.message, pct)
		}
	}
	return n, err
}

// --- Conversion helpers: OpenAPI generated types → proto generated types ---

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefUint32(v *uint32) uint32 {
	if v == nil {
		return 0
	}
	return *v
}

func derefUint64Str(s *string) uint64 {
	if s == nil {
		return 0
	}
	v, err := strconv.ParseUint(*s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func oapi2ProtoInfo(src *cc.ServicesV1Info) *servicesv1.Info {
	info := &servicesv1.Info{
		Name:    derefStr(src.Name),
		State:   derefStr(src.State),
		Hwaddr:  derefStr(src.Hwaddr),
		Node:    derefStr(src.Node),
		ImageId: derefStr(src.ImageId),
	}
	if src.Details != nil {
		info.Details = &settingsv1.VM{
			Cpus:   derefUint32(src.Details.Cpus),
			Memory: derefUint32(src.Details.Memory),
			Disk:   derefUint32(src.Details.Disk),
		}
	}
	if src.CloudInit != nil {
		info.CloudInit = &vmv1.CloudInit{
			Userdata:      derefStr(src.CloudInit.Userdata),
			NetworkConfig: derefStr(src.CloudInit.NetworkConfig),
		}
	}
	if src.RuntimeInfo != nil {
		ri := &runtimev1.RuntimeInfo{
			Name: derefStr(src.RuntimeInfo.Name),
		}
		if src.RuntimeInfo.Ipaddresses != nil {
			ri.Ipaddresses = *src.RuntimeInfo.Ipaddresses
		}
		if src.RuntimeInfo.MemoryStats != nil {
			ri.MemoryStats = &settingsv1.MemoryStats{
				TotalMemory:     derefUint64Str(src.RuntimeInfo.MemoryStats.TotalMemory),
				AvailableMemory: derefUint64Str(src.RuntimeInfo.MemoryStats.AvailableMemory),
				FreeMemory:      derefUint64Str(src.RuntimeInfo.MemoryStats.FreeMemory),
				DiskCaches:      derefUint64Str(src.RuntimeInfo.MemoryStats.DiskCaches),
			}
		}
		if src.RuntimeInfo.DiskStats != nil {
			ri.DiskStats = &settingsv1.DiskStats{
				TotalBytes: derefUint64Str(src.RuntimeInfo.DiskStats.TotalBytes),
				UsedBytes:  derefUint64Str(src.RuntimeInfo.DiskStats.UsedBytes),
			}
		}
		info.RuntimeInfo = ri
	}
	return info
}
