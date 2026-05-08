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
	// ChainCollected records whether chain candidate collection was enabled
	// (i.e., not disabled by opts for this PR type) when this entry was written.
	// A false value means the entry cannot be trusted for chain results if the
	// current run needs them.
	ChainCollected bool `json:"chain_collected"`
	// ForcePushCollected records whether force-push candidate collection was
	// attempted when this entry was written. A false value means the entry
	// cannot be trusted for force-push results if the current run needs them.
	ForcePushCollected bool `json:"force_push_collected"`
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

// clear removes the entire PR cache directory and all its entries.
// Errors are logged as warnings and do not propagate.
func (c *prCache) clear() {
	if c == nil {
		return
	}
	if err := os.RemoveAll(c.dir); err != nil {
		logger.Warn("pr cache: failed to clear cache dir", "dir", c.dir, "error", err)
	}
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
	// Backward compat: entries written before ChainCollected/ForcePushCollected
	// were introduced have both flags as false zero values.
	// For each scope, if the flag is false but the list is non-empty, the entry
	// was written by old code with valid data, so treat it as collected.
	// If the flag is false and the list is empty, the collection may have failed;
	// leave the flag false so the caller re-collects that scope.
	if !entry.ChainCollected && len(entry.ChainCommits) > 0 {
		entry.ChainCollected = true
	}
	if !entry.ForcePushCollected && len(entry.ForcePushCommits) > 0 {
		entry.ForcePushCollected = true
	}
	return &entry
}

// save writes the given chain and force-push candidate lists to the cache for the given PR.
// chainCollected and fpCollected indicate whether each collection was actually
// attempted (not disabled by options) so that a future load can detect when the
// cached entry does not cover the collection scope of the current run.
func (c *prCache) save(prNumber int, headSHA string, chain []*github.RepositoryCommit, forcePushed []*github.RepositoryCommit, chainCollected, fpCollected bool) {
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
		ChainCommits:       tocached(chain),
		ForcePushCommits:   tocached(forcePushed),
		ChainCollected:     chainCollected,
		ForcePushCollected: fpCollected,
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
