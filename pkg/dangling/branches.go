package dangling

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/cli/go-gh/v2/pkg/repository"
	"github.com/srz-zumix/go-gh-extension/pkg/gh"
	"github.com/srz-zumix/go-gh-extension/pkg/logger"
)

// NoPRBranch represents a branch that has no associated pull request.
type NoPRBranch struct {
	// Name is the branch name.
	Name string `json:"name"`
	// CommitSHA is the SHA of the branch tip commit.
	CommitSHA string `json:"commit_sha"`
	// Author is the GitHub login (or git author name as fallback) of the tip commit author.
	// Empty when the author could not be determined.
	Author string `json:"author,omitempty"`
	// AheadCount is the number of commits this branch is ahead of the default branch.
	// -1 when the comparison failed.
	AheadCount int `json:"ahead_count"`
	// UniqueBlobSize is the total byte size of blobs introduced by commits that exist
	// only in this branch and are not present in any other branch.
	// Blob SHAs are deduplicated across unique commits before summing sizes.
	// nil when the blob size could not be computed.
	UniqueBlobSize *uint64 `json:"unique_blob_size,omitempty"`
}

// branchCompareResult holds the ahead-count, set of commit SHAs that are ahead
// of the default branch, and the resolved author of the tip commit for a single branch.
type branchCompareResult struct {
	aheadBy   int
	shas      map[string]bool
	tipAuthor string // GitHub login or git author name of the tip commit; empty if unknown
}

// BranchesOptions controls scanning behavior for FindBranchesWithoutPR.
type BranchesOptions struct {
	// MaxBranches limits the number of no-PR branches for which blob size
	// computation is attempted. Zero or negative means unlimited. When the limit
	// is reached, remaining branches are still listed but with UniqueBlobSize nil.
	MaxBranches int
	// MaxUniqueCommits limits the number of unique commits fetched per branch for
	// blob size computation. Zero or negative means unlimited. When the limit is
	// exceeded, UniqueBlobSize is set to nil for that branch to avoid a partial sum.
	MaxUniqueCommits int
	// NoCache disables the per-commit blob cache. When false (default), commit diff
	// and blob size results are cached on disk and reused on subsequent runs.
	NoCache bool
	// ClearCache clears the commit blob cache before starting the run.
	ClearCache bool
	// NoBlobSize skips all blob size computation. When true, UniqueBlobSize is
	// always nil in results and no GetCommit or GetGitTreeRecursive API calls are
	// made, significantly reducing the number of API calls for large repositories.
	NoBlobSize bool
}

// FindBranchesWithoutPR returns all branches that have no associated pull request
// (open, closed, or merged), excluding the repository's default branch.
// For each such branch, AheadCount (commits ahead of the default branch) and
// UniqueBlobSize (total blob size from commits present only in this branch) are
// computed. Errors for individual branches are logged as warnings and do not abort
// the scan.
func FindBranchesWithoutPR(ctx context.Context, g *GitHubClient, repo repository.Repository, opts BranchesOptions) ([]*NoPRBranch, error) {
	var blobCache *commitBlobCache
	if opts.ClearCache {
		newCommitBlobCache(repo).clear()
	}
	if !opts.NoCache {
		blobCache = newCommitBlobCache(repo)
	}

	defaultBranch, err := getDefaultBranch(ctx, g, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get default branch: %w", err)
	}
	logger.Info("resolved default branch", "branch", defaultBranch)

	logger.Info("listing branches")
	branches, err := g.ListBranches(ctx, repo.Owner, repo.Name, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list branches: %w", err)
	}
	logger.Info("found branches", "count", len(branches))

	// Step 1: For each branch, check whether it has any associated pull requests
	// (open, closed, or merged) via a targeted GraphQL query. Branches with PRs are
	// excluded from results and their CompareCommits call is skipped, significantly
	// reducing API calls on repos with many PRs.
	// Note: uniqueness is computed relative to other no-PR branches only; commits
	// shared with PR-having branches are not accounted for.
	logger.Info("checking branches for associated pull requests and comparing against default", "default", defaultBranch)
	compareResults := make(map[string]*branchCompareResult, len(branches))
	// failedComparisons tracks no-PR branches whose CompareCommits call failed.
	// If any such branch failed, uniqueness cannot be guaranteed for other no-PR
	// branches and UniqueBlobSize is left nil for those branches.
	failedComparisons := make(map[string]bool)
	prBranches := make(map[string]bool)
	for _, b := range branches {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := b.GetName()
		if name == defaultBranch {
			compareResults[name] = &branchCompareResult{aheadBy: 0, shas: map[string]bool{}}
			continue
		}
		// Check for associated pull requests. When the branch has any PR (any state),
		// skip it and avoid the CompareCommits call entirely. If the lookup fails,
		// skip the branch conservatively so only branches confirmed to have no PR
		// are considered for reporting.
		assocPRs, assocErr := gh.GetAssociatedPullRequestsForRef(ctx, g, repo, name,
			gh.AssociatedPullRequestsOptionStateOpen(),
			gh.AssociatedPullRequestsOptionStateClosed(),
			gh.AssociatedPullRequestsOptionStateMerged(),
		)
		if assocErr != nil {
			logger.Warn("failed to check associated pull requests; skipping branch", "branch", name, "error", assocErr)
			continue
		} else if len(assocPRs) > 0 {
			logger.Debug("skipping branch with associated pull requests", "branch", name, "pr_count", len(assocPRs))
			prBranches[name] = true
			continue
		}
		comp, compErr := g.CompareCommits(ctx, repo.Owner, repo.Name, defaultBranch, name)
		if compErr != nil {
			logger.Warn("failed to compare branch against default", "branch", name, "error", compErr)
			failedComparisons[name] = true
			continue
		}
		shaSet := make(map[string]bool, len(comp.Commits))
		for _, c := range comp.Commits {
			if sha := c.GetSHA(); sha != "" {
				shaSet[sha] = true
			}
		}
		// Extract author from the last commit in the compare result if it matches the
		// branch tip SHA. When aheadBy > 250 the GitHub API returns only the oldest
		// commits so the tip may not be present; in that case tipAuthor stays empty
		// and is resolved via GetCommit in Step 3.
		var tipAuthor string
		if n := len(comp.Commits); n > 0 {
			tip := comp.Commits[n-1]
			if tip.GetSHA() == b.GetCommit().GetSHA() {
				if login := tip.GetAuthor().GetLogin(); login != "" {
					tipAuthor = login
				} else {
					tipAuthor = tip.GetCommit().GetAuthor().GetName()
				}
			}
		}
		compareResults[name] = &branchCompareResult{
			aheadBy:   comp.GetAheadBy(),
			shas:      shaSet,
			tipAuthor: tipAuthor,
		}
	}

	// Step 2: Build a global SHA→branch-count map so that SHAs shared across
	// multiple branches can be detected in O(1) per lookup rather than rebuilding
	// an otherSHAs union for every branch (which would be O(branches × total_commits)).
	shaCount := make(map[string]int)
	for _, cr := range compareResults {
		for sha := range cr.shas {
			shaCount[sha]++
		}
	}

	// Step 3: For each no-PR branch, find commits unique to that branch and
	// compute the total blob size introduced by those commits.
	var results []*NoPRBranch
	branchesProcessed := 0
	blobLimitWarned := false
	for _, b := range branches {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := b.GetName()
		commitSHA := b.GetCommit().GetSHA()

		if name == defaultBranch {
			continue
		}
		if prBranches[name] {
			continue
		}

		logger.Info("processing no-PR branch", "branch", name)

		cr := compareResults[name]
		aheadCount := -1
		if cr != nil {
			aheadCount = cr.aheadBy
		}

		// Resolve tip commit author. When the compare result already captured it
		// (aheadBy <= 250), use it directly. Otherwise make a single GetCommit call
		// for the tip so the author is always populated.
		tipAuthor := ""
		if cr != nil {
			tipAuthor = cr.tipAuthor
		}
		if tipAuthor == "" && commitSHA != "" {
			if c, err := g.GetCommit(ctx, repo.Owner, repo.Name, commitSHA); err == nil {
				if login := c.GetAuthor().GetLogin(); login != "" {
					tipAuthor = login
				} else {
					tipAuthor = c.GetCommit().GetAuthor().GetName()
				}
			} else {
				logger.Warn("failed to get tip commit for author", "sha", commitSHA, "branch", name, "error", err)
			}
		}

		// Commits present only in this branch (count==1 means no other successfully
		// compared branch contains it).
		var uniqueSHAs []string
		if cr != nil {
			for sha := range cr.shas {
				if shaCount[sha] == 1 {
					uniqueSHAs = append(uniqueSHAs, sha)
				}
			}
		}

		// anyOtherFailed is true when at least one branch other than this one had a
		// failed CompareCommits. In that case we cannot guarantee these commits are
		// truly unique (the failed branch might share them), so UniqueBlobSize is
		// left nil to avoid reporting an inflated value.
		anyOtherFailed := false
		for failedName := range failedComparisons {
			if failedName != name {
				anyOtherFailed = true
				break
			}
		}

		// Fetch per-commit trees and sum blob sizes introduced by unique commits.
		// Using the branch tip tree would miss blobs that were modified or deleted
		// after the commit, so each commit's own tree is used instead.
		// Blob SHAs are deduplicated across commits before summing.
		// If any commit's tree fetch fails, or if any other branch's comparison
		// failed (making uniqueness unverifiable), UniqueBlobSize is left nil.
		var uniqueBlobSize *uint64
		switch {
		case aheadCount < 0 || cr == nil || anyOtherFailed:
			// Cannot compute: compare failed or uniqueness unverifiable.
		case opts.NoBlobSize:
			// Blob size computation disabled by the caller.
		case opts.MaxBranches > 0 && branchesProcessed >= opts.MaxBranches:
			// Branch blob-size limit reached; leave UniqueBlobSize nil.
			if !blobLimitWarned {
				logger.Warn("branch blob-size limit reached, remaining branches will have unknown blob sizes",
					"limit", opts.MaxBranches)
				blobLimitWarned = true
			}
		case opts.MaxUniqueCommits > 0 && len(uniqueSHAs) > opts.MaxUniqueCommits:
			// Too many unique commits; skip to avoid a partial (misleading) sum.
			logger.Warn("unique commit limit exceeded, skipping blob size computation",
				"branch", name, "unique_commits", len(uniqueSHAs), "limit", opts.MaxUniqueCommits)
		default:
			branchesProcessed++
			seen := make(map[string]bool)
			var total uint64
			sizeKnown := true
			for _, sha := range uniqueSHAs {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				var info *commitBlobInfo
				if cached := blobCache.load(sha); cached != nil {
					logger.Debug("commit blob cache hit", "sha", sha)
					info = cached.toCommitBlobInfo()
				} else {
					commit, commitErr := g.GetCommit(ctx, repo.Owner, repo.Name, sha)
					if commitErr != nil {
						sizeKnown = false
						logger.Warn("failed to get commit diff", "sha", sha, "branch", name, "error", commitErr)
						continue
					}
					info = &commitBlobInfo{Commit: commit}
					if innerCommit := commit.GetCommit(); innerCommit != nil {
						if treeSHA := innerCommit.GetTree().GetSHA(); treeSHA != "" {
							tree, treeErr := g.GetGitTreeRecursive(ctx, repo.Owner, repo.Name, treeSHA)
							if treeErr != nil {
								logger.Warn("failed to fetch commit tree", "sha", sha, "branch", name, "error", treeErr)
								sizeKnown = false
							} else {
								info.BlobSizeMap = make(map[string]int, len(tree.Entries))
								for _, entry := range tree.Entries {
									if entry.GetType() == "blob" {
										info.BlobSizeMap[entry.GetSHA()] = entry.GetSize()
									}
								}
								blobCache.save(sha, info)
							}
						}
					}
				}
				for _, f := range info.Commit.Files {
					status := f.GetStatus()
					if status == "removed" || (status == "renamed" && f.GetChanges() == 0) {
						continue
					}
					blobSHA := f.GetSHA()
					if blobSHA == "" || seen[blobSHA] {
						continue
					}
					seen[blobSHA] = true
					if info.BlobSizeMap != nil {
						if sz, ok := info.BlobSizeMap[blobSHA]; ok {
							total += uint64(sz)
						}
					}
				}
			}
			if sizeKnown {
				uniqueBlobSize = &total
			}
		}

		results = append(results, &NoPRBranch{
			Name:           name,
			CommitSHA:      commitSHA,
			Author:         tipAuthor,
			AheadCount:     aheadCount,
			UniqueBlobSize: uniqueBlobSize,
		})
	}

	return results, nil
}

// SortNoPRBranchesBy sorts branches in-place by the given field name (case-insensitive).
// Supported fields: "branch", "ahead_count", "unique_size".
// desc=true reverses the order.
// Returns an error for unknown field names.
func SortNoPRBranchesBy(branches []*NoPRBranch, field string, desc bool) error {
	var less func(a, b *NoPRBranch) int
	reverse := desc
	switch strings.ToLower(field) {
	case "branch":
		less = func(a, b *NoPRBranch) int { return cmp.Compare(a.Name, b.Name) }
	case "ahead_count":
		less = func(a, b *NoPRBranch) int { return cmp.Compare(a.AheadCount, b.AheadCount) }
	case "unique_size":
		// Treat nil as unknown and always place it after known sizes.
		// This avoids mixing unknown values with actual zero-byte results.
		reverse = false
		less = func(a, b *NoPRBranch) int {
			switch {
			case a.UniqueBlobSize == nil && b.UniqueBlobSize == nil:
				return 0
			case a.UniqueBlobSize == nil:
				return 1
			case b.UniqueBlobSize == nil:
				return -1
			}

			if desc {
				return cmp.Compare(*b.UniqueBlobSize, *a.UniqueBlobSize)
			}
			return cmp.Compare(*a.UniqueBlobSize, *b.UniqueBlobSize)
		}
	default:
		return fmt.Errorf("unknown sort field %q: valid values are branch, ahead_count, unique_size", field)
	}
	slices.SortStableFunc(branches, func(a, b *NoPRBranch) int {
		if reverse {
			return less(b, a)
		}
		return less(a, b)
	})
	return nil
}
