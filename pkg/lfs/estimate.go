package lfs

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/cli/go-gh/v2/pkg/repository"
	"github.com/srz-zumix/go-gh-extension/pkg/gh"
	"github.com/srz-zumix/go-gh-extension/pkg/logger"
)

// lfsPointerSize is the approximate size in bytes of a Git LFS pointer file.
// A real pointer is typically 127–134 bytes; 134 is used as a conservative upper bound.
const lfsPointerSize = 134

// LFSSavingEstimate holds the estimated storage saving for a single file path
// when the file is migrated to Git LFS.
type LFSSavingEstimate struct {
	// Path is the file path relative to the repository root.
	Path string `json:"path"`
	// SHA is the blob SHA of the current version.
	SHA string `json:"sha"`
	// CurrentSize is the byte size of the current blob.
	CurrentSize int `json:"current_size"`
	// VersionCount is the number of commit versions found in history.
	// 1 means only the current tree version was inspected (no history scan).
	VersionCount int `json:"version_count"`
	// EstimatedTotalSize is the estimated total blob size across all versions
	// (CurrentSize * VersionCount — a rough approximation).
	EstimatedTotalSize int64 `json:"estimated_total_size"`
	// EstimatedSaving is the estimated reduction in git object storage
	// (EstimatedTotalSize minus the LFS pointer overhead).
	EstimatedSaving int64 `json:"estimated_saving"`
}

// LFSMigrationSummary contains aggregate statistics for a migration estimate run.
type LFSMigrationSummary struct {
	// CandidateCount is the number of files that exceed the size threshold.
	CandidateCount int `json:"candidate_count"`
	// TotalCurrentSize is the sum of current blob sizes for all candidates.
	TotalCurrentSize int64 `json:"total_current_size"`
	// TotalEstimatedSize is the sum of estimated total historic blob sizes.
	TotalEstimatedSize int64 `json:"total_estimated_size"`
	// TotalEstimatedSaving is the estimated total reduction in git object storage.
	TotalEstimatedSaving int64 `json:"total_estimated_saving"`
	// HistoryScanned reports whether git history was included in the estimate.
	HistoryScanned bool `json:"history_scanned"`
}

// estimateForFile builds a single LFSSavingEstimate for path/sha/size,
// optionally scanning git history up to scanCommitsDepth commits.
func estimateForFile(ctx context.Context, g *GitHubClient, repo repository.Repository, ref, path, sha string, size int, scanCommitsDepth int) (*LFSSavingEstimate, error) {
	versionCount := 1
	if scanCommitsDepth != 0 {
		n, cntErr := countFileVersions(ctx, g, repo, ref, path, scanCommitsDepth)
		if cntErr != nil {
			logger.Warn("failed to count file versions, assuming 1", "path", path, "error", cntErr)
		} else if n > 0 {
			versionCount = n
		}
	}

	estimatedTotal := int64(size) * int64(versionCount)
	pointerOverhead := int64(lfsPointerSize) * int64(versionCount)
	estimatedSaving := max(estimatedTotal-pointerOverhead, 0)

	return &LFSSavingEstimate{
		Path:               path,
		SHA:                sha,
		CurrentSize:        size,
		VersionCount:       versionCount,
		EstimatedTotalSize: estimatedTotal,
		EstimatedSaving:    estimatedSaving,
	}, nil
}

// EstimateMigrationSavings returns per-file saving estimates for blobs that exceed
// threshold bytes in the repository tree at ref.
//
// If scanCommitsDepth != 0, git history is scanned up to scanCommitsDepth commits per
// file to count the number of distinct versions, and the estimated total size is
// approximated as CurrentSize * VersionCount. This is a rough estimate because actual
// historic blob sizes are not retrieved — only the version count is fetched.
// A negative value scans all commits. 0 skips history scanning (VersionCount = 1).
func EstimateMigrationSavings(
	ctx context.Context,
	g *GitHubClient,
	repo repository.Repository,
	ref string,
	threshold int64,
	scanCommitsDepth int,
) ([]*LFSSavingEstimate, *LFSMigrationSummary, error) {
	candidates, err := DetectLFSCandidates(ctx, g, repo, ref, threshold)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to detect LFS candidates: %w", err)
	}

	historyScanned := scanCommitsDepth != 0
	estimates := make([]*LFSSavingEstimate, 0, len(candidates))

	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			return estimates, nil, err
		}

		e, err := estimateForFile(ctx, g, repo, ref, c.Path, c.SHA, c.Size, scanCommitsDepth)
		if err != nil {
			return estimates, nil, err
		}
		estimates = append(estimates, e)
	}

	summary := buildSummary(estimates, historyScanned)
	return estimates, summary, nil
}

// buildSummary aggregates per-file estimates into a run-level summary.
func buildSummary(estimates []*LFSSavingEstimate, historyScanned bool) *LFSMigrationSummary {
	s := &LFSMigrationSummary{
		CandidateCount: len(estimates),
		HistoryScanned: historyScanned,
	}
	for _, e := range estimates {
		s.TotalCurrentSize += int64(e.CurrentSize)
		s.TotalEstimatedSize += e.EstimatedTotalSize
		s.TotalEstimatedSaving += e.EstimatedSaving
	}
	return s
}

// EstimateMigrationSavingsForPaths estimates migration savings for explicitly specified
// file paths. Unlike EstimateMigrationSavings, the threshold is not applied — all
// specified paths are included regardless of their size. If a path cannot be found
// (e.g. it does not exist on ref) an error is returned.
func EstimateMigrationSavingsForPaths(
	ctx context.Context,
	g *GitHubClient,
	repo repository.Repository,
	ref string,
	paths []string,
	scanCommitsDepth int,
) ([]*LFSSavingEstimate, *LFSMigrationSummary, error) {
	historyScanned := scanCommitsDepth != 0
	estimates := make([]*LFSSavingEstimate, 0, len(paths))

	var refPtr *string
	if ref != "" {
		refPtr = &ref
	}

	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return estimates, nil, err
		}

		fileContent, _, err := g.GetRepositoryContent(ctx, repo.Owner, repo.Name, path, refPtr)
		if err != nil {
			return estimates, nil, fmt.Errorf("failed to get content for %q: %w", path, err)
		}
		if fileContent == nil {
			return estimates, nil, fmt.Errorf("path %q is a directory or was not found", path)
		}

		e, err := estimateForFile(ctx, g, repo, ref, path, fileContent.GetSHA(), fileContent.GetSize(), scanCommitsDepth)
		if err != nil {
			return estimates, nil, err
		}
		estimates = append(estimates, e)
	}

	summary := buildSummary(estimates, historyScanned)
	return estimates, summary, nil
}

// countFileVersions returns the number of commits that touched path on ref,
// up to maxDepth. When maxDepth <= 0, all commits are counted.
// Returns at least 1 (the current version is always present).
func countFileVersions(ctx context.Context, g *GitHubClient, repo repository.Repository, ref, path string, maxDepth int) (int, error) {
	logger.Debug("counting file versions", "path", path, "max_depth", maxDepth)
	commits, err := gh.ListCommitsForPath(ctx, g, repo, path, ref, maxDepth)
	if err != nil {
		return 0, err
	}
	if len(commits) == 0 {
		return 1, nil
	}
	return len(commits), nil
}

// SortEstimatesBy sorts estimates in-place by the given field (case-insensitive).
// Supported fields: "saving", "size", "path", "versions".
// desc=true reverses the order.
func SortEstimatesBy(estimates []*LFSSavingEstimate, field string, desc bool) error {
	switch strings.ToLower(field) {
	case "saving":
		slices.SortFunc(estimates, func(a, b *LFSSavingEstimate) int {
			if a.EstimatedSaving != b.EstimatedSaving {
				if a.EstimatedSaving < b.EstimatedSaving {
					return -1
				}
				return 1
			}
			return strings.Compare(a.Path, b.Path)
		})
	case "size":
		slices.SortFunc(estimates, func(a, b *LFSSavingEstimate) int {
			if a.CurrentSize != b.CurrentSize {
				if a.CurrentSize < b.CurrentSize {
					return -1
				}
				return 1
			}
			return strings.Compare(a.Path, b.Path)
		})
	case "path":
		slices.SortFunc(estimates, func(a, b *LFSSavingEstimate) int {
			return strings.Compare(a.Path, b.Path)
		})
	case "versions":
		slices.SortFunc(estimates, func(a, b *LFSSavingEstimate) int {
			if a.VersionCount != b.VersionCount {
				if a.VersionCount < b.VersionCount {
					return -1
				}
				return 1
			}
			return strings.Compare(a.Path, b.Path)
		})
	default:
		return fmt.Errorf("unknown sort field %q: valid fields are saving, size, path, versions", field)
	}
	if desc {
		slices.Reverse(estimates)
	}
	return nil
}
