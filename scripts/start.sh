#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
ALL_ARGS=("$@")

function usage() {
  cat <<'EOF'
Usage: scripts/start.sh [options] [-- <opencode-tmux args>]

Options:
  --skip-build       Skip the Go build step and run existing binaries
  --server <URL>     Set OPENCODE_SERVER (default http://127.0.0.1:62435)
  --panels <list>    Build only the specified panels, comma separated (sessions,messages,input)
  --attach-only      Attach to an existing tmux session without restarting panels
  --reload-layout    Send a hot reload layout command to the running tmux session
  --session <name>   With --reload-layout, target a specific managed tmux session
  -h, --help         Show this help message

Other arguments are forwarded to opencode-tmux, e.g. to add --server-only.
EOF
}

SKIP_BUILD=0
SERVER_URL="${OPENCODE_SERVER:-http://127.0.0.1:62435}"
REQUESTED_PANELS=""
ATTACH_ONLY=0
RELOAD_LAYOUT=0
SESSION_OVERRIDE_RAW=""
RELOAD_TARGET_SESSION=""
declare -a FORWARD_ARGS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-build)
      SKIP_BUILD=1
      shift
      ;;
    --server)
      if [[ $# -lt 2 ]]; then
        echo "Error: --server requires a URL" >&2
        exit 1
      fi
      SERVER_URL="$2"
      shift 2
      ;;
    --panels)
      if [[ $# -lt 2 ]]; then
        echo "Error: --panels requires a comma-separated list of panels, e.g. sessions,messages,input" >&2
        exit 1
      fi
      REQUESTED_PANELS="$2"
      shift 2
      ;;
    --attach-only)
      ATTACH_ONLY=1
      FORWARD_ARGS+=("--attach-only")
      shift
      ;;
    --reload-layout)
      RELOAD_LAYOUT=1
      FORWARD_ARGS+=("--reload-layout")
      shift
      ;;
    --session)
      if [[ $# -lt 2 ]]; then
        echo "Error: --session requires a session name or suffix" >&2
        exit 1
      fi
      SESSION_OVERRIDE_RAW="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --)
      shift
      FORWARD_ARGS+=("$@")
      break
      ;;
    *)
      FORWARD_ARGS+=("$1")
      shift
      ;;
  esac
done

if [[ $ATTACH_ONLY -eq 1 || $RELOAD_LAYOUT -eq 1 ]]; then
  SKIP_BUILD=1
fi

if [[ -n "$SESSION_OVERRIDE_RAW" && $RELOAD_LAYOUT -eq 0 ]]; then
  echo "Error: --session is only valid together with --reload-layout" >&2
  exit 1
fi

SESSION_PREFIX="tmux-coder"
SESSION_SUFFIX_PATTERN='^[A-Za-z0-9_-]+$'
SESSION_DELIMITER="-"
SESSION_DISPLAY_DELIMITER=":"
SESSION_NAME_PREFIX="${SESSION_PREFIX}${SESSION_DELIMITER}"
SESSION_STATE_FILE=""
SESSION_STATE_TMP=""
USER_ABORTED=0
PERFORM_CLEANUP=0
SESSION_ACTION=""
SELECTED_SESSION=""
declare -a STATE_SESSIONS
declare -a LIVE_SESSIONS
declare -a CURRENT_SESSIONS
STATE_SESSIONS=()
LIVE_SESSIONS=()
CURRENT_SESSIONS=()

trim_whitespace() {
  local value="$1"
  # Use sed for portability with bash 3.2
  printf '%s' "$value" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//'
}

array_contains() {
  local needle="$1"
  local array_name="$2"
  local size=0
  eval "size=\${#${array_name}[@]}"
  local idx=0
  while (( idx < size )); do
    local value
    eval "value=\${${array_name}[\$idx]}"
    if [[ "$value" == "$needle" ]]; then
      return 0
    fi
    idx=$((idx + 1))
  done
  return 1
}

build_session_name() {
  local suffix="$1"
  printf '%s%s%s' "${SESSION_PREFIX}" "${SESSION_DELIMITER}" "$suffix"
}

is_managed_session_name() {
  local name="$1"
  [[ "$name" == "${SESSION_NAME_PREFIX}"* ]]
}

format_display_name() {
  local full="$1"
  if ! is_managed_session_name "$full"; then
    printf '%s' "$full"
    return 0
  fi
  local suffix
  suffix="$(session_suffix "$full")"
  printf '%s%s%s' "${SESSION_PREFIX}" "${SESSION_DISPLAY_DELIMITER}" "$suffix"
}

normalize_session_name() {
  local raw
  raw="$(trim_whitespace "$1")"
  if [[ -z "$raw" ]]; then
    return 1
  fi
  if is_managed_session_name "$raw"; then
    printf '%s' "$raw"
    return 0
  fi
  local display_prefix="${SESSION_PREFIX}${SESSION_DISPLAY_DELIMITER}"
  if [[ "$raw" == "$display_prefix"* ]]; then
    local suffix="${raw#$display_prefix}"
    if [[ "$suffix" =~ $SESSION_SUFFIX_PATTERN ]]; then
      printf '%s' "$(build_session_name "$suffix")"
      return 0
    fi
    return 1
  fi
  if [[ "$raw" =~ $SESSION_SUFFIX_PATTERN ]]; then
    printf '%s' "$(build_session_name "$raw")"
    return 0
  fi
  return 1
}

load_state_sessions() {
  STATE_SESSIONS=()
  local file="$1"
  if [[ ! -f "$file" ]]; then
    return 0
  fi
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -z "$line" ]] && continue
    if session_normalized="$(normalize_session_name "$line")"; then
      STATE_SESSIONS+=("$session_normalized")
    fi
  done <"$file"
}

load_live_sessions() {
  LIVE_SESSIONS=()
  local output
  if ! output="$(tmux list-sessions -F '#S' 2>/dev/null)"; then
    return 0
  fi
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -z "$line" ]] && continue
    if session_normalized="$(normalize_session_name "$line")"; then
      LIVE_SESSIONS+=("$session_normalized")
    fi
  done <<EOF
$output
EOF
}

rebuild_current_sessions() {
  CURRENT_SESSIONS=()
  local state_count=${#STATE_SESSIONS[@]}
  local live_count=${#LIVE_SESSIONS[@]}
  if (( state_count > 0 && live_count > 0 )); then
    local idx=0
    while (( idx < state_count )); do
      local entry="${STATE_SESSIONS[idx]}"
      if array_contains "$entry" "LIVE_SESSIONS"; then
        CURRENT_SESSIONS+=("$entry")
      fi
      idx=$((idx + 1))
    done
  fi
  if (( live_count > 0 )); then
    local idx=0
    while (( idx < live_count )); do
      local entry="${LIVE_SESSIONS[idx]}"
      if ! array_contains "$entry" "CURRENT_SESSIONS"; then
        CURRENT_SESSIONS+=("$entry")
      fi
      idx=$((idx + 1))
    done
  fi

  sort_and_dedupe_current_sessions
  write_current_sessions_to_file "$SESSION_STATE_FILE" "$SESSION_STATE_TMP"
}

sort_and_dedupe_current_sessions() {
  if [[ ${#CURRENT_SESSIONS[@]} -eq 0 ]]; then
    return 0
  fi
  local sorted
  sorted="$(printf '%s\n' "${CURRENT_SESSIONS[@]}" | LC_ALL=C sort -u)"
  CURRENT_SESSIONS=()
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -z "$line" ]] && continue
    CURRENT_SESSIONS+=("$line")
  done <<EOF
$sorted
EOF
}

write_current_sessions_to_file() {
  local file="$1"
  local tmp="$2"
  mkdir -p "$(dirname "$file")"
  : >"$tmp"
  if [[ ${#CURRENT_SESSIONS[@]} -gt 0 ]]; then
    for entry in "${CURRENT_SESSIONS[@]}"; do
      printf '%s\n' "$entry" >>"$tmp"
    done
  fi
  mv "$tmp" "$file"
}

refresh_state_from_live() {
  if [[ -z "$SESSION_STATE_FILE" ]]; then
    return 0
  fi
  load_live_sessions
  CURRENT_SESSIONS=("${LIVE_SESSIONS[@]+"${LIVE_SESSIONS[@]}"}")
  sort_and_dedupe_current_sessions
  write_current_sessions_to_file "$SESSION_STATE_FILE" "$SESSION_STATE_TMP"
}

session_suffix() {
  local full="$1"
  if [[ "$full" == "${SESSION_NAME_PREFIX}"* ]]; then
    printf '%s' "${full#${SESSION_NAME_PREFIX}}"
    return 0
  fi
  printf '%s' "$full"
}

generate_default_suffix() {
  local index=1
  while :; do
    local candidate_suffix="$index"
    local full_name
    full_name="$(build_session_name "$candidate_suffix")"
    if ! array_contains "$full_name" "CURRENT_SESSIONS"; then
      printf '%s' "$candidate_suffix"
      return 0
    fi
    index=$((index + 1))
  done
}

add_session_to_state_if_needed() {
  local session_name="$1"
  if array_contains "$session_name" "CURRENT_SESSIONS"; then
    return 0
  fi
  CURRENT_SESSIONS+=("$session_name")
  sort_and_dedupe_current_sessions
  write_current_sessions_to_file "$SESSION_STATE_FILE" "$SESSION_STATE_TMP"
}

prompt_new_session_name() {
  local default_suffix
  default_suffix="$(generate_default_suffix)"
  local default_display
  default_display="$(format_display_name "$(build_session_name "$default_suffix")")"
  local input suffix
  while :; do
    printf 'Enter new session name suffix (default %s -> %s, allowed [A-Za-z0-9_-]): ' "$default_suffix" "$default_display"
    if ! IFS= read -r input; then
      return 1
    fi
    input="$(trim_whitespace "$input")"
    if [[ -z "$input" ]]; then
      suffix="$default_suffix"
    else
      if [[ "$input" == "q" || "$input" == "quit" || "$input" == "!" ]]; then
        return 1
      fi
      if [[ ! "$input" =~ $SESSION_SUFFIX_PATTERN ]]; then
        echo "Invalid name. Use letters, numbers, underscores, or dashes."
        continue
      fi
      if [[ "$input" == "${SESSION_PREFIX}" ]]; then
        echo "Suffix must differ from the tmux prefix."
        continue
      fi
      suffix="$input"
    fi
    local candidate
    candidate="$(build_session_name "$suffix")"
    if array_contains "$candidate" "CURRENT_SESSIONS"; then
      echo "Session $(format_display_name "$candidate") already exists. Choose another name."
      continue
    fi
    SELECTED_SESSION="$candidate"
    SESSION_ACTION="create"
    add_session_to_state_if_needed "$SELECTED_SESSION"
    return 0
  done
}

prompt_existing_session_action() {
  local session="$1"
  local display
  display="$(format_display_name "$session")"

  echo ""
  echo "Session ${display} [${session}] selected. Choose an action:"
  echo "  r) Reuse session (restart orchestrator, keep panes) [default]"
  echo "  a) Attach only (leave existing processes running)"
  echo "  q) Cancel"

  while :; do
    printf 'Action [r/a/q]: '
    local action
    if ! IFS= read -r action; then
      return 1
    fi
    action="$(trim_whitespace "$action")"

    case "$action" in
      ""|"r"|"reuse"|"R"|"REUSE")
        SESSION_ACTION="reuse"
        echo ""
        return 0
        ;;
      "a"|"attach"|"A"|"ATTACH")
        SESSION_ACTION="attach"
        echo ""
        return 0
        ;;
      "q"|"quit"|"Q"|"QUIT"|"!")
        return 1
        ;;
      *)
        echo "Invalid selection."
        ;;
    esac
  done
}

prompt_session_choice() {
  local count=${#CURRENT_SESSIONS[@]}
  if [[ $count -eq 0 ]]; then
    if ! prompt_new_session_name; then
      return 1
    fi
    return 0
  fi

  echo "Managed tmux sessions:"
  local idx=1
  while [[ $idx -le $count ]]; do
    local session="${CURRENT_SESSIONS[$((idx - 1))]}"
    local display
    display="$(format_display_name "$session")"
    printf '  %d) %s [%s]\n' "$idx" "$display" "$session"
    idx=$((idx + 1))
  done
  printf '  %d) Create new session\n' "$((count + 1))"
  echo "  q) Quit"

  while :; do
    printf 'Select an option [default 1]: '
    local choice
    if ! IFS= read -r choice; then
      return 1
    fi
    choice="$(trim_whitespace "$choice")"
    if [[ -z "$choice" ]]; then
      choice="1"
    fi
    if [[ "$choice" == "q" || "$choice" == "quit" || "$choice" == "!" ]]; then
      return 1
    fi
    if [[ "$choice" =~ ^[0-9]+$ ]]; then
      local number="$choice"
      if (( number >= 1 && number <= count )); then
        SELECTED_SESSION="${CURRENT_SESSIONS[$((number - 1))]}"
        if prompt_existing_session_action "$SELECTED_SESSION"; then
          return 0
        fi
        continue
      fi
      if (( number == count + 1 )); then
        if prompt_new_session_name; then
          return 0
        fi
        return 1
      fi
    fi
    echo "Invalid selection."
  done
}

prompt_select_existing_session() {
  local title="$1"
  local count=${#CURRENT_SESSIONS[@]}
  if (( count == 0 )); then
    return 1
  fi

  echo "$title"
  local idx=1
  while (( idx <= count )); do
    local session="${CURRENT_SESSIONS[$((idx - 1))]}"
    local display
    display="$(format_display_name "$session")"
    printf '  %d) %s [%s]\n' "$idx" "$display" "$session"
    idx=$((idx + 1))
  done
  echo "  q) Quit"

  while :; do
    printf 'Select a session [default 1]: '
    local choice
    if ! IFS= read -r choice; then
      return 1
    fi
    choice="$(trim_whitespace "$choice")"
    if [[ -z "$choice" ]]; then
      choice="1"
    fi
    if [[ "$choice" == "q" || "$choice" == "quit" || "$choice" == "!" ]]; then
      return 1
    fi
    if [[ "$choice" =~ ^[0-9]+$ ]]; then
      local number="$choice"
      if (( number >= 1 && number <= count )); then
        SELECTED_SESSION="${CURRENT_SESSIONS[$((number - 1))]}"
        return 0
      fi
    fi
    echo "Invalid selection."
  done
}

prepare_reload_layout_session() {
  local opencode_home="${OPENCODE_HOME:-${HOME}/.opencode}"
  SESSION_STATE_FILE="${opencode_home}/opencode_tmux_sessions"
  SESSION_STATE_TMP="${SESSION_STATE_FILE}.tmp"

  load_state_sessions "$SESSION_STATE_FILE"
  load_live_sessions
  rebuild_current_sessions

  if [[ -n "$SESSION_OVERRIDE_RAW" ]]; then
    if ! RELOAD_TARGET_SESSION="$(normalize_session_name "$SESSION_OVERRIDE_RAW")"; then
      echo "Error: invalid session name for --session: $SESSION_OVERRIDE_RAW" >&2
      echo "Hint: use suffix like '1' or a full name such as 'tmux-coder-1'." >&2
      exit 1
    fi
    if ! array_contains "$RELOAD_TARGET_SESSION" "CURRENT_SESSIONS"; then
      echo "Error: session ${RELOAD_TARGET_SESSION} is not active." >&2
      exit 1
    fi
    return 0
  fi

  local count=${#CURRENT_SESSIONS[@]}
  if (( count == 0 )); then
    echo "Error: no managed tmux sessions found to reload." >&2
    echo "Start a session first or provide --session <name>." >&2
    exit 1
  fi

  if prompt_select_existing_session "Select a session to reload layout:"; then
    RELOAD_TARGET_SESSION="$SELECTED_SESSION"
    return 0
  fi

  echo "Aborted layout reload." >&2
  exit 1
}

manage_tmux_sessions() {
  local opencode_home="${OPENCODE_HOME:-${HOME}/.opencode}"
  SESSION_STATE_FILE="${opencode_home}/opencode_tmux_sessions"
  SESSION_STATE_TMP="${SESSION_STATE_FILE}.tmp"

  load_state_sessions "$SESSION_STATE_FILE"
  load_live_sessions

  rebuild_current_sessions

  if ! prompt_session_choice; then
    USER_ABORTED=1
    SESSION_ACTION=""
    SELECTED_SESSION=""
    return 1
  fi
  return 0
}

on_interrupt() {
  USER_ABORTED=1
  echo ""
  exit 130
}

on_exit() {
  if [[ $PERFORM_CLEANUP -eq 1 ]]; then
    refresh_state_from_live
  fi
}

trap on_interrupt INT TERM
trap on_exit EXIT

function ensure_command() {
  local name="$1"
  local install_hint="$2"
  if ! command -v "$name" >/dev/null 2>&1; then
    echo "Error: $name not found. Please install it. Hint: ${install_hint}" >&2
    exit 1
  fi
}

ensure_command go "https://go.dev/doc/install"

NEED_TMUX=1
for arg in "${ALL_ARGS[@]}"; do
  if [[ "$arg" == "--server-only" ]]; then
    NEED_TMUX=0
    break
  fi
done

if [[ $NEED_TMUX -eq 1 ]]; then
  ensure_command tmux "Install tmux via your package manager, e.g. brew install tmux"
else
  if ! command -v tmux >/dev/null 2>&1; then
    echo "Notice: tmux is not installed. --server-only detected, skipping tmux check." >&2
  fi
fi

if [[ $NEED_TMUX -eq 1 ]]; then
  if [[ $RELOAD_LAYOUT -eq 0 ]]; then
    if ! manage_tmux_sessions; then
      exit 0
    fi
  else
    prepare_reload_layout_session
  fi
fi

if [[ "$SESSION_ACTION" == "attach" ]]; then
  PERFORM_CLEANUP=1
  display_name="$(format_display_name "$SELECTED_SESSION")"
  echo "==> Attaching to tmux session ${display_name} [${SELECTED_SESSION}]"
  if ! tmux attach -t "${SELECTED_SESSION}"; then
    status=$?
    echo "Error: failed to attach to ${SELECTED_SESSION}" >&2
    exit $status
  fi
  exit 0
fi

if [[ "$SESSION_ACTION" == "create" ]]; then
  PERFORM_CLEANUP=1
elif [[ "$SESSION_ACTION" == "reuse" ]]; then
  PERFORM_CLEANUP=1
fi

if [[ $SKIP_BUILD -eq 0 ]]; then
  echo "==> Building tmux panels and scheduler..."
  BUILD_TARGETS=(
    "cmd/opencode-tmux:dist/opencode-tmux"
    "cmd/opencode-sessions:dist/sessions-pane"
    "cmd/opencode-messages:dist/messages-pane"
    "cmd/opencode-input:dist/input-pane"
  )

  declare -a ENABLED_PANELS
  if [[ -n "$REQUESTED_PANELS" ]]; then
    IFS=',' read -r -a ENABLED_PANELS <<<"$REQUESTED_PANELS"
  else
    ENABLED_PANELS=(sessions messages input)
  fi

  should_build_panel() {
    local name="$1"
    for panel in "${ENABLED_PANELS[@]}"; do
      if [[ "$panel" == "$name" ]]; then
        return 0
      fi
    done
    return 1
  }

  pushd "${REPO_ROOT}" >/dev/null
  for target in "${BUILD_TARGETS[@]}"; do
    pkg="${target%%:*}"
    output_rel="${target#*:}"
    base="$(basename "$pkg")"
    panel_name="${base#opencode-}"
    if [[ "$base" != "opencode-tmux" ]]; then
      if ! should_build_panel "$panel_name"; then
        echo "  -> Skipping ${pkg} (not in --panels list)"
        continue
      fi
    fi
    output="${REPO_ROOT}/${pkg}/${output_rel}"
    mkdir -p "$(dirname "$output")"
    echo "  -> go build ./${pkg} -> ${pkg}/${output_rel}"
    GO111MODULE=on go build -o "$output" "./${pkg}"
  done
  popd >/dev/null
else
  echo "==> Skipping build step, using existing binaries"
fi

OPENCODE_HOME="${OPENCODE_HOME:-${HOME}/.opencode}"
export OPENCODE_SERVER="${SERVER_URL}"
# Note: OPENCODE_SOCKET and OPENCODE_STATE are now auto-managed per-session
# Only export them if explicitly set by user
if [[ -n "${OPENCODE_SOCKET:-}" ]]; then
  export OPENCODE_SOCKET
fi
if [[ -n "${OPENCODE_STATE:-}" ]]; then
  export OPENCODE_STATE
fi
export OPENCODE_TMUX_CONFIG="${OPENCODE_TMUX_CONFIG:-${OPENCODE_HOME}/tmux.yaml}"

mkdir -p "${OPENCODE_HOME}"
mkdir -p "$(dirname "${OPENCODE_TMUX_CONFIG}")"

echo "==> Starting opencode-tmux with environment variables"
echo "  OPENCODE_SERVER=${OPENCODE_SERVER}"
if [[ -n "${OPENCODE_SOCKET:-}" ]]; then
  echo "  OPENCODE_SOCKET=${OPENCODE_SOCKET} (user override)"
else
  echo "  OPENCODE_SOCKET=<auto per-session>"
fi
if [[ -n "${OPENCODE_STATE:-}" ]]; then
  echo "  OPENCODE_STATE=${OPENCODE_STATE} (user override)"
else
  echo "  OPENCODE_STATE=<auto per-session>"
fi
echo "  OPENCODE_TMUX_CONFIG=${OPENCODE_TMUX_CONFIG}"

BIN_PATH="${REPO_ROOT}/cmd/opencode-tmux/dist/opencode-tmux"
if [[ ! -x "${BIN_PATH}" ]]; then
  echo "Error: Executable ${BIN_PATH} not found. Please avoid skipping the build, or verify that the build succeeded." >&2
  exit 1
fi

SESSION_FLAG_PRESENT=0
for arg in "${FORWARD_ARGS[@]+"${FORWARD_ARGS[@]}"}"; do
  if [[ "$arg" == "--reuse-session" || "$arg" == "--force-new-session" ]]; then
    SESSION_FLAG_PRESENT=1
    break
  fi
done

# Convert "reuse" action to "reload-layout" command
if [[ "$SESSION_ACTION" == "reuse" && -n "$SELECTED_SESSION" ]]; then
  RELOAD_LAYOUT=1
  RELOAD_TARGET_SESSION="$SELECTED_SESSION"
  SESSION_ACTION=""
fi

if [[ "$SESSION_ACTION" == "" && $SESSION_FLAG_PRESENT -eq 0 && $ATTACH_ONLY -eq 0 && $RELOAD_LAYOUT -eq 0 ]]; then
  if [[ -n "$REQUESTED_PANELS" ]]; then
    FORWARD_ARGS+=("--reuse-session")
    SESSION_FLAG_PRESENT=1
  fi
fi

if [[ "$SESSION_ACTION" == "create" && -n "$SELECTED_SESSION" ]]; then
  display_name="$(format_display_name "$SELECTED_SESSION")"
  echo "==> Launching managed tmux session ${display_name} [${SELECTED_SESSION}]"
fi

if [[ $RELOAD_LAYOUT -eq 1 && -n "$RELOAD_TARGET_SESSION" ]]; then
  display_name="$(format_display_name "$RELOAD_TARGET_SESSION")"
  echo "==> Reloading layout for session ${display_name} [${RELOAD_TARGET_SESSION}]"
fi

CMD=("${BIN_PATH}")
if [[ ${#FORWARD_ARGS[@]} -gt 0 ]]; then
  CMD+=("${FORWARD_ARGS[@]}")
fi
if [[ $RELOAD_LAYOUT -eq 1 ]]; then
  CMD+=("--reload-layout")
fi
if [[ "$SESSION_ACTION" == "create" && -n "$SELECTED_SESSION" ]]; then
  CMD+=("${SELECTED_SESSION}")
fi
if [[ $RELOAD_LAYOUT -eq 1 && -n "$RELOAD_TARGET_SESSION" ]]; then
  CMD+=("${RELOAD_TARGET_SESSION}")
fi

if ! "${CMD[@]}"; then
  status=$?
  exit $status
fi
exit 0
