//go:build linux
// +build linux

package firewall

import (
	"github.com/google/nftables"
	"github.com/google/nftables/expr"
)

// Helper function to convert protocol string to protocol number
func protocolNumber(proto string) byte {
	switch proto {
	case "tcp":
		return 6
	case "udp":
		return 17
	default:
		return 0
	}
}

// Helper function to convert uint16 port to network byte order bytes
func htons(port uint16) []byte {
	return []byte{byte(port >> 8), byte(port & 0xff)}
}

func PortRule(port uint16, proto, chainName, tableName string) NewRule {
	return func(rules *Rules) error {
		chain, table, chainErr := NewChain(
			WithName(chainName),
			WithinTable(tableName),
		)
		if chainErr != nil {
			return chainErr
		}

		masqRule := &nftables.Rule{
			Table: table,
			Chain: chain,
			Exprs: []expr.Any{
				// [ payload load 4b from network header to reg 1 ] (protocol)
				&expr.Payload{
					DestRegister: 1,
					Base:         expr.PayloadBaseNetworkHeader,
					Offset:       9, // IP protocol field offset
					Len:          1,
				},
				// [ cmp eq reg 1 protocol number ]
				&expr.Cmp{
					Op:       expr.CmpOpEq,
					Register: 1,
					Data:     []byte{protocolNumber(proto)},
				},
				// [ payload load 2b from transport header port to reg 1 ]
				&expr.Payload{
					DestRegister: 1,
					Base:         expr.PayloadBaseTransportHeader,
					Offset:       2, // destination port offset
					Len:          2,
				},
				// [ cmp eq reg 1 port number ]
				&expr.Cmp{
					Op:       expr.CmpOpEq,
					Register: 1,
					Data:     htons(port),
				},
				// [ payload load 4b from network header src IP to reg 1 ]
				&expr.Payload{
					DestRegister: 1,
					Base:         expr.PayloadBaseNetworkHeader,
					Offset:       12, // src IP offset (IPv4)
					Len:          4,
				},
				// [ cmp eq reg 1 subnet? ]
				// nftables does not have direct subnet check here, normally use sets or ip saddr match
				// For simplicity here, just accept any source, real use would require sets or more complex matching
				&expr.Verdict{Kind: expr.VerdictAccept},
			},
		}

		rules.rules = append(rules.rules, masqRule)
		return nil
	}
}
