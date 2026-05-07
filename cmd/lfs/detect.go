package lfs

import (
	"fmt"
	"strings"

	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/srz-zumix/gh-diet-kit/pkg/lfs"
	"github.com/srz-zumix/go-gh-extension/pkg/parser"
)

// NewDetectCmd returns the cobra.Command for the lfs detect subcommand.
// It scans the repository tree for blobs that exceed a size threshold and are
// not stored as Git LFS pointer files.
func NewDetectCmd() *cobra.Command {
	var repoFlag string
	var refFlag string
	var thresholdFlag string
	var sortFlag string
	var orderFlag string
	var exporter cmdutil.Exporter

	cmd := &cobra.Command{
		Use:   "detect",
		Short: "Detect files that should be tracked by Git LFS",
		Long: `Detect files in the repository whose size exceeds a threshold and
are not currently stored as Git LFS objects.

Files that are properly tracked by LFS appear as small pointer files (~130
bytes) in the git tree and are therefore not reported. Any blob reported by
this command is stored verbatim in git and is a candidate for LFS migration.

Use --ref to inspect a specific branch, tag, or commit SHA instead of the
repository's default branch.

Use --threshold to change the size cutoff. The value can be a plain
integer (bytes) or use a unit suffix: KB, MB, GB (e.g. 50MB, 1GB).
Default: 10MB.

The tree is traversed recursively; for large repositories this may require
multiple API calls. Interrupt with Ctrl+C to stop early.

Output fields: PATH, SIZE, SHA`,
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

			threshold, err := lfs.ParseSize(thresholdFlag)
			if err != nil {
				return fmt.Errorf("invalid --threshold value %q: %w", thresholdFlag, err)
			}

			candidates, err := lfs.DetectLFSCandidates(ctx, g, repo, refFlag, threshold)
			if err != nil {
				return fmt.Errorf("failed to detect LFS candidates: %w", err)
			}

			if sortFlag != "" {
				desc := strings.EqualFold(orderFlag, "desc")
				if err := lfs.SortCandidatesBy(candidates, sortFlag, desc); err != nil {
					return fmt.Errorf("failed to sort LFS candidates: %w", err)
				}
			}

			r := lfs.NewRenderer(exporter)
			return r.RenderLFSCandidates(candidates, nil)
		},
	}

	f := cmd.Flags()
	f.StringVarP(&repoFlag, "repo", "R", "", "Repository in \"[HOST/]OWNER/REPO\" format (default: current repository)")
	f.StringVar(&refFlag, "ref", "", "Branch, tag, or commit SHA to inspect (default: repository default branch)")
	f.StringVar(&thresholdFlag, "threshold", "10MB", "Minimum file size to report as an LFS candidate (e.g. 50MB, 1GB, 10000000)")
	cmdutil.StringEnumFlag(cmd, &sortFlag, "sort", "", "", []string{"size", "path"}, "Sort by field: size, path")
	cmdutil.StringEnumFlag(cmd, &orderFlag, "order", "", "asc", []string{"asc", "desc"}, "Sort order: asc, desc")
	cmdutil.AddFormatFlags(cmd, &exporter)
	return cmd
}
