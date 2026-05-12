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

// DefaultSizeThreshold is the default file size threshold (10 MiB) above which
// a blob is reported as an LFS candidate.
const DefaultSizeThreshold uint64 = 10 * 1024 * 1024

// LFSPointerSize is the maximum byte size of a Git LFS pointer file.
// A real pointer is typically 127–134 bytes; 134 is used as a conservative upper bound.
const LFSPointerSize uint64 = 134

// MinSizeThreshold is the smallest threshold accepted by DetectLFSCandidates.
// A threshold at or below LFSPointerSize would cause genuine LFS pointer blobs
// to be reported as candidates, contradicting the command's purpose.
const MinSizeThreshold = LFSPointerSize + 1

// ParseSize parses a human-readable size string into a byte count.
// Accepts a plain integer (bytes) or a value with a unit suffix: KB, MB, GB, TB
// (case-insensitive, e.g. "50MB", "1gb", "10000000").
func ParseSize(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	upper := strings.ToUpper(s)

	// Check suffixes from longest to shortest to avoid "B" matching inside "MB".
	suffixes := []struct {
		suffix string
		mult   uint64
	}{
		{"TB", 1024 * 1024 * 1024 * 1024},
		{"GB", 1024 * 1024 * 1024},
		{"MB", 1024 * 1024},
		{"KB", 1024},
		{"B", 1},
	}
	for _, e := range suffixes {
		if strings.HasSuffix(upper, e.suffix) {
			numStr := strings.TrimSpace(s[:len(s)-len(e.suffix)])
			var base uint64
			if _, err := fmt.Sscanf(numStr, "%d", &base); err != nil {
				return 0, fmt.Errorf("cannot parse numeric part %q in %q", numStr, s)
			}
			// Detect multiplication overflow: base*mult > math.MaxUint64.
			if e.mult > 1 && base > ^uint64(0)/e.mult {
				return 0, fmt.Errorf("size value %q overflows uint64", s)
			}
			return base * e.mult, nil
		}
	}

	// Plain integer (bytes)
	var n uint64
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, fmt.Errorf("unsupported size format %q: use a plain integer (bytes) or a suffix: KB, MB, GB, TB", s)
	}
	return n, nil
}

// GitHubClient is a type alias for the shared GitHubClient from go-gh-extension.
type GitHubClient = gh.GitHubClient

// NewGitHubClientWithRepo creates a GitHub API client configured for the given repository.
func NewGitHubClientWithRepo(repo repository.Repository) (*GitHubClient, error) {
	return gh.NewGitHubClientWithRepo(repo)
}

// LFSCandidate represents a blob that exceeds the size threshold and is not stored
// as a Git LFS pointer. Files properly tracked by LFS appear as small pointer files
// (~130 bytes) in the git tree and therefore fall below any reasonable threshold.
type LFSCandidate struct {
	Path string `json:"path"`
	Size uint64 `json:"size"`
	SHA  string `json:"sha"`
}

// DetectLFSCandidates returns all blobs in the repository tree at ref that exceed
// threshold bytes. If ref is empty the repository's default branch is used.
// The tree is traversed recursively to avoid the GitHub API's 100,000-entry
// truncation limit; this may require multiple API calls for large repositories.
// If paths is non-nil and non-empty, only blobs whose path appears in the set are returned.
func DetectLFSCandidates(ctx context.Context, g *GitHubClient, repo repository.Repository, ref string, threshold uint64, paths map[string]bool) ([]*LFSCandidate, error) {
	if threshold < MinSizeThreshold {
		return nil, fmt.Errorf("--threshold must be at least %d bytes (LFS pointer size + 1); got %d", MinSizeThreshold, threshold)
	}
	if ref == "" {
		ghRepo, err := g.GetRepository(ctx, repo.Owner, repo.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to get repository: %w", err)
		}
		ref = ghRepo.GetDefaultBranch()
	}

	logger.Info("resolving ref", "ref", ref)
	commitSHA, err := g.GetCommitSHA1(ctx, repo.Owner, repo.Name, ref, "")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve ref %q: %w", ref, err)
	}

	logger.Info("fetching git commit", "sha", commitSHA)
	gitCommit, err := g.GetGitCommit(ctx, repo.Owner, repo.Name, commitSHA)
	if err != nil {
		return nil, fmt.Errorf("failed to get git commit %q: %w", commitSHA, err)
	}

	treeSHA := gitCommit.GetTree().GetSHA()
	logger.Info("traversing repository tree", "tree_sha", treeSHA)
	tree, err := g.GetGitTreeRecursive(ctx, repo.Owner, repo.Name, treeSHA)
	if err != nil {
		return nil, fmt.Errorf("failed to traverse git tree: %w", err)
	}

	var candidates []*LFSCandidate
	for _, entry := range tree.Entries {
		if err := ctx.Err(); err != nil {
			return candidates, err
		}
		if entry.GetType() != "blob" {
			continue
		}
		if uint64(entry.GetSize()) <= threshold {
			continue
		}
		if len(paths) > 0 && !paths[entry.GetPath()] {
			continue
		}
		candidates = append(candidates, &LFSCandidate{
			Path: entry.GetPath(),
			Size: uint64(entry.GetSize()),
			SHA:  entry.GetSHA(),
		})
	}

	logger.Info("detection complete", "found", len(candidates), "threshold_bytes", threshold)
	return candidates, nil
}

// SortCandidatesBy sorts candidates in-place by the given field ("size" or "path").
// If desc is true the order is reversed after sorting.
func SortCandidatesBy(candidates []*LFSCandidate, field string, desc bool) error {
	switch strings.ToLower(field) {
	case "size":
		slices.SortFunc(candidates, func(a, b *LFSCandidate) int {
			if a.Size != b.Size {
				if a.Size < b.Size {
					return -1
				}
				return 1
			}
			return strings.Compare(a.Path, b.Path)
		})
	case "path":
		slices.SortFunc(candidates, func(a, b *LFSCandidate) int {
			return strings.Compare(a.Path, b.Path)
		})
	default:
		return fmt.Errorf("unknown sort field %q: valid fields are size, path", field)
	}
	if desc {
		slices.Reverse(candidates)
	}
	return nil
}
