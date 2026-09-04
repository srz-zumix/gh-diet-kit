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
// the host is GitHub Enterprise Server or when an asset selected for this run
// (respecting opts.PRNumbers) references a file the API cannot accept.
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
		apiOnly, err := allSelectedAssetsAPIUploadable(ctx, meta, inputDir, opts.PRNumbers)
		if err != nil {
			apiUploader.Close()
			return nil, "", err
		}
		if apiOnly {
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

// allSelectedAssetsAPIUploadable reports whether the REST upload API can handle
// every asset URL this run will actually upload, so UploadMethodAuto can skip
// launching a browser. It mirrors Restore's upload-source selection so the
// decision matches what the upload loop does:
//
//   - Only URLs referenced by the PRs selected via prNumbers can be restored
//     (an empty prNumbers means all PRs), so unsupported assets in unselected
//     PRs never force a browser launch.
//   - For each such URL the file is taken from the first metadata entry across
//     ALL PRs that has a usable local file (exactly as ensureUploaded does),
//     because the upload source is keyed by content, not by the selected PR.
//   - A URL with no usable local file is treated as acceptable, since its
//     upload is skipped rather than routed through the browser.
//
// It checks ctx while scanning so a large dump honors cancellation promptly.
// The returned bool is meaningful only when the returned error is nil.
func allSelectedAssetsAPIUploadable(ctx context.Context, meta *DumpMetadata, inputDir string, prNumbers []int) (bool, error) {
	prFilter := make(map[int]bool, len(prNumbers))
	for _, n := range prNumbers {
		prFilter[n] = true
	}

	// Index every entry by URL across all PRs so candidate selection matches the
	// cross-PR fallback the upload loop performs.
	urlToAssets := make(map[string][]*PRAsset)
	for _, a := range meta.Assets {
		if err := ctx.Err(); err != nil {
			return false, fmt.Errorf("asset API eligibility preflight canceled: %w", err)
		}
		urlToAssets[a.AssetURL] = append(urlToAssets[a.AssetURL], a)
	}

	checked := make(map[string]bool)
	for _, a := range meta.Assets {
		if err := ctx.Err(); err != nil {
			return false, fmt.Errorf("asset API eligibility preflight canceled: %w", err)
		}
		if len(prFilter) > 0 && !prFilter[a.PRNumber] {
			continue
		}
		if checked[a.AssetURL] {
			continue
		}
		checked[a.AssetURL] = true

		sel, localPath, ok, err := firstUsableAssetCandidate(ctx, urlToAssets[a.AssetURL], inputDir)
		if err != nil {
			return false, fmt.Errorf("asset API eligibility preflight canceled: %w", err)
		}
		if !ok {
			continue
		}
		fi, statErr := os.Stat(localPath)
		if statErr != nil {
			continue
		}
		if _, ok := gh.UserAttachmentSupported(sel.Filename, fi.Size()); !ok {
			return false, nil
		}
	}
	return true, nil
}
