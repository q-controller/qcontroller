package arp

import (
	"golang.org/x/net/bpf"
)

// arpReplyFilter is a BPF program that only passes complete ARP reply frames.
// This avoids copying all network traffic to userspace on busy interfaces, and
// guarantees every delivered frame is long enough to parse without bounds checks.
//
// Offsets refer to the Ethernet frame layout (see buildARPRequest in arp.go):
//
//	[12:14] EtherType — 0x0806 for ARP
//	[20:22] ARP opcode — 1 = request, 2 = reply
//
// The leading length gate drops runt frames (< 42 bytes = 14-byte Ethernet header
// + 28-byte ARP payload) that a malformed sender could put on a virtual link: the
// bridge does not pad them, so the kernel enforces the minimum here. ExtLen
// assembles to the classic BPF_LEN opcode, portable across Linux and BSD/macOS BPF.
var arpReplyFilter = []bpf.Instruction{
	bpf.LoadExtension{Num: bpf.ExtLen},                              // load frame length
	bpf.JumpIf{Cond: bpf.JumpGreaterOrEqual, Val: 42, SkipFalse: 5}, // if >= 42 continue, else drop
	bpf.LoadAbsolute{Off: 12, Size: 2},                              // load EtherType
	bpf.JumpIf{Cond: bpf.JumpEqual, Val: 0x0806, SkipFalse: 3},      // if ARP continue, else drop
	bpf.LoadAbsolute{Off: 20, Size: 2},                              // load ARP opcode
	bpf.JumpIf{Cond: bpf.JumpEqual, Val: 0x0002, SkipFalse: 1},      // if reply continue, else drop
	bpf.RetConstant{Val: 0xFFFFFFFF},                                // accept: return full packet
	bpf.RetConstant{Val: 0},                                         // drop: discard packet
}

func AssembleARPReplyFilter() ([]bpf.RawInstruction, error) {
	prog, err := bpf.Assemble(arpReplyFilter)
	if err != nil {
		return nil, err
	}
	return prog, nil
}
