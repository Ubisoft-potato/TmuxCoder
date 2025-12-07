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

## Quickstart

### 1. Check Requirements

- macOS or Linux with tmux ≥ 3.2
- Go 1.24+ (for building binaries)
- `bun` (auto-start OpenCode server)
- OpenCode-compatible API access

### 2. Install Once

```bash
git clone https://github.com/Ubisoft-potato/TmuxCoder
cd TmuxCoder
./install.sh          # builds binaries and installs the tmuxcoder CLI
```

### 3. Launch the Workspace

Run `tmuxcoder` from any project directory. The CLI:
1. Builds binaries if needed
2. Starts or reuses the OpenCode server
3. Creates/attaches to the tmux session with three panes (sessions, messages, input)

Detach with the normal tmux shortcut (`Ctrl-b d`) and re-run `tmuxcoder` to jump back in.

### 4. Handy Commands

| Command | What it does |
|---------|--------------|
| `tmuxcoder list` | Show managed sessions (no server start needed) |
| `tmuxcoder status <name>` | Inspect tmux/daemon status |
| `tmuxcoder <name>` | Create or attach to a named session |
| `tmuxcoder attach <name>` | Attach without rebuilding |
| `tmuxcoder stop <name>` | Stop daemon only |
| `tmuxcoder stop <name> --cleanup` | Stop daemon and kill tmux session |

Use `tmuxcoder --server http://host:port` to point at an existing OpenCode deployment, or export `OPENCODE_SERVER` in your shell.

### 5. Customize Layout & Config

- `tmuxcoder layout <session> [path/to/layout.yaml]` reloads the layout for a **running** session without attaching (defaults to `~/.opencode/tmux.yaml` when the path is omitted). Example:

  ```bash
  tmuxcoder layout my-session ~/.opencode/tests/test-tmux.yaml
  ```

- Environment variables:

  | Variable | Default | Description |
  |----------|---------|-------------|
  | `OPENCODE_SERVER` | auto-started `http://127.0.0.1:55306` | OpenCode API base URL |
  | `OPENCODE_SOCKET` | `${HOME}/.opencode/ipc.sock` | IPC socket |
  | `OPENCODE_STATE` | `${HOME}/.opencode/state.json` | Persisted shared state |
  | `OPENCODE_TMUX_CONFIG` | `${HOME}/.opencode/tmux.yaml` | Layout/session YAML |

Need the full architecture story later? See [docs/TMUX_ARCHITECTURE.md](docs/TMUX_ARCHITECTURE.md).

### 6. Logs, State & Troubleshooting

- Panel + orchestrator logs: `~/.opencode/*.log`
- State snapshots: `~/.opencode/states/<session>.json`
- Quick fixes:
  - IPC errors? Remove stale socket `rm ~/.opencode/ipc.sock`
  - YAML mistakes? `yamllint ~/.opencode/tmux.yaml`

Common symptoms:

- **Panels not starting** – inspect `~/.opencode/tmux-<session>.log`
- **Layout ignored** – double-check `OPENCODE_TMUX_CONFIG` and re-run with `--layout`
- **State not saving** – verify disk permissions and free space

If everything looks stuck, run `tmuxcoder stop <name> --cleanup`, then `tmuxcoder` again for a clean slate.
