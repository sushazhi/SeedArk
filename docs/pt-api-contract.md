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
  "reclaimableBytes": 0,
  "sharedGroups": 171,
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
  "coverage": { "days": 30, "from": "2026-09-26", "to": "2026-10-26", "lastSample": 1761234567, "full": true },
  "windowDays": 30,
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
| `coverage` | 采样窗口覆盖情况：`days` = 本机最早采样日到今天的天数（0 = 不足一天），`full` 表示已覆盖满 30 天。**这是采样能力，不是上传量列的窗口** |
| `windowDays` | `uploaded30d` 这一列实际统计的天数 = 各行 `windowDays` 的最大值（≤30）；0 = 没有任何一行有可用窗口。界面照它写「近 N 天上传」，无窗口时退回「上传量」+ `—` |
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
      "windowDays": 30
    }
  ],
  "totals": {
    "count": 1286,
    "size": 9250000000000,
    "uploaded30d": 3600000000000,
    "avgEfficiency": 38.6,
    "zeroUpload": 23,
    "windowDays": 30
  },
  "sites": [{ "site": "M-Team", "count": 428, "size": 3120000000000 }]
}
```

- `releaseAt=0` 表示规则缺失/无法判断，前端显示「待定」。
- `hrDeadline=0` 表示该站未配置 H&R 规则。
- `uploaded30d` 是**窗口内的实测上传增量**：拿采样里该种子最早一天的上传高水位当基线，今天减去它。**不折算、不外推**——采样只有 5 天就只报这 5 天。`windowDays` 说明这个数统计了几天（≤30），界面照它写「近 N 天上传」，`efficiency` 也按同一天数算。
- `windowDays=0` 表示还没有一个完整自然日的采样（首次运行当天、种子刚进台账、或计数器回退）。此时上传量无意义（增量恒为 0，却是「刚开始看」而不是「没有上传」），界面显示 `—`，`zeroUpload` 也不把它计入。
- `totals.windowDays` = 各行 `windowDays` 的最大值，表头照它写「近 N 天」；某行比它短时该行挂 title 说明自己的天数（行与表头不一致时，以行内为准）。

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
      "reclaimableBytes": 0,
      "verdict": "shared",
      "sites": ["M-Team", "CHDBits"],
      "sameDownloader": true,
      "multiServer": false,
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
  "summary": { "groups": 186, "reclaimableBytes": 0, "sharedGroups": 171 }
}
```

只返回副本数 ≥ 2 的分组，按 `reclaimableBytes` 降序、再按 `totalBytes` 降序，最多 50 组。

**v3 改口径（重要）**：原先的 `duplicateBytes = totalBytes − max(copy.size)` 在 PT 场景里是**虚数**——同一份内容在多个站各挂一个种子（辅种）时磁盘上只有一份数据，删掉任何一条种子都不会释放空间，只会丢掉那个站的义务。现在按 **contentPath**（qB 的 `content_path`；TR 由 `downloadDir + name` 拼出）判定，给出 `verdict` 与**只在能证明重复时才非 0** 的 `reclaimableBytes`：

| verdict | 判定条件 | `reclaimableBytes` | 界面表述 |
|:--|:--|:--|:--|
| `shared` | 所有副本路径相同（跨下载器也成立） | 0 | 辅种 · 共用一份数据（删了不省盘） |
| `duplicate` | **同一台下载器内**出现 ≥2 条不同内容路径 | 该机内「组体积 − 最大单份」之和 | 疑似两份独立数据 |
| `unknown` | 跨下载器路径不一致，或路径缺失 | 0 | 路径不一致，无法判断 |

`sites` 是这一组压着义务的站点集合（风险视角：这份数据一旦丢，几站同时违约）；`multiServer` 表示副本跨多台下载器（跨机路径体系不同，不做共用/重复比较）。`overview` 的 `duplicateBytes` 同步改为 `reclaimableBytes` + `sharedGroups`。

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

## v3 增补：稀缺度 / 释放安全 / 淘汰建议 / 站点规则卡 / 快照 / 考核

依据：ptool 的 `dynamicseeding`（做种人数 < 10 视为「亟需保种」）、autoremove-torrents 的规则语言（freespace 触发 + 淘汰 action 枚举）、tracker-tracker（按站记录站点快照 + 指标口径说明）、NexusPHP `mybonus.php`（时魔公式）。**全部只输出建议，不自动删、不自动上传。**

### 1. TorrentRef 新增字段

| 字段 | 类型 | 说明 |
|:--|:--|:--|
| `seeders` / `leechers` | int | 主 tracker 汇报的做种/下载人数；0 = 未知（私有站可能不给 scrape） |
| `scarcity` | int 0-100 | 稀缺度：做种人数越少越高 |
| `swarm` | `scarce` / `normal` / `abundant` / `unknown` | 稀缺档，阈值取设置 `scarceSeeders`（默认 10） |
| `safeToRelease` | bool | **义务已满足 且 swarm 不稀缺**；`scarce`/`unknown` 一律 false |
| `releaseNote` | string | 人话解释：「义务已满足 · 做种 12 人（健康）」「做种仅 3 人，建议继续保」 |
| `ageDays` | float | 种子年龄（天）：优先 `dateCreated`，退化 `addedDate` |
| `ageSource` | `created` / `client` | `created` = 站点发布时间（准）；`client` = 本地添加时间（**会低估周龄**） |
| `bonusPerHour` | float | **删掉这颗每小时损失的时魔**（nexusphp 档 = 公式精确差值）；没在做种/没得算时为 0 并带 `bonusMethod` 说明 |
| `bonusPerGb` | float | 时魔损失 ÷ 体积(GB)，磁盘性价比；**只在同一站内可比**，淘汰排序用它 |
| `bonusMethod` | string | 口径说明（后端给人话，前端直出）；空串 = 没得算 |
| `bonusMethodKey` | `none` / `nexusphp` / `site` / `stale` | 口径机读键，界面据此出标签与配色 |

#### 时魔口径分档（为什么同一列在不同站是不同东西）

各站内核不同（NexusPHP / Unit3D / Gazelle / 二次开发魔改），时魔的来源与口径因此不同。
引擎由站点规则卡的 `bonusEngine` 决定，取值与语义：

| 引擎 | `bonusMethodKey` | 数值是什么 | 能不能与站方对账 |
|:--|:--|:--|:--|
| `manual` | `site` | 用户抄录的站点时魔（点/小时），单颗按本站做种体积均摊 | **能**，这是唯一对得上的口径 |
| `nexusphp`（缺省） | `nexusphp` | **删掉这颗损失的时魔**：`B(ΣA) − B(ΣA−a)` 精确差值；站点总时魔 B 也能本地复刻（ΣA 只对「我在做种」的种子求和，见 `pt-site-rules-research.md` 1.6） | **能**：B 与页面显示值同源；页面 `(A = xx.x)` 与本地 ΣA 比对更灵敏 |
| `nexusphp` 且常数不全 | `stale` | 只算得出「每种固定加成」那一项 | 不能，且周龄/体积/稀有度都没计入 |
| `none` | `none` | 不显示（该站没有时魔，或没录） | — |

口径硬规则：站方只给 `peers.seeder='yes'` 计时魔——**暂停/下载中的种子一律 0 + 说明**，不给虚数。
捐赠者「N 倍时魔」由 `bonusDonorTimes` 录入（默认 0 = 不乘），只作用于公式档；manual 抄录值已含倍数。

`GET /pt/snapshots` 的 `diff[]` 另带三条**独立通道**用于交叉验证：
`bonusPerHourMeasured`（快照差分实测，对魔改公式站也成立）、`bonusPerHourFormula`（本地复刻 B）、
`bonusSumA` vs `bonusASumObserved`（本地 ΣA 与页面 A 对账，最灵敏）。

参考实现见 [`pt-site-rules-research.md`](./pt-site-rules-research.md) 的 1.6 / 1.6.1（PT-depiler 的「引擎默认 + 逐站覆写」分层）。

### 2. 概览新增块

```json
"disk": { "path": "/downloads", "freeBytes": 412000000000, "totalBytes": 2000000000000,
          "usedPercent": 79.4, "lowSpace": true, "targetFreeGb": 50 },
"scarcity": { "scarceCount": 12, "scarceBytes": 812000000000, "unknownCount": 3 },
"bonusCap": { "perHour": 107, "configured": true, "bestSite": "M-Team",
              "engine": "nexusphp", "configCount": 3,
              "totalPerHour": 186.4,
              "sites": [ { "site": "M-Team", "engine": "nexusphp", "perHour": 92.6, "capPerHour": 107,
                           "localSumA": 567.7, "count": 19, "observedA": 758.9, "verified": true } ] }
```

`disk.lowSpace` = `freeBytes < targetFreeGb`（设置 `releaseTargetFreeGb`，默认 50，0 = 不判断）。

**`bonusCap` 的两套口径，不能混**（v3.1 起拆开）：

- `perHour`（旧字段，保留）= **各站时魔上限里最高的那一条**（`B0 + perseeding×maxseeding`），
  即「一颗不多正好打满」的数。它**不是**你实际每小时拿到的数——用户拿它和站点页
  「你当前每小时能获取 108.345」对不上就会以为面板算错了，所以它必须带 `bestSite`
  与 `engine` 一起显示，且界面**不得**拿它当头条。
- `totalPerHour`（v3.1 新增，**卡片头条用这个**）= 各配置站**实际每小时**的合计：
  nexusphp 站 = 本地复刻的 `B(ΣA, 颗数)`（`bonusTotalFrom`，颗数只计正在做种的），
  manual 站 = 用户抄录的站点时魔。未配口径的站不计入、也不补 0——「不知道」不能冒充「没有」。
- `sites[]` 每站明细：`perHour`（实际）、`capPerHour`（上限）、`localSumA`/`count`
  （本地复刻的 ΣA 与做种颗数）、`observedA`（用户从站点页抄的 `(A = xx.x)`）。
  `localSumA` 与 `observedA` 是**对账锚点**：两者一致 → 公式参数与做种集合都对；
  差得远 → 站点改过公式、或有种子在别的客户端/已停止（站点只给 `seeder='yes'` 的 peer 计时魔）。
- `configured=false` 表示**一个站都没配口径**（不是「时魔为 0」）；`configCount` 是配了口径的站数，
  界面用它写覆盖率（如 `3/8 个站已配口径`）。
- **`sites[]`（站点分布表）的每站时魔列**与 `bonusCap.sites` 共用同一个实现（`bonusActualOf`），
  两处数字不同就是 bug。`SiteStat` 新增 `bonusSet` / `bonusPerHour` / `bonusCapPerHour` /
  `bonusEngine`：`bonusSet=false`（未配口径）界面显示「—」，`bonusSet=true` 且 `bonusPerHour=0`
  显示 `0/h`——「不知道」不能冒充「没有」。注意集合量以**规则名**为键，站点分布的站点名
  可能被规则名覆盖（站点 m-team ↔ 规则名 mt），两边的键必须都走 `profileFor` 归一。

### 3. `GET /pt/eviction`

参数：`freeGb`（目标保留空间，缺省用设置值）、`limit`（默认 50）。

```json
{
  "trigger": { "freeBytes": 412000000000, "targetFreeGb": 50, "needBytes": 88000000000, "low": true },
  "candidates": [
    { "hash": "...", "name": "...", "site": "M-Team", "size": 62000000000,
      "seeders": 42, "scarcity": 8, "bonusPerGb": 0.12, "bonusEff": 0, "efficiency": 3.2,
      "safeToRelease": true, "reason": "义务已满足 · 做种 42 人（充足） · 本站里时魔最不划算",
      "cumulativeBytes": 62000000000 }
  ],
  "totalBytes": 88000000000,
  "note": "按 站内相对时魔 ↓ → 稀缺度 ↑ → 体积 ↓ 排序（跨站不比绝对值）；仅建议，本面板不会删除任何种子"
}
```

候选集只含 `safeToRelease=true` 的种子；`cumulativeBytes` 为累加体积，前端可直接显示「删到第 N 行可腾出 X」。

`bonusEff` 是该颗在**本站候选集**里的相对位置（0 = 本站最该先放，1 = 本站最后才放），排序按它而不是
`bonusPerGb` 的绝对值：各站的「点」不可通约，混排等于拿馒头的积分去和别站的猫粮比大小。
`bonusPerGb` 仍原样带出供人看。

`bonusEff` **缺省（字段省略）表示这个站内排不出名次**，界面不得据此下结论：抄录档（`manual`）按做种体积均摊后
同站每颗的时魔·GB 完全相同，只有一颗候选的站同理——这些情况排序退回「稀缺度 → 体积」，
但不会对用户说「本站里最不划算」。`reason` 文案与它保持一致，不会出现没有依据的断言。

### 4. 站点规则卡扩展（`POST /pt/sites`）

| 字段 | 说明 |
|:--|:--|
| `hrAmnestyRatio` | H&R 豁免分享率（NexusPHP `ignore_when_ratio_reach`，分母是**种子体积**） |
| `hrInspectHours` | H&R 观察窗（`inspect_time`） |
| `hrBanCount` | 累计未达标几次封号 |
| `deleteUnpackedDays` / `deletePackedDays` / `deleteNoTransferDays` | 删号线 |
| `bonusT0Weeks` `bonusN0` `bonusB0` `bonusL` `bonusPerSeeding` `bonusMaxSeeding` | 时魔公式参数（`bonusEngine=nexusphp` 时用） |
| `bonusExtraPct` | 站点页各项加成百分比的**加法合计**（等级/二级验证/持续在线等；M-Team 实测 府尹6%+2FA10% → 填 16）。面板把基本值 ×(1+16%) 才等于站点显示的合计——写成乘法 (1.06×1.10) 会对不上账 |
| `ruleCheckedAt` | 规则核对时间（unix 秒）；概览卡片据此标「已核对/参数未核对」 |
| `bonusEngine` | `none` / `nexusphp` / `manual`；缺省（空串）按 `nexusphp`（v3 老配置语义不变） |
| `bonusManualPerHour` | 抄录的站点时魔（点/小时）；`manual` 档必填 |
| `bonusManualAt` | 上面那个数的抄录时刻（unix 秒），界面据此标「N 天前抄的」（保存时由面板盖章） |
| `bonusDonorTimes` | 捐赠者「N 倍时魔」；缺省 0 = 不乘。只作用于公式档（manual 抄录值已含倍数） |
| `bonusASumObserved` | 从 mybonus.php 页面抄的 `(A = xx.x)`；站点数据页据此与本地复刻的 ΣA 对账 |
| `ruleSource` / `ruleCheckedAt` | 规则来源 URL 与最后核对时间（界面标「已核对 / 待核对 / 超 90 天」） |
| `levels` | 等级阶梯 `[{name, weeks, downloadedGb, ratio, bonus, seedBonus, uploads, kept, alternatives[]}]`，`alternatives` 用于「任一满足即可」的双路径（PTer 式） |

H&R 判定升级为四参数：达标 = `做种小时 ≥ minSeedDays×24` **或** `上传量/体积 ≥ hrAmnestyRatio`；未达标且距完成已超 `hrInspectHours` → 记一次未达标。

### 5. 站点快照（B10）

- `GET /pt/snapshots` → `{ "items": [ { "site","at","uploaded","downloaded","ratio","bonus","seedBonus","levelName","seedingCount","seedingSize","hnrUnsatisfied","hnrPreWarning","lastAccessAt","note" } ], "diff": [ { "site","daysSince","localVsSiteRatio" } ] }`
- `POST /pt/snapshots`（`site` 必填，其余选填）→ 落盘后的条目；`DELETE /pt/snapshots/:site` 清空该站历史。
- 保留最近 180 天，每站最多 400 条。

### 6. 考核（B11）

- `GET /pt/exams` → `{ "items": [ { "site","name","deadline","minRatio","minUploadGb","minSeedingSizeGb","minSeedDays",
  "progress": { "localUploadGb","localSeedingSizeGb","localRatio","daysLeft","met":[],"missing":[] },
  "lowerBound": true, "note": "本地累计是站点统计的下界（促销会放大、站点只计有效 peer）" } ] }`
- `POST /pt/exams` / `DELETE /pt/exams/:site`；**不内置任何站点考核阈值**。

### 7. 档案新增字段

`VaultEntry` 增 `reason`（`manual` / `policy` / `api`）与 `reasonNote`；做种策略自动删种也会先留档。

## 前端约定

- 入口：TopBar 右侧新增「保种台」按钮（`Sprout` 图标），移动端同样可见；点击打开大尺寸 Dialog。
- 页签：概览 / 统计 / 释放日历 / 资产台账 / 档案库 / 多站辅种 / 发种 / 腾空间 / 站点数据 / 站点规则。
- 所有估算必须显式标注「预计/估算」，不得出现「自动删除」措辞。
- 复用现有 `glass-panel-strong`、`rounded-panel`、`text-caption1/footnote/body` 等 token；最小字号 11px。
- 站点规则保存后，台账/日历/概览自动刷新。
- 档案库复活、删除需二次确认；发种只生成资料包，不自动上传。
