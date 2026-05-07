package cmd

import (
	"github.com/spf13/cobra"
	"github.com/srz-zumix/gh-diet-kit/cmd/tree"
)

func init() {
	rootCmd.AddCommand(NewTreeCmd())
}

// NewTreeCmd returns the cobra.Command for the tree subcommand.
// It groups commands for git tree structure analysis.
func NewTreeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tree",
		Short: "Commands for git tree structure analysis",
		Long: `Commands for analyzing the git tree structure of a repository.

Use the subcommands to detect directories with many entries that may be
contributing to large tree object storage in git.`,
	}
	cmd.AddCommand(tree.NewDetectCmd())
	return cmd
}
