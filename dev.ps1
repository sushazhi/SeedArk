<#
.SYNOPSIS
  本地开发一键启动（前端 Vite Dev Server + 后端 Go 服务），保证前端热更新可用
.DESCRIPTION
  同时拉起前后端：前端 http://localhost:5173（HMR 实时热更），后端 http://localhost:8200。
  所有开发运行时产物（后端状态文件 tm-state.json、前后端日志）统一写入
  项目根 dev/ 目录，避免污染代码目录。
  -mock 额外启动下载器 mock：同时拉起 trmock（:9092）与 qbmock（:8080），
  并把状态文件写成「两台都启用」，后端启动即为多下载器聚合视图
  （Transmission 与 qBittorrent 的种子合并展示，默认连的是 trmock）。
  切回真实远端时去掉 -mock，并在设置里把连接地址改回真实下载器。
  -stop 停掉占用 5173/8200（含 -mock 时 9092、8080）的现有进程后退出。

  热更新保证：
  1. 启动前检查端口，被旧实例占用时直接报错退出（vite strictPort 下新进程
     会静默失败，继续访问的将是不热更的旧实例），提示先 -stop 清理；
  2. 清除 DEV_NO_HMR 环境变量（vite.config 据此关闭 HMR）；
  3. 启动后探测 http://localhost:5173/@vite/client —— 这是 Vite HMR 客户端，
     返回 200 才算前端就绪，否则打印日志末尾并以非零码退出。

  注意：调试入口必须用 5173。8200 上是 go:embed 打包进二进制的前端快照，
  改前端源码不会出现在那里。
#>
param(
    [switch]$bg,    # 后台模式：启动后立即返回，不占用终端（日志写入 dev/logs/）
    [switch]$mock,  # 同时启动 trmock(:9092)+qbmock(:8080)，并让后端聚合连上两台
    [switch]$stop   # 停掉占用开发端口的现有进程后退出
)

$ErrorActionPreference = "Stop"

$Root    = $PSScriptRoot
$DevDir  = Join-Path $Root "dev"
$DataDir = Join-Path $DevDir "data"
$LogDir  = Join-Path $DevDir "logs"
$AirDir  = Join-Path $DevDir "air"   # air 热重载的构建输出目录
$null    = New-Item -ItemType Directory -Force -Path $DataDir, $LogDir, $AirDir

$BackendLog  = Join-Path $LogDir "backend.log"
$FrontendLog = Join-Path $LogDir "frontend.log"

function Get-ListenerPid([int]$Port) {
    $conn = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($conn) { return $conn.OwningProcess }
    return $null
}

$devPorts = @(5173, 8200)
if ($mock) { $devPorts += 9092, 8080 }

# -stop 与启动预检共用的清理逻辑。
#
# 只杀「端口监听者」是不够的：air 是监督进程、自身不监听 8200，处于两次构建
# 之间或已崩溃时会被漏掉；残留的旧 air 会在下次启动时抢先重建二进制并让新实例
# 启动失败（表现为 start 后立刻报「后端未就绪」，而日志末尾是上一次运行的陈迹）。
# 因此按进程名一并兜底清理这些开发期进程。
$devProcNames = @("air", "tm-server", "trmock", "qbmock")

function Stop-DevProcesses([switch]$Quiet) {
    foreach ($port in $devPorts) {
        $ownerPid = Get-ListenerPid $port
        if ($ownerPid) {
            $name = (Get-Process -Id $ownerPid -ErrorAction SilentlyContinue).ProcessName
            if (-not $Quiet) {
                Write-Host "[dev] 停止占用 ${port} 端口的进程 PID=$ownerPid ($name)" -ForegroundColor Yellow
            }
            & taskkill /PID $ownerPid /T /F 2>$null | Out-Null
        }
    }
    # 端口监听者之外残留的监督进程（air 等），按进程名兜底
    foreach ($procName in $devProcNames) {
        Get-Process -Name $procName -ErrorAction SilentlyContinue | ForEach-Object {
            if (-not $Quiet) {
                Write-Host "[dev] 停止残留进程 $procName PID=$($_.Id)" -ForegroundColor Yellow
            }
            & taskkill /PID $_.Id /T /F 2>$null | Out-Null
        }
    }
}

if ($stop) {
    Stop-DevProcesses
    Write-Host "[dev] 清理完成" -ForegroundColor Cyan
    exit
}

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
function Initialize-MockAggregationState([string]$StatePath) {
    $trUrl = "http://127.0.0.1:9092/transmission/rpc"
    $qbUrl = "http://127.0.0.1:8080"

    $state = $null
    if (Test-Path $StatePath) {
        try {
            $state = Get-Content $StatePath -Raw -Encoding UTF8 | ConvertFrom-Json
        } catch {
            Write-Host "[dev] 状态文件解析失败，将重建：$StatePath" -ForegroundColor Yellow
            $state = $null
        }
    }
    if ($null -eq $state) { $state = New-Object psobject }

    # 只改这两个属性，其余保持原值（用 PSObject 属性表合并，避免丢字段）
    $mockServers = @(
        [pscustomobject]@{ name = "TR (mock)"; type = "transmission"; url = $trUrl; user = ""; pass = ""; enabled = $true },
        [pscustomobject]@{ name = "QB (mock)"; type = "qbittorrent";  url = $qbUrl; user = ""; pass = ""; enabled = $true }
    )
    if ($state.PSObject.Properties.Name -contains "servers") {
        $state.servers = $mockServers
    } else {
        $state | Add-Member -NotePropertyName servers -NotePropertyValue $mockServers
    }
    if ($state.PSObject.Properties.Name -contains "activeServer") {
        $state.activeServer = 0
    } else {
        $state | Add-Member -NotePropertyName activeServer -NotePropertyValue 0
    }

    $json = $state | ConvertTo-Json -Depth 20
    [System.IO.File]::WriteAllText($StatePath, $json, [System.Text.UTF8Encoding]::new($false))
    Write-Host "[dev] 已写入双下载器聚合配置（TR :9092 + QB :8080）-> $StatePath" -ForegroundColor DarkGray
}

# 启动前端口预检：端口被占则新进程会静默失败（strictPort），必须先清理
foreach ($port in $devPorts) {
    $ownerPid = Get-ListenerPid $port
    if ($ownerPid) {
        $name = (Get-Process -Id $ownerPid -ErrorAction SilentlyContinue).ProcessName
        Write-Host "[dev] 端口 $port 已被 PID=$ownerPid ($name) 占用，拒绝启动。" -ForegroundColor Red
        Write-Host "[dev] 旧实例不会热更新新代码。先执行  .\dev.ps1 -stop  清理，再重新启动。" -ForegroundColor Yellow
        exit 1
    }
}

# 热更新保证：清掉任何禁用 HMR 的环境变量（vite.config 见 DEV_NO_HMR=1 会关 HMR）
Remove-Item Env:DEV_NO_HMR -ErrorAction SilentlyContinue

Write-Host "[dev] 开发产物目录: $DevDir"              -ForegroundColor Cyan
Write-Host "[dev]   后端状态文件 -> $DataDir"         -ForegroundColor DarkGray
Write-Host "[dev]   后端日志     -> $BackendLog"      -ForegroundColor DarkGray
Write-Host "[dev]   前端日志     -> $FrontendLog"     -ForegroundColor DarkGray

# 后端运行时数据（tm-state.json / .env.local 等）写入 dev/data，而非代码目录
$env:TM_DATA_DIR = $DataDir

# 下载器 mock（可选）：-mock 时同时拉起 trmock（:9092）与 qbmock（:8080），
# 并把状态文件写成「两台都启用」，后端启动后即为聚合视图（种子合并展示）。
$MockLog = Join-Path $LogDir "mock.log"
$QBMockLog = Join-Path $LogDir "qbmock.log"
$mockProcess = $null
$qbMockProcess = $null
if ($mock) {
    # TR_TYPE 必须一起设：.env.local 里保存的 TR_TYPE 优先级低于环境变量，
    # 只设 TR_URL 会在「界面里存过 qBittorrent」时配出 qbittorrent+trmock 的
    # 错配（表现为 app/preferences 404 之类的怪错），这里强制成一对。
    $env:TR_TYPE = "transmission"
    # 用 127.0.0.1 而非 localhost：mock 只监听 IPv4，而本机 localhost 会先解析到
    # IPv6 的 [::1]，那里没有监听 → 连接被拒（表现为后端轮询种子失败）。
    $env:TR_URL  = "http://127.0.0.1:9092/transmission/rpc"

    # 让后端一启动就连上两台 mock（聚合视图）。文件名与后端 state.DefaultStatePath 一致。
    Initialize-MockAggregationState (Join-Path $DataDir "tm-state.json")

    Write-Host "[dev] Mock Transmission: $($env:TR_URL) （日志 $MockLog）" -ForegroundColor DarkGray
    $mockProcess = Start-Process -WindowStyle Hidden -FilePath "cmd.exe" `
        -WorkingDirectory (Join-Path $Root "backend") `
        -ArgumentList "/c", "go run ./cmd/trmock > `"$MockLog`" 2>&1" `
        -PassThru

    Write-Host "[dev] Mock qBittorrent: http://localhost:8080 （日志 $QBMockLog）" -ForegroundColor DarkGray
    $qbMockProcess = Start-Process -WindowStyle Hidden -FilePath "cmd.exe" `
        -WorkingDirectory (Join-Path $Root "backend") `
        -ArgumentList "/c", "go run ./cmd/qbmock > `"$QBMockLog`" 2>&1" `
        -PassThru
}

# 后端：装了 air 则启用热重载（.go 变更自动重编译重启），否则回退 go run
$backendCmd = if (Get-Command air -ErrorAction SilentlyContinue) {
    Write-Host "[dev] 后端热重载：air（.go 变更自动重编译重启）"    -ForegroundColor DarkGray
    "air > `"$BackendLog`" 2>&1"
} else {
    Write-Host "[dev] 未检测到 air，后端改动需手动重启"             -ForegroundColor Yellow
    Write-Host "[dev]   安装：go install github.com/air-verse/air@latest" -ForegroundColor Yellow
    "go run ./cmd/server > `"$BackendLog`" 2>&1"
}

# 说明：Start-Process 不允许 stdout/stderr 重定向到同一文件，故经 cmd /c 合并重定向
$backend = Start-Process -WindowStyle Hidden -FilePath "cmd.exe" `
    -WorkingDirectory (Join-Path $Root "backend") `
    -ArgumentList "/c", $backendCmd `
    -PassThru

$frontend = Start-Process -WindowStyle Hidden -FilePath "cmd.exe" `
    -WorkingDirectory (Join-Path $Root "frontend") `
    -ArgumentList "/c", "pnpm dev > `"$FrontendLog`" 2>&1" `
    -PassThru

if ($mockProcess) {
    Write-Host "[dev] 已启动  后端 PID=$($backend.Id)  前端 PID=$($frontend.Id)  TRMock PID=$($mockProcess.Id)  QBMock PID=$($qbMockProcess.Id)" -ForegroundColor Green
    Write-Host "[dev]   Mock RPC -> http://localhost:9092/transmission/rpc  |  http://localhost:8080" -ForegroundColor DarkGray
} else {
    Write-Host "[dev] 已启动  后端 PID=$($backend.Id)  前端 PID=$($frontend.Id)" -ForegroundColor Green
}

# 就绪探测：前端必须等到 HMR 客户端可访问，后端等到 HTTP 可达；失败打印日志末尾
function Wait-Http([string]$Url, [int]$TimeoutSec, [string]$Name, [string]$LogFile) {
    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    while ((Get-Date) -lt $deadline) {
        $code = & curl.exe -s -o NUL -w '%{http_code}' --max-time 2 $Url 2>$null
        if ($code -match '^\d{3}$' -and $code -ne '000') { return $true }
        Start-Sleep -Milliseconds 500
    }
    Write-Host "[dev] $Name 未就绪：$Url。日志末尾：" -ForegroundColor Red
    if (Test-Path $LogFile) {
        Get-Content $LogFile -Tail 15 | ForEach-Object { Write-Host "    $_" -ForegroundColor DarkGray }
    }
    return $false
}

$frontOk  = Wait-Http 'http://localhost:5173/@vite/client' 30 '前端 Vite（HMR 客户端）' $FrontendLog
# 后端给足时间：air 冷启动要重新编译整个后端，30s 在较慢的机器上会误判失败
$backOk   = Wait-Http 'http://localhost:8200/'             90 '后端'                    $BackendLog

# qbmock 探测用 /api/v2/app/version：根路径固定返回 404（WebUI 接口都在 /api/v2/ 下），
# 该端点无需鉴权即返回 200，是可靠的存活信号
$qbOk = $true
if ($qbMockProcess) {
    $qbOk = Wait-Http 'http://localhost:8080/api/v2/app/version' 30 'Mock qBittorrent' $QBMockLog
}

if (-not ($frontOk -and $backOk -and $qbOk)) {
    & taskkill /PID $backend.Id  /T /F 2>$null
    & taskkill /PID $frontend.Id /T /F 2>$null
    if ($mockProcess)   { & taskkill /PID $mockProcess.Id   /T /F 2>$null }
    if ($qbMockProcess) { & taskkill /PID $qbMockProcess.Id /T /F 2>$null }
    Write-Host "[dev] 启动失败，已回滚本次拉起的进程。" -ForegroundColor Red
    exit 1
}

Write-Host "[dev] 前端 http://localhost:5173 （HMR 已就绪，调试入口用这个）" -ForegroundColor Green
Write-Host "[dev] 后端 http://localhost:8200 （API 专用；其前端是打包快照，不热更）" -ForegroundColor Green
if ($mockProcess) {
    Write-Host "[dev] Mock http://127.0.0.1:9092 （Transmission）+ http://127.0.0.1:8080 （qBittorrent）" -ForegroundColor Green
    Write-Host "[dev] 已进入多下载器聚合视图：两台种子合并展示（界面「下载器」列可区分）" -ForegroundColor Green
}
if ($bg) {
    Write-Host "[dev] 后台模式已启动，日志见 dev/logs/" -ForegroundColor Green
    exit
}

Write-Host "[dev] 按 Ctrl+C 退出（将同时结束前后端进程）"                     -ForegroundColor Yellow

try {
    while (-not $backend.HasExited -and -not $frontend.HasExited) {
        Start-Sleep -Seconds 1
    }
} finally {
    # cmd.exe 是父进程，需连同子进程树一起结束
    if ($mockProcess)   { & taskkill /PID $mockProcess.Id   /T /F 2>$null }
    if ($qbMockProcess) { & taskkill /PID $qbMockProcess.Id /T /F 2>$null }
    & taskkill /PID $backend.Id  /T /F 2>$null
    & taskkill /PID $frontend.Id /T /F 2>$null
    Write-Host "[dev] 已停止前后端进程" -ForegroundColor Cyan
}
