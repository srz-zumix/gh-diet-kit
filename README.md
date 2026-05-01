# gh-diet-kit

A slim GitHub CLI extension based on [gh-team-kit](https://github.com/srz-zumix/gh-team-kit), providing only the essential core commands: `root`, `completion`, and `skills`.

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

#### skills

Show available skills documentation embedded in the extension.

```sh
gh diet-kit skills
```
