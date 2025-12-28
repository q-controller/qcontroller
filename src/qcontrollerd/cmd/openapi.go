package cmd

import (
	"fmt"

	"github.com/q-controller/qcontroller/src/qcontrollerd/cmd/utils"
	"github.com/spf13/cobra"
)

var openapiCmd = &cobra.Command{
	Use:    "openapi",
	Short:  "Produces OpenAPI specifications for the gateway service",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		bytes, bytesErr := utils.GenerateOpenAPISpecs()
		if bytesErr != nil {
			return fmt.Errorf("failed to generate OpenAPI specs: %w", bytesErr)
		}

		fmt.Println(bytes)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(openapiCmd)
}
