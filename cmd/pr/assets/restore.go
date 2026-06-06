package assets

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/srz-zumix/gh-diet-kit/pkg/pr/assets"
	"github.com/srz-zumix/go-gh-extension/pkg/parser"
)

// NewRestoreCmd returns the cobra.Command for the pr assets restore subcommand.
// It reads a metadata.json produced by "pr assets dump", uploads each local asset
// file to the destination repository via Playwright browser automation, and
// updates PR bodies, issue comments, and review comments with the new CDN URLs.
func NewRestoreCmd() *cobra.Command {
	var repoFlag string
	var inputDirFlag string
	var metadataFileFlag string
	var prFlag []int
	var dryRunFlag bool
	var browserStateFlag string
	var headedFlag bool
	var clearCacheFlag bool
	var clearCacheOnlyFlag bool

	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Re-upload PR assets and update URLs in the destination repository",
		Args:  cobra.NoArgs,
		Long: `Read the metadata.json produced by "pr assets dump", upload each local asset
file to the destination repository using Playwright browser automation, and
replace the old source asset URLs with the new destination CDN URLs in PR
bodies, issue comments, and review comments.

On the first run a browser window is opened so you can log in to GitHub
interactively. The session is saved to --browser-state for headless operation
on subsequent runs.

Example:
  gh diet-kit pr assets restore -R owner/repo --input-dir ./pr-assets`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			// Resolve the state file path early so --clear-cache-only can use
			// it without requiring --repo or input file flags.
			stateFile := browserStateFlag
			if stateFile == "" {
				configDir, dirErr := os.UserConfigDir()
				if dirErr != nil {
					return fmt.Errorf("failed to determine user config directory: %w", dirErr)
				}
				stateFile = filepath.Join(configDir, "gh-diet-kit", "playwright-state.json")
			}

			if clearCacheOnlyFlag {
				if removeErr := os.Remove(stateFile); removeErr != nil && !os.IsNotExist(removeErr) {
					return fmt.Errorf("failed to clear browser cache %q: %w", stateFile, removeErr)
				}
				cmd.Printf("browser session cleared: %s\n", stateFile)
				return nil
			}

			repo, err := parser.Repository(parser.RepositoryInput(repoFlag))
			if err != nil {
				return fmt.Errorf("failed to determine repository: %w", err)
			}

			g, err := assets.NewGitHubClientWithRepo(repo)
			if err != nil {
				return fmt.Errorf("failed to create GitHub client: %w", err)
			}

			metaPath := metadataFileFlag
			if metaPath == "" {
				metaPath = filepath.Join(inputDirFlag, "metadata.json")
			}

			opts := assets.RestoreOptions{
				PRNumbers:  prFlag,
				DryRun:     dryRunFlag,
				StateFile:  stateFile,
				Headed:     headedFlag,
				ClearCache: clearCacheFlag,
			}

			if err := assets.Restore(ctx, g, repo, inputDirFlag, metaPath, opts); err != nil {
				return fmt.Errorf("failed to restore PR assets: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&repoFlag, "repo", "R", "", "Repository in \"[HOST/]OWNER/REPO\" format (default: current repository)")
	cmd.Flags().StringVar(&inputDirFlag, "input-dir", "./pr-assets", "Directory containing the downloaded asset files")
	cmd.Flags().StringVar(&metadataFileFlag, "metadata-file", "", "Path to metadata.json (default: <input-dir>/metadata.json)")
	cmd.Flags().IntSliceVar(&prFlag, "pr", nil, "PR numbers to restore (repeatable; default: all PRs)")
	cmd.Flags().BoolVarP(&dryRunFlag, "dryrun", "n", false, "Preview uploads and replacements without making any changes")
	cmd.Flags().StringVar(&browserStateFlag, "browser-state", "", "Path to the Playwright browser state file for session persistence (default: <user-config-dir>/gh-diet-kit/playwright-state.json)")
	cmd.Flags().BoolVar(&headedFlag, "headed", false, "Run browser in headed (visible) mode even when a saved session exists (useful for debugging)")
	cmd.Flags().BoolVar(&clearCacheFlag, "clear-cache", false, "Delete the saved browser session after the restore completes")
	cmd.Flags().BoolVar(&clearCacheOnlyFlag, "clear-cache-only", false, "Delete the saved browser session and exit without restoring")

	return cmd
}
