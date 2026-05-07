package dangling

import (
	"fmt"
	"strings"

	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
	"github.com/srz-zumix/gh-diet-kit/pkg/dangling"
	"github.com/srz-zumix/go-gh-extension/pkg/logger"
	"github.com/srz-zumix/go-gh-extension/pkg/parser"
)

// NewBranchesCmd returns the cobra.Command for the dangling branches subcommand.
// It lists branches that have no associated pull request and reports the blob
// size that would be freed by deleting those branches.
func NewBranchesCmd() *cobra.Command {
	var repoFlag string
	var sortFlag string
	var orderFlag string
	var exporter cmdutil.Exporter

	cmd := &cobra.Command{
		Use:   "branches",
		Short: "List branches with no associated pull request and their unique blob sizes",
		Long: `List branches that have no associated pull request (open, closed, or merged),
and calculate the total size of blobs introduced by commits unique to each branch.

A commit is considered unique to a branch when it is not present in any other
branch's commit history (commits ahead of the default branch). UNIQUE_SIZE is
the sum of blob sizes from the diffs of those unique commits, with blob SHAs
deduplicated across commits — an approximation of the space that could be freed
by deleting the branch.

The default branch is always excluded from results.

Output fields: BRANCH, COMMIT_SHA, AHEAD_COUNT, UNIQUE_SIZE`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			repo, err := parser.Repository(parser.RepositoryInput(repoFlag))
			if err != nil {
				return fmt.Errorf("failed to determine repository: %w", err)
			}

			g, err := dangling.NewGitHubClientWithRepo(repo)
			if err != nil {
				return fmt.Errorf("failed to create GitHub client: %w", err)
			}

			logger.Info("searching for branches without pull requests")
			branches, err := dangling.FindBranchesWithoutPR(ctx, g, repo)
			if err != nil {
				return fmt.Errorf("failed to find branches without pull requests: %w", err)
			}

			var totalSize uint64
			for _, b := range branches {
				if b.UniqueBlobSize != nil {
					totalSize += *b.UniqueBlobSize
				}
			}
			logger.Info("no-PR branch scan complete", "found", len(branches), "total_unique_size", humanize.Bytes(totalSize))

			if sortFlag != "" {
				desc := strings.EqualFold(orderFlag, "desc")
				if err := dangling.SortNoPRBranchesBy(branches, sortFlag, desc); err != nil {
					return fmt.Errorf("failed to sort branches: %w", err)
				}
			}

			r := dangling.NewRenderer(exporter)
			return r.RenderNoPRBranches(branches, nil)
		},
	}

	f := cmd.Flags()
	f.StringVarP(&repoFlag, "repo", "R", "", "Repository in \"[HOST/]OWNER/REPO\" format (default: current repository)")
	cmdutil.StringEnumFlag(cmd, &sortFlag, "sort", "", "", []string{"branch", "ahead_count", "unique_size"}, "Sort by field")
	cmdutil.StringEnumFlag(cmd, &orderFlag, "order", "", "asc", []string{"asc", "desc"}, "Sort order")
	cmdutil.AddFormatFlags(cmd, &exporter)
	return cmd
}
