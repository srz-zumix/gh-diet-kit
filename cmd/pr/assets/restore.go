package assets

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

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
	var uploadDelayFlag time.Duration

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

			// Verify write access up front so the user is not prompted for a
			// browser login only to fail when updating PRs later. Skip the check
			// in dry-run mode, which performs no edits and only needs read access
			// so read-only users can preview a restore.
			if !dryRunFlag {
				if err := assets.CheckWriteAccess(ctx, g, repo); err != nil {
					return err
				}
			}

			metaPath := metadataFileFlag
			if metaPath == "" {
				metaPath = filepath.Join(inputDirFlag, "metadata.json")
			}

			opts := assets.RestoreOptions{
				PRNumbers:   prFlag,
				DryRun:      dryRunFlag,
				StateFile:   stateFile,
				Headed:      headedFlag,
				ClearCache:  clearCacheFlag,
				UploadDelay: uploadDelayFlag,
			}

			if err := assets.Restore(ctx, g, repo, inputDirFlag, metaPath, opts); err != nil {
				return fmt.Errorf("failed to restore PR assets: %w", err)
			}

			return nil
		},
	}

	f := cmd.Flags()
	f.StringVarP(&repoFlag, "repo", "R", "", "Repository in \"[HOST/]OWNER/REPO\" format (default: current repository)")
	f.StringVar(&inputDirFlag, "input-dir", "./pr-assets", "Directory containing the downloaded asset files")
	f.StringVar(&metadataFileFlag, "metadata-file", "", "Path to metadata.json (default: <input-dir>/metadata.json)")
	f.IntSliceVar(&prFlag, "pr", nil, "PR numbers to restore (repeatable; default: all PRs)")
	f.BoolVarP(&dryRunFlag, "dryrun", "n", false, "Preview uploads and replacements without making any changes")
	f.StringVar(&browserStateFlag, "browser-state", "", "Path to the Playwright browser state file for session persistence (default: <user-config-dir>/gh-diet-kit/playwright-state.json)")
	f.BoolVar(&headedFlag, "headed", false, "Run browser in headed (visible) mode even when a saved session exists (useful for debugging)")
	f.BoolVar(&clearCacheFlag, "clear-cache", false, "Delete the saved browser session after the restore completes")
	f.BoolVar(&clearCacheOnlyFlag, "clear-cache-only", false, "Delete the saved browser session and exit without restoring")
	f.DurationVar(&uploadDelayFlag, "upload-delay", assets.DefaultUploadDelay, "Minimum delay between asset uploads to avoid GitHub's secondary rate limit")

	return cmd
}
