package arp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/q-controller/qcontroller/src/pkg/utils/network/ip"
)

// Resolver provides a unified interface for resolving MAC addresses to IP addresses
// using ARP scanning. It periodically scans the network and caches the results.
// Resolver implements the ip.AddressResolver interface.
type Resolver struct {
	cancel     context.CancelFunc
	cfg        *ScannerConfig
	knownHosts map[string]net.IP // MAC <-> IP mapping
	mu         sync.RWMutex
}

func NewResolver(ctx context.Context, opts ...ScannerOption) (ip.AddressResolver, error) {
	cfg := &ScannerConfig{
		Interval: 5 * time.Second,
		Timeout:  2 * time.Second,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.Scanner == nil {
		return nil, fmt.Errorf("scanner is required for ARP resolver")
	}

	resolver := &Resolver{
		cfg:        cfg,
		knownHosts: make(map[string]net.IP),
		cancel:     func() {},
	}

	resolver.start(ctx)

	return resolver, nil
}

// start begins the background goroutine that periodically scans for hosts.
// The initial scan is performed asynchronously to avoid blocking the caller.
func (r *Resolver) start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	r.cancel = cancel

	go func() {
		// Initial scan (async)
		scanRes, scanErr := r.cfg.Scanner.Scan(r.cfg.Timeout)
		if scanErr != nil {
			slog.Debug("Initial ARP scan failed", "error", scanErr)
		} else {
			r.mu.Lock()
			r.knownHosts = scanRes
			r.mu.Unlock()
		}

		ticker := time.NewTicker(r.cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				slog.Info("Stopping ARP resolver")
				return
			case <-ticker.C:
				scanRes, scanErr := r.cfg.Scanner.Scan(r.cfg.Timeout)
				if scanErr != nil {
					slog.Debug("ARP scan failed", "error", scanErr)
					continue
				}
				slog.Debug("ARP scan completed", "hosts_found", scanRes)
				r.mu.Lock()
				r.knownHosts = scanRes
				r.mu.Unlock()
			}
		}
	}()
}

func (r *Resolver) Close() {
	r.cancel()
}

// LookupIP returns the IP address associated with the given MAC address.
// The MAC address is normalized before lookup, so both upper and lower case
// formats are supported. Returns an error if the MAC is invalid or not found.
func (r *Resolver) LookupIP(mac string) (net.IP, error) {
	macAddr, macErr := net.ParseMAC(mac) // Validate MAC format
	if macErr != nil {
		return nil, macErr
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if val, exists := r.knownHosts[macAddr.String()]; exists {
		return val, nil
	}
	return nil, fmt.Errorf("MAC address %s not found", mac)
}
