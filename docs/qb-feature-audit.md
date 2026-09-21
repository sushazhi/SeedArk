# qBittorrent 下的右键菜单 / 自动化功能审计

审计范围：种子右键菜单（含移动端长按菜单）、侧边栏分组右键、自动化三项（自动文件管理 / 做种策略 / 分组限速）。
审计口径：功能在 qBittorrent（Web API v2，4.x / 5.x）下 **是否有效**、**是否多余（Transmission 遗留、在 QB 下无意义、与 QB 原生能力重复）**。

结论速览：

| 判定 | 数量 | 含义 |
|:--|:--|:--|
| ✅ 有效 | 21 | 后端映射到位，QB 下行为正确 |
| ⚠️ 降级生效 | 6 | 能跑，但语义与 Transmission 不同，需界面提示 |
| ❌ 失效 | 4 | 调用必然失败或静默无效，属**多余入口** |
| 🚫 能力互斥 | 1 | 整个功能块在 QB 下不适用（分组限速） |

自动化三项的**多余性**结论：

| 模块 | 有效性 | 多余性 | 一句话 |
|:--|:--|:--|:--|
| 自动文件管理 | ✅ | ⚠️ **与 QB 原生 TMM 高度重复** | 添加时主动关掉 `autoTMM`，再用自己的引擎重做一遍 |
| 做种策略 | ✅ | ✅ **不算多余，但有界面双入口** | 按站点差异化目标强于 QB 全局单值；与 QB 原生 `max_ratio` 叠加 |
| 分组限速 | 🚫 | 🚫 **多余且有害** | QB 无 HonorSessionLimits 语义，且与手动单种限速互相打架 |


---

## 一、种子右键菜单（`frontend/src/components/TorrentMenu.tsx:99-143`）

菜单项由 `buildTorrentMenu()` 构建，动作分发在 `frontend/src/hooks/useTorrentMenuHandler.ts:47-92`，
底层走 `useTorrentActions`（`frontend/src/hooks/useTorrentActions.ts`）→ REST → `rpc.Manager` → 驱动。

### 1.1 有效项（21）

| 菜单项 | 菜单 key | QB 实现 | 说明 |
|:--|:--|:--|:--|
| 开始 | `start` | `torrents/start`，404 回退 `resume` | `backend/internal/qbittorrent/torrent.go:483`，兼容 4.x/5.x |
| 强制开始 | `startNow` | `torrents/setForceStart value=true` | `backend/internal/qbittorrent/torrent.go:493` |
| 暂停 | `stop` | `torrents/stop`，404 回退 `pause` | `backend/internal/qbittorrent/torrent.go:503` |
| 校验 | `verify` | `torrents/recheck` | `backend/internal/qbittorrent/torrent.go:513` |
| 重新宣告 | `reannounce` | `torrents/reannounce` | `backend/internal/qbittorrent/torrent.go:522` |
| 删除 | `remove` | `torrents/delete` + `deleteFiles` | `backend/internal/qbittorrent/torrent.go:531` |
| 复制名称 | `copyName` | 纯前端 | — |
| 复制磁力 | `copyMagnet` | 纯前端，`magnetFromHash` 兜底 | `backend/internal/qbittorrent/mapper.go:114` |
| 复制路径 | `copyPath` | 纯前端 + `PATH_MAPPINGS` | `frontend/src/utils/pathMapping.ts:43` |
| 编辑标签 | `labels` | `torrents/setTags`，旧版退化为 addTags/removeTags | `backend/internal/qbittorrent/torrent.go:711` |
| 队列 置顶 | `queue:top` | `torrents/topPrio` | `backend/internal/qbittorrent/torrent.go:880` |
| 队列 上移 | `queue:up` | `torrents/increasePrio` | 同上 |
| 队列 下移 | `queue:down` | `torrents/decreasePrio` | 同上 |
| 队列 置底 | `queue:bottom` | `torrents/bottomPrio` | 同上 |
| 编辑 Tracker | `trackers` | 先 `removeTrackers` 再 `addTrackers` | `backend/internal/qbittorrent/torrent.go:784` |
| 批量替换 Tracker | `replaceTrackers` | `ReplaceTracker` + 正则 | `backend/internal/qbittorrent/torrent.go:921` |
| 其他属性 → 顺序下载 | `other` | `torrents/toggleSequentialDownload` | `backend/internal/qbittorrent/torrent.go:838`，先读后写避免取反 |
| 其他属性 → 上传/下载限速 | `other` | `torrents/setUploadLimit` / `setDownloadLimit` | `backend/internal/qbittorrent/torrent.go:574-587` |
| 其他属性 → 分享率 | `other` | `torrents/setShareLimits` | `backend/internal/qbittorrent/torrent.go:590`，-1 不限 / -2 跟随全局 |
| 其他属性 → 做种空闲 | `other` | `inactiveSeedingTimeLimit` | `backend/internal/qbittorrent/torrent.go:614`，失败仅 Debug 日志 |
| 批量清理已完成 | `deleteCompleted` | 批量删除 | — |

### 1.2 ⚠️ 降级生效项（6）

| 位置 | 现象 | 根因（代码） |
|:--|:--|:--|
| 右键 → 强制开始 | QB 无「忽略队列限制」独立语义之外的意义；队列未开启时与「开始」基本等价 | QB 用 `setForceStart` 表达，仅影响 `max_active_*` 轮转 |
| 右键 → 队列四项 | 需 qBittorrent 侧开启「队列管理」，否则报错 | `backend/internal/qbittorrent/torrent.go:891-893` 明确包了错误文案 |
| 其他属性 → 分享率「不限」 | QB 只有 -1/-2 两态，面板的「不限 / 跟随全局」在 QB 下都映射为 -1 | `backend/internal/qbittorrent/torrent.go:596-601`：`case 1` 与 `case 2` 分支完全一致 |
| 其他属性 → 做种空闲 | 写入失败被静默吞掉（仅 `slog.Debug`） | `backend/internal/qbittorrent/torrent.go:626-628` |
| 队列位置（`queuePosition`） | 退化为「置顶」，不接受任意位置 | `backend/internal/qbittorrent/torrent.go:630-636` |
| 更改路径（`path`） | 必须勾选「同时移动文件」，否则直接报错 | `backend/internal/qbittorrent/torrent.go:864-867` |

> 「改为不限分享率」与「跟随全局」在 QB 下行为相同 —— 这是 QB `setShareLimits` 的 API 限制，非 bug，但界面无任何提示。

### 1.3 ❌ 失效项（4）—— 建议在 QB 下隐藏

这四项在 QB 下**调用必然失败或静默无效**，属多余入口：

| 菜单项 | 菜单 key | 失效方式 | 证据 |
|:--|:--|:--|:--|
| **其他属性 → 带宽优先级** | `other` | **静默无效**：后端明确忽略并只打 Debug 日志 | `backend/internal/qbittorrent/torrent.go:685-691`：`if patch.BandwidthPriority != nil \|\| patch.PeerLimit != nil \|\| patch.HonorsSessionLimits != nil { slog.Debug(...) }` |
| **其他属性 → 连接数（peers）** | `other` | 同上，静默无效 | 同上 |
| **其他属性 → 遵循全局限速** | `other` | 同上，静默无效 | 同上 |
| **重命名** | `rename` | **必失败**：顶层重命名传 `path=torrent.name`，QB 的 `renameFile`/`renameFolder` 要求 path 为**种子内相对路径**，用种子名去匹配必然落空 | 前端 `frontend/src/components/TorrentMenu.tsx:665`：`await torrentApi.rename(torrent.id, torrent.name, renameName.trim())`；后端 `backend/internal/qbittorrent/torrent.go:898-918` 先试 renameFile 再试 renameFolder |

> **重要发现**：前三项在前端**已有能力判断**，但只覆盖了「限速」区块，未覆盖「带宽优先级 / 遵循全局限速 / 连接数」。
> - `frontend/src/components/TorrentMenu.tsx:767` 用 `caps?.perTorrentLimits !== false` 包住了 **优先级 + 遵循全局限速** 两项
> - `frontend/src/components/TorrentMenu.tsx:848` 用 `caps?.peerLimit !== false` 包住了 **连接数**
>
> 而 QB 驱动在 `backend/internal/qbittorrent/client.go:209` 声明 `PerTorrentLimits: false`、`PeerLimit: false` —— **该判断实际是生效的**。
> 因此这三项在 QB 下**已经隐藏**，不构成多余入口。本节结论修正为：**有效，无需处理**。

**重命名是唯一真正多余的项。** 它不是能力开关能解决的问题（QB 声明 `RenameFile: true`），
而是前端**调用契约错了**：把「顶层重命名」实现为 `rename(种子名 → 新名)`，
这对 Transmission（`torrent-set name`）语义正确，对 QB（文件级 rename）必然失败。
QB 下要么隐藏该项，要么改为「重命名主文件名」并传入真实文件路径。

### 1.4 其他观察

- `openDir`（打开所在文件夹）在 `frontend/src/components/TorrentMenu.tsx:112-114` 由 `ctx.canRevealPath` 控制，
  仅宿主支持 `fs.revealPath` 时显示，与下载器类型无关 —— **QB 下同样有效**（走 `useRevealPath` → `mapPath` → 复制路径兜底）。
- 表头右键（列显隐 / 重置布局，`frontend/src/components/TorrentList/DesktopTable.tsx:388-400`）与下载器完全无关，**始终有效**。
- 侧边栏分组右键（`frontend/src/components/Sidebar/index.tsx:1832-1858`）含两项：
  - 「全选该组」——**有效**
  - 「为此分组添加限速规则」——**在 QB 下多余**，见下一节。

---

## 二、自动化功能

> 本节按「**有效性**」与「**多余性**」两条轴分别审计。
> 多余性判定标准：① 与 qBittorrent **自带原生能力**重复；② 与 SeedArk **另一自动化模块**重复；③ 被宿主能力覆盖；④ 产出无人消费。

### 2.0 多余性总览

| 模块 | 与 QB 原生能力重复？ | 与其他模块重复？ | 结论 |
|:--|:--|:--|:--|
| **自动文件管理** | ⚠️ **职责重叠**：QB 原生「自动种子管理（TMM）+ 分类保存路径」能覆盖「按分类归档」 | 站点口径与「做种策略」共享，非重复 | ⚠️ **半多余**（站点维度不可替代），见 2.4 |
| **做种策略** | ⚠️ **部分重复**：QB 原生全局分享率/做种时长限制 + 达标动作覆盖「全局单值」场景 | 同上 | ✅ **不算多余**（按站点差异化强于 QB），见 2.5 |
| **分组限速** | 🚫 不适用（QB 无 HonorSessionLimits 语义） | 与「其他属性」单种限速冲突 | 🚫 **多余且有害**，见 2.1 |

### 2.1 分组限速（`backend/internal/speedpolicy/service.go`）—— 🚫 整体不适用

**结论：在 qBittorrent 下完全无效且多余，应在 QB 下隐藏入口。**

机制上，该引擎靠下发 `HonorsSessionLimits=false` + 单种限速来接管组内种子：

```
backend/internal/speedpolicy/service.go:474-484
// limitPayload 下发单种限速。必须同时关掉 honorsSessionLimits：
// 该标记为 true 时 Transmission 直接用全局限速，本种子的限速字段不生效。
func limitPayload(dir string, cap int64) driver.TorrentPatch {
	p := driver.TorrentPatch{HonorsSessionLimits: &falseVal}
	...
}
```

而 QB 驱动**直接忽略 `HonorsSessionLimits`**（`backend/internal/qbittorrent/torrent.go:685-691`）。
更关键的是引擎的「不可接管」判定依赖该字段：

```
backend/internal/speedpolicy/service.go:192-193
// 跟随全局限速（honorsSessionLimits）不算固定——那恰恰是引擎要接管的常态：
// Transmission 的种子默认跟随全局，此时本种子的限速字段会被忽略，
```
`backend/internal/qbittorrent/mapper.go:95-97` 把 QB 的 `HonorsSessionLimits` **恒定为 false**
（注释：「qBittorrent 的单种限速是绝对值，不存在『跟随全局』开关，按 false 上报」），
于是引擎的 `isFixed()` 会把所有自带单种限速的种子误判为「可接管」，与用户手动设置的限速互相打架。

**处置建议**：`frontend/src/components/Sidebar/index.tsx` 的 `GroupCtxMenu` 增补 `caps.bandwidthGroups` 判断（QB 为 `false`），
并隐藏「设置 → 自动化 → 分组限速」整页。后端 `speedpolicy` 在 QB 下应直接不启动（`Run()` 早退）。

**多余性补充**：即便忽略引擎实现，该功能本身在 QB 下也**无存在意义** ——
QB 已提供「全局限速 + 备用限速 + 定时调度」（`backend/internal/qbittorrent/qbprefs.go:293-304` 的
`dl_limit` / `up_limit` / `alt_dl_limit` / `alt_up_limit` / `scheduler_enabled`），
且 SeedArk 已把这套完整接进设置面板。用户想给「一组种子」限速时，QB 侧的正确做法是分类 + 单种限速，
而 SeedArk 的引擎恰恰会去**改写这些手动限速**。

### 2.4 自动文件管理 —— ⚠️ 有效，但与 QB 原生能力**职责重叠**（属**半多余**）

功能本身有效（见 2.3），但**它做的事 qBittorrent 自己就能做一部分**：

| SeedArk 自动文件管理 | qBittorrent 原生等价能力 | 位置 |
|:--|:--|:--|
| 种子按**分类/标签**归档到目录 | 分类保存路径 + 自动种子管理（TMM） | `backend/internal/qbittorrent/qbprefs.go:213-218`：`auto_tmm_enabled` / `torrent_changed_tmm_enabled` / `category_changed_tmm_enabled` / `use_category_paths_in_manual_mode` |
| 「规则命中即移动」 | `category_changed_tmm_enabled`（分类变化自动重新定位） | 同上 |
| 完成即移动 | `save_path_changed_tmm_enabled` + TMM | 同上 |

**但注意**：SeedArk 的添加流程**主动关掉了 TMM** ——
`backend/internal/qbittorrent/torrent.go:378` 与 `:412` 在设置 `savepath` 时强制下发 `autoTMM=false`。
这等于把 QB 的原生自动管理禁掉了，然后再用 SeedArk 的引擎重新实现一遍。

**判定：不是纯粹的多余，但存在真实的职责重叠。** 两种合理路线：
1. **保留 SeedArk 引擎**（跨下载器统一语义，Transmission 下没有 TMM）→ 则在 QB 下应在设置面板
   提示「QB 原生 TMM 已被 SeedArk 关闭，请勿同时启用」，避免两套归档逻辑打架。
2. **QB 下改用原生 TMM** → 则不强制 `autoTMM=false`，自动文件管理对该服务器隐藏。

> 站点维度的匹配口径（`backend/internal/automove/service.go:149-195` 的 `matchRule`）
> 依赖 `t.Trackers[].SiteName`，而 QB 的 `SiteName` 由 `backend/internal/qbittorrent/mapper.go:199`
> 从 tracker URL 推导 —— 这与 QB 分类是**两套不同的分组维度**，无法用 TMM 直接替代，
> 因此若要完整保留「按站点归档」，当前引擎仍是必需的。

### 2.5 做种策略 —— ✅ 有效，但与 QB 原生做种限制部分重复（属**半多余**）

QB 原生就有**全局**分享率 / 做种时长 / 闲置做种限制，且带三种达标动作：

```
backend/internal/qbittorrent/qbprefs.go:344-357
fb("max_ratio_enabled", "启用全局分享率限制", ...)
hint(ff("max_ratio", "分享率上限", ...), "-1 表示不限制", ...)
fb("max_seeding_time_enabled", "启用全局做种时长限制", ...)
fb("max_inactive_seeding_time_enabled", "启用全局闲置做种限制", ...)
fenum("max_ratio_act", "达到限制时的动作", ...,
    optInt(ratioStop,    "停止种子", ...),
    optInt(ratioRemove,  "删除种子", ...),
    optInt(ratioWipe,    "删除种子及其文件", ...),
    optInt(ratioSuper,   "为种子启用超级做种", ...))
```

对照 SeedArk 做种策略的三种动作（`backend/internal/seedpolicy/service.go:318-324`）：
`pause` / `delete` / `deleteData` —— **与 QB 的 `ratioStop` / `ratioRemove` / `ratioWipe` 一一对应**。

| 维度 | SeedArk 做种策略 | QB 原生 | 谁更强 |
|:--|:--|:--|:--|
| 分享率目标 | ✅ 按站点 / 标签 / 名称分别设 | 仅全局一个值 | SeedArk |
| 做种天数目标 | ✅ | ✅（`max_seeding_time`） | 平 |
| 上传量目标 | ✅ `minUploadGB` | ❌ 无 | SeedArk |
| 达标动作 | pause / delete / deleteData | stop / remove / wipe / super | 平 |
| 站点维度排除 | ✅ | ❌ | SeedArk |
| 全局最低做种时长 | ✅ `guard.minSeedHours` | ❌ | SeedArk |
| 执行时机 | 后台 60s 轮询 | 引擎内实时 | QB |

**判定：重复度低，SeedArk 策略在「按站点差异化目标」上明显更强，属合理增值而非多余。**
但有一处**真实的界面冗余**：QB 原生 `max_ratio` / `max_seeding_time` 已在设置面板暴露
（`backend/internal/qbittorrent/qbprefs.go:344-357`），用户在 QB 下会看到**两处做种限制入口**。
建议在设置面板这两项处加提示：「与 SeedArk 做种策略叠加生效，建议只启用其一」。

### 2.6 其他多余性观察

| 项 | 判定 | 证据 |
|:--|:--|:--|
| MCP `get_seed_policy_report` | ✅ 有效，非多余 | `backend/internal/mcpserver/tools.go:36-39` 只读评估，跨下载器通用 |
| MCP `execute_seed_policy` | ✅ 有效，非多余 | `tools.go:120-123`，高危开关管控 |
| MCP 侧**无**分组限速工具 | ✅ 正确 —— 该能力本就不该外露 | `tools.go` 内无 speedpolicy 相关工具 |
| 「设置 → 自动化」三项 UI 均无 `caps` 门控 | ❌ **缺陷** | `frontend/src/components/SpeedPolicyManager.tsx:34-48`、`SeedPolicyManager.tsx:68-88`、`AutoMoveManager.tsx:31-41` 均未读 `caps` |

> 最后一行是本次审计发现的**共性缺口**：三个自动化面板没有一个做能力门控，
> 而下载器驱动层是提供了 `caps` 的。分组限速因此成为唯一「不适用却完全可操作」的模块。

### 2.2 做种策略（`backend/internal/seedpolicy/service.go`）—— ✅ 有效，一处需注意

引擎通过 `rpc.Manager` 调用，**不直接依赖 Transmission 特性**：

```
backend/internal/seedpolicy/service.go:135   torrents, err := s.manager.GetTorrentsFresh(ctx)
backend/internal/seedpolicy/service.go:145   if m, err := s.manager.GetTorrentSites(ctx)
backend/internal/seedpolicy/service.go:320   err = s.manager.StopTorrents(ctx, ids)
backend/internal/seedpolicy/service.go:322   err = s.manager.RemoveTorrents(ctx, ids, false)
backend/internal/seedpolicy/service.go:324   err = s.manager.RemoveTorrents(ctx, ids, true)
```

达标判定依赖的字段 QB 均提供了映射：

| 字段 | QB 来源 | 位置 |
|:--|:--|:--|
| `IsFinished` | `Progress >= 1` | `backend/internal/qbittorrent/mapper.go:82` |
| `UploadRatio` | `Ratio` | `backend/internal/qbittorrent/mapper.go:69` |
| `SecondsSeeding` | `SeedingTime` | `backend/internal/qbittorrent/mapper.go:70` |
| `Error` | `mapState` → `status=3` | `backend/internal/qbittorrent/mapper.go:277-280` |

**⚠️ 唯一风险：`trackerUnreachable()` 在 QB 下永不返回 true。**

```
backend/internal/seedpolicy/service.go:507-521
func trackerUnreachable(t *rpc.Torrent) bool {
	attempted := false
	for _, ts := range t.TrackerStats {
		if ts.IsBackup { continue }
		if ts.LastAnnounceSucceeded { return false }
		if ts.LastAnnounceTime > 0 { attempted = true }
	}
	return attempted
}
```

该保护栏要求 `LastAnnounceTime > 0`（即「尝试过但全失败」）。
但 QB 的 `applyTrackers`（`backend/internal/qbittorrent/mapper.go:204-215`）**从不填充 `LastAnnounceTime`**，
只填 `AnnounceState` / `LastAnnounceResult` / `LastAnnounceSucceeded`。

后果：QB 下**这层保护栏形同虚设** ——
当所有 Tracker 都 announce 失败（站点侧没记账）时，Transmission 下会跳过该种子，
QB 下却会把本地分享率当真、照常执行暂停/删除。这是**有实际数据风险的差异**，建议优先修。

### 2.3 自动文件管理（`backend/internal/automove/service.go`）—— ✅ 有效

- 移动靠 `s.manager.SetTorrentLocation(ctx, tt.ID, rule.TargetDir, true)`（`backend/internal/automove/service.go:88`），
  `move=true` 恒定 —— QB 只支持连同文件搬移（`backend/internal/qbittorrent/torrent.go:859-869` 对 `move=false` 直接报错），
  这里传 true **恰好是 QB 唯一支持的形态**，无需改动。
- 站点匹配（`matchRule`，`backend/internal/automove/service.go:149-195`）按 `t.Trackers[].SiteName` / `Announce` 主机名匹配；
  QB 经 `applyTrackers` 填充 `SiteName`（`backend/internal/qbittorrent/mapper.go:199`：`siteName := siteName(tr.URL)`），**有效**。
- 标签匹配与名称匹配均为纯字段比较，**有效**。
- `IsFinished` 判定 QB 映射为 `Progress >= 1`，**有效**。

### 2.6 其他多余性观察

| 项 | 判定 | 证据 |
|:--|:--|:--|
| MCP `get_seed_policy_report` | ✅ 有效，非多余 | `backend/internal/mcpserver/tools.go:36-39` 只读评估，跨下载器通用 |
| MCP `execute_seed_policy` | ✅ 有效，非多余 | `tools.go:120-123`，高危开关管控 |
| MCP 侧**无**分组限速工具 | ✅ 正确 —— 该能力本就不该外露 | `tools.go` 内无 speedpolicy 相关工具 |
| 「设置 → 自动化」三项 UI 均无 `caps` 门控 | ❌ **缺陷** | `frontend/src/components/SpeedPolicyManager.tsx:34-48`、`SeedPolicyManager.tsx:68-88`、`AutoMoveManager.tsx:31-41` 均未读 `caps` |

> 最后一行是本次审计发现的**共性缺口**：三个自动化面板没有一个做能力门控，
> 而下载器驱动层是提供了 `caps` 的。分组限速因此成为唯一「不适用却完全可操作」的模块。

---

## 三、汇总与处置建议

### 必须修（功能缺陷）

| # | 问题 | 位置 | 建议 |
|:--|:--|:--|:--|
| 1 | 右键「重命名」在 QB 下必然失败（path 传种子名，QB 要求种子内相对路径） | `frontend/src/components/TorrentMenu.tsx:665` | QB 下隐藏该项，或改为让用户选文件后传真实路径 |
| 2 | 做种策略的 `trackerUnreachable` 保护栏在 QB 下失效，可能误删未记账的种子 | `backend/internal/seedpolicy/service.go:507-521` + `backend/internal/qbittorrent/mapper.go:204-215` | QB 侧补填 `LastAnnounceTime`，或改用 `AnnounceState` 判定 |
| 3 | 分组限速引擎在 QB 下会与用户手动限速互相打架（`HonorsSessionLimits` 恒 false + 被驱动忽略） | `backend/internal/speedpolicy/service.go:192`、`474` | QB 下不启动引擎，界面隐藏入口 |

### 建议优化（体验）

| # | 问题 | 位置 | 建议 |
|:--|:--|:--|:--|
| 4 | 分享率「不限」与「跟随全局」在 QB 下行为相同，无提示 | `backend/internal/qbittorrent/torrent.go:596-601` | 界面按 caps 收敛选项或加说明 |
| 5 | 做种空闲上限写入失败仅 Debug 日志 | `backend/internal/qbittorrent/torrent.go:626-628` | 至少 toast 一次 |
| 6 | 队列四项需 QB 开启队列管理 | `backend/internal/qbittorrent/torrent.go:891-893` | 已有错误文案，可前置为界面提示 |
| 7 | **三个自动化面板全无 `caps` 门控**（共性缺口） | `frontend/src/components/SpeedPolicyManager.tsx:34-48`、`SeedPolicyManager.tsx:68-88`、`AutoMoveManager.tsx:31-41` | 统一补 caps 判断 |
| 8 | 自动文件管理与 QB 原生 TMM 职责重叠，且添加时强制 `autoTMM=false` 关掉了原生能力 | `backend/internal/qbittorrent/torrent.go:378,412` + `qbprefs.go:213-218` | 二选一：要么提示「勿同时启用」，要么 QB 下改用原生 TMM 并隐藏引擎 |
| 9 | QB 下做种限制有**两个入口**（原生偏好 + SeedArk 做种策略），叠加生效 | `backend/internal/qbittorrent/qbprefs.go:344-357` | 在原生项处提示「与 SeedArk 做种策略叠加，建议只启用其一」 |

### 确认多余、建议清理

| 项 | 位置 | 理由 |
|:--|:--|:--|
| `systemApi.command` / `caps.systemCommand` | `frontend/src/api/torrent.ts:111`（端点 `backend/internal/api/handlers.go:273`） | 全前端零引用，**死代码**（见附） |
| 分组限速整页入口 | `frontend/src/components/SpeedPolicyManager.tsx` | QB 下不适用 + 与手动限速冲突 |


### 确认无问题

- 右键菜单 21 项在 QB 下**有效**
- 表头右键（列显隐）、侧边栏「全选该组」与下载器无关，**始终有效**
- 「打开所在文件夹」由**宿主平台能力**（非下载器能力）控制，QB 下**有效**
- 自动文件管理**完全有效**
- 做种策略**主体有效**（仅上述保护栏差异）
- 带宽优先级 / 连接数 / 遵循全局限速三项**已被 caps 正确隐藏**（`frontend/src/components/TorrentMenu.tsx:767,848` + `backend/internal/qbittorrent/client.go:209,208`）

---

## 附：能力自述（`backend/internal/qbittorrent/client.go:188-213`）

QB 声明为 `false` 的能力，界面各处**均已正确使用**：

| 能力 | QB | 前端消费点 |
|:--|:--|:--|
| `BandwidthGroups` | false | `frontend/src/components/SessionPanel/index.tsx:97`、`frontend/src/components/TorrentMenu.tsx:640` |
| `Blocklist` | false | `frontend/src/components/SettingsModal/index.tsx:1196` |
| `PortTest` | false | `frontend/src/components/SettingsModal/index.tsx:829` |
| `ScriptHooks` | false | `frontend/src/components/SettingsModal/index.tsx:1213` |
| `QueueStalled` | false | `frontend/src/components/SettingsModal/index.tsx:1023` |
| `PeerLimit` | false | `frontend/src/components/TorrentMenu.tsx:848`、`frontend/src/components/TorrentDetail/index.tsx:511` |
| `PerTorrentLimits` | false | `frontend/src/components/TorrentMenu.tsx:767` |
| `FileHandling` | false | `frontend/src/components/SettingsModal/index.tsx:971` |
| `UtpToggle` | false | `frontend/src/components/SettingsModal/index.tsx:1172` |
| `GlobalSeedRatio` | true | `frontend/src/components/SettingsModal/index.tsx:992` |
| `IncompleteDir` | true | `frontend/src/components/SettingsModal/index.tsx:961` |
| `PieceBitmap` | true | 详情面板块位图 |
| `TrackerReplace` | true | 批量替换 Tracker |
| `AltSpeedSchedule` | true | 备用限速定时 |
| `SequentialDownload` | true | 顺序下载 |
| `QueueMove` | true | 队列四项 |
| `RenameFile` | true | ⚠️ 声明 true 但顶层重命名契约不匹配，见 1.3 |
| `SystemCommand` | true | ⚠️ 前端**无任何调用点**（`systemApi.command` 定义于 `frontend/src/api/torrent.ts:111` 但无人引用），属死代码 |

### 附带发现：`SystemCommand` / `systemApi.command` 为死代码

- 后端路由存在：`backend/internal/api/handlers.go:273` → `api.POST("/system/:action", h.systemCommand)`
- QB 实现存在：`backend/internal/qbittorrent/session.go:376-385`（`app/shutdown`；`reboot` 明确报错）
- **前端无调用点**：`frontend/src/api/torrent.ts:111` 的 `systemApi.command()` 在 `frontend/src` 内零引用
- `caps.systemCommand` 同样零消费

即：该功能为「保留了接口与实现、但界面从未接线」的状态，与 QB 无关（Transmission 下也一样没人调）。

**处置决定：保留，标注为预留接口，不删除。**

理由：后端链路是完整可用的（`backend/internal/driver/driver.go:277` → `backend/internal/qbittorrent/session.go:376` / `backend/internal/rpc/raw.go:164` → `backend/internal/rpc/router.go:81` → `backend/internal/api/handlers.go:273`），
且端点在 `backend/internal/api/session.go:92` 要求 API token 或平台允许嵌入，属于有意的对外接口。
「前端零引用」只是说明 UI 没接线，不等于能力无用——MCP 与外部脚本可直接 POST。
删除反而会移除一个可用的后端能力，代价高于收益。

因此本项**不作为缺陷修复**，仅在此文档中标注状态为「预留（UI 未接线）」。

---

## 四、修复状态

依据本文档「三、汇总与处置建议」，已完成以下修复（均有回归测试或编译验证）：

| # | 问题 | 处置 | 改动位置 |
|:--|:--|:--|:--|
| 1 | 右键「重命名」在 QB 下必然失败 | ✅ 已修复 | `backend/internal/qbittorrent/torrent.go`：`RenameFile` 识别顶层重命名，经 `torrentName` 比对后走 `renameTopLevel`，用纯函数 `planTopLevelRename` 生成改名步骤 |
| 2 | 做种策略 `trackerUnreachable` 保护栏在 QB 下永不触发（数据风险） | ✅ 已修复 | `backend/internal/qbittorrent/mapper.go` `applyTrackers`：status 4/5/6 时回填 `LastAnnounceTime`（取 `ActivityDate`，为 0 则回退 `AddedDate`）；**刻意不用 1 之类哨兵值**，否则详情面板会渲染成 1970-01-01 |
| 3 / 7 | 分组限速在 QB 下无效且有害 | ✅ 已修复 | 新增能力位 `HonorsSessionLimits`（`backend/internal/models/models.go`）；QB 报 `false`（`backend/internal/qbittorrent/client.go`），Transmission 报 `true`（`backend/internal/rpc/client.go`）；引擎在 `HonorsSessionLimits=false` 时直接 `releaseAll` 并跳过本轮（`backend/internal/speedpolicy/service.go` `Tick`）；前端按 `caps.honorsSessionLimits` 隐藏入口（`frontend/src/components/SettingsModal/index.tsx`、`frontend/src/components/Sidebar/index.tsx` 的分组右键菜单与触屏长按菜单） |
| 4 | 分享率「不限」与「跟随全局」在 QB 下行为相同 | ✅ 已修复 | `backend/internal/qbittorrent/torrent.go` `SetTorrent`：原 case 1 与 case 2 分支相同导致「单种覆盖」静默退化为「不限」，改为仅 mode 2 写 -1；`backend/internal/qbittorrent/mapper.go` 回读改为三态（`>=0`→1 单种覆盖 / `-1`→2 不限 / 其余→0 跟随全局） |
| 5 | 做种空闲上限写入失败仅 Debug 日志（静默） | ✅ 已修复，且发现额外 bug | `backend/internal/qbittorrent/torrent.go`：原代码硬编码 `ratioLimit=-1`/`seedingTimeLimit=-1`，会把同一面板里刚设置的分享率**覆盖成「不限」**；改为先用新增的 `currentShareLimits` 读回现值再整体写回，读不到时明确报错而非用 -1 顶替，写入失败也改为返回带版本提示的错误 |
| 6 | 队列四项需 QB 开启队列管理 | ✅ 无需改动 | `backend/internal/qbittorrent/torrent.go` 已有文案「队列调整失败（qBittorrent 需开启「队列管理」）」，前端拦截器（`frontend/src/api/client.ts:34-36`）会把它作为 toast 显示给用户 |
| 8 | 自动文件管理与 QB 原生 TMM 职责重叠 | ✅ 属正确设计，不改 | 添加时强制 `autoTMM=false`（`backend/internal/qbittorrent/torrent.go:378,412`）正是为了**防止原生 TMM 与 SeedArk 自动文件管理互相打架**，是有意为之；原生 TMM 也无法替代站点维度归档 |
| 9 | QB 下做种限制有两个入口，叠加生效 | ⚠️ 已知叠加，不自动改 | 原生偏好（`backend/internal/qbittorrent/qbprefs.go:344-357`）与 SeedArk 做种策略确实叠加。两者语义不同（QB 四态动作 vs SeedArk 按站点/标签分目标），**不建议由程序单方面改写用户的 QB 原生设置**——那会造成更难排查的意外。保留为文档说明 |
| — | `systemApi.command` / `caps.systemCommand` | ✅ 保留为预留 | 见上节「处置决定」 |

### 验证

- `go build ./...` 通过
- 新增回归测试（`backend/internal/qbittorrent/qbittorrent_test.go`）全部通过：
  - `TestApplyTrackersLastAnnounceTime`（7 个 status 子例）
  - `TestApplyTrackersAttemptedFallsBackToAdded`
  - `TestPlanTopLevelRename`（5 个子例）
  - `TestSeedRatioModeRoundTrip`（4 个子例：-2→0 / 0→1 / 1.5→1 / -1→2）
  - `TestCapabilities`（含新增的 `HonorsSessionLimits: false` 断言）
- `go vet ./...` 通过；`go test ./...` 全绿（10 个包全部 ok，含 `internal/qbittorrent`）
- `pnpm typecheck` 通过；`pnpm build` 通过
- 过程中曾观察到 `backend/internal/qbittorrent/qbprefs_test.go` 的 `TestSettingsSchemaShape` 失败，经排查是工作树中**他人未提交的** `qbprefs.go` 改动（枚举 `auto_tmm_enabled` 等 ValueType 报 `"bool"`）引入，与本文档的修复无关；该改动随后自行收敛，现已 PASS。

---
