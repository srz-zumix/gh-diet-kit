package tree

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/srz-zumix/gh-diet-kit/pkg/tree"
	"github.com/srz-zumix/go-gh-extension/pkg/parser"
)

// NewDetectCmd returns the cobra.Command for the tree detect subcommand.
// It analyses the git tree structure to identify directories with many entries
// that may be contributing to large tree object storage.
func NewDetectCmd() *cobra.Command {
	var repoFlag string
	var refFlag string
	var thresholdFlag int
	var sortFlag string
	var orderFlag string
	var exporter cmdutil.Exporter

	cmd := &cobra.Command{
		Use:   "detect",
		Short: "Detect directories with many entries that bloat git tree objects",
		Args:  cobra.NoArgs,
		Long: `Analyse the git tree structure of a repository and report directories
whose direct entry count (files + subdirectories) meets or exceeds a threshold.

Git stores one tree object per directory per commit. A directory with many
direct entries produces a large tree object, and a deep or wide directory
hierarchy multiplies the number of tree objects written on every commit.

This command fetches the full recursive tree at the given ref (defaulting to
the repository's default branch) and computes, for each directory:

  ENTRY_COUNT   – number of direct children (blobs + sub-trees)
  TOTAL_FILES   – total number of blob entries reachable from this directory
  EST_TREE_SIZE – estimated byte size of the git tree object
                  (28 bytes of overhead per entry + base name length)
  DEPTH         – nesting level (0 = repository root)

Use --threshold to set the minimum ENTRY_COUNT to report. Default: 1.

Use --sort to order results by a field and --order to control direction.
Available sort fields: entry-count, total-files, est-size, depth, path.

For large repositories this may require multiple API calls. Interrupt with
Ctrl+C to stop early and display partial results.

Output fields: PATH, DEPTH, ENTRY_COUNT, TOTAL_FILES, EST_TREE_SIZE`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			if thresholdFlag < 1 {
				return fmt.Errorf("--threshold must be at least 1; got %d", thresholdFlag)
			}

			repo, err := parser.Repository(parser.RepositoryInput(repoFlag))
			if err != nil {
				return fmt.Errorf("failed to determine repository: %w", err)
			}

			g, err := tree.NewGitHubClientWithRepo(repo)
			if err != nil {
				return fmt.Errorf("failed to create GitHub client: %w", err)
			}

			result, err := tree.AnalyzeTreeStructure(ctx, g, repo, refFlag, thresholdFlag)
			interrupted := errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
			if err != nil && !interrupted {
				return fmt.Errorf("failed to analyse tree structure: %w", err)
			}

			if sortFlag != "" {
				desc := strings.EqualFold(orderFlag, "desc")
				if err := tree.SortDirInfoBy(result.Dirs, sortFlag, desc); err != nil {
					return fmt.Errorf("failed to sort tree directories: %w", err)
				}
			}

			r := tree.NewRenderer(exporter)
			if renderErr := r.RenderTreeDirectoryInfo(result, nil); renderErr != nil {
				return renderErr
			}
			if interrupted {
				return err
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVarP(&repoFlag, "repo", "R", "", "Repository in \"[HOST/]OWNER/REPO\" format (default: current repository)")
	f.StringVar(&refFlag, "ref", "", "Branch, tag, or commit SHA to inspect (default: repository default branch)")
	f.IntVar(&thresholdFlag, "threshold", 1, "Minimum number of direct entries in a directory to report (must be >= 1)")
	cmdutil.StringEnumFlag(cmd, &sortFlag, "sort", "", "", []string{"entry-count", "total-files", "est-size", "depth", "path"}, "Sort by field: entry-count, total-files, est-size, depth, path")
	cmdutil.StringEnumFlag(cmd, &orderFlag, "order", "", "asc", []string{"asc", "desc"}, "Sort order: asc, desc")
	cmdutil.AddFormatFlags(cmd, &exporter)
	return cmd
}
