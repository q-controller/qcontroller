package grpcutil

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

type options struct {
	tls *settingsv1.TLSConfig
}

type Option func(*options)

// WithTLS enables mTLS using the provided config. Nil is a no-op (plaintext).
func WithTLS(cfg *settingsv1.TLSConfig) Option {
	return func(o *options) {
		o.tls = cfg
	}
}

// NewServer creates a gRPC server, optionally with mTLS.
func NewServer(opts ...Option) (*grpc.Server, error) {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	if o.tls == nil {
		return grpc.NewServer(), nil
	}

	creds, err := serverCredentials(o.tls)
	if err != nil {
		return nil, err
	}
	return grpc.NewServer(grpc.Creds(creds)), nil
}

// Dial creates a gRPC client connection, optionally with mTLS.
func Dial(endpoint string, opts ...Option) (*grpc.ClientConn, error) {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	if o.tls == nil {
		return grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	creds, err := clientCredentials(o.tls)
	if err != nil {
		return nil, err
	}
	return grpc.NewClient(endpoint, grpc.WithTransportCredentials(creds))
}

func serverCredentials(cfg *settingsv1.TLSConfig) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(cfg.Cert, cfg.Key)
	if err != nil {
		return nil, fmt.Errorf("load server key pair: %w", err)
	}
	caPool, err := loadCAPool(cfg.Ca)
	if err != nil {
		return nil, err
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	}), nil
}

func clientCredentials(cfg *settingsv1.TLSConfig) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(cfg.Cert, cfg.Key)
	if err != nil {
		return nil, fmt.Errorf("load client key pair: %w", err)
	}
	caPool, err := loadCAPool(cfg.Ca)
	if err != nil {
		return nil, err
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS13,
	}), nil
}

func loadCAPool(path string) (*x509.CertPool, error) {
	caBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read CA cert %s: %w", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, fmt.Errorf("failed to parse CA cert %s", path)
	}
	return pool, nil
}
