package main

import (
	"github.com/liza-mas/liza/internal/commands"
	"github.com/spf13/cobra"
)

var validateSpecType string

var validateSpecCmd = &cobra.Command{
	Use:   "validate-spec <spec-path>",
	Short: "Validate a spec file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		specType := validateSpecType
		if specType == "" {
			specType = inferSpecType(args[0])
		}
		return commands.ValidateSpecCommand(args[0], specType)
	},
}

func init() {
	validateSpecCmd.Flags().StringVar(&validateSpecType, "type", "", "spec type: vision or delivery (defaults to filename inference)")
}