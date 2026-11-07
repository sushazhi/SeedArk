# 保种台 · 验收记录

> 验证对象：`docs/pt-feature-proposal.md` §11 列出的 5 个方向 MVP（后端 `backend/internal/pt` + 前端 `components/PT/PTDialog.tsx`）。
> 验证口径：`docs/pt-api-contract.md`。
> 说明：原计划由独立验证员执行，该成员两次启动即异常退出（无产出、无报告），改由 Lead 按同一清单自验并留下可复现记录。所有结论均给出命令与实测输出。

## 1. 结论

**通过。** 后端 `build` / `vet` / `test` 全绿，前端 `typecheck` / `build` 全绿，契约 14 条路径全部实现（16 条路由，含同路径多方法），真机端到端跑通「删除 → 自动留档 → 复活 → 删档」与「站点规则 → 日历/风险联动」。自测过程中发现并修复 4 个真实缺陷（见 §5），无遗留阻塞问题。

## 2. 命令与结果

| 命令 | 结果 |
|:--|:--|
| `cd backend && go build ./...` | exit 0 |
| `cd backend && go vet ./...` | exit 0，无输出 |
| `cd backend && go test ./... -count=1` | exit 0，全部包 `ok`（含 `internal/pt` 0.111s） |
| `gofmt -l backend/internal/pt/ backend/internal/api/pt.go` | 无输出（本次新增代码格式干净） |
| `cd frontend && pnpm typecheck`（`tsc --noEmit`） | exit 0，无错误 |
| `cd frontend && pnpm build` | exit 0，`✓ built in 711ms` |

备注：`gofmt -l backend/internal/` 会列出 50+ 个**既有**文件（automove/config/rpc/qbittorrent…），属仓库历史状态，与本次改动无关；本次新增/修改的 `internal/pt/*`、`internal/api/pt.go` 均无输出。

## 3. 契约逐条比对

契约中出现 14 条路径，`backend/internal/api/handlers.go:276-291` 注册 16 条路由（`/pt/sites` 与 `/pt/vault/:id` 各含两种方法）：

| 契约路径 | 方法 | 路由行 | 结论 |
|:--|:--|:--|:--|
| `/pt/overview` | GET | 276 | ✅ |
| `/pt/ledger` | GET | 277 | ✅ |
| `/pt/calendar` | GET（`?month=`） | 278 | ✅ |
| `/pt/vault` | GET（`?q=`） | 279 | ✅ |
| `/pt/vault/:id` | GET | 280 | ✅ |
| `/pt/vault/:id/file` | GET | 281 | ✅ 无留档文件时 404 + `{code:1,message:"该档案没有留存的种子文件"}` |
| `/pt/vault/:id/revive` | POST | 282 | ✅ 返回 `{id, via}` |
| `/pt/vault/:id` | DELETE | 283 | ✅ 返回 `{deleted:true}` |
| `/pt/matrix` | GET | 284 | ✅ |
| `/pt/publish/preview` | POST | 285 | ✅ |
| `/pt/publish/kit` | POST（multipart） | 286 | ✅ zip 含 `bbcode.txt`/`mediainfo.txt`/`checklist.txt`(+可选 `.torrent`) |
| `/pt/sites` | GET | 287 | ✅ `{profiles, detected}` |
| `/pt/sites` | POST | 288 | ✅ 返回保存后的 profile |
| `/pt/sites/seed` | POST | 289 | ✅ 返回 `{created:n}` |
| `/pt/sites/:id` | DELETE | 290 | ✅ 返回 `{deleted:true}` |
| `/pt/sample` | POST | 291 | ✅ 返回 `{sampled:true,count:n}` |

字段层面抽查（以真机响应为准）：`TorrentRef` 含 `id/name/hash/site/serverName/kind/size/seedDays/ratio/uploaded30d/efficiency/score/lastUpload/status/labels/hrRisk/hrDeadline/releaseAt/releaseReason/estimated`；`Calendar` 的 `unknown` 为 `CountBytes`（`{count,bytes}`）非数组；`Matrix.Group` 含 `key/size/totalBytes/duplicateBytes/sameDownloader/copies`。与契约一致。

## 4. 关键实现点核对

| 检查项 | 位置 | 结论 |
|:--|:--|:--|
| 删除前留档（单删） | `internal/api/torrent.go` `deleteTorrent` | ✅ `h.ptSvc.Capture` 在 `RemoveTorrents` 之前，失败仅 `slog.Warn`，不阻断 |
| 删除前留档（批删） | `internal/api/torrent.go` `deleteTorrents` | ✅ 先按 id 逐个 `Capture`（上限 200），再一次性 `RemoveTorrents` |
| 服务启动 | `cmd/server/main.go` | ✅ `pt.New(...)` 失败仅告警置 nil，成功则 `go ptSvc.Run(ctx)`；nil 时 `/api/pt/*` 全部 503 |
| 无自动删除/上传 | `internal/pt/*` | ✅ 全包无删除种子的上游调用，也无任何外发上传请求；`releaseAt` 仅用于展示 |
| 空数组不返回 null | `service.go` `Overview`/`Ledger`/`Preview`/`VaultList`/`Matrix`/`Calendar` | ✅ 均显式初始化为空切片 |
| 前端入口 | `App.tsx:89,443`、`TopBar/index.tsx:58,296` | ✅ 状态 + 挂载 + `onOpenPT` + `Sprout` ToolBtn（`isMobile` 也渲染） |
| 无新增依赖 | `git diff frontend/package.json frontend/pnpm-lock.yaml backend/go.mod backend/go.sum` | ✅ 无 diff |
| 无品牌色字面量 | `PTDialog.tsx` / `api/pt.ts` / `types/pt.ts` | ✅ 无 `#rrggbb`，颜色全部走 `var(--color-*)` |
| 最小字号 11px | `PTDialog.tsx` | ✅ 无 `text-[10px]` / `text-[0.7rem]` 以下写法 |

## 5. 自测中发现并修复的缺陷

| # | 缺陷 | 影响 | 修复 |
|:--|:--|:--|:--|
| D1 | `window30` 把「当天刚建立的采样基线」当真实增量 | 首次运行/首次安装当天，所有种子 `uploaded30d=0` 且 `estimated=false`，把刚上传过的种子误报成零上传；台账 `zeroUpload` 全量飘红 | `rules.go`：仅当采样覆盖 ≥29 天才采信真实增量，覆盖不足按覆盖天数折算并 `estimated=true`；基线高于累计（计数器重置）时同样退回折算 |
| D2 | 空数组序列化为 `null` | 前端 `o.upcoming.length` / `o.sites.length` 会 TypeError 白屏 | `service.go`：`Overview.Sites/Upcoming`、`Ledger.Sites`、`Preview.Warnings` 初始化空切片；前端另加 `?? []` 兜底 |
| D3 | `/pt/sample` 返回 `count=0` | 同一天重复采样时计数为「上传量变化数」，看起来像采样失败 | `service.go` `Sample`：返回「采到的种子数」（与契约 `count` 语义一致） |
| D4 | `safeHash` 是字符白名单而非校验器 | `"../evil"` 被过滤成 `"e"`，不同非法输入会塌缩成同名文件（v1 不落盘 .torrent，暂无实害） | `store.go` `vaultTorrentPath`：要求规范化后为 40/64 位 hex 且过滤前后完全一致，否则返回空 |

另修：`vaultPut` 重复留档时整条替换会把首次的文件清单/磁力覆盖为空 → 改为仅在新记录确实带值时覆盖（与注释一致）。

单元测试随之调整 3 条 `window30` 用例以匹配新语义（覆盖满 30 天才算真实值、覆盖不足折算、基线高于累计退回折算），其余用例未改。

## 6. 真机端到端记录

环境：`cmd/trmock` 作为下载器（`-addr 127.0.0.1:9092 -seed 20 -static`），后端以临时 `data_dir` 启动（`server_port: 8211`），全部请求走真实 HTTP 路由。

| 用例 | 实测结果 |
|:--|:--|
| `GET /api/pt/overview` | `uploaded30d=61270318793`、`avgEfficiency=8.3`、`upcoming=[]`（数组）、`sites=4`（按 tracker 主机名归并）、`duplicateBytes=28459353664`、`groupCount=6` |
| `GET /api/pt/ledger` | 20 行；有上传的 4 颗标 `estimated=true`；`totals.zeroUpload=16`（mock 中确实累计上传为 0 的种子） |
| `GET /api/pt/calendar` | 未配规则时 `days=[]`、`unknown={count:20,bytes:252209017273}`；配规则后 `days=2`、`unknown={count:15,...}` |
| `GET /api/pt/matrix` | 6 组，单组 `key="debian 12 5 0 amd64 dvd 1"`、`duplicateBytes=14394833977`、`sameDownloader=true` |
| `DELETE /api/torrents/6` | 200；随后 `GET /api/pt/vault` 出现对应档案，`hasTorrent=false`、`canRevive=true`，`files=1`（含文件名与体积） |
| `GET /api/pt/vault/:id/file` | 404 `{"code":1,"message":"该档案没有留存的种子文件"}` |
| `POST /api/pt/vault/:id/revive` | 200 `{"id":21,"via":"magnet"}`（真实重新添加成功） |
| `DELETE /api/pt/vault/:id` | 200 `{"deleted":true}`，列表与体积同步减少 |
| `POST /api/pt/publish/preview` | 字段齐全时 `warnings=[]`；全空时 3 条告警；`bbcode` 含 `[quote]`/`[hide]`/`[img]` |
| `POST /api/pt/publish/kit` | 200，994 字节 zip，条目 `['bbcode.txt','checklist.txt','fake.torrent','mediainfo.txt']` |
| `POST /api/pt/sites` → `/sites/seed` | 新建规则成功；`seed` 为未覆盖站点 `created=3`；`M-Team(match=private-hd)` 命中最长匹配，未生成重复占位 |
| 规则生效联动 | `hrRiskCount 0→5`、`readyCount 0→2`、日历首日 `{date:"2026-10-13",count:1,ready:true,hrRisk:true,reason:"min_days"}` |
| `POST /api/pt/sample` | 同日连续两次均 `{"count":20,"sampled":true}` |
| SPA / 未知路由 | `GET /` → 200；`GET /api/pt/nope` → 404（不 panic） |
| 落盘 | `data/pt/samples.json`（0600，含当日 20 条高水位）、`data/pt/vault/` 目录存在 |

## 7. 已知限制（判定为符合 v1 约定，不计缺陷）

1. 无 `.torrent` 导出能力（`driver.Backend` 未暴露），档案只留磁力；`hasTorrent=false`、`canRevive=true`；文件端点 404。
2. 发种资料包内文本为中文；`PublishRequest.lang` 已预留未使用。
3. `DEMO_MODE` 未补 `/pt/*` mock，演示模式打开 PT 弹窗显示错误态而非白屏。
4. 采样历史依赖运行时长：首日按折算值展示（带 `≈`），随采样累积逐步转为真实增量。
5. 所有释放时间/效率均为预计值，不触发任何自动操作。

## 8. 范围检查

- 改动全部落在 `backend/`、`frontend/src/`、`docs/`、`README.md`。
- `git status`：9 个修改 + 8 个新增（`docs/mockups/`、`docs/pt-api-contract.md`、`docs/pt-feature-proposal.md`、`docs/pt-verification.md`、`backend/internal/pt/`、`backend/internal/api/pt.go`、`frontend/src/api/pt.ts`、`frontend/src/types/pt.ts`、`frontend/src/components/PT/`）；无临时文件残留。
- 未提交任何 commit（按要求）。

## 9. 后续变更：上传量改为实测增量（2026-10-31）

用户要求「近 30 天上传改为实际上传量」，即放弃折算值。改动落在 `backend/internal/pt/{rules,store,types,service}.go`、`backend/internal/mcpserver/{tools,pt_tools}.go`、`frontend/src/types/pt.ts`、`frontend/src/components/PT/PTDialog.tsx`、`frontend/src/i18n/locales/{zh,en}.ts`、`frontend/src/demo/pt.ts`（接口契约与 README/CHANGELOG 同步）。

口径（取代第 5 节 D1 的折算路径，D1 本身记录保留）：

- `window30` → `windowUpload`：拿该 hash 在窗口内**最早一天**的上传高水位当基线，`UploadedEver − 基线` 即实测增量，**不折算、不外推**；`windowDays` 为这个数覆盖的天数（≤30），`efficiency` 按同一天数算。
- `days = int(now − 基线日 00:00)`，不足 1 天即 `windowDays=0`（首次运行当天、种子刚进台账、计数器回退）：界面显示「—」，`zeroUpload` 不计入 —— 增量恒为 0 却是「刚开始看」，写成 0 会被读成「没有上传」。
- `baseline30` 改为**逐 hash** 取各自最早一天（原实现取全库最早一天并要求该 hash 在其中）。这是端到端验证时发现的真实缺陷：h2 只在 2 天前出现、h1 在 5 天前出现时，h2 明明有基线却取不到，`windowDays` 恒为 0。
- `estimated` 字段从 `TorrentRef` 与 MCP `ptRow` 中移除（`ptRow.uploaded30dEstimated` → `uploadedWindowDays`）；`SampleCoverage.Days` 语义改为「最早采样日到今天的跨度」，即本机采了多少天；`LedgerTotals`/`Overview` 增 `windowDays` = **各行 windowDays 的最大值**。前端不再打 `≈`。
- 采样跨度与列窗口是两件事，所以留成两个字段：`coverage.days` 是采样能力（可能已满 30 天），`windowDays` 是这一列实际有多少天数据。取各行最大值而不是 coverage.days，是因为种子可能全是刚加的——那时采样已跑 30 天，这一列却一天数据都没有，标题不该写「近 30 天」；`windowDays=0` 时卡片/表头退回中性的「上传量」、值显示 `—`，概览告警与采样覆盖行文案也改成「上传量**最多**统计这 N 天」（该值下限为 0，不能断言等于 N 天）。

单元测试：`TestWindow30` → `TestWindowUpload`（8 例：无采样、仅当日采样、5 天增量、30 天封顶、窗口内零上传、计数器回退、基线无该 hash、无 hash），`TestBaseline30` 增「逐 hash 取最早一天」用例。`go test ./...` 全绿，`tsc --noEmit` 与 `vite build` / `build:demo` 通过。

真机端到端（`cmd/trmock -static -seed 20` + 真实 HTTP 路由 + 临时 `data_dir`，端口 8211）：

| 用例 | 实测结果 |
|:--|:--|
| 只有当天采样 | `totals.windowDays=0`、`zeroUpload=0`、各行 `windowDays=0/uploaded30d=0`；告警「采样不足 1 天（2026-10-31 起），上传量暂无数据」 |
| 造 5 天前基线（−1GB）、2 天前基线（−2GB）、5 天前基线高于累计（计数器回退） | A `windowDays=5 uploaded30d=1073741824`（efficiency 按 5 天算）、B `windowDays=2 uploaded30d=2147483648`、C `windowDays=0`（显示「—」） |
| 汇总 | `totals.windowDays=5 uploaded30d=3221225472`；`overview.coverage.days=5`、`uploaded30d=3221225472`；站点表分台量与台账一致；`/pt/calendar` 不受影响 |

前端展示核对：`build:demo` + 本地静态服务打开保种台，逐页签读 DOM 文本。概览卡片写「近 30 天上传 813 GB」、采样覆盖行写文案、台账汇总与列头同口径、行内不再出现 `≈`，全页无未解析的 i18n key（首轮曾发现新增文案层级放错——`uploadedHint`/`uploadedNoData` 落到 `pt.*` 而非 `pt.ledger.*`、`pt.card.uploadedWindow` 缺失——DOM 里直接显示原始 key，已修正并复核）。

同日复验（开发服务器 `dev/launch-dev.ps1 -Mock`，:5173 前端 + :8200 后端，air 热重载已带上新代码）：

- 起服务后立刻看台账：本机采样跨度 7 天，但 mock 每次启动都用 `rand.NewSource(time.Now())` 重新生成 infohash（`cmd/trmock/seed.go`），历史采样与今天的 28 颗种子零交集 → 每行 `windowDays=0`、`totals.windowDays=0`、`overview.windowDays=0`。界面因此显示中性的「上传量」+ 全列 `—`，而不是声称「近 7 天」——这正是上面「窗口取各行最大值」要解决的问题。
- 往 `dev/data/pt/samples.json` 注入 3 天前基线（每颗取当前累计的 70%；`dev/` 已被 gitignore）并重启后端后：`totals.windowDays=3`、`uploaded30d=34903542264`、`zeroUpload=12`，28 行全为 `windowDays=3`；`coverage.days` 仍为 7。DOM 复核：列头与汇总写「近 3 天上传」（「共 28 颗 · 290 GB · 近 3 天上传 32.8 GB · 零上传 12 颗 · 连续无上传 12 颗」），行内无 title（3 = 表头天数），全页无未解析 key。


