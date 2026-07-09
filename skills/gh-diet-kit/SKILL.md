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
      - name: --concurrency
        description: Maximum number of concurrent GitHub API calls per PR for commit blob fetches; <=0 uses the package default of 5 (default: 0)
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
        description: Disable closed unmerged PR blob detection; previously cached data for this scope is preserved in the cache when this flag is set (default: false)
      - name: --no-force-push
        description: Disable force-push dropped commit blob detection; previously cached data for this scope is preserved in the cache when this flag is set (default: false)
      - name: --no-squash-merge
        description: Disable squash/rebase merged PR blob detection; previously cached data for this scope is preserved in the cache when this flag is set (default: false)
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

  - name: gh diet-kit dangling branches
    description: List branches that have no associated pull request (open, closed, or merged), and calculate the total size of blobs introduced by commits unique to each branch. The default branch is always excluded. A commit is unique to a branch when it is not present in any other branch's commit history (commits ahead of the default branch). unique_blob_size is the sum of blob sizes from the diffs of those unique commits, with blob SHAs deduplicated. Outputs table (default) or JSON with fields name, commit_sha, ahead_count, unique_blob_size.
    usage: gh diet-kit dangling branches [flags]
    flags:
      - name: --clear-cache
        description: Clear the cache used for branch analysis before running (default: false)
      - name: --format
        description: Output format (json)
      - name: --jq
        shorthand: -q
        description: Filter JSON output using a jq expression
      - name: --max-branches
        description: Maximum number of branches to process (default: no limit)
      - name: --max-commits
        description: Maximum number of unique commits to inspect per branch (default: no limit)
      - name: --no-cache
        description: Disable use of the branch analysis cache for this run (default: false)
      - name: --order
        description: Sort order (asc or desc, default asc)
      - name: --repo
        shorthand: -R
        description: Repository in "[HOST/]OWNER/REPO" format (default: current repository)
      - name: --sort
        description: Sort by field (branch, ahead_count, unique_size)
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
      - name: --concurrency
        description: Maximum number of concurrent GitHub API calls per PR for commit blob fetches; <=0 uses the package default of 5 (default: 0)
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
        description: Disable closed unmerged PR detection; previously cached data for this scope is preserved in the cache when this flag is set (default: false)
      - name: --no-force-push
        description: Disable force-push dropped commit detection; previously cached data for this scope is preserved in the cache when this flag is set (default: false)
      - name: --no-squash-merge
        description: Disable squash/rebase merged PR commit detection; previously cached data for this scope is preserved in the cache when this flag is set (default: false)
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

  - name: gh diet-kit pr assets dump
    description: Download media assets (images and videos) embedded in pull request bodies, issue comments, and review comments to a local directory, and write a metadata.json file recording the source repository, PR numbers, asset locations, and original URLs. Asset files are saved with a hash-prefixed filename to avoid collisions. On subsequent runs, unchanged PRs are skipped and already-downloaded files are not re-fetched unless --overwrite is specified.
    usage: gh diet-kit pr assets dump [flags]
    flags:
      - name: --max-prs
        description: Maximum number of PRs to fetch when --pr is not specified; 0 = unlimited (default 0)
      - name: --metadata-file
        description: Path to write the metadata JSON file (default <output-dir>/metadata.json)
      - name: --no-file-size
        description: Skip the HEAD request used to record asset file sizes in the metadata (default false)
      - name: --output-dir
        description: Directory to download asset files into (default ./pr-assets)
      - name: --overwrite
        description: Re-download all assets and overwrite existing files, skipping repository and timestamp checks (default false)
      - name: --pr
        description: PR numbers to scan, repeatable (default all PRs)
      - name: --repo
        shorthand: -R
        description: Repository in "[HOST/]OWNER/REPO" format (default current repository)
      - name: --state
        description: Filter pull requests by state (all, open, closed; default all)

  - name: gh diet-kit pr assets list
    description: Scan pull request bodies, issue comments, and review comments for GitHub-hosted media assets (images and videos) and print a summary table. Detected URL patterns include user-images.githubusercontent.com, private-user-images.githubusercontent.com, github.com/user-attachments/assets/..., and github.com/<owner>/<repo>/assets/.... Output fields are PR_NUMBER, LOCATION, LOCATION_ID, TYPE, FILENAME, FILE_SIZE, ASSET_URL. FILE_SIZE is determined by a HEAD request; use --no-file-size to skip. Supports JSON and template output.
    usage: gh diet-kit pr assets list [flags]
    flags:
      - name: --fields
        description: Comma-separated list of output fields to display (default all default fields)
      - name: --format
        description: Output format (json)
      - name: --jq
        shorthand: -q
        description: Filter JSON output using a jq expression
      - name: --max-prs
        description: Maximum number of PRs to fetch when --pr is not specified; 0 = unlimited (default 0)
      - name: --no-file-size
        description: Skip the HEAD request used to determine asset file sizes (default false)
      - name: --pr
        description: PR numbers to scan, repeatable (default all PRs)
      - name: --repo
        shorthand: -R
        description: Repository in "[HOST/]OWNER/REPO" format (default current repository)
      - name: --state
        description: Filter pull requests by state (all, open, closed; default all)
      - name: --template
        shorthand: -t
        description: Format JSON output using a Go template

  - name: gh diet-kit pr assets restore
    description: Read the metadata.json produced by "pr assets dump", upload each local asset file to the destination repository using Playwright browser automation, and replace the old source asset URLs with the new destination CDN URLs in PR bodies, issue comments, and review comments. On the first run a browser window opens for interactive GitHub login; the session is saved to --browser-state for headless operation on subsequent runs. The --upload-delay flag paces uploads under GitHub's per-minute secondary rate limit (~80 content-generating requests/minute). Asset uploads also count against a stricter per-endpoint content-creation limit whose hourly bucket is lower than the documented 500/hour (~80-90 uploads/hour in practice), so a large restore eventually hits it regardless of --upload-delay; the restore then automatically waits and resumes by honoring the Retry-After / x-ratelimit-reset response headers (waiting up to ~1 hour) before retrying. When the restore finishes it writes metadata.restored.json into --input-dir listing only the assets still needing work (old URL still present at the destination and not uploaded this run), with each remaining asset's location ID rewritten to the destination comment ID resolved during the run; re-run with --continue to resume from <input-dir>/metadata.restored.json without re-searching already-migrated comments. The file is not written in --dryrun mode.
    usage: gh diet-kit pr assets restore [flags]
    flags:
      - name: --browser-state
        description: Path to the Playwright browser state file for session persistence (default <user-config-dir>/gh-diet-kit/playwright-state.json)
      - name: --clear-cache
        description: Delete the saved browser session after the restore completes (default false)
      - name: --clear-cache-only
        description: Delete the saved browser session and exit without restoring (default false)
      - name: --continue
        description: Resume from <input-dir>/metadata.restored.json written by a previous restore, mutually exclusive with --metadata-file (default false)
      - name: --dryrun
        shorthand: -n
        description: Preview uploads and URL replacements without making any changes (default false)
      - name: --headed
        description: Run browser in headed (visible) mode even when a saved session exists (default false)
      - name: --input-dir
        description: Directory containing the downloaded asset files (default ./pr-assets)
      - name: --metadata-file
        description: Path to the metadata JSON file (default <input-dir>/metadata.json)
      - name: --pr
        description: PR numbers to restore, repeatable (default all PRs)
      - name: --repo
        shorthand: -R
        description: Destination repository in "[HOST/]OWNER/REPO" format (default current repository)
      - name: --upload-delay
        description: Minimum delay between asset uploads to avoid GitHub's secondary rate limit (default 1s)

---

# gh-diet-kit

A slim GitHub CLI extension based on gh-team-kit, providing only the essential core commands.

## CLI Structure

```
gh diet-kit
├── completion                  # Generate shell completion scripts (bash, zsh, fish, powershell)
├── dangling                    # Find git objects not reachable from normal refs
│   ├── blobs                   # List dangling blobs
│   ├── branches                # List branches with no PR and their unique blob sizes
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

### `gh diet-kit dangling branches`

List branches that have no associated pull request (open, closed, or merged) and calculate the total size of blobs introduced by commits unique to each branch. The default branch is always excluded. `UNIQUE_SIZE` is the sum of blob sizes from the diffs of commits that are not present in any other branch, with blob SHAs deduplicated.

Output fields: `BRANCH`, `COMMIT_SHA`, `AHEAD_COUNT`, `UNIQUE_SIZE`.

```sh
gh diet-kit dangling branches -R owner/repo
gh diet-kit dangling branches -R owner/repo --sort unique_size --order desc
gh diet-kit dangling branches -R owner/repo --format json | jq '.[] | .name'
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
