package assets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/repository"
	"github.com/google/go-github/v88/github"
	playwright "github.com/playwright-community/playwright-go"
	"github.com/srz-zumix/go-gh-extension/pkg/gh"
	"github.com/srz-zumix/go-gh-extension/pkg/logger"
)

// uploadPolicy is the JSON response from GitHub's /upload/policies/assets endpoint.
type uploadPolicy struct {
	UploadURL string            `json:"upload_url"`
	Form      map[string]string `json:"form"`
	Asset     struct {
		Href string `json:"href"`
	} `json:"asset"`
	AssetUploadURL               string `json:"asset_upload_url"`
	UploadAuthenticityToken      string `json:"upload_authenticity_token"`
	AssetUploadAuthenticityToken string `json:"asset_upload_authenticity_token"`
}

// PlaywrightUploader manages a Playwright browser session for uploading assets to GitHub.
type PlaywrightUploader struct {
	pw      *playwright.Playwright
	browser playwright.Browser
	bctx    playwright.BrowserContext
	page    playwright.Page
	host    string
	scheme  string
	// headless and stateFile are retained so the session can be rebuilt by
	// Recover after the renderer/browser crashes mid-restore.
	headless  bool
	stateFile string
}

// uploadUserAgent is a realistic desktop Chrome User-Agent so GitHub serves the
// full interactive page (with the markdown editor) rather than a bot-detection
// fallback.
const uploadUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

// uploaderInputID is the id of a hidden <input type="file"> that this tool
// injects into the page. Files are streamed into it with Playwright's
// set_input_files, which makes Chromium read the bytes directly from disk. This
// avoids marshalling the whole file as base64 through the JS heap (which crashes
// the renderer on large videos and breaks every subsequent upload).
const uploaderInputID = "gh-diet-kit-file-input"

// uploadPolicyTimeout bounds how long we wait for GitHub's /upload/policies/assets
// response after dispatching the drop. The policy response only returns presigned
// S3 metadata (the bytes go to S3 separately), so it is fast regardless of file
// size; the timeout mainly guards against a hung page.
const uploadPolicyTimeout = 60 * time.Second

// NewPlaywrightUploader creates a new PlaywrightUploader and launches a browser.
// If stateFile does not exist, the browser is launched in headed mode so the user
// can log in interactively. Otherwise it runs headlessly using the saved session.
// Pass forceHeaded=true to run in headed mode even when a session file exists.
func NewPlaywrightUploader(stateFile, host string, forceHeaded bool) (*PlaywrightUploader, error) {
	if err := playwright.Install(&playwright.RunOptions{
		Browsers: []string{"chromium"},
	}); err != nil {
		return nil, fmt.Errorf("install playwright chromium: %w", err)
	}

	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("start playwright: %w", err)
	}

	_, stateErr := os.Stat(stateFile)
	if stateErr != nil && !os.IsNotExist(stateErr) {
		pw.Stop() //nolint:errcheck
		return nil, fmt.Errorf("check browser session file %q: %w", stateFile, stateErr)
	}
	sessionExists := stateErr == nil
	headless := sessionExists && !forceHeaded

	if !sessionExists {
		fmt.Fprintln(os.Stderr, "No saved browser session found. A browser window will open for interactive GitHub login.")
	} else if forceHeaded {
		fmt.Fprintln(os.Stderr, "Using saved browser session in headed mode.")
	}

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(headless),
	})
	if err != nil {
		pw.Stop() //nolint:errcheck
		return nil, fmt.Errorf("launch chromium: %w", err)
	}

	// Use a realistic User-Agent so GitHub serves the full interactive page
	// rather than a bot-detection fallback without the markdown editor.
	ctxOpts := playwright.BrowserNewContextOptions{
		UserAgent: playwright.String(uploadUserAgent),
	}
	if sessionExists {
		ctxOpts.StorageStatePath = playwright.String(stateFile)
	}

	bctx, err := browser.NewContext(ctxOpts)
	if err != nil {
		browser.Close() //nolint:errcheck
		pw.Stop()       //nolint:errcheck
		return nil, fmt.Errorf("create browser context: %w", err)
	}

	page, err := bctx.NewPage()
	if err != nil {
		bctx.Close()    //nolint:errcheck
		browser.Close() //nolint:errcheck
		pw.Stop()       //nolint:errcheck
		return nil, fmt.Errorf("create page: %w", err)
	}

	return &PlaywrightUploader{
		pw:        pw,
		browser:   browser,
		bctx:      bctx,
		page:      page,
		host:      host,
		scheme:    "https",
		headless:  headless,
		stateFile: stateFile,
	}, nil
}

// extractMeta reads the content attribute of the first meta tag with the given name.
// Returns an empty string if the tag is absent.
func (u *PlaywrightUploader) extractMeta(name string) (string, error) {
	v, err := u.page.Evaluate(
		fmt.Sprintf(`document.querySelector('meta[name="%s"]')?.getAttribute('content')`, name),
	)
	if err != nil {
		return "", fmt.Errorf("evaluate meta[name=%q]: %w", name, err)
	}
	if v == nil {
		return "", nil
	}
	s, _ := v.(string)
	return s, nil
}

// isLoggedIn returns true when the current page carries a user-login meta tag,
// which GitHub includes for authenticated sessions regardless of whether the
// page is a public or private repository.
func (u *PlaywrightUploader) isLoggedIn() (bool, error) {
	login, err := u.extractMeta("user-login")
	if err != nil {
		return false, err
	}
	return login != "", nil
}

// Init navigates to the repository's new-issue form to verify authentication.
// If not authenticated, a headed browser is opened so the user can log in
// interactively (up to 5 minutes). The session is saved to stateFile for
// headless reuse on subsequent runs. Pass headed=true to allow re-login even
// when a (possibly expired) session file already exists.
//
// The issues/new page is used because it:
//   - requires authentication (GitHub redirects to /login if not signed in)
//   - is never served from a CDN cache
func (u *PlaywrightUploader) Init(ctx context.Context, stateFile, owner, repo string, headed bool) error {
	// issues/new?template= forces the blank-issue editor directly, bypassing
	// any issue template chooser. The page is authenticated and non-CDN-cached,
	// so it always carries a user-login meta tag for authenticated users.
	issuesNewURL := fmt.Sprintf("%s://%s/%s/%s/issues/new?template=", u.scheme, u.host, owner, repo)

	if _, err := u.page.Goto(issuesNewURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateLoad,
		Timeout:   playwright.Float(60000),
	}); err != nil {
		return fmt.Errorf("navigate to %s: %w", issuesNewURL, err)
	}

	loggedIn, err := u.isLoggedIn()
	if err != nil {
		return err
	}

	if !loggedIn {
		// Not authenticated – start interactive login flow.
		// If a session file exists but the user did not request headed mode, the
		// session has expired and cannot be renewed headlessly; tell the user how
		// to recover. When headed=true, fall through to the interactive login flow
		// regardless of whether a session file is present.
		if !headed {
			if _, stateErr := os.Stat(stateFile); stateErr == nil {
				return fmt.Errorf("browser session expired; re-run with --headed to log in interactively")
			}
		}

		// Navigate to the login page with return_to pointing at issues/new so
		// WaitForURL can detect when the repo is reached after login.
		returnTo := "/" + owner + "/" + repo + "/issues/new"
		loginURL := fmt.Sprintf("%s://%s/login?return_to=%s", u.scheme, u.host, url.QueryEscape(returnTo))
		if _, err := u.page.Goto(loginURL, playwright.PageGotoOptions{
			Timeout: playwright.Float(30000),
		}); err != nil {
			return fmt.Errorf("navigate to login page: %w", err)
		}
		fmt.Fprintln(os.Stderr, "Please log in to GitHub in the browser window.")

		// Poll until the user is logged in. This is more robust than WaitForURL
		// because it works regardless of intermediate pages GitHub may show
		// (2FA, SSO, device verification, SAML, etc.).
		deadline := time.Now().Add(5 * time.Minute)
		for {
			if time.Now().After(deadline) {
				return fmt.Errorf("login timed out (5 minutes)")
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
			ok, pollErr := u.isLoggedIn()
			if pollErr != nil {
				// Page may still be navigating; ignore transient errors.
				continue
			}
			if ok {
				break
			}
		}

		// Save session for future headless runs.
		if err := os.MkdirAll(filepath.Dir(stateFile), 0o700); err != nil {
			return fmt.Errorf("create state directory: %w", err)
		}
		if _, err := u.bctx.StorageState(stateFile); err != nil {
			return fmt.Errorf("save browser session to %q: %w", stateFile, err)
		}
		// Lock down the state file: it contains auth cookies/tokens that must
		// not be readable by other users on the system.
		if err := os.Chmod(stateFile, 0o600); err != nil {
			return fmt.Errorf("secure browser session file %q: %w", stateFile, err)
		}
		fmt.Fprintln(os.Stderr, "Browser session saved. Subsequent runs will use headless mode.")

		// Explicitly navigate to issues/new?template= after login to ensure we
		// are on the blank-issue editor page before extracting metadata.
		if _, err := u.page.Goto(issuesNewURL, playwright.PageGotoOptions{
			WaitUntil: playwright.WaitUntilStateLoad,
			Timeout:   playwright.Float(30000),
		}); err != nil {
			return fmt.Errorf("navigate to issues/new after login: %w", err)
		}
	}

	return nil
}

// Upload uploads the file at localPath to GitHub and returns the new CDN URL.
//
// Upload pipeline:
//  1. Playwright streams the file from disk into a hidden <input type="file">
//     (set_input_files) and dispatches a drag-drop built from that input, so the
//     browser sends POST /upload/policies/assets with proper session cookies and
//     CSRF. ExpectResponse captures the response. Streaming from disk avoids
//     loading the whole file into the JS heap, which crashes the renderer on
//     large videos.
//  2. Go net/http POST to S3 presigned URL (no auth required), streaming the
//     file from disk so large videos never sit fully in memory.
//  3. Browser fetch() PUT to /upload/assets/{id} to confirm the upload.
func (u *PlaywrightUploader) Upload(ctx context.Context, localPath, filename string) (string, error) {
	fileInfo, err := os.Stat(localPath)
	if err != nil {
		return "", fmt.Errorf("stat file %q: %w", localPath, err)
	}

	// Step 1: let the browser make the authenticated policy request, retrying
	// when GitHub responds with a secondary rate limit (429/403). Asset uploads
	// count against the content-creation limit, whose hourly bucket can require
	// a long wait, so honor any Retry-After / x-ratelimit-reset header to
	// auto-resume and otherwise back off with an increasing delay.
	var policy uploadPolicy
	var lastPolicyErr error
	for attempt := 1; attempt <= maxPolicyAttempts; attempt++ {
		policyResp, reqErr := u.dispatchUploadPolicy(localPath, filename)
		if reqErr != nil {
			return "", fmt.Errorf("upload policy request for %q: %w", filename, reqErr)
		}

		status := policyResp.Status()
		if status == http.StatusOK || status == http.StatusCreated {
			policyBody, bodyErr := policyResp.Body()
			if bodyErr != nil {
				return "", fmt.Errorf("read upload policy body for %q: %w", filename, bodyErr)
			}
			if err := json.Unmarshal(policyBody, &policy); err != nil {
				return "", fmt.Errorf("parse upload policy for %q: %w", filename, err)
			}
			if policy.Asset.Href == "" {
				return "", fmt.Errorf("upload policy missing asset href for %q", filename)
			}
			lastPolicyErr = nil
			break
		}

		policyBody, _ := policyResp.Body()
		policyMsg := string(policyBody)
		if len(policyMsg) > 4096 {
			policyMsg = policyMsg[:4096] + "...(truncated)"
		}
		lastPolicyErr = fmt.Errorf("upload policy returned %d for %q: %s", status, filename, policyMsg)
		if !isRateLimitStatus(status) {
			return "", lastPolicyErr
		}
		if attempt == maxPolicyAttempts {
			break
		}
		wait := retryAfterDelay(policyResp, policyRateLimitBackoff(attempt))
		logger.Warn("upload policy rate-limited, backing off",
			"file", filename, "status", status, "attempt", attempt, "wait", wait.String())
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(wait):
		}
	}
	if lastPolicyErr != nil {
		return "", lastPolicyErr
	}

	httpClient := &http.Client{Timeout: 5 * time.Minute}

	// Step 2: upload the file to S3 using the presigned multipart form.
	// S3 presigned POSTs do not require session cookies or CSRF tokens.
	//
	// The multipart body is streamed from disk: only the small framing prefix
	// (form fields + file part header) and suffix (closing boundary) are held in
	// memory, while the file bytes are read straight from disk on each attempt.
	// This keeps large videos out of RSS, matching the disk-streaming the
	// Playwright step already uses.
	var framing bytes.Buffer
	mw := multipart.NewWriter(&framing)
	for k, v := range policy.Form {
		if err := mw.WriteField(k, v); err != nil {
			return "", fmt.Errorf("write S3 form field %q: %w", k, err)
		}
	}
	// CreateFormFile writes the file part header (boundary + Content-Disposition)
	// into framing but no content; the file bytes are streamed in afterwards.
	if _, err := mw.CreateFormFile("file", filename); err != nil {
		return "", fmt.Errorf("create S3 file field: %w", err)
	}
	// prefix is everything up to and including the file part header; suffix is the
	// closing boundary that multipart.Writer.Close would emit. The file bytes go
	// between them, streamed from disk.
	prefix := append([]byte(nil), framing.Bytes()...)
	suffix := []byte(fmt.Sprintf("\r\n--%s--\r\n", mw.Boundary()))
	s3ContentType := mw.FormDataContentType()
	// Set an explicit Content-Length so net/http sends a known length instead of
	// chunked transfer encoding, which S3 presigned POSTs reject.
	s3ContentLength := int64(len(prefix)) + fileInfo.Size() + int64(len(suffix))

	// newS3Body reopens the file and rebuilds the streaming body for each attempt,
	// since the previous attempt's reader is consumed once sent.
	newS3Body := func() (io.ReadCloser, error) {
		f, err := os.Open(localPath)
		if err != nil {
			return nil, fmt.Errorf("open file %q for S3 upload: %w", localPath, err)
		}
		body := io.MultiReader(bytes.NewReader(prefix), f, bytes.NewReader(suffix))
		return multiReadCloser{Reader: body, Closer: f}, nil
	}

	// S3 presigned uploads occasionally fail with transient errors such as
	// 503 SlowDown when many uploads happen in a short burst. Retry with
	// exponential backoff (and jitter) so the whole restore does not abort on a
	// single throttled request.
	if err := uploadToS3WithRetry(ctx, httpClient, policy.UploadURL, s3ContentType, newS3Body, s3ContentLength, filename); err != nil {
		return "", err
	}

	// Step 3: confirm the upload using the browser so session cookies are
	// included automatically. The upload-specific asset_upload_authenticity_token
	// is passed as the body; it is scoped to this single upload, not the page CSRF.
	confirmToken := policy.AssetUploadAuthenticityToken
	if confirmToken == "" {
		confirmToken = policy.UploadAuthenticityToken
	}
	confirmBodyStr := url.Values{"authenticity_token": {confirmToken}}.Encode()
	if err := u.confirmUpload(ctx, policy.AssetUploadURL, confirmBodyStr, filename); err != nil {
		return "", err
	}

	return policy.Asset.Href, nil
}

// confirmUpload finalizes an uploaded asset via the browser session so cookies
// are included automatically. Confirmation is a content-creating request, so it
// retries on GitHub's secondary rate limit (429/403), honoring Retry-After /
// x-ratelimit-reset to auto-resume at the time the server allows.
func (u *PlaywrightUploader) confirmUpload(ctx context.Context, assetUploadURL, confirmBody, filename string) error {
	var lastErr error
	for attempt := 1; attempt <= maxPolicyAttempts; attempt++ {
		confirmResult, err := u.page.Evaluate(`
			([confirmURL, body]) => fetch(confirmURL, {
				method: 'PUT',
				headers: {
					'Content-Type': 'application/x-www-form-urlencoded; charset=UTF-8',
					'Accept': 'application/json',
					'X-Requested-With': 'XMLHttpRequest',
				},
				body: body,
				credentials: 'include',
			}).then(async r => ({
				status: r.status,
				body: await r.text(),
				retryAfter: r.headers.get('retry-after') || '',
				rateLimitReset: r.headers.get('x-ratelimit-reset') || '',
			}))
		`, []interface{}{assetUploadURL, confirmBody})
		if err != nil {
			return fmt.Errorf("upload confirm request for %q: %w", filename, err)
		}
		resultMap, ok := confirmResult.(map[string]interface{})
		if !ok {
			return fmt.Errorf("unexpected confirm response type for %q: %T", filename, confirmResult)
		}
		status, serr := confirmResponseStatus(resultMap)
		if serr != nil {
			return fmt.Errorf("%w for %q", serr, filename)
		}
		if status == http.StatusOK || status == http.StatusCreated {
			return nil
		}

		lastErr = fmt.Errorf("upload confirmation returned %d for %q: %s", status, filename, resultMap["body"])
		if !isRateLimitStatus(status) {
			return lastErr
		}
		if attempt == maxPolicyAttempts {
			break
		}
		retryAfter, _ := resultMap["retryAfter"].(string)
		reset, _ := resultMap["rateLimitReset"].(string)
		wait := rateLimitWait(retryAfter, reset, policyRateLimitBackoff(attempt))
		logger.Warn("upload confirm rate-limited, backing off",
			"file", filename, "status", status, "attempt", attempt, "wait", wait.String())
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return lastErr
}

// confirmResponseStatus extracts the HTTP status code from the confirm-step
// fetch result, accounting for the numeric types Playwright may return.
func confirmResponseStatus(resultMap map[string]interface{}) (int, error) {
	switch v := resultMap["status"].(type) {
	case float64:
		return int(v), nil
	case int:
		return v, nil
	case int64:
		return int(v), nil
	default:
		return 0, fmt.Errorf("unexpected confirm status type %T", resultMap["status"])
	}
}

// maxS3UploadAttempts is the number of times an S3 presigned upload is tried
// before giving up (1 initial attempt + retries).
const maxS3UploadAttempts = 6

// Upload policy (GitHub secondary rate limit) retry settings.
const (
	// maxPolicyAttempts is the number of times a content-creating request (the
	// upload policy and the upload confirmation) is tried before giving up when
	// GitHub returns a secondary rate limit.
	maxPolicyAttempts = 5
	// policyRateLimitBaseDelay is the initial blind backoff for secondary rate
	// limits when the server does not tell us how long to wait. GitHub's "you
	// have exceeded a secondary rate limit" page asks callers to wait at least a
	// minute, so start high.
	policyRateLimitBaseDelay = 60 * time.Second
	// policyRateLimitMaxDelay caps the blind secondary rate limit backoff used
	// when no Retry-After / x-ratelimit-reset header is present.
	policyRateLimitMaxDelay = 5 * time.Minute
	// maxRateLimitWait caps a server-directed wait (Retry-After or
	// x-ratelimit-reset). Asset uploads count against GitHub's content-creation
	// secondary rate limit, whose hourly bucket can require waiting up to ~1
	// hour, so allow a long wait while guarding against an absurd value.
	maxRateLimitWait = 65 * time.Minute
)

// ensureFileInput injects a hidden <input type="file"> (id uploaderInputID) into
// the page if one is not already present. Playwright streams files into this
// input with set_input_files, which makes Chromium read the bytes directly from
// disk rather than receiving them as a base64 string in the JS heap. The input
// lives in document.body (outside GitHub's file-attachment element) so loading a
// file into it does not itself trigger an upload; the drop is dispatched
// explicitly in dispatchUploadPolicy.
func (u *PlaywrightUploader) ensureFileInput() error {
	_, err := u.page.Evaluate(`
		(id) => {
			if (document.getElementById(id)) return;
			const input = document.createElement('input');
			input.type = 'file';
			input.id = id;
			input.style.display = 'none';
			document.body.appendChild(input);
		}
	`, uploaderInputID)
	if err != nil {
		return fmt.Errorf("inject file input: %w", err)
	}
	return nil
}

// dispatchUploadPolicy streams the file at localPath into the injected file
// input and fires the browser drag-drop that triggers the
// /upload/policies/assets request, returning the captured response.
//
// The file is loaded with set_input_files (read from disk by Chromium), and the
// drop event reuses the resulting disk-backed File object. This keeps large
// files (videos) out of the JS heap, avoiding the renderer crash ("target
// closed") that the previous base64 approach caused.
//
// ExpectResponse registers the listener BEFORE the drop fires so there is no
// race. The browser carries the correct session cookies and CSRF token,
// sidestepping any server-side origin/CSRF validation that rejects Go HTTP.
func (u *PlaywrightUploader) dispatchUploadPolicy(localPath, filename string) (playwright.Response, error) {
	if err := u.ensureFileInput(); err != nil {
		return nil, err
	}
	if err := u.page.Locator("#" + uploaderInputID).SetInputFiles(localPath); err != nil {
		return nil, fmt.Errorf("load file %q from %q into uploader input: %w", filename, localPath, err)
	}
	return u.page.ExpectResponse(
		func(rawURL string) bool {
			return strings.Contains(rawURL, "/upload/policies/assets")
		},
		func() error {
			_, evalErr := u.page.Evaluate(`
				(id) => {
					const input = document.getElementById(id);
					if (!input || !input.files || input.files.length === 0) {
						throw new Error('upload input has no files');
					}
					const dt = new DataTransfer();
					for (const f of input.files) dt.items.add(f);
					// Target the Primer React textarea; fall back broadly.
					const el = document.querySelector('.prc-Textarea-TextArea-snlco') ||
						document.querySelector('file-attachment') ||
						document.querySelector('textarea');
					if (!el) {
						throw new Error('upload drop target not found');
					}
					el.dispatchEvent(new DragEvent('dragenter', {dataTransfer: dt, bubbles: true}));
					el.dispatchEvent(new DragEvent('dragover',  {dataTransfer: dt, bubbles: true, cancelable: true}));
					el.dispatchEvent(new DragEvent('drop',      {dataTransfer: dt, bubbles: true, cancelable: true}));
				}
			`, uploaderInputID)
			return evalErr
		},
		playwright.PageExpectResponseOptions{Timeout: playwright.Float(float64(uploadPolicyTimeout.Milliseconds()))},
	)
}

// isRateLimitStatus reports whether an HTTP status indicates a GitHub rate limit
// (primary or secondary).
func isRateLimitStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusForbidden
}

// retryAfterDelay returns the wait duration before retrying a rate-limited
// Playwright request. It prefers the server-directed Retry-After header, then
// the x-ratelimit-reset header, and otherwise falls back to the provided blind
// backoff. See rateLimitWait for details.
func retryAfterDelay(resp playwright.Response, fallback time.Duration) time.Duration {
	if resp == nil {
		return fallback
	}
	retryAfter, _ := resp.HeaderValue("retry-after")
	reset, _ := resp.HeaderValue("x-ratelimit-reset")
	return rateLimitWait(retryAfter, reset, fallback)
}

// rateLimitWait determines how long to wait before retrying a request that hit
// GitHub's secondary rate limit. It honors the server's guidance to auto-resume
// at the right time: the Retry-After header (in seconds) takes priority, then
// the x-ratelimit-reset header (a UTC epoch second), and finally the provided
// blind backoff when neither header is present. Server-directed waits are capped
// at maxRateLimitWait so the hourly content-creation limit (~1 hour) is honored
// without trusting an absurd value.
func rateLimitWait(retryAfter, rateLimitReset string, fallback time.Duration) time.Duration {
	if secs, ok := parsePositiveSeconds(retryAfter); ok {
		return capRateLimitWait(time.Duration(secs) * time.Second)
	}
	if epoch, ok := parsePositiveSeconds(rateLimitReset); ok {
		// Add a small buffer so we resume just after the window resets.
		if d := time.Until(time.Unix(int64(epoch), 0)) + time.Second; d > 0 {
			return capRateLimitWait(d)
		}
	}
	return fallback
}

// capRateLimitWait clamps a server-directed wait to maxRateLimitWait.
func capRateLimitWait(d time.Duration) time.Duration {
	if d > maxRateLimitWait {
		return maxRateLimitWait
	}
	return d
}

// parsePositiveSeconds parses a header value as a positive integer number of
// seconds (or epoch seconds), returning false when it is empty or non-positive.
func parsePositiveSeconds(v string) (int, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// policyRateLimitBackoff returns the exponential backoff (with jitter) for the
// given (1-based) secondary rate limit retry attempt, capped at the maximum.
func policyRateLimitBackoff(attempt int) time.Duration {
	backoff := min(policyRateLimitBaseDelay<<(attempt-1), policyRateLimitMaxDelay)
	jitter := time.Duration(rand.Int63n(int64(time.Second)))
	return backoff + jitter
}

// multiReadCloser streams an S3 multipart body from disk: Reader concatenates the
// in-memory framing with the on-disk file, and Closer closes the underlying file.
type multiReadCloser struct {
	io.Reader
	io.Closer
}

// uploadToS3WithRetry POSTs the multipart form body to the S3 presigned URL,
// retrying transient failures (network errors and retryable HTTP status codes
// such as 503 SlowDown) with exponential backoff and jitter. newBody rebuilds
// the streaming body (reopening the file) for each attempt, and contentLength is
// the fixed total body size used to set Content-Length on every request.
func uploadToS3WithRetry(ctx context.Context, httpClient *http.Client, uploadURL, contentType string, newBody func() (io.ReadCloser, error), contentLength int64, filename string) error {
	var lastErr error
	for attempt := 1; attempt <= maxS3UploadAttempts; attempt++ {
		body, err := newBody()
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, body)
		if err != nil {
			body.Close() //nolint:errcheck
			return fmt.Errorf("create S3 request: %w", err)
		}
		// httpClient.Do closes req.Body, so the file is closed after each attempt.
		req.ContentLength = contentLength
		req.Header.Set("Content-Type", contentType)

		resp, err := httpClient.Do(req)
		if err != nil {
			// Network/transport errors are treated as transient and retried.
			lastErr = fmt.Errorf("S3 upload for %q: %w", filename, err)
		} else {
			status := resp.StatusCode
			if status == http.StatusOK || status == http.StatusCreated || status == http.StatusNoContent {
				drainAndCloseBody(resp.Body)
				return nil
			}
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			drainAndCloseBody(resp.Body)
			lastErr = fmt.Errorf("S3 upload returned %d for %q: %s", status, filename, string(respBody))
			if !isRetryableS3Status(status) {
				return lastErr
			}
		}

		if attempt < maxS3UploadAttempts {
			backoff := s3RetryBackoff(attempt)
			logger.Warn("S3 upload failed, retrying", "file", filename, "attempt", attempt, "backoff", backoff.String(), "err", lastErr)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
	}
	return lastErr
}

// drainAndCloseBody drains up to 1 MiB of any remaining response body and then
// closes it. Draining lets the underlying keep-alive connection be reused for
// subsequent uploads/retries; the cap avoids spending unbounded time or memory
// on an unexpectedly large body.
func drainAndCloseBody(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 1<<20))
	body.Close() //nolint:errcheck
}

// isRetryableS3Status reports whether an S3 HTTP status code is worth retrying.
func isRetryableS3Status(status int) bool {
	switch status {
	case http.StatusRequestTimeout, // 408
		http.StatusTooManyRequests,     // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503 (SlowDown)
		http.StatusGatewayTimeout:      // 504
		return true
	default:
		return false
	}
}

// s3RetryBackoff returns the wait duration before the given (1-based) retry
// attempt using exponential backoff capped at 60s, plus random jitter to avoid
// synchronized retries across concurrent uploads.
func s3RetryBackoff(attempt int) time.Duration {
	const base = 2 * time.Second
	const maxBackoff = 60 * time.Second
	backoff := min(base<<(attempt-1), maxBackoff)
	jitter := time.Duration(rand.Int63n(int64(time.Second)))
	return backoff + jitter
}

// Close releases all Playwright resources.
func (u *PlaywrightUploader) Close() {
	if u.bctx != nil {
		u.bctx.Close() //nolint:errcheck
	}
	if u.browser != nil {
		u.browser.Close() //nolint:errcheck
	}
	if u.pw != nil {
		u.pw.Stop() //nolint:errcheck
	}
}

// isPageClosedErr reports whether err indicates the Playwright page, browser
// context, or browser has gone away (typically a renderer crash from running
// out of memory). Such an error poisons every subsequent operation on the same
// page, so the caller should rebuild the session with Recover before retrying.
func isPageClosedErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "target closed") ||
		strings.Contains(msg, "target page, context or browser has been closed") ||
		strings.Contains(msg, "browser has been closed") ||
		strings.Contains(msg, "page has been closed") ||
		strings.Contains(msg, "crashed")
}

// Recover rebuilds the browser session after a crash so the restore can
// continue instead of failing every remaining asset. It recreates the page and
// browser context (relaunching the browser if it is no longer connected),
// restores the saved cookies from the state file, and re-navigates to the
// new-issue editor. The saved session is reused, so no interactive login is
// required.
func (u *PlaywrightUploader) Recover(ctx context.Context, owner, repo string) error {
	if u.page != nil {
		u.page.Close() //nolint:errcheck
	}
	if u.bctx != nil {
		u.bctx.Close() //nolint:errcheck
	}

	if u.browser == nil || !u.browser.IsConnected() {
		browser, err := u.pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
			Headless: playwright.Bool(u.headless),
		})
		if err != nil {
			return fmt.Errorf("relaunch chromium: %w", err)
		}
		u.browser = browser
	}

	ctxOpts := playwright.BrowserNewContextOptions{
		UserAgent: playwright.String(uploadUserAgent),
	}
	if _, statErr := os.Stat(u.stateFile); statErr == nil {
		ctxOpts.StorageStatePath = playwright.String(u.stateFile)
	}
	bctx, err := u.browser.NewContext(ctxOpts)
	if err != nil {
		return fmt.Errorf("recreate browser context: %w", err)
	}
	u.bctx = bctx

	page, err := bctx.NewPage()
	if err != nil {
		return fmt.Errorf("recreate page: %w", err)
	}
	u.page = page

	// Re-navigate so the markdown editor is present again. headed=false because
	// the saved session is reused; recovery never starts an interactive login.
	if err := u.Init(ctx, u.stateFile, owner, repo, false); err != nil {
		return fmt.Errorf("reinitialize browser session: %w", err)
	}
	return nil
}

// RestoreOptions holds configuration for the restore operation.
type RestoreOptions struct {
	// PRNumbers limits the restore to specific PR numbers. Empty means all PRs.
	PRNumbers []int
	// DryRun logs intended operations without uploading or updating any content.
	DryRun bool
	// StateFile is the path to the Playwright browser state file used for session
	// persistence between runs.
	StateFile string
	// Headed forces the browser to run in headed (visible) mode even when a
	// saved session file exists. Useful for debugging.
	Headed bool
	// ClearCache deletes the saved browser session file after the restore
	// completes successfully.
	ClearCache bool
	// UploadDelay is the minimum delay inserted before each asset upload to
	// pace requests and avoid tripping GitHub's secondary rate limit. Zero uses
	// DefaultUploadDelay.
	UploadDelay time.Duration
}

// DefaultUploadDelay paces asset uploads to stay under GitHub's secondary rate
// limit for content creation (max ~80 content-generating requests per minute,
// i.e. roughly one every 0.75s). Uploads are otherwise fast enough to trip it.
const DefaultUploadDelay = 1 * time.Second

// RestoredMetadataFilename is the fixed name of the metadata file written to the
// input directory after a restore. It lists the assets still needing work so a
// re-run can resume without re-searching already-migrated comments.
const RestoredMetadataFilename = "metadata.restored.json"

// CheckWriteAccess verifies that the authenticated user has write (push) access
// to the repository, which is required to edit PR bodies and comments during a
// restore. It returns an error when the repository cannot be read or the user
// lacks sufficient permission.
func CheckWriteAccess(ctx context.Context, g *GitHubClient, repo repository.Repository) error {
	r, err := gh.GetRepository(ctx, g, repo)
	if err != nil {
		return fmt.Errorf("check access to repository %q: %w", repo.Owner+"/"+repo.Name, err)
	}
	perms := r.GetPermissions()
	if perms.GetAdmin() || perms.GetMaintain() || perms.GetPush() {
		return nil
	}
	return fmt.Errorf("write access to repository %q is required to restore PR assets", repo.Owner+"/"+repo.Name)
}

// Restore reads metadata from metaPath, uploads each local asset file to the
// destination repository via Playwright browser automation, and updates PR
// bodies, issue comments, and review comments by replacing old source asset
// URLs with the newly uploaded destination URLs.
func Restore(ctx context.Context, g *GitHubClient, repo repository.Repository, inputDir, metaPath string, opts RestoreOptions) error {
	meta, err := LoadMetadata(metaPath)
	if err != nil {
		return fmt.Errorf("load metadata from %q: %w", metaPath, err)
	}

	if len(meta.Assets) == 0 {
		logger.Info("no assets in metadata, nothing to restore")
		return nil
	}

	owner, repoName := repo.Owner, repo.Name

	// Build a set of PR numbers to process for quick lookup.
	prFilter := make(map[int]bool, len(opts.PRNumbers))
	for _, n := range opts.PRNumbers {
		prFilter[n] = true
	}

	// Track the old asset URLs found at each location so a migrated comment can
	// be located by content when its original ID is no longer valid.
	locsByKey := make(map[locKey]map[string]bool)
	// Record the source PR URL per location so dry-run logging can report the
	// original location the asset was recorded at, independent of which asset
	// entry a reused URL happens to resolve to.
	prURLByLoc := make(map[locKey]string)
	for _, a := range meta.Assets {
		if len(prFilter) > 0 && !prFilter[a.PRNumber] {
			continue
		}
		key := locKey{a.PRNumber, a.Location, a.LocationID}
		if locsByKey[key] == nil {
			locsByKey[key] = make(map[string]bool)
		}
		locsByKey[key][a.AssetURL] = true
		// Record only the first non-empty PR URL for the location; do not let a
		// later duplicate entry with an empty PRURL erase it.
		if prURLByLoc[key] == "" && a.PRURL != "" {
			prURLByLoc[key] = a.PRURL
		}
	}

	// Cache PR comments so URL-based fallback lookups list each PR's comments at
	// most once across the whole restore run (avoids an N+1 listing pattern).
	cache := newCommentCache(g, repo)

	// Initialize the Playwright uploader (unless dry-run) up front so the
	// interactive login prompt appears at the very start of the command. The
	// precheck below can issue many API calls and take a while; opening the
	// browser first lets the user log in immediately instead of waiting for the
	// precheck to finish before being prompted.
	var uploader *PlaywrightUploader
	if !opts.DryRun {
		// Abort before installing/launching the browser if the context is
		// already canceled (e.g. Ctrl+C), so a canceled run does not trigger an
		// interactive login prompt or a long browser startup.
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("browser initialization canceled: %w", err)
		}
		uploader, err = NewPlaywrightUploader(opts.StateFile, repo.Host, opts.Headed)
		if err != nil {
			return fmt.Errorf("initialize browser uploader: %w", err)
		}
		defer uploader.Close()

		if err := uploader.Init(ctx, opts.StateFile, owner, repoName, opts.Headed); err != nil {
			return fmt.Errorf("initialize browser session: %w", err)
		}
	}

	// Only upload assets whose source URLs still exist in the destination body or
	// comment that will be updated. This avoids uploading assets that have no
	// replacement target.
	//
	// This precheck issues one or more API calls per location and can take a
	// while for large restores, so it logs its progress periodically. It also
	// aborts as soon as the context is canceled (e.g. Ctrl+C) instead of
	// grinding through the remaining locations with canceled-context warnings.
	logger.Info("checking destination content for asset links", "locations", len(locsByKey))
	const precheckProgressInterval = 100
	checked := 0
	restoreURLs := make(map[string]bool)
	// resolvedDstID maps each location to the destination comment ID resolved
	// during the precheck (0 for a PR body). It lets the restore-result metadata
	// record the real destination ID so a re-run can fetch the comment directly
	// instead of falling back to a full comment search.
	resolvedDstID := make(map[locKey]int64)
	// Per-location precheck outcome, consumed by the deferred resume-metadata
	// writer to decide whether each asset's work is done, still pending, or of
	// unknown status. A location is exactly one of: resolved (body was read, so
	// URL presence is known), unresolved (transient/unknown failure, keep for a
	// --continue retry), or permanently absent (confirmed 404 / not found, so its
	// work is unrestorable and dropped).
	locResolved := make(map[locKey]bool)
	locURLPresent := make(map[locKey]map[string]bool)
	unresolvedLocs := make(map[locKey]bool)
	// Track PRs already reported so the message is logged once per PR instead of
	// once per location on it.
	warnedMissingPRs := make(map[int]bool)
	for loc, oldURLs := range locsByKey {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("destination content check canceled: %w", err)
		}
		// checked counts every location processed regardless of outcome, so it
		// stays comparable to the "locations"/"total" counts logged alongside it.
		checked++
		// Probe the PR once per PR (memoized in the cache) so a failing lookup is
		// not repeated for each of its locations, which is what makes the precheck
		// slow when many locations belong to missing PRs.
		if _, prErr := cache.getPullRequest(ctx, loc.PRNumber); prErr != nil {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("destination content check canceled: %w", err)
			}
			// A confirmed 404 means the source PR does not exist in the
			// destination (e.g. a migration renumbered its PRs); its locations
			// are unrestorable and are intentionally dropped. Any other error
			// (rate limit, 5xx, auth) is transient/unknown, so mark the location
			// unresolved and keep it for a later --continue retry instead of
			// silently treating it as done.
			if gh.IsHTTPNotFound(prErr) {
				if !warnedMissingPRs[loc.PRNumber] {
					warnedMissingPRs[loc.PRNumber] = true
					logger.Warn("destination PR not found, skipping its locations",
						"pr", loc.PRNumber, "err", prErr)
				}
			} else {
				unresolvedLocs[loc] = true
				if !warnedMissingPRs[loc.PRNumber] {
					warnedMissingPRs[loc.PRNumber] = true
					logger.Warn("failed to check destination PR, keeping its locations for --continue",
						"pr", loc.PRNumber, "err", prErr)
				}
			}
			if checked%precheckProgressInterval == 0 {
				logger.Info("checking destination content", "checked", checked, "total", len(locsByKey))
			}
			continue
		}
		body, destID, bodyErr := resolveDstLocationBody(ctx, g, repo, cache, loc.PRNumber, loc.Location, loc.LocationID, oldURLs)
		if bodyErr != nil {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("destination content check canceled: %w", err)
			}
			// A confirmed "not found" is permanent (the comment is gone), so the
			// location is dropped. Any other error is transient/unknown, so mark
			// it unresolved and keep it for a --continue retry.
			if errors.Is(bodyErr, errDstLocationNotFound) {
				logger.Warn("destination location not found, skipping uploads for location",
					"pr", loc.PRNumber, "location", loc.Location, "id", loc.LocationID, "err", bodyErr)
			} else {
				unresolvedLocs[loc] = true
				logger.Warn("failed to resolve restore target, keeping location for --continue",
					"pr", loc.PRNumber, "location", loc.Location, "id", loc.LocationID, "err", bodyErr)
			}
		} else {
			locResolved[loc] = true
			resolvedDstID[loc] = destID
			present := make(map[string]bool)
			for oldURL := range oldURLs {
				if strings.Contains(body, oldURL) {
					present[oldURL] = true
					restoreURLs[oldURL] = true
				}
			}
			locURLPresent[loc] = present
		}
		if checked%precheckProgressInterval == 0 {
			logger.Info("checking destination content", "checked", checked, "total", len(locsByKey))
		}
	}
	logger.Info("destination content check completed", "locations", len(locsByKey), "matched_urls", len(restoreURLs))

	// urlReplacements maps each successfully uploaded old asset URL to its new
	// destination URL. It is declared here (before the deferred writer below) so
	// the writer can tell which candidates were completed this run.
	urlReplacements := make(map[string]string)
	// updatedLocs records locations whose destination content was successfully
	// edited this run, or that were confirmed to no longer contain the old URL
	// (a no-op edit). It is declared before the deferred writer so the writer can
	// tell which uploaded assets were actually applied. A URL update that fails
	// is only logged (it does not abort the restore), so an uploaded asset must
	// stay in the resume file until its location is recorded here.
	updatedLocs := make(map[locKey]bool)

	// Write a "restore result" metadata file (fixed name, in the input dir) when
	// the run finishes, so a re-run can resume without re-searching comments that
	// were already migrated. It keeps only the assets that still need work and
	// omits the ones proven done. Each remaining asset's location ID is rewritten
	// to the destination comment ID resolved during the precheck so the next run
	// fetches it directly. Skipped in dry-run, which makes no changes.
	defer func() {
		if opts.DryRun {
			return
		}
		remaining := make([]*PRAsset, 0)
		for _, a := range meta.Assets {
			if len(prFilter) > 0 && !prFilter[a.PRNumber] {
				continue
			}
			key := locKey{a.PRNumber, a.Location, a.LocationID}
			if !resumeAssetPending(key, a.AssetURL, locResolved, locURLPresent, unresolvedLocs, urlReplacements, updatedLocs) {
				continue
			}
			clone := *a
			if destID, ok := resolvedDstID[key]; ok {
				clone.LocationID = destID
			}
			remaining = append(remaining, &clone)
		}
		outMeta := *meta
		outMeta.Assets = remaining
		restoredPath := filepath.Join(inputDir, RestoredMetadataFilename)
		if writeErr := WriteMetadata(restoredPath, outMeta); writeErr != nil {
			logger.Warn("failed to write restore result metadata", "path", restoredPath, "err", writeErr)
			return
		}
		logger.Info("wrote restore result metadata", "path", restoredPath, "remaining", len(remaining))
	}()

	if len(restoreURLs) == 0 {
		logger.Info("no asset links matched destination content")
		return nil
	}

	// Pace uploads to avoid GitHub's secondary rate limit. lastUploadAt tracks
	// the previous upload so uploads can enforce a minimum gap.
	uploadDelay := opts.UploadDelay
	if uploadDelay <= 0 {
		uploadDelay = DefaultUploadDelay
	}
	var lastUploadAt time.Time

	// Map each asset URL to every metadata entry that references it so its file
	// can be uploaded on demand the first time the URL needs to be rewritten at a
	// location. The same URL can appear in several entries (a reused image), and
	// older/incremental dumps may leave some entries with an empty or stale
	// LocalFile; keeping all candidates lets ensureUploaded fall back to a usable
	// one. Candidates are collected across all PRs (not just prFilter), because
	// the upload source is keyed by content and uploading from an out-of-scope
	// entry's file does not modify that entry's PR.
	urlToAssets := make(map[string][]*PRAsset)
	for _, a := range meta.Assets {
		if err := ctx.Err(); err != nil {
			return err
		}
		urlToAssets[a.AssetURL] = append(urlToAssets[a.AssetURL], a)
	}

	// picked memoizes the chosen usable candidate per URL (including "none
	// usable") so locations that reference the same URL do not re-scan the
	// filesystem or re-log. Candidate selection is deterministic; upload success
	// is tracked separately in urlReplacements.
	type usableAsset struct {
		asset     *PRAsset
		localPath string
		ok        bool
	}
	picked := make(map[string]usableAsset)

	// uploadFailed records URLs whose non-fatal upload failed in this run so a
	// URL reused across locations is not retried (and re-logged) for every
	// location. The memo is per-run only, so a fresh --continue run retries.
	uploadFailed := make(map[string]bool)

	// ensureUploaded uploads the asset for oldURL the first time it is needed and
	// memoizes the result in urlReplacements, so a URL reused across locations is
	// uploaded only once. It returns (newURL, true, nil) on success,
	// ("", false, nil) when the asset cannot be uploaded (skipped and left for a
	// --continue retry), and a non-nil error only for fatal conditions (context
	// canceled or an unrecoverable browser crash) that must abort the restore.
	ensureUploaded := func(oldURL string) (string, bool, error) {
		if newURL, ok := urlReplacements[oldURL]; ok {
			return newURL, true, nil
		}
		if uploadFailed[oldURL] {
			// A previous location already failed to upload this URL in this run;
			// skip and leave it for a --continue retry.
			return "", false, nil
		}
		candidates, ok := urlToAssets[oldURL]
		if !ok || len(candidates) == 0 {
			// Unreachable in normal operation: every URL reached here comes from
			// the same metadata used to build urlToAssets. Log it distinctly so
			// an invariant regression is easy to spot.
			logger.Warn("asset URL missing from upload index, skipping", "url", oldURL)
			return "", false, nil
		}
		sel, memoed := picked[oldURL]
		if !memoed {
			// Pick the first candidate whose local file is present and safe to
			// read. Trying each candidate avoids permanently skipping a URL when
			// an earlier duplicate has an empty or stale LocalFile but a later
			// one is usable.
			for _, a := range candidates {
				if a.LocalFile == "" {
					continue
				}
				if p, good := resolveLocalPath(inputDir, a.LocalFile); good {
					sel = usableAsset{asset: a, localPath: p, ok: true}
					break
				}
			}
			picked[oldURL] = sel
			if !sel.ok {
				logger.Warn("asset has no usable local file, skipping", "url", oldURL)
			}
		}
		if !sel.ok {
			return "", false, nil
		}
		a := sel.asset
		localPath := sel.localPath

		if opts.DryRun {
			newURL := fmt.Sprintf("(dry-run:%s)", a.Filename)
			urlReplacements[oldURL] = newURL
			logger.Info("dry-run: would upload", "file", localPath, "filename", a.Filename)
			return newURL, true, nil
		}

		// Pace uploads to avoid tripping GitHub's secondary rate limit.
		if !lastUploadAt.IsZero() {
			if wait := uploadDelay - time.Since(lastUploadAt); wait > 0 {
				select {
				case <-ctx.Done():
					return "", false, ctx.Err()
				case <-time.After(wait):
				}
			}
		}

		newURL, uploadErr := uploader.Upload(ctx, localPath, a.Filename)
		// A renderer crash ("target closed") poisons the page and would make
		// every remaining upload fail. Rebuild the session and retry this file
		// once before giving up on it.
		if uploadErr != nil && isPageClosedErr(uploadErr) {
			logger.Warn("browser session lost, recovering", "file", localPath, "err", uploadErr)
			if recErr := uploader.Recover(ctx, owner, repoName); recErr != nil {
				return "", false, fmt.Errorf("recover browser session after crash: %w", recErr)
			}
			newURL, uploadErr = uploader.Upload(ctx, localPath, a.Filename)
		}
		if uploadErr != nil {
			// Context cancellation/deadline is fatal: abort instead of recording
			// a resumable skip.
			if ctx.Err() != nil || errors.Is(uploadErr, context.Canceled) || errors.Is(uploadErr, context.DeadlineExceeded) {
				return "", false, uploadErr
			}
			uploadFailed[oldURL] = true
			logger.Warn("upload failed, skipping", "file", localPath, "err", uploadErr)
			return "", false, nil
		}
		// Only record the timestamp after a successful upload so a skipped or
		// failed upload does not pace (delay) the next one.
		lastUploadAt = time.Now()
		urlReplacements[oldURL] = newURL
		logger.Info("uploaded asset", "old", oldURL, "new", newURL)
		return newURL, true, nil
	}

	// Walk each resolved location and, for every asset URL still present there,
	// upload it on demand (only if not already uploaded) and then rewrite the
	// destination body/comment. Uploading lazily per location means a file is
	// only uploaded when its URL actually needs to be rewritten, instead of
	// uploading everything up front.
	for loc, present := range locURLPresent {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(present) == 0 {
			continue
		}
		anyUploaded := false
		for oldURL := range present {
			_, ok, upErr := ensureUploaded(oldURL)
			if upErr != nil {
				return upErr
			}
			if ok {
				anyUploaded = true
			}
		}
		if !anyUploaded {
			// Nothing could be uploaded for this location; leave it for a retry.
			continue
		}

		// Restrict content-based fallback lookups (and dry-run destination
		// resolution) to URLs actually uploaded this run. Matching on a
		// non-uploaded URL could select the wrong comment, produce a no-op
		// replaceURLs, and wrongly mark the location updated (dropping it from
		// the resume file). anyUploaded guarantees this set is non-empty.
		uploadedURLs := make(map[string]bool)
		for oldURL := range present {
			if _, ok := urlReplacements[oldURL]; ok {
				uploadedURLs[oldURL] = true
			}
		}

		if opts.DryRun {
			dstLoc := resolveDstLocationURL(ctx, g, repo, cache, loc.PRNumber, loc.Location, loc.LocationID, uploadedURLs)
			// Prefer the source PR URL recorded in metadata; fall back to
			// reconstructing it from the dump's source repo so the log shows a
			// complete URL instead of a bare "#issuecomment-..." anchor when
			// PRURL is missing from the metadata.
			srcPRURL := prURLByLoc[loc]
			if srcPRURL == "" && meta.SourceRepo != "" {
				srcPRURL = fmt.Sprintf("https://%s/pull/%d", meta.SourceRepo, loc.PRNumber)
			}
			srcLoc := locationURL(srcPRURL, loc.Location, loc.LocationID)
			for oldURL := range present {
				newURL, ok := urlReplacements[oldURL]
				if !ok {
					continue
				}
				logger.Info("dry-run: would replace URL",
					"src_location", srcLoc,
					"dst_location", dstLoc,
					"old", oldURL, "new", newURL)
			}
			continue
		}

		// Apply URL replacements to this body / comment.
		switch loc.Location {
		case LocationBody:
			pr, fetchErr := gh.GetPullRequest(ctx, g, repo, loc.PRNumber)
			if fetchErr != nil {
				logger.Warn("failed to fetch PR body", "pr", loc.PRNumber, "err", fetchErr)
				continue
			}
			newBody := replaceURLs(pr.GetBody(), urlReplacements)
			if newBody == pr.GetBody() {
				// No uploaded URL remains in the body, so this location is
				// already up to date.
				updatedLocs[loc] = true
				continue
			}
			if _, updateErr := g.EditPullRequest(ctx, owner, repoName, loc.PRNumber, &github.PullRequest{Body: github.Ptr(newBody)}); updateErr != nil {
				logger.Warn("failed to update PR body", "pr", loc.PRNumber, "err", updateErr)
			} else {
				updatedLocs[loc] = true
				logger.Info("updated PR body", "pr", loc.PRNumber)
			}

		case LocationIssueComment:
			comment, fetchErr := gh.GetIssueComment(ctx, g, repo, loc.LocationID)
			if fetchErr != nil {
				// The comment ID may no longer be valid (e.g. the repository was
				// migrated and GitHub re-assigned comment IDs). Fall back to
				// searching the PR's comments for one that contains the old URL.
				logger.Warn("failed to fetch issue comment, searching by content", "id", loc.LocationID, "pr", loc.PRNumber, "err", fetchErr)
				comment, fetchErr = findIssueCommentByURLs(ctx, cache, loc.PRNumber, uploadedURLs)
				if fetchErr != nil {
					logger.Warn("failed to search issue comment", "id", loc.LocationID, "pr", loc.PRNumber, "err", fetchErr)
					continue
				}
				if comment == nil {
					logger.Warn("issue comment not found by content", "id", loc.LocationID, "pr", loc.PRNumber)
					continue
				}
			}
			newBody := replaceURLs(comment.GetBody(), urlReplacements)
			if newBody == comment.GetBody() {
				// No uploaded URL remains in the comment, so it is already up to date.
				updatedLocs[loc] = true
				continue
			}
			if _, updateErr := gh.EditIssueComment(ctx, g, repo, comment, newBody); updateErr != nil {
				logger.Warn("failed to update issue comment", "id", comment.GetID(), "err", updateErr)
			} else {
				// Reflect the edit in the (possibly cached) comment so a later
				// URL-based fallback does not re-match this already-updated comment.
				comment.Body = github.Ptr(newBody)
				updatedLocs[loc] = true
				logger.Info("updated issue comment", "id", comment.GetID())
			}

		case LocationReviewComment:
			comment, fetchErr := gh.GetPullRequestComment(ctx, g, repo, loc.LocationID)
			if fetchErr != nil {
				// The comment ID may no longer be valid (e.g. the repository was
				// migrated and GitHub re-assigned comment IDs). Fall back to
				// searching the PR's comments for one that contains the old URL.
				logger.Warn("failed to fetch review comment, searching by content", "id", loc.LocationID, "pr", loc.PRNumber, "err", fetchErr)
				comment, fetchErr = findReviewCommentByURLs(ctx, cache, loc.PRNumber, uploadedURLs)
				if fetchErr != nil {
					logger.Warn("failed to search review comment", "id", loc.LocationID, "pr", loc.PRNumber, "err", fetchErr)
					continue
				}
				if comment == nil {
					logger.Warn("review comment not found by content", "id", loc.LocationID, "pr", loc.PRNumber)
					continue
				}
			}
			newBody := replaceURLs(comment.GetBody(), urlReplacements)
			if newBody == comment.GetBody() {
				// No uploaded URL remains in the comment, so it is already up to date.
				updatedLocs[loc] = true
				continue
			}
			if _, updateErr := gh.EditPullRequestComment(ctx, g, repo, comment, newBody); updateErr != nil {
				logger.Warn("failed to update review comment", "id", comment.GetID(), "err", updateErr)
			} else {
				// Reflect the edit in the (possibly cached) comment so a later
				// URL-based fallback does not re-match this already-updated comment.
				comment.Body = github.Ptr(newBody)
				updatedLocs[loc] = true
				logger.Info("updated review comment", "id", comment.GetID())
			}
		}
	}

	if opts.DryRun {
		return nil
	}

	if len(urlReplacements) == 0 {
		logger.Info("no assets were successfully uploaded")
		return nil
	}

	if opts.ClearCache {
		if removeErr := os.Remove(opts.StateFile); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("clear browser cache after restore %q: %w", opts.StateFile, removeErr)
		}
		logger.Info("browser session cleared", "path", opts.StateFile)
	}

	return nil
}

// locKey identifies a single restore target: a PR body or a specific comment
// within a PR. The source LocationID may differ from the destination after a
// migration; the restore resolves the live ID during its precheck.
type locKey struct {
	PRNumber   int
	Location   AssetLocation
	LocationID int64
}

// errDstLocationNotFound marks a destination location (issue or review comment)
// that was confirmed absent: neither its recorded ID nor a content-based search
// found it. It is permanent (not transient), so the caller can drop the location
// instead of keeping it for a --continue retry.
var errDstLocationNotFound = errors.New("destination location not found")

// resumeAssetPending reports whether an asset entry still needs work and must be
// kept in the resume metadata. It reasons per metadata entry (location + URL)
// rather than per URL, because a URL can appear at several locations that finish
// independently.
//
// Keep the asset when:
//   - its location outcome is unknown/transient (unresolvedLocs) and it was not
//     later applied this run; or
//   - the old URL is present at a resolved location but the upload or the
//     destination edit for that location did not complete.
//
// Omit it when the location is confirmed absent, when the old URL is not present
// at a resolved location, or when it was uploaded and its location was edited
// (or confirmed to already be clean) this run.
func resumeAssetPending(
	key locKey,
	url string,
	locResolved map[locKey]bool,
	locURLPresent map[locKey]map[string]bool,
	unresolvedLocs map[locKey]bool,
	urlReplacements map[string]string,
	updatedLocs map[locKey]bool,
) bool {
	// Unknown/transient precheck outcome: keep unless a later update this run
	// (triggered because the same URL was uploaded for another location) proved
	// the location is now done.
	if unresolvedLocs[key] {
		return !updatedLocs[key]
	}
	if locResolved[key] {
		if !locURLPresent[key][url] {
			// Old URL absent at the destination: nothing to restore here.
			return false
		}
		if _, uploaded := urlReplacements[url]; !uploaded {
			// The asset was never uploaded this run, so it still needs work.
			return true
		}
		// Uploaded: done only once this location's content was edited.
		return !updatedLocs[key]
	}
	// Neither resolved nor unresolved: the location was confirmed permanently
	// absent (404 / not found). Its work is unrestorable, so drop it.
	return false
}

func replaceURLs(body string, replacements map[string]string) string {
	for oldURL, newURL := range replacements {
		body = strings.ReplaceAll(body, oldURL, newURL)
	}
	return body
}

// resolveLocalPath resolves a dump-relative local file path against inputDir and
// reports whether it points to an existing regular file. It rejects absolute
// paths and any path that starts with ".." (upward traversal). The traversal
// check is more precise than a raw strings.Contains check, which would wrongly
// reject legitimate filenames such as "foo..bar" or "..foo". It returns
// (path, true) only when the file is safe and present.
func resolveLocalPath(inputDir, localFile string) (string, bool) {
	cleanedFile := filepath.Clean(localFile)
	if filepath.IsAbs(cleanedFile) || cleanedFile == ".." || strings.HasPrefix(cleanedFile, ".."+string(filepath.Separator)) {
		return "", false
	}
	localPath := filepath.Join(inputDir, cleanedFile)
	info, statErr := os.Stat(localPath)
	if statErr != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return localPath, true
}

// locationURL builds the HTML URL (with anchor) for the asset location within a
// PR. It returns the PR URL for the body and an anchored URL for comments.
func locationURL(prURL string, location AssetLocation, locationID int64) string {
	switch location {
	case LocationIssueComment:
		return fmt.Sprintf("%s#issuecomment-%d", prURL, locationID)
	case LocationReviewComment:
		return fmt.Sprintf("%s#discussion_r%d", prURL, locationID)
	default:
		return prURL
	}
}

// prHTMLURL builds the HTML URL of a pull request in the given repository.
func prHTMLURL(repo repository.Repository, prNumber int) string {
	return fmt.Sprintf("https://%s/%s/%s/pull/%d", repo.Host, repo.Owner, repo.Name, prNumber)
}

// resolveDstLocationURL resolves the destination URL that would actually be
// edited for the given location, mirroring the fetch/search fallback used during
// a real restore. For comments it returns the live comment HTML URL (whose ID
// may differ from the source after a migration). It returns an empty string when
// the destination location cannot be resolved.
func resolveDstLocationURL(ctx context.Context, g *GitHubClient, repo repository.Repository, cache *commentCache, prNumber int, location AssetLocation, locationID int64, oldURLs map[string]bool) string {
	switch location {
	case LocationIssueComment:
		comment, err := gh.GetIssueComment(ctx, g, repo, locationID)
		if err != nil {
			comment, err = findIssueCommentByURLs(ctx, cache, prNumber, oldURLs)
			if err != nil || comment == nil {
				return ""
			}
		}
		return comment.GetHTMLURL()
	case LocationReviewComment:
		comment, err := gh.GetPullRequestComment(ctx, g, repo, locationID)
		if err != nil {
			comment, err = findReviewCommentByURLs(ctx, cache, prNumber, oldURLs)
			if err != nil || comment == nil {
				return ""
			}
		}
		return comment.GetHTMLURL()
	default:
		return prHTMLURL(repo, prNumber)
	}
}

// resolveDstLocationBody resolves the destination body or comment text for the
// given location, using the same ID lookup and content-based fallback as the
// update step. It also returns the resolved destination location ID (the live
// comment ID, which may differ from the source after a migration; 0 for a PR
// body) so callers can record where the location actually lives.
func resolveDstLocationBody(ctx context.Context, g *GitHubClient, repo repository.Repository, cache *commentCache, prNumber int, location AssetLocation, locationID int64, oldURLs map[string]bool) (string, int64, error) {
	switch location {
	case LocationIssueComment:
		comment, err := gh.GetIssueComment(ctx, g, repo, locationID)
		if err != nil {
			comment, err = findIssueCommentByURLs(ctx, cache, prNumber, oldURLs)
			if err != nil {
				return "", 0, err
			}
			if comment == nil {
				return "", 0, fmt.Errorf("destination issue comment %d not found: %w", locationID, errDstLocationNotFound)
			}
		}
		return comment.GetBody(), comment.GetID(), nil
	case LocationReviewComment:
		comment, err := gh.GetPullRequestComment(ctx, g, repo, locationID)
		if err != nil {
			comment, err = findReviewCommentByURLs(ctx, cache, prNumber, oldURLs)
			if err != nil {
				return "", 0, err
			}
			if comment == nil {
				return "", 0, fmt.Errorf("destination review comment %d not found: %w", locationID, errDstLocationNotFound)
			}
		}
		return comment.GetBody(), comment.GetID(), nil
	default:
		pr, err := cache.getPullRequest(ctx, prNumber)
		if err != nil {
			return "", 0, err
		}
		return pr.GetBody(), 0, nil
	}
}

// bodyContainsAnyURL reports whether body contains any of the given URLs.
//
// An any-match (rather than ranking candidates by how many URLs they contain)
// is intentional. replaceURLs rewrites every old->new asset URL in whatever body
// it is given, and that transform is idempotent and safe: applying it to a
// comment that merely contains a tracked old URL is always the desired outcome,
// never a destructive "wrong edit". Ranking by hit-count would not improve
// correctness and would actively break the common case where a single asset URL
// is reused across several comments (every candidate ties at one hit, so treating
// ties as ambiguous would skip legitimate updates). Do not replace this with a
// hit-count / best-match scheme.
func bodyContainsAnyURL(body string, urls map[string]bool) bool {
	for u := range urls {
		if strings.Contains(body, u) {
			return true
		}
	}
	return false
}

// commentCache lazily lists and memoizes a PR's issue and review comments so
// that repeated URL-based lookups within a single restore run reuse one listing
// per PR instead of re-fetching them. This avoids an N+1 listing pattern when a
// migration re-assigned many comment IDs and every lookup falls back to a search.
type commentCache struct {
	g    *GitHubClient
	repo repository.Repository

	pullRequests   map[int]*pullRequestResult
	issueComments  map[int][]*github.IssueComment
	reviewComments map[int][]*github.PullRequestComment
}

// pullRequestResult memoizes a single PR fetch, caching both the successful PR
// and any error (e.g. a 404 for a PR that no longer exists) so repeated lookups
// do not re-issue the request.
type pullRequestResult struct {
	pr  *github.PullRequest
	err error
}

// newCommentCache creates an empty comment cache bound to a client and repo.
func newCommentCache(g *GitHubClient, repo repository.Repository) *commentCache {
	return &commentCache{
		g:              g,
		repo:           repo,
		pullRequests:   make(map[int]*pullRequestResult),
		issueComments:  make(map[int][]*github.IssueComment),
		reviewComments: make(map[int][]*github.PullRequestComment),
	}
}

// getPullRequest returns the PR, fetching it once per number and memoizing both
// success and failure. Caching the error lets callers gate on PR reachability
// (skipping every location on a missing PR) without re-issuing a failing request
// for each location.
func (c *commentCache) getPullRequest(ctx context.Context, prNumber int) (*github.PullRequest, error) {
	if r, ok := c.pullRequests[prNumber]; ok {
		return r.pr, r.err
	}
	pr, err := gh.GetPullRequest(ctx, c.g, c.repo, prNumber)
	c.pullRequests[prNumber] = &pullRequestResult{pr: pr, err: err}
	return pr, err
}

// listIssueComments returns the PR's issue comments, fetching them once per PR
// and serving subsequent calls from the cache.
func (c *commentCache) listIssueComments(ctx context.Context, prNumber int) ([]*github.IssueComment, error) {
	if comments, ok := c.issueComments[prNumber]; ok {
		return comments, nil
	}
	comments, err := gh.ListIssueComments(ctx, c.g, c.repo, prNumber)
	if err != nil {
		logger.Warn("failed to list issue comments while searching", "pr", prNumber, "err", err)
		return nil, err
	}
	c.issueComments[prNumber] = comments
	return comments, nil
}

// listReviewComments returns the PR's review comments, fetching them once per PR
// and serving subsequent calls from the cache.
func (c *commentCache) listReviewComments(ctx context.Context, prNumber int) ([]*github.PullRequestComment, error) {
	if comments, ok := c.reviewComments[prNumber]; ok {
		return comments, nil
	}
	comments, err := gh.ListPullRequestReviewComments(ctx, c.g, c.repo, prNumber)
	if err != nil {
		logger.Warn("failed to list review comments while searching", "pr", prNumber, "err", err)
		return nil, err
	}
	c.reviewComments[prNumber] = comments
	return comments, nil
}

// findIssueCommentByURLs lists the PR's issue comments and returns the first one
// whose body contains any of the given old asset URLs. It is used as a fallback
// when the original comment ID is no longer valid (e.g. a repository migration
// re-assigned comment IDs). Returns (nil, nil) when no matching comment exists.
//
// Returning the first any-match (instead of scoring candidates by URL hit-count
// and treating ties as ambiguous) is deliberate; see bodyContainsAnyURL for the
// rationale. Because replaceURLs is an idempotent old->new rewrite, editing any
// comment that still contains a tracked old URL is safe and correct.
func findIssueCommentByURLs(ctx context.Context, cache *commentCache, prNumber int, oldURLs map[string]bool) (*github.IssueComment, error) {
	comments, err := cache.listIssueComments(ctx, prNumber)
	if err != nil {
		return nil, err
	}
	for _, c := range comments {
		if bodyContainsAnyURL(c.GetBody(), oldURLs) {
			return c, nil
		}
	}
	return nil, nil
}

// findReviewCommentByURLs lists the PR's review comments and returns the first
// one whose body contains any of the given old asset URLs. It is used as a
// fallback when the original comment ID is no longer valid (e.g. a repository
// migration re-assigned comment IDs). Returns (nil, nil) when no matching
// comment exists.
//
// As with findIssueCommentByURLs, returning the first any-match instead of a
// hit-count best-match is deliberate; see bodyContainsAnyURL for the rationale.
func findReviewCommentByURLs(ctx context.Context, cache *commentCache, prNumber int, oldURLs map[string]bool) (*github.PullRequestComment, error) {
	comments, err := cache.listReviewComments(ctx, prNumber)
	if err != nil {
		return nil, err
	}
	for _, c := range comments {
		if bodyContainsAnyURL(c.GetBody(), oldURLs) {
			return c, nil
		}
	}
	return nil, nil
}
