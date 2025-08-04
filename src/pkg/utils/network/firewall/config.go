//go:build linux
// +build linux

package firewall

func ConfigureFirewall(oldInterface, newInterface, subnet string) error {
	// Configure iptables
	config, configErr := NewIPTablesConfig()
	if configErr != nil {
		return configErr
	}

	if oldInterface != "" {
		if err := config.DeleteRules(getNatRules(oldInterface, subnet)); err != nil {
			return err
		}
	}

	if newInterface != "" {
		if err := config.AddRules(getNatRules(newInterface, subnet)); err != nil {
			return err
		}
	}

	return nil
}

func ConfigureTap(oldInterface, newInterface, tap string) error {
	// Configure iptables
	config, configErr := NewIPTablesConfig()
	if configErr != nil {
		return configErr
	}

	if oldInterface != "" {
		if err := config.DeleteRules(getTapRules(oldInterface, tap)); err != nil {
			return err
		}
	}

	if newInterface != "" {
		if err := config.AddRules(getTapRules(newInterface, tap)); err != nil {
			return err
		}
	}

	return nil
}
