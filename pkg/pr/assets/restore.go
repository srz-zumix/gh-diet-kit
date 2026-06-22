package assets

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
}

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
	const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	ctxOpts := playwright.BrowserNewContextOptions{
		UserAgent: playwright.String(userAgent),
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
		pw:      pw,
		browser: browser,
		bctx:    bctx,
		page:    page,
		host:    host,
		scheme:  "https",
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
//  1. Playwright drag-drop event → browser sends POST /upload/policies/assets
//     with proper session cookies and CSRF. ExpectResponse captures the response.
//  2. Go net/http POST to S3 presigned URL (no auth required).
//  3. Browser fetch() PUT to /upload/assets/{id} to confirm the upload.
func (u *PlaywrightUploader) Upload(localPath, filename string) (string, error) {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return "", fmt.Errorf("read file %q: %w", localPath, err)
	}

	contentType := mime.TypeByExtension(filepath.Ext(filename))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Base64-encode so the file bytes survive the Go→JS boundary in Evaluate.
	b64 := base64.StdEncoding.EncodeToString(data)

	// Step 1: let the browser make the authenticated policy request.
	// ExpectResponse registers the listener BEFORE the drag-drop fires so there
	// is no race. The browser carries the correct session cookies and CSRF token,
	// sidestepping any server-side origin/CSRF validation that rejects Go HTTP.
	policyResp, err := u.page.ExpectResponse(
		func(rawURL string) bool {
			return strings.Contains(rawURL, "/upload/policies/assets")
		},
		func() error {
			_, evalErr := u.page.Evaluate(`
				([b64, fname, ctype]) => {
					const binary = atob(b64);
					const bytes = new Uint8Array(binary.length);
					for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
					const file = new File([bytes], fname, {type: ctype});
					const dt = new DataTransfer();
					dt.items.add(file);
					// Target the Primer React textarea; fall back broadly.
					const el = document.querySelector('.prc-Textarea-TextArea-snlco') ||
						document.querySelector('file-attachment') ||
						document.querySelector('textarea');
					if (!el) return;
					el.dispatchEvent(new DragEvent('dragenter', {dataTransfer: dt, bubbles: true}));
					el.dispatchEvent(new DragEvent('dragover',  {dataTransfer: dt, bubbles: true, cancelable: true}));
					el.dispatchEvent(new DragEvent('drop',      {dataTransfer: dt, bubbles: true, cancelable: true}));
				}
			`, []interface{}{b64, filename, contentType})
			return evalErr
		},
		playwright.PageExpectResponseOptions{Timeout: playwright.Float(30000)},
	)
	if err != nil {
		return "", fmt.Errorf("upload policy request for %q: %w", filename, err)
	}
	policyBody, err := policyResp.Body()
	if err != nil {
		return "", fmt.Errorf("read upload policy body for %q: %w", filename, err)
	}
	if policyResp.Status() != http.StatusOK && policyResp.Status() != http.StatusCreated {
		return "", fmt.Errorf("upload policy returned %d for %q: %s", policyResp.Status(), filename, string(policyBody))
	}

	var policy uploadPolicy
	if err := json.Unmarshal(policyBody, &policy); err != nil {
		return "", fmt.Errorf("parse upload policy for %q: %w", filename, err)
	}
	if policy.Asset.Href == "" {
		return "", fmt.Errorf("upload policy missing asset href for %q", filename)
	}

	httpClient := &http.Client{Timeout: 5 * time.Minute}

	// Step 2: upload file bytes to S3 using the presigned multipart form.
	// S3 presigned POSTs do not require session cookies or CSRF tokens.
	var s3Buf bytes.Buffer
	mw := multipart.NewWriter(&s3Buf)
	for k, v := range policy.Form {
		if err := mw.WriteField(k, v); err != nil {
			return "", fmt.Errorf("write S3 form field %q: %w", k, err)
		}
	}
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("create S3 file field: %w", err)
	}
	if _, err := fw.Write(data); err != nil {
		return "", fmt.Errorf("write file data to S3 form: %w", err)
	}
	if err := mw.Close(); err != nil {
		return "", fmt.Errorf("close S3 multipart writer: %w", err)
	}
	s3Req, err := http.NewRequest(http.MethodPost, policy.UploadURL, &s3Buf)
	if err != nil {
		return "", fmt.Errorf("create S3 request: %w", err)
	}
	s3Req.Header.Set("Content-Type", mw.FormDataContentType())
	s3Resp, err := httpClient.Do(s3Req)
	if err != nil {
		return "", fmt.Errorf("S3 upload for %q: %w", filename, err)
	}
	defer s3Resp.Body.Close() //nolint:errcheck
	if s3Resp.StatusCode != http.StatusOK &&
		s3Resp.StatusCode != http.StatusCreated &&
		s3Resp.StatusCode != http.StatusNoContent {
		s3Body, _ := io.ReadAll(io.LimitReader(s3Resp.Body, 4096))
		return "", fmt.Errorf("S3 upload returned %d for %q: %s", s3Resp.StatusCode, filename, string(s3Body))
	}

	// Step 3: confirm the upload using the browser so session cookies are
	// included automatically. The upload-specific asset_upload_authenticity_token
	// is passed as the body; it is scoped to this single upload, not the page CSRF.
	confirmToken := policy.AssetUploadAuthenticityToken
	if confirmToken == "" {
		confirmToken = policy.UploadAuthenticityToken
	}
	confirmBodyStr := url.Values{"authenticity_token": {confirmToken}}.Encode()
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
		}).then(async r => ({ status: r.status, body: await r.text() }))
	`, []interface{}{policy.AssetUploadURL, confirmBodyStr})
	if err != nil {
		return "", fmt.Errorf("upload confirm request for %q: %w", filename, err)
	}
	resultMap, ok := confirmResult.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("unexpected confirm response type for %q: %T", filename, confirmResult)
	}
	var confirmStatus int
	switch v := resultMap["status"].(type) {
	case float64:
		confirmStatus = int(v)
	case int:
		confirmStatus = v
	case int64:
		confirmStatus = int(v)
	default:
		return "", fmt.Errorf("unexpected confirm status type for %q: %T", filename, resultMap["status"])
	}
	if confirmStatus != http.StatusOK && confirmStatus != http.StatusCreated {
		return "", fmt.Errorf("upload confirmation returned %d for %q: %s", confirmStatus, filename, resultMap["body"])
	}

	return policy.Asset.Href, nil
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
}

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

	// Initialize the Playwright uploader (unless dry-run).
	var uploader *PlaywrightUploader
	if !opts.DryRun {
		uploader, err = NewPlaywrightUploader(opts.StateFile, repo.Host, opts.Headed)
		if err != nil {
			return fmt.Errorf("initialize browser uploader: %w", err)
		}
		defer uploader.Close()

		if err := uploader.Init(ctx, opts.StateFile, owner, repoName, opts.Headed); err != nil {
			return fmt.Errorf("initialize browser session: %w", err)
		}
	}

	// Build a set of PR numbers to process for quick lookup.
	prFilter := make(map[int]bool, len(opts.PRNumbers))
	for _, n := range opts.PRNumbers {
		prFilter[n] = true
	}

	// Build URL replacement map: old asset URL → new CDN URL.
	urlReplacements := make(map[string]string)
	for _, a := range meta.Assets {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(prFilter) > 0 && !prFilter[a.PRNumber] {
			continue
		}
		if a.LocalFile == "" {
			logger.Warn("asset has no local file, skipping", "url", a.AssetURL)
			continue
		}
		// Normalize the path, then reject absolute paths and any path that
		// starts with ".." (upward traversal). This is more precise than a
		// raw strings.Contains check, which would wrongly reject legitimate
		// filenames such as "foo..bar" or "..foo".
		cleanedFile := filepath.Clean(a.LocalFile)
		if filepath.IsAbs(cleanedFile) || cleanedFile == ".." || strings.HasPrefix(cleanedFile, ".."+string(filepath.Separator)) {
			logger.Warn("asset local file path is not allowed, skipping", "file", a.LocalFile)
			continue
		}
		localPath := filepath.Join(inputDir, cleanedFile)
		if _, statErr := os.Stat(localPath); statErr != nil {
			logger.Warn("local file not found, skipping", "file", localPath)
			continue
		}

		if opts.DryRun {
			if _, alreadyDone := urlReplacements[a.AssetURL]; !alreadyDone {
				urlReplacements[a.AssetURL] = fmt.Sprintf("(dry-run:%s)", a.Filename)
				logger.Info("dry-run: would upload", "file", localPath, "filename", a.Filename)
			}
			continue
		}

		// Skip re-uploading an asset whose source URL has already been processed.
		// The same AssetURL can appear across multiple PR locations (e.g. a reused
		// image), but we only need to upload it once.
		if _, alreadyDone := urlReplacements[a.AssetURL]; alreadyDone {
			continue
		}

		newURL, uploadErr := uploader.Upload(localPath, a.Filename)
		if uploadErr != nil {
			logger.Warn("upload failed, skipping", "file", localPath, "err", uploadErr)
			continue
		}

		urlReplacements[a.AssetURL] = newURL
		logger.Info("uploaded asset", "old", a.AssetURL, "new", newURL)
	}

	// Group upload results by PR + location to minimise API calls.
	type locKey struct {
		PRNumber   int
		Location   AssetLocation
		LocationID int64
	}
	// Track the old asset URLs found at each location so a migrated comment can
	// be located by content when its original ID is no longer valid.
	locsToUpdate := make(map[locKey]map[string]bool)
	for _, a := range meta.Assets {
		if len(prFilter) > 0 && !prFilter[a.PRNumber] {
			continue
		}
		if _, ok := urlReplacements[a.AssetURL]; !ok {
			continue
		}
		key := locKey{a.PRNumber, a.Location, a.LocationID}
		if locsToUpdate[key] == nil {
			locsToUpdate[key] = make(map[string]bool)
		}
		locsToUpdate[key][a.AssetURL] = true
	}

	// Cache PR comments so URL-based fallback lookups list each PR's comments at
	// most once across the whole restore run (avoids an N+1 listing pattern).
	cache := newCommentCache(g, repo)

	if opts.DryRun {
		// Cache resolved destination location URLs to avoid duplicate API calls
		// when several assets share the same comment.
		dstURLCache := make(map[locKey]string)
		for _, a := range meta.Assets {
			if len(prFilter) > 0 && !prFilter[a.PRNumber] {
				continue
			}
			newURL, ok := urlReplacements[a.AssetURL]
			if !ok {
				continue
			}
			key := locKey{a.PRNumber, a.Location, a.LocationID}
			dstLoc, cached := dstURLCache[key]
			if !cached {
				dstLoc = resolveDstLocationURL(ctx, g, repo, cache, a.PRNumber, a.Location, a.LocationID, locsToUpdate[key])
				dstURLCache[key] = dstLoc
			}
			logger.Info("dry-run: would replace URL",
				"src_location", locationURL(a.PRURL, a.Location, a.LocationID),
				"dst_location", dstLoc,
				"old", a.AssetURL, "new", newURL)
		}
		return nil
	}

	if len(urlReplacements) == 0 {
		logger.Info("no assets were successfully uploaded")
		return nil
	}

	// Apply URL replacements to each body / comment.
	for loc, oldURLs := range locsToUpdate {
		switch loc.Location {
		case LocationBody:
			pr, fetchErr := gh.GetPullRequest(ctx, g, repo, loc.PRNumber)
			if fetchErr != nil {
				logger.Warn("failed to fetch PR body", "pr", loc.PRNumber, "err", fetchErr)
				continue
			}
			newBody := replaceURLs(pr.GetBody(), urlReplacements)
			if newBody == pr.GetBody() {
				continue
			}
			if _, updateErr := g.EditPullRequest(ctx, owner, repoName, loc.PRNumber, &github.PullRequest{Body: github.Ptr(newBody)}); updateErr != nil {
				logger.Warn("failed to update PR body", "pr", loc.PRNumber, "err", updateErr)
			} else {
				logger.Info("updated PR body", "pr", loc.PRNumber)
			}

		case LocationIssueComment:
			comment, fetchErr := gh.GetIssueComment(ctx, g, repo, loc.LocationID)
			if fetchErr != nil {
				// The comment ID may no longer be valid (e.g. the repository was
				// migrated and GitHub re-assigned comment IDs). Fall back to
				// searching the PR's comments for one that contains the old URL.
				logger.Warn("failed to fetch issue comment, searching by content", "id", loc.LocationID, "pr", loc.PRNumber, "err", fetchErr)
				comment, fetchErr = findIssueCommentByURLs(ctx, cache, loc.PRNumber, oldURLs)
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
				continue
			}
			if _, updateErr := gh.EditIssueComment(ctx, g, repo, comment, newBody); updateErr != nil {
				logger.Warn("failed to update issue comment", "id", comment.GetID(), "err", updateErr)
			} else {
				// Reflect the edit in the (possibly cached) comment so a later
				// URL-based fallback does not re-match this already-updated comment.
				comment.Body = github.Ptr(newBody)
				logger.Info("updated issue comment", "id", comment.GetID())
			}

		case LocationReviewComment:
			comment, fetchErr := gh.GetPullRequestComment(ctx, g, repo, loc.LocationID)
			if fetchErr != nil {
				// The comment ID may no longer be valid (e.g. the repository was
				// migrated and GitHub re-assigned comment IDs). Fall back to
				// searching the PR's comments for one that contains the old URL.
				logger.Warn("failed to fetch review comment, searching by content", "id", loc.LocationID, "pr", loc.PRNumber, "err", fetchErr)
				comment, fetchErr = findReviewCommentByURLs(ctx, cache, loc.PRNumber, oldURLs)
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
				continue
			}
			if _, updateErr := gh.EditPullRequestComment(ctx, g, repo, comment, newBody); updateErr != nil {
				logger.Warn("failed to update review comment", "id", comment.GetID(), "err", updateErr)
			} else {
				// Reflect the edit in the (possibly cached) comment so a later
				// URL-based fallback does not re-match this already-updated comment.
				comment.Body = github.Ptr(newBody)
				logger.Info("updated review comment", "id", comment.GetID())
			}
		}
	}

	if opts.ClearCache {
		if removeErr := os.Remove(opts.StateFile); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("clear browser cache after restore %q: %w", opts.StateFile, removeErr)
		}
		logger.Info("browser session cleared", "path", opts.StateFile)
	}

	return nil
}

// replaceURLs replaces all occurrences of old asset URLs in body with their
// mapped new URLs.
func replaceURLs(body string, replacements map[string]string) string {
	for oldURL, newURL := range replacements {
		body = strings.ReplaceAll(body, oldURL, newURL)
	}
	return body
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

	issueComments  map[int][]*github.IssueComment
	reviewComments map[int][]*github.PullRequestComment
}

// newCommentCache creates an empty comment cache bound to a client and repo.
func newCommentCache(g *GitHubClient, repo repository.Repository) *commentCache {
	return &commentCache{
		g:              g,
		repo:           repo,
		issueComments:  make(map[int][]*github.IssueComment),
		reviewComments: make(map[int][]*github.PullRequestComment),
	}
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
