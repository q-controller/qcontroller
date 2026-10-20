package auth

import (
	"context"

	authv1 "github.com/q-controller/qcontroller/src/generated/services/auth/v1"
	"github.com/q-controller/qcontroller/src/pkg/auth/identity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AuthServer implements services.auth.v1.AuthServiceServer over the
// Authenticator. It is registered on the same grpc-gateway runtime mux as
// OrchestratorService so /v1/auth/config and /v1/auth/me flow through the same
// JSON contract pipeline as the rest of the API.
type AuthServer struct {
	authv1.UnimplementedAuthServiceServer
	auth *Authenticator
}

func NewAuthServer(auth *Authenticator) *AuthServer {
	return &AuthServer{auth: auth}
}

func (s *AuthServer) GetConfig(_ context.Context, _ *authv1.GetConfigRequest) (*authv1.GetConfigResponse, error) {
	return &authv1.GetConfigResponse{
		Providers: s.auth.Providers(),
	}, nil
}

func (s *AuthServer) GetMe(ctx context.Context, _ *authv1.GetMeRequest) (*authv1.GetMeResponse, error) {
	id := identity.FromContext(ctx)
	if id == nil {
		return nil, status.Error(codes.Unauthenticated, "identity missing from context")
	}
	return &authv1.GetMeResponse{Identity: id}, nil
}
