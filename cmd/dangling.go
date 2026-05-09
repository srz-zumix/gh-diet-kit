package cmd

import (
	"github.com/spf13/cobra"
	"github.com/srz-zumix/gh-diet-kit/cmd/dangling"
)

func init() {
	rootCmd.AddCommand(NewDanglingCmd())
}

// NewDanglingCmd returns the cobra.Command for the dangling subcommand.
// It groups commands that inspect remote objects not reachable from normal branch or tag refs.
func NewDanglingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dangling",
		Short: "Find dangling objects on the remote repository",
		Long: `Find git objects (commits, blobs) that exist on the remote repository
but are not reachable from any normal branch or tag ref.

The primary source of dangling objects is squash or rebase merged pull requests:
when a PR is merged this way the original commits and their blobs remain in the
remote object store, reachable only via refs/pull/{number}/head, not via branches.`,
	}
	cmd.AddCommand(dangling.NewCommitsCmd())
	cmd.AddCommand(dangling.NewBlobsCmd())
	cmd.AddCommand(dangling.NewBranchesCmd())
	cmd.AddCommand(dangling.NewLocalCmd())
	return cmd
}
