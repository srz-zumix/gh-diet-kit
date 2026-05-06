---
name: gh-diet-kit
description: gh-diet-kit is a slim GitHub CLI extension based on gh-team-kit. It provides core extension utilities including shell completion, skills documentation, and commands to find dangling git objects (commits and blobs) on a remote GitHub repository. Use when you need a minimal GitHub CLI extension scaffold or when inspecting objects not reachable from normal branch/tag refs.
license: MIT
compatibility:
  - gh
commands:
  - name: gh diet-kit
    description: Root command for the gh-diet-kit extension.
    usage: gh diet-kit [flags]
    flags:
      - name: --log-level
        shorthand: -L
        description: Set log level (e.g. debug, info, warn, error)
      - name: --read-only
        description: Run in read-only mode (prevent write operations)
      - name: --version
        description: Print the version number
      - name: --help
        shorthand: -h
        description: Show help

  - name: gh diet-kit completion
    description: Generate shell completion scripts for bash, zsh, fish, or PowerShell.
    usage: gh diet-kit completion [bash|zsh|fish|powershell]

  - name: gh diet-kit dangling blobs
    description: List blobs referenced only by commits from squash or rebase merged PRs, commits dropped by force-pushes on PR head branches, or commits from closed unmerged PRs, that are not reachable from any normal branch or tag ref. All detection methods are enabled by default. Outputs table (default) or JSON with fields SHA, PATH, SIZE, COMMIT_SHA, PR_NUMBER, PR_URL. Supports sorting by size, path, or pr_number.
    usage: gh diet-kit dangling blobs [flags]
    flags:
      - name: --limit
        description: Maximum number of closed PRs to inspect (default: unlimited, ignored when --pr is specified)
      - name: --no-closed
        description: Disable closed unmerged PR blob detection (default: false)
      - name: --no-force-push
        description: Disable force-push dropped commit blob detection (default: false)
      - name: --no-squash-merge
        description: Disable squash/rebase merged PR blob detection (default: false)
      - name: --order
        description: Sort order (asc or desc, default asc)
      - name: --pr
        description: PR numbers to inspect, comma-separated or repeated (default: all closed PRs)
      - name: --reachability-check
        description: "Filter out blobs reachable from a local ref (requires git fetch --all --tags). Options: none (no verification, default), local-object (local git object store). Default: none. Note: git fetch --all alone does not fetch tags unreachable from any branch."
      - name: --repo
        shorthand: -R
        description: Repository in "[HOST/]OWNER/REPO" format (default: current repository)
      - name: --sort
        description: Sort by field (size, path, pr_number)
      - name: --format
        description: Output format (json)
      - name: --jq
        shorthand: -q
        description: Filter JSON output using a jq expression
      - name: --template
        shorthand: -t
        description: Format JSON output using a Go template

  - name: gh diet-kit dangling commits
    description: List commits from squash or rebase merged PRs, commits dropped by force-pushes on PR head branches, or commits from closed unmerged PRs, that are not reachable from any normal branch or tag ref. All detection methods are enabled by default. Outputs table (default) or JSON with fields SHA, PR_NUMBER, PR_URL, SIZE (total size of unique added or modified blobs in the commit diff), MESSAGE.
    usage: gh diet-kit dangling commits [flags]
    flags:
      - name: --limit
        description: Maximum number of closed PRs to inspect (default: unlimited, ignored when --pr is specified)
      - name: --no-closed
        description: Disable closed unmerged PR detection (default: false)
      - name: --no-force-push
        description: Disable force-push dropped commit detection (default: false)
      - name: --no-squash-merge
        description: Disable squash/rebase merged PR commit detection (default: false)
      - name: --order
        description: Sort order (asc or desc, default asc)
      - name: --pr
        description: PR numbers to inspect, comma-separated or repeated (default: all closed PRs)
      - name: --reachability-check
        description: "Verify candidates are truly unreachable. Options: none (no verification, default), default-branch (check against default branch only), branches (all branches), refs (all refs), local-object (local git object store), local-refs (local refs). Default: none."
      - name: --repo
        shorthand: -R
        description: Repository in "[HOST/]OWNER/REPO" format (default: current repository)
      - name: --sort
        description: Sort by field (size, pr_number)
      - name: --format
        description: Output format (json)
      - name: --jq
        shorthand: -q
        description: Filter JSON output using a jq expression
      - name: --template
        shorthand: -t
        description: Format JSON output using a Go template

  - name: gh diet-kit dangling local
    description: List commits that are not reachable from any local branch or tag ref but exist on the remote GitHub repository. Must be run inside a local git clone. Outputs table (default) or JSON with fields SHA, MESSAGE.
    usage: gh diet-kit dangling local [flags]
    flags:
      - name: --no-reflogs
        description: Ignore reflog entries when determining local reachability (default false)
      - name: --repo
        shorthand: -R
        description: Repository in "[HOST/]OWNER/REPO" format (default: current repository)
      - name: --format
        description: Output format (json)
      - name: --jq
        shorthand: -q
        description: Filter JSON output using a jq expression
      - name: --template
        shorthand: -t
        description: Format JSON output using a Go template

  - name: gh diet-kit skills
    description: Show available skills documentation for gh-diet-kit.
    usage: gh diet-kit skills [flags]
---

# gh-diet-kit

A slim GitHub CLI extension based on gh-team-kit, providing only the essential core commands.

## CLI Structure

```
gh diet-kit
├── completion                  # Generate shell completion scripts (bash, zsh, fish, powershell)
├── dangling                    # Find git objects not reachable from normal refs
│   ├── blobs                   # List dangling blobs
│   ├── commits                 # List dangling commits
│   └── local                   # List local commits that exist on remote but are unreachable locally
└── skills                      # Show embedded skills documentation
```

## Commands

### `gh diet-kit`

Root command. Accepts `--version`, `--log-level` / `-L`, and `--read-only` flags.

### `gh diet-kit completion`

Generate shell completion scripts.

```sh
gh diet-kit completion bash
gh diet-kit completion zsh
gh diet-kit completion fish
gh diet-kit completion powershell
```

### `gh diet-kit dangling blobs`

List blobs that are referenced only by commits from squash or rebase merged pull requests, commits dropped by force-pushes on PR head branches, or commits from closed unmerged pull requests, and are not reachable from any normal branch or tag ref on the remote. All detection methods are enabled by default.

**Note:** Git blobs are content-addressed. A blob introduced by a dangling commit may also appear in a live branch tree via identical file content (e.g. `package-lock.json`, generated files). Without a local git clone this cannot be detected efficiently via the GitHub API, so results may contain false positives. Use `--reachability-check local-object` (after running `git fetch --all --tags`) to filter out blobs that are still reachable from any local ref. Note: `git fetch --all` alone does not fetch tags that are not reachable from any branch.

Output fields: `SHA`, `PATH`, `SIZE`, `COMMIT_SHA`, `PR_NUMBER`, `PR_URL`.

```sh
# Inspect up to 200 closed PRs
gh diet-kit dangling blobs -R owner/repo --limit 200
# Inspect specific PRs only
gh diet-kit dangling blobs -R owner/repo --pr 42,43
# Squash/rebase detection only, confirm with default-branch reachability check
gh diet-kit dangling blobs --no-closed --no-force-push --reachability-check default-branch
# Force-push detection only
gh diet-kit dangling blobs --no-closed --no-squash-merge
# All methods, output as JSON
gh diet-kit dangling blobs --format json | jq '.[] | .sha'
```

### `gh diet-kit dangling commits`

List commits from squash or rebase merged pull requests, commits dropped by force-pushes on PR head branches, or commits from closed unmerged pull requests, that are not reachable from any normal branch or tag ref. All detection methods are enabled by default.

Output fields: `SHA`, `PR_NUMBER`, `PR_URL`, `SIZE`, `MESSAGE`. `SIZE` is the total size of unique blobs added or modified in the commit diff (human-readable, e.g. `1.2 MB`).

```sh
# Inspect up to 200 closed PRs
gh diet-kit dangling commits -R owner/repo --limit 200
# Inspect specific PRs only
gh diet-kit dangling commits -R owner/repo --pr 42,43
# Force-push detection only, confirm with local ref reachability check
gh diet-kit dangling commits --no-closed --no-squash-merge --reachability-check local-refs
# Closed PR detection only
gh diet-kit dangling commits --no-squash-merge --no-force-push
# All methods, output as JSON
gh diet-kit dangling commits --format json | jq '.[] | .sha'
```

### `gh diet-kit dangling local`

List commits that are not reachable from any local branch or tag ref but exist on the remote GitHub repository.

Output fields: `SHA`, `MESSAGE`.

```sh
gh diet-kit dangling local
gh diet-kit dangling local --no-reflogs
gh diet-kit dangling local -R owner/repo --format json | jq '.[] | .sha'
```

### `gh diet-kit skills`

Show skills documentation embedded in the extension.

```sh
gh diet-kit skills
```
