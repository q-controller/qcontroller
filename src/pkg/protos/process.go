package protos

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/q-controller/qapi-client/src/client"
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

type MonitorEvent struct {
	Add        bool
	Id         string
	Prefix     string
	SocketPath string
}

type QemuServer struct {
	servicesv1.UnimplementedQemuServiceServer

	mu        sync.RWMutex
	monitor   *process.InstanceMonitor
	monitorCh chan MonitorEvent
	instances map[string]*process.Item
	nm        network.NetworkManager
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

		inst, qemuInstanceErr := qemu.Start(req.Config.Id, req.Config.Image, req.Config.OutFilePath, req.Config.ErrFilePath, qemu.Config{
			Cpus:   req.Config.Hardware.Cpus,
			Memory: req.Config.Hardware.Memory,
			Disk:   req.Config.Hardware.Disk,
			HwAddr: req.Config.Network.Mac,
		})

		if qemuInstanceErr != nil {
			return nil, status.Errorf(codes.Internal, "method Start failed: %v", qemuInstanceErr)
		}
		qemuInstance = inst
	}

	item, itemErr := process.CreateItem(id, qemuInstance)
	if itemErr != nil {
		return nil, status.Errorf(codes.Internal, "method Start failed: %v", itemErr)
	}

	q.mu.Lock()
	q.instances[req.Config.Id] = item
	qmpSocketPath := qemuInstance.QMP
	qgaSocketPath := qemuInstance.QGA
	q.mu.Unlock()

	go func() {
		ch := make(chan *servicesv1.Event)
		defer close(ch)
		item.Subscribe(ch)
		added := false
		for event := range ch {
			switch data := event.EventKind.(type) {
			case *servicesv1.Event_Status:
				if !data.Status.Running || !added {
					q.monitorCh <- MonitorEvent{
						Add:        data.Status.Running,
						Id:         event.Id,
						SocketPath: qmpSocketPath,
						Prefix:     process.PREFIX_QMP,
					}
					q.monitorCh <- MonitorEvent{
						Add:        data.Status.Running,
						Id:         event.Id,
						SocketPath: qgaSocketPath,
						Prefix:     process.PREFIX_QGA,
					}
					if data.Status.Running {
						added = true
					}
				}
			}
		}
	}()

	return &servicesv1.QemuServiceStartResponse{
		Pid: int32(qemuInstance.Pid),
	}, nil
}

func (q *QemuServer) Stop(ctx context.Context,
	req *servicesv1.QemuServiceStopRequest) (*emptypb.Empty, error) {
	q.mu.RLock()
	instance, exists := q.instances[req.Id]
	q.mu.RUnlock()

	if exists {
		if req.Force {
			if stopErr := instance.Instance.Stop(); stopErr != nil {
				return nil, stopErr
			}

			return &emptypb.Empty{}, nil
		}

		shReq, shReqErr := qapi.PrepareSystemPowerdownRequest()
		if shReqErr != nil {
			return nil, shReqErr
		}
		_, chErr := q.monitor.Execute(fmt.Sprintf("%s:%s", process.PREFIX_QMP, req.Id), client.Request(*shReq))
		if chErr != nil {
			return nil, chErr
		}
	}

	return &emptypb.Empty{}, nil
}

func (q *QemuServer) Status(req *servicesv1.QemuServiceStatusRequest,
	stream grpc.ServerStreamingServer[servicesv1.QemuServiceStatusResponse]) error {
	q.mu.RLock()
	instance, exists := q.instances[req.Id]
	q.mu.RUnlock()

	if exists {
		ch := make(chan *servicesv1.Event)
		defer close(ch)
		instance.Subscribe(ch)
		ctx := stream.Context()
		for {
			select {
			case <-ctx.Done():
				return nil
			case event, ok := <-ch:
				if !ok {
					if sendErr := stream.Send(&servicesv1.QemuServiceStatusResponse{
						Event: &servicesv1.Event{
							EventKind: &servicesv1.Event_Status{
								Status: &servicesv1.Status{
									Running: false,
								},
							},
						},
					}); sendErr != nil {
						slog.Warn("Failed to stream data", "error", sendErr)
					}
					return nil
				}
				if sendErr := stream.Send(&servicesv1.QemuServiceStatusResponse{
					Event: event,
				}); sendErr != nil {
					return sendErr
				}

				switch data := event.EventKind.(type) {
				case *servicesv1.Event_Status:
					if !data.Status.Running {
						return nil
					}
				}
			}
		}
	}

	return nil
}

func (q *QemuServer) Info(ctx context.Context, request *servicesv1.QemuServiceInfoRequest) (*servicesv1.QemuServiceInfoResponse, error) {
	res := []*servicesv1.QemuServiceInfo{}
	for _, id := range request.Ids {
		ipaddresses := []string{}
		if req, reqErr := qga.PrepareGuestNetworkGetInterfacesRequest(); reqErr == nil {
			if resCh, resErr := q.monitor.Execute(fmt.Sprintf("%s:%s", process.PREFIX_QGA, id), client.Request(*req)); resErr == nil {
				res := <-resCh
				if res.Raw.Return != nil {
					var networkInterfaces qga.GuestNetworkInterfaceList
					if unmarshalErr := json.Unmarshal(res.Raw.Return, &networkInterfaces); unmarshalErr == nil {
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
	q := &QemuServer{
		instances: map[string]*process.Item{},
		monitor:   monitor,
		monitorCh: make(chan MonitorEvent),
	}

	if linuxSettings := config.GetLinuxSettings(); linuxSettings != nil {
		nm, nmErr := network.NewNetworkManager(linuxSettings.Bridge.Name, linuxSettings.Bridge.Subnet)
		if nmErr != nil {
			return nil, nmErr
		}
		q.nm = nm
	}

	go func() {
		for event := range q.monitorCh {
			if event.Add {
				if addErr := q.monitor.Add(event.Id, event.SocketPath, event.Prefix, 10, 1000); addErr != nil {
					slog.Error("could not add instance to monitor", "instance", event.Id, "error", addErr)
				}
			} else {
				if deleteErr := q.monitor.Delete(event.Id, event.Prefix); deleteErr != nil {
					slog.Error("could not delete instance from monitor", "instance", event.Id, "error", deleteErr)
				}
			}
		}
	}()

	return q, nil
}
