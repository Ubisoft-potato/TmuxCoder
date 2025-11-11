#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
ALL_ARGS=("$@")

function usage() {
  cat <<'EOF'
Usage: scripts/start.sh [options] [-- <opencode-tmux args>]

Options:
  --skip-build       跳过 Go 构建步骤，直接运行已有二进制
  --server <URL>     设置 OPENCODE_SERVER（默认 http://127.0.0.1:62435）
  --panels <list>    仅构建指定面板，逗号分隔（sessions,messages,input）
  -h, --help         显示本帮助

其它参数会原样传给 opencode-tmux，可用于添加 --server-only 等标志。
EOF
}

SKIP_BUILD=0
SERVER_URL="${OPENCODE_SERVER:-http://127.0.0.1:62435}"
REQUESTED_PANELS=""
declare -a FORWARD_ARGS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-build)
      SKIP_BUILD=1
      shift
      ;;
    --server)
      if [[ $# -lt 2 ]]; then
        echo "错误：--server 需要提供 URL" >&2
        exit 1
      fi
      SERVER_URL="$2"
      shift 2
      ;;
    --panels)
      if [[ $# -lt 2 ]]; then
        echo "错误：--panels 需要提供以逗号分隔的面板列表，例如 sessions,messages,input" >&2
        exit 1
      fi
      REQUESTED_PANELS="$2"
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

function ensure_command() {
  local name="$1"
  local install_hint="$2"
  if ! command -v "$name" >/dev/null 2>&1; then
    echo "错误：未检测到 $name，请先安装。提示：${install_hint}" >&2
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
  ensure_command tmux "使用系统包管理器安装 tmux，例如 brew install tmux"
else
  if ! command -v tmux >/dev/null 2>&1; then
    echo "提示：未安装 tmux，已检测到 --server-only，将跳过 tmux 检查。" >&2
  fi
fi

if [[ $SKIP_BUILD -eq 0 ]]; then
  echo "==> 构建 tmux 面板与调度器..."
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
        echo "  -> 跳过 ${pkg}（未在 --panels 列表中）"
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
  echo "==> 跳过构建步骤，直接使用现有二进制"
fi

OPENCODE_HOME="${OPENCODE_HOME:-${HOME}/.opencode}"
export OPENCODE_SERVER="${SERVER_URL}"
export OPENCODE_SOCKET="${OPENCODE_SOCKET:-${OPENCODE_HOME}/ipc.sock}"
export OPENCODE_STATE="${OPENCODE_STATE:-${OPENCODE_HOME}/state.json}"
export OPENCODE_TMUX_CONFIG="${OPENCODE_TMUX_CONFIG:-${OPENCODE_HOME}/tmux.yaml}"

mkdir -p "${OPENCODE_HOME}"
mkdir -p "$(dirname "${OPENCODE_SOCKET}")"
mkdir -p "$(dirname "${OPENCODE_STATE}")"
mkdir -p "$(dirname "${OPENCODE_TMUX_CONFIG}")"

echo "==> 使用环境变量启动 opencode-tmux"
echo "  OPENCODE_SERVER=${OPENCODE_SERVER}"
echo "  OPENCODE_SOCKET=${OPENCODE_SOCKET}"
echo "  OPENCODE_STATE=${OPENCODE_STATE}"
echo "  OPENCODE_TMUX_CONFIG=${OPENCODE_TMUX_CONFIG}"

BIN_PATH="${REPO_ROOT}/cmd/opencode-tmux/dist/opencode-tmux"
if [[ ! -x "${BIN_PATH}" ]]; then
  echo "错误：未找到可执行文件 ${BIN_PATH}，请不要跳过构建或检查构建是否成功。" >&2
  exit 1
fi

if [[ ${#FORWARD_ARGS[@]} -gt 0 ]]; then
  exec "${BIN_PATH}" "${FORWARD_ARGS[@]}"
else
  exec "${BIN_PATH}"
fi
