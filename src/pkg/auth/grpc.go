package auth

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const (
	mdSubject  = "x-qctl-user-subject"
	mdEmail    = "x-qctl-user-email"
	mdName     = "x-qctl-user-name"
	mdIssuedBy = "x-qctl-user-issued-by"
)

// UnaryClientInterceptor attaches the caller's Identity (from context) to
// outgoing gRPC metadata, so downstream services can attribute the request
// to the originating user. No-op when context has no Identity or only the
// anonymous one. Uses AppendToOutgoingContext to preserve any other metadata
// already present (e.g., trace context from another interceptor).
func UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if id := FromContext(ctx); id != nil && id.Subject != "" && id.IssuedBy != "anonymous" {
			pairs := []string{
				mdSubject, id.Subject,
				mdIssuedBy, id.IssuedBy,
			}
			if id.Email != "" {
				pairs = append(pairs, mdEmail, id.Email)
			}
			if id.Name != "" {
				pairs = append(pairs, mdName, id.Name)
			}
			ctx = metadata.AppendToOutgoingContext(ctx, pairs...)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// UnaryServerInterceptor reads the caller's Identity from incoming gRPC
// metadata into context, so handlers can retrieve it via auth.FromContext
// and structured slog calls automatically pick up `user`/`issuedBy` attrs.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if sub := mdFirst(md, mdSubject); sub != "" {
				ctx = WithIdentity(ctx, &Identity{
					Subject:  sub,
					Email:    mdFirst(md, mdEmail),
					Name:     mdFirst(md, mdName),
					IssuedBy: mdFirst(md, mdIssuedBy),
				})
			}
		}
		return handler(ctx, req)
	}
}

func mdFirst(md metadata.MD, key string) string {
	vs := md.Get(key)
	if len(vs) == 0 {
		return ""
	}
	return vs[0]
}
