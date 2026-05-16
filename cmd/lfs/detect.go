package lfs

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/srz-zumix/gh-diet-kit/pkg/lfs"
	"github.com/srz-zumix/go-gh-extension/pkg/actions"
	"github.com/srz-zumix/go-gh-extension/pkg/cmdflags"
	"github.com/srz-zumix/go-gh-extension/pkg/logger"
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
	var formatFlag string

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

When running inside a GitHub Actions pull_request or pull_request_target event,
the PR head commit SHA is used as the ref automatically (if --ref is not set),
and only files changed in the PR are checked.

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

			// Detect GitHub Actions PR event context to auto-resolve ref and filter files.
			var prChangedPaths map[string]bool
			eventName := actions.GetEventName()
			isPREvent := eventName == actions.EventPullRequest || eventName == actions.EventPullRequestTarget
			if isPREvent {
				payload, payloadErr := actions.GetEventPayload()
				if payloadErr != nil {
					logger.Warn("failed to read GitHub Actions event payload", "err", payloadErr)
				} else if payload.PullRequest != nil {
					if refFlag == "" {
						refFlag = payload.PullRequest.Head.GetSHA()
						logger.Info("using PR head SHA as ref from GitHub Actions event", "ref", refFlag)
					}
					prNumber := payload.PullRequest.GetNumber()
					prFiles, filesErr := g.ListPullRequestFiles(ctx, repo.Owner, repo.Name, prNumber)
					if filesErr != nil {
						logger.Warn("failed to list PR files, showing all candidates", "err", filesErr)
					} else {
						prChangedPaths = make(map[string]bool, len(prFiles))
						for _, f := range prFiles {
							prChangedPaths[f.GetFilename()] = true
						}
						logger.Info("filtering LFS candidates to PR changed files", "count", len(prChangedPaths))
					}
				}
			}

			candidates, err := lfs.DetectLFSCandidates(ctx, g, repo, refFlag, threshold, prChangedPaths)
			interrupted := errors.Is(err, context.Canceled)
			if err != nil && !interrupted {
				return fmt.Errorf("failed to detect LFS candidates: %w", err)
			}
			if interrupted {
				logger.Warn("interrupted: showing partial results", "found", len(candidates))
			}

			if sortFlag != "" {
				desc := strings.EqualFold(orderFlag, "desc")
				if err := lfs.SortCandidatesBy(candidates, sortFlag, desc); err != nil {
					return fmt.Errorf("failed to sort LFS candidates: %w", err)
				}
			}

			r := lfs.NewRenderer(exporter)
			if formatFlag == "sarif" {
				return r.RenderLFSCandidatesAsSARIF(candidates)
			}
			return r.RenderLFSCandidates(candidates, nil)
		},
	}

	f := cmd.Flags()
	f.StringVarP(&repoFlag, "repo", "R", "", "Repository in \"[HOST/]OWNER/REPO\" format (default: current repository)")
	f.StringVar(&refFlag, "ref", "", "Branch, tag, or commit SHA to inspect (default: repository default branch, or PR head SHA when running in GitHub Actions PR event)")
	f.StringVar(&thresholdFlag, "threshold", "10MB", "Minimum file size to report as an LFS candidate (e.g. 50MB, 1GB, 10000000)")
	cmdutil.StringEnumFlag(cmd, &sortFlag, "sort", "", "", []string{"size", "path"}, "Sort by field: size, path")
	cmdutil.StringEnumFlag(cmd, &orderFlag, "order", "", "asc", []string{"asc", "desc"}, "Sort order: asc, desc")
	if err := cmdflags.AddFormatFlags(cmd, &exporter, &formatFlag, "", []string{"sarif"}); err != nil {
		panic(err)
	}
	return cmd
}

