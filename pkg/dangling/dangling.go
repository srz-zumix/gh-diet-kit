package dangling

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/cli/go-gh/v2/pkg/repository"
	"github.com/google/go-github/v84/github"
	"github.com/srz-zumix/go-gh-extension/pkg/gh"
	"github.com/srz-zumix/go-gh-extension/pkg/gitutil"
	"github.com/srz-zumix/go-gh-extension/pkg/logger"
	"golang.org/x/sync/errgroup"
)

// GitHubClient is a type alias for the shared GitHubClient from go-gh-extension.
// This allows callers to use gh.GitHubClient without importing go-gh-extension directly.
type GitHubClient = gh.GitHubClient

// NewGitHubClientWithRepo creates a GitHub API client configured for the given repository.
func NewGitHubClientWithRepo(repo repository.Repository) (*GitHubClient, error) {
	return gh.NewGitHubClientWithRepo(repo)
}

// IsHTTPNotFound reports whether err is a GitHub API 404 response.
func IsHTTPNotFound(err error) bool {
	return gh.IsHTTPNotFound(err)
}

// GetPRsByNumbers fetches specific pull request numbers from the repository.
func GetPRsByNumbers(ctx context.Context, g *GitHubClient, repo repository.Repository, numbers []int) ([]*github.PullRequest, error) {
	return gh.GetPRsByNumbers(ctx, g, repo, numbers)
}

// DanglingCommit represents a commit that is not reachable from normal branch/tag refs.
// These typically originate from squash or rebase merged pull requests where the
// original PR commits are not ancestors of the resulting merge commit, or commits
// dropped by force-pushes on the PR head branch.
type DanglingCommit struct {
	SHA           string  `json:"sha"`
	Message       string  `json:"message"`
	PRNumber      int     `json:"pr_number,omitempty"`
	PRURL         string  `json:"pr_url,omitempty"`
	TotalBlobSize *uint64 `json:"total_blob_size,omitempty"`
}

// DanglingBlob represents a blob that is referenced only by dangling commits.
// These blobs may exist in GitHub's object store but are not part of any current
// branch or tag tree.
type DanglingBlob struct {
	SHA       string `json:"sha"`
	Path      string `json:"path"`
	Size      *int   `json:"size,omitempty"` // nil when size is unavailable (e.g. diff-based detection)
	CommitSHA string `json:"commit_sha"`
	PRNumber  int    `json:"pr_number"`
	PRURL     string `json:"pr_url"`
}

// ReachabilityCheckMode specifies the method used to verify that a candidate commit
// is truly not reachable from any branch ref before reporting it as dangling.
// Zero value (empty string) disables the check.
type ReachabilityCheckMode string

const (
	// ReachabilityCheckNone skips reachability verification (default). Candidates
	// from PR history are reported as dangling without additional API/git checks.
	ReachabilityCheckNone ReachabilityCheckMode = "none"
	// ReachabilityCheckDefaultBranch uses the GitHub Compare API to confirm the
	// commit is not reachable from the repository's default branch.
	ReachabilityCheckDefaultBranch ReachabilityCheckMode = "default-branch"
	// ReachabilityCheckBranches uses the GitHub Compare API to confirm the commit
	// is not reachable from any branch. Requires one API call per branch per
	// candidate commit.
	ReachabilityCheckBranches ReachabilityCheckMode = "branches"
	// ReachabilityCheckRefs uses the GitHub Compare API to confirm the commit is
	// not reachable from any branch or tag. More thorough than branches but
	// requires additional API calls for tags.
	ReachabilityCheckRefs ReachabilityCheckMode = "refs"
	// ReachabilityCheckLocalObject checks whether the commit object exists in the
	// local git repository. Fastest check; a missing object means the commit is
	// not reachable from any remote ref. Stale loose objects from previous fetches
	// can cause false negatives. Requires git fetch --all --tags to have been run
	// first; git fetch --all alone does not fetch tags unreachable from any branch.
	ReachabilityCheckLocalObject ReachabilityCheckMode = "local-object"
	// ReachabilityCheckLocalRefs uses git branch -r --contains and git tag
	// --contains to confirm the commit is not reachable from any remote-tracking
	// branch or any tag. Requires git fetch --all --tags to have been run first;
	// git fetch --all alone does not fetch tags unreachable from any branch.
	ReachabilityCheckLocalRefs ReachabilityCheckMode = "local-refs"
)

// commitFetchConcurrency is the maximum number of concurrent GitHub API calls
// used when fetching commit blob info within a single PR. Keeping this small
// avoids secondary rate-limit spikes while still parallelising I/O.
const commitFetchConcurrency = 5

// ReachabilityCheckModeValues is the ordered list of valid ReachabilityCheckMode
// string values, suitable for use with flag enum helpers.
var ReachabilityCheckModeValues = []string{
	string(ReachabilityCheckNone),
	string(ReachabilityCheckDefaultBranch),
	string(ReachabilityCheckBranches),
	string(ReachabilityCheckRefs),
	string(ReachabilityCheckLocalObject),
	string(ReachabilityCheckLocalRefs),
}

// DanglingOptions controls which detection methods are active when searching for
// dangling commits and blobs. Zero value enables all detection methods and skips
// reachability verification.
type DanglingOptions struct {
	DisableSquashRebase bool // if true, skip squash/rebase merged PR commit detection
	DisableForcePush    bool // if true, skip force-push dropped commit detection
	DisableClosed       bool // if true, skip closed unmerged PR detection
	// ReachabilityCheck specifies an optional secondary verification step that
	// confirms each candidate commit is truly not reachable from any branch or tag
	// before it is included in results. Zero value skips the check.
	ReachabilityCheck ReachabilityCheckMode
	// StrictErrors controls behavior when API or git errors are encountered during
	// search. When false (default), errors are logged as warnings and processing
	// continues; results may be incomplete. When true, any error terminates the
	// search immediately.
	StrictErrors bool
	// GitDir, if non-empty, specifies the path to a git directory (e.g. a bare clone)
	// to use for local reachability checks instead of the current working directory.
	GitDir string
	// NoCache disables the per-PR result cache. When false (default), results for
	// each PR are written to disk and reused on subsequent runs with the same options,
	// allowing interrupted runs to resume without re-processing already-checked PRs.
	// Does not clear existing cache entries; combine with ClearCache to wipe and disable.
	NoCache bool
	// ClearCache clears the per-PR and commit blob cache directories before starting
	// the run. Can be combined with NoCache to wipe without using the cache, or used
	// alone to start fresh while still caching results of the current run.
	ClearCache bool
	// CommitFetchConcurrency is the maximum number of concurrent GitHub API calls
	// used when fetching commit blob info within a single PR. Zero or negative uses
	// commitFetchConcurrency as the default.
	CommitFetchConcurrency int
}

// fetchConcurrency returns the effective concurrency limit for commit blob fetches.
// It falls back to commitFetchConcurrency when CommitFetchConcurrency is not positive.
func (o DanglingOptions) fetchConcurrency() int {
	if o.CommitFetchConcurrency > 0 {
		return o.CommitFetchConcurrency
	}
	return commitFetchConcurrency
}

// parentUnreachable reports whether any of the given parent commits has a SHA
// already recorded in unreachableSHAs. Because reachability from any ref is
// closed under ancestry, a commit whose parent is unreachable is also unreachable,
// so no further API or git call is needed.
func parentUnreachable(parents []*github.Commit, unreachableSHAs map[string]bool) bool {
	for _, p := range parents {
		if unreachableSHAs[p.GetSHA()] {
			return true
		}
	}
	return false
}

// isCommitDanglingByReachability returns true if sha should be included in dangling
// results according to the configured ReachabilityCheck.
// Returns (true, nil) when no check is configured, meaning the commit is assumed dangling.
//
// Remote existence of sha is assumed: callers must have obtained sha via a GitHub API
// call (e.g. ListPullRequestCommits) which already confirms the commit exists on the
// remote. The local-object and local-refs modes only check local reachability; they do
// NOT re-confirm remote existence, relying on the caller's prior API fetch.
func isCommitDanglingByReachability(ctx context.Context, g *GitHubClient, repo repository.Repository, sha string, opts DanglingOptions) (bool, error) {
	switch opts.ReachabilityCheck {
	case ReachabilityCheckNone, "":
		return true, nil
	case ReachabilityCheckDefaultBranch:
		reachable, err := IsCommitReachableFromDefaultBranch(ctx, g, repo, sha)
		if err != nil {
			return false, err
		}
		return !reachable, nil
	case ReachabilityCheckBranches:
		reachable, err := IsCommitReachableFromAnyBranch(ctx, g, repo, sha)
		if err != nil {
			return false, err
		}
		return !reachable, nil
	case ReachabilityCheckRefs:
		reachable, err := IsCommitReachableFromAnyRef(ctx, g, repo, sha)
		if err != nil {
			return false, err
		}
		return !reachable, nil
	case ReachabilityCheckLocalObject:
		exists, err := gitutil.IsCommitObjectExists(ctx, gitutil.ClientForDir(opts.GitDir), sha)
		if err != nil {
			return false, err
		}
		return !exists, nil
	case ReachabilityCheckLocalRefs:
		reachable, err := gitutil.IsCommitReachableFromAnyRef(ctx, gitutil.ClientForDir(opts.GitDir), sha)
		if err != nil {
			return false, err
		}
		return !reachable, nil
	default:
		return false, fmt.Errorf("unknown reachability check mode %q", opts.ReachabilityCheck)
	}
}

// commitBlobInfo holds a fetched commit and its blob SHA→size map.
// BlobSizeMap is nil when tree sizes could not be retrieved.
type commitBlobInfo struct {
	Commit      *github.RepositoryCommit
	BlobSizeMap map[string]int // nil when unavailable
}

// fetchCommitBlobInfo fetches the full commit diff and builds a blob SHA→size map
// from the commit's tree. Returns (nil, nil) when the commit should be skipped
// (404 or lenient non-fatal error). Returns (nil, err) on a fatal error.
// When blobCache is non-nil, cached results are returned immediately, and newly
// fetched results are persisted only when the blob size map is available.
func fetchCommitBlobInfo(ctx context.Context, g *GitHubClient, repo repository.Repository, sha string, opts DanglingOptions, blobCache *commitBlobCache) (*commitBlobInfo, error) {
	if cached := blobCache.load(sha); cached != nil {
		logger.Debug("commit blob cache hit", "sha", sha)
		return cached.toCommitBlobInfo(), nil
	}

	commit, err := g.GetCommit(ctx, repo.Owner, repo.Name, sha)
	if err != nil {
		if IsHTTPNotFound(err) {
			logger.Debug("skipping commit: not found on remote", "sha", sha)
			return nil, nil
		}
		if opts.StrictErrors {
			return nil, fmt.Errorf("failed to fetch commit %s: %w", sha, err)
		}
		logger.Warn("skipping commit: failed to fetch (lenient mode)", "sha", sha, "error", err)
		return nil, nil
	}

	info := &commitBlobInfo{Commit: commit}
	if innerCommit := commit.GetCommit(); innerCommit != nil {
		if treeSHA := innerCommit.GetTree().GetSHA(); treeSHA != "" {
			tree, treeErr := g.GetGitTreeRecursive(ctx, repo.Owner, repo.Name, treeSHA)
			if treeErr != nil {
				if opts.StrictErrors {
					return nil, fmt.Errorf("failed to get tree for commit %s: %w", sha, treeErr)
				}
				logger.Warn("skipping blob sizes: failed to fetch tree (lenient mode)", "sha", sha, "error", treeErr)
			} else {
				info.BlobSizeMap = make(map[string]int, len(tree.Entries))
				for _, entry := range tree.Entries {
					if entry.GetType() == "blob" {
						info.BlobSizeMap[entry.GetSHA()] = entry.GetSize()
					}
				}
			}
		}
	}
	if info.BlobSizeMap != nil {
		blobCache.save(sha, info)
	}
	return info, nil
}

// sumDiffBlobSize sums the sizes of unique blobs added or modified by a commit diff.
// Removed and rename-only files are excluded. Returns nil when blobSizeMap is nil.
func sumDiffBlobSize(files []*github.CommitFile, blobSizeMap map[string]int) *uint64 {
	if blobSizeMap == nil {
		return nil
	}
	var total uint64
	seen := make(map[string]bool)
	for _, f := range files {
		status := f.GetStatus()
		if status == "removed" || (status == "renamed" && f.GetChanges() == 0) {
			continue
		}
		blobSHA := f.GetSHA()
		if blobSHA == "" || seen[blobSHA] {
			continue
		}
		seen[blobSHA] = true
		if sz, ok := blobSizeMap[blobSHA]; ok {
			total += uint64(sz)
		}
	}
	return &total
}

// isSquashOrRebaseMerge returns true when the merge commit does NOT have the PR head
// SHA as a direct parent. This indicates a squash or rebase merge strategy, which
// leaves the original PR commits unreachable from normal branch refs.
func isSquashOrRebaseMerge(mergeCommit *github.RepositoryCommit, prHeadSHA string) bool {
	for _, parent := range mergeCommit.Parents {
		if parent.GetSHA() == prHeadSHA {
			return false
		}
	}
	return true
}

// appendUniqueCommitsBySHA appends commits whose SHA was not seen yet.
func appendUniqueCommitsBySHA(dst []*github.RepositoryCommit, seen map[string]bool, src []*github.RepositoryCommit) []*github.RepositoryCommit {
	for _, c := range src {
		sha := c.GetSHA()
		if sha == "" || seen[sha] {
			continue
		}
		seen[sha] = true
		dst = append(dst, c)
	}
	return dst
}

// listForcePushedOutPRCommits returns commits that became unreachable from a PR
// head branch due to head_ref_force_pushed timeline events.
// Each force-push event is processed independently; a Compare failure for one
// event does not discard results already collected from earlier events.
// Skippable errors (HTTP 404/422, e.g. no common ancestor) are always silently
// skipped per event. Non-skippable errors (rate-limit, 5xx, etc.) are fatal
// when opts.StrictErrors is true, or logged and skipped otherwise.
func listForcePushedOutPRCommits(ctx context.Context, g *GitHubClient, repo repository.Repository, prNumber int, opts DanglingOptions) ([]*github.RepositoryCommit, error) {
	events, err := g.ListPullRequestHeadRefForcePushEvents(ctx, repo.Owner, repo.Name, prNumber)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var result []*github.RepositoryCommit
	for _, e := range events {
		if e.BeforeSHA == "" || e.AfterSHA == "" {
			continue
		}
		logger.Debug("processing force-push event", "pr", prNumber, "before", e.BeforeSHA, "after", e.AfterSHA)

		// Compare after...before to enumerate commits that existed before the force-push
		// and are no longer reachable from the updated head.
		comp, err := g.CompareCommits(ctx, repo.Owner, repo.Name, e.AfterSHA, e.BeforeSHA)
		if err != nil {
			if isSkippableCompareError(err) {
				// 404 = no common ancestor (e.g. rewrite onto unrelated history) or
				// 422 = invalid range; commits from this event are not enumerable.
				logger.Debug("skipping force-push event: compare not available", "pr", prNumber, "before", e.BeforeSHA, "after", e.AfterSHA, "error", err)
				continue
			}
			if opts.StrictErrors {
				return nil, fmt.Errorf("compare commits for force-push event in PR #%d before=%s after=%s: %w", prNumber, e.BeforeSHA, e.AfterSHA, err)
			}
			logger.Warn("skipping force-push event: compare failed (lenient mode)", "pr", prNumber, "before", e.BeforeSHA, "after", e.AfterSHA, "error", err)
			continue
		}

		result = appendUniqueCommitsBySHA(result, seen, comp.Commits)
	}

	return result, nil
}

// listSquashRebaseChainCandidates returns the PR commit chain (oldest first)
// when the PR was merged via squash or rebase. Returns nil for regular merges or
// when required metadata is missing. The returned slice is the linear ancestry
// chain leading to the PR head, suitable for the head-first reachability shortcut.
func listSquashRebaseChainCandidates(ctx context.Context, g *GitHubClient, repo repository.Repository, pr *github.PullRequest) ([]*github.RepositoryCommit, error) {
	mergeCommitSHA := pr.GetMergeCommitSHA()
	head := pr.GetHead()
	if mergeCommitSHA == "" {
		logger.Debug("skipping squash/rebase check: no merge commit SHA", "pr", pr.GetNumber())
		return nil, nil
	}
	if head == nil || head.GetSHA() == "" {
		logger.Debug("skipping squash/rebase check: no head SHA", "pr", pr.GetNumber())
		return nil, nil
	}
	mergeCommit, err := g.GetCommitMeta(ctx, repo.Owner, repo.Name, mergeCommitSHA)
	if err != nil {
		return nil, fmt.Errorf("failed to get merge commit for PR #%d: %w", pr.GetNumber(), err)
	}
	if !isSquashOrRebaseMerge(mergeCommit, head.GetSHA()) {
		logger.Debug("PR is regular merge (not squash/rebase)", "pr", pr.GetNumber())
		return nil, nil
	}
	logger.Debug("found squash/rebase merged PR", "pr", pr.GetNumber())
	prCommits, err := g.ListPullRequestCommits(ctx, repo.Owner, repo.Name, pr.GetNumber())
	if err != nil {
		return nil, fmt.Errorf("failed to list commits for PR #%d: %w", pr.GetNumber(), err)
	}
	return prCommits, nil
}

// listClosedUnmergedChainCandidates returns the PR commit chain (oldest first)
// for a closed, unmerged PR whose head branch is gone. Returns nil for active
// fork PRs (head branch lives in another repository and we cannot confirm it is
// deleted) and when the head branch still exists on the base repository. Errors
// other than 404 from the branch existence check are propagated to avoid
// misclassification on transient failures.
//
// When pr.Head.Repo is nil the source fork has been deleted, which means the
// head branch is definitively gone. Those commits are included as candidates
// even though they originated from a fork.
func listClosedUnmergedChainCandidates(ctx context.Context, g *GitHubClient, repo repository.Repository, pr *github.PullRequest) ([]*github.RepositoryCommit, error) {
	headRepo := pr.GetHead().GetRepo()
	baseRepo := pr.GetBase().GetRepo()

	// Active fork PR: head branch lives in another repository and we cannot
	// confirm it is deleted without querying the fork.
	if headRepo != nil && baseRepo != nil && headRepo.GetFullName() != baseRepo.GetFullName() {
		logger.Debug("skipping closed unmerged fork PR", "pr", pr.GetNumber(), "head_repo", headRepo.GetFullName())
		return nil, nil
	}

	if headRepo == nil {
		// Source fork has been deleted: head branch is definitively gone.
		// Commits are still accessible via the base repo's PR ref.
		logger.Debug("head repo is nil (deleted fork), treating PR commits as dangling", "pr", pr.GetNumber())
	} else {
		headRef := pr.GetHead().GetRef()
		if headRef != "" {
			_, err := g.GetBranch(ctx, repo.Owner, repo.Name, headRef)
			if err == nil {
				logger.Debug("skipping closed PR: head branch still exists", "pr", pr.GetNumber(), "branch", headRef)
				return nil, nil
			}
			if !IsHTTPNotFound(err) {
				return nil, fmt.Errorf("failed to check branch %q for PR #%d: %w", headRef, pr.GetNumber(), err)
			}
			logger.Debug("head branch not found, treating PR commits as dangling", "pr", pr.GetNumber(), "branch", headRef)
		}
	}

	prCommits, err := g.ListPullRequestCommits(ctx, repo.Owner, repo.Name, pr.GetNumber())
	if err != nil {
		return nil, fmt.Errorf("failed to list commits for PR #%d: %w", pr.GetNumber(), err)
	}
	return prCommits, nil
}

// listCandidatesForPR returns the linear PR commit chain (oldest first) for a PR.
// The chain is suitable for chain-level reachability shortcuts.
// Force-push dropped commits are collected separately by the caller.
func listCandidatesForPR(ctx context.Context, g *GitHubClient, repo repository.Repository, pr *github.PullRequest, opts DanglingOptions) ([]*github.RepositoryCommit, error) {
	if pr.MergedAt != nil {
		if opts.DisableSquashRebase {
			logger.Debug("skipping squash/rebase detection: disabled", "pr", pr.GetNumber())
			return nil, nil
		}
		return listSquashRebaseChainCandidates(ctx, g, repo, pr)
	}
	if opts.DisableClosed {
		logger.Debug("skipping closed unmerged PR: closed detection disabled", "pr", pr.GetNumber())
		return nil, nil
	}
	return listClosedUnmergedChainCandidates(ctx, g, repo, pr)
}

// ListClosedPRs returns all closed pull requests for the repository, ordered by
// most recently updated. maxPRs limits the number of results; use -1 for unlimited.
func ListClosedPRs(ctx context.Context, g *GitHubClient, repo repository.Repository, maxPRs int) ([]*github.PullRequest, error) {
	opts := &github.PullRequestListOptions{
		State:     "closed",
		Sort:      "updated",
		Direction: "desc",
	}
	prs, err := g.ListPullRequests(ctx, repo.Owner, repo.Name, opts, maxPRs)
	if err != nil {
		return nil, fmt.Errorf("failed to list pull requests for %s/%s: %w", repo.Owner, repo.Name, err)
	}
	return prs, nil
}

// checkCommitDangling determines whether a single commit should be treated as
// dangling, applying the parent-based shortcut and recording the result in
// unreachableSHAs when applicable. It is the single per-commit reachability
// decision used by both chain and force-push processing.
func checkCommitDangling(ctx context.Context, g *GitHubClient, repo repository.Repository, c *github.RepositoryCommit, opts DanglingOptions, unreachableSHAs map[string]bool) (bool, error) {
	sha := c.GetSHA()
	if unreachableSHAs[sha] {
		return true, nil
	}
	if parentUnreachable(c.Parents, unreachableSHAs) {
		unreachableSHAs[sha] = true
		return true, nil
	}
	dangling, err := isCommitDanglingByReachability(ctx, g, repo, sha, opts)
	if err != nil {
		if opts.StrictErrors {
			return false, err
		}
		// Lenient mode: treat as dangling but do not update unreachableSHAs to
		// avoid cascading misclassification when the error is transient.
		logger.Warn("reachability check failed, treating commit as dangling (lenient mode)", "sha", sha, "error", err)
		return true, nil
	}
	if dangling {
		unreachableSHAs[sha] = true
	}
	return dangling, nil
}

// processChainCandidates returns the dangling subset of a linear PR commit
// chain (oldest first). Reachability is closed under ancestry:
//   - oldest unreachable → all descendants unreachable via parent shortcut (common case)
//   - oldest reachable → check newest; if newest is also reachable → all chain reachable
//
// Check order: oldest → (if reachable) newest → parent shortcut from oldest.
// For the common "all unreachable" scenario, this costs exactly 1 API call.
func processChainCandidates(ctx context.Context, g *GitHubClient, repo repository.Repository, chain []*github.RepositoryCommit, opts DanglingOptions, unreachableSHAs map[string]bool) ([]*github.RepositoryCommit, error) {
	if len(chain) == 0 {
		return nil, nil
	}

	// Two-endpoint shortcut: only when reachability verification is active and
	// there are at least two commits to make the pre-checks worthwhile.
	if opts.ReachabilityCheck != ReachabilityCheckNone && opts.ReachabilityCheck != "" && len(chain) > 1 {
		oldest := chain[0]
		if !unreachableSHAs[oldest.GetSHA()] && !parentUnreachable(oldest.Parents, unreachableSHAs) {
			oldestDangling, err := isCommitDanglingByReachability(ctx, g, repo, oldest.GetSHA(), opts)
			if err != nil {
				if opts.StrictErrors {
					return nil, fmt.Errorf("check chain oldest reachability for %s: %w", oldest.GetSHA(), err)
				}
				// Lenient mode: skip the two-endpoint shortcut and fall through to per-commit iteration.
				logger.Warn("reachability check failed for chain oldest, skipping chain shortcut", "sha", oldest.GetSHA(), "error", err)
			} else if oldestDangling {
				// Oldest unreachable → parent shortcut propagates to every later commit
				// in the chain; no further API/git calls needed for this shortcut.
				unreachableSHAs[oldest.GetSHA()] = true
				logger.Debug("chain oldest unreachable, parent shortcut covers rest", "sha", oldest.GetSHA(), "chain_len", len(chain))
			} else {
				// Oldest reachable → check newest for a full-chain reachable shortcut.
				newest := chain[len(chain)-1]
				if !unreachableSHAs[newest.GetSHA()] && !parentUnreachable(newest.Parents, unreachableSHAs) {
					newestDangling, err := isCommitDanglingByReachability(ctx, g, repo, newest.GetSHA(), opts)
					if err != nil {
						if opts.StrictErrors {
							return nil, fmt.Errorf("check chain newest reachability for %s: %w", newest.GetSHA(), err)
						}
						// Lenient mode: skip full-chain shortcut and fall through to per-commit iteration.
						logger.Warn("reachability check failed for chain newest, skipping full-chain shortcut", "sha", newest.GetSHA(), "error", err)
					} else if !newestDangling {
						// Both endpoints reachable → all chain commits reachable; skip.
						logger.Debug("skipping chain: oldest and newest both reachable", "oldest", oldest.GetSHA(), "newest", newest.GetSHA())
						return nil, nil
					} else {
						unreachableSHAs[newest.GetSHA()] = true
					}
				}
			}
		}
	}

	var result []*github.RepositoryCommit
	for _, c := range chain {
		dangling, err := checkCommitDangling(ctx, g, repo, c, opts, unreachableSHAs)
		if err != nil {
			return nil, fmt.Errorf("check commit reachability for %s: %w", c.GetSHA(), err)
		}
		if dangling {
			result = append(result, c)
		} else {
			logger.Debug("skipping commit: reachable from a ref", "sha", c.GetSHA())
		}
	}
	return result, nil
}

// processPRCandidates applies reachability checks to the chain and force-push
// candidate lists, then invokes visit with the confirmed dangling commits.
// Returns nil immediately when no dangling commits are found.
// This is the common final step shared by the cache-hit path and the normal path.
func processPRCandidates(ctx context.Context, g *GitHubClient, repo repository.Repository, pr *github.PullRequest, chain, forcePushed []*github.RepositoryCommit, opts DanglingOptions, unreachableSHAs map[string]bool, visit danglingCommitVisitor) error {
	chainDangling, err := processChainCandidates(ctx, g, repo, chain, opts, unreachableSHAs)
	if err != nil {
		if opts.StrictErrors {
			return err
		}
		logger.Warn("partial result: chain reachability check failed", "pr", pr.GetNumber(), "error", err)
	}
	var fpDangling []*github.RepositoryCommit
	for _, c := range forcePushed {
		dangling, err := checkCommitDangling(ctx, g, repo, c, opts, unreachableSHAs)
		if err != nil {
			return fmt.Errorf("check commit reachability for %s: %w", c.GetSHA(), err)
		}
		if dangling {
			fpDangling = append(fpDangling, c)
		}
	}
	combined := append(chainDangling, fpDangling...)
	if len(combined) == 0 {
		return nil
	}
	return visit(pr, combined)
}

// danglingCommitVisitor is invoked for each PR with the confirmed dangling
// commits found in that PR. The slice combines chain and force-push commits,
// preserving their original collection order.
type danglingCommitVisitor func(pr *github.PullRequest, commits []*github.RepositoryCommit) error

// iterateDanglingCommits walks the PR list, collects candidates, applies all
// reachability shortcuts, and invokes visit for each PR that has at least one
// confirmed dangling commit. It is the shared driver behind FindDanglingCommits
// and FindDanglingBlobs.
func iterateDanglingCommits(ctx context.Context, g *GitHubClient, repo repository.Repository, prs []*github.PullRequest, opts DanglingOptions, visit danglingCommitVisitor) error {
	// unreachableSHAs accumulates commit SHAs confirmed unreachable from any ref.
	// It is shared across PRs because reachability is a property of the commit
	// object itself, not of any individual PR.
	unreachableSHAs := make(map[string]bool)

	var cache *prCache
	if opts.ClearCache {
		newPRCache(repo).clear()
	}
	if !opts.NoCache {
		cache = newPRCache(repo)
	}

	for i, pr := range prs {
		if err := ctx.Err(); err != nil {
			return err
		}
		logger.Debug("checking PR", "progress", fmt.Sprintf("%d/%d", i+1, len(prs)), "pr", pr.GetNumber(), "title", pr.GetTitle())

		// Skip merged PRs that have all detection methods disabled.
		if pr.MergedAt != nil && opts.DisableSquashRebase && opts.DisableForcePush {
			logger.Debug("skipping merged PR: all merged-PR methods disabled", "pr", pr.GetNumber())
			continue
		}

		headSHA := prHeadSHA(pr)
		chainEnabled := (pr.MergedAt != nil && !opts.DisableSquashRebase) ||
			(pr.MergedAt == nil && !opts.DisableClosed)
		fpEnabled := !opts.DisableForcePush

		// Initialize candidates; these may be pre-populated from cache below.
		// needChain/needFP track which collections still require a live API call.
		var chain []*github.RepositoryCommit
		var forcePushed []*github.RepositoryCommit
		var chainFailed, fpFailed bool
		needChain := chainEnabled
		needFP := fpEnabled

		if headSHA != "" {
			if cached := cache.load(pr.GetNumber(), headSHA); cached != nil {
				// Pre-populate from cache for each collection scope that was covered.
				// A scope not covered by the cache still requires a live API call below.
				if !chainEnabled || cached.ChainCollected {
					if chainEnabled {
						chain = reconstruct(cached.ChainCommits)
					}
					needChain = false
				}
				if !fpEnabled || cached.ForcePushCollected {
					if fpEnabled {
						forcePushed = reconstruct(cached.ForcePushCommits)
					}
					needFP = false
				}
				if !needChain && !needFP {
					logger.Debug("pr cache hit", "pr", pr.GetNumber())
				} else {
					logger.Debug("pr cache partial hit: re-collecting missing data", "pr", pr.GetNumber(),
						"need_chain", needChain, "need_fp", needFP)
				}
			}
		}

		// Collect only the data not already loaded from cache.
		// Chain and force-push lookups are independent; run them concurrently.
		// errgroup.WithContext ensures that if either goroutine returns a fatal
		// error the derived context is canceled so the other goroutine stops
		// making further API calls. Wait() guarantees no in-flight work continues
		// after this block exits.
		if needChain || needFP {
			collEG, collCtx := errgroup.WithContext(ctx)
			if needChain {
				collEG.Go(func() error {
					c, err := listCandidatesForPR(collCtx, g, repo, pr, opts)
					if err != nil {
						if opts.StrictErrors {
							return err
						}
						logger.Warn("partial result: chain collection failed (lenient mode)", "pr", pr.GetNumber(), "error", err)
						chainFailed = true
						return nil
					}
					chain = c
					return nil
				})
			}
			if needFP {
				collEG.Go(func() error {
					fp, err := listForcePushedOutPRCommits(collCtx, g, repo, pr.GetNumber(), opts)
					if err != nil {
						if opts.StrictErrors {
							return err
						}
						logger.Debug("skipping force-push based commit collection", "pr", pr.GetNumber(), "error", err)
						fpFailed = true
						return nil
					}
					forcePushed = fp
					return nil
				})
			}
			if err := collEG.Wait(); err != nil {
				return err
			}

			// Persist only when new data was fetched (partial or full cache miss).
			// chainCollected/fpCollected flags in the entry tell future runs whether
			// each collection was both enabled and successful.
			if headSHA != "" {
				cache.save(pr.GetNumber(), headSHA, chain, forcePushed,
					chainEnabled && !chainFailed,
					fpEnabled && !fpFailed)
			}
		}

		if len(chain) == 0 && len(forcePushed) == 0 {
			continue
		}

		if err := processPRCandidates(ctx, g, repo, pr, chain, forcePushed, opts, unreachableSHAs, visit); err != nil {
			return err
		}
	}
	return nil
}

// FindDanglingCommits finds commits that are not reachable from any normal branch
// or tag ref. Detection methods are controlled by opts:
//   - Squash/rebase merged PR commits (disabled by opts.DisableSquashRebase)
//   - Commits dropped by force-push on a PR head branch (disabled by opts.DisableForcePush)
//   - All commits from closed unmerged PRs (disabled by opts.DisableClosed)
//
// Note: GitHub retains refs/pull/{number}/head for all PRs, so these commits remain
// accessible via PR refs even after the source branch is deleted.
//
// Limitation: closed unmerged PRs from active forks are not reported. The head
// branch lives in the fork repository and we cannot confirm it is deleted without
// querying the fork. PRs from deleted forks (pr.Head.Repo == nil) are included.
func FindDanglingCommits(ctx context.Context, g *GitHubClient, repo repository.Repository, prs []*github.PullRequest, opts DanglingOptions) ([]*DanglingCommit, error) {
	var result []*DanglingCommit
	var blobCache *commitBlobCache
	if opts.ClearCache {
		newCommitBlobCache(repo).clear()
	}
	if !opts.NoCache {
		blobCache = newCommitBlobCache(repo)
	}
	err := iterateDanglingCommits(ctx, g, repo, prs, opts, func(pr *github.PullRequest, commits []*github.RepositoryCommit) error {
		// Fetch blob info for all unique commit SHAs in this PR concurrently.
		// This avoids concurrent cache access for duplicate SHAs while preserving
		// the original commit order in the output.
		infos := make([]*commitBlobInfo, len(commits))
		shaIndexes := make(map[string][]int, len(commits))
		shaCommits := make(map[string]*github.RepositoryCommit, len(commits))
		for i, c := range commits {
			sha := c.GetSHA()
			shaIndexes[sha] = append(shaIndexes[sha], i)
			if _, ok := shaCommits[sha]; !ok {
				shaCommits[sha] = c
			}
		}

		eg, egCtx := errgroup.WithContext(ctx)
		eg.SetLimit(opts.fetchConcurrency())
		for sha := range shaCommits {
			sha := sha
			eg.Go(func() error {
				info, err := fetchCommitBlobInfo(egCtx, g, repo, sha, opts, blobCache)
				if err != nil {
					return err
				}
				for _, idx := range shaIndexes[sha] {
					infos[idx] = info
				}
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return err
		}
		for i, c := range commits {
			info := infos[i]
			if info == nil {
				continue
			}
			message := ""
			if info.Commit.GetCommit() != nil {
				message = info.Commit.GetCommit().GetMessage()
			}
			result = append(result, &DanglingCommit{
				SHA:           c.GetSHA(),
				Message:       message,
				PRNumber:      pr.GetNumber(),
				PRURL:         pr.GetHTMLURL(),
				TotalBlobSize: sumDiffBlobSize(info.Commit.Files, info.BlobSizeMap),
			})
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

// FindDanglingBlobs finds blobs that were introduced by dangling commits but are
// not reachable from any current branch or tag. Only files added or modified by
// each dangling commit are considered; files removed by the commit are skipped
// because those blobs may still be referenced by the parent commit's tree.
//
// False positives: Git blobs are content-addressed, so a blob introduced by a
// dangling commit may also appear in a live branch tree via identical file content
// (e.g. package-lock.json, generated files). Without a local reachability check
// there is no API-efficient way to detect this. Use --reachability-check
// local-object (after git fetch --all --tags) to filter out blobs that are
// still reachable from any local ref. Note: git fetch --all alone does not
// fetch tags unreachable from any branch.
//
// Blobs are deduplicated by SHA within each PR.
func FindDanglingBlobs(ctx context.Context, g *GitHubClient, repo repository.Repository, prs []*github.PullRequest, opts DanglingOptions) ([]*DanglingBlob, error) {
	var result []*DanglingBlob
	var blobCache *commitBlobCache
	if opts.ClearCache {
		newCommitBlobCache(repo).clear()
	}
	if !opts.NoCache {
		blobCache = newCommitBlobCache(repo)
	}
	// reachableBlobSHAs caches blob SHAs confirmed reachable from a local ref so
	// they are not re-checked across multiple PRs. Only positive (reachable)
	// results are cached because a reachable blob must be skipped globally,
	// whereas unreachable blobs are per-PR-deduplicated via the visitor's seen map.
	reachableBlobSHAs := make(map[string]bool)
	err := iterateDanglingCommits(ctx, g, repo, prs, opts, func(pr *github.PullRequest, commits []*github.RepositoryCommit) error {
		// Fetch blob info for all commits in this PR concurrently, then process
		// results sequentially for correct blob deduplication and cross-PR caching.
		infos := make([]*commitBlobInfo, len(commits))
		eg, egCtx := errgroup.WithContext(ctx)
		eg.SetLimit(opts.fetchConcurrency())
		for i, c := range commits {
			i, c := i, c
			eg.Go(func() error {
				info, err := fetchCommitBlobInfo(egCtx, g, repo, c.GetSHA(), opts, blobCache)
				if err != nil {
					return err
				}
				infos[i] = info
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return err
		}

		// Deduplicate blob SHAs within this PR to avoid redundant output.
		seen := make(map[string]bool)
		for i, c := range commits {
			commitSHA := c.GetSHA()
			info := infos[i]
			if info == nil {
				continue
			}
			for _, f := range info.Commit.Files {
				// Removed files remain referenced by the parent tree; skip them.
				// Rename-only files also keep referencing an existing blob from the parent tree.
				status := f.GetStatus()
				if status == "removed" || (status == "renamed" && f.GetChanges() == 0) {
					continue
				}
				blobSHA := f.GetSHA()
				if blobSHA == "" || seen[blobSHA] {
					continue
				}
				// When a local reachability check is active, verify the blob is not
				// reachable from any local ref. Git blobs are content-addressed, so
				// identical content in a live branch tree has the same SHA and would
				// otherwise be falsely reported as dangling.
				if opts.ReachabilityCheck == ReachabilityCheckLocalObject {
					if reachableBlobSHAs[blobSHA] {
						logger.Debug("skipping blob: reachable from a local ref (cached)", "sha", blobSHA)
						seen[blobSHA] = true
						continue
					}
					reachable, blobReachErr := gitutil.IsBlobReachableFromAnyRef(ctx, gitutil.ClientForDir(opts.GitDir), blobSHA)
					if blobReachErr != nil {
						if opts.StrictErrors {
							return fmt.Errorf("check blob reachability for %s: %w", blobSHA, blobReachErr)
						}
						logger.Warn("blob reachability check failed, treating as dangling (lenient mode)", "sha", blobSHA, "error", blobReachErr)
					} else if reachable {
						logger.Debug("skipping blob: reachable from a local ref", "sha", blobSHA)
						reachableBlobSHAs[blobSHA] = true
						seen[blobSHA] = true
						continue
					}
				}
				seen[blobSHA] = true
				var blobSize *int
				if info.BlobSizeMap != nil {
					if sz, ok := info.BlobSizeMap[blobSHA]; ok {
						s := sz
						blobSize = &s
					}
				}
				result = append(result, &DanglingBlob{
					SHA:       blobSHA,
					Path:      f.GetFilename(),
					Size:      blobSize,
					CommitSHA: commitSHA,
					PRNumber:  pr.GetNumber(),
					PRURL:     pr.GetHTMLURL(),
				})
			}
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

// SortBlobsBy sorts blobs in-place by the given field name (case-insensitive).
// Supported fields: "size", "path", "pr_number".
// desc=true reverses the order.
// Returns an error for unknown field names.
func SortBlobsBy(blobs []*DanglingBlob, field string, desc bool) error {
	var less func(a, b *DanglingBlob) int
	switch strings.ToLower(field) {
	case "size":
		derefBlob := func(p *int) int {
			if p == nil {
				return 0
			}
			return *p
		}
		less = func(a, b *DanglingBlob) int { return cmp.Compare(derefBlob(a.Size), derefBlob(b.Size)) }
	case "path":
		less = func(a, b *DanglingBlob) int { return cmp.Compare(a.Path, b.Path) }
	case "pr_number":
		less = func(a, b *DanglingBlob) int { return cmp.Compare(a.PRNumber, b.PRNumber) }
	default:
		return fmt.Errorf("unknown sort field %q: valid values are size, path, pr_number", field)
	}
	slices.SortStableFunc(blobs, func(a, b *DanglingBlob) int {
		if desc {
			return less(b, a)
		}
		return less(a, b)
	})
	return nil
}

// SortCommitsBy sorts commits in-place by the given field name (case-insensitive).
// Supported fields: "size", "pr_number".
// desc=true reverses the order.
// Returns an error for unknown field names.
func SortCommitsBy(commits []*DanglingCommit, field string, desc bool) error {
	var less func(a, b *DanglingCommit) int
	switch strings.ToLower(field) {
	case "size":
		deref := func(p *uint64) uint64 {
			if p == nil {
				return 0
			}
			return *p
		}
		less = func(a, b *DanglingCommit) int { return cmp.Compare(deref(a.TotalBlobSize), deref(b.TotalBlobSize)) }
	case "pr_number":
		less = func(a, b *DanglingCommit) int { return cmp.Compare(a.PRNumber, b.PRNumber) }
	default:
		return fmt.Errorf("unknown sort field %q: valid values are size, pr_number", field)
	}
	slices.SortStableFunc(commits, func(a, b *DanglingCommit) int {
		if desc {
			return less(b, a)
		}
		return less(a, b)
	})
	return nil
}

// FindLocalDanglingCommitsOnRemote checks which of the given commit SHAs exist on
// the remote GitHub repository. SHAs that are not found on the remote (404) are
// skipped and treated as not present. Any other error (auth, rate-limit, network,
// etc.) is returned immediately to prevent silent partial results.
// GetCommitMeta is used instead of GetCommit to avoid paginating file details,
// since only commit existence and message are needed.
func FindLocalDanglingCommitsOnRemote(ctx context.Context, g *GitHubClient, repo repository.Repository, shas []string) ([]*DanglingCommit, error) {
	logger.Info("checking local dangling commits against remote", "total", len(shas))
	var result []*DanglingCommit
	for _, sha := range shas {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		commit, err := g.GetCommitMeta(ctx, repo.Owner, repo.Name, sha)
		if err != nil {
			if IsHTTPNotFound(err) {
				logger.Debug("commit not found on remote", "sha", sha)
				continue
			}
			return result, fmt.Errorf("failed to get commit %s from remote: %w", sha, err)
		}
		message := ""
		if commit.GetCommit() != nil {
			message = commit.GetCommit().GetMessage()
		}
		result = append(result, &DanglingCommit{
			SHA:     sha,
			Message: message,
		})
	}
	logger.Info("local dangling commit check complete", "found", len(result))
	return result, nil
}
