package protos

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/q-controller/qapi-client/src/client"
	"github.com/q-controller/qapi-client/src/monitor"
	"github.com/q-controller/qcontroller/src/generated/qapi"
	"github.com/q-controller/qcontroller/src/generated/qga"
	servicesv1 "github.com/q-controller/qcontroller/src/generated/services/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qcontroller/src/pkg/qemu/process"
	"github.com/q-controller/qcontroller/src/pkg/utils/network"
	"github.com/q-controller/qemu-client/pkg/qemu"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

type QemuServer struct {
	servicesv1.UnimplementedQemuServiceServer

	nm                   network.NetworkManager
	instanceEventChannel chan<- *InstanceEvent
	commandCh            chan<- Command
	shutdownCh           chan<- struct{}
	forceStopCh          chan<- string
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

func (q *QemuServer) Start(ctx context.Context,
	req *servicesv1.QemuServiceStartRequest) (*servicesv1.QemuServiceStartResponse, error) {
	id := req.Config.Id

	var qemuInstance *qemu.Instance
	if req.Pid != nil {
		if inst, instErr := qemu.Attach(req.Config.Id, int(*req.Pid)); instErr == nil {
			qemuInstance = inst
		} else {
			return nil, status.Errorf(codes.Internal, "method Start failed [instance: %s, err: %v]", id, instErr)
		}
	}

	if qemuInstance == nil {
		if q.nm != nil {
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

		inst, qemuInstanceErr := qemu.Start(req.Config.Id, req.Config.Image, req.Config.OutFilePath, req.Config.ErrFilePath, qemu.Config{
			Cpus:      req.Config.Hardware.Cpus,
			Memory:    req.Config.Hardware.Memory,
			Disk:      req.Config.Hardware.Disk,
			HwAddr:    req.Config.Network.Mac,
			CloudInit: cloudInit,
		})

		if qemuInstanceErr != nil {
			return nil, status.Errorf(codes.Internal, "method Start failed: %v", qemuInstanceErr)
		}
		qemuInstance = inst
	}

	q.instanceEventChannel <- &InstanceEvent{
		Instance: qemuInstance,
		Id:       id,
	}

	return &servicesv1.QemuServiceStartResponse{
		Pid: int32(qemuInstance.Pid),
	}, nil
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
			resp, respErr := q.Info(ctx, &servicesv1.QemuServiceInfoRequest{
				Ids: []string{req.Id},
			})
			if respErr != nil {
				slog.Error("Failed to get VM info", "error", respErr)
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
			if len(resp.Info) > 0 {
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

func (q *QemuServer) Info(ctx context.Context, request *servicesv1.QemuServiceInfoRequest) (*servicesv1.QemuServiceInfoResponse, error) {
	res := []*servicesv1.QemuServiceInfo{}
	for _, id := range request.Ids {
		ipaddresses := []string{}
		if req, reqErr := qga.PrepareGuestNetworkGetInterfacesRequest(); reqErr == nil {
			ch := make(chan CommandResult)
			q.commandCh <- Command{
				Id:          id,
				RequestKind: RequestKindQGA,
				Request:     client.Request(*req),
				Result:      ch,
			}
			res := <-ch
			if res.Error == nil {
				if res.Result != nil {
					res, resOk := res.Result.Get(ctx, -1)
					if resOk && res.Return != nil {
						var networkInterfaces qga.GuestNetworkInterfaceList
						if unmarshalErr := json.Unmarshal(res.Return, &networkInterfaces); unmarshalErr == nil {
							for _, networkInterface := range networkInterfaces {
								if networkInterface.IpAddresses != nil {
									for _, ipaddress := range *networkInterface.IpAddresses {
										ip := net.ParseIP(ipaddress.IpAddress)
										if !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
											ipaddresses = append(ipaddresses, ipaddress.IpAddress)
										}
									}
								}
							}
						}
					}
				} else {
					return nil, fmt.Errorf("no result from QGA for instance %s", id)
				}
			} else {
				if res.Error == process.ErrNotReady {
					continue
				}
				return nil, res.Error
			}
		}
		res = append(res, &servicesv1.QemuServiceInfo{
			Name:        id,
			Ipaddresses: ipaddresses,
		})
	}

	return &servicesv1.QemuServiceInfoResponse{
		Info: res,
	}, nil
}

func NewQemuService(monitor *process.InstanceMonitor, config *settingsv1.QemuConfig) (servicesv1.QemuServiceServer, error) {
	instanceCh := make(chan *InstanceEvent)
	commandCh := make(chan Command)
	stop := make(chan struct{})
	forceStop := make(chan string)
	q := &QemuServer{
		instanceEventChannel: instanceCh,
		commandCh:            commandCh,
		shutdownCh:           stop,
		forceStopCh:          forceStop,
	}

	if linuxSettings := config.GetLinuxSettings(); linuxSettings != nil {
		nm, nmErr := network.NewNetworkManager(linuxSettings.Network.Name, linuxSettings.Network.BridgeIp)
		if nmErr != nil {
			return nil, nmErr
		}
		q.nm = nm
	}

	go instanceLifecycleLoop(monitor, forceStop, commandCh, stop, instanceCh)

	return q, nil
}
