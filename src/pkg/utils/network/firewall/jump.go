//go:build linux
// +build linux

package firewall

import (
	"fmt"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
)

func AddJumpRule(fromChainName, toChainName, tableName string) error {
	fromChain, table, fromChainErr := NewChain(
		WithName(fromChainName),
		WithinTable(tableName),
		Create(),
	)
	if fromChainErr != nil {
		return fmt.Errorf("failed to create or get chain %s: %w", fromChainName, fromChainErr)
	}

	toChain, _, toChainErr := NewChain(
		WithName(toChainName),
		WithinTable(tableName),
		Create(),
	)
	if toChainErr != nil {
		return fmt.Errorf("failed to create or get chain %s: %w", toChainName, toChainErr)
	}

	rules, rulesErr := getConnection().GetRules(table, fromChain)
	if rulesErr != nil {
		return rulesErr
	}

	// Check if jump to customChain already exists anywhere in FORWARD chain
	for _, r := range rules {
		for _, e := range r.Exprs {
			if jump, ok := e.(*expr.Verdict); ok && jump.Kind == expr.VerdictJump && jump.Chain == toChain.Name {
				// Jump already present, no insertion needed
				return nil
			}
		}
	}

	var firstRuleHandle uint32
	if len(rules) > 0 {
		firstRuleHandle = uint32(rules[0].Handle)
	}

	jumpRule := &nftables.Rule{
		Table: table,
		Chain: fromChain,
		Exprs: []expr.Any{
			&expr.Verdict{Kind: expr.VerdictJump, Chain: toChain.Name},
		},
	}

	if firstRuleHandle != 0 {
		jumpRule.Handle = uint64(firstRuleHandle)
	}

	getConnection().InsertRule(jumpRule)

	return getConnection().Flush()
}
