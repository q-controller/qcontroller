package arp

import (
	"net"
	"time"
)

// Scanner defines the interface for ARP scanning operations.
type Scanner interface {
	// Scan performs an ARP scan and returns a map of MAC addresses to IP addresses.
	// The timeout parameter controls how long to wait for responses.
	Scan(timeout time.Duration) (map[string]net.IP, error)
}

type ScannerConfig struct {
	Scanner  Scanner
	Interval time.Duration
	Timeout  time.Duration
}

type ScannerOption func(*ScannerConfig)

func WithInterval(d time.Duration) ScannerOption {
	return func(s *ScannerConfig) {
		s.Interval = d
	}
}

func WithTimeout(d time.Duration) ScannerOption {
	return func(s *ScannerConfig) {
		s.Timeout = d
	}
}

func WithScanner(scanner Scanner) ScannerOption {
	return func(s *ScannerConfig) {
		s.Scanner = scanner
	}
}
