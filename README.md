# gh-diet-kit

Providing commands for inspecting dangling git objects on the remote and detecting or estimating storage savings from Git LFS migration.

## Installation

```sh
gh extension install srz-zumix/gh-diet-kit
```

## Shell Completion

**Workaround Available!** While gh CLI doesn't natively support extension completion, we provide a patch script that enables it.

**Prerequisites:** Before setting up gh-diet-kit completion, ensure gh CLI completion is configured for your shell. See [gh completion documentation](https://cli.github.com/manual/gh_completion) for setup instructions.

For detailed installation instructions and setup for each shell, see the [Shell Completion Guide](https://github.com/srz-zumix/go-gh-extension/blob/main/docs/shell-completion.md).

## Agent Skills

gh-diet-kit bundles agent skills for AI. Use the `skills` subcommand to install and manage them.

```sh
gh diet-kit skills [subcommand] [args...]
```

For details, see [Songmu/skillsmith](https://github.com/Songmu/skillsmith).

### Flags

| Flag | Shorthand | Description |
| ------ | ----------- | ------------- |
| `--log-level` | `-L` | Set log level (e.g. `debug`, `info`, `warn`, `error`) |
| `--read-only` | | Run in read-only mode (prevent write operations) |
| `--version` | | Print the version number |
| `--help` | `-h` | Show help |

### Commands

#### completion

Generate shell completion scripts for `bash`, `zsh`, `fish`, or `powershell`.

```sh
gh diet-kit completion [bash|zsh|fish|powershell]
```

#### dangling blobs

List blobs that are referenced only by commits from squash or rebase merged pull requests, commits dropped by force-pushes on PR head branches, or commits from closed unmerged pull requests, and are not reachable from any normal branch or tag ref on the remote. All detection methods are enabled by default.

**Note:** Git blobs are content-addressed. A blob introduced by a dangling commit may also appear in a live branch tree via identical file content (e.g. `package-lock.json`, generated files). Without a local git clone this cannot be detected efficiently via the GitHub API, so results may contain false positives. Use `--reachability-check local-object` (after running `git fetch --all --tags`) to filter out blobs that are still reachable from any local ref. Note: `git fetch --all` alone does not fetch tags that are not reachable from any branch, so commits reachable only from such tags may be misreported.

Output fields: `SHA`, `PATH`, `SIZE`, `COMMIT_SHA`, `PR_NUMBER`, `PR_URL`. In JSON, `SIZE` is omitted when blob sizes are unavailable.

```sh
gh diet-kit dangling blobs [flags]
```

| Flag | Shorthand | Default | Description |
| ------ | ----------- | ------- | ------------- |
| `--clear-cache` | | `false` | Clear the per-PR and commit blob cache before running, then use cache normally |
| `--clear-git-cache` | | `false` | Clear the git bare clone cache and re-clone before running |
| `--concurrency` | | `0` | Maximum number of concurrent GitHub API calls per PR for commit blob fetches (`<=0` uses the package default of 5) |
| `--format` | | table | Output format: `json` |
| `--jq` | `-q` | | Filter JSON output using a jq expression |
| `--limit` | | unlimited | Maximum number of closed PRs to inspect (ignored when `--pr` is specified) |
| `--no-cache` | | `false` | Disable per-PR result cache; always re-process all PRs (does not clear existing cache entries) |
| `--no-closed` | | `false` | Disable closed unmerged PR blob detection. Previously cached data for this scope is preserved in the cache when this flag is set. |
| `--no-force-push` | | `false` | Disable force-push dropped commit blob detection. Previously cached data for this scope is preserved in the cache when this flag is set. |
| `--no-squash-merge` | | `false` | Disable squash/rebase merged PR blob detection. Previously cached data for this scope is preserved in the cache when this flag is set. |
| `--order` | | `asc` | Sort order: `asc` or `desc` |
| `--pr` | | all closed PRs | PR numbers to inspect (comma-separated or repeated, e.g. `--pr 1,2` or `--pr 1 --pr 2`) |
| `--reachability-check` | | `none` | Filter out blobs reachable from a local ref (requires `git fetch --all --tags`): `none`, `local-object` |
| `--repo` | `-R` | current repository | Repository in `[HOST/]OWNER/REPO` format |
| `--sort` | | | Sort by field: `size`, `path`, `pr_number` |
| `--strict-errors` | | `false` | Fail immediately on any API or git error instead of logging and continuing |
| `--template` | `-t` | | Format JSON output using a Go template |

#### dangling branches

List unprotected branches that have no associated pull request (open, closed, or merged), and calculate the total size of blobs introduced by commits unique to each branch. The default branch and all protected branches are always excluded from results.

A commit is considered unique to a branch when it is not present in any other no-PR branch's commit history (commits ahead of the default branch). Commits shared with branches that have an associated pull request are not considered because those branches are excluded from the scan. `UNIQUE_SIZE` is the sum of blob sizes from the diffs of those unique commits, with blob SHAs deduplicated across commits — an approximation of the space that could be freed by deleting the branch.

Output fields: `BRANCH`, `COMMIT_SHA`, `AHEAD_COUNT`, `AUTHOR`, `UNIQUE_SIZE`.

```sh
gh diet-kit dangling branches [flags]
```

| Flag | Shorthand | Default | Description |
| ------ | ----------- | ------- | ------------- |
| `--clear-cache` | | | Clear the cached branch analysis data before running |
| `--format` | | table | Output format: `json` |
| `--jq` | `-q` | | Filter JSON output using a jq expression |
| `--max-branches` | | | Limit the number of no-PR branches for which blob sizes are computed (0 = unlimited) |
| `--max-commits` | | | Limit the number of unique commits fetched per branch for blob size computation (0 = unlimited) |
| `--no-blob-size` | | | Skip blob size computation; `UNIQUE_SIZE` will be empty in output |
| `--no-cache` | | | Run without using cached branch analysis data |
| `--order` | | `asc` | Sort order: `asc` or `desc` |
| `--repo` | `-R` | current repository | Repository in `[HOST/]OWNER/REPO` format |
| `--sort` | | | Sort by field: `branch`, `ahead_count`, `unique_size` |
| `--template` | `-t` | | Format JSON output using a Go template |

#### dangling commits

List commits that originate from squash or rebase merged pull requests, commits dropped by force-pushes on PR head branches, or commits from closed unmerged pull requests, and are not reachable from any normal branch or tag ref on the remote. All detection methods are enabled by default.

Output fields: `SHA`, `PR_NUMBER`, `PR_URL`, `SIZE`, `MESSAGE`. `SIZE` is the sum of sizes of unique blobs added or modified in the commit diff. In JSON, `SIZE` is omitted when blob sizes are unavailable.

```sh
gh diet-kit dangling commits [flags]
```

| Flag | Shorthand | Default | Description |
| ------ | ----------- | ------- | ------------- |
| `--clear-cache` | | `false` | Clear the per-PR and commit blob cache before running, then use cache normally |
| `--clear-git-cache` | | `false` | Clear the git bare clone cache and re-clone before running |
| `--concurrency` | | `0` | Maximum number of concurrent GitHub API calls per PR for commit blob fetches (`<=0` uses the package default of 5) |
| `--format` | | table | Output format: `json` |
| `--jq` | `-q` | | Filter JSON output using a jq expression |
| `--limit` | | unlimited | Maximum number of closed PRs to inspect (ignored when `--pr` is specified) |
| `--no-cache` | | `false` | Disable per-PR result cache; always re-process all PRs (does not clear existing cache entries) |
| `--no-closed` | | `false` | Disable closed unmerged PR detection. Previously cached data for this scope is preserved in the cache when this flag is set. |
| `--no-force-push` | | `false` | Disable force-push dropped commit detection. Previously cached data for this scope is preserved in the cache when this flag is set. |
| `--no-squash-merge` | | `false` | Disable squash/rebase merged PR commit detection. Previously cached data for this scope is preserved in the cache when this flag is set. |
| `--order` | | `asc` | Sort order: `asc` or `desc` |
| `--pr` | | all closed PRs | PR numbers to inspect (comma-separated or repeated, e.g. `--pr 1,2` or `--pr 1 --pr 2`) |
| `--reachability-check` | | `none` | Verify candidates are truly unreachable: `none`, `default-branch`, `branches`, `refs`, `local-object`, `local-refs` |
| `--repo` | `-R` | current repository | Repository in `[HOST/]OWNER/REPO` format |
| `--sort` | | | Sort by field: `size`, `pr_number` |
| `--strict-errors` | | `false` | Fail immediately on any API or git error instead of logging and continuing |
| `--template` | `-t` | | Format JSON output using a Go template |

#### dangling local

List commits that are not reachable from any local branch or tag ref but exist on the remote GitHub repository.

Locally dangling commits can originate from rebasing, amending, fetching now-deleted remote branches, or fetching pull request refs (`refs/pull/*/head`). When those objects have not been garbage-collected on the remote they remain accessible via the GitHub API.

Must be run inside a local git clone. By default reflog entries are included in reachability analysis; use `--no-reflogs` to ignore them.

Output fields: `SHA`, `MESSAGE`. In JSON, `pr_number`, `pr_url`, and `size` fields are omitted.

```sh
gh diet-kit dangling local [flags]
```

| Flag | Shorthand | Default | Description |
| ------ | ----------- | ------- | ------------- |
| `--no-reflogs` | | `false` | Ignore reflog entries when determining local reachability |
| `--repo` | `-R` | current repository | Repository in `[HOST/]OWNER/REPO` format |
| `--format` | | table | Output format: `json` |
| `--jq` | `-q` | | Filter JSON output using a jq expression |
| `--template` | `-t` | | Format JSON output using a Go template |

#### lfs detect

Detect files in the repository whose size exceeds a threshold and are not currently stored as Git LFS objects. Files properly tracked by LFS appear as small pointer files (~134 bytes) in the git tree and are therefore not reported.

The minimum allowed threshold is 135 bytes (one byte above the LFS pointer size). Values at or below the pointer size would cause genuine LFS pointer blobs to be reported as candidates.

Output fields: `PATH`, `SIZE`, `SHA`.

```sh
gh diet-kit lfs detect [flags]
```

| Flag | Shorthand | Default | Description |
| ------ | ----------- | ------- | ------------- |
| `--format` | | table | Output format: `json` |
| `--jq` | `-q` | | Filter JSON output using a jq expression |
| `--order` | | `asc` | Sort order: `asc` or `desc` |
| `--ref` | | repository default branch | Branch, tag, or commit SHA to inspect |
| `--repo` | `-R` | current repository | Repository in `[HOST/]OWNER/REPO` format |
| `--sort` | | | Sort by field: `size`, `path` |
| `--template` | `-t` | | Format JSON output using a Go template |
| `--threshold` | | `10MB` | Minimum file size to report as an LFS candidate (minimum: 135 bytes; e.g. `50MB`, `1GB`, `10000000`) |

#### lfs estimate

Estimate how much git object storage would be freed by migrating large files to Git LFS.

When `path` arguments are given, only those specific files are estimated regardless of `--threshold`. When no paths are given, the entire repository tree is scanned and files exceeding `--threshold` are reported.

For each candidate the estimated saving is `estimated_total_size - (lfs_pointer_size × version_count)` where `lfs_pointer_size ≈ 134` bytes. By default only the current tree is inspected (`version_count = 1`). Use `--scan-commits` to count historic versions; the estimated total size is approximated as `current_size × version_count`.

Default table columns (without `--scan-commits`): `PATH`, `CURRENT_SIZE`, `ESTIMATED_SAVING`

Default table columns (with `--scan-commits`): `PATH`, `CURRENT_SIZE`, `VERSIONS`, `ESTIMATED_TOTAL_SIZE`, `ESTIMATED_SAVING`

When `--format json` is used, the exported object includes JSON fields for the estimate data, including `path`, `current_size`, `estimated_saving`, `sha`, and `version_count`. With `--scan-commits`, the JSON output also includes `estimated_total_size`.

```sh
gh diet-kit lfs estimate [path...] [flags]
```

| Flag | Shorthand | Default | Description |
| ------ | ----------- | ------- | ------------- |
| `--format` | | table | Output format: `json` |
| `--jq` | `-q` | | Filter JSON output using a jq expression |
| `--order` | | `asc` | Sort order: `asc` or `desc` |
| `--ref` | | repository default branch | Branch, tag, or commit SHA to inspect |
| `--repo` | `-R` | current repository | Repository in `[HOST/]OWNER/REPO` format |
| `--scan-commits` | | `0` | Scan up to N commits per file to count historic versions (`0` = current tree only, negative = all commits) |
| `--sort` | | | Sort by field: `saving`, `size`, `path`, `versions` |
| `--template` | `-t` | | Format JSON output using a Go template |
| `--threshold` | | `10MB` | Minimum file size to include in the estimate; must be at least 135 bytes (ignored when path arguments are given) |

#### pr assets dump

Download media assets (images and videos) embedded in pull request bodies, issue comments, and review comments to a local directory, and write a `metadata.json` file that records the source repository, PR numbers, asset locations, and original URLs.

On subsequent runs, unchanged PRs (by `updated_at` timestamp) are skipped and already-downloaded files are not re-fetched. Use `--overwrite` to force a full re-download regardless.

```sh
gh diet-kit pr assets dump [flags]
```

| Flag | Shorthand | Default | Description |
| ------ | ----------- | ------- | ------------- |
| `--max-prs` | | `0` | Maximum number of PRs to fetch when `--pr` is not specified (`0` = unlimited) |
| `--metadata-file` | | `<output-dir>/metadata.json` | Path to write the metadata JSON file |
| `--no-file-size` | | `false` | Skip the HEAD request used to record asset file sizes in the metadata |
| `--output-dir` | | `./pr-assets` | Directory to download asset files into |
| `--overwrite` | | `false` | Re-download all assets and overwrite existing files, skipping repository and timestamp checks |
| `--pr` | | all PRs | PR numbers to scan (repeatable, e.g. `--pr 1 --pr 2`) |
| `--repo` | `-R` | current repository | Repository in `[HOST/]OWNER/REPO` format |
| `--state` | | `all` | Filter pull requests by state: `all`, `open`, `closed` |

#### pr assets list

Scan pull request bodies, issue comments, and review comments for GitHub-hosted media assets (images and videos) and print a summary table. Detected URL patterns include `user-images.githubusercontent.com`, `private-user-images.githubusercontent.com`, `github.com/user-attachments/assets/...`, and `github.com/<owner>/<repo>/assets/...`.

Output fields: `PR_NUMBER`, `LOCATION`, `LOCATION_ID`, `TYPE`, `FILENAME`, `FILE_SIZE`, `ASSET_URL`.

```sh
gh diet-kit pr assets list [flags]
```

| Flag | Shorthand | Default | Description |
| ------ | ----------- | ------- | ------------- |
| `--fields` | | all default fields | Comma-separated list of output fields to display |
| `--format` | | table | Output format: `json` |
| `--jq` | `-q` | | Filter JSON output using a jq expression |
| `--max-prs` | | `0` | Maximum number of PRs to fetch when `--pr` is not specified (`0` = unlimited) |
| `--no-file-size` | | `false` | Skip the HEAD request used to determine asset file sizes |
| `--pr` | | all PRs | PR numbers to scan (repeatable, e.g. `--pr 1 --pr 2`) |
| `--repo` | `-R` | current repository | Repository in `[HOST/]OWNER/REPO` format |
| `--state` | | `all` | Filter pull requests by state: `all`, `open`, `closed` |
| `--template` | `-t` | | Format JSON output using a Go template |

#### pr assets restore

Read the `metadata.json` produced by `pr assets dump`, upload each local asset file to the destination repository using Playwright browser automation, and replace the old source asset URLs with the new destination CDN URLs in PR bodies, issue comments, and review comments.

On the first run a browser window is opened so you can log in to GitHub interactively. The session is persisted to the `--browser-state` file for headless operation on subsequent runs.

```sh
gh diet-kit pr assets restore [flags]
```

| Flag | Shorthand | Default | Description |
| ------ | ----------- | ------- | ------------- |
| `--browser-state` | | `<user-config-dir>/gh-diet-kit/playwright-state.json` | Path to the Playwright browser state file for session persistence |
| `--clear-cache` | | `false` | Delete the saved browser session after the restore completes |
| `--clear-cache-only` | | `false` | Delete the saved browser session and exit without restoring |
| `--dryrun` | `-n` | `false` | Preview uploads and URL replacements without making any changes |
| `--headed` | | `false` | Run browser in headed (visible) mode even when a saved session exists |
| `--input-dir` | | `./pr-assets` | Directory containing the downloaded asset files |
| `--metadata-file` | | `<input-dir>/metadata.json` | Path to the metadata JSON file |
| `--pr` | | all PRs | PR numbers to restore (repeatable) |
| `--repo` | `-R` | current repository | Destination repository in `[HOST/]OWNER/REPO` format |

#### tree detect

Analyse the git tree structure of a repository and report directories whose direct entry count meets or exceeds a threshold.

Git stores one tree object per directory per commit. A directory with many direct entries produces a large tree object, and a deep or wide hierarchy multiplies the number of tree objects written on every commit. This command helps identify which directories are the primary contributors to tree object bloat.

For each directory the following fields are reported:

- `ENTRY_COUNT` – number of direct children (blobs + sub-trees)
- `TOTAL_FILES` – total number of blob entries reachable from this directory (recursive)
- `EST_TREE_SIZE` – estimated byte size of the git tree object (28 bytes fixed overhead per entry + base name length)
- `DEPTH` – nesting level (0 = repository root)

A summary line with total directory count, total file count, total estimated tree object size, and maximum depth is printed after the table.

Output fields: `PATH`, `DEPTH`, `ENTRY_COUNT`, `TOTAL_FILES`, `EST_TREE_SIZE`.

```sh
gh diet-kit tree detect [flags]
```

| Flag | Shorthand | Default | Description |
| ------ | ----------- | ------- | ------------- |
| `--format` | | table | Output format: `json` |
| `--jq` | `-q` | | Filter JSON output using a jq expression |
| `--order` | | `asc` | Sort order: `asc` or `desc` |
| `--ref` | | repository default branch | Branch, tag, or commit SHA to inspect |
| `--repo` | `-R` | current repository | Repository in `[HOST/]OWNER/REPO` format |
| `--sort` | | | Sort by field: `entry-count`, `total-files`, `est-size`, `depth`, `path` |
| `--template` | `-t` | | Format JSON output using a Go template |
| `--threshold` | | `1` | Minimum number of direct entries in a directory to report (must be >= 1) |
