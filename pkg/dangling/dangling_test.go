package dangling

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/repository"
	"github.com/google/go-github/v90/github"
)

func TestUnreachableSetConcurrentAccess(t *testing.T) {
	s := newUnreachableSet()
	const workers = 16
	const shas = 32

	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range shas {
				sha := fmt.Sprintf("sha-%d", (w+i)%shas)
				s.Has(sha)
				s.Add(sha)
			}
		}()
	}
	wg.Wait()

	for i := range shas {
		sha := fmt.Sprintf("sha-%d", i)
		if !s.Has(sha) {
			t.Errorf("expected %s to be recorded as unreachable", sha)
		}
	}
	if s.Has("sha-missing") {
		t.Error("unexpected hit for a SHA that was never added")
	}
}

// testCacheRepo redirects the cache base directory to a temporary directory and
// returns the repository the test operates on.
func testCacheRepo(t *testing.T) repository.Repository {
	t.Helper()
	dir := t.TempDir()
	// os.UserCacheDir reads XDG_CACHE_HOME on Linux and HOME elsewhere.
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("HOME", dir)
	return repository.Repository{Host: "github.com", Owner: "octocat", Name: "hello-world"}
}

func testCommit(sha string, parents ...string) *github.RepositoryCommit {
	ps := make([]*github.Commit, 0, len(parents))
	for _, p := range parents {
		ps = append(ps, &github.Commit{SHA: github.Ptr(p)})
	}
	return &github.RepositoryCommit{
		SHA:     github.Ptr(sha),
		Parents: ps,
		Commit:  &github.Commit{Message: github.Ptr("message of " + sha)},
	}
}

// seedPRCache builds merged PRs whose chain and force-push candidates are fully
// cached, so iterateDanglingCommits runs without issuing any API call.
// Chains deliberately share commits across PRs so that concurrent workers hit
// the same unreachableSet entries.
func seedPRCache(t *testing.T, repo repository.Repository, count int) []*github.PullRequest {
	t.Helper()
	cache := newPRCache(repo)
	if cache == nil {
		t.Fatal("failed to create pr cache")
	}
	prs := make([]*github.PullRequest, 0, count)
	for i := range count {
		headSHA := fmt.Sprintf("head-%02d", i)
		chain := []*github.RepositoryCommit{
			testCommit(fmt.Sprintf("chain-%02d-0", i)),
			testCommit(fmt.Sprintf("chain-%02d-1", i), fmt.Sprintf("chain-%02d-0", i)),
			testCommit(fmt.Sprintf("shared-%d", i%4), fmt.Sprintf("chain-%02d-1", i)),
		}
		forcePushed := []*github.RepositoryCommit{
			testCommit(fmt.Sprintf("dropped-%02d", i), fmt.Sprintf("shared-%d", i%4)),
		}
		cache.save(i+1, headSHA, chain, forcePushed, true, true)
		prs = append(prs, &github.PullRequest{
			Number:   github.Ptr(i + 1),
			Title:    github.Ptr(fmt.Sprintf("pull request %d", i+1)),
			HTMLURL:  github.Ptr(fmt.Sprintf("https://github.com/octocat/hello-world/pull/%d", i+1)),
			MergedAt: &github.Timestamp{Time: time.Unix(int64(i), 0)},
			Head:     &github.PullRequestBranch{SHA: github.Ptr(headSHA)},
		})
	}
	return prs
}

// collectVisits runs the driver at the given PR concurrency and returns one
// "<pr number>:<sha>,<sha>..." entry per visited PR, in visit order.
func collectVisits(t *testing.T, repo repository.Repository, prs []*github.PullRequest, concurrency int) []string {
	t.Helper()
	var visited []string
	opts := DanglingOptions{PRConcurrency: concurrency}
	// A nil client is safe here: every PR is served from the cache and the
	// default reachability check makes no API or git call.
	err := iterateDanglingCommits(context.Background(), nil, repo, prs, opts, func(pr *github.PullRequest, commits []*github.RepositoryCommit) error {
		shas := make([]string, 0, len(commits))
		for _, c := range commits {
			shas = append(shas, c.GetSHA())
		}
		visited = append(visited, fmt.Sprintf("%d:%s", pr.GetNumber(), strings.Join(shas, ",")))
		return nil
	})
	if err != nil {
		t.Fatalf("iterateDanglingCommits(concurrency=%d) returned error: %v", concurrency, err)
	}
	return visited
}

func TestIterateDanglingCommitsIsDeterministic(t *testing.T) {
	repo := testCacheRepo(t)
	prs := seedPRCache(t, repo, 24)

	sequential := collectVisits(t, repo, prs, 1)
	if len(sequential) != len(prs) {
		t.Fatalf("visited %d PRs, want %d", len(sequential), len(prs))
	}
	for i, got := range sequential {
		want := fmt.Sprintf("%d:chain-%02d-0,chain-%02d-1,shared-%d,dropped-%02d", i+1, i, i, i%4, i)
		if got != want {
			t.Fatalf("visit %d = %q, want %q", i, got, want)
		}
	}

	parallel := collectVisits(t, repo, prs, 8)
	if len(parallel) != len(sequential) {
		t.Fatalf("parallel run visited %d PRs, want %d", len(parallel), len(sequential))
	}
	for i := range sequential {
		if parallel[i] != sequential[i] {
			t.Errorf("visit %d: parallel = %q, sequential = %q", i, parallel[i], sequential[i])
		}
	}
}
