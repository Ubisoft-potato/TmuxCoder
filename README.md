# TmuxCoder

[![Go Version](https://img.shields.io/badge/go-%3E%3D1.24-blue.svg)](https://golang.org/)
[![tmux](https://img.shields.io/badge/tmux-%3E%3D3.2-green.svg)](https://github.com/tmux/tmux)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

An AI agent coding orchestrator with a tmux-based TUI interface. Provides a multi-pane terminal workspace for interacting with AI coding assistants powered by the [OpenCode](https://github.com/sst/opencode) API.

## What & Why

AI coding tools typically run in isolated environments, but developers need a persistent, organized workspace to:
- Manage multiple AI coding sessions simultaneously
- Review AI-generated code and messages with proper formatting
- Send commands and prompts to AI agents
- Track conversation history across sessions

TmuxCoder solves this by providing a **tmux-based orchestrator** that integrates with the OpenCode server API to deliver:

- **Multi-session AI coding** – Create, switch between, and manage multiple AI coding sessions
- **Real-time streaming** – SSE-based streaming of AI responses from OpenCode API
- **Persistent workspace** – All sessions, messages, and state persist across restarts
- **Organized UI** – Dedicated panels for session browsing, message history, and input

Built for developers who want a terminal-native AI coding assistant without leaving tmux.

## Features

- **AI Session Management** – Create, browse, switch, and delete AI coding sessions via OpenCode API with keyboard navigation
- **Streaming Message Display** – Real-time SSE streaming of AI responses with markdown rendering and syntax highlighting
- **Interactive Input Panel** – Send prompts with command mode and multiline support
- **Smart State Persistence** – Version-based optimistic locking with JSON persistence and automatic conflict resolution
- **Manual Layout Reload** – Apply YAML config changes via `--reload-layout` flag without killing processes

## Screenshots

_Coming soon - add screenshots showing the three-pane layout in action_

## Architecture

![Architecture Diagram](docs/architecture.svg)

For a deep dive into why we use tmux and how the multi-process architecture works, see:
- [Architecture Deep Dive](docs/TMUX_ARCHITECTURE.md)

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

## Installation

### Quick Start

1. **Clone and Install**
   Run the automated installation script. This will check dependencies (Go, tmux, bun), build binaries, and install the `tmuxcoder` command to your system (or `~/bin`).

   ```bash
   git clone https://github.com/Ubisoft-potato/TmuxCoder
   cd TmuxCoder
   ./install.sh
   ```

2. **Launch**
   Start the orchestrator with a single command:

   ```bash
   tmuxcoder
   ```

   This command handles everything:
   - Auto-builds binaries if needed
   - Starts the OpenCode server in the background
   - Creates and attaches to the tmux session

## Usage

### Basic Usage

Just run:
```bash
tmuxcoder
```

You will be dropped into a tmux session with three panels:
- **Left**: Session Browser (Manage AI sessions)
- **Top-Right**: Message History (Read AI responses)
- **Bottom-Right**: Input (Type commands to AI)

### Advanced Usage

**Custom Server**:
```bash
tmuxcoder --server http://localhost:8080
```

**Custom Layout Config**:
```bash
tmuxcoder --layout ~/.opencode/custom-layout.yaml
```
If the session is already running, this flag triggers a layout reload before attaching.

**Attach Only** (Don't try to start server/build):
```bash
tmuxcoder --attach-only
```

**Hot Reload Layout**:
If you edit `~/.opencode/tmux.yaml`, apply changes without restarting:
```bash
tmuxcoder -- --reload-layout
```

### Default Layout

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
