package auth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// fakeInvoker captures the ctx that the interceptor would pass to the actual
// gRPC call, so we can inspect the outgoing metadata it injected.
func fakeInvoker(seenCtx *context.Context) grpc.UnaryInvoker {
	return func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		*seenCtx = ctx
		return nil
	}
}

func TestUnaryClientInterceptor_AddsMetadataWhenIdentityPresent(t *testing.T) {
	icpt := UnaryClientInterceptor()

	ctx := WithIdentity(context.Background(), &Identity{
		Subject:  "alice-uuid",
		Email:    "alice@example.com",
		Name:     "Alice",
		IssuedBy: "oidc:keycloak",
	})

	var seen context.Context
	require.NoError(t, icpt(ctx, "/x", nil, nil, nil, fakeInvoker(&seen)))

	md, ok := metadata.FromOutgoingContext(seen)
	require.True(t, ok)
	assert.Equal(t, []string{"alice-uuid"}, md.Get(mdSubject))
	assert.Equal(t, []string{"oidc:keycloak"}, md.Get(mdIssuedBy))
	assert.Equal(t, []string{"alice@example.com"}, md.Get(mdEmail))
	assert.Equal(t, []string{"Alice"}, md.Get(mdName))
}

func TestUnaryClientInterceptor_OmitsEmptyOptionalFields(t *testing.T) {
	icpt := UnaryClientInterceptor()

	ctx := WithIdentity(context.Background(), &Identity{
		Subject:  "alice-uuid",
		IssuedBy: "oidc:keycloak",
		// Email and Name intentionally empty.
	})

	var seen context.Context
	require.NoError(t, icpt(ctx, "/x", nil, nil, nil, fakeInvoker(&seen)))

	md, ok := metadata.FromOutgoingContext(seen)
	require.True(t, ok)
	assert.Equal(t, []string{"alice-uuid"}, md.Get(mdSubject))
	assert.Empty(t, md.Get(mdEmail))
	assert.Empty(t, md.Get(mdName))
}

func TestUnaryClientInterceptor_SkipsAnonymous(t *testing.T) {
	icpt := UnaryClientInterceptor()

	ctx := WithIdentity(context.Background(), Anonymous())

	var seen context.Context
	require.NoError(t, icpt(ctx, "/x", nil, nil, nil, fakeInvoker(&seen)))

	// No outgoing metadata should be set for anonymous identity.
	md, ok := metadata.FromOutgoingContext(seen)
	if ok {
		assert.Empty(t, md.Get(mdSubject))
	}
}

func TestUnaryClientInterceptor_PreservesExistingOutgoingMetadata(t *testing.T) {
	// Simulates OTel having already added e.g. a traceparent header.
	icpt := UnaryClientInterceptor()

	ctx := metadata.AppendToOutgoingContext(context.Background(), "traceparent", "00-abc-xyz-01")
	ctx = WithIdentity(ctx, &Identity{Subject: "alice", IssuedBy: "oidc:t"})

	var seen context.Context
	require.NoError(t, icpt(ctx, "/x", nil, nil, nil, fakeInvoker(&seen)))

	md, ok := metadata.FromOutgoingContext(seen)
	require.True(t, ok)
	assert.Equal(t, []string{"00-abc-xyz-01"}, md.Get("traceparent"))
	assert.Equal(t, []string{"alice"}, md.Get(mdSubject))
}

// ---- UnaryServerInterceptor ----

func TestUnaryServerInterceptor_ExtractsIdentity(t *testing.T) {
	icpt := UnaryServerInterceptor()

	md := metadata.MD{}
	md.Set(mdSubject, "bob-uuid")
	md.Set(mdIssuedBy, "oidc:keycloak")
	md.Set(mdEmail, "bob@example.com")
	md.Set(mdName, "Bob")
	ctx := metadata.NewIncomingContext(context.Background(), md)

	handler := func(ctx context.Context, _ any) (any, error) {
		return FromContext(ctx), nil
	}

	got, err := icpt(ctx, nil, &grpc.UnaryServerInfo{}, handler)
	require.NoError(t, err)
	id := got.(*Identity)
	require.NotNil(t, id)
	assert.Equal(t, "bob-uuid", id.Subject)
	assert.Equal(t, "oidc:keycloak", id.IssuedBy)
	assert.Equal(t, "bob@example.com", id.Email)
	assert.Equal(t, "Bob", id.Name)
}

func TestUnaryServerInterceptor_NoMetadataLeavesIdentityNil(t *testing.T) {
	icpt := UnaryServerInterceptor()

	handler := func(ctx context.Context, _ any) (any, error) {
		return FromContext(ctx), nil
	}

	got, err := icpt(context.Background(), nil, &grpc.UnaryServerInfo{}, handler)
	require.NoError(t, err)
	assert.Nil(t, got, "no metadata, no identity in context")
}

func TestUnaryServerInterceptor_MissingSubjectLeavesIdentityNil(t *testing.T) {
	icpt := UnaryServerInterceptor()

	md := metadata.MD{}
	md.Set(mdEmail, "bob@example.com") // email but no subject
	ctx := metadata.NewIncomingContext(context.Background(), md)

	handler := func(ctx context.Context, _ any) (any, error) {
		return FromContext(ctx), nil
	}

	got, err := icpt(ctx, nil, &grpc.UnaryServerInfo{}, handler)
	require.NoError(t, err)
	assert.Nil(t, got)
}

// ---- Round-trip ----

func TestClientServerInterceptors_RoundTrip(t *testing.T) {
	// Client side: stamp Identity onto outgoing metadata.
	clientCtx := WithIdentity(context.Background(), &Identity{
		Subject:  "alice-uuid",
		Email:    "alice@example.com",
		Name:     "Alice",
		IssuedBy: "oidc:keycloak",
	})
	var fwd context.Context
	require.NoError(t, UnaryClientInterceptor()(clientCtx, "/x", nil, nil, nil, fakeInvoker(&fwd)))

	// Translate outgoing metadata to incoming (the gRPC transport does this).
	md, ok := metadata.FromOutgoingContext(fwd)
	require.True(t, ok)
	serverCtx := metadata.NewIncomingContext(context.Background(), md)

	// Server side: extract Identity from incoming metadata.
	handler := func(ctx context.Context, _ any) (any, error) {
		return FromContext(ctx), nil
	}
	got, err := UnaryServerInterceptor()(serverCtx, nil, &grpc.UnaryServerInfo{}, handler)
	require.NoError(t, err)
	id := got.(*Identity)
	require.NotNil(t, id)
	assert.Equal(t, "alice-uuid", id.Subject)
	assert.Equal(t, "oidc:keycloak", id.IssuedBy)
	assert.Equal(t, "alice@example.com", id.Email)
	assert.Equal(t, "Alice", id.Name)
}
