package dangling

import (
	"context"
	"fmt"

	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/srz-zumix/gh-diet-kit/pkg/dangling"
	"github.com/srz-zumix/go-gh-extension/pkg/gitutil"
	"github.com/srz-zumix/go-gh-extension/pkg/parser"
)

// NewLocalCmd returns the cobra.Command for the dangling local subcommand.
// It lists commits that are not reachable from any local ref but exist on the
// remote GitHub repository.
func NewLocalCmd() *cobra.Command {
	var repoFlag string
	var noReflogsFlag bool
	var exporter cmdutil.Exporter

	cmd := &cobra.Command{
		Use:   "local",
		Short: "List locally dangling commits that exist on the remote",
		Long: `List commits that are not reachable from any local branch or tag ref
but exist on the remote GitHub repository.

Locally dangling commits can originate from operations such as rebasing,
amending, fetching now-deleted remote branches, or fetching pull request refs
(refs/pull/*/head). When those objects have not been garbage-collected on the
remote they are still accessible via the GitHub API even though no branch or
tag points to them.

By default reflog entries are included in local reachability analysis, which
means commits still referenced by the reflog are not reported. Pass
--no-reflogs to ignore reflogs and surface those additional candidates.

The command must be run inside a local git clone. --repo overrides which
remote repository is queried; without it the repository is auto-detected from
the git remote.

Output fields: SHA, MESSAGE`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			repo, err := parser.Repository(parser.RepositoryInput(repoFlag))
			if err != nil {
				return fmt.Errorf("failed to determine repository: %w", err)
			}

			g, err := dangling.NewGitHubClientWithRepo(repo)
			if err != nil {
				return fmt.Errorf("failed to create GitHub client: %w", err)
			}

			shas, err := gitutil.ListUnreachableCommits(ctx, noReflogsFlag)
			if err != nil {
				return fmt.Errorf("failed to list local dangling commits: %w", err)
			}

			commits, err := dangling.FindLocalDanglingCommitsOnRemote(ctx, g, repo, shas)
			if err != nil {
				return fmt.Errorf("failed to check dangling commits on remote: %w", err)
			}

			r := dangling.NewRenderer(exporter)
			return r.RenderDanglingCommits(commits, []string{"SHA", "MESSAGE"})
		},
	}

	f := cmd.Flags()
	f.StringVarP(&repoFlag, "repo", "R", "", "Repository in \"[HOST/]OWNER/REPO\" format (default: current repository)")
	f.BoolVar(&noReflogsFlag, "no-reflogs", false, "Ignore reflog entries when determining local reachability")
	cmdutil.AddFormatFlags(cmd, &exporter)
	return cmd
}
