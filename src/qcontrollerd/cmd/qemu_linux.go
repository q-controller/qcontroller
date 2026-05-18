package cmd

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/q-controller/network-utils/src/utils/network/dhcp"
	"github.com/q-controller/network-utils/src/utils/network/ifc"
	"github.com/q-controller/network-utils/src/utils/network/network"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qcontroller/src/pkg/utils"
	"github.com/q-controller/qcontroller/src/pkg/utils/network/arp"
	"github.com/vishvananda/netlink"

	dnsresolver "github.com/q-controller/network-utils/src/utils/network/dns"
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
		slog.Debug("Read config", "config", config)

		linuxConfig, linuxConfigErr := NewLinuxConfig(config.GetLinuxSettings())
		if linuxConfigErr != nil {
			return fmt.Errorf("failed to create linux config: %w", linuxConfigErr)
		}

		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)

		done := make(chan struct{})
		go func() {
			<-stop
			close(done)
		}()

		inNamespace, inNamespaceErr := cmd.Flags().GetBool("in-namespace")
		if inNamespaceErr != nil {
			return fmt.Errorf("failed to get in-namespace flag: %w", inNamespaceErr)
		}

		if inNamespace {
			scanner, scannerErr := arp.NewScanner(linuxConfig.Name, linuxConfig.Subnet)
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

			return Entrypoint(config, resolver, done)
		} else {
			ip, _, ipErr := net.ParseCIDR(config.GetLinuxSettings().Network.GatewayIp)
			if ipErr != nil {
				return fmt.Errorf("failed to parse bridge ip: %w", ipErr)
			}

			netw, netwErr := network.NewNetwork(
				network.WithName(linuxConfig.Name),
				network.WithGateway(linuxConfig.GatewayIP),
				network.WithBridge(linuxConfig.BridgeIP),
				network.WithSubnet(linuxConfig.Subnet),
				network.WithLinkManager(ifc.NetlinkBridgeManager{}),
			)
			if netwErr != nil {
				return fmt.Errorf("failed to create network: %w", netwErr)
			}

			dhcpServer, dhcpServerErr := dhcp.StartDHCPServer(
				dhcp.WithDNS(linuxConfig.GatewayIP),
				dhcp.WithRouter(linuxConfig.GatewayIP),
				dhcp.WithLeaseFile(config.GetLinuxSettings().Network.Dhcp.LeaseFile),
				dhcp.WithRange(linuxConfig.StartIP, linuxConfig.EndIP),
			)
			if dhcpServerErr != nil {
				return fmt.Errorf("failed to start DHCP server: %w", dhcpServerErr)
			}
			defer dhcpServer.Stop()

			if dnsCfg := config.GetLinuxSettings().Network.Dns; dnsCfg != nil {
				opts := []dnsresolver.DNSForwarderOption{
					dnsresolver.WithForwarderAddress(ip.String() + ":53"),
					dnsresolver.WithForwarderTimeout(2 * time.Second),
				}
				switch v := dnsCfg.Upstream.(type) {
				case *settingsv1.Dns_ResolvConf:
					opts = append(opts, dnsresolver.WithResolvconfPath(v.ResolvConf))
				case *settingsv1.Dns_Static:
					opts = append(opts, dnsresolver.WithUpstreams(v.Static.GetEndpoints()))
				}
				forwarder, forwarderErr := dnsresolver.NewDNSFailoverForwarder(cmd.Context(), opts...)
				if forwarderErr != nil {
					return fmt.Errorf("failed to create dns forwarder: %w", forwarderErr)
				}
				stop, serveErr := forwarder.Serve()
				if serveErr != nil {
					return fmt.Errorf("failed to start dns forwarder: %w", serveErr)
				}
				defer stop()
			}

			subscription, subscribeErr := ifc.SubscribeDefaultInterfaceChanges()
			if subscribeErr != nil {
				return fmt.Errorf("failed to subscribe to default interface changes: %w", subscribeErr)
			}
			defer subscription.Stop()

			// Reactive firewall reconcile: Docker (and other firewall tools)
			// can flush the standard FORWARD/POSTROUTING chains on reload,
			// removing our rules. A single goroutine owns the iface state
			// and re-applies on default-iface changes, link changes, and a
			// periodic safety tick. netw.Connect is idempotent.
			go func() {
				linkCh := make(chan netlink.LinkUpdate, 16)
				linkDone := make(chan struct{})
				defer close(linkDone)
				if err := netlink.LinkSubscribe(linkCh, linkDone); err != nil {
					slog.Warn("Failed to subscribe to link changes; reconcile relies on ticker only", "error", err)
					linkCh = nil
				}

				ticker := time.NewTicker(30 * time.Second)
				defer ticker.Stop()

				subCh := subscription.InterfaceCh

				var iface string
				for {
					select {
					case <-done:
						return
					case newIface, ok := <-subCh:
						if !ok {
							slog.Warn("Default-interface subscription closed; reconcile continues with ticker and link events")
							subCh = nil
							continue
						}
						slog.Info("Default interface changed", "interface", newIface)
						iface = newIface
					case <-linkCh:
						// Drain pending link events so a burst (Docker
						// creating many veth/bridge interfaces in quick
						// succession) coalesces into a single Connect.
					drain:
						for {
							select {
							case <-linkCh:
							default:
								break drain
							}
						}
					case <-ticker.C:
					}
					if iface == "" {
						continue
					}
					if err := netw.Connect(iface, true); err != nil {
						slog.Warn("Apply forwarding rules failed", "interface", iface, "error", err)
					}
				}
			}()

			args := append([]string(nil), os.Args[1:]...) // clone without program name
			args = append(args, "--in-namespace", linuxConfig.Name)

			// Re-execing the same binary with our own args; os.Args is trusted.
			cmd := exec.Command(os.Args[0], args...) //nolint:gosec // G204: re-exec self
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

			// Ensure child process receives SIGTERM if parent dies
			cmd.SysProcAttr = &syscall.SysProcAttr{
				Pdeathsig: syscall.SIGTERM,
			}
			if err := netw.Execute(func() error {
				if err := cmd.Start(); err != nil {
					return fmt.Errorf("failed to start qemu command: %w", err)
				}
				return nil
			}); err != nil {
				return fmt.Errorf("failed to execute network command: %w", err)
			}

			<-done
		}

		return nil
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
