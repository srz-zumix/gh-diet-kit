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

// DefaultSizeThreshold is the default file size threshold (50 MiB) above which
// a blob is reported as an LFS candidate.
const DefaultSizeThreshold int64 = 50 * 1024 * 1024

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
	Size int    `json:"size"`
	SHA  string `json:"sha"`
}

// DetectLFSCandidates returns all blobs in the repository tree at ref that exceed
// threshold bytes. If ref is empty the repository's default branch is used.
// The tree is traversed recursively to avoid the GitHub API's 100,000-entry
// truncation limit; this may require multiple API calls for large repositories.
func DetectLFSCandidates(ctx context.Context, g *GitHubClient, repo repository.Repository, ref string, threshold int64) ([]*LFSCandidate, error) {
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
		if entry.GetType() != "blob" {
			continue
		}
		if int64(entry.GetSize()) <= threshold {
			continue
		}
		candidates = append(candidates, &LFSCandidate{
			Path: entry.GetPath(),
			Size: entry.GetSize(),
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
