//go:build darwin

package arp

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeBPFHeader constructs a 20-byte bpf_hdr with the given caplen and datalen.
// hdrlen is set to bpfHdrSize (20).
func makeBPFHeader(caplen, datalen uint32) []byte {
	hdr := make([]byte, bpfHdrSize)
	// bytes 0-7: timeval (zeroed)
	binary.NativeEndian.PutUint32(hdr[8:12], caplen)
	binary.NativeEndian.PutUint32(hdr[12:16], datalen)
	binary.NativeEndian.PutUint16(hdr[16:18], bpfHdrSize)
	return hdr
}

// bpfWordAlign rounds up to the next 4-byte boundary.
func bpfWordAlign(n int) int {
	return ((n + 3) / 4) * 4
}

func collectFrames(data []byte) [][]byte {
	var frames [][]byte
	for frame := range bpfPackets(data) {
		frames = append(frames, frame)
	}
	return frames
}

func TestBpfPackets_SinglePacket(t *testing.T) {
	payload := []byte{0xAA, 0xBB, 0xCC}
	hdr := makeBPFHeader(uint32(len(payload)), uint32(len(payload)))
	buf := append(hdr, payload...)

	frames := collectFrames(buf)

	require.Len(t, frames, 1)
	assert.Equal(t, payload, frames[0])
}

func TestBpfPackets_MultiplePackets(t *testing.T) {
	payload1 := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	payload2 := []byte{0x0A, 0x0B, 0x0C}

	hdr1 := makeBPFHeader(uint32(len(payload1)), uint32(len(payload1)))
	hdr2 := makeBPFHeader(uint32(len(payload2)), uint32(len(payload2)))

	// First packet: header + payload, then pad to word boundary
	pkt1 := append(hdr1, payload1...)
	aligned1 := make([]byte, bpfWordAlign(len(pkt1)))
	copy(aligned1, pkt1)

	// Second packet: header + payload
	pkt2 := append(hdr2, payload2...)
	buf := append(aligned1, pkt2...)

	frames := collectFrames(buf)

	require.Len(t, frames, 2)
	assert.Len(t, frames[0], len(payload1))
	assert.Len(t, frames[1], len(payload2))
}

func TestBpfPackets_EmptyBuffer(t *testing.T) {
	assert.Empty(t, collectFrames(nil))
}

func TestBpfPackets_TruncatedHeader(t *testing.T) {
	assert.Empty(t, collectFrames(make([]byte, bpfHdrSize-1)))
}

func TestBpfPackets_TruncatedPacket(t *testing.T) {
	// Header claims 100 bytes of capture but buffer is too short
	hdr := makeBPFHeader(100, 100)
	buf := append(hdr, make([]byte, 10)...) // only 10 bytes of payload

	assert.Empty(t, collectFrames(buf))
}

func TestBpfPackets_ZeroCaplen(t *testing.T) {
	assert.Empty(t, collectFrames(makeBPFHeader(0, 0)))
}

func TestBpfPackets_EarlyBreak(t *testing.T) {
	payload := []byte{0x01, 0x02, 0x03}
	hdr := makeBPFHeader(uint32(len(payload)), uint32(len(payload)))
	pkt := append(hdr, payload...)

	// Two packets back-to-back
	aligned := make([]byte, bpfWordAlign(len(pkt)))
	copy(aligned, pkt)
	buf := append(aligned, pkt...)

	count := 0
	for range bpfPackets(buf) {
		count++
		break // stop after first
	}
	assert.Equal(t, 1, count)
}
