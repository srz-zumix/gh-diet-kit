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

// FindBranchesWithoutPR returns all branches that have no associated pull request
// (open, closed, or merged), excluding the repository's default branch.
// For each such branch, AheadCount (commits ahead of the default branch) and
// UniqueBlobSize (total blob size from commits present only in this branch) are
// computed. Errors for individual branches are logged as warnings and do not abort
// the scan.
func FindBranchesWithoutPR(ctx context.Context, g *GitHubClient, repo repository.Repository) ([]*NoPRBranch, error) {
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
	for _, b := range branches {
		name := b.GetName()
		if name == defaultBranch {
			compareResults[name] = &branchCompareResult{aheadBy: 0, shas: map[string]bool{}}
			continue
		}
		comp, compErr := g.CompareCommits(ctx, repo.Owner, repo.Name, defaultBranch, name)
		if compErr != nil {
			logger.Warn("failed to compare branch against default", "branch", name, "error", compErr)
			compareResults[name] = &branchCompareResult{aheadBy: -1, shas: map[string]bool{}}
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

	// Step 2: For each no-PR branch, find commits unique to that branch and
	// compute the total blob size introduced by those commits.
	var results []*NoPRBranch
	for _, b := range branches {
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

		// Build a union of commit SHAs from every OTHER branch.
		otherSHAs := make(map[string]bool)
		for otherName, otherCR := range compareResults {
			if otherName == name {
				continue
			}
			for sha := range otherCR.shas {
				otherSHAs[sha] = true
			}
		}

		// Commits present only in this branch (not reachable from any other branch).
		var uniqueSHAs []string
		if cr != nil {
			for sha := range cr.shas {
				if !otherSHAs[sha] {
					uniqueSHAs = append(uniqueSHAs, sha)
				}
			}
		}

		// Fetch the branch tip tree once to get blob SHA→size mapping, then walk
		// the diff of each unique commit and sum the sizes of blobs it introduces.
		// Blob SHAs are deduplicated so a blob appearing in multiple commits is
		// counted only once.
		var uniqueBlobSize *uint64
		blobSizeMap, treeErr := fetchBranchBlobSizeMap(ctx, g, repo, name)
		if treeErr != nil {
			logger.Warn("failed to fetch branch tree", "branch", name, "error", treeErr)
		} else {
			seen := make(map[string]bool)
			var total uint64
			for _, sha := range uniqueSHAs {
				commit, commitErr := g.GetCommit(ctx, repo.Owner, repo.Name, sha)
				if commitErr != nil {
					logger.Warn("failed to get commit diff", "sha", sha, "branch", name, "error", commitErr)
					continue
				}
				for _, f := range commit.Files {
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
			}
			uniqueBlobSize = &total
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

// fetchBranchBlobSizeMap fetches the recursive git tree for the branch's tip commit
// and returns a map of blob SHA to size in bytes.
func fetchBranchBlobSizeMap(ctx context.Context, g *GitHubClient, repo repository.Repository, branch string) (map[string]int, error) {
	commitSHA, err := g.GetCommitSHA1(ctx, repo.Owner, repo.Name, branch, "")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve branch %q: %w", branch, err)
	}

	gitCommit, err := g.GetGitCommit(ctx, repo.Owner, repo.Name, commitSHA)
	if err != nil {
		return nil, fmt.Errorf("failed to get git commit %q: %w", commitSHA, err)
	}

	treeSHA := gitCommit.GetTree().GetSHA()
	gitTree, err := g.GetGitTreeRecursive(ctx, repo.Owner, repo.Name, treeSHA)
	if err != nil {
		return nil, fmt.Errorf("failed to traverse git tree for branch %q: %w", branch, err)
	}

	blobSizeMap := make(map[string]int, len(gitTree.Entries))
	for _, entry := range gitTree.Entries {
		if entry.GetType() == "blob" {
			blobSizeMap[entry.GetSHA()] = entry.GetSize()
		}
	}
	return blobSizeMap, nil
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
