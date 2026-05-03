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

Output fields: `SHA`, `PATH`, `SIZE`, `COMMIT_SHA`, `PR_NUMBER`, `PR_URL`.

```sh
gh diet-kit dangling blobs [flags]
```

| Flag | Shorthand | Default | Description |
|------|-----------|---------|-------------|
| `--local-default-branch` | | auto-detected | Remote-tracking ref for `--reachability-check=local-default` (e.g. `origin/main`) |
| `--limit` | | unlimited | Maximum number of closed PRs to inspect (ignored when `--pr` is specified) |
| `--no-closed` | | `false` | Disable closed unmerged PR blob detection |
| `--no-force-push` | | `false` | Disable force-push dropped commit blob detection |
| `--no-squash-merge` | | `false` | Disable squash/rebase merged PR blob detection |
| `--order` | | `asc` | Sort order: `asc` or `desc` |
| `--pr` | | all closed PRs | PR numbers to inspect (comma-separated or repeated, e.g. `--pr 1,2` or `--pr 1 --pr 2`) |
| `--reachability-check` | | `none` | Verify candidates are truly unreachable: `none`, `default-branch`, `all-branches`, `local-object`, `local-default`, `local-any` |
| `--repo` | `-R` | current repository | Repository in `[HOST/]OWNER/REPO` format |
| `--sort` | | | Sort by field: `size`, `path`, `pr_number` |
| `--format` | | table | Output format: `json` |
| `--jq` | `-q` | | Filter JSON output using a jq expression |
| `--template` | `-t` | | Format JSON output using a Go template |

#### dangling commits

List commits that originate from squash or rebase merged pull requests, commits dropped by force-pushes on PR head branches, or commits from closed unmerged pull requests, and are not reachable from any normal branch or tag ref on the remote. All detection methods are enabled by default.

Output fields: `SHA`, `PR_NUMBER`, `PR_URL`, `SIZE`, `MESSAGE`. `SIZE` is the total of all blob sizes in the commit tree.

```sh
gh diet-kit dangling commits [flags]
```

| Flag | Shorthand | Default | Description |
|------|-----------|---------|-------------|
| `--local-default-branch` | | auto-detected | Remote-tracking ref for `--reachability-check=local-default` (e.g. `origin/main`) |
| `--limit` | | unlimited | Maximum number of closed PRs to inspect (ignored when `--pr` is specified) |
| `--no-closed` | | `false` | Disable closed unmerged PR detection |
| `--no-force-push` | | `false` | Disable force-push dropped commit detection |
| `--no-squash-merge` | | `false` | Disable squash/rebase merged PR commit detection |
| `--order` | | `asc` | Sort order: `asc` or `desc` |
| `--pr` | | all closed PRs | PR numbers to inspect (comma-separated or repeated, e.g. `--pr 1,2` or `--pr 1 --pr 2`) |
| `--reachability-check` | | `none` | Verify candidates are truly unreachable: `none`, `default-branch`, `all-branches`, `local-object`, `local-default`, `local-any` |
| `--repo` | `-R` | current repository | Repository in `[HOST/]OWNER/REPO` format |
| `--sort` | | | Sort by field: `size`, `pr_number` |
| `--format` | | table | Output format: `json` |
| `--jq` | `-q` | | Filter JSON output using a jq expression |
| `--template` | `-t` | | Format JSON output using a Go template |

#### skills

Show available skills documentation embedded in the extension.

```sh
gh diet-kit skills
```
