package dangling

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cli/go-gh/v2/pkg/repository"
)

// cacheBaseDir returns the XDG cache base directory for gh-diet-kit.
// On non-Linux platforms os.UserCacheDir() follows the OS convention.
func cacheBaseDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve cache dir: %w", err)
	}
	return filepath.Join(base, "gh-diet-kit"), nil
}

// CacheGitDir returns the path of the bare clone cache directory for the given repo.
// The directory name encodes the clone mode to prevent a blobless cache from being
// used when a full clone is required.
// Format: <cacheBase>/<host>/<owner>/<repo>.git         (full clone)
//
//	<cacheBase>/<host>/<owner>/<repo>.blobless.git (blobless clone)
func CacheGitDir(repo repository.Repository, blobless bool) (string, error) {
	base, err := cacheBaseDir()
	if err != nil {
		return "", err
	}
	suffix := ".git"
	if blobless {
		suffix = ".blobless.git"
	}
	return filepath.Join(base, repo.Host, repo.Owner, repo.Name+suffix), nil
}

// ghCredArgs returns git global -c flags that delegate credential lookup to
// gh's built-in credential helper, overriding any existing helper configuration.
// Git invokes helpers prefixed with "!" through a shell, so this works even
// when git is called via exec.Command without a shell.
func ghCredArgs() []string {
	return []string{
		"-c", "credential.helper=",
		"-c", "credential.helper=!gh auth git-credential",
	}
}

// SetupLocalGitCache ensures a bare clone of the repo exists in the XDG cache and is up to date.
// When blobless is true, --filter=blob:none is used (suitable for commit reachability only).
// When blobless is false, a full clone is performed (required for blob content checks).
// Returns the path to the bare clone directory.
func SetupLocalGitCache(ctx context.Context, repo repository.Repository, blobless bool) (string, error) {
	dir, err := CacheGitDir(repo, blobless)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		// Clone bare into the cache directory
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return "", fmt.Errorf("create cache parent dir: %w", err)
		}
		cloneURL := fmt.Sprintf("https://%s/%s/%s.git", repo.Host, repo.Owner, repo.Name)
		args := append(ghCredArgs(), "clone", "--bare")
		if blobless {
			args = append(args, "--filter=blob:none")
		}
		args = append(args, cloneURL, dir)
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("git clone --bare %s: %w", cloneURL, err)
		}
	} else {
		// Fetch to update existing clone
		args := append(ghCredArgs(), "--git-dir="+dir, "fetch", "--all", "--tags", "--force")
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("git fetch in cache %s: %w", dir, err)
		}
	}

	return dir, nil
}
