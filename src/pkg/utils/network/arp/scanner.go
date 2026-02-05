package arp

import (
	"fmt"
	"log/slog"
	"net"
	"syscall"
	"time"

	"github.com/q-controller/qcontroller/src/pkg/utils/network/ip"
)

// rawConn abstracts platform-specific raw packet I/O for ARP scanning.
type rawConn interface {
	// Send sends a raw Ethernet frame.
	Send(frame []byte) error
	// Recv reads available frames from the connection into buf.
	// Returns parsed Ethernet frames. Returns syscall.EAGAIN when no data is available.
	Recv(buf []byte) ([][]byte, error)
	// Close closes the underlying socket/device.
	Close() error
	// BufSize returns the recommended read buffer size.
	BufSize() int
}

type scannerImpl struct {
	ifcName string // optional: explicit interface name, empty = auto-discover
	subnet  *net.IPNet
}

// resolveInterface dynamically resolves the network interface to use for scanning.
// If ifcName is set, it uses that interface; otherwise, it finds an interface
// that has an IP in the target subnet.
func (s *scannerImpl) resolveInterface() (*net.Interface, error) {
	if s.ifcName != "" {
		return net.InterfaceByName(s.ifcName)
	}
	return findInterfaceForSubnet(s.subnet)
}

// Scan performs an ARP scan on the configured subnet and returns a map of
// MAC addresses to IP addresses. The timeout parameter controls how long
// to wait for ARP responses after sending all requests.
func (s *scannerImpl) Scan(timeout time.Duration) (map[string]net.IP, error) {
	// Dynamically resolve interface on each scan
	iface, ifaceErr := s.resolveInterface()
	if ifaceErr != nil {
		return nil, fmt.Errorf("failed to resolve interface: %w", ifaceErr)
	}

	// Open platform-specific raw connection
	conn, err := newRawConn(iface)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	// Get source IP and MAC for this interface
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, fmt.Errorf("failed to get interface addresses: %w", err)
	}

	var srcIP net.IP
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok {
			ip4 := ipNet.IP.To4()
			if ip4 != nil && s.subnet.Contains(ip4) {
				srcIP = ip4
				break
			}
		}
	}
	if srcIP == nil {
		return nil, fmt.Errorf("no IPv4 address found on interface %s in subnet %s", iface.Name, s.subnet)
	}

	srcMAC := iface.HardwareAddr

	// Collect ARP responses
	currentHosts := make(map[string]net.IP)
	buf := make([]byte, conn.BufSize())
	readDeadline := time.Now().Add(timeout)

	// Send all requests quickly but read replies in between batches
	batchSize := 10
	currentBatch := 0
	for targetIP := range ip.SubnetHosts(s.subnet) {
		frame := buildARPRequest(srcMAC, srcIP, targetIP)
		if err := conn.Send(frame); err != nil {
			slog.Debug("Failed to send ARP request", "target", targetIP, "error", err)
		}
		currentBatch++

		// Every batch, check for replies to avoid missing early responses
		if currentBatch >= batchSize {
			frames, _ := conn.Recv(buf)
			for _, f := range frames {
				if reply, ok := parseARPReply(f); ok && s.subnet.Contains(reply.IP) {
					currentHosts[reply.MAC.String()] = reply.IP
				}
			}
			currentBatch = 0
		}
	}

	// Continue reading replies until timeout
	for time.Now().Before(readDeadline) {
		frames, err := conn.Recv(buf)
		if err != nil {
			if err == syscall.EAGAIN {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			slog.Debug("Failed to receive ARP reply", "error", err)
			continue
		}

		for _, f := range frames {
			if reply, ok := parseARPReply(f); ok && s.subnet.Contains(reply.IP) {
				currentHosts[reply.MAC.String()] = reply.IP
			}
		}
	}

	return currentHosts, nil
}

// NewScanner creates a new ARP scanner for the given subnet.
// If ifcName is provided, it uses that interface; otherwise, it automatically
// finds an interface with an IP in the given subnet during each scan.
// The interface is resolved dynamically on each scan, so it doesn't need to
// exist at creation time (useful for vmnet interfaces that are created when VMs start).
func NewScanner(ifcName string, subnet *net.IPNet) (Scanner, error) {
	if subnet == nil {
		return nil, fmt.Errorf("subnet is required")
	}

	return &scannerImpl{
		ifcName: ifcName,
		subnet:  subnet,
	}, nil
}

// findInterfaceForSubnet finds the network interface that has an IP in the given subnet.
// This is intended for macOS where vmnet creates the interface with an unpredictable name.
// On Linux, the interface name should be passed explicitly.
func findInterfaceForSubnet(subnet *net.IPNet) (*net.Interface, error) {
	ifaces, ifacesErr := net.Interfaces()
	if ifacesErr != nil {
		return nil, fmt.Errorf("failed to list interfaces: %w", ifacesErr)
	}

	for _, iface := range ifaces {
		// Skip loopback and down interfaces
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, addrsErr := iface.Addrs()
		if addrsErr != nil {
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}

			// Check if this interface's IP is in the target subnet
			if subnet.Contains(ipNet.IP) {
				return &iface, nil
			}
		}
	}

	return nil, fmt.Errorf("no interface found for subnet %s", subnet.String())
}
