# PT 站点规则与做种管理工具调研（v3 增补依据）

> 用途：为 SeedArk「保种台」v3 增补（稀缺度 / 释放安全 / 淘汰建议 / 站点规则卡 / 快照 / 考核）提供口径依据。
> 与 [`moviepilot-pt-capabilities-research.md`](./moviepilot-pt-capabilities-research.md) 并列：那份讲 MoviePilot 的能力面，这份讲**站点侧规则本身**与**做种类工具的实现细节**。
> 对应的实现口径见 [`pt-api-contract.md`](./pt-api-contract.md) 的「v3 增补」一节。

标注约定：`【事实】` = 有源码/配置/原文可复核；`【观点】` = 主观诉求；`【未验证】` = 未取到出处，**不要当结论引用**。

## 0. 本轮取证条件

- 可用：GitHub raw、GitHub 站点页面、各项目文档站；`web_search` 默认 provider 不可用，改用 `bing / anysearch / exa / tavily` 等具体引擎。
- 不可用：`api.github.com`（403/challenge，无法做仓库搜索）、`v2ex.com` 直连、`savept.icu`（超时）。
- 因此**贴吧 / NGA / Telegram 的原始用户言论未取证**，社区侧证据一律用 GitHub Issue、站点规则原文转贴、开源工具行为替代。

## 1. 站点侧指标体系

### 1.1 底层：一套 NexusPHP 默认值 + 各站改参
默认参数集中在 [`config/allconfig.php`](https://raw.githubusercontent.com/xiaomlove/nexusphp/php8/config/allconfig.php)。

### 1.2 等级阶梯【事实】

| 等级 | 注册周数 | 下载量 | 升级分享率 | 降级分享率 |
|---|---|---|---|---|
| Power User | 4 | 50GB | 1.05 | 0.95 |
| Elite | 8 | 120GB | 1.55 | 1.45 |
| Crazy | 15 | 300GB | 2.05 | 1.95 |
| Insane | 25 | 500GB | 2.55 | 2.45 |
| Veteran | 40 | 750GB | 3.05 | 2.95 |
| Extreme | 60 | 1024GB | 3.55 | 3.45 |
| Ultimate | 80 | 1536GB | 4.05 | 3.95 |
| Nexus Master | 100 | 3072GB | 4.55 | 4.45 |

- **升级线与降级线是两套，中间 0.1 死区**——这是「离降级还差多少」可以算的前提。
- 做种积分默认门槛（Power 40000 … NM 1000000）：【未验证】来自 PT-depiler 注释，allconfig 原文未找到对应键。
- 各站实测差异（硬数据）：[M-Team](https://raw.githubusercontent.com/pt-plugins/PT-depiler/master/src/packages/site/definitions/mteam.ts)（4周/200GB/2 …）、[CHDBits](https://raw.githubusercontent.com/pt-plugins/PT-depiler/master/src/packages/site/definitions/chdbits.ts)（分享率与魔力门槛明显更高）、[HDSky](https://raw.githubusercontent.com/pt-plugins/PT-depiler/master/src/packages/site/definitions/hdsky.ts)（2025-03-01 换过规则，**老用户仍按旧规则**）、[PTer](https://raw.githubusercontent.com/pt-plugins/PT-Plugin-Plus/dev/resource/sites/pterclub.com/config.json)（分享率 **或** 做种积分+发布数，双路径）、[U2](https://raw.githubusercontent.com/pt-plugins/PT-Plugin-Plus/dev/resource/sites/u2.dmhy.org/config.json)。

→ 结论：**等级规则必须由用户录入**，代码里内置任何一份都会过期。契约里 `SiteLevel.alternatives` 就是为 PTer 这种双路径留的。

### 1.3 删号线【事实】
`neverdelete=6`（Insane 以上永不删）、`neverdeletepacked=3`（Elite 以上封存不删）、`deletepacked=400`、`deleteunpacked=150`、`deletenotransfer=60`、`deletepeasant=30`。
站点原文佐证（[#1903](https://github.com/pt-plugins/PT-Plugin-Plus/issues/1903)）里有一条被改成「连续 100 天未登录且无流量删」——**说明必须按站录入**。

### 1.4 阶梯降级 Peasant【事实】
>50GB&<0.4 / >100GB&<0.5 / >200GB&<0.6 / >400GB&<0.7 / >800GB&<0.8 → 降级；降级后 30 天不改善则封禁。站点原文（#1903）结构与内核一致、阈值不同。

### 1.5 H&R 四参数【事实】
来源：[HitAndRunRepository.php](https://raw.githubusercontent.com/xiaomlove/nexusphp/php8/app/Repositories/HitAndRunRepository.php)、[HitAndRun.php](https://raw.githubusercontent.com/xiaomlove/nexusphp/php8/app/Models/HitAndRun.php)

- `seed_time_minimum`（小时）：做种满即达标；
- `ignore_when_ratio_reach`：分享率达标豁免，判定式 **上传量 ÷ 种子体积**（分母不是下载量！）；
- `inspect_time`（小时）：观察窗，超窗未达标记一次未达标；
- `ban_user_when_counts_reach`：累计 N 次封号；
- 状态机 `Inspecting / Reached / Unreached / Pardoned`（**可赦免**）；VIP 与捐赠者豁免。
- 站点实例（[#1816](https://github.com/pt-plugins/PT-Plugin-Plus/issues/1816)）：168h 做种 / 720h 观察窗 / 分享率 1.0 / 10 次封号。
- 非 NPHP 对照（[#1153](https://github.com/pt-plugins/PT-Plugin-Plus/issues/1153)）：U-232 站「最多 42 个未保种满 14 天的种子」——本质同一种约束。

→ SeedArk 实现：`obligationMet()` 用「最短天数与 H&R 小时取更晚」+ 豁免分享率（分母种子体积）。

### 1.6 时魔公式【事实】
来源：[mybonus.php](https://raw.githubusercontent.com/zjutjh/NexusPHP/master/mybonus.php)

```
A_t = (1 - 10^(-周龄/T0)) × 体积GB × (1 + √2 × 10^(-(做种人数-1)/(N0-1)))
B   = B0×(2/π)×atan(ΣA/L) + perseeding × min(做种数, maxseeding)
内核默认  T0=4周 N0=7 B0=100 L=300 perseeding=1   maxseeding=7  → 上限 107/h
M-Team 实测 T0=4周 N0=7 B0=50  L=300 perseeding=0.1 maxseeding=50 → 上限 55×1.16 = 63.8/h
```

三个乘子 = **周龄 × 体积 × 稀有度**。`maxseeding` 意味着**第 N+1 种起不再有每种加成**，
但它的 A 仍然进 ΣA（所以「多挂一颗完全不值钱」只对第二项成立）。

| 参数 | 含义（不是字母，是作用） | 站点页上的样子 | 常见取值 |
|:--|:--|:--|:--|
| `T0` | **周龄刻度**：做种满 T0 周乘子≈0.9，越老越接近 1，刚发布≈0 | `T0 = 4` | 4 周 |
| `N0` | **稀有度刻度**：1 个做种≈2.41，N0 人≈1.41，人越多越趋近 1 | `N0 = 7` | 7 |
| `B0` | **atan 项的饱和值**（时魔上限的主体）：A 趋无穷才逼近它，永远拿不满 | `B0 = 50` | 50 / 100 |
| `L` | **收敛刻度**：A≈L 时 atan 项≈B0/2；L 越小越早饱和 | 常显示成没渲染的模板变量 | 300 |
| `perseeding` | **每种加成**：只乘 `min(做种数, maxseeding)`，与 atan 项**相加**才是页面「基本獎勵」 | 「0.1 個魔力值 * 你的做種數」里的 0.1 | 0.1 / 1 |
| `maxseeding` | **计满种数**：超出者不给每种加成，但 A 仍进 ΣA | 「做種數最多計 50 個」 | 50 / 7 |

**站点页那段公式排版是坏的，照抄必错**（实测 M-Team）：

- 上下标丢失：`10(-T_i/T_0)` 实为 `10^(-T_i/T_0)`，`10(-(N_i-1)/(N_0-1))` 同理；
- 分式丢失：`B0 × 2π arctan(A L)` 实为 `B0 × (2/π) × atan(A/L)`；
- `L = {{l_bonus}}` 是**模板变量没渲染**（不是值）——L 得反解：由页面 `(A = xx.x)` 与
  「基本獎勵」倒推 `L = A / tan((基本 − perseeding×min(做种数,maxseeding)) / (B0×2/π))`，
  M-Team 反解结果 300。

**为什么本地复刻会差 0.2%（47.66 vs 47.767）——误差是可分解的，不是玄学**：

1. **基本獎勵含第二项**：M-Team 的 41.178 = atan 项 36.178 + 0.1×50＝5。拿面板的 atan 项
   去比 41.178，差正好是 5 点（最容易误判成「公式错」的一处）；
2. **加成是加法**：府尹 6% + 二级验证 10% → ×1.16 = 47.767；写成 1.06×1.10 = 48.01，多 0.5%；
3. **显示四舍五入**：41.178+2.471+4.117 = 47.766，页面写 47.767 —— 千分位差，不是模型差；
4. **ΣA 级别的差被 atan 压扁**：本地 ΣA 比页面 A 低约 0.7%（做种人数取本地 scrape、
   周龄取 `dateCreated`、L 是反解值），经 atan 后 B 只差 0.2%。所以**对账要看 A，别看 B**。

**ΣA 是对「我自己在做种的种子」求和，不是全站**（关键事实，取自源码）：

```php
select ... from torrents LEFT JOIN peers ON peers.torrent = torrents.id
 WHERE peers.userid = $CURUSER[id] AND peers.seeder='yes' GROUP BY torrents.id
$A += (1 - exp($valueone*$weeks_alive)) * $gb_size * (1 + $sqrtof2*exp($valuethree*($torrent[seeders]-1)));
$all_bonus = $valuetwo * atan($A / $l_bonus) + ($perseeding_bonus * $count);
print("... getting ".round($all_bonus,3)." 点/小时 (A = ".round($A,1).")");
```

→ 推论：**B 可以在本地精确复刻**，四个输入全在本地——
体积（`TotalSize`）、发布时间（`dateCreated`）、做种人数（tracker scrape）、我的做种集合（下载器）。
不需要登录站点、不需要任何 API。早期文档写「拿不到全站做种集合所以算不出 B」是**错的**，已核正。

→ 与站方可能有出入的三处（必须标注，不许假装没有）：
1. `seeders`：站方读 `torrents.seeders`（全站快照），我们只有自己 tracker 的 scrape，可能滞后；
   缺失时按 1 人算 → B 被高估（`atan` 会压缩一部分误差）；
2. `added`：站方用种子发布时间，我们必须用 `dateCreated`；退化到 `addedDate` 会把周龄估小；
3. **站点魔改过公式**时标准公式不成立。页面会直接打印 `A = xx.x`，拿它和本地 ΣA 对账即可发现——
   比用 B 对账更灵敏，因为 B 经过 `atan` 饱和，误差被压缩。

→ 另外 `donortimes_bonus`（捐赠者「N 倍时魔」）是独立乘子，页面会打印
「Donors always get N times of bonus」；用户是否为捐赠者要单独录一个开关，否则 B 会差一整个倍数。

→ SeedArk 实现取舍：
- 单颗 A 值（`aValueOf`）用于凑 ΣA；「删掉这颗损失多少时魔」= `bonusLossOf` 的精确差值 `B(ΣA) − B(ΣA−a)`，
  这是单颗唯一站得住的读法（A×perseeding 的线性近似在 atan 饱和区会显著高估，已废弃）；
- **站点总时魔 B 走 `bonusTotalFrom` 本地复刻**，并用页面 `A = xx.x` 对账；
- 快照差分（Δ魔力/Δ小时）是第三条独立通道：与公式无关，对魔改站也成立；
- 做种人数缺失时按 1 人算得**上限**（宁可高估：高估的代价是「建议继续保」，低估的代价是「建议删掉稀缺种子」）；
- 未填公式参数则返回 0 并显示「—」，**不拿内核默认值假装准确**；
- 周龄优先用 `dateCreated`（站点发布时间），退化到 `addedDate` 并标 `ageSource`——老种新加时后者会低估；
- 口径硬规则：站方只给 `peers.seeder='yes'` 计时魔，所以**暂停/下载中的种子一律 0 + 说明**。

### 1.6.1 时魔「每站都不一样」怎么显示：PT-depiler 的分层法【事实 + 取舍】

上一节只解决了「NPHP 站按公式估单颗」。但站点内核不止一种，时魔的来源、口径、能否取到都不一样。
[PT-depiler](https://github.com/pt-plugins/PT-depiler)（PT-Plugin-Plus 的继任者，站点定义最全的开源实现）
用三层结构解决这个问题，是目前能找到的最直接参考。

**(a) 它把 `bonusPerHour` 当成一个「可覆写的字段」，而不是一条内置公式【事实】**
（`userInfo.selectors.bonusPerHour`，字段定义见 `src/packages/site/types/site.ts`）：

| 引擎 | 数值来源 | 源码坐标 |
|:--|:--|:--|
| NexusPHP | `mybonus.php` 页面：`#outer td[rowspan]`、`div:contains('你当前每小时能获取'):last` | `schemas/NexusPHP.ts` 的 `userInfo.process` 第 3 步（`/index.php` → `/userdetails.php` → `/mybonus.php`） |
| Unit3D | 单独一跳 `/users/{name}/earnings`，选择器 `.panelV2 dl.key-value dd:nth(2)`；并抽成可覆写方法 `getUserBonusPerHour(name)` | `schemas/Unit3D.ts:720-730` |
| M-Team | 官方 API `/api/tracker/mybonus` → `data.formulaParams.finalBs` | `definitions/mteam.ts` 的 `userInfo.process` |
| Gazelle / GazelleJSONAPI | **默认没有这个字段**（`userInfo.process` 里根本不列 `bonusPerHour`）→ 界面留空 | `schemas/Gazelle.ts` |

→ 两条可直接照抄的纪律：
1. **取不到就不显示，不用估算填坑**。Unit3D 文档明确写：9.0.0+ 的 `/api/user` 返回的 10 个字段里
   **没有时魔**，「前端对应列会留空，这是接口限制不是解析错误」（`.dsh/skills/ptd-site-definition/references/engines/unit3d.md`）。
2. **它显示的时魔永远是站点侧的值**（站方算出的「本人当前每小时能获取」），从不拿本地数据反推。

**(b) 逐站修正，而不是全局默认【事实】**
- `donorConfig.bonusPerHourMultiplier` 默认 2（NPHP 黄星时魔翻倍）；**若 selector 抓到的已经是加倍后的数，必须改成 1**
  （`definitions/baozi.ts`、`definitions/52movie.ts`，写成「常见坑」第 9 条）。
- 站点改版换路径：`definitions/byrbt.ts:119-125` 把默认的 `/mybonus.php` 那一步**过滤掉换成 `/bonus.php`**。
- 用户侧还有第三层：`ISiteUserConfig.merge` 可覆盖 metadata 任意字段
  （`metadata-fields.md` 的「与用户配置的边界」）。

**(c) 分工约定【事实】**
`metadata-fields.md` 写明：schema 用 `process[*].fields` 声明「这一步取哪些字段」，选择器统一放 `userInfo.selectors`，
于是**站点改版时 definition 只需覆写 selector，请求流程不动**。

**→ SeedArk 的取舍【观点】**：抄它的**分层与口径**，不抄它的**取数方式**。

| PT-depiler | SeedArk | 落点 |
|:--|:--|:--|
| schema 引擎默认 | `BonusEngine` 分派 | `bonusOf()` 按引擎分支 |
| definition 覆写（选择器 / 倍数 / 换路径） | 规则卡的公式常数、手工时魔 | `SiteProfile.BonusT0Weeks…` + `BonusManualPerHour` |
| 用户 `merge` | 面板不适用（无远端定义），用户录入即是最上层 | 同上一行 |
| 登录抓页面 / 官方 API | **一条都不用**（除 M-Team token 可选） | 保持「不登录站点」 |

理由：它的三层全部建立在「能带 cookie 或 token 访问站点」之上（`cookiecloud` 甚至要模拟 Chrome TLS 指纹绕 CF，
见 [`moviepilot-pt-capabilities-research.md`](./moviepilot-pt-capabilities-research.md)），而 SeedArk 的定位是不登录站点、
所有站点侧数字由用户录入一次。所以能抄的是**分层与「取不到就空着」的纪律**，取数只能落在「用户录入 + M-Team token」这两条路上。

**(d) passkey 解决不了这件事【事实 + 结论】**
passkey 是 **Tracker 汇报凭证**（生效位置 `/announce`），时魔长在**账号页**上，请求 `mybonus.php` 需要的是
登录 Cookie（`uid` + `pass` + `session`）。拿 passkey 去请求账号页只会被站点当未登录。
且 SeedArk 早就持有 passkey 的另一处用途：重建磁力时 announce URL 里就带它
（`backend/internal/pt/service.go` 的 `magnetOf`，档案内保存、不进列表接口，`store.go` 0600 权限），
它只能用于「passkey 失效体检」这类 Tracker 侧的事。
另注：M-Team 官方 API 走的是控制台另取的 `x-api-key`，**不是 passkey**。
→ 所以「加 passkey」与「显示时魔」是两条不相交的路。

**(e) 落进口径：三档引擎 + 一个读法**
- `nexusphp`（用户录入 6 个公式常数）：可算**单颗边际 A 值**，标「估算 · NPHP 边际值」，**不与站方对账**；
- `manual`（用户从站点魔力页抄一个「当前每小时能获取」）：等价于 PT-depiler 抓到的那个数，
  标「录入 · 站点时魔」，**按本站做种体积均摊**得到每 GB 效率——站方没有公开按颗计费的权重，均摊是唯一诚实的取法；
- `none`：显示「—」，并说明是「没录引擎」而不是「该站必须没有」。

**「时魔·GB」的真实含义**：站点侧每小时点数 ÷ 本站做种体积，即「每 GB 盘位每小时换多少点」，
这是能算得出来、也能拿去和站方对账的收益指标；单颗 A 值只用于**站内**淘汰排序。
也因此**跨站不能比绝对值**（各站货币不可通约），一律按站内相对位置排序。

### 1.6.2 曲线也可能被换掉：Springsunday 的 log 版【事实 + 反解】

上一节的闭式公式有个前提：站点用的是 `B0×(2/π)×atan(A/L)` 这条 **atan 曲线**。
Springsunday（春天）把它换成了 **log**：`B = E × ln(1 + A/D)`（站点页字母是 E=51.5、D=435）。
这是**换函数**，不是换常数——调 B0/L 永远凑不出来。

反解证据（2026-11，站点页三行数据）：

| 行 | A | 页面 B值 | `51.5×ln(1+A/435)` | `51.5×(2/π)×atan(A/435)` |
|---|---|---|---|---|
| 我的数据 | 46.962 | 5.280 | **5.2797** ✅ | 3.53（−33%） |
| 全站前 5% | 5,120.463 | 131.180 | **131.182** ✅ | 48.72（−63%） |
| 全站平均 | 1,351.738 | 38.806 | 72.76 | — |

注意两处**方向**：小 A 点上 atan 偏低 33%，大 A 点上 atan 偏低 63%——atan 项的上限就是 B0（本站 51.5），A 再大也到不了页面上的 131.180。所以「用哪条曲线」不能只看线性区的贴合。

第三行对不上是**口径**问题不是公式问题：它是「按用户先平均」的聚合值，`ln` 是凹函数，
`f(平均A) ≠ 平均 f(A)`。**聚合行不能当校验点，只有单用户行能。**

同页另外两处与内核不同（都已在本地面板可表达）：
- 每种加成只数**官种**且限 ≥3GB：基础积分 `8.800 = 0.4×22`，而该用户官种有 23 颗
  （原版 `$count` 数全部做种颗数）→ 规则卡的 `bonusOfficialPattern`（官种正则，对种子名
  匹配、大小写不敏感）与 `bonusOfficialMinGB`（计入的最小体积）就是这一条；两个都留空
  时退回原版口径。正则写错按「不区分官种」降级，并在时魔口径文案里写明「暂按全部做种算」，
  不静默改口径；
- 加成 `等级 0.15 + 身份 0 + 官种 0.01 + 受邀 0 = 16%`，加法叠加后整体乘（同 M-Team）。

→ 识别方法（30 秒）：把页面 `(A = xx.x)` 与「B值」代进两条曲线，命中即定案；两条都
不中就说明还改了别的地方，退到 `manual` 抄录。**看 A 与 B 同时命中才可信**——只看 B 时，
两条曲线在线性区差别很小（A≪L 时 `atan x ≈ ln(1+x) ≈ x`）。

→ SeedArk 实现：`SiteProfile.BonusCurve`（空/`atan` = 原版，`log` = 魔改），曲线项收敛在
`curveTerm()`；log 站**没有上限**，`bonusCap` 返回 0（界面只写「估 X/h」，不编一个封顶值），
但 `BonusCap.Configured` 仍为 true——「配了口径」与「有上限」是两件事，
取 0 只影响「上限」这个展示位（`bonusCapsOf` 里配站数单独判）。
本地复刻整行的例子：`(0.4×22 + 5.2797) × 1.16 = 16.3325`，页面印 16.341
（站点内部用未四舍五入的 A 与 B，差 0.05%）。

**同一站还把 A 的形态也换了【事实】**：2026-11 站点页给出的公式是

```
A = Σ_i f(S_i) · (0.25 + 0.32·ln(1+T_i)) · (0.12 + 2.88 · 10^(−⌈(N_i−1)/5⌉/(N0−1)))
B = E · ln(1 + A/D)
```

站点原文对每个符号的定义（2026-11 抄录）：

| 符号 | 定义 |
|:--|:--|
| `S_i` | 第 i 个种子的大小，单位 GB |
| `f()` | 大小调节函数，**函数定义不公开** |
| `T_i` | 该种子的**累积做种时间**，单位周 |
| `N_i` | 该种子当前的做种者数 |
| `N0` | 参数，站点页印 `N0 = 12` |
| `E` / `D` | 参数，站点页印 `E = 51.5`、`D = 435` |

另外两条口径：部分种子不参与 A 值计算（管理组标为中性、系统按预定义规则排除；
当时页面显示 0 颗 0.00 KB），本地拿不到这份排除名单；官种加成取「数量加成 0.01 与
体积加成 0.03 中较小者」，总加成 `0.15 + 0 + 0.01 + 0 = 16%`。

与原版内核的三处差别：

| 项 | 原版 NPHP | 本站 |
|:--|:--|:--|
| 时间 | 发布至今的周龄 `1 − 10^(−周龄/T0)`，T0 周后贴近 1（有上限） | **累积做种时间** `0.25 + 0.32·ln(1+周)`，无上限（1 周 0.47、10 周 1.02、100 周 1.72） |
| 稀缺 | `1 + √2·10^(−(N−1)/(N0−1))`，连续 | `0.12 + 2.88·10^(−⌈(N−1)/5⌉/(N0−1))`，人数按 5 人一档向上取整，下限 0.12 |
| 体积 | `体积GB` | `f(S_i)`，**定义不公开** |

**结论：这类站的 A 无法在本地精确复刻**——`f` 不公开，站点还会按内部名单排除部分种子。
所以 Springsunday 只能走 `manual` 抄录；公式档的价值只剩「站内相对排序」，
前提是 f 对体积单调：按 `f=体积GB` 代入虽然绝对值偏大，但大小顺序不会反。

→ SeedArk 实现：`SiteProfile.BonusAModel`（空/`classic` = 原版，`log` = 上述对数周龄形态），
由 `aValueOf()` 按形态分派：

- **`T_i` 取 `Torrent.SecondsSeeding`（累积做种时间），不用 `dateCreated`（发布至今）**——
  这是两种形态时间口径的硬差别：老种新加时后者会把 Ti 估大，而 Ti 从我做种那天起累积；
- `f(S_i)` 只能按体积 GB 假设，口径文案写明「f 按体积假设」，避免用户拿它和页面 A 对账；
- `log` 形态不需要 `T0`（`bonusParamsOf` 不再要求），`N0` 必填，`E→BonusB0`、`D→BonusL`；
- 0.25/0.32/0.12/2.88/5 是该形态的定义（站点页本来就印这几个数），不做成可调参数。

**对账表（2026-11，站点「当前每小时能获得的积分/茉莉」）【事实】**：

| 行 | 做种数 | 做种体积GB | 官种数 | 基础积分 | A值 | B值 | 合计 | 加成 | 每小时茉莉 |
|:--|--:|--:|--:|--:|--:|--:|--:|--:|--:|
| 我的数据 | 24 | 869.2 | 23 | 8.800 | 46.962 | 5.280 | 14.080 | 1.16 | 16.341 |
| 全站平均 | 116 | 2,051.4 | 84 | 7.873 | 1,351.738 | 38.806 | 46.679 | — | — |
| 全站前 5% | 462 | 8,221.0 | 356 | 20.000 | 5,120.463 | 131.180 | 150.086 | — | — |

「我的数据」行四个环节都能逐项对上：`8.800 = 0.4×22`（官种 23 颗里 1 颗 <3GB）、
`5.280 = 51.5×ln(1+46.962/435)`、`14.080 = 8.800+5.280`、`16.341 ≈ 14.080×1.16`
（站点用未舍入的 A/B 值）。

**A 与体积的比值反向印证了 f 不是线性体积**：`46.962 ÷ 869.2 = 0.0540`。若 `f(S_i)=体积GB`，
这个比值就是「时间项 × 稀缺项」的平均值，其理论最小值是 `0.25×0.12 = 0.030`
（周龄 0、做种人数无穷）。要凑到 0.054，需要平均周龄≈0（<1.5 天）**且**每颗做种 80 人以上
（`T=0` 时稀缺项要 0.216 → `10^(−s/11)=0.0333` → 档位 17 → N>81）。
24 颗种子全部处于这种状态并不寻常——与站点「f 定义不公开」一起看，结论就是
**f 不是「体积 GB 直接相乘」**（可能是分档或带缩比）。

→ 所以 Springsunday **不要**配成公式档：本地按 `f=体积GB` 算出的 ΣA 会比页面大一个数量级
以上，而 B 是 `E·ln(1+A/D)`、误差不会被压小。用 `manual` 抄录；
若一定要看「哪几颗更值」，可临时用公式档做**站内相对排序**，但别读绝对值。
（「全站平均 / 前 5%」两行是按用户先平均的聚合值，`A` 是人均 A，不能拿 `A/体积` 去校验公式。）

数值锚点（单测口径）：10GB、刚累积做种 0 周、1 人做种 → `10 × 0.25 × 3.0 = 7.5`；
同输入在原版形态下时间项为 `1−10^0 = 0`，两者必须能分开。

### 1.7 促销与官种【事实】
类型枚举与列表页 class 固定（`pro_free / pro_free2up / pro_2up / pro_50pctdown2up / pro_30pctdown / pro_50pctdown / hitandrun`，见 [NexusPHP.ts](https://raw.githubusercontent.com/pt-plugins/PT-depiler/master/src/packages/site/schemas/NexusPHP.ts)）。促销时长各站差异大【未验证】，**必须用户录入**。
官种可用本地正则识别：M-Team `-(mteam|mpad|tnp|BMDru|MWEB)`、HDSky `[@-]\s?(HDSky|HDSWEB|HDSTV|HDSPad|HDS)`、CHDBits `[@-]\s?(CHD(?:Bits|TV|PAD|WEB|HKTV)?|StBOX|OneHD)`。

### 1.8 考核
NexusPHP 内核**没有**独立考核模块【事实：allconfig 无相关配置段】。唯一可引用证据：PT 助手新站模板固定要求「如有考核，请确认是否可免考，因为开发人员无法保证完成考核」【[#1816](https://github.com/pt-plugins/PT-Plugin-Plus/issues/1816)】。
→ SeedArk **不内置任何站点考核阈值**，只做「用户录入目标 + 本地实况算下界」。

## 2. 站点侧数据通道

| 通道 | 内容 | 代价 |
|:--|:--|:--|
| NPHP cookie 抓取（`userdetails.php` / `mybonus.php` / `getusertorrentlistajax.php` / `myhr.php`） | 上传/下载/魔力/做种积分/等级/H&R 计数/做种体积、**每颗种子的站点侧上传** | 需登录态；站点普遍禁止脚本抓取，**封号风险最集中** |
| **M-Team 官方 API（`x-api-key` + `Origin`）** | 结构化 JSON：`/api/member/profile`、`/api/tracker/mybonus`（`formulaParams.finalBs` = 时魔）、`/api/tracker/myPeerStatistics`（seederCount/seederSize） | 只需用户粘一次 token，官方支持 |
| `getusertorrentlistajax.php` 的 `snatched` 表 | **站点侧单种上传/下载** | 同第一条 |

→ SeedArk 定位：**默认一个都不用**。v3 的站点侧数据全部走「用户手工抄一次」（快照 + 考核），只有 M-Team token 留作将来可选的唯一站点通道。

## 3. 开源工具对标

### 3.1 cross-seed —— 站点压力分类与价值观
[Impact on Trackers](https://www.cross-seed.org/docs/reference/tracker-impact)
- 负载分四类：RSS（便宜）/ search（贵在站点算力）/ **announce（走 IRC，对站点零负载）**/ snatch（贵在站点网络 I/O，有些站把 snatch 计入等级）。
- 默认纪律：批量搜索 ≥30s 间隔、每日 ≤400 次、snatch 去重缓存不重复抓；批量任务会 drift（跑的过程中用户还在加删种子）→ 批次要短、可续跑。
- 价值观原话：**「重要的是长期做种，而不是最早开始做种。」** —— 这正是保种台的取向，与 autobrr 那类「抢首发」反向。

### 3.2 autoremove-torrents —— 规则语言该长什么样
[属性表](https://autoremove-torrents.readthedocs.io/zh-cn/latest/intro.html) / [配置](https://autoremove-torrents.readthedocs.io/zh-cn/latest/config.html)
- 22 项可用条件：`ratio / seeding_time / create_time / last_activity / upload_ratio(上传量÷**种子大小**) / max_seeder / min_leecher / max_progress / free_space / remote_free_space / average_uploadspeed …`
- **集合级触发**（先看整体再挑一部分删）：`seed_size: {limit, action}`、`maximum_number`、`free_space: {min, path, action}`；
- **淘汰维度是独立枚举**：`remove-old / new / big / small / active / inactive / fast-upload / slow-upload-seeds`；
- 过滤器支持 `excluded_categories / excluded_trackers / excluded_status`；
- 强制 `--view` 预览。

→ 直接影响了 SeedArk 的设计：**把「触发条件（磁盘/体积/数量）」与「淘汰排序」拆开**，且先做 dry-run。

### 3.3 ptool —— 最接近「保种台」的 OSS 实现
[仓库](https://github.com/sagan/ptool) / [dynamicseeding 文档](https://pkg.go.dev/github.com/sagan/ptool/cmd/dynamicseeding)
- **dynamicseeding（全站动态保种）**：自动下载「**做种人数 < 10**」的种子并做种；每站可设 `dynamicSeedingSize` 总保种体积上限；默认只拿**免费且无 H&R**；**当前保种的种子若做种人数充足（无断种风险）会在需要时被删掉腾空间**；自己加的种子放 `dynamic-seeding-<site>` 分类并打 `site:` 标签。
- brush：硬盘空间不足自动删种。
- `findalone`：查找下载目录里「有文件但没在做种」的内容。
- `cookiecloud` 同步站点 cookie、`impersonate` 模拟 Chrome TLS/HTTP2 指纹绕 CF 盾 —— **这正是「碰站点账号」的真实代价**。

→ SeedArk 取它的**判据**（稀缺阈值 10、体积上限、免费/H&R 闸门、swarm 健康才可释放），**不取它的动作**（它真的会下载/删除；我们只输出建议）。`findalone` 已移植为「腾空间 → 有文件没做种」。

### 3.4 tracker-tracker —— 账号侧数据层的成熟形态
[仓库](https://github.com/jordanlambrecht/tracker-tracker) / [文档](https://jordanlambrecht.github.io/tracker-tracker/)
- 按计划轮询站点 API，把 upload/download/ratio/**buffer**/seedbonus/**rank** 存成时间序列并出图；qB/TR 集成（每站种子明细、做种数、**辅种统计**、**活跃热力图**、速度历史）。
- **每个 tracker 适配状态公开标注**：Verified / Unverified / Draft / Stuck / Broken，欢迎用户帮忙验证。
- 专门的 **Stats Explained** 文档逐项解释指标口径，并明确写 **"We record what the tracker returns. No cross-tracker comparison."**
- 告警触发点：ratio 下跌 / 站点宕机 / buffer 里程碑 / 等级变化；凭据用主密码派生密钥加密。

→ SeedArk 借鉴：规则卡带 `ruleSource / ruleCheckedAt` 并标「已核对 / 待核对 / 超 90 天」；「本地 vs 站点」只记录偏差、不做跨站比较。

### 3.5 其它
- **qui**（[仓库](https://github.com/autobrr/qui)）：Automations（条件+动作）、**Backups & Restore（定时快照 + 恢复模式）** → 佐证「整库快照」是与档案库互补的另一层保险。
- **autobrr**（[仓库](https://github.com/autobrr/autobrr)）：IRC announce 抢首发；过滤表达式 + 多下载器投递 + 20 余种通知渠道。取向不同，只借「过滤表达式」与「通用 webhook 就够」。
- **PTMyBonusCalc**（[仓库](https://github.com/neoblackxt/PTMyBonusCalc)）：种子列表加「时魔 / 时魔·GB」两列并着色；README 明写**「首次使用须打开各站的魔力值公式或商店页面获取公式参数」** → 「手工录入一次」的社区先例。
- **IYUU**（[仓库](https://github.com/ledccn/IYUUAutoReseed)）：自动辅种；NexusPHP 后台甚至预留 `iyuu_secret` / `nas_tools_key` 集成口。风险反面教材：[PT-Plugin-Plus#2110](https://github.com/pt-plugins/PT-Plugin-Plus/issues/2110)「多站点辅种时 PT 助手**自动用魔力购买种子**」。

## 4. 社区证据（可复核）
- [kymjs《PT 站规则扫盲》](https://kymjs.com/stickies/2025/12/12/01/)：开头「最近邀请了不少新人，**结果好多被封号的**，浪费了邀请码实在可惜」——封号是高频真实事件。**正文为私密文章，只引用此句，未取证正文。**
- [V2EX《PT 新手考核疑问》](https://global.v2ex.co/t/936832)：新人对「做种时间」等指标算不明白。
- [vertex 刷流工具教程](https://guyuan.de/archives/vertex)：多组删种规则 + **优先级轮询**（先删上传速度小的，保证空间）。
- PT-Plugin-Plus 长期需求 Issue：[做种体积/做种数缺失 #2223 #2127 #2116 #2132 #2099](https://github.com/pt-plugins/PT-Plugin-Plus/issues/2223)、[H&R 列 #924](https://github.com/pt-plugins/PT-Plugin-Plus/issues/924) → 做种体积/做种数正是 SeedArk 的强项（本地直接有，比抓 HTML 稳）。

## 5. 落到 SeedArk 的结论

1. **站点侧决定生死的只有三件事**：阶梯降级、不活跃/无流量删号、H&R 累计次数封号；它们的参数（含时魔 6 个常数）都是**少量、稳定、可手工录入一次**的 → 不需要站点账号。
2. **稀缺度是第二维价值**：ptool 用「做种人数 < 10」当判据，与本项目「保种分只看分享率+效率」互补。已落为 `swarm / scarcity`，并作为释放安全的前置条件。
3. **释放判据升级**：从「最短做种天数到期就能放」变成「义务已满足 **且** swarm 不稀缺」——稀缺的即使到期也标「建议继续保」。
4. **触发与淘汰分离**：磁盘剩余是真实触发条件（数据接口早已存在），淘汰按 `时魔·GB → 稀缺度 → 体积` 排序并给出人话理由。
5. **必须诚实标注的三处精度损失**：本地累计流量 ≠ 站点统计（促销放大、只计有效 peer，所以考核进度是**下界**）；tracker scrape 的做种人数可能缺失（按最坏情况处理）；时魔周龄若无 `dateCreated` 只能用本地添加时间（会低估）。

## 6. 未验证 / 未能取证
1. 贴吧 / NGA / V2EX / Telegram 的原始用户言论（`web_search` 默认 provider 不可用、`api.github.com` 403、`v2ex.com` 直连失败）——社区侧只用 GitHub Issue / 规则原文 / 工具行为替代。
2. 各站「新人考核」具体阈值与时长（考核页需登录）。
3. NexusPHP 做种积分默认门槛 40000/80000/…（PT-depiler 注释指向 `User.php#L61-L68`，allconfig 原文未找到）。
4. allconfig `$TORRENT` 随机促销数值的精确语义、各站促销到期时长。
5. Pterclub 的 H&R 参数、U2 的 H&R/考核规则、「官种是否有额外考核权重」。
6. `savept.icu`（PT监护室）站点健康度汇总——本次抓取超时，未取证。

## 7. 一手来源清单
[NexusPHP allconfig](https://raw.githubusercontent.com/xiaomlove/nexusphp/php8/config/allconfig.php) · [mybonus.php](https://raw.githubusercontent.com/zjutjh/NexusPHP/master/mybonus.php) · [getusertorrentlistajax.php](https://raw.githubusercontent.com/zjutjh/NexusPHP/master/getusertorrentlistajax.php) · [HitAndRunRepository.php](https://raw.githubusercontent.com/xiaomlove/nexusphp/php8/app/Repositories/HitAndRunRepository.php) · [HitAndRun.php](https://raw.githubusercontent.com/xiaomlove/nexusphp/php8/app/Models/HitAndRun.php) · [nexus.php 集成密钥](https://raw.githubusercontent.com/xiaomlove/nexusphp/php8/config/nexus.php) · [PT-depiler NexusPHP schema](https://raw.githubusercontent.com/pt-plugins/PT-depiler/master/src/packages/site/schemas/NexusPHP.ts) · [M-Team](https://raw.githubusercontent.com/pt-plugins/PT-depiler/master/src/packages/site/definitions/mteam.ts) · [CHDBits](https://raw.githubusercontent.com/pt-plugins/PT-depiler/master/src/packages/site/definitions/chdbits.ts) · [HDSky](https://raw.githubusercontent.com/pt-plugins/PT-depiler/master/src/packages/site/definitions/hdsky.ts) · [U2](https://raw.githubusercontent.com/pt-plugins/PT-Plugin-Plus/dev/resource/sites/u2.dmhy.org/config.json) · [PTer](https://raw.githubusercontent.com/pt-plugins/PT-Plugin-Plus/dev/resource/sites/pterclub.com/config.json) · 规则原文 [#1903](https://github.com/pt-plugins/PT-Plugin-Plus/issues/1903) [#1816](https://github.com/pt-plugins/PT-Plugin-Plus/issues/1816) [#1153](https://github.com/pt-plugins/PT-Plugin-Plus/issues/1153) · [cross-seed tracker impact](https://www.cross-seed.org/docs/reference/tracker-impact) · [cross-seed getting started](https://www.cross-seed.org/docs/basics/getting-started) · [autoremove-torrents intro](https://autoremove-torrents.readthedocs.io/zh-cn/latest/intro.html) · [autoremove-torrents config](https://autoremove-torrents.readthedocs.io/zh-cn/latest/config.html) · [ptool](https://github.com/sagan/ptool) · [ptool dynamicseeding](https://pkg.go.dev/github.com/sagan/ptool/cmd/dynamicseeding) · [tracker-tracker](https://github.com/jordanlambrecht/tracker-tracker) · [tracker-tracker docs](https://jordanlambrecht.github.io/tracker-tracker/) · [qui](https://github.com/autobrr/qui) · [autobrr](https://github.com/autobrr/autobrr) · [PTMyBonusCalc](https://github.com/neoblackxt/PTMyBonusCalc) · [IYUUAutoReseed](https://github.com/ledccn/IYUUAutoReseed) · [辅种误用魔力 #2110](https://github.com/pt-plugins/PT-Plugin-Plus/issues/2110) · [kymjs 规则扫盲](https://kymjs.com/stickies/2025/12/12/01/) · [V2EX 新手考核](https://global.v2ex.co/t/936832) · [vertex 教程](https://guyuan.de/archives/vertex)
