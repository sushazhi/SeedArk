# MoviePilot-Frontend（jxxghp/MoviePilot-Frontend）UI 借鉴调研

> 调研对象：`jxxghp/MoviePilot-Frontend`，本地浅克隆 `main`，**version 3.2.4，commit `f392504`（2026-11-05）**，License MIT。
> 复现方式：`git clone --depth 1 https://github.com/jxxghp/MoviePilot-Frontend.git`
> 下面所有 `src/...` 路径均相对该仓库根，行号取自上述 commit。
> 对照对象：SeedArk 前端 `frontend/src`（React 19 + Vite + Tailwind 4 + Radix + zustand + i18next，28115 行）。
> 调研时间：2026-11-05。方法：逐文件阅读源码与 `docs/*.md` 契约，用 `grep` 定位实现，**未运行其应用**；标注「未验证」处即未实机确认。
> 用途：给 SeedArk 的 Web 面板挑「能真正落地」的 UI/交互/工程做法，并明确哪些**不该学**。

---

## 0. 结论速览

MoviePilot-Frontend 和 SeedArk 是同一类东西的两个物种：它偏「媒体库自动化 + 插件生态 + 移动端 App 外壳」，SeedArk 偏「下载器/保种台 + 桌面表格 + 玻璃质感」。**它 90% 的视觉与玻璃光学实现对 SeedArk 无用**（SeedArk 的 CSS 玻璃体系已经更轻更稳），真正值得拿的是三类东西：

| 类别 | 值得拿的东西 | 价值 |
|:--|:--|:--|
| **信息呈现的细节** | 文件名尾部保留省略、空态/诊断面板、日志流范式、卡片首屏快照 | 直接提升「保种台每天要看几十次」的场景质量，成本低 |
| **布局可配置** | GridStack 可拖拽仪表板（12 列 + 三档设备布局独立持久化） | SeedArk 的 Dashboard 目前是写死的，这是最大的一块能力空白 |
| **体系化工程** | 设置页按 tab 分包 + 已访问保留状态、能力协商式插件 SDK、PWA manifest 完整能力 | 决定后续 1 年的可扩展性 |

**优先级建议：P0 五项（1 天内可做完，收益立现）→ P1 四项 → P2 三项（先别做）。**

另有一个**现成 bug 级缺口**：SeedArk `PwaUpdatePrompt.tsx:19-22` 捕获了 `beforeinstallprompt` 存进 `deferredPrompt`，但整个文件（64 行）**从未使用它**，等于捕获了安装机会却没有任何安装入口。

---

## 1. 优先级清单

| # | 借鉴点 | MP 出处 | SeedArk 落点 | 改动量 | 优先级 |
|:--|:--|:--|:--|:--|:--|
| 1 | 文件名尾部保留省略 | `src/components/filebrowser/tailEllipsis.ts` | `TorrentList/DesktopTable.tsx:45`、`GridView.tsx:234`、`TorrentDetail/index.tsx:141` | S | **P0** |
| 2 | 搜索/筛选诊断面板 | `src/components/states/SearchDiagnosticsPanel.vue` | 新增，PT 聚合搜索与保种策略命中解释 | M | **P0** |
| 3 | 运行日志流查看器 | `src/views/system/LoggingView.vue` | 新增（后端有日志即可接） | M | **P0** |
| 4 | 可用高度实测 hook | `src/composables/useAvailableHeight.ts` | `hooks/`，供 PTDialog/TorrentDetail/日志页用 | S | **P0** |
| 5 | PWA manifest 能力 + 安装入口 | `vite.config.ts:109-215` | `public/manifest.webmanifest`、`PwaUpdatePrompt.tsx` | S | **P0** |
| 6 | 卡片首屏快照（按用户 + TTL） | `src/composables/useDashboardSnapshot.ts` | `stores/appStore.ts` + 各面板 | S | P1 |
| 7 | 可折叠设置卡片 | `src/components/system/CollapsibleSettingCard.vue` | `SettingsModal/*` | S | P1 |
| 8 | 设置页分包 + 已访问保留状态 | `src/pages/setting.vue:13-45` | `SettingsModal/index.tsx`（1644 行） | M | P1 |
| 9 | 可拖拽可持久化仪表板 | `src/pages/dashboard.vue:39-115, 1355-1395` | `components/Dashboard/` | L | P1 |
| 10 | 渐进式卡片网格（自动分列 + 整行首屏） | `src/components/misc/ProgressiveCardGrid.vue:35-44` | `TorrentList/GridView.tsx` | M | P2 |
| 11 | 主题自定义项分层 + 预览/提交/取消 | `src/composables/useThemeCustomizer.ts:66-100` | `TopBar/index.tsx` 的 `THEME_PRESETS`、`lib/theme.tsx` | M | P2 |
| 12 | 能力协商式插件前端 SDK | `src/sdk/plugin.ts`、`docs/plugin-frontend-sdk.md` | 与已有 MCP 后端结合 | L | P2 |

---

## 2. P0：低成本、收益立现

### 2.1 文件名尾部保留省略（`tailEllipsis`）

**出处**：`src/components/filebrowser/tailEllipsis.ts`（全文 90 行，`MAX_TAIL_LENGTH = 18`，见 :4）

**做法细节**：
- 常规省略是 `text-overflow: ellipsis`，把**扩展名和发布组一起吃掉**。它改成「主体可省略 + 尾部固定保留」两段：先整名渲染，**仅在真的溢出时**（`scrollHeight > clientHeight+1 || scrollWidth > clientWidth+1`）才拆成 `head`/`tail` 两个 span，未溢出保持原样连续文本。
- 尾部切分不是简单取最后一个点：`getNameTail()`（:16）先取最后一段扩展名，若整名尾长 ≤18 就**再往前吃一个点分段**，于是 `.chs&eng.ass`、`.5.1-GROUP.mkv` 能整段保留；`isDecimalPoint()`（:9-11）判断点号两侧都是数字时视为小数点（`5.1`、`AAC2.0`），继续向前找，避免把 `5.1` 劈开。
- 用 `ResizeObserver` 只在**宽度变化**时重算（`renderedWidths` 比对，:76-79），拆分本身引起的高度变化不会触发重渲染；无 `ResizeObserver` 环境只做首次渲染（:72）。
- 元素内容由指令独占，宽度变化自动恢复。

**为什么好**：对 SeedArk 这类面板，用户扫一眼种子名靠的是**分辨率和编码后缀**（`2160p.WEB-DL.x265`），而不是开头相同的剧集名。现在的实现（`DesktopTable.tsx:45`、`GridView.tsx:234` 都是 `truncate` + `title=` 悬浮提示）在窄列里必须悬停才能确认版本，移动端根本没有 hover。

**迁移到 SeedArk**：写一个 `<TailEllipsis name className>` 组件（React 化：`useRef` + `useLayoutEffect` 挂 `ResizeObserver`，`useState<{head,tail}|null>`；两段用 `<span className="truncate">{head}</span><span className="shrink-0">{tail}</span>`，父级 `flex min-w-0`）。**依赖：无。改动量 S。**
注意：`title` 属性保留（原实现没有全名提示），并给 `<span>` 加 `aria-label={name}` 以免读屏读到被拆开的碎片。

---

### 2.2 搜索/筛选诊断面板

**出处**：`src/components/states/SearchDiagnosticsPanel.vue`（全文约 270 行）

**做法细节**：
- 一份 `SearchDiagnostics` 数据 = `scope`（用哪个阶段的诊断）+ `stage_counts`（各阶段计数，一行摘要内联展示）+ `searched_sites[]`（每站 `site_name / attempted / outcome / raw_count`）+ `rejection_reasons[]`（`code` + `count`）+ `warnings[]`（`code` + 可选 `rule_group`）。
- **默认只显示摘要**，详细来源需用户主动展开（`expanded = ref(false)`，:12）；`aria-expanded` / `aria-controls` 用 `useId()` 生成受控 id（:13、:38-42）。
- 原因码走**白名单映射**（`reasonCodes` / `warningCodes` 两个 Set，:14-27），未知码统一落到 `other` 文案，**后端新增码不会显示成乱码 key**。
- 未尝试的站点单独一个 `notAttempted` 文案（:44），把「没搜」和「搜了没结果」区分开。
- 数字列用 `font-variant-numeric: tabular-nums`（:139），多行数字对齐。

**为什么好**：SeedArk 的核心痛点是「PT 站点聚合搜索 0 结果 / 保种策略没生效时，用户不知道为什么」。当前只能看到空列表。有了诊断面板，可以一次说清：哪几个站被流控冷却、哪几个站返回 0 条、多少条被哪个过滤规则拒掉。**这是把「黑盒」变「可解释」的组件，而不是装饰组件。**

**迁移到 SeedArk**：SeedArk 后端已经有站点/策略概念（PT 面板、SeedPolicyManager），只需在搜索/策略执行响应里附带一个 `diagnostics` 结构；前端加一个默认折叠的 `<DiagnosticsPanel>`。原因码建议直接沿用「白名单 + other 兜底」写法，否则每次后端加码都要发版。**改动量 M（前端 S + 后端补字段）。**

---

### 2.3 运行日志流查看器

**出处**：`src/views/system/LoggingView.vue`（常量见 :55-59）

**做法细节**（这些都是踩过坑才有的数值）：
- SSE 流式追加（`useSSE`，:522），落盘行数**硬上限 600**（`MAX_LOG_LINES`，:487 `slice(-MAX_LOG_LINES)`）——防止长跑页面内存爆掉。
- 逐条渲染会卡：用 **80ms 批量 flush**（`FLUSH_DELAY`，:508）合并高频日志。
- 相同「秒 + 级别」且时间戳间距 ≤ **100ms** 的连续行**合并成一组**（`GROUP_GAP_MS`、`canMergeIntoGroup`，:427）——这是压掉「一秒钟刷 200 行」的关键。
- `followTail` 自动跟随，距底 ≤ **32px** 视为在底部（`SCROLL_BOTTOM_THRESHOLD`，:440）；用户上滚离开底部则停止跟随，并显示「N 条新日志」按钮（:753）而不是强行把视口拽回去。
- 级别下拉**从数据里动态收集**出现的级别（`levelOptions` 计算属性），`TRACE`/`CRITICAL` 默认隐藏（`HIDDEN_LEVELS`，:54）；ANSI 颜色码用正则剥离（`ANSI_PATTERN`，:162）。
- 高度用 2.4 的 `useAvailableHeight(96, 320)` 算（:93），而不是 `100vh`。

**为什么好**：SeedArk 目前没有日志页（`components/` 下无 LogViewer）。下载器面板的排障极度依赖日志，而日志页最容易写崩（滚动跳动、内存、刷屏）。这套数值可以直接抄。

**迁移到 SeedArk**：SeedArk 已有 WebSocket 基础设施（`hooks/useWebSocket.ts`）+ 后端日志，改写为 WS 订阅即可。React 化注意：把「批量 flush + 分组」放在 `useReducer`/ref 缓冲里，不要让每条日志触发一次 setState；`followTail` 用 `onScroll` 读取 `scrollHeight - scrollTop - clientHeight`。**改动量 M。**

---

### 2.4 可用高度实测 hook

**出处**：`src/composables/useAvailableHeight.ts`（`layoutPaddingTop = ref(72)` / `layoutPaddingBottom = ref(24)`，:20-21；公式与下限见 :83-97）

**做法细节**：
- 不是自己算，而是**从 DOM 反测量**：读 `.layout-page-content` 的 `getComputedStyle().paddingTop/Bottom`（真实值含 `env(safe-area-inset-*)`），再查询底部 Dock 元素 `.footer-nav-container` 的 `offsetHeight`（通过 Teleport 挂在 body，存在即测量、不存在即 0）。
- 公式：`viewport - paddingTop - paddingBottom - footerDock - componentOffset`，并对 `minHeight` 取 `Math.max`（:97）——避免算出 50px 的畸形视口。
- 同时监听 `window.resize` 和 `window.visualViewport.resize`（:69-72，移动端软键盘/地址栏收缩必须靠后者），并用 `ResizeObserver` 观察 Dock 高度变化（:23-32）——Dock 会出现/消失（PWA 模式切换），所以还要 `watch(appMode)` 后 `nextTick` 重测（:62-64）。

**为什么好**：SeedArk 已经大量用 `dvh`（12 处），比 `vh` 好，但**在弹窗/面板内部仍然算不准**：Dock、安全区、软键盘叠加后只能实测。这类「测出来」而不是「猜出来」的高度逻辑，正是玻璃面板 + 底部 Dock 布局的刚需。

**迁移到 SeedArk**：写 `useAvailableHeight(componentOffset, minHeight)`，测量 `--pad-top`/`--pad-bottom` 对应元素与 `StatusBar`/`FloatingBar` 实际高度。**改动量 S。**

---

### 2.5 PWA manifest 完整能力 + 安装入口（含一个现成缺口）

**出处**：`vite.config.ts:109-215`（manifest 全文）

**值得抄的具体字段**：
- `display_override: ['window-controls-overlay', 'standalone']` — 桌面端装成 App 后能用窗口控件覆盖层，省一条标题栏。
- `shortcuts: [...]` — 长按/右键 App 图标直接跳「推荐 / 探索 / 更多」，**每项带 192px 图标**（`sparkles-icon-192x192.png` 等）。对 SeedArk 就是「下载中 / 保种台 / 添加种子」。
- `launch_handler: { client_mode: 'navigate-existing' }` — 从快捷方式再次打开时**复用已有窗口**，而不是新开一个，这对「常驻面板」体验差别很大。
- `handle_links: 'preferred'`、`id: 'moviepilot-app'`（固定 App 身份，避免改 `start_url` 后变成另一个应用）。
- `screenshots`（含 `form_factor: 'wide'`）与 `edge_side_panel.preferred_width: 320`。
- iOS 侧：`index.html` 里 **`apple-touch-startup-image` 全套 splash**（:63-129）+ `mask-icon` + 多尺寸 `apple-touch-icon`，另有 `--initial-loader-*` CSS 变量做首屏骨架，颜色随主题（避免白闪）。
- 附带脚本：`scripts/generate-pwa-icons.mjs` / `generate-pwa-splash.mjs` 自动生成这些图。

**SeedArk 现状与缺口**：
- `public/manifest.webmanifest` 只有 name/icons/display/colors，**没有** `shortcuts`、`display_override`、`launch_handler`、`screenshots`、`id`。
- `src/components/PwaUpdatePrompt.tsx` 全文 64 行：`let deferredPrompt` 在 :19 声明、:22 赋值，**之后再无引用**——捕获了安装机会却没有安装按钮。要么补一个「安装到桌面」入口，要么删掉这段死代码。

**迁移到 SeedArk**：manifest 补字段 + 三张 shortcut 图标（可用 `public/icons/icon-192.png` 的变体）；安装入口放在设置页「关于」或首次访问的轻提示，点击调 `deferredPrompt.prompt()` 并读 `userChoice` 决定是否再提示。**改动量 S。**

---

## 3. P1：结构性收益

### 3.1 卡片首屏快照（按用户 + TTL + 版本化 key）

**出处**：`src/composables/useDashboardSnapshot.ts`（`PREFIX_V1` :10、`TTL = 24h` :11）

**做法细节**：
- key 是 `${PREFIX}:${userID}:${key}`（:26），**按登录用户隔离**；组件实例绑定创建时的用户 ID（:21），迟到响应不会写进「之后登录的另一个用户」的快照。
- 读取时校验 `savedAt`：非有限数、负数、超 TTL、缺 `value` 字段**一律清除并返回 undefined**（:44）。
- 写入失败（`localStorage` 不可用/超配额）静默降级（:63-65）——快照只是首屏优化，永远不能成为错误来源。
- 数据结构变更时**换版本号**而不是写迁移（key 里带 `V1`）。

**为什么好**：SeedArk 的面板打开时是「一片空白 → 数据到来 → 跳一下」。有快照就能先渲染上次的成功值再静默刷新。它比「全局 loading 骨架」体验好得多，而且实现只有 60 行。

**迁移到 SeedArk**：`utils/snapshot.ts`，接 PT 站点数据、流量历史、磁盘空间这几张「贵」的卡片。React 化：`useSyncExternalStore` 或简单的 `useState(readSnapshot() ?? null)` + effect 写回。**改动量 S。**

---

### 3.2 可折叠设置卡片

**出处**：`src/components/system/CollapsibleSettingCard.vue`

**做法细节**：
- 用 `<button>` 承载整个头部（不是给 `div` 绑 click），带 `aria-expanded` 和 `aria-label`（:44-49），`focus-visible` 有明确 outline（:100-104）——键盘与读屏可用。
- `subtitle` 说明这块配置是干什么的；另有 `status` **具名插槽**放「当前值摘要」，折叠状态下也能看到关键状态（:56-58）。
- 标题/副标题 `nowrap + ellipsis`（:112-118），避免长路径撑破卡片。
- 默认 `collapsed: true`（:12）：设置页首屏只给目录感，不给人压迫感。
- 移动端 `align-items: flex-start`（:126-130），长标题换行时不与图标错位。

**为什么好**：SeedArk 的 SettingsModal 1644 行、`glass-section` 分块已有基础，但缺少「默认折叠 + 折叠态仍能看见当前值」这一层。这是设置页从「一屏塞满」变成「可扫描」的最小改动。

**迁移到 SeedArk**：`components/ui/collapsible-card.tsx`（Radix 已有，或用原生 `<details>` + 受控）。**改动量 S。**

---

### 3.3 设置页分包 + 已访问保留状态

**出处**：`src/pages/setting.vue:13-45, 96-110`

**做法细节**：
- 每个设置大类 `defineAsyncComponent(() => import(...))`（:13-19），注释写得很直白：「设置页的每个大类都很重，按标签页拆包，避免进入设置时一次性下载全部配置面板」。
- `visitedTabs = ref(new Set())`（:21）：**只挂载访问过的 tab**（模板 `v-if="visitedTabs.has(item.value)"`），访问过之后即使切走也**保留组件状态**（输入框里的半截路径不会丢）。
- 深链 `?tab=` 校验：`validRouteTab()` 用 `settingTabValues` Set 过滤，非法值不能把用户留在空白 `VWindow`；`onMounted` 里非法则回落到第一个 tab（:96-99）。
- 传给子面板一个 `:active` prop，让面板知道自己是当前可见的（:110）。

**为什么好**：SeedArk 的 SettingsModal 是 1644 行单文件、首屏全量渲染。拆成 lane（连接/目录/站点/自动化/关于）并按需加载，能同时解决三件事：首包体积、首屏渲染时间、以及「切走再切回丢掉未保存输入」。

**迁移到 SeedArk**：先做 `React.lazy` + 按 lane 的 `visited` Set，再做文件拆分。注意 React 没有 `keep-alive`：要保留状态需把**未保存的表单状态提到 store**（或常驻挂载 + `hidden`），二选一，别指望切走还能保留 DOM。**改动量 M。**

---

### 3.4 可拖拽可持久化的仪表板（最大的一块能力空白）

**出处**：`src/pages/dashboard.vue`（常量 :39-57、默认布局 :79-90、编辑态 :115、GridStack 初始化 :1355-1395）

**做法细节**：
- **12 列网格 + 三档设备 profile**：桌面断点 1280、平板 960、手机 640（:40-42），单元格高 16px、间距 8px（:43、:45）；`getDashboardLayoutProfile()`（:732-736）按显示宽度选档。
- **每档设备各存一份布局**：storage key 与后端 config key 都按 profile 派生（`MP_DASHBOARD_GRID_LAYOUT` / `_TABLET` / `_MOBILE`，:760-776）——手机上拖成单列不会破坏桌面布局。
- 默认布局按设计稿写死一份，**用户保存过的优先**（:79-90 + :983）。
- **编辑模式与浏览模式分离**：`isLayoutEditing` 切换 `staticGrid`（:1374、:1381-1388）；拖拽句柄限定 `.dashboard-grid-drag-handle`，且 `draggable.cancel` 排除 `input,textarea,button,select,option,a`（:1362-1366）——**否则拖卡片会误触发输入框选中和按钮点击**，这是必须抄的一行。
- 卡片按需加载：`DashboardElement.vue` 里每个内置卡片都是 `defineAsyncComponent`，且用 `Map` 缓存加载 Promise（`builtInDashboardComponentPromises`），「关闭的卡片不再挤进 dashboard 首屏 chunk」。
- 同一份持久化对象里同时存 `enabled`（显示项）和 `items`（几何），并带 `updatedAt`。

**为什么好**：SeedArk 的 Dashboard 是固定布局。而「下载器面板」的用户画像分化极大：有人只看速度曲线，有人只看磁盘与保种风险。可拖拽 + 可隐藏 + 按设备分档，是把这个页面从「一种布局服务所有人」变成「每人一个布局」。

**迁移到 SeedArk**：
- React 现成方案：`react-grid-layout`（对应 GridStack）或 `@dnd-kit`（自己写网格更适合已经用 Tailwind 的项目）；SeedArk 已有 `@radix-ui` 全套，没有网格库依赖。
- **务必抄**：编辑态开关、拖拽句柄白名单、`cancel` 排除交互元素、按设备档位分别持久化。
- **不要抄**：MP 那套双向同步（`isSyncingDashboardGrid` + `isPersistingDashboardGridLayoutFromGrid` 两个互斥标志位，:126-131）。Vue 侧因为 GridStack 自己管 DOM 才需要这层；React 用「单一数据源（store）+ 受控渲染」可以避免。
**改动量 L（先做 2 张可拖动卡片验证，再全量）。**

---

### 3.5 无障碍与动效纪律（顺手就能对齐）

**出处**：`src/components/states/NoDataFound.vue`（`@media (prefers-reduced-motion: reduce)` 一次性关掉容器/光晕/胶卷盘/胶片帧全部动画，:263-271）、`SearchDiagnosticsPanel.vue`（`role="status"` + `aria-expanded` + `aria-controls` 受控 id）、`CollapsibleSettingCard.vue`（`<button>` + `aria-expanded` + `focus-visible` outline）

**SeedArk 现状**：`prefers-reduced-motion` **只在 `App.tsx` 命中一处**；`EmptyList.tsx` 等空态组件没有降级动效；扫描型组件（诊断、可折叠卡片）尚不存在。

**做法**：凡是新增的自定义动画（`tm-press`、浮层入场、涟漪、粒子/光效），都补一条 `@media (prefers-reduced-motion: reduce) { animation: none; }`；可折叠区域一律用真实 `<button>` + `aria-expanded`，展开内容的 `id` 用 `useId()` 关联到 `aria-controls`。**改动量 S，但属于「越晚补越贵」的类型。**

---

## 4. P2：先别做，但值得记下来

### 4.1 渐进式卡片网格

**出处**：`src/components/misc/ProgressiveCardGrid.vue`（默认值 :35-44）

**做法细节**：
- 按容器宽度**自动分列**（`minItemWidth: 144`）而不是让调用方写断点；未测高条目按 `itemAspectRatio: 1.5` 估高。
- 首屏**补齐整行并覆盖当前视口**（`initialCount: 6`），之后每帧补 `batchSize: 6`（至少一整行），上下额外保留 `overscanRows: 4` 行。
- `virtualizeInOverlay: false` 默认值 + `keyboardInteractionActive`（:81、:111、:458）：**弹窗/浮层里默认不虚拟化**，且焦点进入后保留全部条目——因为虚拟回收会让键盘 Tab / 读屏跳过一个「不存在」的节点。这是很少见的、真正踩过无障碍坑的默认值。
- 最近滚动容器用 `findNearestScrollTarget()` 解析并做祖先链缓存（`src/utils/scrollTarget.ts`），适配「列表嵌在弹窗里」的情况。

**为什么好**：SeedArk 的 `GridView.tsx` 已经用了 `@tanstack/react-virtual` + `measureElement`（动态测高），所以**基础虚拟化不缺**。缺的是「自动分列 + 首屏补齐整行」这两个细节，以及「浮层内不虚拟化」这条无障碍决策。**改动量 M（只抄细节）。**

### 4.2 主题自定义项分层 + 预览/提交/取消

**出处**：`src/composables/useThemeCustomizer.ts:66-100`（设置对象）、`ThemeCustomizer.vue`

**做法细节**：
- 可调项分三层：**身份层**（`theme` 明暗、`primaryColor`、`radius` 5 档、`layout` 三档）、**开关层**（`glassEnabled`）、**参数层**（透明度/折射/反射等 0-100 滑杆）。
- 关键设计：**关闭玻璃后参数仍完整保留**（注释原文「仅在用户开启时启用网页玻璃材质及其实时计算，参数独立保留」）——用户不会因为关一次开关就丢掉调了半天的参数。
- 玻璃方案是「预设 + 每组合覆盖 + 缺失组合回落预设矩阵」（`glassPresetOverrides`），而不是让用户从 6 个裸滑杆开始。
- 三段式事务：`previewGlassSettings` / `commitGlassPreview` / `cancelGlassPreview`（:603-652）——滑杆实时预览，取消能整体回退。
- 主色是 **12 个具名预设**（:40-53，Purple/Indigo/Blue/…/Slate）而不是自由取色器；同时提供「自定义颜色」入口，并用 `isHexColor` 正则校验（:127-129）。

**为什么好**：SeedArk 已有 7 个主题预设（`TopBar/index.tsx` 的 `THEME_PRESETS`）+ 明暗，但**没有圆角档位、没有布局档位、没有预览/取消事务**。抄「预设优先 + 参数独立保留 + 预览可撤销」这三条设计原则就够了，不要抄玻璃参数本身。**改动量 M。**

### 4.3 能力协商式插件前端 SDK

**出处**：`docs/plugin-frontend-sdk.md`、`src/sdk/plugin.ts`（`PLUGIN_HOST_SDK_VERSION = 1` :10、`PLUGIN_HOST_SDK_KEY = 'moviepilot:sdk'` :12、`supports()` :381/479）、`src/pages/plugin/app.vue`、`src/utils/federationLoader.ts`

**做法细节（契约部分，这才是精华）**：
- **能力矩阵而非版本号判断**：宿主构造 `capabilities` 列表（:429-454，逐项按 `if (services.xxx)` 追加），插件只调 `sdk.supports('subscription.manage.edit')`；`supports` 对未知能力返回 `false`（:479）。文档明确写：「能力表示宿主已经实现接口，不代替当前用户权限或服务端授权」——**能力 ≠ 权限**，这层区分很清醒。
- **唯一契约源 + 版本化产物**：`src/sdk/plugin.ts` 是唯一真源，`npm run build:plugin-sdk` 生成 `plugin-sdk/v1/index.js` / `index.d.ts` / `agent.d.ts` 三个公开文件，插件下载到自己仓库里本地导入；`check:plugin-sdk` 校验产物与源一致；**不兼容变更换主版本目录**。
- **宿主服务与视图分层**：插件只拿到 `api`（宿主管认证/反馈/路径映射）、`ui.toast/dialog/confirm`、业务能力（subscriptions/media/resources/files/workflows）、`shell.button`/`shell.tabs`、`agent`；`sdk.context` 的身份用**动态 getter**，切实例后不会继续返回旧客户端。
- **加载失败有明确降级**：`plugin/app.vue` 里 `loadError` → `VAlert` 显示可操作的错误说明（「多入口时请暴露 AppPage 或 AppPage{Pascal}」），加载中 → `VSkeletonLoader`；`loadGeneration` 代际校验丢弃过期加载（:28-48）。
- **并发去重**：`federationLoader.ts` 用 `remoteRegistrationFlights` Map 保证同一 remote 只发现一次，避免并发写同一运行时槽位。
- 文档化的载荷上限：菜单契约里「最多三层、96 项、24 KB」（`docs/native-menus.md`）——**声明式数据必须有上限**，否则宿主被插件拖死。

**为什么好**：SeedArk 已有后端 MCP 端点（`McpManager.tsx`），但**前端没有插件表面**。要不要做取决于产品方向：如果希望第三方给 SeedArk 加页面，「能力协商 + 版本化 SDK + 明确降级」是唯一能把生态做稳的路子。

**迁移到 SeedArk —— 最小可用版本（建议顺序）**：
1. **先不引入 Module Federation。** 用「后端声明式页面」：后端把插件页面描述成 JSON（字段/表格/按钮 + 动作 ID），前端 `PageRender`/`FormRender` 通用渲染。MP 自己也有这套（`src/components/render/{PageRender,DashboardRender,FormRender}.vue`）。
2. 需要真组件时再上 **iframe + `postMessage` 能力协商**：宿主只暴露 `capabilities` 字符串数组 + 受控 API，插件拿不到 DOM 也拿不到凭据。比联邦简单一个数量级，且天然隔离。
3. 真需要共享 React 运行时/主题时才考虑 Module Federation——但那意味着插件必须与 SeedArk 同 React 版本，脆性急剧上升。
4. 能力名建议直接用 MCP 工具名，复用后端已有的工具清单，避免造第二套权限体系。
**改动量 L。风险**：安全（插件拿到的 API 面）、版本（React 主版本）、打包体积（共享依赖膨胀）。**种子项目的正确做法是先做 1。**

---

## 5. 明确「别学」的部分

| 别学 | 出处/事实 | 原因 |
|:--|:--|:--|
| 手写 WebGL 玻璃光学渲染器 | `src/composables/useGlassOpticalRenderer.ts` = **188 KB 单文件**，配套 `Glass*RefractionDefs.vue` 每个 20+ KB，另有 **292 KB** 的对应测试 | 维护成本与收益完全不成比例；SeedArk 的 CSS 玻璃（`glass-panel` / `glass-section` / `tm-dock`）已够用，且性能可控 |
| 180 KB 级组件文件 | `src/components/agent/AgentAssistantPanel.vue` = 184515 字节；`PluginCard.vue` = 43714；`SubscribeCard.vue` = 53523 | 反面教材。SeedArk 已经有一个 1995 行的 `PTDialog.tsx`，不要再添 |
| 把数据同步与网格几何事务写在同一文件 | `dashboard.vue` **2186 行**，含 `isSyncingDashboardGrid` / `isPersistingDashboardGridLayoutFromGrid` 双向同步标志、几何事务 begin/end | 这类双向同步是特定库（GridStack 自管 DOM）逼出来的补丁；React 下应坚持单一数据源 |
| 「六参数预设矩阵 + 每材质/质量/方案组合覆盖」的参数系统 | `useThemeCustomizer.ts` 的 `glassPresetOverrides` | 过度参数化：组合爆炸、无法测试、用户也调不懂。预设 3 个 + 2 个滑杆足够 |
| 原生 App 接管协议 | `src/native/*`（20+ 文件，含票据/代际/能力协商/最长 96 项菜单契约，`session.ts` 单文件 23 KB） | 只对「把网页塞进 iOS/Android 原生外壳」有意义。SeedArk 面向 fnOS/Docker Web，用不上 |
| 文档与实现脱节的注释 | `docs/plugin-frontend-sdk.md` 自己声明「当前源码新增此 SDK，实际可用性以包含本实现的前端发行包为准」 | 借鉴其诚实，但别学「先写契约文档再迟迟不落地」的节奏 |

---

## 6. 与 SeedArk 现状的对照（避免重复造轮子）

读 SeedArk 现有代码后确认**已经具备、不需要借鉴**的能力：

| 能力 | SeedArk 现状 |
|:--|:--|
| 列表虚拟化 | 已有 `@tanstack/react-virtual`：`DesktopTable.tsx:386`（`overscan: 12`，行高随单行模式切换需注意缓存）、`GridView.tsx:60`（`measureElement` 动态测高） |
| 玻璃质感主题 | 已有 `glass-panel` / `glass-panel-strong` / `glass-section` / `tm-dock` 体系 + `hooks/useGlassChrome.ts` |
| 主题预设 | `TopBar/index.tsx` 已有 7 组预设（blue/green/purple/orange/pink/teal/graphite），明暗双模，持久化在 `appStore` |
| 安全区与动态视口 | 已有 `dvh` 14 处、`safe-area` 7 处（3 个文件）；`visualViewport` 已在 `hooks/useFloatingMenuPosition.ts:29-31`（浮层定位）与 `hooks/useGlassChrome.ts:53-54`（软键盘避让）中按「iOS 上 innerHeight 不变、只有 visualViewport 反映可见区」处理；另有 `useSafeAreaGuard.ts` |
| 手势 | 已有 `hooks/useSwipeGesture.ts`、`useHideOnScroll.ts`、`useRevealPath.ts`、`useNavRail.ts` |
| 空状态语义 | 已经比 MP 更讲究：`TorrentList/EmptyList.tsx` 明确区分「确实没有任务 / 被筛选条件滤空 / 未连接下载器」三态，并给了各自的下一步动作 |
| 国际化 / 演示模式 / 平台抽象 | `i18n/locales/{zh,en}.ts` 各 1300+ 行；`src/demo/*` 完整离线演示；`src/platform/{web,fnos}.ts` 平台抽象 |
| 快捷键 | `hooks/useKeyboardShortcuts.ts` |

**真正的空白就三块**：① 可配置仪表板；② 诊断/可解释性面板（搜索、策略命中、日志）；③ 标题类文本的窄列呈现质量。这三块正好是上面 P0/P1 的重心。

---

## 7. 建议的落地顺序（每个都可独立验收）

1. **S**：`TailEllipsis` 组件 → 用在 `DesktopTable` 名称列 / `GridView` 卡片标题 / `TorrentDetail` 文件树。
2. **S**：manifest 补 `shortcuts` / `display_override` / `launch_handler`，并修掉 `PwaUpdatePrompt.tsx` 的 `deferredPrompt` 死代码（改成真正的安装入口）。
3. **S**：`useAvailableHeight` + `useDashboardSnapshot` 两个 hook，先接 PT 站点数据与流量历史。
4. **M**：诊断面板（后端加 `diagnostics` 字段 → 前端折叠面板，未知原因码白名单兜底）。
5. **M**：日志页（WS + 600 行上限 + 80ms 批量 + 100ms 分组 + 跟随阈值 32px）。
6. **M**：设置页按 lane 分包 + 已访问保留 + `CollapsibleSettingCard`。
7. **L**：仪表板可拖拽（先 2 张卡片验证编辑态/句柄/分档持久化，再推广）。
8. **L（可选）**：插件表面按「后端声明式页面 → iframe 能力协商 → 联邦」三步走，第一步就能交付价值。

---

## 8. 未验证与保留

- 未运行 MoviePilot-Frontend，所有结论来自源码与文档；其中的**数值型经验**（600 行、80ms、100ms、32px、18 字符、12 列、各断点）建议在 SeedArk 落地时按自己的数据实测复调。
- `docs/module-federation-guide.md` 长达 58 KB，本报告只抽取了契约与降级相关结论，未逐节验证其示例代码可运行性。
- 未评估 MoviePilot 后端 API 是否足以支撑诊断面板所需的字段（`stage_counts` / `rejection_reasons` 的等价物），**需要 SeedArk 后端确认后再定前端数据契约**。
- 仓库当前为 v3.2.4（2026-11-05）；moviepilot 前端迭代很快，行号会漂移，引用时以内容匹配而非行号为准。
