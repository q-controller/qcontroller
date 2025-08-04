//go:build linux
// +build linux

package firewall

import (
	"github.com/google/nftables"
	"github.com/google/nftables/expr"
)

func ForwardOutboundRule(chainName, tableName, hostIf, internalIf string) NewRule {
	return func(rules *Rules) error {
		chain, table, chainErr := NewChain(
			WithName(chainName),
			WithinTable(tableName),
		)
		if chainErr != nil {
			return chainErr
		}

		rule := &nftables.Rule{
			Table: table,
			Chain: chain,
			Exprs: []expr.Any{
				// [ meta load iifname => reg 1 ]
				&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
				// [ cmp eq reg 1 interfaceA ]
				&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte(internalIf + "\x00")},
				// [ meta load oifname => reg 2 ]
				&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 2},
				// [ cmp eq reg 2 interfaceB ]
				&expr.Cmp{Op: expr.CmpOpEq, Register: 2, Data: []byte(hostIf + "\x00")},
				// [ immediate verdict ACCEPT ]
				&expr.Verdict{Kind: expr.VerdictAccept},
			},
		}

		rules.rules = append(rules.rules, rule)
		return nil
	}
}

func ForwardReturnTrafficRule(chainName, tableName, hostIf, internalIf string) NewRule {
	return func(rules *Rules) error {
		chain, table, chainErr := NewChain(
			WithName(chainName),
			WithinTable(tableName),
		)
		if chainErr != nil {
			return chainErr
		}

		rule := &nftables.Rule{
			Table: table,
			Chain: chain,
			Exprs: []expr.Any{
				// [ meta load iifname => reg 1 ]
				&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
				// [ cmp eq reg 1 interfaceB ]
				&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte(hostIf + "\x00")},
				// [ meta load oifname => reg 2 ]
				&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 2},
				// [ cmp eq reg 2 interfaceA ]
				&expr.Cmp{Op: expr.CmpOpEq, Register: 2, Data: []byte(internalIf + "\x00")},
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

		rules.rules = append(rules.rules, rule)
		return nil
	}
}
