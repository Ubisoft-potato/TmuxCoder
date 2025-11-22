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

  pushd "${REPO_ROOT}/packages/opencode/packages/opencode" >/dev/null || {
    echo "Error: opencode package directory not found" >&2
    exit 1
  }
  bun run src/index.ts serve --hostname "$AUTO_SERVER_HOST" --port "$AUTO_SERVER_PORT" >>"$log" 2>&1 &
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
  [[ -z "$OPENCODE_BRANCH" ]] && OPENCODE_BRANCH="main"

  git clone --depth 1 --branch "$OPENCODE_BRANCH" "$OPENCODE_URL" "$OPENCODE_DIR" || {
    echo "Error: Failed to clone opencode" >&2
    exit 1
  }
  echo "==> Successfully cloned opencode to $OPENCODE_DIR"
fi

# Install opencode dependencies if needed
# Note: opencode is a monorepo, need to install from root directory
if [[ -d "$OPENCODE_DIR" ]]; then
  if [[ ! -d "$OPENCODE_DIR/node_modules" ]] || [[ ! -d "$OPENCODE_DIR/node_modules/solid-js" ]]; then
    echo "==> Installing opencode dependencies..."

    # Check for permission issues in cache directories
    echo "    Checking cache permissions..."
    permission_issues=0

    # Check for root-owned files in bun cache
    if [[ -d "${HOME}/.bun/install/cache" ]]; then
      if find "${HOME}/.bun/install/cache" -user root -print -quit 2>/dev/null | grep -q .; then
        echo "    ⚠ Warning: Found root-owned files in bun cache" >&2
        permission_issues=1
      fi
    fi

    # Check for root-owned files in npm cache
    if [[ -d "${HOME}/.npm" ]]; then
      if find "${HOME}/.npm" -user root -print -quit 2>/dev/null | grep -q .; then
        echo "    ⚠ Warning: Found root-owned files in npm cache" >&2
        permission_issues=1
      fi
    fi

    if [[ $permission_issues -eq 1 ]]; then
      echo ""
      echo "    To fix permission issues, run these commands:" >&2
      echo "      sudo chown -R \$(whoami):staff ~/.bun" >&2
      echo "      sudo chown -R \$(whoami):staff ~/.npm" >&2
      echo ""
      read -p "    Fix permissions now? (requires sudo) [y/N]: " -n 1 -r
      echo
      if [[ $REPLY =~ ^[Yy]$ ]]; then
        echo "    Fixing permissions..."
        sudo chown -R $(whoami):staff ~/.bun ~/.npm 2>&1 || {
          echo "    Failed to fix permissions. Please run the commands manually." >&2
        }
      else
        echo "    Continuing anyway (installation may fail)..."
      fi
    fi

    # Clean potentially problematic cache directories
    if [[ -d "${HOME}/.npm/_libvips" ]]; then
      echo "    Cleaning npm libvips cache..."
      rm -rf "${HOME}/.npm/_libvips" 2>/dev/null || {
        echo "    Warning: Could not clean npm libvips cache" >&2
      }
    fi

    pushd "$OPENCODE_DIR" >/dev/null

    # Use a log file for full installation output
    INSTALL_LOG="${HOME}/.opencode/opencode-install.log"
    mkdir -p "$(dirname "$INSTALL_LOG")"

    echo "    Installing dependencies (this may take a few minutes)..."
    echo "    Full log: $INSTALL_LOG"
    echo ""

    # Show filtered progress
    (bun install 2>&1 | tee "$INSTALL_LOG" | while IFS= read -r line; do
      # Show lines with emojis (bun progress indicators) or important keywords
      if echo "$line" | grep -qE '(📦|🚚|✓|⚠|✗|error|Error|warning|Warning|Saved|Installing \[)'; then
        echo "    $line"
      fi
    done) &
    INSTALL_PID=$!

    # Wait for installation to complete
    wait $INSTALL_PID
    INSTALL_EXIT=$?

    if [ $INSTALL_EXIT -eq 0 ]; then
      echo ""
      echo "==> OpenCode dependencies installed successfully"

      # Check for missing catalog dependencies (bun workspace catalog issue)
      # These are needed but not always installed correctly by bun install
      OPENCODE_PKG="${OPENCODE_DIR}/packages/opencode"
      if [[ -d "$OPENCODE_PKG" ]]; then
        echo "    Checking for catalog dependencies..."

        missing_deps=()

        # Check for solid-js
        if [[ ! -d "$OPENCODE_PKG/node_modules/solid-js" ]]; then
          missing_deps+=("solid-js@1.9.9")
        fi

        # Check for react (needed for jsx-dev-runtime)
        if [[ ! -d "$OPENCODE_PKG/node_modules/react" ]]; then
          missing_deps+=("react@18" "react-dom@18")
        fi

        if [[ ${#missing_deps[@]} -gt 0 ]]; then
          echo "    Installing missing catalog dependencies: ${missing_deps[*]}"
          pushd "$OPENCODE_PKG" >/dev/null
          bun add ${missing_deps[@]} >>"$INSTALL_LOG" 2>&1 || {
            echo "    Warning: Failed to install some dependencies" >&2
          }
          popd >/dev/null
          echo "    ✓ Catalog dependencies installed"
        fi
      fi
    else
      echo ""
      echo "==> Installation encountered errors, trying with --force..."

      (bun install --force 2>&1 | tee -a "$INSTALL_LOG" | while IFS= read -r line; do
        if echo "$line" | grep -qE '(📦|🚚|✓|⚠|✗|error|Error|warning|Warning|Saved|Installing \[)'; then
          echo "    $line"
        fi
      done) &
      INSTALL_PID=$!

      wait $INSTALL_PID
      INSTALL_EXIT=$?

      if [ $INSTALL_EXIT -eq 0 ]; then
        echo ""
        echo "==> OpenCode dependencies installed successfully"

        # Check for missing catalog dependencies (bun workspace catalog issue)
        OPENCODE_PKG="${OPENCODE_DIR}/packages/opencode"
        if [[ -d "$OPENCODE_PKG" ]]; then
          echo "    Checking for catalog dependencies..."

          missing_deps=()

          # Check for solid-js
          if [[ ! -d "$OPENCODE_PKG/node_modules/solid-js" ]]; then
            missing_deps+=("solid-js@1.9.9")
          fi

          # Check for react (needed for jsx-dev-runtime)
          if [[ ! -d "$OPENCODE_PKG/node_modules/react" ]]; then
            missing_deps+=("react@18" "react-dom@18")
          fi

          if [[ ${#missing_deps[@]} -gt 0 ]]; then
            echo "    Installing missing catalog dependencies: ${missing_deps[*]}"
            pushd "$OPENCODE_PKG" >/dev/null
            bun add ${missing_deps[@]} >>"$INSTALL_LOG" 2>&1 || {
              echo "    Warning: Failed to install some dependencies" >&2
            }
            popd >/dev/null
            echo "    ✓ Catalog dependencies installed"
          fi
        fi
      else
        echo ""
        echo "Error: Failed to install dependencies." >&2
        echo "Check the log at: $INSTALL_LOG" >&2
        echo "" >&2
        echo "You may need to fix permissions:" >&2
        echo "  sudo chown -R $(whoami):staff ~/.npm ~/.bun" >&2
        echo "" >&2
        echo "Or install manually:" >&2
        echo "  cd $OPENCODE_PKG_DIR" >&2
        echo "  bun install" >&2
        popd >/dev/null
        exit 1
      fi
    fi
    popd >/dev/null
  else
    echo "==> OpenCode dependencies already installed"

    # Still check for missing catalog dependencies even if node_modules exists
    OPENCODE_PKG="${OPENCODE_DIR}/packages/opencode"
    if [[ -d "$OPENCODE_PKG" ]]; then
      missing_deps=()

      # Check for solid-js
      if [[ ! -d "$OPENCODE_PKG/node_modules/solid-js" ]]; then
        missing_deps+=("solid-js@1.9.9")
      fi

      # Check for react (needed for jsx-dev-runtime)
      if [[ ! -d "$OPENCODE_PKG/node_modules/react" ]]; then
        missing_deps+=("react@18" "react-dom@18")
      fi

      if [[ ${#missing_deps[@]} -gt 0 ]]; then
        echo "    Installing missing catalog dependencies: ${missing_deps[*]}"
        INSTALL_LOG="${HOME}/.opencode/opencode-install.log"
        mkdir -p "$(dirname "$INSTALL_LOG")"

        pushd "$OPENCODE_PKG" >/dev/null
        bun add ${missing_deps[@]} >>"$INSTALL_LOG" 2>&1 || {
          echo "    Warning: Failed to install some dependencies" >&2
        }
        popd >/dev/null
        echo "    ✓ Catalog dependencies installed"
      fi
    fi
  fi
else
  echo "Warning: OpenCode directory not found at $OPENCODE_DIR" >&2
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
