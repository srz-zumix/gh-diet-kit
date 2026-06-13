package assets

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/cli/go-gh/v2/pkg/repository"
	"github.com/google/go-github/v88/github"
	"github.com/srz-zumix/go-gh-extension/pkg/gh"
	"github.com/srz-zumix/go-gh-extension/pkg/httputil"
	"github.com/srz-zumix/go-gh-extension/pkg/ioutil"
	"github.com/srz-zumix/go-gh-extension/pkg/logger"
	"github.com/srz-zumix/go-gh-extension/pkg/markdown"
)

// GitHubClient is a type alias for the shared GitHubClient from go-gh-extension.
type GitHubClient = gh.GitHubClient

// NewGitHubClientWithRepo creates a GitHub API client configured for the given repository.
func NewGitHubClientWithRepo(repo repository.Repository) (*GitHubClient, error) {
	return gh.NewGitHubClientWithRepo(repo)
}

// AssetLocation indicates where an asset was found in a PR.
type AssetLocation string

const (
	// LocationBody is the PR description body.
	LocationBody AssetLocation = "body"
	// LocationIssueComment is a general (issue-style) comment on the PR.
	LocationIssueComment AssetLocation = "issue_comment"
	// LocationReviewComment is a code-review inline comment on the PR.
	LocationReviewComment AssetLocation = "review_comment"
)

// AssetType classifies the media type of a PR asset.
type AssetType string

const (
	// AssetTypeImage is a still image (jpg, png, gif, …).
	AssetTypeImage AssetType = "image"
	// AssetTypeVideo is a video file (mp4, mov, …).
	AssetTypeVideo AssetType = "video"
	// AssetTypeOther is any other binary attachment.
	AssetTypeOther AssetType = "other"
)

var (
	imageExtensions = map[string]bool{
		".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
		".webp": true, ".bmp": true, ".tiff": true, ".svg": true, ".ico": true,
	}
	videoExtensions = map[string]bool{
		".mp4": true, ".mov": true, ".avi": true, ".mkv": true,
		".webm": true, ".wmv": true, ".flv": true,
	}
)

// PRAsset holds information about a single media asset attached to a PR.
type PRAsset struct {
	PRNumber   int           `json:"pr_number"`
	PRURL      string        `json:"pr_url"`
	Location   AssetLocation `json:"location"`
	LocationID int64         `json:"location_id,omitempty"` // 0 for body, comment ID otherwise
	AssetURL   string        `json:"asset_url"`
	Filename   string        `json:"filename"`
	FileSize   int64         `json:"file_size"` // -1 when unknown
	Type       AssetType     `json:"type"`
	LocalFile  string        `json:"local_file,omitempty"` // relative path under output-dir; set by dump
}

// DumpMetadata is the top-level object written to metadata.json during a dump.
type DumpMetadata struct {
	SourceRepo  string         `json:"source_repo"` // host/owner/repo
	DumpedAt    string         `json:"dumped_at"`
	PRUpdatedAt map[int]string `json:"pr_updated_at,omitempty"` // pr_number → updated_at (RFC3339)
	Assets      []*PRAsset     `json:"assets"`
}

// AssetsOptions controls which PRs are scanned for assets.
type AssetsOptions struct {
	// State filters PRs by state: "open", "closed", or "all". Defaults to "all".
	State string
	// PRNumbers limits the scan to specific PR numbers. Empty means all PRs.
	PRNumbers []int
	// MaxPRs caps the number of PRs fetched when PRNumbers is empty. 0 = unlimited.
	MaxPRs int
	// NoFileSize skips the HEAD request used to determine asset file sizes.
	NoFileSize bool
}

// GetAssetType classifies a URL by its filename extension.
func GetAssetType(assetURL string) AssetType {
	name := strings.ToLower(ioutil.GetFilename(assetURL))
	ext := path.Ext(name)
	if imageExtensions[ext] {
		return AssetTypeImage
	}
	if videoExtensions[ext] {
		return AssetTypeVideo
	}
	return AssetTypeOther
}

// assetTypeFromName classifies a media type from a resolved filename.
func assetTypeFromName(filename string) AssetType {
	ext := strings.ToLower(path.Ext(filename))
	if imageExtensions[ext] {
		return AssetTypeImage
	}
	if videoExtensions[ext] {
		return AssetTypeVideo
	}
	return AssetTypeOther
}

// ExtractAssetURLs returns all GitHub-hosted asset URLs found in text.
// patterns is a list of compiled regular expressions built by BuildAssetURLPatterns.
// Duplicate URLs are deduplicated.
func ExtractAssetURLs(text string, patterns []*regexp.Regexp) []string {
	seen := make(map[string]bool)
	var result []string
	for _, re := range patterns {
		for _, match := range re.FindAllString(text, -1) {
			// Strip trailing punctuation that may have been captured.
			match = strings.TrimRight(match, ".,;:!?")
			if !seen[match] {
				seen[match] = true
				result = append(result, match)
			}
		}
	}
	return result
}

// assetsFromText converts raw asset URLs found in text into PRAsset records.
// It extracts filename hints from surrounding Markdown syntax before scanning for URLs.
func assetsFromText(text string, prNumber int, prURL string, location AssetLocation, locationID int64, patterns []*regexp.Regexp) []*PRAsset {
	hints := markdown.ExtractFilenameHints(text)
	urls := ExtractAssetURLs(text, patterns)
	assets := make([]*PRAsset, 0, len(urls))
	for _, u := range urls {
		filename := hints[u]
		if filename == "" {
			filename = ioutil.GetFilename(u)
		}
		assets = append(assets, &PRAsset{
			PRNumber:   prNumber,
			PRURL:      prURL,
			Location:   location,
			LocationID: locationID,
			AssetURL:   u,
			Filename:   filename,
			FileSize:   -1,
			Type:       assetTypeFromName(filename),
		})
	}
	return assets
}

// bodyText safely dereferences a *string from GitHub API objects.
func bodyText(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// prURL builds a human-readable HTML URL for a PR.
func prURL(pr *github.PullRequest) string {
	if pr.HTMLURL != nil {
		return *pr.HTMLURL
	}
	return ""
}

// FindPRsWithAssets scans pull requests in the repository and returns all media
// assets found in their bodies, issue comments, and review comments.
// It respects opts.State, opts.PRNumbers, and opts.MaxPRs for filtering.
func FindPRsWithAssets(ctx context.Context, g *GitHubClient, repo repository.Repository, opts AssetsOptions, httpClient *http.Client) ([]*PRAsset, error) {
	prs, err := FetchPRs(ctx, g, repo, opts)
	if err != nil {
		return nil, err
	}

	patterns := gh.BuildAssetURLPatterns(repo.Host)
	logger.Info("scanning pull requests for assets", "count", len(prs))

	var allAssets []*PRAsset
	for _, pr := range prs {
		allAssets = append(allAssets, ScanSinglePR(ctx, g, repo, pr, patterns, httpClient, opts.NoFileSize)...)
	}

	logger.Info("asset scan complete", "assets_found", len(allAssets))
	return allAssets, nil
}

// FetchPRs retrieves the list of pull requests according to opts.
func FetchPRs(ctx context.Context, g *GitHubClient, repo repository.Repository, opts AssetsOptions) ([]*github.PullRequest, error) {
	state := opts.State
	if state == "" {
		state = "all"
	}
	if len(opts.PRNumbers) > 0 {
		prs := make([]*github.PullRequest, 0, len(opts.PRNumbers))
		for _, num := range opts.PRNumbers {
			pr, err := gh.GetPullRequest(ctx, g, repo, num)
			if err != nil {
				return nil, fmt.Errorf("failed to get PR #%d: %w", num, err)
			}
			prs = append(prs, pr)
		}
		return prs, nil
	}
	listOpts := &github.PullRequestListOptions{State: state, Sort: "updated", Direction: "desc"}
	prs, err := g.ListPullRequests(ctx, repo.Owner, repo.Name, listOpts, opts.MaxPRs)
	if err != nil {
		return nil, fmt.Errorf("failed to list pull requests: %w", err)
	}
	return prs, nil
}

// ScanSinglePR scans one pull request's body, issue comments, and review comments
// for media assets and optionally fetches file size and filename metadata.
func ScanSinglePR(ctx context.Context, g *GitHubClient, repo repository.Repository, pr *github.PullRequest, patterns []*regexp.Regexp, httpClient *http.Client, noFileSize bool) []*PRAsset {
	num := pr.GetNumber()
	url := prURL(pr)

	var prAssets []*PRAsset

	// 1. PR body
	prAssets = append(prAssets, assetsFromText(bodyText(pr.Body), num, url, LocationBody, 0, patterns)...)

	// 2. Issue-style comments
	issueComments, err := gh.ListIssueComments(ctx, g, repo, num)
	if err != nil {
		logger.Warn("failed to list issue comments", "pr", num, "error", err)
	} else {
		for _, c := range issueComments {
			prAssets = append(prAssets, assetsFromText(bodyText(c.Body), num, url, LocationIssueComment, c.GetID(), patterns)...)
		}
	}

	// 3. Code review comments
	reviewComments, err := gh.ListPullRequestReviewComments(ctx, g, repo, num)
	if err != nil {
		logger.Warn("failed to list review comments", "pr", num, "error", err)
	} else {
		for _, c := range reviewComments {
			prAssets = append(prAssets, assetsFromText(bodyText(c.Body), num, url, LocationReviewComment, c.GetID(), patterns)...)
		}
	}

	if !noFileSize && httpClient != nil {
		applyAssetMeta(ctx, httpClient, prAssets)
	}

	return prAssets
}

// applyAssetMeta fetches HTTP metadata (size, filename, content-type) for each asset
// and updates the asset fields in place.
func applyAssetMeta(ctx context.Context, httpClient *http.Client, prAssets []*PRAsset) {
	for _, a := range prAssets {
		ghHost := ""
		if u, err := url.Parse(a.AssetURL); err == nil {
			ghHost = u.Hostname()
		}
		meta := httputil.FetchAssetMeta(ctx, httpClient, a.AssetURL, ghHost)
		a.FileSize = meta.Size
		if meta.Filename != "" {
			a.Filename = meta.Filename
			a.Type = assetTypeFromName(meta.Filename)
		} else {
			// Determine extension: prefer ExtHint (from redirect URL path) over
			// ContentType inference so that S3-hosted assets with an extension in
			// their pre-signed URL path get the correct suffix.
			ext := meta.ExtHint
			if ext == "" && meta.ContentType != "" {
				ext = httputil.ExtFromContentType(meta.ContentType)
			}
			if ext != "" {
				base := strings.TrimSuffix(a.Filename, path.Ext(a.Filename))
				a.Filename = base + ext
			}
			if meta.ContentType != "" {
				ct := httputil.AssetTypeFromContentType(meta.ContentType)
				switch ct {
				case "image":
					a.Type = AssetTypeImage
				case "video":
					a.Type = AssetTypeVideo
				default:
					a.Type = AssetTypeOther
				}
			} else if ext != "" {
				a.Type = assetTypeFromName(a.Filename)
			}
		}
	}
}
