//go:build linux
// +build linux

package firewall

import (
	"fmt"

	"github.com/coreos/go-iptables/iptables"
)

// IPTablesConfig represents the configuration for iptables rules
type IPTablesConfig struct {
	IPT *iptables.IPTables
}

// Rule represents a single iptables rule
type Rule struct {
	Table string
	Chain string
	Spec  []string
}

// NewIPTablesConfig initializes a new IPTablesConfig
func NewIPTablesConfig() (*IPTablesConfig, error) {
	ipt, err := iptables.New()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize iptables: %v", err)
	}
	return &IPTablesConfig{
		IPT: ipt,
	}, nil
}

// Configure applies all iptables rules
func (c *IPTablesConfig) AddRules(rules []Rule) error {
	for _, rule := range rules {
		if err := c.addRule(rule); err != nil {
			return fmt.Errorf("failed to add rule %v: %v", rule.Spec, err)
		}
	}
	return nil
}

func (c *IPTablesConfig) DeleteRules(rules []Rule) error {
	for _, rule := range rules {
		if err := c.deleteRule(rule); err != nil {
			return fmt.Errorf("failed to delete rule %v: %v", rule.Spec, err)
		}
	}
	return nil
}

// addRule adds a single rule if it doesn't exist
func (c *IPTablesConfig) addRule(rule Rule) error {
	exists, err := c.IPT.Exists(rule.Table, rule.Chain, rule.Spec...)
	if err != nil {
		return fmt.Errorf("failed to check rule existence: %v", err)
	}
	if !exists {
		// Use Insert for FORWARD chain, Append for POSTROUTING
		if rule.Chain == "FORWARD" {
			return c.IPT.Insert(rule.Table, rule.Chain, 1, rule.Spec...)
		}
		return c.IPT.Append(rule.Table, rule.Chain, rule.Spec...)
	}
	return nil
}

func (c *IPTablesConfig) deleteRule(rule Rule) error {
	return c.IPT.DeleteIfExists(rule.Table, rule.Chain, rule.Spec...)
}

func getTapRules(lanInterface, tapDevice string) []Rule {
	return []Rule{
		{"filter", "FORWARD", []string{"-i", tapDevice, "-o", lanInterface, "-j", "ACCEPT"}},
		{"filter", "FORWARD", []string{"-i", lanInterface, "-o", tapDevice, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"}},
	}
}

func getNatRules(lanInterface, subnet string) []Rule {
	return []Rule{
		{"nat", "POSTROUTING", []string{"-o", lanInterface, "-j", "MASQUERADE"}},
		{"nat", "POSTROUTING", []string{"-j", "RETURN"}},
		{"filter", "FORWARD", []string{"-p", "udp", "--dport", "53", "-s", subnet, "-j", "ACCEPT"}},
		{"filter", "FORWARD", []string{"-p", "tcp", "--dport", "53", "-s", subnet, "-j", "ACCEPT"}},
		{"filter", "FORWARD", []string{"-p", "udp", "--dport", "67", "-s", subnet, "-j", "ACCEPT"}},
		{"filter", "FORWARD", []string{"-p", "tcp", "--dport", "67", "-s", subnet, "-j", "ACCEPT"}},
		{"filter", "FORWARD", []string{"-p", "udp", "--dport", "68", "-s", subnet, "-j", "ACCEPT"}},
		{"filter", "FORWARD", []string{"-p", "tcp", "--dport", "68", "-s", subnet, "-j", "ACCEPT"}},
	}
}
