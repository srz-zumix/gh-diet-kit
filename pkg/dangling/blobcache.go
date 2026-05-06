package dangling

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/cli/go-gh/v2/pkg/repository"
	"github.com/google/go-github/v84/github"
	"github.com/srz-zumix/go-gh-extension/pkg/logger"
)

// cachedBlobFile holds the commit file fields needed to reconstruct blob output.
type cachedBlobFile struct {
	BlobSHA string `json:"blob_sha"`
	Path    string `json:"path"`
	Status  string `json:"status"`
	Changes int    `json:"changes"`
}

// cachedCommitBlob is the on-disk structure for a cached commit blob result.
// BlobSizes is nil when the tree fetch failed during the original run; an empty
// (non-nil) map means the tree was fetched but contained no blob entries.
type cachedCommitBlob struct {
	Message   string           `json:"message"`
	Files     []cachedBlobFile `json:"files"`
	BlobSizes map[string]int   `json:"blob_sizes,omitempty"`
}

// commitBlobCache caches per-commit blob info (message, file list, blob sizes)
// under the repo cache directory so that repeated runs avoid re-fetching the
// commit diff and full tree for each dangling commit.
type commitBlobCache struct {
	dir string
}

// newCommitBlobCache returns a commitBlobCache rooted at
// cacheBaseDir/<host>/<owner>/<repo>/commit-blobs.
// Returns nil when the cache base directory cannot be resolved.
func newCommitBlobCache(repo repository.Repository) *commitBlobCache {
	base, err := cacheBaseDir()
	if err != nil {
		logger.Warn("commit blob cache disabled: cannot resolve cache dir", "error", err)
		return nil
	}
	dir := filepath.Join(base, repo.Host, repo.Owner, repo.Name, "commit-blobs")
	return &commitBlobCache{dir: dir}
}

// clear removes the entire commit blob cache directory and all its entries.
// Errors are logged as warnings and do not propagate.
func (c *commitBlobCache) clear() {
	if c == nil {
		return
	}
	if err := os.RemoveAll(c.dir); err != nil {
		logger.Warn("commit blob cache: failed to clear cache dir", "dir", c.dir, "error", err)
	}
}

// blobCachePath returns the file path for the given commit SHA.
// Entries are sharded into 256 subdirectories by the first two hex characters
// of the SHA to avoid large flat directories on repos with many commits.
func (c *commitBlobCache) blobCachePath(sha string) string {
	if len(sha) < 2 {
		return filepath.Join(c.dir, sha+".json")
	}
	return filepath.Join(c.dir, sha[:2], sha+".json")
}

// load reads a cached commit blob entry. Returns nil on miss or corrupt entry.
func (c *commitBlobCache) load(sha string) *cachedCommitBlob {
	if c == nil {
		return nil
	}
	p := c.blobCachePath(sha)
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var entry cachedCommitBlob
	if err := json.Unmarshal(data, &entry); err != nil {
		logger.Debug("commit blob cache: ignoring corrupt entry", "path", p, "error", err)
		return nil
	}
	return &entry
}

// save writes commit blob info to the cache for the given SHA.
// A nil info is silently ignored.
func (c *commitBlobCache) save(sha string, info *commitBlobInfo) {
	if c == nil || info == nil {
		return
	}
	p := c.blobCachePath(sha)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		logger.Warn("commit blob cache: failed to create dir", "dir", filepath.Dir(p), "error", err)
		return
	}
	cached := &cachedCommitBlob{
		BlobSizes: info.BlobSizeMap,
	}
	if inner := info.Commit.GetCommit(); inner != nil {
		cached.Message = inner.GetMessage()
	}
	for _, f := range info.Commit.Files {
		cached.Files = append(cached.Files, cachedBlobFile{
			BlobSHA: f.GetSHA(),
			Path:    f.GetFilename(),
			Status:  f.GetStatus(),
			Changes: f.GetChanges(),
		})
	}
	data, err := json.Marshal(cached)
	if err != nil {
		logger.Warn("commit blob cache: failed to marshal entry", "sha", sha, "error", err)
		return
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		logger.Warn("commit blob cache: failed to write entry", "path", p, "error", err)
	}
}

// toCommitBlobInfo reconstructs a *commitBlobInfo from the cached entry.
// The returned value contains synthetic github SDK objects populated with only
// the fields used by FindDanglingCommits and FindDanglingBlobs.
func (cached *cachedCommitBlob) toCommitBlobInfo() *commitBlobInfo {
	message := cached.Message
	files := make([]*github.CommitFile, 0, len(cached.Files))
	for _, f := range cached.Files {
		sha := f.BlobSHA
		path := f.Path
		status := f.Status
		changes := f.Changes
		files = append(files, &github.CommitFile{
			SHA:      &sha,
			Filename: &path,
			Status:   &status,
			Changes:  &changes,
		})
	}
	commit := &github.RepositoryCommit{
		Commit: &github.Commit{Message: &message},
		Files:  files,
	}
	return &commitBlobInfo{
		Commit:      commit,
		BlobSizeMap: cached.BlobSizes,
	}
}
