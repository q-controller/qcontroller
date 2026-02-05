//go:build darwin

package arp

import (
	"encoding/binary"
	"fmt"
	"iter"
	"syscall"
	"unsafe"
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
