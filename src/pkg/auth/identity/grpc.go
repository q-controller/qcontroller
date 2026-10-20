package identity

import (
	"context"

	v1 "github.com/q-controller/qcontroller/src/generated/services/auth/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const (
	mdSubject  = "x-qctl-user-subject"
	mdEmail    = "x-qctl-user-email"
	mdName     = "x-qctl-user-name"
	mdGroups   = "x-qctl-user-groups"
	mdIssuedBy = "x-qctl-user-issued-by"
)

// UnaryClientInterceptor attaches the caller's Identity (from context) to
// outgoing gRPC metadata, so downstream services can attribute the request
// to the originating user.
func UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return invoker(toOutgoing(ctx), method, req, reply, cc, opts...)
	}
}

func StreamClientInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		return streamer(toOutgoing(ctx), desc, cc, method, opts...)
	}
}

// UnaryServerInterceptor reads the caller's Identity from incoming gRPC
// metadata into context, so handlers can retrieve it via FromContext.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		return handler(fromIncoming(ctx), req)
	}
}

func StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		return handler(srv, &serverStream{ServerStream: ss, ctx: fromIncoming(ss.Context())})
	}
}

type serverStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *serverStream) Context() context.Context {
	return s.ctx
}

func toOutgoing(ctx context.Context) context.Context {
	id := FromContext(ctx)
	if id == nil || id.Subject == "" {
		return ctx
	}
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
	for _, g := range id.Groups {
		if g != "" {
			pairs = append(pairs, mdGroups, g)
		}
	}
	return metadata.AppendToOutgoingContext(ctx, pairs...)
}

func fromIncoming(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}
	sub := mdFirst(md, mdSubject)
	if sub == "" {
		return ctx
	}
	return WithContext(ctx, &v1.Identity{
		Subject:  sub,
		Email:    mdFirst(md, mdEmail),
		Name:     mdFirst(md, mdName),
		Groups:   md.Get(mdGroups),
		IssuedBy: mdFirst(md, mdIssuedBy),
	})
}

func mdFirst(md metadata.MD, key string) string {
	vs := md.Get(key)
	if len(vs) == 0 {
		return ""
	}
	return vs[0]
}
