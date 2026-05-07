---
name: gh-diet-kit
description: gh-diet-kit is a slim GitHub CLI extension based on gh-team-kit. It provides core extension utilities including shell completion, skills documentation, commands to find dangling git objects (commits and blobs) on a remote GitHub repository, and LFS commands to detect large files and estimate migration savings. Use when you need a minimal GitHub CLI extension scaffold or when inspecting objects not reachable from normal branch/tag refs.
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
    description: List blobs referenced only by commits from squash or rebase merged PRs, commits dropped by force-pushes on PR head branches, or commits from closed unmerged PRs, that are not reachable from any normal branch or tag ref. All detection methods are enabled by default. Outputs table (default) or JSON with fields SHA, PATH, SIZE, COMMIT_SHA, PR_NUMBER, PR_URL. In JSON, SIZE is omitted when blob sizes are unavailable. Supports sorting by size, path, or pr_number. Per-PR results are cached under the OS user cache directory (e.g. ~/.cache/gh-diet-kit/ on Linux) for resume support; use --no-cache to bypass. Use --clear-git-cache to clear and re-clone the git bare clone cache.
    usage: gh diet-kit dangling blobs [flags]
    flags:
      - name: --clear-cache
        description: Clear the per-PR and commit blob cache before running, then use cache normally (default: false)
      - name: --clear-git-cache
        description: Clear the git bare clone cache and re-clone before running (default: false)
      - name: --format
        description: Output format (json)
      - name: --jq
        shorthand: -q
        description: Filter JSON output using a jq expression
      - name: --limit
        description: Maximum number of closed PRs to inspect (default: unlimited, ignored when --pr is specified)
      - name: --no-cache
        description: Disable per-PR result cache; always re-process all PRs (does not clear existing cache entries) (default: false)
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
        description: "Filter out blobs reachable from a local ref (requires git fetch --all --tags). Options: none (no verification, default), local-object (local git object store). Default: none. Note: git fetch --all alone does not fetch tags unreachable from any branch. When --repo is specified, a bare clone cache is auto-created under the OS user cache directory (e.g. ~/.cache/gh-diet-kit/ on Linux)."
      - name: --repo
        shorthand: -R
        description: Repository in "[HOST/]OWNER/REPO" format (default: current repository)
      - name: --sort
        description: Sort by field (size, path, pr_number)
      - name: --strict-errors
        description: Fail immediately on any API or git error instead of logging and continuing (default: false)
      - name: --template
        shorthand: -t
        description: Format JSON output using a Go template

  - name: gh diet-kit dangling commits
    description: List commits from squash or rebase merged PRs, commits dropped by force-pushes on PR head branches, or commits from closed unmerged PRs, that are not reachable from any normal branch or tag ref. All detection methods are enabled by default. Outputs table (default) or JSON with fields SHA, PR_NUMBER, PR_URL, SIZE (total size of unique added or modified blobs in the commit diff, human-readable), MESSAGE. In JSON, SIZE is omitted when blob sizes are unavailable. Per-PR results are cached under the OS user cache directory (e.g. ~/.cache/gh-diet-kit/ on Linux) for resume support; use --no-cache to bypass. Use --clear-git-cache to clear and re-clone the git bare clone cache.
    usage: gh diet-kit dangling commits [flags]
    flags:
      - name: --clear-cache
        description: Clear the per-PR and commit blob cache before running, then use cache normally (default: false)
      - name: --clear-git-cache
        description: Clear the git bare clone cache and re-clone before running (default: false)
      - name: --format
        description: Output format (json)
      - name: --jq
        shorthand: -q
        description: Filter JSON output using a jq expression
      - name: --limit
        description: Maximum number of closed PRs to inspect (default: unlimited, ignored when --pr is specified)
      - name: --no-cache
        description: Disable per-PR result cache; always re-process all PRs (does not clear existing cache entries) (default: false)
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
        description: "Verify candidates are truly unreachable. Options: none (no verification, default), default-branch (check against default branch only), branches (all branches), refs (all refs), local-object (local git object store), local-refs (local refs). Default: none. When --repo is specified, a bare clone cache is auto-created under the OS user cache directory (e.g. ~/.cache/gh-diet-kit/ on Linux)."
      - name: --repo
        shorthand: -R
        description: Repository in "[HOST/]OWNER/REPO" format (default: current repository)
      - name: --sort
        description: Sort by field (size, pr_number)
      - name: --strict-errors
        description: Fail immediately on any API or git error instead of logging and continuing (default: false)
      - name: --template
        shorthand: -t
        description: Format JSON output using a Go template

  - name: gh diet-kit dangling local
    description: List commits that are not reachable from any local branch or tag ref but exist on the remote GitHub repository. Must be run inside a local git clone. Outputs table (default) or JSON with fields SHA, MESSAGE. In JSON, pr_number, pr_url, and size fields are omitted.
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

  - name: gh diet-kit lfs detect
    description: Detect files in the repository whose size exceeds a threshold and are not currently stored as Git LFS objects. Outputs table (default) or JSON with fields PATH, SIZE, SHA. Default threshold is 10MB. The minimum allowed threshold is 135 bytes (one byte above the LFS pointer size of 134 bytes); values at or below the pointer size would cause genuine LFS pointer blobs to be reported as candidates.
    usage: gh diet-kit lfs detect [flags]
    flags:
      - name: --format
        description: Output format (json)
      - name: --jq
        shorthand: -q
        description: Filter JSON output using a jq expression
      - name: --order
        description: Sort order (asc or desc, default asc)
      - name: --ref
        description: Branch, tag, or commit SHA to inspect (default: repository default branch)
      - name: --repo
        shorthand: -R
        description: Repository in "[HOST/]OWNER/REPO" format (default: current repository)
      - name: --sort
        description: Sort by field (size, path)
      - name: --template
        shorthand: -t
        description: Format JSON output using a Go template
      - name: --threshold
        description: Minimum file size to report as an LFS candidate; must be at least 135 bytes (e.g. 50MB, 1GB, 10000000; default 10MB)

  - name: gh diet-kit tree detect
    description: Analyse the git tree structure of a repository and report directories whose direct entry count meets or exceeds a threshold. Outputs table (default) or JSON with fields PATH, DEPTH, ENTRY_COUNT, TOTAL_FILES, EST_TREE_SIZE. A summary line (total dirs, total files, total estimated tree object size, max depth) is printed after the table. EST_TREE_SIZE is estimated as 28 bytes of fixed overhead per entry plus the base name length.
    usage: gh diet-kit tree detect [flags]
    flags:
      - name: --format
        description: Output format (json)
      - name: --jq
        shorthand: -q
        description: Filter JSON output using a jq expression
      - name: --order
        description: Sort order (asc or desc, default asc)
      - name: --ref
        description: Branch, tag, or commit SHA to inspect (default: repository default branch)
      - name: --repo
        shorthand: -R
        description: Repository in "[HOST/]OWNER/REPO" format (default: current repository)
      - name: --sort
        description: Sort by field (entry-count, total-files, est-size, depth, path)
      - name: --template
        shorthand: -t
        description: Format JSON output using a Go template
      - name: --threshold
        description: Minimum number of direct entries in a directory to report; must be >= 1 (default 1)

  - name: gh diet-kit lfs estimate
    description: Estimate how much git object storage would be freed by migrating large files to Git LFS. When path arguments are given, only those files are estimated regardless of --threshold. The default table output shows PATH, CURRENT_SIZE, and ESTIMATED_SAVING, and also shows VERSIONS and ESTIMATED_TOTAL_SIZE when --scan-commits is used. JSON or template output includes those fields plus per-estimate metadata such as sha and version_count, and exported output can also include a top-level summary object. Default threshold is 10MB.
    usage: gh diet-kit lfs estimate [path...] [flags]
    flags:
      - name: --format
        description: Output format (json)
      - name: --jq
        shorthand: -q
        description: Filter JSON output using a jq expression
      - name: --order
        description: Sort order (asc or desc, default asc)
      - name: --ref
        description: Branch, tag, or commit SHA to inspect (default: repository default branch)
      - name: --repo
        shorthand: -R
        description: Repository in "[HOST/]OWNER/REPO" format (default: current repository)
      - name: --scan-commits
        description: Scan up to N commits per file to count historic versions (0 = current tree only, negative = all commits; default 0)
      - name: --sort
        description: Sort by field (saving, size, path, versions)
      - name: --template
        shorthand: -t
        description: Format JSON output using a Go template
      - name: --threshold
        description: Minimum file size to include in the estimate; must be at least 135 bytes (ignored when path arguments are given; default 10MB)
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
├── lfs                         # Git LFS utilities
│   ├── detect                  # Detect large files that should be tracked by LFS
│   └── estimate                # Estimate storage savings from LFS migration
├── tree                        # Git tree structure analysis
│   └── detect                  # Detect directories with many entries bloating tree objects
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

Output fields: `SHA`, `PATH`, `SIZE`, `COMMIT_SHA`, `PR_NUMBER`, `PR_URL`. In JSON, `SIZE` is omitted when blob sizes are unavailable.

```sh
# Inspect up to 200 closed PRs
gh diet-kit dangling blobs -R owner/repo --limit 200
# Inspect specific PRs only
gh diet-kit dangling blobs -R owner/repo --pr 42,43
# Squash/rebase detection only, confirm with local-object reachability check
gh diet-kit dangling blobs --no-closed --no-force-push --reachability-check local-object
# Force-push detection only
gh diet-kit dangling blobs --no-closed --no-squash-merge
# All methods, output as JSON
gh diet-kit dangling blobs --format json | jq '.[] | .sha'
```

### `gh diet-kit dangling commits`

List commits from squash or rebase merged pull requests, commits dropped by force-pushes on PR head branches, or commits from closed unmerged pull requests, that are not reachable from any normal branch or tag ref. All detection methods are enabled by default.

Output fields: `SHA`, `PR_NUMBER`, `PR_URL`, `SIZE`, `MESSAGE`. `SIZE` is the total size of unique blobs added or modified in the commit diff (human-readable, e.g. `1.2 MB`). In JSON, `SIZE` is omitted when blob sizes are unavailable.

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

Output fields: `SHA`, `MESSAGE`. In JSON, `pr_number`, `pr_url`, and `size` fields are omitted.

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

### `gh diet-kit lfs detect`

Detect files in the repository whose size exceeds a threshold and are not currently stored as Git LFS objects. Files properly tracked by LFS appear as small pointer files (~134 bytes) in the git tree and are therefore not reported.

The minimum allowed threshold is 135 bytes (one byte above the LFS pointer size).

Output fields: `PATH`, `SIZE`, `SHA`.

```sh
gh diet-kit lfs detect -R owner/repo
gh diet-kit lfs detect -R owner/repo --threshold 50MB
gh diet-kit lfs detect -R owner/repo --sort size --order desc
gh diet-kit lfs detect -R owner/repo --format json | jq '.[] | .path'
```

### `gh diet-kit lfs estimate`

Estimate how much git object storage would be freed by migrating large files to Git LFS.

When path arguments are given, only those specific files are estimated regardless of `--threshold`. Use `--scan-commits` to count historic versions; the estimated total size is approximated as `current_size × version_count`.

Output fields (without `--scan-commits`): `PATH`, `CURRENT_SIZE`, `ESTIMATED_SAVING`

Output fields (with `--scan-commits`): `PATH`, `CURRENT_SIZE`, `VERSIONS`, `ESTIMATED_TOTAL_SIZE`, `ESTIMATED_SAVING`

```sh
gh diet-kit lfs estimate -R owner/repo
gh diet-kit lfs estimate -R owner/repo --threshold 50MB --scan-commits -1
gh diet-kit lfs estimate -R owner/repo path/to/large/file.bin
gh diet-kit lfs estimate -R owner/repo --format json | jq '.estimates[] | .estimated_saving'
```

### `gh diet-kit tree detect`

Analyse the git tree structure of a repository and report directories whose direct entry count (files + subdirectories) meets or exceeds a threshold.

Git stores one tree object per directory per commit. A directory with many direct entries produces a large tree object, and a deep or wide hierarchy multiplies the number of tree objects written on every commit.

Output fields: `PATH`, `DEPTH`, `ENTRY_COUNT`, `TOTAL_FILES`, `EST_TREE_SIZE`.

A summary line (total dirs, total files, total estimated tree object size, max depth) is printed after the table.

```sh
# List all directories sorted by entry count descending
gh diet-kit tree detect -R owner/repo --sort entry-count --order desc
# Report only directories with 100 or more direct entries
gh diet-kit tree detect -R owner/repo --threshold 100
# Inspect a specific branch
gh diet-kit tree detect -R owner/repo --ref main
# Export as JSON
gh diet-kit tree detect -R owner/repo --format json | jq '.dirs[] | select(.entry_count > 50)'
```
