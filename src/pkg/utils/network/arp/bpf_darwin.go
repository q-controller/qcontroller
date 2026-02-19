//go:build darwin

package arp

import (
	"encoding/binary"
	"fmt"
	"iter"
	"syscall"
	"unsafe"

	"golang.org/x/net/bpf"
)

const (
	bpfDevicePrefix = "/dev/bpf"
	maxBPFDevices   = 256
	// struct bpf_hdr {
	//    struct timeval  bh_tstamp;     /* time stamp */
	//    uint32_t	      bh_caplen;     /* length	of captured portion */
	//    uint32_t	      bh_datalen;    /* original length of packet */
	//    u_short	      bh_hdrlen;     /* length	of bpf header (this struct
	//    plus alignment padding) */
	// };
	// timeval is 8 bytes for macos (2x4), caplen and datalen are 4 bytes each, hdrlen is 2 bytes, plus 2 bytes padding for alignment
	bpfHdrSize = 20 // 4+4+4+4+2+2 bytes
)

// bpfPackets returns an iterator over the Ethernet frames in a BPF read buffer.
// A single BPF read may return multiple concatenated packets, each prefixed by a bpf_hdr.
// This iterator parses the headers and yields the raw Ethernet frame for each packet,
// advancing by BPF_WORDALIGN (4-byte boundary) between packets.
func bpfPackets(data []byte) iter.Seq[[]byte] {
	return func(yield func([]byte) bool) {
		offset := 0
		n := len(data)

		for offset < n {
			if offset+bpfHdrSize > n {
				return
			}

			hdr := data[offset:]
			caplen := binary.NativeEndian.Uint32(hdr[8:12])
			hdrlen := binary.NativeEndian.Uint16(hdr[16:18])
			if hdrlen == 0 || caplen == 0 {
				return
			}

			packetOffset := offset + int(hdrlen)
			packetEnd := packetOffset + int(caplen)
			if packetEnd > n {
				return
			}

			if !yield(data[packetOffset:packetEnd]) {
				return
			}

			// Advance to next packet, rounding up to BPF_WORDALIGN (4-byte boundary)
			offset = ((packetEnd + 3) / 4) * 4
		}
	}
}

// openBPF opens an available BPF device
func openBPF() (int, error) {
	for i := range maxBPFDevices {
		device := fmt.Sprintf("%s%d", bpfDevicePrefix, i)
		fd, err := syscall.Open(device, syscall.O_RDWR, 0)
		if err == nil {
			return fd, nil
		}
	}
	return -1, fmt.Errorf("no available BPF devices")
}

// bindBPFToInterface binds the BPF device to a network interface
func bindBPFToInterface(fd int, ifaceName string) error {
	type ifreq struct {
		Name [syscall.IFNAMSIZ]byte
		_    [24]byte // padding
	}

	var ifr ifreq
	copy(ifr.Name[:], ifaceName)

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		syscall.BIOCSETIF,
		uintptr(unsafe.Pointer(&ifr)),
	)
	if errno != 0 {
		return fmt.Errorf("BIOCSETIF failed: %v", errno)
	}
	return nil
}

// setBPFImmediate sets immediate mode on the BPF device
func setBPFImmediate(fd int, enable bool) error {
	var val uint32
	if enable {
		val = 1
	}

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		syscall.BIOCIMMEDIATE,
		uintptr(unsafe.Pointer(&val)),
	)
	if errno != 0 {
		return fmt.Errorf("BIOCIMMEDIATE failed: %v", errno)
	}
	return nil
}

// setBPFPromisc enables promiscuous mode on the BPF device
func setBPFPromisc(fd int, enable bool) error {
	if !enable {
		return nil
	}

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		syscall.BIOCPROMISC,
		0,
	)
	if errno != 0 {
		return fmt.Errorf("BIOCPROMISC failed: %v", errno)
	}
	return nil
}

// arpReplyFilter is a BPF program that only passes ARP reply frames.
// This avoids copying all network traffic to userspace on busy interfaces.
//
// Offsets refer to the Ethernet frame layout (see buildARPRequest in arp.go):
//
//	[12:14] EtherType — 0x0806 for ARP
//	[20:22] ARP opcode — 1 = request, 2 = reply
var arpReplyFilter = []bpf.Instruction{
	bpf.LoadAbsolute{Off: 12, Size: 2},                         // load EtherType
	bpf.JumpIf{Cond: bpf.JumpEqual, Val: 0x0806, SkipFalse: 3}, // if ARP continue, else drop
	bpf.LoadAbsolute{Off: 20, Size: 2},                         // load ARP opcode
	bpf.JumpIf{Cond: bpf.JumpEqual, Val: 0x0002, SkipFalse: 1}, // if reply continue, else drop
	bpf.RetConstant{Val: 0xFFFFFFFF},                           // accept: return full packet
	bpf.RetConstant{Val: 0},                                    // drop: discard packet
}

// setBPFFilterARPReply installs the ARP reply filter on the BPF device.
func setBPFFilterARPReply(fd int) error {
	raw, err := bpf.Assemble(arpReplyFilter)
	if err != nil {
		return fmt.Errorf("failed to assemble BPF filter: %w", err)
	}

	// bpf.RawInstruction and syscall.BpfInsn have identical memory layouts
	prog := syscall.BpfProgram{
		Len:   uint32(len(raw)),
		Insns: (*syscall.BpfInsn)(unsafe.Pointer(&raw[0])),
	}
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		syscall.BIOCSETF,
		uintptr(unsafe.Pointer(&prog)),
	)
	if errno != 0 {
		return fmt.Errorf("BIOCSETF failed: %v", errno)
	}
	return nil
}

// getBPFBufLen gets the BPF buffer size
func getBPFBufLen(fd int) (int, error) {
	var bufLen uint32

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		syscall.BIOCGBLEN,
		uintptr(unsafe.Pointer(&bufLen)),
	)
	if errno != 0 {
		return 0, fmt.Errorf("BIOCGBLEN failed: %v", errno)
	}
	return int(bufLen), nil
}
