#!/usr/bin/env bash
# 本地开发一键启动（前端 Vite Dev Server + 后端 Go 服务）
# 所有开发运行时产物（后端状态文件、前后端日志）统一写入项目根 dev/ 目录，
# 避免污染代码目录。
#
# 用法：
#   ./dev.sh             前台运行，Ctrl+C 一并退出前后端
#   ./dev.sh -bg         后台运行，日志写入 dev/logs/，不占用终端
#   ./dev.sh -mock       额外启动 Transmission mock（:9092）并把后端指向它；
#                        切回真实远端时去掉 -mock，并在设置里把连接地址改回真实 Transmission
set -euo pipefail

BG=0
MOCK=0
for arg in "$@"; do
  case "$arg" in
    -bg) BG=1 ;;
    -mock) MOCK=1 ;;
  esac
done

ROOT="$(cd "$(dirname "$0")" && pwd)"
DEV_DIR="$ROOT/dev"
DATA_DIR="$DEV_DIR/data"
LOG_DIR="$DEV_DIR/logs"
mkdir -p "$DATA_DIR" "$LOG_DIR" "$DEV_DIR/air"   # air 为后端热重载的构建输出目录

echo "[dev] 开发产物目录: $DEV_DIR"
echo "[dev]   后端状态文件 -> $DATA_DIR"
echo "[dev]   后端日志     -> $LOG_DIR/backend.log"
echo "[dev]   前端日志     -> $LOG_DIR/frontend.log"

# 依赖检查，避免后台任务静默失败
for cmd in go pnpm; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "[dev] 错误：未找到 $cmd，请先安装并加入 PATH" >&2
    exit 1
  fi
done

# 后端运行时数据写入 dev/data，而非代码目录
export SA_DATA_DIR="$DATA_DIR"

# Mock Transmission（可选）：-mock 时拉起 trmock 并用 SA_URL 把后端指向它
MOCK_PID=""
if [ "$MOCK" -eq 1 ]; then
  export SA_URL="http://localhost:9092/transmission/rpc"
  echo "[dev] Mock Transmission: $SA_URL （日志 $LOG_DIR/mock.log）"
  ( cd "$ROOT/backend" && go run ./cmd/trmock ) >"$LOG_DIR/mock.log" 2>&1 &
  MOCK_PID=$!
fi

# 后端：装了 air 则启用热重载（.go 变更自动重编译重启），否则回退 go run
if command -v air >/dev/null 2>&1; then
  echo "[dev] 后端热重载：air（.go 变更自动重编译重启）"
  BACKEND_CMD="air"
else
  echo "[dev] 未检测到 air，后端改动需手动重启"
  echo "[dev]   安装：go install github.com/air-verse/air@latest"
  BACKEND_CMD="go run ./cmd/server"
fi

( cd "$ROOT/backend"  && $BACKEND_CMD ) >"$LOG_DIR/backend.log" 2>&1 &
BACKEND_PID=$!

( cd "$ROOT/frontend" && pnpm dev )           >"$LOG_DIR/frontend.log" 2>&1 &
FRONTEND_PID=$!

# go run / pnpm 会派生子进程，仅 kill 直接子 shell 会留下实际服务进程占用端口，
# 因此递归结束整棵进程树（等价于 Windows 的 taskkill /T）
kill_tree() {
  local pid="$1"
  local child
  for child in $(pgrep -P "$pid" 2>/dev/null || true); do
    kill_tree "$child"
  done
  kill "$pid" 2>/dev/null || true
}

cleanup() {
  echo "[dev] 停止进程..."
  [ -n "$MOCK_PID" ] && kill_tree "$MOCK_PID"
  kill_tree "$BACKEND_PID"
  kill_tree "$FRONTEND_PID"
  wait 2>/dev/null || true
  echo "[dev] 已停止前后端进程"
}

if [ -n "$MOCK_PID" ]; then
  echo "[dev] 已启动 后端PID=$BACKEND_PID 前端PID=$FRONTEND_PID Mock PID=$MOCK_PID"
  echo "[dev]   Mock RPC -> http://localhost:9092/transmission/rpc"
else
  echo "[dev] 已启动 后端PID=$BACKEND_PID 前端PID=$FRONTEND_PID"
fi

if [ "$BG" -eq 0 ]; then
  trap cleanup EXIT INT TERM
  echo "[dev] 前端 http://localhost:5173  后端 http://localhost:8200"
  echo "[dev] 按 Ctrl+C 退出"
  wait
else
  echo "[dev] 前端 http://localhost:5173  后端 http://localhost:8200"
  echo "[dev] 后台模式已启动，日志见 $LOG_DIR"
fi
