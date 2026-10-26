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
