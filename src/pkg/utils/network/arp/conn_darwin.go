//go:build darwin

package arp

import (
	"fmt"
	"log/slog"
	"net"
	"syscall"
)

type darwinRawConn struct {
	fd      int
	bufSize int
}

// newRawConn opens a BPF device bound to the given interface for raw ARP I/O.
func newRawConn(iface *net.Interface) (rawConn, error) {
	fd, err := openBPF()
	if err != nil {
		return nil, fmt.Errorf("failed to open BPF device: %w", err)
	}

	bufSize, err := getBPFBufLen(fd)
	if err != nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("failed to get BPF buffer size: %w", err)
	}

	if err := bindBPFToInterface(fd, iface.Name); err != nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("failed to bind BPF to interface: %w", err)
	}

	if err := setBPFImmediate(fd, true); err != nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("failed to set immediate mode: %w", err)
	}

	if err := setBPFFilterARPReply(fd); err != nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("failed to set BPF filter: %w", err)
	}

	if err := setBPFPromisc(fd, true); err != nil {
		slog.Debug("Failed to enable promiscuous mode", "error", err)
	}

	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("failed to set non-blocking mode: %w", err)
	}

	return &darwinRawConn{fd: fd, bufSize: bufSize}, nil
}

func (c *darwinRawConn) Send(frame []byte) error {
	_, err := syscall.Write(c.fd, frame)
	return err
}

func (c *darwinRawConn) Recv(buf []byte) ([][]byte, error) {
	n, err := syscall.Read(c.fd, buf)
	if err != nil {
		return nil, err
	}
	if n <= 0 {
		return nil, nil
	}

	var frames [][]byte
	for frame := range bpfPackets(buf[:n]) {
		frames = append(frames, frame)
	}
	return frames, nil
}

func (c *darwinRawConn) Close() error {
	return syscall.Close(c.fd)
}

func (c *darwinRawConn) BufSize() int {
	return c.bufSize
}
