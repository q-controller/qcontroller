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
		if err := config.DeleteRules(config.getNatRules(oldInterface, subnet)); err != nil {
			return err
		}
	}

	if newInterface != "" {
		if err := config.AddRules(config.getNatRules(newInterface, subnet)); err != nil {
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
		if err := config.DeleteRules(config.getTapRules(oldInterface, tap)); err != nil {
			return err
		}
	}

	if newInterface != "" {
		if err := config.AddRules(config.getTapRules(newInterface, tap)); err != nil {
			return err
		}
	}

	return nil
}
