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
	// AheadCount is the number of commits this branch is ahead of the default branch.
	// -1 when the comparison failed.
	AheadCount int `json:"ahead_count"`
	// UniqueBlobSize is the total byte size of blobs introduced by commits that exist
	// only in this branch and are not present in any other branch.
	// Blob SHAs are deduplicated across unique commits before summing sizes.
	// nil when the blob size could not be computed.
	UniqueBlobSize *uint64 `json:"unique_blob_size,omitempty"`
}

// branchCompareResult holds the ahead-count and set of commit SHAs that are ahead
// of the default branch for a single branch.
type branchCompareResult struct {
	aheadBy int
	shas    map[string]bool
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

	// List all PRs (any state) once to build a set of head ref names that have PRs.
	logger.Info("listing pull requests (all states)")
	prs, err := gh.ListPullRequests(ctx, g, repo, gh.ListPullRequestsOptionStateAll())
	if err != nil {
		return nil, fmt.Errorf("failed to list pull requests: %w", err)
	}
	logger.Info("found pull requests", "count", len(prs))

	targetRepoFullName := fmt.Sprintf("%s/%s", repo.Owner, repo.Name)
	prHeadRefs := make(map[string]bool, len(prs))
	for _, pr := range prs {
		head := pr.GetHead()
		headRepo := head.GetRepo()
		if headRepo == nil || headRepo.GetFullName() != targetRepoFullName {
			continue
		}
		if ref := head.GetRef(); ref != "" {
			prHeadRefs[ref] = true
		}
	}

	// Step 1: For each branch, compare against the default branch to collect the
	// set of commit SHAs that are ahead of (not reachable from) the default branch.
	// This is used to determine which commits are unique to a given branch.
	logger.Info("comparing all branches against default branch", "default", defaultBranch)
	compareResults := make(map[string]*branchCompareResult, len(branches))
	// failedComparisons tracks branches whose CompareCommits call failed.
	// If any branch other than the one being analyzed failed, uniqueness cannot be
	// guaranteed and UniqueBlobSize is left nil for that branch.
	failedComparisons := make(map[string]bool)
	for _, b := range branches {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := b.GetName()
		if name == defaultBranch {
			compareResults[name] = &branchCompareResult{aheadBy: 0, shas: map[string]bool{}}
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
		compareResults[name] = &branchCompareResult{
			aheadBy: comp.GetAheadBy(),
			shas:    shaSet,
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
		if prHeadRefs[name] {
			continue
		}

		logger.Info("processing no-PR branch", "branch", name)

		cr := compareResults[name]
		aheadCount := -1
		if cr != nil {
			aheadCount = cr.aheadBy
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
