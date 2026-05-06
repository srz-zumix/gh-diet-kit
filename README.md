# gh-diet-kit

A slim GitHub CLI extension based on [gh-team-kit](https://github.com/srz-zumix/gh-team-kit), providing only the essential core commands: `root`, `completion`, and `skills`, plus commands for inspecting dangling git objects on the remote.

## Installation

```sh
gh extension install srz-zumix/gh-diet-kit
```

## Usage

```sh
gh diet-kit [flags]
gh diet-kit [command]
```

### Flags

| Flag | Shorthand | Description |
|------|-----------|-------------|
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

Output fields: `SHA`, `PATH`, `SIZE`, `COMMIT_SHA`, `PR_NUMBER`, `PR_URL`.

```sh
gh diet-kit dangling blobs [flags]
```

| Flag | Shorthand | Default | Description |
|------|-----------|---------|-------------|
| `--clear-cache` | | `false` | Clear the per-PR and commit blob cache before running, then use cache normally |
| `--clear-git-cache` | | `false` | Clear the git bare clone cache and re-clone before running |
| `--format` | | table | Output format: `json` |
| `--jq` | `-q` | | Filter JSON output using a jq expression |
| `--limit` | | unlimited | Maximum number of closed PRs to inspect (ignored when `--pr` is specified) |
| `--no-cache` | | `false` | Disable per-PR result cache; always re-process all PRs (does not clear existing cache entries) |
| `--no-closed` | | `false` | Disable closed unmerged PR blob detection |
| `--no-force-push` | | `false` | Disable force-push dropped commit blob detection |
| `--no-squash-merge` | | `false` | Disable squash/rebase merged PR blob detection |
| `--order` | | `asc` | Sort order: `asc` or `desc` |
| `--pr` | | all closed PRs | PR numbers to inspect (comma-separated or repeated, e.g. `--pr 1,2` or `--pr 1 --pr 2`) |
| `--reachability-check` | | `none` | Filter out blobs reachable from a local ref (requires `git fetch --all --tags`): `none`, `local-object` |
| `--repo` | `-R` | current repository | Repository in `[HOST/]OWNER/REPO` format |
| `--sort` | | | Sort by field: `size`, `path`, `pr_number` |
| `--strict-errors` | | `false` | Fail immediately on any API or git error instead of logging and continuing |
| `--template` | `-t` | | Format JSON output using a Go template |

#### dangling commits

List commits that originate from squash or rebase merged pull requests, commits dropped by force-pushes on PR head branches, or commits from closed unmerged pull requests, and are not reachable from any normal branch or tag ref on the remote. All detection methods are enabled by default.

Output fields: `SHA`, `PR_NUMBER`, `PR_URL`, `SIZE`, `MESSAGE`. `SIZE` is the sum of sizes of unique blobs added or modified in the commit diff.

```sh
gh diet-kit dangling commits [flags]
```

| Flag | Shorthand | Default | Description |
|------|-----------|---------|-------------|
| `--clear-cache` | | `false` | Clear the per-PR and commit blob cache before running, then use cache normally |
| `--clear-git-cache` | | `false` | Clear the git bare clone cache and re-clone before running |
| `--format` | | table | Output format: `json` |
| `--jq` | `-q` | | Filter JSON output using a jq expression |
| `--limit` | | unlimited | Maximum number of closed PRs to inspect (ignored when `--pr` is specified) |
| `--no-cache` | | `false` | Disable per-PR result cache; always re-process all PRs (does not clear existing cache entries) |
| `--no-closed` | | `false` | Disable closed unmerged PR detection |
| `--no-force-push` | | `false` | Disable force-push dropped commit detection |
| `--no-squash-merge` | | `false` | Disable squash/rebase merged PR commit detection |
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

Output fields: `SHA`, `MESSAGE`.

```sh
gh diet-kit dangling local [flags]
```

| Flag | Shorthand | Default | Description |
|------|-----------|---------|-------------|
| `--no-reflogs` | | `false` | Ignore reflog entries when determining local reachability |
| `--repo` | `-R` | current repository | Repository in `[HOST/]OWNER/REPO` format |
| `--format` | | table | Output format: `json` |
| `--jq` | `-q` | | Filter JSON output using a jq expression |
| `--template` | `-t` | | Format JSON output using a Go template |

#### skills

Show available skills documentation embedded in the extension.

```sh
gh diet-kit skills
```
