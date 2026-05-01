---
name: gh-diet-kit
description: gh-diet-kit is a slim GitHub CLI extension based on gh-team-kit. It provides core extension utilities including shell completion and skills documentation. Use when you need a minimal GitHub CLI extension scaffold with root, completion, and skills commands.
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

  - name: gh diet-kit skills
    description: Show available skills documentation for gh-diet-kit.
    usage: gh diet-kit skills [flags]
---

# gh-diet-kit

A slim GitHub CLI extension based on gh-team-kit, providing only the essential core commands.

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

### `gh diet-kit skills`

Show skills documentation embedded in the extension.

```sh
gh diet-kit skills
```
