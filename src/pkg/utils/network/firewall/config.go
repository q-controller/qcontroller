//go:build linux
// +build linux

package firewall

func ConfigureFirewall(oldInterface, newInterface, bridgeName string) error {
	if jumpErr := AddJumpRule("FORWARD", "QEMU-FORWARD", "filter"); jumpErr != nil {
		return jumpErr
	}

	if jumpErr := AddJumpRule("INPUT", "QEMU-INPUT", "filter"); jumpErr != nil {
		return jumpErr
	}

	if oldInterface != "" {
		rules, rulesErr := NewRules(
			ForwardOutboundRule("QEMU-FORWARD", "filter", oldInterface, bridgeName),
			ForwardReturnTrafficRule("QEMU-FORWARD", "filter", oldInterface, bridgeName),
			MasqueradeRule("POSTROUTING", "nat", oldInterface),
			PortRule(53, "udp", "QEMU-INPUT", "filter"),
			PortRule(67, "udp", "QEMU-INPUT", "filter"),
			PortRule(68, "udp", "QEMU-INPUT", "filter"),
			PortRule(53, "tcp", "QEMU-INPUT", "filter"),
			PortRule(67, "tcp", "QEMU-INPUT", "filter"),
			PortRule(68, "tcp", "QEMU-INPUT", "filter"),
		)
		if rulesErr != nil {
			return rulesErr
		}
		if removeRules := RemoveRules(rules); removeRules != nil {
			return removeRules
		}
	}

	if newInterface != "" {
		rules, rulesErr := NewRules(
			ForwardOutboundRule("QEMU-FORWARD", "filter", newInterface, bridgeName),
			ForwardReturnTrafficRule("QEMU-FORWARD", "filter", newInterface, bridgeName),
			MasqueradeRule("POSTROUTING", "nat", newInterface),
			PortRule(53, "udp", "QEMU-INPUT", "filter"),
			PortRule(67, "udp", "QEMU-INPUT", "filter"),
			PortRule(68, "udp", "QEMU-INPUT", "filter"),
			PortRule(53, "tcp", "QEMU-INPUT", "filter"),
			PortRule(67, "tcp", "QEMU-INPUT", "filter"),
			PortRule(68, "tcp", "QEMU-INPUT", "filter"),
		)
		if rulesErr != nil {
			return rulesErr
		}
		if removeRules := AddRules(rules); removeRules != nil {
			return removeRules
		}
	}

	return nil
}
