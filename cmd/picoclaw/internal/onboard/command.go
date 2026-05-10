package onboard

import (
	"embed"

	"github.com/spf13/cobra"
)

//go:generate go run ../../../../scripts/copydir.go ../../../../workspace ./workspace
//go:embed workspace
var embeddedFiles embed.FS

func NewOnboardCommand() *cobra.Command {
	var encrypt bool
	var resetPrompts bool

	cmd := &cobra.Command{
		Use:     "onboard",
		Aliases: []string{"o"},
		Short:   "Initialize picoclaw configuration and workspace",
		// Run without subcommands → original onboard flow
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) == 0 {
				onboard(encrypt, resetPrompts)
			} else {
				_ = cmd.Help()
			}
		},
	}

	cmd.Flags().BoolVar(&encrypt, "enc", false,
		"Enable credential encryption (generates SSH key and prompts for passphrase)")
	cmd.Flags().BoolVar(&resetPrompts, "reset-prompts", false,
		"Reset prompts to defaults (overwrite user-edited prompts)")

	return cmd
}
