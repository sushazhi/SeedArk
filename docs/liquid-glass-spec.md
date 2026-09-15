# 设计规范：Liquid Glass（HIG / WWDC25–26 对齐）

本项目以 CSS 变量 + backdrop-filter 在 Web 上实现 iOS 26 的 Liquid Glass，并按
iOS 27 beta 的方向保留「玻璃更透、用户可控」的无障碍降级。改任何界面先读本文。
令牌唯一定义处：`frontend/src/styles/index.css`。

## 1. 材质分层（核心规则）

| 档位 | 类名 | 用途 | 模糊 |
| --- | --- | --- | --- |
| dock | `.glass-panel` + `.tm-dock` | 压在滚动内容之上的停靠栏（顶栏/侧栏/状态栏/列表表头） | rest 8px → scrolled 28px，随滚动增强 |
| card | `.glass-card` | 种子卡片（列表项） | 14px / 160% |
| float | `.glass-panel-strong` | 弹窗 / 菜单 / Toast / 悬浮胶囊（顶层浮层） | 40px / 200%，不随滚动变化 |
| solid | `.glass-panel-solid` | 玻璃弹窗**内**的下拉 / 子菜单（二级浮层） | 无（实底 + 浮层阴影） |
| subcard | `.glass-subcard` | 面板**内部**的子卡片、分组块 | 无（实色半透明 + 内高光） |
| section | `.glass-section` | 玻璃弹窗/面板内的**分区块与规则行**（设置、统计卡片） | 无（极淡填充 + 内高光） |

- **只有承载滚动内容的顶层面板允许 `backdrop-filter`**；玻璃容器内的子卡片用
  `.glass-subcard`、二级浮层（下拉/子菜单）用 `.glass-panel-solid`，禁止二次模糊叠加。
- 玻璃浓度可由用户调节（设置 → 界面滑杆，20–100%），统一由 `--glass-user-opacity`
  乘数驱动；新材质填充 alpha 必须写成 `calc(基础alpha * var(--glass-user-opacity))`。
  `.glass-panel-solid` 与 reduce 实底不参与缩放。
- 遮罩层：`bg-black/25 backdrop-blur-[10px] backdrop-saturate-[.8]`（dialog/sheet 通用）。
- 滚动边缘（scroll edge effect）：内容滑入停靠栏用 `.tm-scroll` 的 mask 渐隐，
  材质增强由 `.tm-shell[data-edge='scrolled']` 驱动，两处都不要手写。

## 2. 同心圆角

外层 22 → 面板 20 → 卡片/弹层内容 16 → 控件 10–14。
嵌套公式：**内圆角 = 外圆角 − 内间距**（如 22 容器、12 间距 → 内 10）。
令牌：`rounded-sm/md/lg/tile/panel/dock` = 10/12/14/16/20/22。禁止 `rounded-[..]` 任意值。

## 3. 字号（HIG Dynamic Type 语义刻度）

| 令牌 | px | 用途 |
| --- | --- | --- |
| `text-caption2` | 11 | 玻璃上的最小文字；**禁止 <11px**（对比度不足） |
| `text-caption1` | 12 | 标签、徽章、次级时间 |
| `text-footnote` | 13 | 元信息、状态栏 |
| `text-body` | 14 | 正文、列表项 |
| `text-subhead` | 15 | 小标题、按钮 |
| `text-title2` | 17 | 弹窗/区块标题 |
| `text-title1` | 20 | 页面标题 |

- 玻璃上的次要文字叠加 `.tm-glass-label`（text-shadow）把对比度拉回 ≥4.5:1。
- 数字一律 `.tm-mono`（等宽数字）。

## 4. 颜色与语义

- 主色 `#007aff`、破坏性 `#ff3b30`（暗色 `#ff453a`）、成功 `#34c759`、
  警告 `#ff9500`；中性灰用令牌 `--color-gray-50..950`。
- 状态色写 `tagColor()/statusDotColor()` 之类既有工具，不要临时拼 `${hex}1c` 透明度。
- 阴影只用令牌：`--glass-shadow-light/float/dark/float-dark` + `--glass-ring-*`。
- 选中态 = 品牌色渗入玻璃 + 内高光翻转（`.glass-card[data-selected]`），不是描边框。

## 5. 触控目标

- 桌面最小命中区 **32px**；移动端主操作按钮 **≥44px**，行级控件 ≥32。
- 触屏无 hover：`(hover:none),(pointer:coarse)` 下选择框/菜单按钮必须常显
  （`.tm-reveal-hover` / `.tm-touch-menu` 已处理，新控件照抄该模式）。
- 交互元素需有 `:focus-visible` 可见环；菜单项至少要有背景高亮。

## 6. 动效

- 曲线只用两个：`var(--ease-standard)` cubic-bezier(.25,.1,.25,1)、
  `var(--ease-spring)` cubic-bezier(.34,1.56,.64,1)。
- 时长 0.15–0.42s；进度条、导航胶囊用 spring；材质/阴影过渡用 standard。
- 按压反馈 `.tm-press`（scale 0.955 + 高光翻转）；主按钮扫光 `.tm-btn-primary`。
- 不写 `duration-150/200`、`ease-in-out` 之类随意值。

## 7. 布局与安全区

- 组件内只读 `var(--safe-*) / --chrome-inset-* / --pad-top/bottom`，
  不直接写 `env(safe-area-*)`（已在 :root 收口）。
- 全出血滚动容器用 `.tm-content` + `.tm-scroll`；停靠栏用 `.tm-dock-top/bottom`。
- 横屏刘海域：列表左右留白需叠加 `--safe-left/right`。

## 8. 深色与无障碍（必维护）

- 深色模式玻璃转黑玻璃；新增材质类必须同时写 `.dark` 变体。
- `data-a11y-glass="reduce"`：玻璃退化为实底高对比，**所有新玻璃样式都要有降级**。
- `data-a11y-contrast="more"`：次要文字自动升级；`data-a11y-motion="reduce"`：
  动画归零。新动画不要绕过这两条。
- 对应 HIG：Reduce Transparency / Increase Contrast / Reduce Motion。

## 9. 禁止清单

1. 嵌套 `backdrop-filter`（玻璃弹层内再开玻璃下拉：下拉走 portal，视觉上形成
   双重模糊——现状遗留，见审计项，新代码避免）。
2. `rounded-[..]`、`text-[..px]`、`blur-*`、`shadow-md/lg` 等绕过令牌的写法。
3. <11px 文字、<32px 桌面命中区。
4. 玻璃上用裸灰字而不加 `.tm-glass-label`。
5. 硬编码中文/英文字面量：可见文案全走 `t()`；设置开关/选项必须配一行说明。
6. `transition-all`、无曲线的默认过渡。

## 10. 合规审计（2026-08-29，已全部修复）

已符合：材质分层（无嵌套模糊）、同心圆角、字号刻度、安全区变量、选中态/按压语言、
触屏常显控件、Reduce-* 三档降级、scroll edge effect。

修复记录：

| # | 问题 | 修复 |
| --- | --- | --- |
| 1 | 绝大多数设置开关缺一行说明 | SettingsModal `Row` 增加 `hint`，约 34 个开关/选项补齐 `*Hint` 文案（zh/en） |
| 2 | 控件 28px（排序胶囊、`h-7` 按钮、弹窗关闭钮） | 全站交互控件统一抬到 `h-8`/`w-8`（ListHeader、Settings、Detail、AutoMove、AddTorrent、Sidebar、TopBar、TagInput、ui/dialog 关闭钮 32px） |
| 3 | 硬编码文案 `['一'…'日']`、`Server n`、`Tier n`、`more/Auto` | 新增 `session.dayShort*`、`multiServer.server`、`detail.trackerTier`、`createTorrent.autoPiece/moreFiles/fileSummary` |
| 4 | 双重模糊：玻璃弹窗内的 Select/子菜单 | 新增 `.glass-panel-solid` 实底材质，弹窗内下拉/子菜单全部换用；顶层浮层保持 `.glass-panel-strong` |
| 5 | 详情页移动端是 Dialog；InfoGrid 手写背景；文件树展开钮过小 | 移动端改底部 Sheet（可下滑关闭）、InfoGrid 用 `.glass-subcard rounded-tile`、展开/重命名钮 28px+负边距扩至 ≥32px 命中区 |
| 6 | GridView 横屏左右留白未叠加安全区 | 容器 padding 改 `calc(var(--safe-left/right) + .75rem)` |
| 7 | DesktopTable 表头裸 `backdrop-blur-xl bg-white/50` | 改 `tm-dock glass-panel`，纳入滚动联动材质 |
| 8 | 焦点态缺失：周几按钮、手写右键菜单项、文件树按钮 | 统一补 `focus-visible:outline-none` + `focus-visible:ring-1 ring-ring`（菜单项用主题色底） |
| 9 | Canvas 块位图硬编码 hex 色 | 绘制改读 `--color-green-500`/`--color-blue-500` 令牌（暗色 pending 用 gray-700），随 theme/themePreset 重绘；图例圆点化 |

## 11. 合规审计（2026-09-11，第二轮）

第一轮把规范立了起来，但执行到具体组件时仍有漂移；这一轮逐条收敛。

| # | 问题 | 修复 |
| --- | --- | --- |
| 1 | DesktopTable 表头 `justify-center`、单元格 `text-right`，数字列无共同基线 | 表头对齐跟随 `NUMERIC_COLS` 分流（数字列 `justify-end`，其余 `justify-start`）；排序图标改为常驻等宽槽位，切排序时表头文字不再左右跳 |
| 2 | TopBar 右侧常驻 9 颗图标，选中态再加 8 颗，顶栏从未"安静" | 低频项（创建种子、打开下载目录、配置目录）收进 `⋯` 溢出菜单，顶栏只留排序/视图/刷新/添加/头像 |
| 3 | 第 10 节 #2 声称命中区已全站 32px，实际仍有 `w-7 h-7`（McpManager 复制钮、TorrentDetail 树展开钮）、`h-7`（SeedPolicy 清日志、App 标签模式分段控件） | 全部抬到 `h-8`/`w-8`；树展开钮用负边距抵消视觉尺寸，不改变行高 |
| 4 | `shadow-md/lg/xl` 与 `border-gray-200/70 dark:border-gray-700/50` 散落 10+ 处，绕过令牌 | 新增 `.glass-section`（玻璃内分区块，含 `.dark` 与 `data-a11y-glass="reduce"` 降级）；Dashboard/AutoMove/SpeedPolicy/SeedPolicy/SettingsModal 的卡片全部换用；阴影改用具体令牌值或 `color-mix` |
| 5 | 弹窗标题字号不一：TorrentDetail 用 `text-subhead`，其余用 `text-title2` | 统一回 `text-title2`；`ui/input.tsx` 的 `text-subhead md:text-body` 改为固定 `text-body`（触屏 ≥16px 由 CSS 层统一承担） |
| 6 | 状态栏窄屏把计数/空间全部 `hidden md:` 隐藏，移动端用户看不到任何汇总 | 底栏保留图标级空间指示 + 新增窄屏摘要（任务数/活跃/异常）；明细补进「详情」浮层（`md:hidden` 分组） |
| 7 | ~~侧栏单选/多选分组无法判断点击语义~~（**已撤销**） | 曾加 `mode` 属性在标题行显示「单选/多选」标识，后判定为伪需求：两种语义本就由视觉语言区分——单选走滑动胶囊（`tm-nav-rail`，一次只亮一个且可见位移），多选走逐项 `tm-nav-active` + ✓（可同时亮多个）。标识反而在标题行制造噪声且重复 9 次，已移除 |
| 8 | 侧栏标签 chip 内名称/体积/计数同为 `opacity-70`，无主次 | 名称加 `truncate` 承担主体，体积与计数收进低对比度分组并用 `·` 分隔 |
| 9 | GridView 元信息行 `flex-nowrap + overflow-hidden`，窄屏静默截断做种时长 | 窄屏允许折行（`flex-wrap`），宽屏保持 `md:flex-nowrap`；分隔点在折行时隐藏 |
| 10 | 拖拽落点整屏 `bg-primary/20` + 16px 图标，深色下刺眼 | 底色降到 `/7`，提示内容收进 `.glass-panel-strong` 卡片，图标 11px 圆形衬底 |
| 11 | 空列表两种语义（无数据 / 被筛选）共用 `AlertCircle`，正常空态带告警色彩 | 分化为 `Inbox`（无数据）与 `SearchX`（被筛选），并补 `focus-visible` 环 |

**遗留（未处理，需产品决策）**：侧栏六个分组信息量仍偏大，若要根治需引入分组 Tab 或折叠默认值调整，属交互模型变更而非样式修正，留待单独评审。

## 参考

- WWDC25/iOS 26：Liquid Glass 首发——半透明折射、滚动边缘、控件随内容形变。
- WWDC26/iOS 27 beta：玻璃默认更透、提供「降低透明度」系统开关——对应本项目
  `data-a11y-glass="reduce"` 必须是可预期的一等路径，不是兜底。
