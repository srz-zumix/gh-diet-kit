package dangling

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
	"github.com/srz-zumix/gh-diet-kit/internal/prs"
	"github.com/srz-zumix/gh-diet-kit/pkg/dangling"
	"github.com/srz-zumix/go-gh-extension/pkg/logger"
	"github.com/srz-zumix/go-gh-extension/pkg/parser"
)

// NewBlobsCmd returns the cobra.Command for the dangling blobs subcommand.
// It lists blobs from commits that originate from squash or rebase merged pull
// requests, commits dropped by force-pushes, or from closed unmerged pull
// requests, that are not reachable from any normal branch or tag ref.
func NewBlobsCmd() *cobra.Command {
	var repoFlag string
	var limitFlag int
	var prFlag []int
	var sortFlag string
	var orderFlag string
	var noSquashMergeFlag bool
	var noForcePushFlag bool
	var noClosedFlag bool
	var reachabilityCheckFlag string
	var strictErrorsFlag bool
	var noCacheFlag bool
	var clearCacheFlag bool
	var clearGitCacheFlag bool
	var exporter cmdutil.Exporter

	cmd := &cobra.Command{
		Use:   "blobs",
		Short: "List blobs not reachable from any branch or tag ref",
		Long: `List blobs that are referenced only by commits from squash or rebase
merged pull requests, commits dropped by force-pushes on PR head branches, or
commits from closed unmerged pull requests, and are not reachable from any
normal branch or tag ref on the remote. All detection methods are enabled by
default.

Use --no-squash-merge to skip squash/rebase merge detection.
Use --no-force-push to skip force-push dropped commit detection.
Use --no-closed to skip closed unmerged PR detection.

Use --pr to inspect specific pull request numbers. Without --pr, all closed PRs
are inspected (up to --limit).

Note: Git blobs are content-addressed. A blob introduced by a dangling commit
may also appear in a live branch tree via identical file content (e.g.
package-lock.json, generated files). Without a local git clone this cannot be
detected efficiently via the GitHub API, so results may contain false positives.
Use --reachability-check local-object (after running git fetch --all --tags)
to filter out blobs that are still reachable from any local ref. Note:
git fetch --all alone does not fetch tags that are not reachable from any
branch, so commits reachable only from such tags may be misreported.

Output fields: SHA, PATH, SIZE, COMMIT_SHA, PR_NUMBER, PR_URL`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			// When --repo is specified with a local reachability check, auto-setup a bare clone
			// cache so the check can run against the correct remote repository.
			var gitDir string
			if repoFlag != "" && reachabilityCheckFlag == string(dangling.ReachabilityCheckLocalObject) {
				repo, err := parser.Repository(parser.RepositoryInput(repoFlag))
				if err != nil {
					return fmt.Errorf("failed to determine repository: %w", err)
				}
				// full clone needed for blob content reachability
				dir, err := dangling.SetupLocalGitCache(ctx, repo, false, clearGitCacheFlag)
				if err != nil {
					return fmt.Errorf("failed to set up local git cache for --repo: %w", err)
				}
				gitDir = dir
			}

			repo, err := parser.Repository(parser.RepositoryInput(repoFlag))
			if err != nil {
				return fmt.Errorf("failed to determine repository: %w", err)
			}

			g, err := dangling.NewGitHubClientWithRepo(repo)
			if err != nil {
				return fmt.Errorf("failed to create GitHub client: %w", err)
			}

			prList, err := prs.FetchPRsForDangling(ctx, g, repo, prFlag, limitFlag)
			if err != nil {
				return fmt.Errorf("failed to fetch pull requests: %w", err)
			}

			opts := dangling.DanglingOptions{
				DisableSquashRebase: noSquashMergeFlag,
				DisableForcePush:    noForcePushFlag,
				DisableClosed:       noClosedFlag,
				ReachabilityCheck:   dangling.ReachabilityCheckMode(reachabilityCheckFlag),
				StrictErrors:        strictErrorsFlag,
				GitDir:              gitDir,
				NoCache:             noCacheFlag,
				ClearCache:          clearCacheFlag,
			}

			logger.Info("inspecting PRs for dangling blobs", "total", len(prList))
			blobs, err := dangling.FindDanglingBlobs(ctx, g, repo, prList, opts)
			interrupted := errors.Is(err, context.Canceled)
			if err != nil && !interrupted {
				return fmt.Errorf("failed to find dangling blobs: %w", err)
			}
			if interrupted {
				logger.Warn("interrupted: showing partial results", "found", len(blobs))
			}

			var totalSize uint64
			for _, b := range blobs {
				if b.Size != nil && *b.Size > 0 {
					totalSize += uint64(*b.Size)
				}
			}
			logger.Info("dangling blob search complete", "found", len(blobs), "total_size", humanize.Bytes(totalSize))

			if sortFlag != "" {
				desc := strings.EqualFold(orderFlag, "desc")
				if err := dangling.SortBlobsBy(blobs, sortFlag, desc); err != nil {
					return fmt.Errorf("failed to sort dangling blobs: %w", err)
				}
			}

			r := dangling.NewRenderer(exporter)
			return r.RenderDanglingBlobs(blobs, nil)
		},
	}

	f := cmd.Flags()
	f.StringVarP(&repoFlag, "repo", "R", "", "Repository in \"[HOST/]OWNER/REPO\" format (default: current repository)")
	f.IntVar(&limitFlag, "limit", -1, "Maximum number of closed PRs to inspect (ignored when --pr is specified)")
	f.IntSliceVar(&prFlag, "pr", nil, "PR numbers to inspect (default: all closed PRs)")
	f.BoolVar(&noSquashMergeFlag, "no-squash-merge", false, "Disable squash/rebase merged PR blob detection")
	f.BoolVar(&noForcePushFlag, "no-force-push", false, "Disable force-push dropped commit blob detection")
	f.BoolVar(&noClosedFlag, "no-closed", false, "Disable closed unmerged PR blob detection")
	cmdutil.StringEnumFlag(cmd, &reachabilityCheckFlag, "reachability-check", "", string(dangling.ReachabilityCheckNone), []string{string(dangling.ReachabilityCheckNone), string(dangling.ReachabilityCheckLocalObject)}, "Filter out blobs reachable from a local ref (requires git fetch --all --tags): none, local-object")
	f.BoolVar(&strictErrorsFlag, "strict-errors", false, "Fail immediately on any API or git error instead of logging and continuing")
	f.BoolVar(&noCacheFlag, "no-cache", false, "Disable per-PR result cache (always re-process all PRs); does not clear existing cache entries")
	f.BoolVar(&clearCacheFlag, "clear-cache", false, "Clear the per-PR and commit blob cache before running, then use cache normally")
	f.BoolVar(&clearGitCacheFlag, "clear-git-cache", false, "Clear the git bare clone cache and re-clone before running")
	cmdutil.StringEnumFlag(cmd, &sortFlag, "sort", "", "", []string{"size", "path", "pr_number"}, "Sort by field")
	cmdutil.StringEnumFlag(cmd, &orderFlag, "order", "", "asc", []string{"asc", "desc"}, "Sort order")
	cmdutil.AddFormatFlags(cmd, &exporter)
	return cmd
}
