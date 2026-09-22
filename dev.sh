#!/usr/bin/env bash
# 本地开发一键启动（前端 Vite Dev Server + 后端 Go 服务），保证前端热更新可用
#
# 同时拉起前后端：前端 http://localhost:5173（HMR 实时热更），后端 http://localhost:8200。
# 所有开发运行时产物（后端状态文件 sa-state.json、前后端日志）统一写入
# 项目根 dev/ 目录，避免污染代码目录。
#
# 用法：
#   ./dev.sh             前台运行，Ctrl+C 一并退出前后端
#   ./dev.sh -bg         后台运行，日志写入 dev/logs/，不占用终端
#   ./dev.sh -mock       额外启动下载器 mock：同时拉起 trmock（:9092）与
#                         qbmock（:8080），并把状态文件写成「两台都启用」，
#                        后端启动即为多下载器聚合视图（Transmission 与
#                        qBittorrent 的种子合并展示，默认连的是 trmock）；
#                        切回真实远端时去掉 -mock，并在设置里把连接地址改回真实下载器
#   ./dev.sh -stop       停掉占用 5173/8200（含 -mock 时 9092、8080）的现有进程后退出
#
# 热更新保证：
#   1. 启动前检查端口，被旧实例占用时直接报错退出（vite strictPort 下新进程
#      会静默失败，继续访问的将是不热更的旧实例），提示先 -stop 清理；
#   2. 清除 DEV_NO_HMR 环境变量（vite.config 据此关闭 HMR）；
#   3. 启动后探测 http://localhost:5173/@vite/client —— 这是 Vite HMR 客户端，
#      返回 200 才算前端就绪，否则打印日志末尾并以非零码退出。
#
# 注意：调试入口必须用 5173。8200 上是 go:embed 打包进二进制的前端快照，
# 改前端源码不会出现在那里。
set -euo pipefail

BG=0
MOCK=0
STOP=0
for arg in "$@"; do
  case "$arg" in
    -bg)   BG=1 ;;
    -mock) MOCK=1 ;;
    -stop) STOP=1 ;;
    -h|--help)
      sed -n '2,26p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "[dev] 未知参数：$arg（可用：-bg / -mock / -stop）" >&2
      exit 1
      ;;
  esac
done

ROOT="$(cd "$(dirname "$0")" && pwd)"
DEV_DIR="$ROOT/dev"
DATA_DIR="$DEV_DIR/data"
LOG_DIR="$DEV_DIR/logs"
mkdir -p "$DATA_DIR" "$LOG_DIR" "$DEV_DIR/air"   # air 为后端热重载的构建输出目录

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

# 监听某个 TCP 端口的进程 PID（lsof 优先，回退 ss / fuser）
listener_pids() {
  local port="$1"
  if command -v lsof >/dev/null 2>&1; then
    lsof -nP -ti "tcp:$port" -sTCP:LISTEN 2>/dev/null || true
  elif command -v ss >/dev/null 2>&1; then
    ss -lptn "sport = :$port" 2>/dev/null | sed -n 's/.*pid=\([0-9]*\).*/\1/p' | sort -u || true
  elif command -v fuser >/dev/null 2>&1; then
    fuser -n tcp "$port" 2>/dev/null | tr -s ' ' '\n' | grep -E '^[0-9]+$' || true
  fi
}

DEV_PORTS=(5173 8200)
if [ "$MOCK" -eq 1 ]; then DEV_PORTS+=(9092 8080); fi

# -stop 与启动预检共用的清理逻辑。
#
# 只杀「端口监听者」是不够的：air 是监督进程、自身不监听 8200，处于两次构建
# 之间或已崩溃时会被漏掉；残留的旧 air 会在下次启动时抢先重建二进制并让新实例
# 启动失败（表现为 start 后立刻报「后端未就绪」，而日志末尾是上一次运行的陈迹）。
# 因此按进程名一并兜底清理这些开发期进程。
stop_dev_processes() {
  local port pid name
  for port in "${DEV_PORTS[@]}"; do
    for pid in $(listener_pids "$port"); do
      [ -n "$pid" ] || continue
      echo "[dev] 停止占用 $port 端口的进程 PID=$pid"
      kill_tree "$pid"
    done
  done
  for name in air tm-server trmock qbmock; do
    for pid in $(pgrep -x "$name" 2>/dev/null || true); do
      [ -n "$pid" ] || continue
      echo "[dev] 停止残留进程 $name PID=$pid"
      kill_tree "$pid"
    done
  done
}

if [ "$STOP" -eq 1 ]; then
  stop_dev_processes
  echo "[dev] 清理完成"
  exit 0
fi

# 启动前端口预检：端口被占则新进程会静默失败（strictPort），必须先清理
for port in "${DEV_PORTS[@]}"; do
  pids="$(listener_pids "$port")"
  if [ -n "$pids" ]; then
    echo "[dev] 端口 $port 已被 PID=$pids 占用，拒绝启动。" >&2
    echo "[dev] 旧实例不会热更新新代码。先执行 ./dev.sh -stop 清理，再重新启动。" >&2
    exit 1
  fi
done

# 热更新保证：清掉任何禁用 HMR 的环境变量（vite.config 见 DEV_NO_HMR=1 会关 HMR）
unset DEV_NO_HMR 2>/dev/null || true

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

# 把两台 mock 写进后端的状态文件，使 -mock 启动即进入「多下载器聚合视图」。
#
# 为什么需要它：聚合的判定条件是状态文件里「启用且 URL 非空」的服务器 ≥ 2 台
# （见 internal/api/handlers.go 的 SyncAggregateTargets），而该文件只在后端
# 启动时读取一次。没有这一步的话，每次重启都要手工改文件才连得上两台。
#
# 两个必须守住的点：
#   1. 文件必须是无 BOM 的 UTF-8 —— Go 的 encoding/json 不接受 BOM，会直接
#      报 invalid character '\ufeff' 并让后端启动失败；
#   2. 只覆盖 servers / activeServer，其余字段（做种策略、自动归档等）原样保留，
#      避免把界面上调过的其它配置一并抹掉。
write_mock_state() {
  local state="$DATA_DIR/sa-state.json"
  if command -v python3 >/dev/null 2>&1; then
    SA_STATE_PATH="$state" python3 - <<'PY'
import json, os
p = os.environ["SA_STATE_PATH"]
try:
    state = json.load(open(p, encoding="utf-8"))
    if not isinstance(state, dict):
        state = {}
except Exception:
    state = {}
state["servers"] = [
    {"name": "TR (mock)", "type": "transmission",
     "url": "http://127.0.0.1:9092/transmission/rpc", "user": "", "pass": "", "enabled": True},
    {"name": "QB (mock)", "type": "qbittorrent",
     "url": "http://127.0.0.1:8080", "user": "", "pass": "", "enabled": True},
]
state["activeServer"] = 0
with open(p, "w", encoding="utf-8") as f:
    json.dump(state, f, ensure_ascii=False, indent=2)
PY
  elif command -v node >/dev/null 2>&1; then
    SA_STATE_PATH="$state" node -e '
const fs = require("fs")
const p = process.env.SA_STATE_PATH
let s = {}
try { s = JSON.parse(fs.readFileSync(p, "utf8")) || {} } catch (e) { s = {} }
s.servers = [
  { name: "TR (mock)", type: "transmission", url: "http://127.0.0.1:9092/transmission/rpc", user: "", pass: "", enabled: true },
  { name: "QB (mock)", type: "qbittorrent", url: "http://127.0.0.1:8080", user: "", pass: "", enabled: true },
]
s.activeServer = 0
fs.writeFileSync(p, JSON.stringify(s, null, 2))
'
  else
    echo "[dev] 未找到 python3 / node，跳过聚合状态写入（-mock 将只连得上 trmock）" >&2
    return 0
  fi
  echo "[dev] 已写入双下载器聚合配置（TR :9092 + QB :8080）-> $state"
}

# 下载器 mock（可选）：-mock 时同时拉起 trmock（:9092）与 qbmock（:8080），
# 并把状态文件写成「两台都启用」，后端启动后即为聚合视图（种子合并展示）。
MOCK_PID=""
QB_MOCK_PID=""
if [ "$MOCK" -eq 1 ]; then
  # SA_TYPE 必须一起设：.env.local 里保存的 SA_TYPE 优先级低于环境变量，
  # 只设 SA_URL 会在「界面里存过 qBittorrent」时配出 qbittorrent+trmock 的
  # 错配（表现为 app/preferences 404 之类的怪错），这里强制成一对。
  export SA_TYPE="transmission"
  # 用 127.0.0.1 而非 localhost：mock 只监听 IPv4，而 localhost 可能先解析到
  # IPv6 的 [::1]，那里没有监听 → 连接被拒（表现为后端轮询种子失败）。
  export SA_URL="http://127.0.0.1:9092/transmission/rpc"

  # 让后端一启动就连上两台 mock（聚合视图）。文件名与后端 state.DefaultStatePath 一致。
  write_mock_state

  echo "[dev] Mock Transmission: $SA_URL （日志 $LOG_DIR/mock.log）"
  ( cd "$ROOT/backend" && go run ./cmd/trmock ) >"$LOG_DIR/mock.log" 2>&1 &
  MOCK_PID=$!

  echo "[dev] Mock qBittorrent: http://127.0.0.1:8080 （日志 $LOG_DIR/qbmock.log）"
  ( cd "$ROOT/backend" && go run ./cmd/qbmock ) >"$LOG_DIR/qbmock.log" 2>&1 &
  QB_MOCK_PID=$!
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

cleanup() {
  echo "[dev] 停止进程..."
  if [ -n "$MOCK_PID" ];    then kill_tree "$MOCK_PID"; fi
  if [ -n "$QB_MOCK_PID" ]; then kill_tree "$QB_MOCK_PID"; fi
  kill_tree "$BACKEND_PID"
  kill_tree "$FRONTEND_PID"
  wait 2>/dev/null || true
  echo "[dev] 已停止前后端进程"
}

# 就绪探测：避免把「进程起来了但服务没就绪 / 已经崩了」当成启动成功
http_ok() {
  local url="$1"
  if command -v curl >/dev/null 2>&1; then
    curl -fsS -o /dev/null "$url" 2>/dev/null
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O /dev/null "$url" 2>/dev/null
  else
    return 0   # 两个工具都没有时不做探测，避免误报
  fi
}

wait_http() {
  # 调用方按 (url 超时秒数 名称 日志) 传参，与 dev.ps1 的 Wait-Http 一致
  local url="$1" timeout="${2:-90}" name="$3" log="$4" waited=0
  while [ "$waited" -lt "$timeout" ]; do
    if http_ok "$url"; then return 0; fi
    sleep 1
    waited=$((waited + 1))
  done
  echo "[dev] $name 启动超时（${timeout}s）：$url" >&2
  if [ -n "$log" ] && [ -f "$log" ]; then
    echo "[dev] ---- $log 末尾 ----" >&2
    tail -n 20 "$log" >&2 || true
  fi
  return 1
}

# 后端给足时间：air 冷启动要重新编译整个后端，30s 在较慢的机器上会误判失败
if ! wait_http 'http://localhost:8200/' 90 '后端' "$LOG_DIR/backend.log"; then
  cleanup; exit 1
fi
if ! wait_http 'http://localhost:5173/@vite/client' 60 '前端' "$LOG_DIR/frontend.log"; then
  cleanup; exit 1
fi
if [ -n "$QB_MOCK_PID" ]; then
  # qbmock 探测用 /api/v2/app/version：根路径固定返回 404（WebUI 接口都在
  # /api/v2/ 下），该端点无需鉴权即返回 200，是可靠的存活信号
  if ! wait_http 'http://localhost:8080/api/v2/app/version' 30 'Mock qBittorrent' "$LOG_DIR/qbmock.log"; then
    cleanup; exit 1
  fi
fi

if [ -n "$MOCK_PID" ]; then
  echo "[dev] 已启动 后端PID=$BACKEND_PID 前端PID=$FRONTEND_PID TRMock PID=$MOCK_PID QBMock PID=$QB_MOCK_PID"
else
  echo "[dev] 已启动 后端PID=$BACKEND_PID 前端PID=$FRONTEND_PID"
fi
echo "[dev] 前端 http://localhost:5173 （HMR 已就绪，调试入口用这个）"
echo "[dev] 后端 http://localhost:8200 （API 专用；其前端是打包快照，不热更）"
if [ -n "$MOCK_PID" ]; then
  echo "[dev] Mock http://127.0.0.1:9092 （Transmission）+ http://127.0.0.1:8080 （qBittorrent）"
  echo "[dev] 已进入多下载器聚合视图：两台种子合并展示（界面「下载器」列可区分）"
fi

if [ "$BG" -eq 0 ]; then
  trap cleanup EXIT INT TERM
  echo "[dev] 按 Ctrl+C 退出"
  wait
else
  echo "[dev] 后台模式已启动，日志见 $LOG_DIR"
  echo "[dev] 停止：./dev.sh -stop"
fi
