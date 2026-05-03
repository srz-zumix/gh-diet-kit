package dangling

import (
	"context"
	"fmt"
	"strings"

	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/srz-zumix/gh-diet-kit/internal/prs"
	"github.com/srz-zumix/go-gh-extension/pkg/gh"
	"github.com/srz-zumix/go-gh-extension/pkg/parser"
	"github.com/srz-zumix/go-gh-extension/pkg/render"
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
	var localDefaultBranchFlag string
	var exporter cmdutil.Exporter

	cmd := &cobra.Command{
		Use:   "blobs",
		Short: "List blobs not reachable from any branch or tag ref",
		Long: `List blobs that are referenced only by commits from squash or rebase
merged pull requests, commits dropped by force-pushes on PR head branches, or
commits from closed unmerged pull requests, and are not reachable from any
normal branch or tag ref on the remote. All detection methods are enabled by
default.

For each dangling commit the full git tree is traversed recursively; all blob
entries are reported. Blob SHAs are deduplicated per PR but the same SHA may
appear in output when referenced by commits in different PRs.

Use --no-squash-merge to skip squash/rebase merge detection.
Use --no-force-push to skip force-push dropped commit detection.
Use --no-closed to skip closed unmerged PR detection.

Use --pr to inspect specific pull request numbers. Without --pr, all closed PRs
are inspected (up to --limit).

Note: a blob that also exists in a current branch tree is not truly dangling at
the object level. This command reports blobs referenced by dangling commits as
candidates; cross-checking against current branch trees is left to the caller.

Output fields: SHA, PATH, SIZE, COMMIT_SHA, PR_NUMBER, PR_URL`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			repo, err := parser.Repository(parser.RepositoryInput(repoFlag))
			if err != nil {
				return fmt.Errorf("failed to determine repository: %w", err)
			}

			g, err := gh.NewGitHubClientWithRepo(repo)
			if err != nil {
				return fmt.Errorf("failed to create GitHub client: %w", err)
			}

			prList, err := prs.FetchPRsForDangling(ctx, g, repo, prFlag, limitFlag)
			if err != nil {
				return fmt.Errorf("failed to fetch pull requests: %w", err)
			}

			opts := gh.DanglingOptions{
				DisableSquashRebase: noSquashMergeFlag,
				DisableForcePush:    noForcePushFlag,
				DisableClosed:       noClosedFlag,
				ReachabilityCheck:   gh.ReachabilityCheckMode(reachabilityCheckFlag),
				LocalDefaultBranch:  localDefaultBranchFlag,
			}

			blobs, err := gh.FindDanglingBlobs(ctx, g, repo, prList, opts)
			if err != nil {
				return fmt.Errorf("failed to find dangling blobs: %w", err)
			}

			if sortFlag != "" {
				desc := strings.EqualFold(orderFlag, "desc")
				if err := gh.SortBlobsBy(blobs, sortFlag, desc); err != nil {
					return fmt.Errorf("failed to sort dangling blobs: %w", err)
				}
			}

			r := render.NewRenderer(exporter)
			return r.RenderDanglingBlobs(blobs, nil)
		},
	}

	cmd.Flags().StringVarP(&repoFlag, "repo", "R", "", "Repository in \"[HOST/]OWNER/REPO\" format (default: current repository)")
	cmd.Flags().IntVar(&limitFlag, "limit", -1, "Maximum number of closed PRs to inspect (ignored when --pr is specified)")
	cmd.Flags().IntSliceVar(&prFlag, "pr", nil, "PR numbers to inspect (default: all closed PRs)")
	cmd.Flags().BoolVar(&noSquashMergeFlag, "no-squash-merge", false, "Disable squash/rebase merged PR blob detection")
	cmd.Flags().BoolVar(&noForcePushFlag, "no-force-push", false, "Disable force-push dropped commit blob detection")
	cmd.Flags().BoolVar(&noClosedFlag, "no-closed", false, "Disable closed unmerged PR blob detection")
	cmdutil.StringEnumFlag(cmd, &reachabilityCheckFlag, "reachability-check", "", string(gh.ReachabilityCheckNone), gh.ReachabilityCheckModeValues, "Verify candidates are truly not reachable from a branch")
	cmd.Flags().StringVar(&localDefaultBranchFlag, "local-default-branch", "", "Remote-tracking ref for --reachability-check=local-default (e.g. \"origin/main\"; auto-detected if empty)")
	cmdutil.StringEnumFlag(cmd, &sortFlag, "sort", "", "", []string{"size", "path", "pr_number"}, "Sort by field")
	cmdutil.StringEnumFlag(cmd, &orderFlag, "order", "", "asc", []string{"asc", "desc"}, "Sort order")
	cmdutil.AddFormatFlags(cmd, &exporter)
	return cmd
}
