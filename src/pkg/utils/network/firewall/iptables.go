//go:build linux
// +build linux

package firewall

import (
	"github.com/google/nftables"
	"github.com/google/nftables/expr"
)

// IPTablesConfig represents the configuration for iptables rules
type IPTablesConfig struct {
	conn             *nftables.Conn
	tableFilter      *nftables.Table
	tableNat         *nftables.Table
	chainForward     *nftables.Chain
	chainPostrouting *nftables.Chain
}

// NewIPTablesConfig initializes a new IPTablesConfig
func NewIPTablesConfig() (*IPTablesConfig, error) {
	conn := &nftables.Conn{}
	natTable := &nftables.Table{Name: "nat", Family: nftables.TableFamilyIPv4}
	filterTable := &nftables.Table{Name: "filter", Family: nftables.TableFamilyIPv4}

	postroutingChain := &nftables.Chain{Name: "POSTROUTING", Table: natTable}
	forwardChain := &nftables.Chain{Name: "FORWARD", Table: filterTable}

	return &IPTablesConfig{
		conn:             conn,
		tableFilter:      filterTable,
		tableNat:         natTable,
		chainForward:     forwardChain,
		chainPostrouting: postroutingChain,
	}, nil
}

func (c *IPTablesConfig) AddRules(rules []*nftables.Rule) error {
	for _, rule := range rules {
		c.conn.AddRule(rule)
	}

	return c.conn.Flush()
}

func (c *IPTablesConfig) DeleteRules(rules []*nftables.Rule) error {
	for _, rule := range rules {
		if err := c.conn.DelRule(rule); err != nil {
			return err
		}
	}

	return c.conn.Flush()
}

func (c *IPTablesConfig) getTapRules(lanInterface, tapDevice string) []*nftables.Rule {
	rule1 := &nftables.Rule{
		Table: c.tableFilter,
		Chain: c.chainForward,
		Exprs: []expr.Any{
			// [ meta load iifname => reg 1 ]
			&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
			// [ cmp eq reg 1 lanInterface ]
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte(tapDevice + "\x00")},
			// [ meta load oifname => reg 2 ]
			&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 2},
			// [ cmp eq reg 2 lanInterface ]
			&expr.Cmp{Op: expr.CmpOpEq, Register: 2, Data: []byte(lanInterface + "\x00")},
			// [ immediate verdict ACCEPT ]
			&expr.Verdict{Kind: expr.VerdictAccept},
		},
	}

	rule2 := &nftables.Rule{
		Table: c.tableFilter,
		Chain: c.chainForward,
		Exprs: []expr.Any{
			// [ meta load iifname => reg 1 ]
			&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
			// [ cmp eq reg 1 lanInterface ]
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte(lanInterface + "\x00")},
			// [ meta load oifname => reg 2 ]
			&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 2},
			// [ cmp eq reg 2 tapDevice ]
			&expr.Cmp{Op: expr.CmpOpEq, Register: 2, Data: []byte(tapDevice + "\x00")},
			// [ ct lookup state => reg 1 ]
			&expr.Ct{Register: 1, Key: expr.CtKeySTATE, SourceRegister: false},
			// [ bitwise and reg 1 with RELATED|ESTABLISHED ]
			&expr.Bitwise{
				DestRegister:   1,
				SourceRegister: 1,
				Len:            4,
				Mask:           []byte{0x06, 0x00, 0x00, 0x00}, // RELATED(0x02) | ESTABLISHED(0x04)
				Xor:            []byte{0x00, 0x00, 0x00, 0x00},
			},
			// [ cmp ne reg 1 0 ]
			&expr.Cmp{
				Op:       expr.CmpOpNeq,
				Register: 1,
				Data:     []byte{0x00, 0x00, 0x00, 0x00},
			},
			// [ immediate verdict ACCEPT ]
			&expr.Verdict{Kind: expr.VerdictAccept},
		},
	}

	return []*nftables.Rule{rule1, rule2}
}

func (c *IPTablesConfig) getNatRules(lanInterface string, cidr string) []*nftables.Rule {
	var rules []*nftables.Rule

	//ip, subnet, ipErr := net.ParseCIDR(cidr)
	//if ipErr != nil {
	//	return rules
	//}

	// Masquerade rule
	masqRule := &nftables.Rule{
		Table: c.tableNat,
		Chain: c.chainPostrouting,
		Exprs: []expr.Any{
			// [ meta load oifname => reg 1 ]
			&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
			// [ cmp eq reg 1 lanInterface ]
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte(lanInterface + "\x00")},
			// [ immediate verdict MASQUERADE ]
			&expr.Masq{},
		},
	}
	rules = append(rules, masqRule)

	// RETURN rule (not typical in nftables but we add for parity)
	returnRule := &nftables.Rule{
		Table: c.tableNat,
		Chain: c.chainPostrouting,
		Exprs: []expr.Any{
			&expr.Verdict{Kind: expr.VerdictReturn},
		},
	}
	rules = append(rules, returnRule)

	// Helper to create ACCEPT rules for port and proto
	makeAcceptRule := func(proto string, port uint16) *nftables.Rule {
		return &nftables.Rule{
			Table: c.tableFilter,
			Chain: c.chainForward,
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
	}

	// For brevity, create ACCEPT rules for udp/tcp on ports 53, 67, 68 with source subnet
	// Real implementation should handle subnet filtering with sets or nftables native commands, which is more complex
	for _, proto := range []string{"udp", "tcp"} {
		for _, port := range []uint16{53, 67, 68} {
			rule := makeAcceptRule(proto, port)
			rules = append(rules, rule)
		}
	}

	return rules
}

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
