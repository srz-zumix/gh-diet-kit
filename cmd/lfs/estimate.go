package lfs

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/srz-zumix/gh-diet-kit/pkg/lfs"
	"github.com/srz-zumix/go-gh-extension/pkg/logger"
	"github.com/srz-zumix/go-gh-extension/pkg/parser"
)

// NewEstimateCmd returns the cobra.Command for the lfs estimate subcommand.
// It estimates how much git object storage would be freed by migrating large files
// to Git LFS.
func NewEstimateCmd() *cobra.Command {
	var repoFlag string
	var refFlag string
	var thresholdFlag string
	var scanCommitsFlag int
	var sortFlag string
	var orderFlag string
	var exporter cmdutil.Exporter

	cmd := &cobra.Command{
		Use:   "estimate [path...]",
		Short: "Estimate git storage savings from migrating large files to Git LFS",
		Args:  cobra.ArbitraryArgs,
		Long: `Estimate how much git object storage would be freed by migrating large
files to Git LFS.

When path arguments are given, only those specific files are estimated
regardless of --threshold. When no paths are given, the entire repository tree
is scanned and files exceeding --threshold are reported.

For each candidate the estimated saving is:

  estimated_total_size - (lfs_pointer_size * version_count)

where lfs_pointer_size ≈ 134 bytes (the size of a Git LFS pointer file).

By default only the current tree is inspected (version_count = 1), giving a
minimum estimate. Use --scan-commits to also count how many commits in the
branch history touched each large file. The estimated total size is then
approximated as current_size × version_count, which is a rough upper bound
because actual historic blob sizes are not retrieved.

Use --ref to inspect a specific branch, tag, or commit SHA instead of the
repository's default branch.

Use --threshold to change the size cutoff (e.g. 50MB, 1GB, 10000000).
Default: 10MB. Ignored when path arguments are given.

Default table columns (without --scan-commits): PATH, CURRENT_SIZE,
ESTIMATED_SAVING
Default table columns (with --scan-commits):    PATH, CURRENT_SIZE, VERSIONS,
                                                ESTIMATED_TOTAL_SIZE,
                                                ESTIMATED_SAVING

When using --format json, the output is an object with "estimates" and
"summary". Each item in "estimates" also includes "sha" and "version_count".`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			repo, err := parser.Repository(parser.RepositoryInput(repoFlag))
			if err != nil {
				return fmt.Errorf("failed to determine repository: %w", err)
			}

			g, err := lfs.NewGitHubClientWithRepo(repo)
			if err != nil {
				return fmt.Errorf("failed to create GitHub client: %w", err)
			}

			var estimates []*lfs.LFSSavingEstimate
			var summary *lfs.LFSMigrationSummary

			if len(args) > 0 {
				estimates, summary, err = lfs.EstimateMigrationSavingsForPaths(ctx, g, repo, refFlag, args, scanCommitsFlag)
				interrupted := errors.Is(err, context.Canceled)
				if err != nil && !interrupted {
					return fmt.Errorf("failed to estimate LFS migration savings: %w", err)
				}
				if interrupted {
					logger.Warn("interrupted: showing partial results", "found", len(estimates))
				}
			} else {
				threshold, parseErr := lfs.ParseSize(thresholdFlag)
				if parseErr != nil {
					return fmt.Errorf("invalid --threshold value %q: %w", thresholdFlag, parseErr)
				}
				estimates, summary, err = lfs.EstimateMigrationSavings(ctx, g, repo, refFlag, threshold, scanCommitsFlag)
				interrupted := errors.Is(err, context.Canceled)
				if err != nil && !interrupted {
					return fmt.Errorf("failed to estimate LFS migration savings: %w", err)
				}
				if interrupted {
					logger.Warn("interrupted: showing partial results", "found", len(estimates))
				}
			}

			if sortFlag != "" {
				desc := strings.EqualFold(orderFlag, "desc")
				if err := lfs.SortEstimatesBy(estimates, sortFlag, desc); err != nil {
					return fmt.Errorf("failed to sort estimates: %w", err)
				}
			}

			r := lfs.NewRenderer(exporter)
			return r.RenderLFSSavingEstimates(estimates, summary, nil)
		},
	}

	f := cmd.Flags()
	f.StringVarP(&repoFlag, "repo", "R", "", "Repository in \"[HOST/]OWNER/REPO\" format (default: current repository)")
	f.StringVar(&refFlag, "ref", "", "Branch, tag, or commit SHA to inspect (default: repository default branch)")
	f.StringVar(&thresholdFlag, "threshold", "10MB", "Minimum file size to include in the estimate (e.g. 50MB, 1GB, 10000000)")
	f.IntVar(&scanCommitsFlag, "scan-commits", 0, "Scan up to N commits per file to count historic versions (0 = current tree only, negative = all commits)")
	cmdutil.StringEnumFlag(cmd, &sortFlag, "sort", "", "", []string{"saving", "size", "path", "versions"}, "Sort by field: saving, size, path, versions")
	cmdutil.StringEnumFlag(cmd, &orderFlag, "order", "", "asc", []string{"asc", "desc"}, "Sort order: asc, desc")
	cmdutil.AddFormatFlags(cmd, &exporter)
	return cmd
}
