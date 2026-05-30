package assets

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
	"github.com/srz-zumix/gh-diet-kit/pkg/pr/assets"
	"github.com/srz-zumix/go-gh-extension/pkg/gh"
	"github.com/srz-zumix/go-gh-extension/pkg/httputil"
	"github.com/srz-zumix/go-gh-extension/pkg/ioutil"
	"github.com/srz-zumix/go-gh-extension/pkg/logger"
	"github.com/srz-zumix/go-gh-extension/pkg/parser"
)

// NewDumpCmd returns the cobra.Command for the pr assets dump subcommand.
// It detects media assets in pull requests, downloads them to a local directory,
// and writes a metadata.json file that can be used by the restore command.
func NewDumpCmd() *cobra.Command {
	var repoFlag string
	var stateFlag string
	var prFlag []int
	var maxPRsFlag int
	var outputDirFlag string
	var metadataFileFlag string
	var noFileSizeFlag bool
	var overwriteFlag bool

	cmd := &cobra.Command{
		Use:   "dump",
		Short: "Download PR media assets and write a metadata file for later restoration",
		Long: `Detect media assets embedded in pull request bodies, issue comments, and
review comments, download each asset file to a local directory, and write a
metadata.json file recording the source repository, PR numbers, locations, and
original URLs.

On subsequent runs against the same output directory, unchanged PRs (by
updated_at timestamp) are skipped and already-downloaded files are not
re-fetched. Use --overwrite to force a full re-download regardless.

The dump output can be used by the restore command to re-upload assets to a
migrated repository (e.g. after a gh-gei migration) and update the URLs in the
corresponding PR bodies and comments.

Example:
  gh pr assets dump -R owner/repo --output-dir ./pr-assets`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			repo, err := parser.Repository(parser.RepositoryInput(repoFlag))
			if err != nil {
				return fmt.Errorf("failed to determine repository: %w", err)
			}

			g, err := assets.NewGitHubClientWithRepo(repo)
			if err != nil {
				return fmt.Errorf("failed to create GitHub client: %w", err)
			}

			httpClient, err := api.NewHTTPClient(api.ClientOptions{Host: repo.Host})
			if err != nil {
				return fmt.Errorf("failed to create HTTP client: %w", err)
			}
			// downloadClient switches to http.DefaultTransport for non-GitHub hosts
			// (e.g. Azure Blob Storage on GHES) so that API-specific headers are not
			// sent to third-party storage backends during redirects.
			downloadClient := httputil.NewHostAwareClient(httpClient, repo.Host)

			opts := assets.AssetsOptions{
				State:      stateFlag,
				PRNumbers:  prFlag,
				MaxPRs:     maxPRsFlag,
				NoFileSize: noFileSizeFlag,
			}

			// Ensure output directory exists before resolving metaPath.
			if err := os.MkdirAll(outputDirFlag, 0o755); err != nil {
				return fmt.Errorf("failed to create output directory %q: %w", outputDirFlag, err)
			}

			metaPath := metadataFileFlag
			if metaPath == "" {
				metaPath = filepath.Join(outputDirFlag, "metadata.json")
			}

			sourceRepo := parser.GetRepositoryFullNameWithHost(repo)

			// Load existing metadata for incremental mode.
			var existingMeta *assets.DumpMetadata
			if !overwriteFlag {
				if _, statErr := os.Stat(metaPath); statErr == nil {
					existingMeta, err = assets.LoadMetadata(metaPath)
					if err != nil {
						return fmt.Errorf("failed to load existing metadata from %q: %w", metaPath, err)
					}
					if existingMeta.SourceRepo != sourceRepo {
						return fmt.Errorf("source repository mismatch: metadata has %q but current repository is %q", existingMeta.SourceRepo, sourceRepo)
					}
					logger.Info("incremental mode: loaded existing metadata", "source_repo", existingMeta.SourceRepo)
				}
			}

			// Build lookup maps from existing metadata.
			existingUpdatedAt := make(map[int]string)              // pr_number → updated_at
			existingAssetByURL := make(map[string]*assets.PRAsset) // asset_url → PRAsset
			if existingMeta != nil {
				for num, ts := range existingMeta.PRUpdatedAt {
					existingUpdatedAt[num] = ts
				}
				for _, a := range existingMeta.Assets {
					existingAssetByURL[a.AssetURL] = a
				}
			}

			patterns := gh.BuildAssetURLPatterns(repo.Host)

			prs, err := assets.FetchPRs(ctx, g, repo, opts)
			if err != nil {
				return fmt.Errorf("failed to fetch pull requests: %w", err)
			}

			logger.Info("processing pull requests", "count", len(prs))

			var allAssets []*assets.PRAsset
			newUpdatedAt := make(map[int]string)

		prLoop:
			for _, pr := range prs {
				// Stop processing if the context has been cancelled (e.g. Ctrl+C).
				if ctx.Err() != nil {
					logger.Info("interrupted, saving partial results")
					break
				}

				num := pr.GetNumber()
				updatedAt := pr.GetUpdatedAt().Format(time.RFC3339)
				newUpdatedAt[num] = updatedAt

				// Skip unchanged PRs in incremental mode.
				if !overwriteFlag && existingUpdatedAt[num] == updatedAt {
					logger.Info("skipping unchanged PR", "pr", num)
					// Carry over existing assets for this PR; re-download any whose
					// local file is missing from disk (e.g. deleted after a previous dump).
					for _, a := range existingMeta.Assets {
						if a.PRNumber != num {
							continue
						}
						localFile := a.LocalFile
						if localFile == "" {
							localFile = ioutil.SafeFilename(a.AssetURL, a.Filename)
						}
						destPath := filepath.Join(outputDirFlag, localFile)
						if _, statErr := os.Stat(destPath); statErr != nil {
							if dlErr := ioutil.DownloadFile(ctx, downloadClient, a.AssetURL, destPath); dlErr != nil {
								if ctx.Err() != nil {
									logger.Info("interrupted, saving partial results")
									allAssets = append(allAssets, a)
									break prLoop
								}
								logger.Warn("failed to re-download missing asset", "url", a.AssetURL, "error", dlErr)
							} else {
								logger.Info("re-downloaded missing asset", "file", destPath)
							}
						}
						allAssets = append(allAssets, a)
					}
					continue
				}

				logger.Info("scanning PR", "pr", num)
				prAssets := assets.ScanSinglePR(ctx, g, repo, pr, patterns, httpClient, noFileSizeFlag)

				for _, a := range prAssets {
					safeFile := ioutil.SafeFilename(a.AssetURL, a.Filename)
					destPath := filepath.Join(outputDirFlag, safeFile)

					// Skip download if file already exists on disk and asset is unchanged.
					// If the local file is missing (e.g. deleted after a previous dump),
					// fall through to re-download even when metadata has an entry.
					existing := existingAssetByURL[a.AssetURL]
					if !overwriteFlag && existing != nil {
						if _, statErr := os.Stat(destPath); statErr == nil {
							logger.Info("skipping existing asset", "file", destPath)
							a.LocalFile = safeFile
							if existing.FileSize != 0 {
								a.FileSize = existing.FileSize
							}
							if existing.Filename != "" {
								a.Filename = existing.Filename
							}
							allAssets = append(allAssets, a)
							continue
						}
					}

					if err := ioutil.DownloadFile(ctx, downloadClient, a.AssetURL, destPath); err != nil {
						if ctx.Err() != nil {
							logger.Info("interrupted, saving partial results")
							a.LocalFile = safeFile
							allAssets = append(allAssets, a)
							break prLoop
						}
						logger.Warn("failed to download asset", "url", a.AssetURL, "error", err)
					} else {
						logger.Info("downloaded", "file", destPath)
					}
					a.LocalFile = safeFile
					allAssets = append(allAssets, a)
				}
			}

			// Carry over PRUpdatedAt and assets for PRs that were not processed in
			// this run (e.g. when --pr limits the scope to specific PR numbers).
			// Without this, entries for unscanned PRs would be lost on every
			// partial dump.
			if existingMeta != nil {
				processedPRNums := make(map[int]bool, len(newUpdatedAt))
				for num := range newUpdatedAt {
					processedPRNums[num] = true
				}
				for num, ts := range existingMeta.PRUpdatedAt {
					if !processedPRNums[num] {
						newUpdatedAt[num] = ts
					}
				}
				for _, a := range existingMeta.Assets {
					if !processedPRNums[a.PRNumber] {
						// PRs outside this run's scope (e.g. excluded by --pr) are carried
						// over as-is without re-downloading, even if the local file is missing.
						allAssets = append(allAssets, a)
					}
				}
			}

			// Write metadata.json.
			meta := assets.DumpMetadata{
				SourceRepo:  sourceRepo,
				DumpedAt:    time.Now().UTC().Format(time.RFC3339),
				PRUpdatedAt: newUpdatedAt,
				Assets:      allAssets,
			}

			if err := assets.WriteMetadata(metaPath, meta); err != nil {
				return fmt.Errorf("failed to write metadata file: %w", err)
			}

			// Compute total size from all local files (new downloads + pre-existing).
			var totalSize int64
			for _, a := range allAssets {
				localFile := a.LocalFile
				if localFile == "" {
					continue
				}
				if info, statErr := os.Stat(filepath.Join(outputDirFlag, localFile)); statErr == nil {
					totalSize += info.Size()
				}
			}

			logger.Info("dump complete", "assets", len(allAssets), "dir", outputDirFlag, "total", humanize.Bytes(uint64(totalSize)))
			logger.Info("metadata written", "file", metaPath)
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVarP(&repoFlag, "repo", "R", "", "Repository in \"[HOST/]OWNER/REPO\" format (default: current repository)")
	f.StringVar(&stateFlag, "state", "all", "Filter pull requests by state: all, open, closed")
	f.IntSliceVar(&prFlag, "pr", nil, "PR numbers to scan (repeatable; default: all PRs)")
	f.IntVar(&maxPRsFlag, "max-prs", 0, "Maximum number of PRs to fetch when --pr is not specified (0 = unlimited)")
	f.StringVar(&outputDirFlag, "output-dir", "./pr-assets", "Directory to download asset files into")
	f.StringVar(&metadataFileFlag, "metadata-file", "", "Path for the metadata JSON file (default: <output-dir>/metadata.json)")
	f.BoolVar(&noFileSizeFlag, "no-file-size", false, "Skip the HEAD request used to record asset file sizes in the metadata")
	f.BoolVar(&overwriteFlag, "overwrite", false, "Re-download all assets and overwrite existing files, skipping repository and timestamp checks")
	return cmd
}
