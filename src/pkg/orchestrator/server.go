package orchestrator

import (
	"context"
	"log/slog"

	eventv1 "github.com/q-controller/qcontroller/src/generated/services/event/v1"
	orchestratorv1 "github.com/q-controller/qcontroller/src/generated/services/orchestrator/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qcontroller/src/pkg/images"
	"github.com/q-controller/qcontroller/src/pkg/node"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

type Server struct {
	orchestratorv1.UnimplementedOrchestratorServiceServer
	nodes       map[string]node.Manager
	localImages images.ImageClient
	broadcaster *Broadcaster
}

func NewServer(nodes []*settingsv1.Node, localImages images.ImageClient, bc *Broadcaster) (*Server, error) {
	nodeMap := make(map[string]node.Manager, len(nodes))
	for _, n := range nodes {
		nm, err := newRemoteNodeManager(n.Name, n.Endpoint, n.FileRegistryEndpoint, localImages, bc)
		if err != nil {
			return nil, err
		}
		nodeMap[n.Name] = nm
	}

	return &Server{
		nodes:       nodeMap,
		localImages: localImages,
		broadcaster: bc,
	}, nil
}

func (s *Server) getNode(name string) (string, node.Manager, error) {
	nm, ok := s.nodes[name]
	if !ok {
		return "", nil, status.Errorf(codes.NotFound, "node %s not found", name)
	}
	return name, nm, nil
}

func (s *Server) Create(ctx context.Context, req *orchestratorv1.CreateRequest) (*emptypb.Empty, error) {
	nodeName, nm, err := s.getNode(req.Node)
	if err != nil {
		return nil, err
	}

	cloudInit := req.Spec.GetCloudInit()
	if createErr := nm.Create(ctx, req.Name, req.Spec.GetImage(),
		req.Spec.GetVm().GetCpus(), req.Spec.GetVm().GetMemory(),
		req.Spec.GetVm().GetDisk(), cloudInit); createErr != nil {
		return nil, status.Errorf(codes.Internal, "failed to create: %v", createErr)
	}

	if req.Start {
		go func() {
			if startErr := nm.Start(context.Background(), req.Name); startErr != nil {
				slog.Error("failed to start after create", "error", startErr)
				s.broadcaster.Send(&orchestratorv1.Event{
					Node: nodeName,
					Update: &eventv1.Update{
						Payload: &eventv1.Update_ErrorEvent{
							ErrorEvent: &eventv1.ErrorEvent{
								Message:  startErr.Error(),
								Resource: req.Name,
							},
						},
					},
				})
			}
		}()
	}

	return &emptypb.Empty{}, nil
}

func (s *Server) Start(ctx context.Context, req *orchestratorv1.StartRequest) (*emptypb.Empty, error) {
	_, nm, err := s.getNode(req.Node)
	if err != nil {
		return nil, err
	}

	go func() {
		if startErr := nm.Start(context.Background(), req.Name); startErr != nil {
			slog.Error("Start failed", "node", req.Node, "name", req.Name, "error", startErr)
			s.broadcaster.Send(&orchestratorv1.Event{
				Node: req.Node,
				Update: &eventv1.Update{
					Payload: &eventv1.Update_ErrorEvent{
						ErrorEvent: &eventv1.ErrorEvent{
							Message:  startErr.Error(),
							Resource: req.Name,
						},
					},
				},
			})
		}
	}()

	return &emptypb.Empty{}, nil
}

func (s *Server) Stop(ctx context.Context, req *orchestratorv1.StopRequest) (*emptypb.Empty, error) {
	_, nm, err := s.getNode(req.Node)
	if err != nil {
		return nil, err
	}

	if stopErr := nm.Stop(ctx, req.Name, req.Force); stopErr != nil {
		return nil, status.Errorf(codes.Internal, "failed to stop: %v", stopErr)
	}

	return &emptypb.Empty{}, nil
}

func (s *Server) Remove(ctx context.Context, req *orchestratorv1.RemoveRequest) (*emptypb.Empty, error) {
	_, nm, err := s.getNode(req.Node)
	if err != nil {
		return nil, err
	}

	if removeErr := nm.Remove(ctx, req.Name); removeErr != nil {
		return nil, status.Errorf(codes.Internal, "failed to remove: %v", removeErr)
	}

	return &emptypb.Empty{}, nil
}

func (s *Server) Info(ctx context.Context, req *orchestratorv1.InfoRequest) (*orchestratorv1.InfoResponse, error) {
	nodeName, nm, err := s.getNode(req.Node)
	if err != nil {
		return nil, err
	}

	infos, infoErr := nm.Info(ctx, req.Name)
	if infoErr != nil {
		return nil, status.Errorf(codes.Internal, "failed to get info: %v", infoErr)
	}

	result := make([]*orchestratorv1.Info, 0, len(infos))
	for _, info := range infos {
		result = append(result, &orchestratorv1.Info{
			Node: nodeName,
			Info: info,
		})
	}

	return &orchestratorv1.InfoResponse{Info: result}, nil
}

func (s *Server) Close() {
	for _, nm := range s.nodes {
		nm.Close()
	}
}

func (s *Server) ListNodes(_ context.Context, _ *emptypb.Empty) (*orchestratorv1.ListNodesResponse, error) {
	out := make([]*settingsv1.Node, 0, len(s.nodes))
	for name, nm := range s.nodes {
		out = append(out, &settingsv1.Node{Name: name, Endpoint: nm.Endpoint()})
	}
	return &orchestratorv1.ListNodesResponse{Nodes: out}, nil
}
