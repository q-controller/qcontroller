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

	"github.com/q-controller/network-utils/src/utils/network/dhcp"
	"github.com/q-controller/network-utils/src/utils/network/ifc"
	"github.com/q-controller/network-utils/src/utils/network/network"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qcontroller/src/pkg/utils"

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
			dns := []net.IP{}
			for _, ipStr := range config.GetLinuxSettings().Network.Dhcp.Dns {
				ip := net.ParseIP(ipStr)
				if ip == nil {
					return fmt.Errorf("failed to parse dns ip %s", ipStr)
				}
				dns = append(dns, ip)
			}

			dhcpServer, dhcpServerErr := dhcp.StartDHCPServer(
				dhcp.WithDNS(dns...),
				dhcp.WithInterface(linuxConfig.Name, linuxConfig.BridgeIp),
				dhcp.WithLeaseFile(config.GetLinuxSettings().Network.Dhcp.LeaseFile),
				dhcp.WithRange(linuxConfig.StartIp, linuxConfig.EndIp),
			)
			if dhcpServerErr != nil {
				return fmt.Errorf("failed to start DHCP server: %w", dhcpServerErr)
			}
			defer dhcpServer.Stop()

			return Entrypoint(config, done)
		} else {
			netw, netwErr := network.NewNetwork(
				network.WithName(linuxConfig.Name),
				network.WithGateway(linuxConfig.GatewayIp),
				network.WithBridge(linuxConfig.BridgeIp),
				network.WithSubnet(linuxConfig.Subnet),
				network.WithLinkManager(ifc.NetlinkBridgeManager{}),
			)
			if netwErr != nil {
				return fmt.Errorf("failed to create network: %w", netwErr)
			}

			subscription, subscribeErr := ifc.SubscribeDefaultInterfaceChanges()
			if subscribeErr != nil {
				return fmt.Errorf("failed to subscribe to default interface changes: %w", subscribeErr)
			}
			defer subscription.Stop()

			go func() {
				for iface := range subscription.InterfaceCh {
					slog.Info("Default interface changed", "interface", iface)
					if err := netw.Connect(iface); err != nil {
						slog.Error("Failed to connect network to interface", "interface", iface, "error", err)
					}
				}
			}()

			args := append([]string(nil), os.Args[1:]...) // clone without program name
			args = append(args, "--in-namespace", linuxConfig.Name)

			cmd := exec.Command(os.Args[0], args...)
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
