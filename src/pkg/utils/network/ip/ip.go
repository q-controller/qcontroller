package ip

import (
	"iter"
	"net"
)

// SubnetHosts returns an iterator over all usable host IP addresses in the given subnet.
// It excludes the network address (first IP) and broadcast address (last IP).
//
// Note: For /31 networks (point-to-point links per RFC 3021), this returns 0 hosts
// because both addresses are considered network/broadcast. If you need to support
// /31 networks, handle them separately.
func SubnetHosts(subnet *net.IPNet) iter.Seq[net.IP] {
	return func(yield func(net.IP) bool) {
		start := subnet.IP.Mask(subnet.Mask)
		for ip := nextIP(start); subnet.Contains(ip); ip = nextIP(ip) {
			if isBroadcast(ip, subnet) {
				continue
			}
			if !yield(cloneIP(ip)) {
				return
			}
		}
	}
}

func nextIP(ip net.IP) net.IP {
	next := cloneIP(ip)
	for i := len(next) - 1; i >= 0; i-- {
		next[i]++
		if next[i] != 0 {
			break
		}
	}
	return next
}

func cloneIP(ip net.IP) net.IP {
	dup := make(net.IP, len(ip))
	copy(dup, ip)
	return dup
}

func isBroadcast(ip net.IP, subnet *net.IPNet) bool {
	// Calculate broadcast address
	broadcast := make(net.IP, len(subnet.IP))
	for i := range subnet.IP {
		broadcast[i] = subnet.IP[i] | ^subnet.Mask[i]
	}
	return ip.Equal(broadcast)
}
