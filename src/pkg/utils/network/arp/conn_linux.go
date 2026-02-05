//go:build linux

package arp

import (
	"fmt"
	"net"
	"syscall"
)

type linuxRawConn struct {
	fd       int
	destAddr *syscall.SockaddrLinklayer
}

// newRawConn opens an AF_PACKET raw socket bound to the given interface for raw ARP I/O.
func newRawConn(iface *net.Interface) (rawConn, error) {
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, int(htons(syscall.ETH_P_ARP)))
	if err != nil {
		return nil, fmt.Errorf("failed to open AF_PACKET socket: %w", err)
	}

	sockaddr := &syscall.SockaddrLinklayer{
		Protocol: htons(syscall.ETH_P_ARP),
		Ifindex:  iface.Index,
	}
	if err := syscall.Bind(fd, sockaddr); err != nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("failed to bind to interface %s: %w", iface.Name, err)
	}

	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("failed to set non-blocking mode: %w", err)
	}

	destAddr := &syscall.SockaddrLinklayer{
		Protocol: htons(syscall.ETH_P_ARP),
		Ifindex:  iface.Index,
		Halen:    6,
		Addr:     [8]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	}

	return &linuxRawConn{fd: fd, destAddr: destAddr}, nil
}

func (c *linuxRawConn) Send(frame []byte) error {
	return syscall.Sendto(c.fd, frame, 0, c.destAddr)
}

func (c *linuxRawConn) Recv(buf []byte) ([][]byte, error) {
	n, _, err := syscall.Recvfrom(c.fd, buf, 0)
	if err != nil {
		return nil, err
	}
	if n <= 0 {
		return nil, nil
	}
	return [][]byte{buf[:n]}, nil
}

func (c *linuxRawConn) Close() error {
	return syscall.Close(c.fd)
}

func (c *linuxRawConn) BufSize() int {
	return 65536
}

// htons converts a uint16 from host to network byte order.
func htons(v uint16) uint16 {
	return v>>8 | v<<8
}
