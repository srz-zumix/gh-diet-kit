package dangling

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cli/go-gh/v2/pkg/repository"
	"github.com/google/go-github/v84/github"
	"github.com/srz-zumix/go-gh-extension/pkg/logger"
)

// prCacheEntry is the on-disk structure for a cached PR result.
// Chain and force-push candidates are stored separately so that
// processChainCandidates can be applied on resume.
type prCacheEntry struct {
	// ChainCommits holds the PR commit chain candidates (oldest first).
	ChainCommits []cachedCommit `json:"chain_commits"`
	// ForcePushCommits holds commits dropped by force-push events.
	ForcePushCommits []cachedCommit `json:"force_push_commits"`
}

// cachedCommit holds the SHA and parent SHAs of a commit.
type cachedCommit struct {
	SHA     string   `json:"sha"`
	Parents []string `json:"parents"`
}

// prCache manages the per-PR cache directory.
type prCache struct {
	dir string // path to the cache directory for this repo+opts combination
}

// newPRCache returns a prCache rooted at cacheBaseDir/<host>/<owner>/<repo>/pr-cache.
// Candidate commits are cached per-repo regardless of search options; reachability
// checks are always re-run on resume.
// Returns nil when cacheBaseDir cannot be resolved (non-fatal; callers should skip caching).
func newPRCache(repo repository.Repository) *prCache {
	base, err := cacheBaseDir()
	if err != nil {
		logger.Warn("pr cache disabled: cannot resolve cache dir", "error", err)
		return nil
	}
	dir := filepath.Join(base, repo.Host, repo.Owner, repo.Name, "pr-cache")
	return &prCache{dir: dir}
}

// cachePath returns the file path for the cache entry of a single PR.
// Key: <prNumber>-<headSHA>.json
func (c *prCache) cachePath(prNumber int, headSHA string) string {
	name := fmt.Sprintf("%d-%s.json", prNumber, headSHA)
	return filepath.Join(c.dir, name)
}

// load reads a cached entry for the given PR. Returns nil when no valid cache
// entry exists.
func (c *prCache) load(prNumber int, headSHA string) *prCacheEntry {
	if c == nil {
		return nil
	}
	p := c.cachePath(prNumber, headSHA)
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var entry prCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		logger.Debug("pr cache: ignoring corrupt entry", "path", p, "error", err)
		return nil
	}
	return &entry
}

// save writes the given chain and force-push candidate lists to the cache for the given PR.
func (c *prCache) save(prNumber int, headSHA string, chain []*github.RepositoryCommit, forcePushed []*github.RepositoryCommit) {
	if c == nil {
		return
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		logger.Warn("pr cache: failed to create cache dir", "dir", c.dir, "error", err)
		return
	}
	tocached := func(commits []*github.RepositoryCommit) []cachedCommit {
		result := make([]cachedCommit, 0, len(commits))
		for _, commit := range commits {
			parents := make([]string, 0, len(commit.Parents))
			for _, p := range commit.Parents {
				parents = append(parents, p.GetSHA())
			}
			result = append(result, cachedCommit{SHA: commit.GetSHA(), Parents: parents})
		}
		return result
	}
	entry := prCacheEntry{
		ChainCommits:     tocached(chain),
		ForcePushCommits: tocached(forcePushed),
	}
	data, err := json.Marshal(entry)
	if err != nil {
		logger.Warn("pr cache: failed to marshal entry", "pr", prNumber, "error", err)
		return
	}
	p := c.cachePath(prNumber, headSHA)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		logger.Warn("pr cache: failed to write entry", "path", p, "error", err)
	}
}

// reconstruct converts a []cachedCommit back into []*github.RepositoryCommit.
func reconstruct(cached []cachedCommit) []*github.RepositoryCommit {
	commits := make([]*github.RepositoryCommit, 0, len(cached))
	for _, cc := range cached {
		sha := cc.SHA
		parents := make([]*github.Commit, 0, len(cc.Parents))
		for _, p := range cc.Parents {
			pSHA := p
			parents = append(parents, &github.Commit{SHA: &pSHA})
		}
		commits = append(commits, &github.RepositoryCommit{
			SHA:     &sha,
			Parents: parents,
		})
	}
	return commits
}

// prHeadSHA returns the PR head SHA, or empty string when unavailable.
func prHeadSHA(pr *github.PullRequest) string {
	if pr.GetHead() == nil {
		return ""
	}
	return pr.GetHead().GetSHA()
}
