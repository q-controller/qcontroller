package cmd

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

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
		slog.Debug("Read config", "config", config)

		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)

		done := make(chan struct{})
		go func() {
			<-stop
			close(done)
		}()

		var subnet *net.IPNet
		var scannerIfcName string

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
