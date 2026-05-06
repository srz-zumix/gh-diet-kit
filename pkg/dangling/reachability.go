package dangling

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/cli/go-gh/v2/pkg/repository"
	"github.com/google/go-github/v84/github"
	"github.com/srz-zumix/go-gh-extension/pkg/parser"
)

var defaultBranchCache sync.Map
var branchListCache sync.Map
var tagListCache sync.Map

// repoCacheKey returns a stable cache key for repository-scoped lookups.
// Host is included to correctly distinguish github.com from GitHub Enterprise
// Server instances when the same process targets multiple hosts.
func repoCacheKey(repo repository.Repository) string {
	return parser.GetRepositoryFullNameWithHost(repo)
}

// listBranchesCached returns all branches for the repository, caching the result
// for the duration of the process. The branch list is repo-scoped and stable for
// a single scan, so fetching it once avoids O(candidates) extra API calls in
// branches/refs reachability mode.
func listBranchesCached(ctx context.Context, g *GitHubClient, repo repository.Repository) ([]*github.Branch, error) {
	key := repoCacheKey(repo)
	if cached, ok := branchListCache.Load(key); ok {
		return cached.([]*github.Branch), nil
	}
	branches, err := g.ListBranches(ctx, repo.Owner, repo.Name, nil)
	if err != nil {
		return nil, err
	}
	branchListCache.Store(key, branches)
	return branches, nil
}

// listTagsCached returns all tags for the repository, caching the result for the
// duration of the process. Same rationale as listBranchesCached.
func listTagsCached(ctx context.Context, g *GitHubClient, repo repository.Repository) ([]*github.RepositoryTag, error) {
	key := repoCacheKey(repo)
	if cached, ok := tagListCache.Load(key); ok {
		return cached.([]*github.RepositoryTag), nil
	}
	tags, err := g.ListTags(ctx, repo.Owner, repo.Name)
	if err != nil {
		return nil, err
	}
	tagListCache.Store(key, tags)
	return tags, nil
}

// getDefaultBranch returns the repository default branch, caching the result to
// avoid fetching repository metadata for every reachability check.
func getDefaultBranch(ctx context.Context, g *GitHubClient, repo repository.Repository) (string, error) {
	if branch, ok := defaultBranchCache.Load(repoCacheKey(repo)); ok {
		if cached, ok := branch.(string); ok && cached != "" {
			return cached, nil
		}
	}

	repoInfo, err := g.GetRepository(ctx, repo.Owner, repo.Name)
	if err != nil {
		return "", err
	}

	defaultBranch := repoInfo.GetDefaultBranch()
	if defaultBranch == "" {
		defaultBranch = "main"
	}

	defaultBranchCache.Store(repoCacheKey(repo), defaultBranch)
	return defaultBranch, nil
}

// IsCommitReachableFromDefaultBranch reports whether sha is an ancestor of (or equal
// to) the repository's default branch, using the GitHub Compare API.
// Returns (true, nil) if reachable, (false, nil) if not reachable.
func IsCommitReachableFromDefaultBranch(ctx context.Context, g *GitHubClient, repo repository.Repository, sha string) (bool, error) {
	defaultBranch, err := getDefaultBranch(ctx, g, repo)
	if err != nil {
		return false, err
	}
	return isCommitReachableFromRef(ctx, g, repo, sha, defaultBranch)
}

// IsCommitReachableFromAnyBranch reports whether sha is an ancestor of any branch
// in the repository, using the GitHub Compare API. Short-circuits on first match.
// The branch list is fetched once and cached for subsequent calls on the same repo.
func IsCommitReachableFromAnyBranch(ctx context.Context, g *GitHubClient, repo repository.Repository, sha string) (bool, error) {
	branches, err := listBranchesCached(ctx, g, repo)
	if err != nil {
		return false, err
	}
	for _, b := range branches {
		reachable, err := isCommitReachableFromRef(ctx, g, repo, sha, b.GetName())
		if err != nil {
			if !isSkippableCompareError(err) {
				return false, err
			}
			// Skip branches that cannot be compared (e.g. empty repos, no common ancestor).
			continue
		}
		if reachable {
			return true, nil
		}
	}
	return false, nil
}

// IsCommitReachableFromAnyRef reports whether sha is an ancestor of any branch or
// tag in the repository, using the GitHub Compare API. Short-circuits on first match.
func IsCommitReachableFromAnyRef(ctx context.Context, g *GitHubClient, repo repository.Repository, sha string) (bool, error) {
	reachable, err := IsCommitReachableFromAnyBranch(ctx, g, repo, sha)
	if err != nil {
		return false, err
	}
	if reachable {
		return true, nil
	}
	tags, err := listTagsCached(ctx, g, repo)
	if err != nil {
		return false, err
	}
	for _, t := range tags {
		reachable, err := isCommitReachableFromRef(ctx, g, repo, sha, t.GetName())
		if err != nil {
			if !isSkippableCompareError(err) {
				return false, err
			}
			continue
		}
		if reachable {
			return true, nil
		}
	}
	return false, nil
}

// isCommitReachableFromRef reports whether sha is an ancestor of (or equal to) the
// given ref via the GitHub Compare API.
// compare(ref, sha): ahead_by == 0 means sha has no commits that are not in ref,
// i.e. sha is an ancestor of ref (or they are identical).
// Only metadata is fetched (no commit list pagination).
func isCommitReachableFromRef(ctx context.Context, g *GitHubClient, repo repository.Repository, sha, ref string) (bool, error) {
	comp, err := g.CompareCommitsMeta(ctx, repo.Owner, repo.Name, ref, sha)
	if err != nil {
		return false, err
	}
	return comp.GetAheadBy() == 0, nil
}

// isSkippableCompareError reports whether err is an expected, non-transient error
// from the Compare API that indicates the ref and sha simply cannot be compared
// (e.g. ref not found, no common ancestor). Transient errors such as rate-limit
// or server errors must NOT be skipped so that callers surface them correctly.
func isSkippableCompareError(err error) bool {
	// Rate-limit errors must be propagated, not silently skipped.
	if _, ok := errors.AsType[*github.RateLimitError](err); ok {
		return false
	}
	if _, ok := errors.AsType[*github.AbuseRateLimitError](err); ok {
		return false
	}
	// 404 (ref/sha not found) and 422 (no common ancestor, too divergent) are
	// expected when comparing across unrelated histories and can be skipped.
	if errResp, ok := errors.AsType[*github.ErrorResponse](err); ok {
		switch errResp.Response.StatusCode {
		case http.StatusNotFound, http.StatusUnprocessableEntity:
			return true
		}
	}
	return false
}
