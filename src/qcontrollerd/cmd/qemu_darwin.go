package cmd

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

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

		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)

		done := make(chan struct{})
		go func() {
			<-stop
			close(done)
		}()

		return Entrypoint(config, done)
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
