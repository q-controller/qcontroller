package ip

import (
	"net"
)

// AddressResolver defines the interface for resolving MAC addresses to IP addresses.
type AddressResolver interface {
	// LookupIP returns the IP address associated with the given MAC address.
	// Returns an error if the MAC address is not found or is invalid.
	LookupIP(mac string) (net.IP, error)
	Close()
}
