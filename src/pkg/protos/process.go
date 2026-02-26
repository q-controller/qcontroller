package protos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/q-controller/qapi-client/src/client"
	"github.com/q-controller/qapi-client/src/monitor"
	"github.com/q-controller/qcontroller/src/generated/qapi"
	servicesv1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	runtimev1 "github.com/q-controller/qcontroller/src/generated/vm/runtime/v1"
	"github.com/q-controller/qcontroller/src/pkg/images"
	"github.com/q-controller/qcontroller/src/pkg/qemu/process"
	"github.com/q-controller/qcontroller/src/pkg/utils/network"
	"github.com/q-controller/qcontroller/src/pkg/utils/network/ip"
	"github.com/q-controller/qemu-client/pkg/qemu"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

var ErrInstanceNotRunning = errors.New("instance not running")

type QemuServer struct {
	servicesv1.UnimplementedQemuServiceServer

	config               *settingsv1.QemuConfig
	nm                   network.NetworkManager
	instanceEventChannel chan<- *InstanceEvent
	commandCh            chan<- Command
	shutdownCh           chan<- struct{}
	forceStopCh          chan<- string
	addressResolver      ip.AddressResolver
	instancesDir         string
	imageClient          images.ImageClient
	startingMu           sync.Mutex
	starting             map[string]struct{}
}

type InstanceEvent struct {
	Instance *qemu.Instance
	Id       string
}

type RequestKind int

const (
	RequestKindQMP RequestKind = iota
	RequestKindQGA
)

type CommandResult struct {
	Result *monitor.ExecuteResult
	Error  error
}

type Command struct {
	Id          string
	RequestKind RequestKind
	Request     client.Request
	Result      chan<- CommandResult
}

func instanceLifecycleLoop(monitor *process.InstanceMonitor, forceStop <-chan string, cmd <-chan Command, stop <-chan struct{}, ch <-chan *InstanceEvent) {
	type instanceState struct {
		inst   *qemu.Instance
		cancel context.CancelFunc
	}
	instances := map[string]*instanceState{}
	removeCh := make(chan string)
	for {
		select {
		case <-stop:
			for _, state := range instances {
				state.cancel()
			}
			return
		case id, ok := <-removeCh:
			if ok {
				slog.Debug("Removing instance from monitor", "id", id)
				if state, exists := instances[id]; exists {
					state.cancel()
				}
				delete(instances, id)
			}
		case id, ok := <-forceStop:
			if ok {
				if state, exists := instances[id]; exists {
					state.cancel()
					delete(instances, id)
					if stopErr := state.inst.Stop(); stopErr != nil {
						slog.Error("could not stop instance", "instance", id, "error", stopErr)
					}
				}
			}
		case command, ok := <-cmd:
			if ok {
				if _, exists := instances[command.Id]; exists {
					name := fmt.Sprintf("%s:%s", process.PREFIX_QGA, command.Id)
					if command.RequestKind == RequestKindQMP {
						name = fmt.Sprintf("%s:%s", process.PREFIX_QMP, command.Id)
					}
					result, resErr := monitor.Execute(name, command.Request)
					command.Result <- CommandResult{
						Result: result,
						Error:  resErr,
					}
				} else {
					command.Result <- CommandResult{
						Result: nil,
						Error:  ErrInstanceNotRunning,
					}
				}
				close(command.Result)
			}
		case event, ok := <-ch:
			if ok {
				errorCh := make(chan error)
				ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
				instances[event.Id] = &instanceState{inst: event.Instance, cancel: cancel}
				go func(ctx context.Context) {
					err := monitor.Add(ctx, event.Id, event.Instance.QMP, process.PREFIX_QMP)
					if err != nil {
						errorCh <- fmt.Errorf("could not add QMP instance to monitor: %w", err)
					}
				}(ctx)
				go func(ctx context.Context) {
					err := monitor.Add(ctx, event.Id, event.Instance.QGA, process.PREFIX_QGA)
					if err != nil {
						errorCh <- fmt.Errorf("could not add QGA instance to monitor: %w", err)
					}
				}(ctx)
				go func(event *InstanceEvent, ctx context.Context) {
					defer cancel()
					select {
					case <-event.Instance.Done:
						slog.Info("Instance stopped", "instance", event.Id)
						removeCh <- event.Id
						return
					case err := <-errorCh:
						slog.Error("Failed to add instance to monitor", "instance", event.Id, "error", err)
					case <-ctx.Done():
						slog.Warn("Instance monitoring context deadline exceeded", "instance", event.Id)
					}
					<-event.Instance.Done
					slog.Info("Instance stopped", "instance", event.Id)
					removeCh <- event.Id
				}(event, ctx)
			}
		}
	}
}

func (q *QemuServer) instanceDir(id string) string {
	return filepath.Join(q.instancesDir, id)
}

func (q *QemuServer) Start(ctx context.Context,
	req *servicesv1.QemuServiceStartRequest) (*servicesv1.QemuServiceStartResponse, error) {
	id := req.Config.Id

	q.startingMu.Lock()
	if _, ok := q.starting[id]; ok {
		q.startingMu.Unlock()
		return nil, status.Errorf(codes.AlreadyExists, "instance %s is already starting", id)
	}
	q.starting[id] = struct{}{}
	q.startingMu.Unlock()
	defer func() {
		q.startingMu.Lock()
		delete(q.starting, id)
		q.startingMu.Unlock()
	}()

	dir := q.instanceDir(id)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create instance dir: %v", err)
	}

	// Download image if not already present
	imagePath := qemu.ImagePath(dir)
	if _, err := os.Stat(imagePath); os.IsNotExist(err) {
		slog.Info("Downloading image", "image_id", req.Config.ImageId, "instance", id)
		downloadStart := time.Now()
		tmpPath := imagePath + ".tmp"
		if downloadErr := q.imageClient.Download(ctx, req.Config.ImageId, tmpPath); downloadErr != nil {
			os.Remove(tmpPath)
			slog.Error("Failed to download image", "image_id", req.Config.ImageId, "instance", id, "error", downloadErr)
			return nil, status.Errorf(codes.Internal, "failed to download image: %v", downloadErr)
		}
		if renameErr := os.Rename(tmpPath, imagePath); renameErr != nil {
			os.Remove(tmpPath)
			return nil, status.Errorf(codes.Internal, "failed to finalize image: %v", renameErr)
		}
		if info, statErr := os.Stat(imagePath); statErr == nil {
			slog.Info("Image downloaded", "image_id", req.Config.ImageId, "instance", id,
				"size_mb", info.Size()/(1024*1024), "duration", time.Since(downloadStart).Round(time.Second))
		}
	} else {
		slog.Info("Image already present", "image_id", req.Config.ImageId, "instance", id)
	}

	if q.nm != nil {
		if removeErr := q.nm.RemoveInterface(id); removeErr != nil {
			slog.Warn("Failed to remove existing interface", "instance", id, "error", removeErr)
		}
		if ifcErr := q.nm.CreateInterface(id); ifcErr != nil {
			return nil, status.Errorf(codes.Internal, "method Start failed: %v", ifcErr)
		}
	}

	cloudInit := qemu.CloudInitConfig{}
	if req.Config.CloudInit != nil {
		cloudInit = qemu.CloudInitConfig{
			Userdata:      req.Config.CloudInit.Userdata,
			NetworkConfig: req.Config.CloudInit.NetworkConfig,
		}
	}

	platformConfig, platformErr := buildPlatformConfig(q.config)
	if platformErr != nil {
		return nil, status.Errorf(codes.Internal, "failed to build platform config: %v", platformErr)
	}

	inst, qemuInstanceErr := qemu.Start(id, dir, qemu.Config{
		Cpus:      req.Config.Hardware.Cpus,
		Memory:    req.Config.Hardware.Memory,
		Disk:      req.Config.Hardware.Disk,
		HwAddr:    req.Config.Network.Mac,
		Platform:  platformConfig,
		CloudInit: cloudInit,
	})
	if qemuInstanceErr != nil {
		return nil, status.Errorf(codes.Internal, "method Start failed: %v", qemuInstanceErr)
	}

	q.instanceEventChannel <- &InstanceEvent{
		Instance: inst,
		Id:       id,
	}

	return &servicesv1.QemuServiceStartResponse{}, nil
}

func (q *QemuServer) Stop(ctx context.Context,
	req *servicesv1.QemuServiceStopRequest) (*emptypb.Empty, error) {
	if req.Force {
		q.forceStopCh <- req.Id
		return &emptypb.Empty{}, nil
	}

	shReq, shReqErr := qapi.PrepareSystemPowerdownRequest()
	if shReqErr != nil {
		return nil, shReqErr
	}
	ch := make(chan CommandResult)
	q.commandCh <- Command{
		Id:          req.Id,
		RequestKind: RequestKindQMP,
		Request:     client.Request(*shReq),
		Result:      ch,
	}

	res := <-ch
	if res.Error != nil {
		return nil, res.Error
	}

	return &emptypb.Empty{}, nil
}

func (q *QemuServer) Status(req *servicesv1.QemuServiceStatusRequest,
	stream grpc.ServerStreamingServer[servicesv1.QemuServiceStatusResponse]) error {
	ctx := stream.Context()
	// Create a timer to periodically send VM info
	timer := time.NewTicker(1 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			running := false
			if statReq, reqErr := qapi.PrepareQueryStatusRequest(); reqErr != nil {
				slog.Error("Failed to prepare QMP status request", "error", reqErr)
			} else {
				ch := make(chan CommandResult)
				q.commandCh <- Command{
					Id:          req.Id,
					RequestKind: RequestKindQMP,
					Request:     client.Request(*statReq),
					Result:      ch,
				}
				res := <-ch
				if res.Error != nil {
					slog.Debug("Failed to execute QMP status command", "error", res.Error)
				} else if res.Result != nil {
					if r, ok := res.Result.Get(ctx, 2*time.Second); ok && r.Return != nil {
						var status qapi.StatusInfo
						if unmarshalErr := json.Unmarshal(r.Return, &status); unmarshalErr == nil {
							slog.Debug("QMP status", "instance", req.Id, "status", status.Status)
							if status.Status == "running" {
								running = true
							}
						} else {
							slog.Error("Failed to unmarshal QMP status response", "error", unmarshalErr)
						}
					}
				}
			}
			if !running {
				if sendErr := stream.Send(&servicesv1.QemuServiceStatusResponse{
					Event: &servicesv1.Event{
						Id: req.Id,
						EventKind: &servicesv1.Event_Status{
							Status: &servicesv1.Status{
								Running: false,
							},
						},
					},
				}); sendErr != nil {
					return sendErr
				}

				return nil
			}
			if sendErr := stream.Send(&servicesv1.QemuServiceStatusResponse{
				Event: &servicesv1.Event{
					Id: req.Id,
					EventKind: &servicesv1.Event_Status{
						Status: &servicesv1.Status{
							Running: true,
						},
					},
				},
			}); sendErr != nil {
				return sendErr
			}
			if resp, respErr := q.Info(ctx, &servicesv1.QemuServiceInfoRequest{
				Ids: []string{req.Id},
			}); respErr != nil || len(resp.Info) == 0 {
				slog.Debug("Failed to get VM info", "error", respErr)
			} else {
				info := resp.Info[0]
				if sendErr := stream.Send(&servicesv1.QemuServiceStatusResponse{
					Event: &servicesv1.Event{
						Id: req.Id,
						EventKind: &servicesv1.Event_Info{
							Info: info,
						},
					},
				}); sendErr != nil {
					slog.Warn("Failed to stream data", "error", sendErr)
					return sendErr
				}
			}
		}
	}
}

// executeQMPCommand sends a QMP command and waits for the result.
func (q *QemuServer) executeQMPCommand(ctx context.Context, id string, req client.Request) ([]byte, error) {
	ch := make(chan CommandResult)
	q.commandCh <- Command{
		Id:          id,
		RequestKind: RequestKindQMP,
		Request:     req,
		Result:      ch,
	}

	res := <-ch
	if res.Error != nil {
		return nil, res.Error
	}

	if res.Result == nil {
		return nil, fmt.Errorf("no result from QMP command")
	}

	r, ok := res.Result.Get(ctx, 2*time.Second)
	if !ok || r.Return == nil {
		return nil, fmt.Errorf("timeout or empty response from QMP command")
	}

	return r.Return, nil
}

// getMacAddressForInstance retrieves the MAC address for a VM instance via QMP.
func (q *QemuServer) getMacAddressForInstance(ctx context.Context, id string) (string, error) {
	path := fmt.Sprintf("/machine/peripheral/%s/virtio-backend", id)

	// First, list properties to check if "mac" exists
	listReq, err := qapi.PrepareQomListRequest(qapi.QObjQomListArg{Path: path})
	if err != nil {
		return "", fmt.Errorf("failed to prepare qom-list request: %w", err)
	}

	listResult, err := q.executeQMPCommand(ctx, id, client.Request(*listReq))
	if err != nil {
		return "", fmt.Errorf("failed to list QOM properties: %w", err)
	}

	var properties qapi.ObjectPropertyInfoList
	if err := json.Unmarshal(listResult, &properties); err != nil {
		return "", fmt.Errorf("failed to unmarshal QOM properties: %w", err)
	}

	// Find the mac property
	var macProp *qapi.ObjectPropertyInfo
	for i := range properties {
		if properties[i].Name == "mac" {
			macProp = &properties[i]
			break
		}
	}

	if macProp == nil {
		return "", fmt.Errorf("mac property not found for instance %s", id)
	}

	if macProp.Type != "str" {
		return "", fmt.Errorf("unexpected mac property type: %s", macProp.Type)
	}

	// Get the MAC address value
	getReq, err := qapi.PrepareQomGetRequest(qapi.QObjQomGetArg{
		Path:     path,
		Property: "mac",
	})
	if err != nil {
		return "", fmt.Errorf("failed to prepare qom-get request: %w", err)
	}

	getResult, err := q.executeQMPCommand(ctx, id, client.Request(*getReq))
	if err != nil {
		return "", fmt.Errorf("failed to get MAC address: %w", err)
	}

	var macAddress string
	if err := json.Unmarshal(getResult, &macAddress); err != nil {
		return "", fmt.Errorf("failed to unmarshal MAC address: %w", err)
	}

	return macAddress, nil
}

// getIpAddressesForInstance retrieves IP addresses for a VM by looking up its MAC in the ARP cache.
func (q *QemuServer) getIpAddressesForInstance(ctx context.Context, id string) ([]string, error) {
	mac, err := q.getMacAddressForInstance(ctx, id)
	if err != nil {
		return nil, err
	}

	ipaddr, err := q.addressResolver.LookupIP(mac)
	if err != nil {
		// MAC not found in ARP cache yet - not an error, just no IP available
		slog.Debug("MAC not found in ARP cache", "instance", id, "mac", mac, "error", err)
		return nil, nil
	}

	return []string{ipaddr.String()}, nil
}

func parseGuestStats(data []byte) *settingsv1.MemoryStats {
	var wrapper struct {
		Stats struct {
			TotalMemory     uint64 `json:"stat-total-memory"`
			AvailableMemory uint64 `json:"stat-available-memory"`
			FreeMemory      uint64 `json:"stat-free-memory"`
			DiskCaches      uint64 `json:"stat-disk-caches"`
		} `json:"stats"`
		LastUpdate uint64 `json:"last-update"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil
	}
	if wrapper.LastUpdate == 0 {
		return nil
	}
	return &settingsv1.MemoryStats{
		TotalMemory:     wrapper.Stats.TotalMemory,
		AvailableMemory: wrapper.Stats.AvailableMemory,
		FreeMemory:      wrapper.Stats.FreeMemory,
		DiskCaches:      wrapper.Stats.DiskCaches,
	}
}

func parseBlockInfo(data []byte) *settingsv1.DiskStats {
	var blocks []qapi.BlockInfo
	if err := json.Unmarshal(data, &blocks); err != nil {
		return nil
	}
	for _, block := range blocks {
		if block.Inserted == nil {
			continue
		}
		img := block.Inserted.Image
		if img.Format == "qcow2" && img.ActualSize != nil {
			return &settingsv1.DiskStats{
				TotalBytes: uint64(img.VirtualSize),
				UsedBytes:  uint64(*img.ActualSize),
			}
		}
	}
	return nil
}

func (q *QemuServer) getDiskStatsForInstance(ctx context.Context, id string) *settingsv1.DiskStats {
	req, reqErr := qapi.PrepareQueryBlockRequest()
	if reqErr != nil {
		return nil
	}

	result, err := q.executeQMPCommand(ctx, id, client.Request(*req))
	if err != nil {
		slog.Debug("Failed to get block info", "instance", id, "error", err)
		return nil
	}

	return parseBlockInfo(result)
}

func (q *QemuServer) getMemoryStatsForInstance(ctx context.Context, id string) *settingsv1.MemoryStats {
	req, reqErr := qapi.PrepareQomGetRequest(qapi.QObjQomGetArg{
		Path:     fmt.Sprintf("/machine/peripheral/balloon-%s", id),
		Property: "guest-stats",
	})
	if reqErr != nil {
		return nil
	}

	result, err := q.executeQMPCommand(ctx, id, client.Request(*req))
	if err != nil {
		slog.Debug("Failed to get balloon guest-stats", "instance", id, "error", err)
		return nil
	}

	return parseGuestStats(result)
}

func (q *QemuServer) Info(ctx context.Context, request *servicesv1.QemuServiceInfoRequest) (*servicesv1.QemuServiceInfoResponse, error) {
	res := []*runtimev1.RuntimeInfo{}
	for _, id := range request.Ids {
		info := &runtimev1.RuntimeInfo{
			Name:        id,
			MemoryStats: q.getMemoryStatsForInstance(ctx, id),
			DiskStats:   q.getDiskStatsForInstance(ctx, id),
		}

		ipaddresses, ipaddressesErr := q.getIpAddressesForInstance(ctx, id)
		if ipaddressesErr != nil {
			slog.Debug("Failed to get IP addresses", "instance", id, "error", ipaddressesErr)
		} else {
			info.Ipaddresses = ipaddresses
		}

		res = append(res, info)
	}

	return &servicesv1.QemuServiceInfoResponse{
		Info: res,
	}, nil
}

func (q *QemuServer) List(ctx context.Context, req *servicesv1.QemuServiceListRequest) (*servicesv1.QemuServiceListResponse, error) {
	entries, err := os.ReadDir(q.instancesDir)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to read instances dir: %v", err)
	}

	var ids []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := q.instanceDir(entry.Name())
		pid, pidErr := qemu.ReadPidfile(dir)
		if pidErr != nil || !qemu.ProcessAlive(pid) {
			continue
		}
		ids = append(ids, entry.Name())
	}

	return &servicesv1.QemuServiceListResponse{Ids: ids}, nil
}

func (q *QemuServer) Remove(ctx context.Context, req *servicesv1.QemuServiceRemoveRequest) (*emptypb.Empty, error) {
	dir := q.instanceDir(req.Id)

	// Refuse to remove if process is still alive
	if pid, err := qemu.ReadPidfile(dir); err == nil && qemu.ProcessAlive(pid) {
		return nil, status.Errorf(codes.FailedPrecondition, "instance %s is still running", req.Id)
	}

	if err := os.RemoveAll(dir); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to remove instance dir: %v", err)
	}

	return &emptypb.Empty{}, nil
}

func (q *QemuServer) reattachOnStartup() {
	entries, err := os.ReadDir(q.instancesDir)
	if err != nil {
		slog.Error("Failed to read instances dir for reattach", "error", err)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		dir := q.instanceDir(id)

		pid, pidErr := qemu.ReadPidfile(dir)
		if pidErr != nil {
			slog.Debug("No pidfile for instance, skipping reattach", "id", id)
			continue
		}

		if !qemu.ProcessAlive(pid) {
			slog.Info("Instance process not alive, skipping reattach", "id", id, "pid", pid)
			continue
		}

		inst, attachErr := qemu.Attach(id, dir, pid)
		if attachErr != nil {
			slog.Error("Failed to reattach to instance", "id", id, "error", attachErr)
			continue
		}

		slog.Info("Reattached to running instance", "id", id, "pid", pid)
		q.instanceEventChannel <- &InstanceEvent{
			Instance: inst,
			Id:       id,
		}
	}
}

func NewQemuService(monitor *process.InstanceMonitor, addressResolver ip.AddressResolver, config *settingsv1.QemuConfig, imageClient images.ImageClient) (servicesv1.QemuServiceServer, error) {
	instancesDir := filepath.Join(config.Root, "instances")
	if err := os.MkdirAll(instancesDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create instances dir: %w", err)
	}

	instanceCh := make(chan *InstanceEvent)
	commandCh := make(chan Command)
	stop := make(chan struct{})
	forceStop := make(chan string)
	q := &QemuServer{
		config:               config,
		instanceEventChannel: instanceCh,
		commandCh:            commandCh,
		shutdownCh:           stop,
		forceStopCh:          forceStop,
		addressResolver:      addressResolver,
		instancesDir:         instancesDir,
		imageClient:          imageClient,
		starting:             make(map[string]struct{}),
	}

	if linuxSettings := config.GetLinuxSettings(); linuxSettings != nil {
		nm, nmErr := network.NewNetworkManager(linuxSettings.Network.Name, linuxSettings.Network.BridgeIp)
		if nmErr != nil {
			return nil, nmErr
		}
		q.nm = nm
	}

	go instanceLifecycleLoop(monitor, forceStop, commandCh, stop, instanceCh)

	q.reattachOnStartup()

	return q, nil
}
