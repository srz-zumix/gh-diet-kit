package pr

import (
	"github.com/spf13/cobra"
	"github.com/srz-zumix/gh-diet-kit/cmd/pr/assets"
)

// NewAssetsCmd returns the cobra.Command for the pr assets subcommand group.
func NewAssetsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "assets",
		Short: "Manage media assets attached to pull requests",
		Long: `Commands for finding, downloading, and restoring media assets
(images and videos) that are embedded in pull request bodies,
issue comments, and review comments.`,
	}
	cmd.AddCommand(assets.NewListCmd())
	cmd.AddCommand(assets.NewDumpCmd())
	cmd.AddCommand(assets.NewRestoreCmd())
	return cmd
}
