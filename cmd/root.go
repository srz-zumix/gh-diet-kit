/*
Copyright © 2025 srz_zumix
*/
package cmd

import (
	"context"
	"os"
	"os/signal"

	"github.com/spf13/cobra"
	"github.com/srz-zumix/gh-diet-kit/version"
	"github.com/srz-zumix/go-gh-extension/pkg/actions"
	"github.com/srz-zumix/go-gh-extension/pkg/cmdflags"
)

var rootCmd = &cobra.Command{
	Use:   "gh-diet-kit",
	Short: "Slim down GitHub repositories by finding wasted storage",
	Long: `gh-diet-kit is a GitHub CLI extension for putting your repositories on a diet.

It helps you inspect and reduce repository storage usage:
  - Detect dangling git objects (blobs, commits, branches) left on the remote
    by squash/rebase merges, force-pushes, or closed unmerged pull requests.
  - Detect large files and estimate the storage savings of migrating to Git LFS.
  - Inspect the repository tree to find where storage is being consumed.
  - Manage pull request assets (dump, list, restore).`,
	Version: version.Version,
}

func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := rootCmd.ExecuteContext(ctx)
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	if actions.IsRunsOn() {
		rootCmd.SetErrPrefix(actions.GetErrorPrefix())
	}
	cmdflags.AddPersistentFlags(rootCmd)
}
