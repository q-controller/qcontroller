package arp

import (
	"encoding/binary"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeARPReplyFrame builds a 42-byte Ethernet frame containing an ARP reply.
func makeARPReplyFrame(senderMAC net.HardwareAddr, senderIP net.IP) []byte {
	buf := make([]byte, 42)

	// Ethernet header
	copy(buf[0:6], []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	copy(buf[6:12], senderMAC)
	binary.BigEndian.PutUint16(buf[12:14], 0x0806) // ARP

	// ARP payload
	binary.BigEndian.PutUint16(buf[14:16], 1)      // Ethernet
	binary.BigEndian.PutUint16(buf[16:18], 0x0800) // IPv4
	buf[18] = 6                                    // MAC size
	buf[19] = 4                                    // IP size
	binary.BigEndian.PutUint16(buf[20:22], 2)      // reply

	copy(buf[22:28], senderMAC)
	copy(buf[28:32], senderIP.To4())

	return buf
}

func TestParseARPReply_ValidReply(t *testing.T) {
	mac := net.HardwareAddr{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01}
	ip := net.IP{192, 168, 1, 42}

	reply, ok := parseARPReply(makeARPReplyFrame(mac, ip))
	require.True(t, ok)
	assert.Equal(t, mac.String(), reply.MAC.String())
	assert.True(t, reply.IP.Equal(ip))
}

func TestParseARPReply_ARPRequest(t *testing.T) {
	frame := makeARPReplyFrame(
		net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06},
		net.IP{10, 0, 0, 1},
	)
	// Change operation to request (1)
	binary.BigEndian.PutUint16(frame[20:22], 1)

	_, ok := parseARPReply(frame)
	assert.False(t, ok)
}

func TestParseARPReply_NonARP(t *testing.T) {
	frame := make([]byte, 42)
	binary.BigEndian.PutUint16(frame[12:14], 0x0800) // IPv4, not ARP

	_, ok := parseARPReply(frame)
	assert.False(t, ok)
}

func TestParseARPReply_ShortFrame(t *testing.T) {
	_, ok := parseARPReply(make([]byte, 41))
	assert.False(t, ok)
}

func TestParseARPReply_CopiesData(t *testing.T) {
	mac := net.HardwareAddr{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}
	ip := net.IP{172, 16, 0, 1}
	frame := makeARPReplyFrame(mac, ip)

	reply, ok := parseARPReply(frame)
	require.True(t, ok)

	// Mutate the original frame and verify reply is unaffected
	frame[22] = 0x00
	frame[28] = 0x00

	assert.Equal(t, byte(0xAA), reply.MAC[0], "MAC was aliased to frame buffer")
	assert.Equal(t, byte(172), reply.IP[0], "IP was aliased to frame buffer")
}

func TestBuildARPRequest_Layout(t *testing.T) {
	srcMAC := net.HardwareAddr{0x11, 0x22, 0x33, 0x44, 0x55, 0x66}
	srcIP := net.IP{10, 0, 0, 1}
	dstIP := net.IP{10, 0, 0, 2}

	buf := buildARPRequest(srcMAC, srcIP, dstIP)

	require.Len(t, buf, 42)

	// Destination MAC is broadcast
	assert.Equal(t, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, buf[0:6], "destination MAC")

	// Source MAC
	assert.Equal(t, []byte(srcMAC), buf[6:12], "source MAC")

	// EtherType
	assert.Equal(t, uint16(0x0806), binary.BigEndian.Uint16(buf[12:14]), "EtherType")

	// ARP operation = request (1)
	assert.Equal(t, uint16(1), binary.BigEndian.Uint16(buf[20:22]), "ARP operation")

	// Sender MAC and IP in ARP payload
	assert.Equal(t, []byte(srcMAC), buf[22:28], "ARP sender MAC")
	assert.True(t, net.IP(buf[28:32]).Equal(srcIP), "ARP sender IP")

	// Target MAC should be zeros
	assert.Equal(t, make([]byte, 6), buf[32:38], "ARP target MAC")

	// Target IP
	assert.True(t, net.IP(buf[38:42]).Equal(dstIP), "ARP target IP")
}
