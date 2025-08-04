package cmd

import (
	"log/slog"
	"os"

	"github.com/krjakbrjak/qcontroller/src/pkg/logging"
	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "qcontrollerd",
	Short: "A tool controlling and managing the lifecycle of QEMU virtual machine instances",
}

func Execute() {
	slog.SetDefault(logging.CreateLogger(slog.LevelDebug))
	err := rootCmd.Execute()
	if err != nil {
		slog.Error("failed to execute command", "error", err)
		os.Exit(1)
	}
}

func init() {}
