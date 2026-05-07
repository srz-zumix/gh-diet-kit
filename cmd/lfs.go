package cmd

import (
	"github.com/spf13/cobra"
	"github.com/srz-zumix/gh-diet-kit/cmd/lfs"
)

func init() {
	rootCmd.AddCommand(NewLFSCmd())
}

// NewLFSCmd returns the cobra.Command for the lfs subcommand.
// It groups commands for Git Large File Storage (LFS) analysis.
func NewLFSCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lfs",
		Short: "Commands for Git LFS analysis",
		Long: `Commands for analyzing Git Large File Storage (LFS) in a repository.

Use the subcommands to detect files that should be tracked by LFS and estimate git storage savings from migrating files to LFS.`,
	}
	cmd.AddCommand(lfs.NewDetectCmd())
	cmd.AddCommand(lfs.NewEstimateCmd())
	return cmd
}
