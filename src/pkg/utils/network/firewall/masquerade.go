//go:build linux
// +build linux

package firewall

import (
	"github.com/google/nftables"
	"github.com/google/nftables/expr"
)

func MasqueradeRule(chainName, tableName, interfaceName string) NewRule {
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
				// [ meta load oifname => reg 1 ]
				&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
				// [ cmp eq reg 1 lanInterface ]
				&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte(interfaceName + "\x00")},
				// [ immediate verdict MASQUERADE ]
				&expr.Masq{},
			},
		}

		rules.rules = append(rules.rules, masqRule)
		return nil
	}
}
