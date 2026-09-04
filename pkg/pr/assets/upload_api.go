package assets

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/cli/go-gh/v2/pkg/auth"
	"github.com/cli/go-gh/v2/pkg/repository"
	"github.com/srz-zumix/go-gh-extension/pkg/gh"
	"github.com/srz-zumix/go-gh-extension/pkg/logger"
)

// apiUploaderTimeout bounds a single asset upload, generous enough for a
// 100 MiB video over a slow connection.
const apiUploaderTimeout = 5 * time.Minute

// APIUploader uploads asset files directly through GitHub's REST
// user-attachments upload endpoint, without any browser automation.
type APIUploader struct {
	g            *GitHubClient
	host         string
	repositoryID int64
}

// NewAPIUploader builds an uploader that posts asset bytes directly to
// GitHub's REST upload endpoint using g's authenticated transport. It rejects
// GitHub Enterprise Server hosts, which do not serve this endpoint.
func NewAPIUploader(g *GitHubClient, host string, repositoryID int64) (*APIUploader, error) {
	if err := gh.CheckUserAttachmentUploadSupported(host, repositoryID); err != nil {
		return nil, err
	}
	return &APIUploader{g: g, host: host, repositoryID: repositoryID}, nil
}

var _ assetUploader = (*APIUploader)(nil)

// Upload streams localPath to GitHub's REST upload endpoint and returns the
// new asset URL, retrying with backoff while the endpoint reports a rate
// limit. It returns gh.ErrUserAttachmentUnsupported (wrapped) without making a
// request when the file's extension or size is not accepted by the endpoint.
func (u *APIUploader) Upload(ctx context.Context, localPath, filename string) (string, error) {
	fi, err := os.Stat(localPath)
	if err != nil {
		return "", fmt.Errorf("stat file %q: %w", localPath, err)
	}
	contentType, ok := gh.UserAttachmentSupported(filename, fi.Size())
	if !ok {
		return "", fmt.Errorf("%q: %w", filename, gh.ErrUserAttachmentUnsupported)
	}

	upload := gh.UserAttachmentUpload{
		Host:         u.host,
		RepositoryID: u.repositoryID,
		Name:         filepath.Base(filename),
		ContentType:  contentType,
		Size:         fi.Size(),
		Open:         func() (io.ReadCloser, error) { return os.Open(localPath) },
		Timeout:      apiUploaderTimeout,
	}

	var lastErr error
	for attempt := 1; attempt <= maxPolicyAttempts; attempt++ {
		assetURL, err := gh.UploadUserAttachment(ctx, u.g, upload)
		if err == nil {
			return assetURL, nil
		}
		lastErr = err
		var rl *gh.UserAttachmentRateLimitError
		if !errors.As(err, &rl) {
			return "", err
		}
		if attempt == maxPolicyAttempts {
			break
		}
		wait := retryAfterDelayFromHeader(rl.Header, policyRateLimitBackoff(attempt))
		logger.Warn("asset upload rate-limited, backing off",
			"file", filename, "attempt", attempt, "wait", wait.String())
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(wait):
		}
	}
	return "", lastErr
}

// Recover is a no-op: the API uploader holds no browser session to rebuild.
func (u *APIUploader) Recover(_ context.Context, _, _ string) error { return nil }

// Close is a no-op: the API uploader holds no resources to release.
func (u *APIUploader) Close() {}

// hybridUploader uploads through the REST API first and falls back to browser
// automation only for files the API rejects as unsupported (never for a
// transient failure, which is reported to the caller as-is).
type hybridUploader struct {
	api     *APIUploader
	browser assetUploader
}

var _ assetUploader = (*hybridUploader)(nil)

func (u *hybridUploader) Upload(ctx context.Context, localPath, filename string) (string, error) {
	assetURL, err := u.api.Upload(ctx, localPath, filename)
	if err == nil || !errors.Is(err, gh.ErrUserAttachmentUnsupported) {
		return assetURL, err
	}
	return u.browser.Upload(ctx, localPath, filename)
}

func (u *hybridUploader) Recover(ctx context.Context, owner, repo string) error {
	return u.browser.Recover(ctx, owner, repo)
}

func (u *hybridUploader) Close() {
	u.api.Close()
	u.browser.Close()
}

// newRestoreUploader builds the uploader Restore uses for this run, honoring
// opts.UploadMethod. For UploadMethodAuto it prefers the REST API and only
// pays the cost of a browser launch (and a possible interactive login) when
// the host is GitHub Enterprise Server or when metadata references a file the
// API cannot accept.
//
// The second return value is the resolved browser state file path, set only
// when a browser uploader was actually constructed (empty for API-only runs).
// Restore uses it to scope --clear-cache cleanup to sessions this run created.
func newRestoreUploader(ctx context.Context, g *GitHubClient, repo repository.Repository, owner, repoName string, opts RestoreOptions, meta *DumpMetadata, inputDir string) (assetUploader, string, error) {
	method := opts.UploadMethod
	if method == "" {
		method = UploadMethodAuto
	}
	isEnterprise := auth.IsEnterprise(repo.Host)

	// browserStateFile is populated by newBrowserUploader when (and only when) a
	// browser session is actually resolved and launched.
	var browserStateFile string
	newBrowserUploader := func() (assetUploader, error) {
		stateFile, err := ResolveBrowserStateFile(opts.StateFile)
		if err != nil {
			return nil, err
		}
		browserStateFile = stateFile
		u, err := NewPlaywrightUploader(stateFile, repo.Host, opts.Headed)
		if err != nil {
			return nil, fmt.Errorf("initialize browser uploader: %w", err)
		}
		if err := u.Init(ctx, stateFile, owner, repoName, opts.Headed); err != nil {
			u.Close()
			return nil, fmt.Errorf("initialize browser session: %w", err)
		}
		return u, nil
	}

	switch method {
	case UploadMethodBrowser:
		u, err := newBrowserUploader()
		return u, browserStateFile, err

	case UploadMethodAPI:
		if isEnterprise {
			return nil, "", fmt.Errorf("--upload-method api is not supported on GitHub Enterprise Server (%s)", repo.Host)
		}
		u, err := newAPIUploaderForRestore(ctx, g, repo)
		return u, "", err

	case UploadMethodAuto:
		if isEnterprise {
			u, err := newBrowserUploader()
			return u, browserStateFile, err
		}
		apiUploader, err := newAPIUploaderForRestore(ctx, g, repo)
		if err != nil {
			return nil, "", err
		}
		if allAssetsAPIUploadable(meta, inputDir) {
			return apiUploader, "", nil
		}
		browserUploader, err := newBrowserUploader()
		if err != nil {
			apiUploader.Close()
			return nil, "", err
		}
		return &hybridUploader{api: apiUploader, browser: browserUploader}, browserStateFile, nil

	default:
		return nil, "", fmt.Errorf("unknown upload method %q", method)
	}
}

// newAPIUploaderForRestore resolves the destination repository's numeric id
// and builds an APIUploader for it.
func newAPIUploaderForRestore(ctx context.Context, g *GitHubClient, repo repository.Repository) (*APIUploader, error) {
	r, err := gh.GetRepository(ctx, g, repo)
	if err != nil {
		return nil, fmt.Errorf("resolve repository id for asset upload: %w", err)
	}
	return NewAPIUploader(g, repo.Host, r.GetID())
}

// allAssetsAPIUploadable reports whether every asset in meta is one the REST
// upload API would accept, so UploadMethodAuto can skip the browser launch
// entirely. An asset whose local file cannot be resolved or stat'd is treated
// as acceptable here (its own upload will simply be skipped later); this
// function only judges whether known files rule out the API-only path.
func allAssetsAPIUploadable(meta *DumpMetadata, inputDir string) bool {
	for _, a := range meta.Assets {
		if _, ok := gh.UserAttachmentSupported(a.Filename, -1); !ok {
			return false
		}
		if a.LocalFile == "" {
			continue
		}
		localPath, ok := resolveLocalPath(inputDir, a.LocalFile)
		if !ok {
			continue
		}
		fi, err := os.Stat(localPath)
		if err != nil {
			continue
		}
		if _, ok := gh.UserAttachmentSupported(a.Filename, fi.Size()); !ok {
			return false
		}
	}
	return true
}
