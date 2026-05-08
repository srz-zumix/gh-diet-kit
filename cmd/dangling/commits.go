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

// NewCommitsCmd returns the cobra.Command for the dangling commits subcommand.
// It lists commits from squash or rebase merged pull requests, commits dropped
// by force-pushes, or from closed unmerged pull requests, that are not reachable
// from any normal branch or tag ref on the remote repository.
func NewCommitsCmd() *cobra.Command {
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
	var concurrencyFlag int
	var exporter cmdutil.Exporter

	cmd := &cobra.Command{
		Use:   "commits",
		Short: "List commits not reachable from any branch or tag ref",
		Long: `List commits that originate from squash or rebase merged pull requests,
commits dropped by force-pushes on PR head branches, or commits from closed
unmerged pull requests, that are not reachable from any normal branch or tag
ref on the remote. All detection methods are enabled by default.

Use --no-squash-merge to skip squash/rebase merge detection.
Use --no-force-push to skip force-push dropped commit detection.
Use --no-closed to skip closed unmerged PR detection.

Use --pr to inspect specific pull request numbers. Without --pr, all closed PRs
are inspected (up to --limit).

Output fields: SHA, PR_NUMBER, PR_URL, SIZE, MESSAGE`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			localCheck := reachabilityCheckFlag == string(dangling.ReachabilityCheckLocalObject) || reachabilityCheckFlag == string(dangling.ReachabilityCheckLocalRefs)

			// When --repo is specified with a local reachability check, auto-setup a bare clone
			// cache so the check can run against the correct remote repository.
			var gitDir string
			if repoFlag != "" && localCheck {
				repo, err := parser.Repository(parser.RepositoryInput(repoFlag))
				if err != nil {
					return fmt.Errorf("failed to determine repository: %w", err)
				}
				blobless := true // commit reachability only; blobs not needed
				dir, err := dangling.SetupLocalGitCache(ctx, repo, blobless, clearGitCacheFlag)
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
				DisableSquashRebase:    noSquashMergeFlag,
				DisableForcePush:       noForcePushFlag,
				DisableClosed:          noClosedFlag,
				ReachabilityCheck:      dangling.ReachabilityCheckMode(reachabilityCheckFlag),
				StrictErrors:           strictErrorsFlag,
				GitDir:                 gitDir,
				NoCache:                noCacheFlag,
				ClearCache:             clearCacheFlag,
				CommitFetchConcurrency: concurrencyFlag,
			}

			logger.Info("inspecting PRs for dangling commits", "total", len(prList))
			commits, err := dangling.FindDanglingCommits(ctx, g, repo, prList, opts)
			interrupted := errors.Is(err, context.Canceled)
			if err != nil && !interrupted {
				return fmt.Errorf("failed to find dangling commits: %w", err)
			}
			if interrupted {
				logger.Warn("interrupted: showing partial results", "found", len(commits))
			}

			var totalSize uint64
			for _, c := range commits {
				if c.TotalBlobSize != nil {
					totalSize += *c.TotalBlobSize
				}
			}
			logger.Info("dangling commit search complete", "found", len(commits), "total_size", humanize.Bytes(totalSize))

			if sortFlag != "" {
				desc := strings.EqualFold(orderFlag, "desc")
				if err := dangling.SortCommitsBy(commits, sortFlag, desc); err != nil {
					return fmt.Errorf("failed to sort dangling commits: %w", err)
				}
			}

			r := dangling.NewRenderer(exporter)
			return r.RenderDanglingCommits(commits, nil)
		},
	}

	f := cmd.Flags()
	f.StringVarP(&repoFlag, "repo", "R", "", "Repository in \"[HOST/]OWNER/REPO\" format (default: current repository)")
	f.IntVar(&limitFlag, "limit", -1, "Maximum number of closed PRs to inspect (ignored when --pr is specified)")
	f.IntSliceVar(&prFlag, "pr", nil, "PR numbers to inspect (default: all closed PRs)")
	f.BoolVar(&noSquashMergeFlag, "no-squash-merge", false, "Disable squash/rebase merged PR commit detection")
	f.BoolVar(&noForcePushFlag, "no-force-push", false, "Disable force-push dropped commit detection")
	f.BoolVar(&noClosedFlag, "no-closed", false, "Disable closed unmerged PR detection")
	cmdutil.StringEnumFlag(cmd, &reachabilityCheckFlag, "reachability-check", "", string(dangling.ReachabilityCheckNone), dangling.ReachabilityCheckModeValues, "Verify candidates are truly not reachable from a branch or tag")
	f.BoolVar(&strictErrorsFlag, "strict-errors", false, "Fail immediately on any API or git error instead of logging and continuing")
	f.BoolVar(&noCacheFlag, "no-cache", false, "Disable per-PR result cache (always re-process all PRs); does not clear existing cache entries")
	f.BoolVar(&clearCacheFlag, "clear-cache", false, "Clear the per-PR and commit blob cache before running, then use cache normally")
	f.BoolVar(&clearGitCacheFlag, "clear-git-cache", false, "Clear the git bare clone cache and re-clone before running")
	f.IntVar(&concurrencyFlag, "concurrency", 0, "Maximum number of concurrent GitHub API calls per PR for commit blob fetches (<=0 uses the package default)")
	cmdutil.StringEnumFlag(cmd, &sortFlag, "sort", "", "", []string{"size", "pr_number"}, "Sort by field")
	cmdutil.StringEnumFlag(cmd, &orderFlag, "order", "", "asc", []string{"asc", "desc"}, "Sort order")
	cmdutil.AddFormatFlags(cmd, &exporter)
	return cmd
}
