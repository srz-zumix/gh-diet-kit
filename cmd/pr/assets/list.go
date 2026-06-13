package assets

import (
	"fmt"
	"net/http"

	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
	"github.com/srz-zumix/gh-diet-kit/pkg/pr/assets"
	"github.com/srz-zumix/go-gh-extension/pkg/gh"
	"github.com/srz-zumix/go-gh-extension/pkg/logger"
	"github.com/srz-zumix/go-gh-extension/pkg/parser"
)

// NewListCmd returns the cobra.Command for the pr assets list subcommand.
// It scans pull request bodies, issue comments, and review comments for embedded
// media assets (images and videos hosted on GitHub's CDN) and prints a table of results.
func NewListCmd() *cobra.Command {
	var repoFlag string
	var stateFlag string
	var prFlag []int
	var maxPRsFlag int
	var fieldsFlag []string
	var noFileSizeFlag bool
	var exporter cmdutil.Exporter

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List media assets embedded in pull request bodies and comments",
		Args:  cobra.NoArgs,
		Long: `Scan pull request bodies, issue comments, and review comments for
GitHub-hosted media assets (images and videos) and print a summary table.

Asset URLs from the following GitHub CDN patterns are detected:
  - https://user-images.githubusercontent.com/...
  - https://private-user-images.githubusercontent.com/...
  - https://github.com/user-attachments/assets/...
  - https://github.com/<owner>/<repo>/assets/...

Output fields: PR_NUMBER, LOCATION, LOCATION_ID, TYPE, FILENAME, FILE_SIZE, ASSET_URL`,
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

			var httpClient *http.Client
			if !noFileSizeFlag {
				httpClient = g.GetClient().Client()
			}

			opts := assets.AssetsOptions{
				State:      stateFlag,
				PRNumbers:  prFlag,
				MaxPRs:     maxPRsFlag,
				NoFileSize: noFileSizeFlag,
			}

			prAssets, err := assets.FindPRsWithAssets(ctx, g, repo, opts, httpClient)
			if err != nil {
				return fmt.Errorf("failed to find PR assets: %w", err)
			}

			r := assets.NewRenderer(exporter)
			if err := r.RenderPRAssets(prAssets, fieldsFlag); err != nil {
				return err
			}

			var totalSize int64
			for _, a := range prAssets {
				if a.FileSize >= 0 {
					totalSize += a.FileSize
				}
			}
			logger.Info("total", "assets", len(prAssets), "size", humanize.Bytes(uint64(totalSize)))
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVarP(&repoFlag, "repo", "R", "", "Repository in \"[HOST/]OWNER/REPO\" format (default: current repository)")
	cmdutil.StringEnumFlag(cmd, &stateFlag, "state", "", gh.PRStateAll, gh.PRStateValues, "Filter pull requests by state")
	f.IntSliceVar(&prFlag, "pr", nil, "PR numbers to scan (repeatable; default: all PRs)")
	f.IntVar(&maxPRsFlag, "max-prs", 0, "Maximum number of PRs to fetch when --pr is not specified (0 = unlimited)")
	f.StringSliceVar(&fieldsFlag, "fields", nil, "Comma-separated list of output fields (default: PR_NUMBER,LOCATION,LOCATION_ID,TYPE,FILENAME,FILE_SIZE,ASSET_URL)")
	f.BoolVar(&noFileSizeFlag, "no-file-size", false, "Skip the HEAD request used to determine asset file sizes")
	cmdutil.AddFormatFlags(cmd, &exporter)
	return cmd
}
