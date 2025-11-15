# tmux_coder

[![Go Version](https://img.shields.io/badge/go-%3E%3D1.24-blue.svg)](https://golang.org/)
[![tmux](https://img.shields.io/badge/tmux-%3E%3D3.2-green.svg)](https://github.com/tmux/tmux)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A tmux-based workspace manager that orchestrates multi-pane TUI applications with shared state and persistent sessions.

## What & Why

Most terminal multiplexers handle window splitting, but managing **state synchronization** between panes, **process lifecycles**, and **hot-reloading layouts** requires custom glue code. tmux_coder solves this by providing:

- A process orchestrator that supervises TUI panels and restarts them on failure
- Shared state persistence across all panes (sessions, messages, themes)
- IPC infrastructure for inter-pane communication
- Hot-reloadable YAML-based layouts

Built for developers who want a robust terminal workspace without leaving tmux.

## Features

- **Process orchestration** – Automatically spawns, monitors, and restarts panel processes
- **TUI panels** – Built-in Bubble Tea UIs for sessions, messages, and input (as separate binaries)
- **Shared state** – JSON-backed persistence with event bus for cross-pane sync
- **SSE streaming** – Real-time event ingestion from OpenCode API
- **Hot-reload layouts** – Update YAML configs and apply changes without killing processes
- **Helper scripts** – `start.sh` handles builds, launches, and selective panel rebuilds

## Screenshots

_Coming soon - add screenshots showing the three-pane layout in action_

## Architecture

```
┌─────────────────────────────────────────┐
│  opencode-tmux (orchestrator)           │
│  ├─ Spawns & monitors panel processes   │
│  ├─ IPC server (Unix socket)            │
│  └─ Hot-reload config watcher           │
└─────────────────────────────────────────┘
            │ (IPC messages)
    ┌───────┴───────┬───────────────┐
    ▼               ▼               ▼
┌─────────┐   ┌──────────┐   ┌────────┐
│sessions │   │ messages │   │ input  │
│ panel   │   │  panel   │   │ panel  │
└─────────┘   └──────────┘   └────────┘
                    │
            (shared state.json)
```

**Key Components:**

- **Orchestrator** ([cmd/opencode-tmux/main.go](cmd/opencode-tmux/main.go)) – Session manager, process supervisor
- **Panels** ([internal/panels/](internal/panels/)) – Bubble Tea TUIs (sessions, messages, input)
- **IPC** ([internal/ipc](internal/ipc)) – Unix socket with message framing
- **State** ([internal/state](internal/state), [internal/persistence](internal/persistence)) – Event bus + JSON persistence
- **Config** ([internal/config](internal/config), [internal/theme](internal/theme)) – YAML loader + theme registry

**Tech Stack:** Go 1.24+, tmux ≥ 3.2, Bubble Tea, Lip Gloss, OpenCode SDK

## Requirements

- macOS or Linux
- tmux ≥ 3.2
- Go 1.24+
- Bash, sed, awk (for start script)
- OpenCode-compatible API server

## Installation & Build

```bash
git clone <your-repo-url>
cd tmux_coder
go mod download
```

**Build binaries:**

```bash
go build ./cmd/opencode-tmux
go build ./cmd/opencode-input
go build ./cmd/opencode-messages
go build ./cmd/opencode-sessions
```

Or let `scripts/start.sh` build them automatically (use `--skip-build` to skip).

## Quick Start

**1. Set environment variables:**

```bash
export OPENCODE_SERVER="http://127.0.0.1:62435"
export OPENCODE_SOCKET="${HOME}/.opencode/ipc.sock"       # optional
export OPENCODE_STATE="${HOME}/.opencode/state.json"      # optional
export OPENCODE_TMUX_CONFIG="${HOME}/.opencode/tmux.yaml" # optional
```

**2. Create layout config** (optional, defaults provided) at `~/.opencode/tmux.yaml`:

```yaml
version: "1.0"
mode: raw
session:
  name: tmux-coder
panels:
  - id: sessions
    type: sessions
    width: "22%"
  - id: messages
    type: messages
  - id: input
    type: input
    height: "25%"
splits:
  - type: horizontal
    target: root
    panels: ["sessions", "messages"]
    ratio: "1:2"
  - type: vertical
    target: messages
    panels: ["messages", "input"]
    ratio: "3:1"
```

**3. Launch:**

```bash
./scripts/start.sh
```

The script builds binaries, starts the orchestrator, and attaches to the tmux session.

**4. Detach/reattach:**

```bash
tmux detach          # or Ctrl-b d
tmux attach -t tmux-coder
```

## Usage

**Start script options:**

```bash
./scripts/start.sh [options]

--panels sessions,messages    # Rebuild specific panels only
--attach-only                 # Attach to existing session
--reload-layout               # Hot-reload layout without restart
--server <URL>                # Override OPENCODE_SERVER
--skip-build                  # Skip compilation step
```

**Direct binary flags:**

```bash
./opencode-tmux --reuse-session       # Reuse existing session
./opencode-tmux --force-new-session   # Force new session
./opencode-tmux --reload-layout       # Reload layout
```

**Default layout:**

- **Left pane** – Session browser (`opencode-sessions`)
- **Top-right** – Message history with markdown rendering (`opencode-messages`)
- **Bottom-right** – Command input (`opencode-input`)

**Logs & state:**

- Panel logs: `~/.opencode/*.log`
- Shared state: `~/.opencode/state.json` (auto-saved every few seconds)
- The orchestrator auto-restarts failed panels

## Configuration

### Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `OPENCODE_SERVER` | _(required)_ | Base URL for the OpenCode API used by the orchestrator and panels. |
| `OPENCODE_SOCKET` | `${HOME}/.opencode/ipc.sock` | Path to the Unix domain socket for IPC between orchestrator and panels. |
| `OPENCODE_STATE` | `${HOME}/.opencode/state.json` | Location of persisted shared state (sessions, messages, theme). |
| `OPENCODE_TMUX_CONFIG` | `${HOME}/.opencode/tmux.yaml` | YAML file containing session + layout definitions. |

### Layout YAML

Key fields:

- `version` – Config schema version (default: `1.0`)
- `session.name` – tmux session name
- `mode` – Layout strategy (`raw` applies splits as-is)
- `panels` – Panel definitions with `id`, `type`, `width`, `height`, `command`
- `splits` – Split operations with `ratio` (e.g., `"1:2"`)

**Hot-reload changes:**

```bash
./scripts/start.sh --reload-layout
```

## Troubleshooting

**Issue: Panels not starting**
- Check `~/.opencode/*.log` for panel-specific errors
- Verify `OPENCODE_SERVER` is accessible: `curl $OPENCODE_SERVER`
- Ensure tmux version: `tmux -V` (need ≥ 3.2)

**Issue: IPC connection failures**
- Check socket path: `ls -l ~/.opencode/ipc.sock`
- Kill stale socket: `rm ~/.opencode/ipc.sock` and restart
- Look for "connection refused" in orchestrator logs

**Issue: Layout not applying**
- Validate YAML syntax: `yamllint ~/.opencode/tmux.yaml`
- Check config path matches `OPENCODE_TMUX_CONFIG`
- Use `--reload-layout` flag after edits

**Issue: State not persisting**
- Verify write permissions: `ls -la ~/.opencode/state.json`
- Check for disk space: `df -h ~`
- Review autosave logs in `~/.opencode/opencode-tmux.log`

## Contributing

Contributions are welcome! Please:

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

**Before submitting:**
- Run tests: `go test ./...`
- Format code: `go fmt ./...`
- Update docs if adding features

## License

MIT License - see [LICENSE](LICENSE) file for details.

## Project Status

Active development. The core orchestration and panel system is stable. Upcoming work includes:
- [ ] Plugin system for custom panels
- [ ] Better error recovery
- [ ] Windows/WSL support
- [ ] Configuration UI

---

For implementation details, see:
- Orchestrator: [cmd/opencode-tmux/main.go](cmd/opencode-tmux/main.go)
- Panels: [internal/panels/](internal/panels/)
- IPC: [internal/ipc](internal/ipc)
