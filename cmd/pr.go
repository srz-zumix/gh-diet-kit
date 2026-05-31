package cmd

import (
	"github.com/spf13/cobra"
	"github.com/srz-zumix/gh-diet-kit/cmd/pr"
)

func init() {
	rootCmd.AddCommand(NewPRCmd())
}

// NewPRCmd returns the cobra.Command for the pr subcommand group.
// It groups commands that operate on pull request data, such as media assets.
func NewPRCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "Commands for managing pull request data",
		Long:  `Commands for inspecting and managing pull request data such as media assets.`,
	}
	cmd.AddCommand(pr.NewAssetsCmd())
	return cmd
}
