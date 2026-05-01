package auth

import (
	"context"
	"net/http"
	"net/url"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	authv1 "github.com/q-controller/qcontroller/src/generated/services/auth/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

// AuthServer implements services.auth.v1.AuthServiceServer over the
// OIDCVerifier. It is registered on the same grpc-gateway runtime mux as
// OrchestratorService so /auth/config, /auth/me, /auth/logout flow through
// the same JSON contract pipeline as the rest of the API.
type AuthServer struct {
	authv1.UnimplementedAuthServiceServer
	v *OIDCVerifier // nil when auth is disabled
}

func NewAuthServer(v *OIDCVerifier) *AuthServer {
	return &AuthServer{v: v}
}

func (s *AuthServer) GetConfig(_ context.Context, _ *emptypb.Empty) (*authv1.GetConfigResponse, error) {
	resp := &authv1.GetConfigResponse{}
	if s.v != nil {
		for _, name := range s.v.LoginProviders() {
			resp.Providers = append(resp.Providers, &authv1.AuthProvider{Name: name})
		}
	}
	return resp, nil
}

func (s *AuthServer) GetMe(ctx context.Context, _ *emptypb.Empty) (*authv1.GetMeResponse, error) {
	id := FromContext(ctx)
	if id == nil {
		return nil, status.Error(codes.Unauthenticated, "identity missing from context")
	}
	return &authv1.GetMeResponse{
		Identity: &authv1.Identity{
			Subject:  id.Subject,
			Email:    id.Email,
			Name:     id.Name,
			Groups:   id.Groups,
			IssuedBy: id.IssuedBy,
		},
	}, nil
}

func (s *AuthServer) Logout(ctx context.Context, _ *emptypb.Empty) (*authv1.LogoutResponse, error) {
	resp := &authv1.LogoutResponse{}

	// Decode the session cookie (if any) before clearing it, so we can build
	// the IdP RP-initiated logout URL with id_token_hint.
	var sp sessionPayload
	if s.v != nil {
		for _, raw := range incomingCookies(ctx) {
			req := http.Request{Header: http.Header{"Cookie": []string{raw}}}
			if c, err := req.Cookie(sessionCookieName); err == nil {
				_ = decodeSigned(s.v.secret, c.Value, &sp)
				break
			}
		}
	}

	// Clear the session cookie. The gateway forwards "set-cookie" gRPC
	// header metadata to the HTTP response (see ForwardResponseOption in
	// orchestrator wiring).
	if s.v != nil {
		clear := &http.Cookie{
			Name:     sessionCookieName,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   s.v.cookieSecure(),
			MaxAge:   -1,
		}
		_ = grpc.SendHeader(ctx, metadata.Pairs("set-cookie", clear.String()))
	}

	if s.v == nil {
		return resp, nil
	}
	iss, ok := s.v.issuers[sp.Provider]
	if !ok || iss.endSessionEndpoint == "" {
		return resp, nil
	}
	params := url.Values{}
	if sp.IDToken != "" {
		params.Set("id_token_hint", sp.IDToken)
	}
	if iss.clientID != "" {
		params.Set("client_id", iss.clientID)
	}
	if s.v.externalURL != "" {
		params.Set("post_logout_redirect_uri", s.v.externalURL+"/ui/")
	}
	resp.LogoutUrl = iss.endSessionEndpoint + "?" + params.Encode()
	return resp, nil
}

// incomingCookies returns the raw Cookie header values from an inbound
// grpc-gateway request. The gateway forwards the HTTP Cookie header as
// metadata under the "grpcgateway-cookie" key.
func incomingCookies(ctx context.Context) []string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil
	}
	return md.Get("grpcgateway-cookie")
}

// ForwardSetCookie copies "set-cookie" gRPC header metadata onto the HTTP
// response. Pass it to runtime.WithForwardResponseOption when constructing
// the gateway ServeMux so that handlers can set/clear browser cookies via
// grpc.SendHeader(ctx, metadata.Pairs("set-cookie", ...)).
func ForwardSetCookie(ctx context.Context, w http.ResponseWriter, _ proto.Message) error {
	md, ok := runtime.ServerMetadataFromContext(ctx)
	if !ok {
		return nil
	}
	for _, v := range md.HeaderMD.Get("set-cookie") {
		w.Header().Add("Set-Cookie", v)
	}
	return nil
}
