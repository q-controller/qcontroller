package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"

	"syscall"
	"time"

	dnsresolver "github.com/q-controller/network-utils/src/utils/network/dns"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qcontroller/src/pkg/utils"
	"github.com/q-controller/qcontroller/src/pkg/utils/network"
	"github.com/q-controller/qcontroller/src/pkg/utils/network/arp"

	"github.com/spf13/cobra"
)

var qemuCmd = &cobra.Command{
	Use:   "qemu",
	Short: "Starts QEMU client",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if os.Geteuid() != 0 {
			return errors.New("this command must be run as root")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath, configPathErr := cmd.Flags().GetString("config")
		if configPathErr != nil {
			return fmt.Errorf("failed to get config: %w", configPathErr)
		}

		config := &settingsv1.QemuConfig{}
		if unmarshalErr := utils.Unmarshal(config, configPath); unmarshalErr != nil {
			return unmarshalErr
		}

		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)

		done := make(chan struct{})
		go func() {
			<-stop
			close(done)
		}()

		var subnet *net.IPNet
		var scannerIfcName string
		var dnsListenIP net.IP

		macosSettings := config.GetMacosSettings()
		if macosSettings != nil && macosSettings.Mode == settingsv1.MacosSettings_MODE_BRIDGED {
			// Bridged mode: use specified interface (or default) and get its subnet
			bridgeInterface, bridgeErr := network.GetBridgeInterface(config)
			if bridgeErr != nil {
				return fmt.Errorf("failed to get bridge interface: %w", bridgeErr)
			}

			iface, ifaceErr := net.InterfaceByName(bridgeInterface)
			if ifaceErr != nil {
				return fmt.Errorf("failed to get interface by name: %w", ifaceErr)
			}
			addrs, addrsErr := iface.Addrs()
			if addrsErr != nil {
				return fmt.Errorf("failed to get interface addresses: %w", addrsErr)
			}
			if len(addrs) == 0 {
				return fmt.Errorf("no addresses found for interface %s", bridgeInterface)
			}
			ipNet, ok := addrs[0].(*net.IPNet)
			if !ok {
				return fmt.Errorf("address is not an IPNet: %v", addrs[0])
			}
			subnet = ipNet
			scannerIfcName = bridgeInterface
			dnsListenIP = ipNet.IP.To4()
		} else {
			// Shared mode: get subnet from config, auto-discover interface
			if macosSettings == nil || macosSettings.Shared == nil || macosSettings.Shared.Subnet == "" {
				return errors.New("macOS shared mode requires subnet configuration")
			}
			_, ipNet, parseErr := net.ParseCIDR(macosSettings.Shared.Subnet)
			if parseErr != nil {
				return fmt.Errorf("invalid subnet in macOS settings: %w", parseErr)
			}
			subnet = ipNet
			scannerIfcName = "" // auto-discover interface by subnet
		}

		scanner, scannerErr := arp.NewScanner(scannerIfcName, subnet)
		if scannerErr != nil {
			return fmt.Errorf("failed to create arp scanner: %w", scannerErr)
		}

		resolver, resolverErr := arp.NewResolver(
			cmd.Context(),
			arp.WithInterval(2*time.Second),
			arp.WithTimeout(2*time.Second),
			arp.WithScanner(scanner),
		)
		if resolverErr != nil {
			return fmt.Errorf("failed to create arp resolver: %w", resolverErr)
		}
		defer resolver.Close()

		if dnsCfg := macosSettings.GetDns(); dnsCfg != nil {
			var forwarder dnsresolver.DNSForwarder
			if dnsListenIP != nil {
				// Bridged mode: host IP is known, create forwarder directly
				f, fErr := newDNSForwarder(cmd.Context(), dnsListenIP.String(), dnsCfg)
				if fErr != nil {
					return fmt.Errorf("failed to create dns forwarder: %w", fErr)
				}
				forwarder = f
			} else {
				// Shared mode: vmnet interface appears only when a VM starts.
				// ManagedForwarder polls for it and restarts on interface changes.
				forwarder = dnsresolver.NewManagedForwarder(
					cmd.Context(),
					2*time.Second,
					&subnetProber{subnet: subnet},
					&dnsForwarderFactory{dnsCfg: dnsCfg},
				)
			}
			stop, serveErr := forwarder.Serve()
			if serveErr != nil {
				return fmt.Errorf("failed to start dns forwarder: %w", serveErr)
			}
			defer stop()
		}

		return Entrypoint(config, resolver, done)
	},
}

func init() {
	rootCmd.AddCommand(qemuCmd)

	qemuCmd.Flags().StringP("config", "c", "", "Path to qemu's config file")
	if err := qemuCmd.MarkFlagRequired("config"); err != nil {
		panic(fmt.Errorf("failed to mark flag `config` as required: %w", err))
	}

	qemuCmd.Flags().Bool("in-namespace", false, "Run QEMU inside network namespace")
	if err := qemuCmd.Flags().MarkHidden("in-namespace"); err != nil {
		panic(fmt.Errorf("failed to mark flag `in-namespace` as hidden: %w", err))
	}
}

// newDNSForwarder creates a DNS forwarder configured for the given address.
func newDNSForwarder(ctx context.Context, listenAddr string, dnsCfg *settingsv1.Dns) (dnsresolver.DNSForwarder, error) {
	opts := []dnsresolver.DNSForwarderOption{
		dnsresolver.WithForwarderAddress(fmt.Sprintf("%s:53", listenAddr)),
		dnsresolver.WithForwarderTimeout(2 * time.Second),
		dnsresolver.WithReusePort(),
	}
	switch v := dnsCfg.Upstream.(type) {
	case *settingsv1.Dns_ResolvConf:
		opts = append(opts, dnsresolver.WithResolvconfPath(v.ResolvConf))
	case *settingsv1.Dns_Static:
		opts = append(opts, dnsresolver.WithUpstreams(v.Static.GetEndpoints()))
	}
	return dnsresolver.NewDNSFailoverForwarder(ctx, opts...)
}

// subnetProber implements dnsresolver.InterfaceProber by scanning for a host
// IP on the given subnet.
type subnetProber struct {
	subnet *net.IPNet
}

func (p *subnetProber) Probe() (net.IP, error) {
	return findHostIPForSubnet(p.subnet)
}

// dnsForwarderFactory implements dnsresolver.ForwarderFactory using the
// project's DNS config.
type dnsForwarderFactory struct {
	dnsCfg *settingsv1.Dns
}

func (f *dnsForwarderFactory) NewForwarder(ctx context.Context, addr string) (dnsresolver.DNSForwarder, error) {
	return newDNSForwarder(ctx, addr, f.dnsCfg)
}

// findHostIPForSubnet finds the host's IPv4 address on the interface matching the given subnet.
func findHostIPForSubnet(subnet *net.IPNet) (net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("failed to list interfaces: %w", err)
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, addrsErr := iface.Addrs()
		if addrsErr != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ip4 := ipNet.IP.To4(); ip4 != nil && subnet.Contains(ip4) {
				return ip4, nil
			}
		}
	}

	return nil, fmt.Errorf("no interface found for subnet %s", subnet)
}
