# TmuxCoder

TmuxCoder is a standalone tmux orchestrator extracted from the OpenCode project. It maintains tmux session lifecycle management, pane bootstrapping, IPC state synchronisation, and YAML layout parsing. The repository is structured as follows:

- `cmd/opencode-tmux`: Go CLI entry point that parses configuration, builds the state manager, and launches tmux.
- `internal/`: shared functionality for configuration loading, IPC, state synchronisation, themes, and persistence.
- `sdk/`: vendored `github.com/sst/opencode-sdk-go` sources used to interact with the OpenCode backend via REST/SSE.

## Prerequisites
- Go 1.24 or newer.
- `tmux` installed and available on `PATH`.
- An OpenCode backend endpoint provided through the `OPENCODE_SERVER` environment variable.

## Build
```bash
go build ./cmd/opencode-tmux
go build ./cmd/opencode-sessions
go build ./cmd/opencode-messages
go build ./cmd/opencode-input
```
or build everything at once:
```bash
go build ./cmd/...
```
Binaries are emitted into their respective `cmd/<name>` folders.

## Run
```bash
OPENCODE_SERVER=http://127.0.0.1:62435 \
OPENCODE_SOCKET=${HOME}/.opencode/ipc.sock \
OPENCODE_STATE=${HOME}/.opencode/state.json \
OPENCODE_TMUX_CONFIG=${HOME}/.opencode/tmux.yaml \
./cmd/opencode-tmux/opencode-tmux
```
- When `OPENCODE_SOCKET`, `OPENCODE_STATE`, or `OPENCODE_TMUX_CONFIG` are omitted, the defaults under `~/.opencode` are used.
- `--server-only` launches the IPC/state services without attaching to tmux (useful for headless testing).
- The remaining panes are spawned automatically, but each panel binary can be invoked manually by exporting `OPENCODE_SERVER` and `OPENCODE_SOCKET`.

## Quick Start Script

Use `scripts/start.sh` for a single-command build & run sequence:

```bash
./scripts/start.sh
```

The script compiles all `cmd/*` binaries into their `dist/` folders, prepares the default `~/.opencode` directory structure, and launches `opencode-tmux`. Common flags:

- `./scripts/start.sh --server <URL>`: override `OPENCODE_SERVER`.
- `./scripts/start.sh --skip-build`: reuse existing binaries without rebuilding.
- `./scripts/start.sh --panels sessions,input`: only build the listed panels for faster iterations.
- Arguments after `--` are forwarded directly to `opencode-tmux`, for example `./scripts/start.sh -- --server-only`.

## Configuration
- Layouts default to `~/.opencode/tmux.yaml`, matching the original OpenCode format (see `internal/config/loader.go` and comments in `cmd/opencode-tmux`).
- Logs are written to `~/.opencode/tmux.log`.
- Panel binaries depend on the local `input/` module, which replaces `github.com/charmbracelet/x/input`.
- Each entry in the YAML `panels` array now supports a `module` field that maps to a registered panel implementation (e.g. `sessions`, `messages`, `input`). If omitted, the legacy `type` → binary mapping is used.

## Roadmap
- Add repository-specific GitHub Actions and integration tests.
- Optionally generalise the default `~/.opencode` paths into a configurable `TMUXCODER_HOME`.
