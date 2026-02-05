package arp

import (
	"encoding/binary"
	"net"
)

// arpReply represents a parsed ARP reply with the sender's MAC and IP addresses.
type arpReply struct {
	MAC net.HardwareAddr
	IP  net.IP
}

// buildARPRequest constructs a 42-byte raw Ethernet frame containing an ARP request.
// Layout:
//
//	Ethernet header (14 bytes):
//	  [0:6]   destination MAC (broadcast ff:ff:ff:ff:ff:ff)
//	  [6:12]  source MAC
//	  [12:14] EtherType 0x0806 (ARP)
//	ARP payload (28 bytes):
//	  [14:16] hardware type: 1 (Ethernet)
//	  [16:18] protocol type: 0x0800 (IPv4)
//	  [18]    hardware address length: 6
//	  [19]    protocol address length: 4
//	  [20:22] operation: 1 (request)
//	  [22:28] sender MAC
//	  [28:32] sender IP
//	  [32:38] target MAC (zeros — unknown)
//	  [38:42] target IP
func buildARPRequest(srcMAC net.HardwareAddr, srcIP, dstIP net.IP) []byte {
	buf := make([]byte, 42)

	// Ethernet header
	copy(buf[0:6], []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}) // broadcast
	copy(buf[6:12], srcMAC)
	binary.BigEndian.PutUint16(buf[12:14], 0x0806) // ARP

	// ARP header
	binary.BigEndian.PutUint16(buf[14:16], 1)      // Ethernet
	binary.BigEndian.PutUint16(buf[16:18], 0x0800) // IPv4
	buf[18] = 6                                    // MAC size
	buf[19] = 4                                    // IP size
	binary.BigEndian.PutUint16(buf[20:22], 1)      // request

	copy(buf[22:28], srcMAC)
	copy(buf[28:32], srcIP.To4())
	copy(buf[32:38], []byte{0, 0, 0, 0, 0, 0})
	copy(buf[38:42], dstIP.To4())

	return buf
}

// parseARPReply extracts an ARP reply from a raw Ethernet frame.
// Returns ok=false if the frame is not an ARP reply (see buildARPRequest for the frame layout;
// [22:28] is the sender hardware address and [28:32] is the sender protocol address).
// MAC and IP are copied into new slices to avoid aliasing the read buffer.
func parseARPReply(frame []byte) (arpReply, bool) {
	if len(frame) < 42 {
		return arpReply{}, false
	}
	if binary.BigEndian.Uint16(frame[12:14]) != 0x0806 { // not ARP
		return arpReply{}, false
	}
	if binary.BigEndian.Uint16(frame[20:22]) != 2 { // not a reply
		return arpReply{}, false
	}

	mac := make(net.HardwareAddr, 6)
	copy(mac, frame[22:28])

	ip := make(net.IP, 4)
	copy(ip, frame[28:32])

	return arpReply{MAC: mac, IP: ip}, true
}
