package arp

import (
	"golang.org/x/net/bpf"
)

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

func AssembleARPReplyFilter() ([]bpf.RawInstruction, error) {
	prog, err := bpf.Assemble(arpReplyFilter)
	if err != nil {
		return nil, err
	}
	return prog, nil
}
