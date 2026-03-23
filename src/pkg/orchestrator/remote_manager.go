package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	controllerv1 "github.com/q-controller/qcontroller/src/generated/services/controller/v1"
	eventv1 "github.com/q-controller/qcontroller/src/generated/services/event/v1"
	fileregistryv1 "github.com/q-controller/qcontroller/src/generated/services/fileregistry/v1"
	orchestratorv1 "github.com/q-controller/qcontroller/src/generated/services/orchestrator/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	vmv1 "github.com/q-controller/qcontroller/src/generated/vm/statemachine/v1"
	"github.com/q-controller/qcontroller/src/pkg/images"
	"github.com/q-controller/qcontroller/src/pkg/node"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// remoteNodeManager implements node.Manager by calling a remote controller via gRPC.
type remoteNodeManager struct {
	name        string
	endpoint    string
	client      controllerv1.ControllerServiceClient
	nodeImages  images.ImageClient
	localImages images.ImageClient
	publisher   *Broadcaster
	conns       []*grpc.ClientConn
}

func newRemoteNodeManager(name, controllerEndpoint, fileRegistryEndpoint string, localImages images.ImageClient, publisher *Broadcaster) (node.Manager, error) {
	controllerConn, controllerConnErr := grpc.NewClient(controllerEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if controllerConnErr != nil {
		return nil, fmt.Errorf("failed to connect to controller %s: %w", name, controllerConnErr)
	}

	frConn, frErr := grpc.NewClient(fileRegistryEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if frErr != nil {
		_ = controllerConn.Close()
		return nil, fmt.Errorf("failed to connect to file registry %s: %w", name, frErr)
	}
	nodeImages, imgErr := images.CreateImageClient(fileregistryv1.NewFileRegistryServiceClient(frConn))
	if imgErr != nil {
		_ = controllerConn.Close()
		_ = frConn.Close()
		return nil, fmt.Errorf("failed to create image client for %s: %w", name, imgErr)
	}

	return &remoteNodeManager{
		name:        name,
		endpoint:    controllerEndpoint,
		client:      controllerv1.NewControllerServiceClient(controllerConn),
		nodeImages:  nodeImages,
		localImages: localImages,
		publisher:   publisher,
		conns:       []*grpc.ClientConn{controllerConn, frConn},
	}, nil
}

func (n *remoteNodeManager) Close() {
	for _, conn := range n.conns {
		_ = conn.Close()
	}
}

func (n *remoteNodeManager) Endpoint() string {
	return n.endpoint
}

func (n *remoteNodeManager) Create(ctx context.Context, id, imageId string,
	cpus, memory, disk uint32, cloudInit *vmv1.CloudInit) error {
	if err := n.ensureImage(ctx, imageId); err != nil {
		return fmt.Errorf("ensure image on %s: %w", n.name, err)
	}

	req := &controllerv1.CreateRequest{
		Name: id,
		Spec: &controllerv1.VMSpec{
			Image: imageId,
			Vm:    &settingsv1.VM{Cpus: cpus, Memory: memory, Disk: disk},
		},
	}
	if cloudInit != nil {
		req.Spec.CloudInit = cloudInit
	}
	_, err := n.client.Create(ctx, req)
	if err != nil {
		return fmt.Errorf("create on %s: %w", n.name, err)
	}
	return nil
}

func (n *remoteNodeManager) ensureImage(ctx context.Context, imageId string) error {
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

	remoteImgs, err := n.nodeImages.List(ctx)
	if err != nil {
		return fmt.Errorf("list remote images: %w", err)
	}
	for _, img := range remoteImgs {
		if img.ImageId == imageId && img.Hash == localHash {
			return nil
		}
	}

	slog.Info("Pushing image to remote node", "image", imageId, "node", n.name)
	n.publishProgress(imageId, 0)

	tmpFile, tmpFileErr := os.CreateTemp("", "image-push-*")
	if tmpFileErr != nil {
		return fmt.Errorf("create temp file: %w", tmpFileErr)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	if err := n.localImages.Download(ctx, imageId, tmpPath); err != nil {
		return fmt.Errorf("download image locally: %w", err)
	}

	file, fileErr := os.Open(tmpPath)
	if fileErr != nil {
		return fmt.Errorf("open temp file: %w", fileErr)
	}
	defer func() { _ = file.Close() }()

	stat, statErr := file.Stat()
	if statErr != nil {
		return fmt.Errorf("stat temp file: %w", statErr)
	}

	pf := &progressFile{
		File:    file,
		total:   stat.Size(),
		imageId: imageId,
		rm:      n,
	}

	if err := n.nodeImages.Upload(ctx, imageId, pf); err != nil {
		return fmt.Errorf("upload image to %s: %w", n.name, err)
	}

	slog.Info("Image pushed to remote node", "image", imageId, "node", n.name)
	n.publishProgress(imageId, 100)
	return nil
}

func (n *remoteNodeManager) publishProgress(imageId string, percent int32) {
	n.publisher.Send(&orchestratorv1.Event{
		Node: n.name,
		Update: &eventv1.Update{
			Payload: &eventv1.Update_ProgressEvent{
				ProgressEvent: &eventv1.ProgressEvent{
					Resource: fmt.Sprintf("image:%s", imageId),
					Message:  fmt.Sprintf("Pushing image to %s", n.name),
					Percent:  percent,
				},
			},
		},
	})
}

func (n *remoteNodeManager) Start(ctx context.Context, name string) error {
	_, err := n.client.Start(ctx, &controllerv1.StartRequest{Name: name})
	if err != nil {
		return fmt.Errorf("start on %s: %w", n.name, err)
	}
	return nil
}

func (n *remoteNodeManager) Stop(ctx context.Context, name string, force bool) error {
	_, err := n.client.Stop(ctx, &controllerv1.StopRequest{Name: name, Force: force})
	if err != nil {
		return fmt.Errorf("stop on %s: %w", n.name, err)
	}
	return nil
}

func (n *remoteNodeManager) Remove(ctx context.Context, name string) error {
	_, err := n.client.Remove(ctx, &controllerv1.RemoveRequest{Name: name})
	if err != nil {
		return fmt.Errorf("remove on %s: %w", n.name, err)
	}
	return nil
}

func (n *remoteNodeManager) Info(ctx context.Context, name string) ([]*controllerv1.Info, error) {
	resp, err := n.client.Info(ctx, &controllerv1.InfoRequest{Name: name})
	if err != nil {
		return nil, fmt.Errorf("info on %s: %w", n.name, err)
	}
	return resp.Info, nil
}

// progressFile embeds *os.File and overrides Read to track upload progress.
type progressFile struct {
	*os.File
	total   int64
	read    int64
	lastPct int32
	imageId string
	rm      *remoteNodeManager
}

func (pf *progressFile) Read(p []byte) (int, error) {
	n, err := pf.File.Read(p)
	pf.read += int64(n)
	if pf.total > 0 {
		pct := int32(pf.read * 100 / pf.total)
		if pct != pf.lastPct {
			pf.lastPct = pct
			pf.rm.publishProgress(pf.imageId, pct)
		}
	}
	return n, err
}
