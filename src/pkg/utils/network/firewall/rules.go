//go:build linux
// +build linux

package firewall

import (
	"bytes"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
)

type Rules struct {
	rules []*nftables.Rule
}

type NewRule func(*Rules) error

func NewRules(rules ...NewRule) (*Rules, error) {
	r := &Rules{}
	for _, rule := range rules {
		if err := rule(r); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func AddRules(rules *Rules) error {
	conn := getConnection()

	for _, r := range rules.rules {
		existing, getRulesErr := conn.GetRules(r.Table, r.Chain)
		if getRulesErr != nil {
			return getRulesErr
		}

		unique := true
		for _, er := range existing {
			if equalExprs(r.Exprs, er.Exprs) {
				unique = false
				break
			}
		}

		if unique {
			conn.AddRule(r)
		}
	}

	return conn.Flush()
}

func RemoveRules(rules *Rules) error {
	conn := getConnection()
	for _, rule := range rules.rules {
		if err := conn.DelRule(rule); err != nil {
			return err
		}
	}

	return conn.Flush()
}

func equalExprs(a, b []expr.Any) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if !exprEqual(a[i], b[i]) {
			return false
		}
	}

	return true
}

func exprEqual(a, b expr.Any) bool {
	switch x := a.(type) {
	case *expr.Meta:
		y, ok := b.(*expr.Meta)
		return ok &&
			x.Key == y.Key &&
			x.Register == y.Register

	case *expr.Cmp:
		y, ok := b.(*expr.Cmp)
		return ok &&
			x.Op == y.Op &&
			x.Register == y.Register &&
			bytes.Equal(x.Data, y.Data)

	case *expr.Ct:
		y, ok := b.(*expr.Ct)
		return ok &&
			x.Register == y.Register &&
			x.Key == y.Key &&
			x.SourceRegister == y.SourceRegister

	case *expr.Bitwise:
		y, ok := b.(*expr.Bitwise)
		return ok &&
			x.DestRegister == y.DestRegister &&
			x.SourceRegister == y.SourceRegister &&
			x.Len == y.Len &&
			bytes.Equal(x.Mask, y.Mask) &&
			bytes.Equal(x.Xor, y.Xor)

	case *expr.Verdict:
		y, ok := b.(*expr.Verdict)
		return ok &&
			x.Kind == y.Kind &&
			x.Chain == y.Chain

	case *expr.Masq:
		_, ok := b.(*expr.Masq)
		return ok

	case *expr.Payload:
		y, ok := b.(*expr.Payload)
		return ok &&
			x.DestRegister == y.DestRegister &&
			x.Base == y.Base &&
			x.Offset == y.Offset &&
			x.Len == y.Len

	default:
		return false
	}
}
