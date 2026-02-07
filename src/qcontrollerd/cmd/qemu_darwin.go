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

		if config.GetMacosSettings() == nil || config.GetMacosSettings().Subnet == "" {
			return errors.New("macOS settings are not configured")
		}

		_, ipNet, ipErr := net.ParseCIDR(config.GetMacosSettings().Subnet)
		if ipErr != nil {
			return fmt.Errorf("invalid subnet format: %w", ipErr)
		}

		scanner, scannerErr := arp.NewScanner("", ipNet)
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
