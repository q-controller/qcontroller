package ip

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubnetHosts_Slash24(t *testing.T) {
	_, subnet, err := net.ParseCIDR("192.168.1.0/24")
	require.NoError(t, err, "failed to parse CIDR")

	var hosts []net.IP
	for ip := range SubnetHosts(subnet) {
		hosts = append(hosts, ip)
	}

	// /24 has 256 addresses, minus network (192.168.1.0) and broadcast (192.168.1.255)
	// = 254 usable hosts
	assert.Equal(t, 254, len(hosts), "expected 254 hosts")

	// First host should be 192.168.1.1
	assert.True(t, hosts[0].Equal(net.ParseIP("192.168.1.1")), "first host should be 192.168.1.1, got %s", hosts[0])

	// Last host should be 192.168.1.254
	assert.True(t, hosts[len(hosts)-1].Equal(net.ParseIP("192.168.1.254")), "last host should be 192.168.1.254, got %s", hosts[len(hosts)-1])

	// Should not contain network address
	for _, ip := range hosts {
		assert.False(t, ip.Equal(net.ParseIP("192.168.1.0")), "hosts should not contain network address 192.168.1.0")
	}

	// Should not contain broadcast address
	for _, ip := range hosts {
		assert.False(t, ip.Equal(net.ParseIP("192.168.1.255")), "hosts should not contain broadcast address 192.168.1.255")
	}
}

func TestSubnetHosts_Slash30(t *testing.T) {
	_, subnet, err := net.ParseCIDR("10.0.0.0/30")
	require.NoError(t, err, "failed to parse CIDR")

	var hosts []net.IP
	for ip := range SubnetHosts(subnet) {
		hosts = append(hosts, ip)
	}

	// /30 has 4 addresses: 10.0.0.0 (network), 10.0.0.1, 10.0.0.2, 10.0.0.3 (broadcast)
	// = 2 usable hosts
	assert.Equal(t, 2, len(hosts), "expected 2 hosts")
	assert.True(t, hosts[0].Equal(net.ParseIP("10.0.0.1")), "first host should be 10.0.0.1, got %s", hosts[0])
	assert.True(t, hosts[1].Equal(net.ParseIP("10.0.0.2")), "second host should be 10.0.0.2, got %s", hosts[1])
}

func TestSubnetHosts_Slash31(t *testing.T) {
	_, subnet, err := net.ParseCIDR("10.0.0.0/31")
	require.NoError(t, err, "failed to parse CIDR")

	var hosts []net.IP
	for ip := range SubnetHosts(subnet) {
		hosts = append(hosts, ip)
	}

	assert.Equal(t, 0, len(hosts), "expected 0 hosts for /31, got %d: %v", len(hosts), hosts)
}

func TestIsBroadcast(t *testing.T) {
	tests := []struct {
		ip        string
		cidr      string
		broadcast bool
	}{
		{"192.168.1.255", "192.168.1.0/24", true},
		{"192.168.1.254", "192.168.1.0/24", false},
		{"192.168.1.0", "192.168.1.0/24", false},
		{"10.0.0.3", "10.0.0.0/30", true},
		{"10.0.0.2", "10.0.0.0/30", false},
		{"10.255.255.255", "10.0.0.0/8", true},
		{"10.255.255.254", "10.0.0.0/8", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip+"_"+tt.cidr, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			_, subnet, _ := net.ParseCIDR(tt.cidr)
			result := isBroadcast(ip, subnet)
			assert.Equal(t, tt.broadcast, result, "isBroadcast(%s, %s)", tt.ip, tt.cidr)
		})
	}
}

func TestNextIP(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"192.168.1.1", "192.168.1.2"},
		{"192.168.1.255", "192.168.2.0"},
		{"192.168.255.255", "192.169.0.0"},
		{"255.255.255.255", "0.0.0.0"}, // overflow wraps
		{"10.0.0.0", "10.0.0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			ip := net.ParseIP(tt.input).To4()
			result := nextIP(ip)
			expected := net.ParseIP(tt.expected).To4()
			assert.True(t, result.Equal(expected), "nextIP(%s) = %s, want %s", tt.input, result, tt.expected)
		})
	}
}

func TestCloneIP(t *testing.T) {
	original := net.ParseIP("192.168.1.1").To4()
	clone := cloneIP(original)

	// Modify clone
	clone[3] = 99

	// Original should be unchanged
	assert.Equal(t, byte(1), original[3], "cloneIP did not create independent copy")
}
