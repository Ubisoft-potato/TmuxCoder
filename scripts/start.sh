#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

function usage() {
  cat <<'EOF'
Usage: scripts/start.sh [options] [-- <opencode-tmux args>]

Options:
  --skip-build       Skip the Go build step
  --server <URL>     Set OPENCODE_SERVER (default auto-start on 127.0.0.1:55306)
  --attach-only      Attach to existing tmux session only
  -h, --help         Show this help message
EOF
}

SKIP_BUILD=0
SERVER_URL="${OPENCODE_SERVER:-}"
ATTACH_ONLY=0
declare -a FORWARD_ARGS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-build) SKIP_BUILD=1; shift ;;
    --server)
      [[ $# -lt 2 ]] && { echo "Error: --server requires a URL" >&2; exit 1; }
      SERVER_URL="$2"; shift 2 ;;
    --attach-only) ATTACH_ONLY=1; FORWARD_ARGS+=("--attach-only"); shift ;;
    -h|--help) usage; exit 0 ;;
    --) shift; FORWARD_ARGS+=("$@"); break ;;
    *) FORWARD_ARGS+=("$1"); shift ;;
  esac
done

[[ $ATTACH_ONLY -eq 1 ]] && SKIP_BUILD=1

# Session config
SESSION_PREFIX="tmux-coder"
SELECTED_SESSION=""
SESSION_ACTION=""

# Auto server config
AUTO_SERVER_HOST="127.0.0.1"
AUTO_SERVER_PORT="${OPENCODE_AUTO_SERVER_PORT:-55306}"
AUTO_SERVER_URL="http://${AUTO_SERVER_HOST}:${AUTO_SERVER_PORT}"
AUTO_SERVER_PID=""
AUTO_SERVER_STARTED=0

# Get managed tmux sessions
get_sessions() {
  tmux list-sessions -F '#S' 2>/dev/null | grep "^${SESSION_PREFIX}-" || true
}

# Generate next session suffix
next_suffix() {
  local i=1 sessions
  sessions="$(get_sessions)"
  while echo "$sessions" | grep -q "^${SESSION_PREFIX}-${i}$"; do
    i=$((i + 1))
  done
  echo "$i"
}

# Prompt for session
prompt_session() {
  local sessions count=0
  sessions="$(get_sessions)"
  local -a list=()

  if [[ -n "$sessions" ]]; then
    while IFS= read -r s; do
      [[ -n "$s" ]] && { list+=("$s"); count=$((count + 1)); }
    done <<< "$sessions"
  fi

  if [[ $count -eq 0 ]]; then
    local suffix
    suffix="$(next_suffix)"
    printf 'Enter session suffix (default %s -> %s:%s): ' "$suffix" "$SESSION_PREFIX" "$suffix"
    read -r input
    input="${input:-$suffix}"
    [[ "$input" == "q" ]] && return 1
    [[ ! "$input" =~ ^[A-Za-z0-9_-]+$ ]] && { echo "Invalid name"; return 1; }
    SELECTED_SESSION="${SESSION_PREFIX}-${input}"
    SESSION_ACTION="create"
    return 0
  fi

  echo "Sessions:"
  local i=1
  for s in "${list[@]}"; do
    printf '  %d) %s\n' "$i" "$s"
    i=$((i + 1))
  done
  printf '  %d) Create new\n' "$((count + 1))"
  echo "  q) Quit"

  while :; do
    printf 'Select [1]: '
    read -r choice
    choice="${choice:-1}"
    [[ "$choice" == "q" ]] && return 1

    if [[ "$choice" =~ ^[0-9]+$ ]]; then
      if (( choice >= 1 && choice <= count )); then
        SELECTED_SESSION="${list[$((choice - 1))]}"
        echo "Action: r)Reuse a)Attach q)Cancel"
        while :; do
          printf '[r/a/q]: '
          read -r act
          case "${act:-r}" in
            r|R) SESSION_ACTION="reuse"; return 0 ;;
            a|A) SESSION_ACTION="attach"; return 0 ;;
            q|Q) return 1 ;;
            *) echo "Invalid" ;;
          esac
        done
      elif (( choice == count + 1 )); then
        local suffix
        suffix="$(next_suffix)"
        printf 'Session suffix [%s]: ' "$suffix"
        read -r input
        input="${input:-$suffix}"
        [[ ! "$input" =~ ^[A-Za-z0-9_-]+$ ]] && { echo "Invalid"; return 1; }
        SELECTED_SESSION="${SESSION_PREFIX}-${input}"
        SESSION_ACTION="create"
        return 0
      fi
    fi
    echo "Invalid"
  done
}

server_ready() {
  curl -s --max-time 1 "http://${AUTO_SERVER_HOST}:${AUTO_SERVER_PORT}/" >/dev/null 2>&1
}

start_server() {
  command -v bun >/dev/null 2>&1 || { echo "Error: bun not found" >&2; exit 1; }
  local home="${OPENCODE_HOME:-${HOME}/.opencode}"
  mkdir -p "$home"
  local log="${home}/opencode-server.log"

  pushd "${REPO_ROOT}/packages/opencode" >/dev/null
  bun run packages/opencode/src/index.ts serve --hostname "$AUTO_SERVER_HOST" --port "$AUTO_SERVER_PORT" >>"$log" 2>&1 &
  AUTO_SERVER_PID=$!
  popd >/dev/null

  local i=0
  while (( i < 25 )); do
    server_ready && { AUTO_SERVER_STARTED=1; echo "==> Started server at ${AUTO_SERVER_URL}"; return 0; }
    sleep 0.2
    i=$((i + 1))
  done
  echo "Error: Server failed to start" >&2
  [[ -n "$AUTO_SERVER_PID" ]] && kill "$AUTO_SERVER_PID" 2>/dev/null || true
  exit 1
}

maybe_start_server() {
  if server_ready; then
    echo "==> Reusing server at ${AUTO_SERVER_URL}"
  else
    start_server
  fi
}

on_exit() {
  if [[ $AUTO_SERVER_STARTED -eq 1 && -n "$AUTO_SERVER_PID" ]]; then
    kill "$AUTO_SERVER_PID" 2>/dev/null || true
    wait "$AUTO_SERVER_PID" 2>/dev/null || true
  fi
}
trap on_exit EXIT
trap 'exit 130' INT TERM

# Check commands
command -v go >/dev/null 2>&1 || { echo "Error: go not found" >&2; exit 1; }
command -v tmux >/dev/null 2>&1 || { echo "Error: tmux not found" >&2; exit 1; }

# Check and setup opencode dependency
OPENCODE_DIR="${REPO_ROOT}/packages/opencode"
if [[ ! -d "$OPENCODE_DIR" || ! -f "$OPENCODE_DIR/package.json" ]]; then
  echo "==> Setting up opencode dependency..."
  rm -rf "$OPENCODE_DIR" "${REPO_ROOT}/.git/modules/packages/opencode" 2>/dev/null
  mkdir -p "$(dirname "$OPENCODE_DIR")"

  # Read URL and branch from .gitmodules
  GITMODULES="${REPO_ROOT}/.gitmodules"
  OPENCODE_URL=$(git config -f "$GITMODULES" submodule.packages/opencode.url)
  OPENCODE_BRANCH=$(git config -f "$GITMODULES" submodule.packages/opencode.branch)

  [[ -z "$OPENCODE_URL" ]] && { echo "Error: opencode URL not found in .gitmodules" >&2; exit 1; }
  [[ -z "$OPENCODE_BRANCH" ]] && OPENCODE_BRANCH="main"  # Default to main if branch not specified

  git clone --depth 1 --branch "$OPENCODE_BRANCH" "$OPENCODE_URL" "$OPENCODE_DIR" || {
    echo "Error: Failed to clone opencode" >&2
    exit 1
  }
fi

# Install opencode dependencies if needed
if [[ -d "$OPENCODE_DIR" && ! -d "$OPENCODE_DIR/node_modules" ]]; then
  echo "==> Installing opencode dependencies..."
  pushd "$OPENCODE_DIR" >/dev/null
  bun install
  popd >/dev/null
fi

# Session selection
prompt_session || exit 0

# Handle attach
if [[ "$SESSION_ACTION" == "attach" ]]; then
  echo "==> Attaching to ${SELECTED_SESSION}"
  tmux attach -t "$SELECTED_SESSION"
  exit $?
fi

# Start server if needed
if [[ -z "$SERVER_URL" ]]; then
  maybe_start_server
  SERVER_URL="$AUTO_SERVER_URL"
fi

# Build
if [[ $SKIP_BUILD -eq 0 ]]; then
  echo "==> Building..."
  pushd "$REPO_ROOT" >/dev/null
  for pkg in cmd/opencode-tmux cmd/opencode-sessions cmd/opencode-messages cmd/opencode-input; do
    out="$REPO_ROOT/$pkg/dist/$(basename "$pkg" | sed 's/opencode-//')-pane"
    [[ "$pkg" == "cmd/opencode-tmux" ]] && out="$REPO_ROOT/$pkg/dist/opencode-tmux"
    mkdir -p "$(dirname "$out")"
    echo "  -> $pkg"
    go build -o "$out" "./$pkg"
  done
  popd >/dev/null
fi

# Environment
export OPENCODE_SERVER="$SERVER_URL"
export OPENCODE_TMUX_CONFIG="${OPENCODE_TMUX_CONFIG:-${HOME}/.opencode/tmux.yaml}"

echo "==> Starting opencode-tmux (server: $OPENCODE_SERVER)"

BIN="${REPO_ROOT}/cmd/opencode-tmux/dist/opencode-tmux"
[[ ! -x "$BIN" ]] && { echo "Error: $BIN not found" >&2; exit 1; }

# Build command
CMD=("$BIN")
[[ ${#FORWARD_ARGS[@]} -gt 0 ]] && CMD+=("${FORWARD_ARGS[@]}")

if [[ "$SESSION_ACTION" == "reuse" ]]; then
  CMD+=("--reuse-session" "$SELECTED_SESSION")
elif [[ "$SESSION_ACTION" == "create" ]]; then
  echo "==> Creating session ${SELECTED_SESSION}"
  CMD+=("$SELECTED_SESSION")
fi

"${CMD[@]}"
