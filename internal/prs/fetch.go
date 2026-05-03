package prs

import (
	"context"

	"github.com/cli/go-gh/v2/pkg/repository"
	"github.com/google/go-github/v84/github"
	"github.com/srz-zumix/go-gh-extension/pkg/gh"
)

// FetchPRsForDangling returns the PRs to inspect for dangling detection.
// If numbers is non-empty, those specific PRs are fetched by number.
// Otherwise, all closed PRs up to maxPRs are listed (use -1 for unlimited).
func FetchPRsForDangling(ctx context.Context, g *gh.GitHubClient, repo repository.Repository, numbers []int, maxPRs int) ([]*github.PullRequest, error) {
	if len(numbers) > 0 {
		return gh.GetPRsByNumbers(ctx, g, repo, numbers)
	}
	return gh.ListClosedPRs(ctx, g, repo, maxPRs)
}
