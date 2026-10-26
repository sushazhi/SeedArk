# 保种台 API 契约（v1）

前后端并行实现的唯一接口依据。字段名一律 camelCase；字节为 `int64`，时间为 **Unix 秒**；效率单位为 `MB/GB·日`。

## 通用

- 基址：沿用 `frontend/src/api/client.ts` 的 `client`（`APP_BASE + '/api'`），所有响应走 `request<T>()` 解包。
- 错误：非 0 `code` 由现有拦截器统一提示；空数据返回空数组，不返回 `null`。
- 站点：`site` 为 Tracker 主机名或站点展示名；未识别时为空串。
- 下载器：`kind` 为 `transmission` / `qbittorrent`；`serverName` 为空表示单机直连。

## 枚举

```ts
type PTStatus = 'seeding' | 'downloading' | 'paused' | 'error' | 'other'
type ReleaseReason = 'min_days' | 'hr' | 'ratio' | 'unknown'
```

## GET /pt/overview

```json
{
  "torrentCount": 1286,
  "seedCount": 1102,
  "totalSize": 9250000000000,
  "uploaded30d": 3600000000000,
  "avgEfficiency": 38.6,
  "hrRiskCount": 3,
  "readyCount": 38,
  "readyBytes": 2020000000000,
  "vaultCount": 342,
  "vaultBytes": 1288490188,
  "groupCount": 186,
  "duplicateBytes": 2040000000000,
  "sites": [
    {
      "site": "M-Team",
      "count": 428,
      "size": 3120000000000,
      "uploaded30d": 1560000000000,
      "ratio": 4.82,
      "budgetBytes": 2199023255552,
      "overBudget": true
    }
  ],
  "upcoming": [
    { "date": "2026-10-24", "count": 8, "bytes": 350000000000 }
  ],
  "generatedAt": 1761234567,
  "coverage": { "days": 31, "from": "2026-09-26", "to": "2026-10-26", "lastSample": 1761234567, "full": true },
  "idleCount": 42,
  "idleBytes": 880000000000,
  "invalidCount": 5,
  "invalidBytes": 62000000000,
  "contractCount": 12,
  "contractBreach": 1,
  "contractDueCount": 3,
  "contractDueBytes": 140000000000,
  "overBudgetCount": 1,
  "overBudgetBytes": 900000000000,
  "uploadedToday": 48200000000,
  "todayQuality": "partial",
  "todayNote": "相对 2026-10-25：部分种子当日新加或未被采到，合计偏小",
  "warnings": ["1 个站点超出保种预算，合计超出 838 GB"]
}
```

`upcoming` 为未来 14 天内有释放计划的日期，按日期升序。

**v2 新增字段口径**（均为只读提示，不触发任何动作）：

| 字段 | 含义 |
|:--|:--|
| `coverage` | 采样窗口覆盖情况。`days < 29` 时 `uploaded30d` 必然是折算估算值，`full=false`；界面据此说明 `≈` 的可信度 |
| `uploadedToday` / `todayQuality` / `todayNote` | 今日上传增量，严格对比**前一自然日**的逐颗读数。没有可比基线时**不返回 0 当作「今天没上传」**，而是给 0 + 非 `exact` 的 quality + 说明文案。quality 取值见下 |
| `idleCount` / `idleBytes` | 连续 ≥7 天没有上传增长（且**确有采样数据**）的种子。无采样数据的种子不计入，避免把新种子误报成僵尸 |
| `invalidCount` / `invalidBytes` | 站点已删种（Tracker 宣告「未注册」）。判定要求该站点下还有别的种子宣告成功，否则视为站点不可达而不判 |
| `contractCount` / `contractBreach` | 已设 / 已过期的保种契约数 |
| `contractDueCount` / `contractDueBytes` | 未来 7 天内到期的契约 |
| `overBudgetCount` / `overBudgetBytes` | 站点保种预算超出的站点数与超出体积 |
| `warnings` | 上述告警的现成文案，空数组而非 `null` |

### todayQuality 枚举

| 取值 | 含义 |
|:--|:--|
| `exact` | 与前一自然日逐颗对比得到，可信 |
| `partial` | 部分种子当日新加或未被采到（缺可比基线），合计偏小 |
| `unknown` | 没有前一自然日的采样，无法计算增量 |
| `rollback` | 读数回退（计数器重置 / 更换下载器），按「不补算」处理 |

> 口径来源与 MoviePilot 的 `SiteStatistic` 一致（增量只与严格前一自然日比、没有基线不计算、增量不为负、读数回退不推断）。**一处刻意不同**：`SiteStatistic` 要对 PB 级读数标 `≈`，是因为它读的是站点网页上三位小数的显示值（最小步长约 1.02T）；SeedArk 读的是下载器上报的字节级累计计数器，不存在该舍入问题，所以这里只需标注「有没有可比基线」。

## GET /pt/ledger

```json
{
  "rows": [
    {
      "id": 123,
      "name": "Dune.Part.Two.2024.2160p.UHD.BluRay.x265",
      "hash": "8f3a...",
      "site": "M-Team",
      "serverName": "NAS",
      "kind": "qbittorrent",
      "size": 62400000000,
      "seedDays": 128,
      "ratio": 4.82,
      "uploaded30d": 195000000000,
      "efficiency": 82.4,
      "score": 96,
      "lastUpload": 1761230000,
      "status": "seeding",
      "labels": ["已整理"],
      "hrRisk": false,
      "hrDeadline": 0,
      "releaseAt": 0,
      "releaseReason": "min_days",
      "estimated": false
    }
  ],
  "totals": {
    "count": 1286,
    "size": 9250000000000,
    "uploaded30d": 3600000000000,
    "avgEfficiency": 38.6,
    "zeroUpload": 23
  },
  "sites": [{ "site": "M-Team", "count": 428, "size": 3120000000000 }]
}
```

- `releaseAt=0` 表示规则缺失/无法判断，前端显示「待定」。
- `hrDeadline=0` 表示该站未配置 H&R 规则。
- `estimated=true` 表示 `uploaded30d` 不是完整 30 天窗口的真实增量，而是折算值，前端要标注「估算」（`≈`）。判定口径：采样只覆盖 `n` 天（`n < 29`）时按 `增量 ÷ n × 30` 折算；完全没有可用采样时按 `累计上传 ÷ 做种天数 × 30` 折算，两者都以累计上传为上限。**首次运行当天基线就是本次采样、增量恒为 0，必须走折算路径并标 `estimated=true`**，否则会把刚上传过的种子误报成零上传。

## GET /pt/calendar?month=YYYY-MM

`month` 缺省为当前月。

```json
{
  "month": "2026-10",
  "days": [
    {
      "date": "2026-10-22",
      "count": 4,
      "bytes": 137000000000,
      "ready": true,
      "hrRisk": 0,
      "torrents": [
        {
          "id": 123,
          "name": "Dune.Part.Two.2024.2160p.UHD.BluRay.x265",
          "site": "M-Team",
          "size": 62400000000,
          "seedDays": 128,
          "hrRisk": false,
          "releaseAt": 1761234567,
          "reason": "min_days"
        }
      ]
    }
  ],
  "unknown": { "count": 120, "bytes": 1800000000000 },
  "summary": { "todayBytes": 137000000000, "weekBytes": 1900000000000, "monthBytes": 3000000000000, "hrCount": 3 }
}
```

只返回有内容的日期；月历空格由前端补齐。`ready=true` 表示该日期 ≤ 今天。

## 档案库

### GET /pt/vault?q=

```json
{
  "entries": [
    {
      "id": "8f3a...",
      "hash": "8f3a...",
      "name": "Dune.Part.Two.2024.2160p.UHD.BluRay.x265",
      "site": "M-Team",
      "size": 62400000000,
      "deletedAt": 1761234567,
      "seedDays": 128,
      "ratio": 4.82,
      "uploadedEver": 195000000000,
      "labels": ["已整理"],
      "serverName": "NAS",
      "kind": "qbittorrent",
      "savePath": "/vol1/影视/电影/Dune",
      "hasTorrent": false,
      "canRevive": true
    }
  ],
  "total": 342,
  "bytes": 1288490188
}
```

`q` 匹配名称/hash/站点，大小写不敏感。

### GET /pt/vault/:id

```json
{
  "entry": { "...": "同上" },
  "files": [{ "name": "Dune...mkv", "size": 61800000000 }]
}
```

### POST /pt/vault/:id/revive

重新添加种子（优先原始 `.torrent`，否则用归档的磁力）。

```json
{ "id": 456, "via": "magnet" }
```

`via`：`torrent` / `magnet`。失败返回非 0 code。

### DELETE /pt/vault/:id

```json
{ "deleted": true }
```

### GET /pt/vault/:id/file

二进制 `.torrent`（`application/x-bittorrent`）。没有原始文件时 404。

## GET /pt/matrix

```json
{
  "groups": [
    {
      "key": "dune part two 2024 2160p uhd bluray x265",
      "name": "Dune.Part.Two.2024.2160p.UHD.BluRay.x265",
      "size": 62400000000,
      "totalBytes": 249600000000,
      "duplicateBytes": 186400000000,
      "sameDownloader": true,
      "copies": [
        {
          "id": 123,
          "site": "M-Team",
          "serverName": "NAS",
          "kind": "qbittorrent",
          "status": "seeding",
          "size": 62400000000,
          "uploaded": 195000000000,
          "labels": ["已整理"]
        }
      ]
    }
  ],
  "summary": { "groups": 186, "duplicateBytes": 2040000000000 }
}
```

只返回副本数 ≥ 2 的分组，按 `duplicateBytes` 降序，最多 50 组。`duplicateBytes = totalBytes - max(copy.size)`（保守估计）。

## 发种

### POST /pt/publish/preview

```json
{
  "title": "沙丘2",
  "name": "Dune.Part.Two.2024.2160p.UHD.BluRay.x265",
  "site": "M-Team",
  "category": "电影",
  "sizeBytes": 62400000000,
  "mediaInfo": "General\nComplete name : ...",
  "screenshots": ["https://example.com/01.jpg"],
  "subtitle": "中英特效",
  "video": "HEVC 10bit",
  "audio": "DTS-HD MA 5.1",
  "resolution": "3840x2160",
  "year": 2024,
  "country": "美国",
  "genre": "剧情 / 科幻",
  "description": "",
  "lang": "zh"
}
```

```json
{
  "bbcode": "[img]...[/img]\n[quote]...[/quote]",
  "warnings": ["未填写截图链接，发布前请补齐"]
}
```

- `warnings`：缺失项提示数组；**无告警时返回 `[]`，不是 `null`**（前端直接做 `.length` / `.map`）。
- `lang`：模板语言，预留字段；当前只实现 `zh`，省略或传其它值都按中文生成。

### POST /pt/publish/kit

`multipart/form-data`：

- `payload`：上面的 JSON 字符串
- `torrent`：可选 `.torrent` 文件

返回 `application/zip`，内含 `bbcode.txt`、`mediainfo.txt`、`checklist.txt`，有种子时附 `<name>.torrent`。

## 站点规则

```json
{
  "id": "mt",
  "name": "M-Team",
  "match": "m-team",
  "minSeedDays": 30,
  "minRatio": 1.0,
  "hrSeedHours": 0,
  "enabled": true
}
```

- `match`：大小写不敏感的子串，匹配 Tracker 主机名/站点名。
- 规则用于：`releaseAt = max(doneDate + minSeedDays 天, doneDate + hrSeedHours 小时)`；`hrSeedHours>0 && 做种秒数 < hrSeedHours*3600` 判为 H&R 风险。
- `minSeedDays=0 && hrSeedHours=0` → `releaseAt=0`，前端显示「待定」。

### GET /pt/sites

```json
{
  "profiles": [{ "...": "同上" }],
  "detected": [{ "site": "M-Team", "count": 428 }]
}
```

`detected` 来自当前种子快照，用于「从当前站点生成规则」。

### POST /pt/sites

请求体为完整 profile；`id` 为空则新建，否则更新。返回保存后的 profile。

### DELETE /pt/sites/:id

```json
{ "deleted": true }
```

### POST /pt/sites/seed

为 `detected` 中尚无规则的站点批量创建 `minSeedDays=0, minRatio=0, hrSeedHours=0, enabled=true` 的占位规则。

```json
{ "created": 5 }
```

## POST /pt/sample

手动触发一次快照采样（后台每 60s 自动采样）。

```json
{ "sampled": true, "count": 1286 }
```

## 保种契约

只记录「承诺保种到什么时候」，不触发暂停 / 删除，也不上报站点。按 infohash（小写）索引，落在 `DataDir/pt/contracts.json`（权限 0600）。

### POST /pt/contract

入参二选一：`days`（从今天起 N 天）或 `until`（unix 秒）；`days` 优先。

```json
{ "hash": "c003a87a6c6114a084698c585af87528f6d8957f", "days": 30, "note": "站庆活动要求" }
```

响应为落盘后的契约：

```json
{
  "hash": "c003a87a6c6114a084698c585af87528f6d8957f",
  "until": 1795601664,
  "note": "站庆活动要求",
  "createdAt": 1793009664
}
```

`hash` 为空、或 `days` 与 `until` 都无效时返回 400。

### DELETE /pt/contract/:hash

解除契约。返回 `{ "deleted": true }`；契约不存在时返回 404。

台账行里的契约字段：`hasContract`、`contractUntil`、`contractDaysLeft`（可为负，表示已过期天数）、`contractBreach`。契约到期日也会标在释放日历上（`CalendarDay.contractDue`、`CalendarSummary.contractDue` / `contractBreach`），它与「可释放」是两件事，因此是独立的一层。

## 前端约定

- 入口：TopBar 右侧新增「保种台」按钮（`Sprout` 图标），移动端同样可见；点击打开大尺寸 Dialog。
- 页签：概览 / 释放日历 / 资产台账 / 档案库 / 同内容 / 发种 / 站点规则。
- 所有估算必须显式标注「预计/估算」，不得出现「自动删除」措辞。
- 复用现有 `glass-panel-strong`、`rounded-panel`、`text-caption1/footnote/body` 等 token；最小字号 11px。
- 站点规则保存后，台账/日历/概览自动刷新。
- 档案库复活、删除需二次确认；发种只生成资料包，不自动上传。
