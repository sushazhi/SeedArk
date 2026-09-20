<div align="center">

# trpanel

**基于 Go + React 重构的 Transmission 现代化 Web 管理面板**
*专为飞牛 OS（fnOS）打造 · 前后端一体交付*

<br>

[![Go](https://img.shields.io/badge/Go-1.27%2B-00ADD8?style=flat-square&logo=go&logoColor=white)](https://go.dev)
[![React](https://img.shields.io/badge/React-19-61DAFB?style=flat-square&logo=react&logoColor=black)](https://react.dev)
[![PWA](https://img.shields.io/badge/PWA-Ready-5A0FC8?style=flat-square&logo=pwa&logoColor=white)]()
[![fnOS](https://img.shields.io/badge/Optimized%20for-fnOS-FF6B35?style=flat-square)]()
[![License](https://img.shields.io/badge/license-MIT-green?style=flat-square)](./LICENSE)
[![Release](https://img.shields.io/badge/release-v0.1.0-blue?style=flat-square)](../../releases)
[![Docker Hub](https://img.shields.io/badge/Docker%20Hub-jianhuayanyu%2Ftrpanel-2496ED?style=flat-square&logo=docker&logoColor=white)](https://hub.docker.com/r/jianhuayanyu/trpanel)
[![GHCR](https://img.shields.io/badge/GHCR-trpanel-2496ED?style=flat-square&logo=docker&logoColor=white)](https://github.com/sushazhi/trpanel/pkgs/container/trpanel)
[![在线预览](https://img.shields.io/badge/在线预览-Demo-8A2BE2?style=flat-square&logo=data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCAyNCAyNCIgZmlsbD0id2hpdGUiPjxwYXRoIGQ9Ik0xMiAyQTEwIDEwIDAgMSAwIDIyIDEyIDEwIDEwIDAgMCAwIDEyIDJ6bTAgMmE4IDggMCAxIDEgOCA4IDggOCAwIDEgLTggOHptLTEtMTJ2NGw0LTQtNC00eiIvPjwvc3ZnPg==)](https://sushazhi.github.io/trpanel/)

</div>

---

## ✨ 核心功能

| | 功能 | 说明 |
|:--|:--|:--|
| 🧲 | **种子管理** | 添加（`.torrent` / 磁力 / 批量）、开始 / 暂停 / 删除、优先级、标签、队列调整，**全操作支持批量** |
| 📁 | **文件操作** | 文件树设优先级、重命名，自动重新校验；按路径添加种子受白名单保护 |
| ⚡ | **实时推送** | WebSocket 推送速度 / 进度 / Peers / Tracker 状态 / 块位图 |
| 🔍 | **模糊搜索** | 忽略大小写、空格与标点，保留字母 / 数字 / 中日韩文字，`m team` 也能命中 `M-Team` |
| 🧰 | **多维筛选 / 排序** | 状态 / 标签 / 站点 / 目录 / 错误状态 + 状态优先级 + 二级字段多级排序 |
| 🖥️ | **桌面交互** | 鼠标虚拟滚动表格 + 右键菜单 + 拖拽排序 + 可伸缩侧栏（持久化） + 表头右键管理列 |
| 📱 | **触屏交互** | 玻璃卡片列表，长按 / ⋮ 唤出菜单，左右滑切换分类，勾选进入批量，动作条"全选"补齐分组，底部悬浮胶囊承载添加 / 启停 / 清理 |
| 🌐 | **会话与全局** | 多服务器切换、全局限速、带宽定时调度、下载 / 做种队列、轮询间隔、**带宽组管理**（Transmission 4.x） |
| 🔀 | **多下载器聚合** | 支持 **Transmission 与 qBittorrent**（Web API v2，qBittorrent 4.x / 5.x，含 5.2+ API Key）；「设置 → 多服务器管理」启用 2 台及以上即进入聚合视图：种子列表合并展示、批量操作按种子自动路由到所属下载器、统计与站点维度跨服务器汇总 |
| 🛠 | **工具集** | 浏览器端 `.torrent` 创建（bencode + 分片 SHA1）、**服务器路径建种**（后端多线程哈希，大文件秒级完成）、按分享率 / 做种时长批量清理已完成种子 |
| 🤖 | **自动化** | 已完成种子按站点归档；**做种策略**按站点分享率 / 做种天数 / 上传量目标达标后暂停 / 删除 / 删除并清理文件 |
| 🤝 | **MCP 服务** | 内置 MCP 端点（`/mcp`），Claude / Qoder 等 AI 客户端可直接查询种子 / 站点 / 会话配置与磁盘空间，执行添加、启停、校验、重宣告、限速、打标签、队列调整等操作；「设置 → 自动化 → MCP 服务」开启，独立接入令牌鉴权，删除与移动 / 重命名 / 立即执行做种策略等高危操作默认关闭 |
| 📈 | **图表统计** | 速度历史曲线 + 统计仪表盘 |
| 📲 | **PWA** | 可安装、离线缓存、新版本更新提示、键盘快捷键（`N` / `Space` / `Delete` / `Ctrl+A` / `/` / `Esc` / `Ctrl±`） |
| 🎨 | **外观** | 明 / 暗主题、中 / 英文、**可调玻璃浓度**、**背景壁纸**（作为玻璃折射色源形成色彩流动） |
| 🍃 | **飞牛 OS 深度集成** | 原生文件选择器、主题 / 语言同步、"打开所在文件夹"、应用更新（fpk）、问题反馈入口 |
| 🔌 | **宿主平台抽象** | 后端 `internal/platform/` + 前端 `src/platform/`，同一套代码跑 Docker / 物理机 / fnOS，新宿主零业务改动 |

---

## 📸 预览

### 🌐 在线体验

无需安装，打开即用，数据在浏览器内模拟生成、可完整交互：

| | 地址 | 说明 |
|:--|:--|:--|
| 🖥️ | **[电脑版预览](https://sushazhi.github.io/trpanel/)** | 桌面布局：虚拟滚动表格、右键菜单、拖拽排序 |
| 📱 | **[移动版预览](https://sushazhi.github.io/trpanel/mobile.html)** | 手机壳样式的移动版预览；真机打开自动进入应用本体 |

> 演示模式为纯前端构建（`pnpm build:demo`），不连接任何后端；推送到 `master` 自动部署（[工作流](.github/workflows/demo-pages.yml)）。首次启用需在仓库 **Settings → Pages** 将 Source 设为 **GitHub Actions**。

<table align="center">
  <tr>
    <th align="center">桌面 · 浅色</th>
    <th align="center">桌面 · 深色</th>
  </tr>
  <tr>
    <td><img src="docs/screenshots/desktop-light.png" alt="Desktop Light" /></td>
    <td><img src="docs/screenshots/desktop-dark.png" alt="Desktop Dark" /></td>
  </tr>
</table>

<table align="center">
  <tr>
    <th align="center">移动端</th>
  </tr>
  <tr>
    <td align="center"><img src="docs/screenshots/mobile-dark.png" alt="Mobile" width="380" /></td>
  </tr>
</table>

> 截图基于内置 Transmission Mock（`./dev.ps1 -mock`）生成，14 个种子覆盖各种状态。

---

## 🚀 快速开始

> trpanel **是一个独立的 Transmission 管理面板**，需要你已有一台运行中的 Transmission（已开启 RPC）。它通过浏览器提供增强界面；**并非 Transmission 本体，也不通过 `TRANSMISSION_WEB_HOME` 替换其自带 Web 界面**。

### ✅ 前置条件

- 一台运行中的 Transmission，**已开启 RPC**（默认 `http://localhost:9091/transmission/rpc`）
- 与 Transmission 网络互通（同机 / 局域网 / 容器网络皆可）

### 🐳 方式一：Docker 部署（推荐）

```bash
docker run -d --name trpanel \
  -p 8200:8200 \
  -e TR_TYPE=transmission \
  -e TR_URL=http://host.docker.internal:9091/transmission/rpc \
  -e API_TOKEN=change-me \
  -v trpanel-data:/data \
  jianhuayanyu/trpanel:latest
```

也可以直接用仓库自带的 [`docker-compose.yml`](docker-compose.yml)：

```bash
API_TOKEN=change-me docker compose up -d
```

| 项 | 值 |
|:--|:--|
| 镜像 | `jianhuayanyu/trpanel`（Docker Hub，推荐）／`ghcr.io/sushazhi/trpanel`（GHCR） |
| 架构 | `linux/amd64`、`linux/arm64` |
| 标签 | `v1.2.3`（与仓库 Git 标签完全一致）、`latest`（当前最新版本的镜像） |
| 数据卷 | `/data`（即 `TM_DATA_DIR`，存放 `tm-state.json` 与界面保存的连接配置） |
| 健康检查 | `GET /` 每 30s |

> ⚠️ 容器内 `SERVER_HOST=0.0.0.0`，**未设置 `API_TOKEN` 时服务会拒绝启动**（防止局域网裸奔）；令牌在浏览器首次访问时粘贴一次即可，之后持久化在本地。
> 镜像由 [Docker 工作流](.github/workflows/docker.yml)自动构建发布：仅推送 `v*` 标签触发多架构构建；PR 只验证构建不推送。同一次构建会同时推送到 Docker Hub 与 GHCR，**两个仓库的镜像内容完全一致**，拉取哪个都行（Docker Hub 在国内通常更快）。
> GHCR 包首次发布后需在仓库 **Packages → Package settings** 中把可见性改为 Public，才能匿名拉取；Docker Hub 推送需在仓库 Secrets 中配置 `DOCKERHUB_USERNAME` / `DOCKERHUB_TOKEN`（目标仓库默认为 `jianhuayanyu/trpanel`，可用仓库变量 `DOCKERHUB_REPO` 覆盖）。

### 📦 方式二：二进制 / 源码构建

#### 1️⃣ 获取程序

- **直接下载**：在 [Releases](../../releases) 下载 `trpanel-*.tar.gz`，解压即用
- **自行构建**：

  ```bash
  cd backend && go build -o trpanel ./cmd/server
  ```

#### 2️⃣ 配置连接

任选其一（优先级：**环境变量 > `.env.local` > `config.yaml` > 默认值**）：

<details>
<summary><b>环境变量</b>（启动前 export）</summary>

```bash
export TR_URL=http://<transmission-ip>:9091/transmission/rpc
export TR_USER=admin
export TR_PASS=password
```
</details>

<details>
<summary><b>配置文件</b>（以 <code>backend/config.example.yaml</code> 为模板）</summary>

```yaml
# backend/config.yaml
server:
  port: 8200
transmission:
  url: "http://<transmission-ip>:9091/transmission/rpc"
  username: "admin"
  password: "password"
```
</details>

<details>
<summary><b>界面设置</b>（启动后填写，热更新无需重启）</summary>

「设置」→ 连接地址 → 填写 RPC 地址 / 账号 → 保存。
</details>

#### 3️⃣ 运行

```bash
./trpanel        # Linux / macOS
trpanel.exe      # Windows
```

#### 4️⃣ 访问

浏览器打开 **`http://<服务器IP>:8200`**，连接成功后即可添加 / 管理种子。

#### 🔧 （可选）从源码构建前端并嵌入

```bash
cd frontend && pnpm install && pnpm build
cd ../backend
Copy-Item ..\frontend\dist\* web\dist\ -Recurse -Force   # PowerShell
# cp -r ../frontend/dist/* web/dist/                      # Linux/macOS
go build -o trpanel ./cmd/server
```

---

## 🛠 本地开发

项目根提供 `dev.ps1`（Windows）与 `dev.sh`（Linux/macOS）一键拉起前后端：

```bash
.\dev.ps1          # Windows：前台运行，Ctrl+C 一并退出
.\dev.ps1 -bg      # Windows：后台运行，日志写入 dev/logs/
./dev.sh           # Linux / macOS：前台运行
./dev.sh -bg       # Linux / macOS：后台运行
```

启动后：

| 服务 | 地址 |
|:--|:--|
| 前端（Vite） | http://localhost:5173（已代理 `/api`、`/ws` 到后端） |
| 后端（Go） | http://localhost:8200 |

### 🔥 热更新（默认开启）

- **前端**：Vite HMR + React Fast Refresh，保存 `.tsx` / `.ts` / CSS 即热替换，尽量保留组件状态
- **后端**：检测到 `air` 时启用 Go 热重载，保存 `.go` 自动重编译重启（前端 WebSocket 自动重连）
  ```bash
  go install github.com/air-verse/air@latest
  ```

特殊环境开关（环境变量）：

| 变量 | 作用 |
|:--|:--|
| `DEV_NO_HMR=1` | 关闭前端 HMR（内嵌 WebView 等环境会阻断 WebSocket） |
| `DEV_WATCH_POLL=1` | 文件监听改用轮询（网络盘 / 虚拟机共享目录 / WSL 挂载） |

### 📂 运行时产物

所有开发运行时产物统一写入项目根 `dev/`：

```
dev/
├── data/        # tm-state.json、界面保存的连接配置 .env.local
└── logs/        # backend.log、frontend.log、mock.log
```

> `dev/` 已在 `.gitignore` 中整体忽略。后端数据目录通过 `TM_DATA_DIR` 指向 `dev/data`；生产部署不设置时仍使用默认 `~/.trpanel`。

### 🧪 没有可用的 Transmission？用内置 Mock

仓库自带说 Transmission RPC 协议的 mock（`backend/cmd/trmock`），内置覆盖各状态的种子数据并实时推进进度 / 速度：

```bash
./dev.sh -mock       # Linux / macOS
.\dev.ps1 -mock      # Windows（mock 监听 :9092）
```

单独运行：`cd backend && go run ./cmd/trmock`（`127.0.0.1:9092`，`-h` 查看 `-seed` / `-tick` / `-static` 等选项）。

**运行时在 mock 与真实远端之间热切换**：

- 界面切换：设置 → 连接地址，填 `http://localhost:9092/transmission/rpc`（mock）或真实远端，保存即生效
- 启动时指定：`TR_URL=http://localhost:9092/transmission/rpc`

---

## 📖 使用指南

### 首次连接

1. 浏览器访问 `http://<服务器IP>:8200`（本机 `http://localhost:8200`）
2. 若 Transmission 不在本机或开启了认证，打开「设置」（桌面顶栏右侧头像菜单 / 移动侧滑抽屉底部）填写 RPC 地址、用户名、密码并保存；亦可环境变量 / `config.yaml` 预先配置
3. 连接成功后主界面实时显示速度、进度与种子列表

### 添加任务

- 点击顶栏「+」（移动端为底部悬浮胶囊的「+」）或按 `N` 打开添加面板
- 拖入 `.torrent` 文件或粘贴磁力链接：自动解析并预览文件列表与体积
- 支持多文件、批量磁力，解析后立即校验

### 日常管理

- **选中**：`Ctrl+A` 或动作条首颗「全选」按钮选中当前筛选结果，`Esc` 取消
- **操作**：`Space` 开始 / 暂停，`Delete` 删除（弹窗含名称列表）
- **桌面**：拖拽行调整队列顺序；拖拽侧边栏调宽（自动持久化）；表头右键管理列显隐 / 顺序
- **移动**：点卡片打开详情，长按或 ⋮ 弹菜单；左右滑切分类；勾选进入批量；底部悬浮胶囊承载添加 / 全部启停 / 清理
- **右键菜单**：强制开始、队列调整、优先级、Tracker 批量替换、打开所在文件夹（飞牛环境）

### 过滤与排序

顶部搜索框（`/` 聚焦）按名称 / 哈希模糊检索，配合状态 / 标签 / 站点 / 下载目录多维组合筛选；支持按状态优先级 + 二级字段（完成时间、上传 / 下载量等）多级排序。

### 会话与全局设置

「设置」面板内可切换多服务器、查看会话统计、配置备用带宽定时调度、全局限速、下载 / 做种队列、轮询间隔，以及清除目录历史。

### 主题、语言与外观

右上角切换明 / 暗主题与中文 / 英文（偏好持久化）；飞牛 OS 中自动跟随系统主题。

外观（设置 → 外观）：

- **玻璃浓度**：调节玻璃材质的填充通透度。浓度越低，玻璃越通透，模糊与饱和折射同步增强
- **背景壁纸**：可选一张本地图片（自动压缩到长边 1920 的 JPEG，约 200–400KB），铺满全屏作为玻璃的折射色源，形成随玻璃流动的色彩；可读性洗罩层保证文字仍可辨识

### 宿主平台（fnOS / 通用部署）

核心功能不绑定任何系统，飞牛特性全部收敛在「宿主平台」一层：

- **后端** `internal/platform/`：平台提供安全策略（iframe 嵌入 / 信任转发头）、本地文件读取白名单，以及自身专属路由（如 fnOS 应用更新接口）
- **前端** `src/platform/`：平台声明自己具备哪些**能力**（文件选择器、打开目录、应用更新……），UI 只按能力显示入口，不支持时自动隐藏或降级为复制路径

同一套代码可直接跑 Docker / 物理机 / 任意 Linux（默认 `platform=generic`），飞牛专属入口自动消失，其余功能完全可用。

部署到 fnOS 时（`platform=fnos` 或检测到宿主注入的环境变量自动切换）额外启用：

- 文件选择器选择下载目录
- 在系统文件管理器中定位目录
- 系统主题 / 语言同步
- 「设置 → 关于」中的应用更新（下载 GitHub Release 的 fpk 包到应用中心安装）
- 问题反馈入口（界面 / 功能问题反馈到 `trpanel`；fpk 安装 / 应用更新等问题反馈到 `fnos-transmission`）

新增一套宿主只需：后端实现 `platform.Platform` 接口并在 `init` 中 `Register`，前端实现 `HostPlatform` 接口并声明能力——**业务代码一行都不用改**。

### MCP（AI 客户端接入）

trpanel 内置 MCP（Model Context Protocol）服务，AI 客户端可通过自然语言管理 Transmission。推荐直接在 Web「设置 → 自动化 → MCP 服务」中开启并配置（即时生效，保存后写入 `.env.local`）；也可在配置文件中设置启动初值：

```yaml
mcp_enabled: true          # 启用 MCP 端点 /mcp
mcp_allow_delete: false    # 删除类工具默认关闭，需显式开启
mcp_allow_dangerous: false # 移动 / 重命名 / 立即执行做种策略等其它高危操作，默认关闭
mcp_token: ""              # 接入令牌，留空 = 不启用鉴权
```

客户端接入示例（Claude Desktop / Qoder / Cursor 等支持 streamable HTTP 的 MCP 客户端）：

```json
{
  "mcpServers": {
    "trpanel": {
      "url": "http://192.168.1.10:8200/mcp",
      "headers": { "Authorization": "Bearer <你的 MCP 接入令牌>" }
    }
  }
}
```

- **鉴权**：`/mcp` 使用独立接入令牌 `MCP_TOKEN`（与 `API_TOKEN` 互不相干），在 Web「设置 → 自动化 → MCP 服务」中即可配置，修改后即时生效——记得同步更新 AI 客户端配置；留空表示不启用鉴权，此时仅建议在回环 / 内网环境使用。飞牛应用经统一网关访问界面（登录态即鉴权），但外部 AI 客户端无法通过网关鉴权——需直连服务端口
- **直连端口**：`MCP_PORT` 不经过宿主网关的统一认证，因此**必须同时配置 `MCP_TOKEN` 才会启动**（未配置时只记一条错误日志并跳过该端口，界面功能不受影响）；运行期清空令牌后，该端口的所有请求一律被拒绝

**工具清单**（按风险分级，高危工具需在设置界面显式开启）：

| 级别 | 工具 | 说明 |
|:--|:--|:--|
| 🔍 只读 | `list_torrents` | 列出种子，支持关键词 / 状态 / 站点过滤 |
| 🔍 只读 | `get_torrent` | 单个种子详情（文件 / Peers / Tracker 状态） |
| 🔍 只读 | `get_stats` | 会话统计：版本、种子数、当前与累计上传 / 下载量 |
| 🔍 只读 | `get_free_space` | 查询目录剩余空间（留空用全局下载目录） |
| 🔍 只读 | `get_session_config` | 会话配置摘要：下载目录 / 限速 / 队列 / 黑名单等 |
| 🔍 只读 | `test_port` | 监听端口外网可达性检测 |
| 🔍 只读 | `get_torrent_sites` | 站点汇总与种子归属 |
| 🔍 只读 | `get_seed_policy_report` | 做种策略规则 + 已达标种子及依据（只读评估，不执行） |
| ✏️ 写操作 | `add_torrent` | 磁力 / URL / 白名单路径添加，默认以暂停状态添加 |
| ✏️ 写操作 | `start_torrents` / `stop_torrents` | 批量开始 / 暂停 |
| ✏️ 写操作 | `verify_torrents` | 批量触发本地数据校验 |
| ✏️ 写操作 | `reannounce_torrents` | 批量重新宣告 Tracker |
| ✏️ 写操作 | `set_torrent_labels` | 批量设置标签（整体覆盖） |
| ✏️ 写操作 | `set_torrent_limits` | 单种上传 / 下载限速（KB/s）与是否遵循全局限速 |
| ✏️ 写操作 | `queue_move` | 队列排序（top / up / down / bottom） |
| ✏️ 写操作 | `update_blocklist` | 更新黑名单规则 |
| ✏️ 写操作 | `set_session_config` | 修改会话设置：下载目录（仅影响新添加的种子）、限速（全局 / 备用含定时）、队列、网络（端口 / 加密 / DHT / PEX 等）、做种策略默认值、黑名单、磁盘缓存 |
| ⚠️ 高危 | `remove_torrents` | 批量删除种子，`deleteData=true` 连同本地文件（需开启「允许通过 MCP 删除种子」） |
| ⚠️ 高危 | `move_torrents` | 批量移动数据目录，可实际搬移文件（需开启「允许通过 MCP 执行高危操作」） |
| ⚠️ 高危 | `rename_file` | 重命名种子内文件 / 目录（需开启「允许通过 MCP 执行高危操作」） |
| ⚠️ 高危 | `execute_seed_policy` | 立即执行一轮做种策略，按规则暂停或删除（需开启「允许通过 MCP 执行高危操作」） |
| 🧩 透传 | `transmission_api_request` | 通用透传：直接调用 Transmission 官方 RPC 白名单方法（含库未封装能力）。只读方法直接可用；写方法需开启「允许通过 MCP 执行高危操作」；`session-close` 与脚本类配置永久屏蔽 |

### 进阶

- **PWA 安装**：手机浏览器或桌面 Chrome / Edge 选择「添加到主屏幕 / 安装应用」，可全屏使用并离线缓存，有新版本会提示刷新
- **Peer 地理位置**：启动时自动从镜像下载 GeoLite2 数据库到数据目录（仅首次，约 60MB）；也可手动将 `GeoLite2-City.mmdb` 放入 `backend/mmdb/`（手动放置优先生效）
- **浏览器内做种**：工具集「创建种子」可在本地用 bencode 分片 SHA1 生成 `.torrent`，生成后可一键添加
- **批量清理**：按分享率 / 做种时长过滤，批量清理已完成种子
- **做种策略**：「设置 → 自动化 → 做种策略」按站点设置分享率 / 做种天数 / 上传量目标，达标后自动暂停、删除种子（保留文件）或删除种子及文件；默认只生成待处理清单，需手动开启「自动执行」；下载未完成、本地报错、所有 tracker 都没 announce 成功的种子一律跳过；另有全局最低做种时长与站点 / 标签排除名单；站点匹配口径与「自动文件管理」「侧边栏站点分组」一致——**按站点名或简称（如 `m-team`）匹配，而非 tracker 主机名**

---

## 🌐 环境变量

| 变量 | 默认值 | 说明 |
|:--|:--|:--|
| `TR_TYPE` | `transmission` | 下载器类型：`transmission` / `qbittorrent`（qBittorrent 下 `TR_URL` 填 WebUI 根地址，密码可填 5.2+ API Key） |
| `TR_URL` | `http://localhost:9091/transmission/rpc` | 下载器 RPC 端点 |
| `TR_USER` | 空 | RPC 用户名 |
| `TR_PASS` | 空 | RPC 密码 |
| `SERVER_PORT` | `8200` | 本服务端口 |
| `SERVER_HOST` | `127.0.0.1` | 监听地址（非回环地址需配置 `API_TOKEN`） |
| `API_TOKEN` | 空 | REST 接口访问令牌（不含 `/mcp`）；非空时浏览器首次访问弹出令牌输入框 |
| `POLL_INTERVAL` | `2s` | WebSocket 轮询间隔 |
| `LOG_LEVEL` | `info` | 日志级别 |
| `TM_PLATFORM` | 自动推断 | 宿主平台：`generic`（默认） / `fnos` |
| `GATEWAY_PREFIX` | 空 | 宿主网关挂载的 URL 前缀（如 `/app/transmission`） |
| `TORRENT_PATH_ROOTS` | `/vol,/mnt,/media,/volume1` | 「按路径添加种子」允许读取的根目录（逗号分隔；按解析符号链接后的真实路径判定，仅允许普通文件） |
| `PATH_MAPPINGS` | 空 | 远端→本地路径映射（逗号分隔，每项 `远端路径=本地路径`）；Transmission 跑在容器内等路径不一致场景，用于「打开所在文件夹」「复制路径」的展示转换 |
| `SERVER_SOCKET` | 空 | Unix socket 监听路径（宿主网关接入用） |
| `MCP_ENABLED` | `false` | 启用 MCP 服务（`/mcp` 端点，供 AI 客户端接入） |
| `MCP_ALLOW_DELETE` | `false` | 允许通过 MCP 删除种子（`deleteData=true` 时连同本地文件） |
| `MCP_ALLOW_DANGEROUS` | `false` | 允许通过 MCP 执行其它高危操作（移动 / 重命名种子文件、立即执行做种策略） |
| `MCP_TOKEN` | 空 | MCP 接入令牌（独立于 `API_TOKEN`，仅作用于 `/mcp`）；留空 = 不启用鉴权（**开启 `MCP_PORT` 时必填**） |
| `MCP_PORT` | 空 | MCP 专用直连端口（1-65535，需重启生效）；该端口不经过宿主网关认证，仅在配置了 `MCP_TOKEN` 时启动 |

配置优先级：**环境变量 > `.env.local` > `.env` > `config.yaml` > 默认值**
在界面「设置」中保存连接会写入 `.env.local` 并热更新，无需重启。

---

## 🧪 测试验证

```bash
# 后端单元验证
cd backend
go vet ./...
go test ./...

# 前端类型检查
cd frontend
pnpm typecheck
```

---

## 📁 目录结构

```
trpanel/
├── Dockerfile                        # 多阶段构建：Node 前端 → Go 后端 → alpine 运行镜像
├── docker-compose.yml                # 容器部署示例
├── .github/workflows/
│   ├── release.yml                   # 打 v* 标签时交叉编译并发布 Release
│   └── docker.yml                    # 构建并发布多架构镜像（GHCR + Docker Hub）
├── backend/                          # Go 后端（单二进制）
│   ├── cmd/
│   │   ├── server/                   # 入口（解析平台 + 组装服务）
│   │   └── trmock/                   # Transmission RPC 开发用 mock
│   ├── internal/
│   │   ├── api/                      # REST API + WebSocket Hub（宿主无关）
│   │   ├── config/                   # 配置加载（环境变量/.env/config.yaml）
│   │   ├── middleware/               # CORS、安全头、鉴权（策略由平台提供）
│   │   ├── models/                   # 数据结构
│   │   ├── platform/                 # 宿主平台抽象
│   │   │   ├── generic.go            # 通用部署（默认，最小权限）
│   │   │   └── fnos/                 # 飞牛 fnOS（网关集成 + fpk 更新）
│   │   ├── rpc/                      # Transmission RPC 封装 + 热更新管理
│   │   ├── automove/                 # 自动文件管理（已完成种子按站点归档）
│   │   ├── seedpolicy/               # 做种策略引擎
│   │   └── state/                    # 运行时状态持久化
│   ├── web/dist/                     # 内嵌前端构建产物
│   └── trpanel(.exe)                 # 已编译产物
└── frontend/                         # React + TypeScript 前端
    └── src/
        ├── platform/                 # 宿主能力抽象（web / fnos）
        ├── components/               # UI 组件
        ├── layout/                   # 整体布局骨架
        ├── stores/                   # Zustand 状态
        ├── hooks/                    # 自定义 Hook
        ├── api/                      # 后端接口封装
        ├── lib/                      # 通用工具
        ├── types/                    # 类型定义
        ├── i18n/                     # 中 / 英国际化
        └── styles/                   # 全局样式（Liquid Glass 令牌）
```

---

## 💬 反馈与支持

- 界面 / 功能问题 → [trpanel Issues](../../issues)
- fpk 安装、应用更新等问题 → 联系飞牛 OS 应用仓库 `fnos-transmission`

---

<div align="center">

**trpanel** · 一份独立、精致、开箱即用的 Transmission 管理面板

Made with ❤️ for the **飞牛 OS** community

</div>