package assets

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cli/go-gh/v2/pkg/repository"
	"github.com/google/go-github/v84/github"
	playwright "github.com/playwright-community/playwright-go"
	"github.com/srz-zumix/go-gh-extension/pkg/gh"
	"github.com/srz-zumix/go-gh-extension/pkg/logger"
)

// uploadPolicy is the JSON response from GitHub's upload policies endpoint.
type uploadPolicy struct {
	UploadURL string                 `json:"upload_url"`
	Header    map[string]string      `json:"header"`
	Form      map[string]interface{} `json:"form"`
	Asset     struct {
		Href        string `json:"href"`
		Name        string `json:"name"`
		ContentType string `json:"content_type"`
		Size        int    `json:"size"`
	} `json:"asset"`
}

// PlaywrightUploader manages a Playwright browser session for uploading assets to GitHub.
type PlaywrightUploader struct {
	pw      *playwright.Playwright
	browser playwright.Browser
	bctx    playwright.BrowserContext
	page    playwright.Page
	host    string
	scheme  string
	csrf    string
	repoID  string
	repoURL string
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
	headless := stateErr == nil && !forceHeaded

	if !headless {
		fmt.Fprintln(os.Stderr, "No saved browser session found. A browser window will open for interactive GitHub login.")
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
	if stateErr == nil {
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

// extractCSRF reads the CSRF token from the current page.
// Checks meta[name="csrf-token"] first, then input[name="authenticity_token"]
// as a fallback for pages that embed the token in a form field.
func (u *PlaywrightUploader) extractCSRF() (string, error) {
	v, err := u.page.Evaluate(
		`document.querySelector('meta[name="csrf-token"]')?.getAttribute('content') ||` +
			`document.querySelector('input[name="authenticity_token"]')?.value ||` +
			`null`,
	)
	if err != nil {
		return "", fmt.Errorf("evaluate CSRF token: %w", err)
	}
	if v == nil {
		return "", nil
	}
	s, _ := v.(string)
	return s, nil
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

// Init navigates to the repository's new-issue form and acquires the CSRF token
// required for subsequent uploads. The repository ID is provided by the caller
// (obtained via the GitHub API) so we do not need to scrape it from the page.
//
// The issues/new page is used because it:
//   - requires authentication (GitHub redirects to /login if not signed in)
//   - is never served from a CDN cache, so it always includes the CSRF meta tag
//
// If not authenticated, a headed browser is opened so the user can log in
// interactively (up to 5 minutes). The session is saved to stateFile for
// headless reuse on subsequent runs.
func (u *PlaywrightUploader) Init(stateFile, owner, repo, repoID string) error {
	// issues/new?template= forces the blank-issue editor directly, bypassing
	// any issue template chooser. The page is authenticated and non-CDN-cached,
	// so it always carries the CSRF meta tag and the markdown editor with the
	// hidden file-attachment component.
	issuesNewURL := fmt.Sprintf("%s://%s/%s/%s/issues/new?template=", u.scheme, u.host, owner, repo)
	repoURL := fmt.Sprintf("%s://%s/%s/%s", u.scheme, u.host, owner, repo)

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
		if _, stateErr := os.Stat(stateFile); stateErr == nil {
			return fmt.Errorf("browser session expired; delete %q and re-run to log in interactively", stateFile)
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

		repoGlob := fmt.Sprintf("%s://%s/%s/%s**", u.scheme, u.host, owner, repo)
		if waitErr := u.page.WaitForURL(repoGlob, playwright.PageWaitForURLOptions{
			Timeout: playwright.Float(300000),
		}); waitErr != nil {
			return fmt.Errorf("login timed out (5 minutes): %w", waitErr)
		}
		if waitErr := u.page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
			State: playwright.LoadStateDomcontentloaded,
		}); waitErr != nil {
			return fmt.Errorf("wait for page load after login: %w", waitErr)
		}

		// Save session for future headless runs.
		if err := os.MkdirAll(filepath.Dir(stateFile), 0o700); err != nil {
			return fmt.Errorf("create state directory: %w", err)
		}
		if _, err := u.bctx.StorageState(stateFile); err != nil {
			return fmt.Errorf("save browser session to %q: %w", stateFile, err)
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

	// Store the caller-supplied repository ID (obtained via the GitHub API).
	u.repoID = repoID

	// The issues/new page is a React SPA: the CSRF token is injected into the
	// DOM asynchronously after the initial load event. Poll until it appears.
	if _, waitErr := u.page.WaitForFunction(
		`document.querySelector('meta[name="csrf-token"]') !== null ||`+
			`document.querySelector('input[name="authenticity_token"]') !== null`,
		playwright.PageWaitForFunctionOptions{Timeout: playwright.Float(15000)},
	); waitErr != nil {
		return fmt.Errorf("wait for CSRF token on issues/new (URL: %s): %w", u.page.URL(), waitErr)
	}

	// Extract the CSRF token. The issues/new page is not CDN-cached and always
	// includes meta[name="csrf-token"] for authenticated users.
	csrf, err := u.extractCSRF()
	if err != nil {
		return fmt.Errorf("acquire CSRF token: %w", err)
	}
	if csrf == "" {
		return fmt.Errorf("CSRF token not found on issues/new page (URL: %s)", u.page.URL())
	}
	u.csrf = csrf
	u.repoURL = repoURL
	// Wait for network idle so React finishes hydrating the page (including the
	// markdown editor) before Upload() tries to dispatch drag-drop events.
	// Ignore the error: some pages have background polling that prevents a true
	// idle; as long as the CSRF token was found the page is sufficiently ready.
	_ = u.page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State:   playwright.LoadStateNetworkidle,
		Timeout: playwright.Float(15000),
	})
	return nil
}

// Upload uploads the file at localPath to GitHub and returns the new CDN URL.
// It dispatches a native drag-drop event with a DataTransfer containing the
// file onto the markdown editor textarea. GitHub's React markdown editor
// component handles the drop event, posting to /upload/policies/assets and
// then uploading to S3 — all with proper CSRF/session handling because the
// request originates from inside the browser page context.
func (u *PlaywrightUploader) Upload(localPath, filename string) (string, error) {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return "", fmt.Errorf("read file %q: %w", localPath, err)
	}

	contentType := mime.TypeByExtension(filepath.Ext(filename))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Base64-encode the file so it can be passed through page.Evaluate().
	b64 := base64.StdEncoding.EncodeToString(data)

	// ExpectResponse registers the listener BEFORE the callback runs, so no
	// race can occur between the upload trigger and the response capture.
	// The first argument must be func(string) bool (URL predicate), not
	// func(playwright.Response) bool — playwright-go's newURLMatcher only
	// accepts string, *regexp.Regexp, or func(string) bool.
	resp, err := u.page.ExpectResponse(
		func(u string) bool {
			return strings.Contains(u, "/upload/policies/assets")
		},
		func() error {
			// Dispatch a drag-drop event with the file data onto the markdown
			// editor. The React MarkdownEditor component listens for native drop
			// events, reads dataTransfer.files, and triggers the upload flow.
			_, evalErr := u.page.Evaluate(`
				([b64, fname, ctype]) => {
					const binary = atob(b64);
					const bytes = new Uint8Array(binary.length);
					for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
					const file = new File([bytes], fname, {type: ctype});
					const dt = new DataTransfer();
					dt.items.add(file);
					// Target the React textarea; fall back to file-attachment or any textarea.
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
		return "", fmt.Errorf("upload %q: %w", filename, err)
	}

	body, err := resp.Body()
	if err != nil {
		return "", fmt.Errorf("read upload policy body for %q: %w", filename, err)
	}
	if resp.Status() != 200 && resp.Status() != 201 {
		return "", fmt.Errorf("upload policy returned %d for %q: %s", resp.Status(), filename, string(body))
	}

	var policy uploadPolicy
	if err := json.Unmarshal(body, &policy); err != nil {
		return "", fmt.Errorf("parse upload policy for %q: %w", filename, err)
	}
	if policy.Asset.Href == "" {
		return "", fmt.Errorf("upload policy missing asset href for %q", filename)
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
		// Obtain the repository ID via the GitHub API. This is required by
		// GitHub's upload policies endpoint and is more reliable than scraping
		// it from a browser page.
		ghRepo, err := g.GetRepository(ctx, owner, repoName)
		if err != nil {
			return fmt.Errorf("get repository info: %w", err)
		}
		repoID := strconv.FormatInt(ghRepo.GetID(), 10)

		uploader, err = NewPlaywrightUploader(opts.StateFile, repo.Host, opts.Headed)
		if err != nil {
			return fmt.Errorf("initialize browser uploader: %w", err)
		}
		defer uploader.Close()

		if err := uploader.Init(opts.StateFile, owner, repoName, repoID); err != nil {
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
		if len(prFilter) > 0 && !prFilter[a.PRNumber] {
			continue
		}
		if a.LocalFile == "" {
			logger.Warn("asset has no local file, skipping", "url", a.AssetURL)
			continue
		}
		localPath := filepath.Join(inputDir, a.LocalFile)
		if _, statErr := os.Stat(localPath); statErr != nil {
			logger.Warn("local file not found, skipping", "file", localPath)
			continue
		}

		if opts.DryRun {
			urlReplacements[a.AssetURL] = fmt.Sprintf("(dry-run:%s)", a.Filename)
			logger.Info("dry-run: would upload", "file", localPath, "filename", a.Filename)
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

	if opts.DryRun {
		for oldURL, newURL := range urlReplacements {
			logger.Info("dry-run: would replace URL", "old", oldURL, "new", newURL)
		}
		return nil
	}

	if len(urlReplacements) == 0 {
		logger.Info("no assets were successfully uploaded")
		return nil
	}

	// Group upload results by PR + location to minimise API calls.
	type locKey struct {
		PRNumber   int
		Location   AssetLocation
		LocationID int64
	}
	locsToUpdate := make(map[locKey]bool)
	for _, a := range meta.Assets {
		if _, ok := urlReplacements[a.AssetURL]; !ok {
			continue
		}
		locsToUpdate[locKey{a.PRNumber, a.Location, a.LocationID}] = true
	}

	// Apply URL replacements to each body / comment.
	for loc := range locsToUpdate {
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
				logger.Warn("failed to fetch issue comment", "id", loc.LocationID, "err", fetchErr)
				continue
			}
			newBody := replaceURLs(comment.GetBody(), urlReplacements)
			if newBody == comment.GetBody() {
				continue
			}
			if _, updateErr := gh.EditIssueComment(ctx, g, repo, loc.LocationID, newBody); updateErr != nil {
				logger.Warn("failed to update issue comment", "id", loc.LocationID, "err", updateErr)
			} else {
				logger.Info("updated issue comment", "id", loc.LocationID)
			}

		case LocationReviewComment:
			comment, fetchErr := gh.GetPullRequestComment(ctx, g, repo, loc.LocationID)
			if fetchErr != nil {
				logger.Warn("failed to fetch review comment", "id", loc.LocationID, "err", fetchErr)
				continue
			}
			newBody := replaceURLs(comment.GetBody(), urlReplacements)
			if newBody == comment.GetBody() {
				continue
			}
			if _, updateErr := gh.EditPullRequestComment(ctx, g, repo, loc.LocationID, newBody); updateErr != nil {
				logger.Warn("failed to update review comment", "id", loc.LocationID, "err", updateErr)
			} else {
				logger.Info("updated review comment", "id", loc.LocationID)
			}
		}
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
