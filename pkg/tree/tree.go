package tree

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/cli/go-gh/v2/pkg/repository"
	"github.com/srz-zumix/go-gh-extension/pkg/gh"
	"github.com/srz-zumix/go-gh-extension/pkg/logger"
)

// GitHubClient is a type alias for the shared GitHubClient from go-gh-extension.
type GitHubClient = gh.GitHubClient

// NewGitHubClientWithRepo creates a GitHub API client configured for the given repository.
func NewGitHubClientWithRepo(repo repository.Repository) (*GitHubClient, error) {
	return gh.NewGitHubClientWithRepo(repo)
}

// TreeDirectoryInfo holds analysis data for a single directory in the git tree.
type TreeDirectoryInfo struct {
	// Path is the directory path relative to the repository root.
	// The root directory is represented as ".".
	Path string `json:"path"`
	// Depth is the directory nesting level (0 = root).
	Depth int `json:"depth"`
	// EntryCount is the number of direct children (blobs + sub-trees) in this directory.
	EntryCount int `json:"entry_count"`
	// TotalFiles is the total number of blob entries reachable from this directory (recursive).
	TotalFiles int `json:"total_files"`
	// EstTreeSize is the estimated byte size of the git tree object for this directory.
	// Each entry contributes 28 bytes of fixed overhead (mode, space, null, 20-byte SHA)
	// plus the length of the entry's base name.
	EstTreeSize int `json:"est_tree_size"`
}

// TreeAnalysisResult holds the full result of an AnalyzeTreeStructure call.
type TreeAnalysisResult struct {
	// Dirs contains per-directory statistics, filtered by the threshold.
	Dirs []*TreeDirectoryInfo `json:"dirs"`
	// TotalDirs is the total number of directories in the tree (before filtering).
	TotalDirs int `json:"total_dirs"`
	// TotalFiles is the total number of blob entries in the tree.
	TotalFiles int `json:"total_files"`
	// MaxDepth is the maximum directory nesting depth found.
	MaxDepth int `json:"max_depth"`
	// TotalEstTreeSize is the sum of EstTreeSize across all directories (before filtering).
	TotalEstTreeSize int `json:"total_est_tree_size"`
}

// entryOverhead is the fixed byte cost per git tree entry:
// mode (up to 6 bytes) + space (1) + null (1) + SHA-1 (20) = 28 bytes.
// The variable part is the base name length.
const entryOverhead = 28

// AnalyzeTreeStructure fetches the recursive git tree at ref and computes
// per-directory statistics. Directories with fewer direct entries than
// threshold are omitted from Dirs but still counted in the aggregate totals.
// If ref is empty the repository's default branch is used.
func AnalyzeTreeStructure(ctx context.Context, g *GitHubClient, repo repository.Repository, ref string, threshold int) (*TreeAnalysisResult, error) {
	if ref == "" {
		ghRepo, err := g.GetRepository(ctx, repo.Owner, repo.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to get repository: %w", err)
		}
		ref = ghRepo.GetDefaultBranch()
	}

	logger.Info("resolving ref", "ref", ref)
	commitSHA, err := g.GetCommitSHA1(ctx, repo.Owner, repo.Name, ref, "")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve ref %q: %w", ref, err)
	}

	logger.Info("fetching git commit", "sha", commitSHA)
	gitCommit, err := g.GetGitCommit(ctx, repo.Owner, repo.Name, commitSHA)
	if err != nil {
		return nil, fmt.Errorf("failed to get git commit %q: %w", commitSHA, err)
	}

	treeSHA := gitCommit.GetTree().GetSHA()
	logger.Info("traversing repository tree", "tree_sha", treeSHA)
	gitTree, err := g.GetGitTreeRecursive(ctx, repo.Owner, repo.Name, treeSHA)
	if err != nil {
		return nil, fmt.Errorf("failed to traverse git tree: %w", err)
	}

	// dirChildren maps each directory path to the basenames of its direct children.
	// The root directory is keyed as "".
	dirChildren := make(map[string][]string)
	// blobChildren maps each directory path to the count of direct blob children.
	blobChildren := make(map[string]int)

	// Ensure root is always present.
	dirChildren[""] = nil

	// interrupted holds a context cancellation error if the loop is stopped early.
	var interrupted error
	for _, entry := range gitTree.Entries {
		if err := ctx.Err(); err != nil {
			interrupted = err
			break
		}
		entryPath := entry.GetPath()
		parentDir := path.Dir(entryPath)
		if parentDir == "." {
			parentDir = ""
		}
		base := path.Base(entryPath)
		dirChildren[parentDir] = append(dirChildren[parentDir], base)
		if entry.GetType() == "blob" {
			blobChildren[parentDir]++
		}
		// Ensure all ancestor directories are present in the map.
		for parentDir != "" {
			grandParent := path.Dir(parentDir)
			if grandParent == "." {
				grandParent = ""
			}
			if _, exists := dirChildren[grandParent]; !exists {
				dirChildren[grandParent] = nil
			}
			parentDir = grandParent
		}
	}

	// Compute TotalFiles for each directory by summing blob counts upward.
	// We process directories in reverse depth order (deepest first) to allow
	// propagation from children to parents.
	totalFilesPerDir := make(map[string]int)
	for dirPath := range dirChildren {
		totalFilesPerDir[dirPath] = blobChildren[dirPath]
	}
	// Sort keys by depth (deepest first).
	allDirPaths := make([]string, 0, len(dirChildren))
	for dirPath := range dirChildren {
		allDirPaths = append(allDirPaths, dirPath)
	}
	slices.SortFunc(allDirPaths, func(a, b string) int {
		da := depthOf(a)
		db := depthOf(b)
		if da != db {
			if da > db {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})
	for _, dirPath := range allDirPaths {
		if dirPath == "" {
			continue
		}
		parent := path.Dir(dirPath)
		if parent == "." {
			parent = ""
		}
		totalFilesPerDir[parent] += totalFilesPerDir[dirPath]
	}

	// Build result.
	result := &TreeAnalysisResult{
		TotalDirs:  len(dirChildren),
		TotalFiles: totalFilesPerDir[""],
	}

	maxDepth := 0
	for _, dirPath := range allDirPaths {
		d := depthOf(dirPath)
		if d > maxDepth {
			maxDepth = d
		}
	}
	result.MaxDepth = maxDepth

	for _, dirPath := range allDirPaths {
		children := dirChildren[dirPath]
		entryCount := len(children)

		estSize := 0
		for _, base := range children {
			estSize += entryOverhead + len(base)
		}
		result.TotalEstTreeSize += estSize

		displayPath := dirPath
		if displayPath == "" {
			displayPath = "."
		}

		info := &TreeDirectoryInfo{
			Path:        displayPath,
			Depth:       depthOf(dirPath),
			EntryCount:  entryCount,
			TotalFiles:  totalFilesPerDir[dirPath],
			EstTreeSize: estSize,
		}

		if entryCount >= threshold {
			result.Dirs = append(result.Dirs, info)
		}
	}

	logger.Info("tree analysis complete", "total_dirs", result.TotalDirs, "total_files", result.TotalFiles)
	return result, interrupted
}

// depthOf returns the nesting depth of a directory path.
// "" (root) has depth 0; "a" has depth 1; "a/b" has depth 2; etc.
func depthOf(p string) int {
	if p == "" {
		return 0
	}
	return strings.Count(p, "/") + 1
}

// SortDirInfoBy sorts dirs in-place by the given field.
// Accepted fields: "entry-count", "total-files", "est-size", "depth", "path".
// If desc is true the order is reversed.
func SortDirInfoBy(dirs []*TreeDirectoryInfo, field string, desc bool) error {
	switch strings.ToLower(field) {
	case "entry-count":
		slices.SortStableFunc(dirs, func(a, b *TreeDirectoryInfo) int {
			if a.EntryCount != b.EntryCount {
				if a.EntryCount < b.EntryCount {
					return -1
				}
				return 1
			}
			return strings.Compare(a.Path, b.Path)
		})
	case "total-files":
		slices.SortStableFunc(dirs, func(a, b *TreeDirectoryInfo) int {
			if a.TotalFiles != b.TotalFiles {
				if a.TotalFiles < b.TotalFiles {
					return -1
				}
				return 1
			}
			return strings.Compare(a.Path, b.Path)
		})
	case "est-size":
		slices.SortStableFunc(dirs, func(a, b *TreeDirectoryInfo) int {
			if a.EstTreeSize != b.EstTreeSize {
				if a.EstTreeSize < b.EstTreeSize {
					return -1
				}
				return 1
			}
			return strings.Compare(a.Path, b.Path)
		})
	case "depth":
		slices.SortStableFunc(dirs, func(a, b *TreeDirectoryInfo) int {
			if a.Depth != b.Depth {
				if a.Depth < b.Depth {
					return -1
				}
				return 1
			}
			return strings.Compare(a.Path, b.Path)
		})
	case "path":
		slices.SortStableFunc(dirs, func(a, b *TreeDirectoryInfo) int {
			return strings.Compare(a.Path, b.Path)
		})
	default:
		return fmt.Errorf("unknown sort field %q: must be one of entry-count, total-files, est-size, depth, path", field)
	}
	if desc {
		slices.Reverse(dirs)
	}
	return nil
}
