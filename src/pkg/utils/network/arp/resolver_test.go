package arp

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockScanner struct {
	mu        sync.Mutex
	results   map[string]net.IP
	err       error
	scanCount int
}

func (m *mockScanner) Scan(timeout time.Duration) (map[string]net.IP, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scanCount++
	if m.err != nil {
		return nil, m.err
	}
	// Return a copy to avoid mutation issues
	result := make(map[string]net.IP)
	for k, v := range m.results {
		result[k] = v
	}
	return result, nil
}

func (m *mockScanner) setResults(results map[string]net.IP) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results = results
}

func (m *mockScanner) setError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

func (m *mockScanner) getScanCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.scanCount
}

// waitForInitialScan waits for the resolver to complete its initial scan.
// Since the initial scan is async, we need to wait for it before testing.
func waitForInitialScan(mock *mockScanner) {
	for i := 0; i < 100; i++ {
		if mock.getScanCount() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestResolver_LookupIP_Found(t *testing.T) {
	mock := &mockScanner{
		results: map[string]net.IP{
			"00:11:22:33:44:55": net.ParseIP("192.168.1.10"),
			"aa:bb:cc:dd:ee:ff": net.ParseIP("192.168.1.20"),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resolver, err := NewResolver(ctx,
		WithScanner(mock),
		WithInterval(1*time.Hour), // Long interval to avoid background scans
	)
	require.NoError(t, err, "NewResolver failed")
	defer resolver.Close()

	waitForInitialScan(mock)

	ip, err := resolver.LookupIP("00:11:22:33:44:55")
	require.NoError(t, err, "LookupIP failed")
	assert.True(t, ip.Equal(net.ParseIP("192.168.1.10")), "expected 192.168.1.10, got %s", ip)
}

func TestResolver_LookupIP_NotFound(t *testing.T) {
	mock := &mockScanner{
		results: map[string]net.IP{
			"00:11:22:33:44:55": net.ParseIP("192.168.1.10"),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resolver, err := NewResolver(ctx,
		WithScanner(mock),
		WithInterval(1*time.Hour),
	)
	require.NoError(t, err, "NewResolver failed")
	defer resolver.Close()

	waitForInitialScan(mock)

	_, err = resolver.LookupIP("ff:ff:ff:ff:ff:ff")
	assert.Error(t, err, "expected error for unknown MAC, got nil")
}

func TestResolver_LookupIP_InvalidMAC(t *testing.T) {
	mock := &mockScanner{
		results: map[string]net.IP{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resolver, err := NewResolver(ctx,
		WithScanner(mock),
		WithInterval(1*time.Hour),
	)
	require.NoError(t, err, "NewResolver failed")
	defer resolver.Close()

	waitForInitialScan(mock)

	_, err = resolver.LookupIP("not-a-valid-mac")
	assert.Error(t, err, "expected error for invalid MAC, got nil")
}

func TestResolver_LookupIP_MACNormalization(t *testing.T) {
	// MAC addresses can be written in different formats
	mock := &mockScanner{
		results: map[string]net.IP{
			"00:11:22:33:44:5a": net.ParseIP("192.168.1.10"),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resolver, err := NewResolver(ctx,
		WithScanner(mock),
		WithInterval(1*time.Hour),
	)
	require.NoError(t, err, "NewResolver failed")
	defer resolver.Close()

	waitForInitialScan(mock)

	// Try uppercase
	ip, err := resolver.LookupIP("00:11:22:33:44:5A")
	require.NoError(t, err, "LookupIP failed")
	assert.True(t, ip.Equal(net.ParseIP("192.168.1.10")), "expected 192.168.1.10, got %s", ip)
}

func TestResolver_UpdatesOnPeriodicScan(t *testing.T) {
	mock := &mockScanner{
		results: map[string]net.IP{
			"00:11:22:33:44:55": net.ParseIP("192.168.1.10"),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resolver, err := NewResolver(ctx,
		WithScanner(mock),
		WithInterval(50*time.Millisecond),
		WithTimeout(10*time.Millisecond),
	)
	require.NoError(t, err, "NewResolver failed")
	defer resolver.Close()

	waitForInitialScan(mock)

	// Initial lookup
	ip, err := resolver.LookupIP("00:11:22:33:44:55")
	require.NoError(t, err, "initial LookupIP failed")
	assert.True(t, ip.Equal(net.ParseIP("192.168.1.10")), "expected 192.168.1.10, got %s", ip)

	// Update mock results
	mock.setResults(map[string]net.IP{
		"00:11:22:33:44:55": net.ParseIP("192.168.1.99"), // Changed IP
		"aa:bb:cc:dd:ee:ff": net.ParseIP("192.168.1.20"), // New host
	})

	// Wait for periodic scan
	time.Sleep(100 * time.Millisecond)

	// Check updated IP
	ip, err = resolver.LookupIP("00:11:22:33:44:55")
	require.NoError(t, err, "LookupIP after update failed")
	assert.True(t, ip.Equal(net.ParseIP("192.168.1.99")), "expected updated IP 192.168.1.99, got %s", ip)

	// Check new host
	ip, err = resolver.LookupIP("aa:bb:cc:dd:ee:ff")
	require.NoError(t, err, "LookupIP for new host failed")
	assert.True(t, ip.Equal(net.ParseIP("192.168.1.20")), "expected 192.168.1.20, got %s", ip)
}

func TestResolver_KeepsPreviousOnScanError(t *testing.T) {
	mock := &mockScanner{
		results: map[string]net.IP{
			"00:11:22:33:44:55": net.ParseIP("192.168.1.10"),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resolver, err := NewResolver(ctx,
		WithScanner(mock),
		WithInterval(50*time.Millisecond),
		WithTimeout(10*time.Millisecond),
	)
	require.NoError(t, err, "NewResolver failed")
	defer resolver.Close()

	waitForInitialScan(mock)

	// Initial lookup works
	ip, err := resolver.LookupIP("00:11:22:33:44:55")
	require.NoError(t, err, "initial LookupIP failed")
	assert.True(t, ip.Equal(net.ParseIP("192.168.1.10")), "expected 192.168.1.10, got %s", ip)

	// Make scanner return error
	mock.setError(net.UnknownNetworkError("test error"))

	// Wait for periodic scan (which will fail)
	time.Sleep(100 * time.Millisecond)

	// Should still have previous data
	ip, err = resolver.LookupIP("00:11:22:33:44:55")
	require.NoError(t, err, "LookupIP after error should still work")
	assert.True(t, ip.Equal(net.ParseIP("192.168.1.10")), "expected 192.168.1.10 (kept from before error), got %s", ip)
}

func TestResolver_Stop(t *testing.T) {
	mock := &mockScanner{
		results: map[string]net.IP{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resolver, err := NewResolver(ctx,
		WithScanner(mock),
		WithInterval(10*time.Millisecond),
	)
	require.NoError(t, err, "NewResolver failed")

	// Let it run a few scans
	time.Sleep(50 * time.Millisecond)
	countBefore := mock.getScanCount()

	// Stop the resolver
	resolver.Close()

	// Wait a bit more
	time.Sleep(50 * time.Millisecond)
	countAfter := mock.getScanCount()

	// Scan count should not have increased (or maybe by 1 if timing is unlucky)
	assert.LessOrEqual(t, countAfter, countBefore+1, "resolver didn't stop properly: scans before=%d, after=%d", countBefore, countAfter)
}

func TestResolver_RequiresScanner(t *testing.T) {
	ctx := context.Background()
	_, err := NewResolver(ctx) // No scanner provided
	assert.Error(t, err, "expected error when no scanner provided, got nil")
}
