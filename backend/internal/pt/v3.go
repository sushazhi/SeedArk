package pt

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sushazhi/seedark/backend/internal/models"
	"github.com/sushazhi/seedark/backend/internal/state"
)

// 本文件是 v3 增补的计算口径：稀缺度、时魔估算、种子年龄、释放安全判据、淘汰排序。
//
// 三处外部依据（详见 docs/pt-site-rules-research.md）：
//   - ptool 的 dynamicseeding：「做种人数 < 10」视为亟需保种，swarm 健康才允许删掉腾空间；
//   - NexusPHP mybonus.php：时魔是 周龄 × 体积 × 稀有度 的闭式公式；
//   - autoremove-torrents：把「集合级触发条件（体积/数量/磁盘）」与「淘汰维度」拆开。
//
// 本文件的产出全部是**建议**：不删、不暂停、不上报。

// defaultScarceSeeders 稀缺阈值默认值。取 ptool 的经验值 10（<10 视为亟需保种），
// 它是参考值不是站点规则，可在 PT 设置里改。
const defaultScarceSeeders = 10

// swarmOf 按做种人数分档。seeders <= 0 视为「未知」：私有站常常不给 scrape，
// 这时不能断言「稀缺」也不能断言「充足」，只能如实标 unknown 并禁止自动建议释放。
func swarmOf(seeders, threshold int) Swarm {
	if seeders <= 0 {
		return SwarmUnknown
	}
	if threshold <= 0 {
		threshold = defaultScarceSeeders
	}
	switch {
	case seeders < threshold:
		return SwarmScarce
	case seeders < threshold*5:
		return SwarmNormal
	default:
		return SwarmAbundant
	}
}

// scarcityOf 稀缺度 0-100：做种人数越少越高。
// 用 1/(1+n) 类的柔软衰减而不是线性：从 2 人变到 5 人的意义远大于从 80 人变到 83 人。
// 未知一律给 100——「不知道」要按最坏情况处理，否则会把可能稀缺的种子建议释放掉。
func scarcityOf(seeders int) int {
	if seeders <= 0 {
		return 100
	}
	v := 100.0 / (1.0 + float64(seeders)/3.0)
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	return int(v + 0.5)
}

// ageOf 返回种子年龄（天）与来源。dateCreated 是站点发布时刻（TR 的 dateCreated、
// qB 从 .torrent 解析），比本地 addedDate 准：老种新加时 addedDate 会把周龄估小，
// 进而把时魔估小。取两者中较早者作为「这颗种活了多久」，并标明这个年龄的起点。
// 两个时刻都拿不到时退到「已做种时长」——语义最接近，但仍不是发布日期。
func ageOf(t *models.Torrent, now time.Time) (days float64, source string) {
	var start int64
	switch {
	case t.DateCreated > 0 && t.AddedDate > 0:
		start, source = t.DateCreated, AgeCreated
		if t.AddedDate < t.DateCreated {
			start, source = t.AddedDate, AgeClient
		}
	case t.DateCreated > 0:
		start, source = t.DateCreated, AgeCreated
	case t.AddedDate > 0:
		start, source = t.AddedDate, AgeClient
	default:
		return float64(t.SecondsSeeding) / 86400, AgeClient
	}
	days = float64(now.Unix()-start) / 86400
	if days < 0 {
		days = 0
	}
	return days, source
}

// BonusMethodKey 机读口径键（TorrentRef.BonusMethodKey）。
const (
	MethodNone  = "none"     // 没得算：没录引擎 / 该站没有时魔
	MethodNPHP  = "nexusphp" // 按 NPHP 公式估的单颗边际值
	MethodSite  = "site"     // 用户抄录的站点时魔（本行是按做种体积均摊到每 GB 的效率）
	MethodStale = "stale"    // 公式常数不全，只算得出「每种固定加成」那部分
)

// bonusParamsOf 从站点规则里取时魔公式常数。缺一个就不算——宁可不显示，也不拿默认值凑。
//
// T0 只有 classic 形态需要：对数周龄形态（BonusAModelLog）的时间项是固定的
// 0.25+0.32·ln(1+周龄)，填了 T0 也不参与计算，所以不因为它为空而判「未配置」。
func bonusParamsOf(p state.SiteProfile) (state.SiteProfile, bool) {
	if p.BonusN0 <= 0 || p.BonusB0 <= 0 || p.BonusL <= 0 {
		return p, false
	}
	if state.BonusAModelOf(p) == state.BonusAModelClassic && p.BonusT0Weeks <= 0 {
		return p, false
	}
	if p.BonusPerSeeding <= 0 && p.BonusMaxSeeding <= 0 {
		return p, false
	}
	return p, true
}

// aValueOf 单颗的 A_t（不含 perseeding 乘子）——站方页面那个 `(A = xx.x)` 就是全体 A_t 的和。
// 算 ΣA、复刻 B、算「删掉损失」都必须用它；任何地方都不要再乘 perseeding，
// 否则 atan(ΣA/L) 会被整体放大（perseeding 只在 B 的第二项出现）。
//
// 形态按 BonusAModel 分派（见 state.BonusAModelOf）：classic 走原版 NPHP，
// log 走「对数周龄 + 分档稀缺」。log 形态里的 f(S_i) 在站点侧是**不公开的函数**
// （Springsunday 原文如此），这里只能取 f=体积GB 的假设口径；用它算出的 A 与页面
// A 差一个数量级（见 docs 1.6.2 对账表），所以值不能当绝对值，只能做站内相对排序。
//
// 两种形态的**时间口径不同**，不能共用一个参数：
//   - classic 用 ageDays（站点发布时间至今的周龄，原版 mybonus.php 的 $weeks_alive）；
//   - log 用 Ti =「该种子的累积做种时间」（站点原文），本地取 SecondsSeeding。
//     刻意不用 ageDays：老种新加时 ageDays 会把 Ti 估大，而 Ti 是从我做种那天起累积的。
func aValueOf(t *models.Torrent, p state.SiteProfile, ageDays float64, seeders int) float64 {
	sizeGB := float64(t.TotalSize) / float64(1<<30)
	if sizeGB <= 0 {
		return 0
	}
	if state.BonusAModelOf(p) == state.BonusAModelLog {
		return logAgeOf(float64(t.SecondsSeeding)/86400) * sizeGB * steppedRarityOf(seeders, p.BonusN0)
	}
	return growthOf(ageDays, p.BonusT0Weeks) * sizeGB * rarityOf(float64(seeders), p.BonusN0)
}

// donorTimesOf 捐赠者「N 倍时魔」乘子。未录入（<=0）按 1 处理——刻意不给默认 2：
// PT-depiler 的教训是默认 2 之后，selector 已含倍数的站忘改回 1 就会双倍（其「常见坑」第 9 条）。
// 站点页面会打印「Donors always get N times of bonus」，是捐赠者才需要录的开关。
func donorTimesOf(p state.SiteProfile) float64 {
	if p.BonusDonorTimes > 0 {
		return p.BonusDonorTimes
	}
	return 1
}

// compileOfficialPattern 编译「官种匹配正则」（大小写不敏感）。
//
// 返回 re == nil 表示**不区分官种**：要么用户没录（空串），要么录错了（bad=true）。
// 写错正则时按「不区分」降级而不是当成「一颗官种都没有」——后者会静默把这一项算成 0，
// 用户只会以为站点改了规则；bad 标记让界面能直说「正则编译不过」。
func compileOfficialPattern(pat string) (re *regexp.Regexp, bad bool) {
	pat = strings.TrimSpace(pat)
	if pat == "" {
		return nil, false
	}
	compiled, err := regexp.Compile("(?i)" + pat)
	if err != nil {
		return nil, true
	}
	return compiled, false
}

// countedForPerSeeding 这颗种子是否计入「每种加成」的颗数。
//
// 三档口径（站点差异全在这里，代码不内置任何站点规则）：
//   - 没录正则、也没设最小体积 → 全部做种颗数（原版 NexusPHP）；
//   - 录了正则 → 只有种子名匹配的才算（如官种）；
//   - 设了最小体积 → 体积不足的不算（Springsunday：≥3GB）。
//
// re 由调用方按站点预编译传入（每颗现编译会拖慢全量遍历）；nil = 不区分官种。
func countedForPerSeeding(t *models.Torrent, p state.SiteProfile, re *regexp.Regexp) bool {
	if t == nil {
		return false
	}
	if re != nil && !re.MatchString(t.Name) {
		return false
	}
	if p.BonusOfficialMinGB > 0 && float64(t.TotalSize) < p.BonusOfficialMinGB*float64(1<<30) {
		return false
	}
	return true
}

// curveTerm 公式里那条曲线项（不含每种加成、加成% 与捐赠倍数）：
//
//	atan（原版 NexusPHP）：B0 × (2/π) × atan(A/L)  —— 有上限 B0，A 趋无穷才逼近
//	log （魔改站实测）：   B0 × ln(1 + A/L)        —— 无上限，A≪L 时 ≈ B0·A/L
//
// 为什么必须分两条而不是「调大 B0/L 凑」：两条曲线在小 A 区都近似线性，一处贴合
// 不代表另一处贴合。判据是页面上的两个数——Springsunday（2026-11）A=46.962→5.280、
// A=5120.463→131.180，log 式两点都中到 0.01%；代进 atan 式则是 3.53 / 48.72
// （差 33% / 63%：B0=51.5 的 atan 项无论 A 多大都越不过 51.5，而页面已到 131）。
//
// BonusL<=0 时按 0 处理：分母缺失时不猜（bonusParamsOf 已经拦过一次，这里是兜底）。
func curveTerm(p state.SiteProfile, sumA float64) float64 {
	if p.BonusL <= 0 || p.BonusB0 <= 0 {
		return 0
	}
	if sumA < 0 {
		sumA = 0
	}
	if state.BonusCurveOf(p) == state.BonusCurveLog {
		return p.BonusB0 * math.Log1p(sumA/p.BonusL)
	}
	return p.BonusB0 * (2 / math.Pi) * math.Atan(sumA/p.BonusL)
}

// bonusTotalFrom 由 ΣA 与**计入数量项的颗数**复刻站点侧会显示的那个总时魔 B：
//
//	B = 曲线项(ΣA) + perseeding × min(计入数量项的颗数, maxseeding)，再 × 加成% × 捐赠倍数
//	曲线项 = B0×(2/π)×atan(ΣA/L)（原版）或 B0×ln(1+ΣA/L)（魔改，见 curveTerm）
//
// 第二个参数是 perCount 而不是「做种颗数」：站点若把数量项限成官种/限体积
// （Springsunday：≥3GB 的官种），拿全部做种颗数会系统性偏高。调用方从 bonusCtx.perCount 取。
//
// 为什么本地能算准：mybonus.php 里 ΣA 只对「我自己在做种（peers.seeder='yes'）」的种子求和
// （`WHERE peers.userid = $CURUSER[id]`），不需要全站数据。只要「6 个常数 + 曲线形态 +
// 数量项口径 + 我的做种集合」与站方一致，B 就与页面显示值一致。
//
// 与站方可能有出入的三处，都必须如实标注、不许假装没有：
//  1. `seeders`：站方读 torrents.seeders（全站快照），我们只有自己 tracker 的 scrape，
//     可能滞后或缺失；缺失时按 1 人算 → B 会被**高估**（atan 会压缩一部分误差）；
//  2. `added`：站方用种子发布时间，我们必须用 dateCreated；退化到本地添加时间会把周龄估小；
//  3. 站点若改过公式（很多站魔改 mybonus.php，连曲线都换），标准公式就不成立 ——
//     用页面显示的 `A = xx.x` 与 `B值` 一起对账即可发现，对不上就说明该站不能按
//     当前曲线算（先换曲线，再换档到 manual）。
func bonusTotalFrom(p state.SiteProfile, sumA float64, count int) float64 {
	countF := float64(count)
	if p.BonusMaxSeeding > 0 && countF > p.BonusMaxSeeding {
		countF = p.BonusMaxSeeding
	}
	b := curveTerm(p, sumA) + p.BonusPerSeeding*countF
	return b * extraOf(p) * donorTimesOf(p)
}

// extraOf 站点页的加成百分比（等级奖励 / 二级验证 / 持续在线等，**加法**叠加在基本值上）。
// M-Team 实测：基本 41.178 + 府尹 6%(2.471) + 二级验证 10%(4.117) = 47.767，逐项吻合，
// 所以是 (1 + 16%) 而不是 (1.06 × 1.10)。捐赠者的 2 倍是另一层乘法，走 BonusDonorTimes。
func extraOf(p state.SiteProfile) float64 {
	if p.BonusExtraPct > 0 {
		return 1 + p.BonusExtraPct/100
	}
	return 1
}

// bonusLossOf 「删掉这一颗，站点总时魔少多少」的**精确差值**：
//
//	loss = B(ΣA) − B(ΣA − a) = [曲线项(ΣA) − 曲线项(ΣA−a)] + perseeding×[min(pe,M) − min(pe−1,M)]
//
// 其中 a 是这一颗的 A 值，M = maxseeding，pe = **计入数量项的颗数**（bonusCtx.perCount）。
// counted 表示这颗种子本身在数量项里（站点若只数官种/限体积，删掉一颗非官种不损失这一项）。
// 曲线项对两条曲线都成立（见 curveTerm）：atan 与 log 都是单调函数，差值恒正，只是
// 饱和速度不同。第二项只在 pe ≤ M 时才非零（超过 maxseeding 的颗数本来就不计 perseeding）。
// 这是单颗时魔唯一站得住的读法：A×perseeding 那种线性近似在曲线弯折处会显著高估。
func bonusLossOf(p state.SiteProfile, sumA float64, perCount int, a float64, counted bool) float64 {
	d := curveTerm(p, sumA) - curveTerm(p, sumA-a)
	per := 0.0
	if counted && (p.BonusMaxSeeding <= 0 || float64(perCount) <= p.BonusMaxSeeding) {
		per = p.BonusPerSeeding
	}
	// 与 bonusTotalFrom 同一套乘法：加成百分比（加法）与捐赠倍数（乘法）都作用在边际差值上
	return (d + per) * extraOf(p) * donorTimesOf(p)
}

// perCountNote 数量项口径的说明后缀（接在「站点页可对账 A = …」后面）。
// 站点若只数官种/限体积，必须写出来：否则用户删掉非官种、看到「损失 0」会以为算错了。
// 措辞避免再套一层括号：它本身已经在外层括号里，最里面的计数改成「22/24 种」。
func perCountNote(p state.SiteProfile, bc bonusCtx) string {
	switch {
	case bc.officialBad:
		return "；官种正则编译不过，数量项暂按全部做种算"
	case bc.officialRe != nil && p.BonusOfficialMinGB > 0:
		return "；数量项口径 ≥" + f3(p.BonusOfficialMinGB) + "GB 的官种，" + itoa(bc.count) + " 种做种里计入 " + itoa(bc.perCount) + " 种"
	case bc.officialRe != nil:
		return "；数量项只数官种，" + itoa(bc.count) + " 种做种里计入 " + itoa(bc.perCount) + " 种"
	case p.BonusOfficialMinGB > 0:
		return "；数量项口径 ≥" + f3(p.BonusOfficialMinGB) + "GB，" + itoa(bc.count) + " 种做种里计入 " + itoa(bc.perCount) + " 种"
	default:
		return ""
	}
}

// bonusCtx 一颗种子算时魔所需的**本站集合量**，由 buildRefs 先按站预计算好再逐行传入。
// 单独一个结构是因为这些量必须来自「同一份快照的同一遍遍历」：
// 分散在每行里现算，先算的行会把后算的行漏进/多算进 ΣA 与分母。
type bonusCtx struct {
	// sumA / count nexusphp 档：本站**正在做种**集合的 ΣA 与颗数（用于精确差值）
	sumA  float64
	count int
	// perCount nexusphp 档：其中**计入数量项**的颗数（没录官种口径时等于 count）。
	// 「每种加成 × min(做种数, 计满种数)」里的做种数按站点口径算：Springsunday 只数
	// ≥3GB 的官种，拿 count 去算会偏高。
	perCount int
	// officialRe 该站官种正则（nil = 不区分官种）；officialBad 表示录了但编译不过，
	// 已降级为「不区分」，界面要能直说而不是静默改口径。
	officialRe  *regexp.Regexp
	officialBad bool
	// hasParams nexusphp 档：该站公式参数是否齐全（不齐时无法复刻 B）
	hasParams bool
	// siteSeedBytes manual 档：本站正在做种的总体积（字节，0 = 拿不到）
	siteSeedBytes int64
}

// bonusOf 按引擎分派这颗种子的时魔与「时魔·GB」，并返回**人话口径**
// （前端直出；机读键在 TorrentRef.BonusMethodKey）。
//
// 三档引擎（见 SiteProfile.BonusEngine 与 docs/pt-site-rules-research.md 1.6.1）：
//
//	nexusphp：bc.hasParams 时给「删掉这颗损失多少时魔」的精确差值（bonusLossOf），
//	          这是单颗唯一站得住的读法；集合量由调用方按同一份快照预计算传入；
//	manual  ：用户抄的站点时魔 ÷ 本站做种体积 —— 唯一能和站方对账的口径，
//	          站方没有公开「按颗计费」的权重，均摊是唯一诚实的取法；
//	none    ：显示「—」，并说清是「没录」还是「这站没有」。
//
// 口径统一的一条硬规则：站方 SQL 是 peers.seeder='yes'，**没在做种的 peer 站方不计时魔**
// （下载中 left>0 不是 seeder，暂停的 peer 不在线）。所以非做种状态一律 0 + 说明，
// 绝不给一个「假如它在做种会值多少」的虚数。
func bonusOf(t *models.Torrent, p state.SiteProfile, ageDays float64, seeders int, bc bonusCtx) (perHour, perGb float64, method, methodKey string) {
	if t == nil {
		return 0, 0, "", MethodNone
	}
	engine := state.BonusEngineOf(p)
	if engine == state.BonusEngineNone {
		return 0, 0, "该站未启用时魔口径（或该站本来没有时魔）", MethodNone
	}
	if engine == state.BonusEngineManual {
		if p.BonusManualPerHour <= 0 {
			return 0, 0, "未抄录站点时魔（在规则卡里填一次「你当前每小时能获取」）", MethodNone
		}
		perHour = p.BonusManualPerHour
		if bc.siteSeedBytes <= 0 {
			// 拿不到本站做种体积：总量是真的，但摊不到单个种子。宁可不给单个的数，也不编一个。
			return 0, 0, "已抄录站点时魔 " + f3(perHour) + "/h，但拿不到本站做种体积，无法摊到每个种子", MethodSite
		}
		perGb = perHour / (float64(bc.siteSeedBytes) / float64(1<<30))
		return perHour, perGb, "录入 · 站点时魔（" + f3(perHour) + "/h ÷ 本站在做种体积）", MethodSite
	}

	// —— nexusphp ——
	// 站方只给 seeder='yes' 的 peer 计时魔：暂停/未完成的种子显示 0 并说明，
	// 不给「假如它在做种会值多少」的虚数——那正是「看起来准、其实错」的来源。
	if statusOf(t) != StatusSeeding {
		return 0, 0, "未在做种（暂停或未完成）：站点只给 seeder 计时魔", MethodNone
	}
	if _, ok := bonusParamsOf(p); ok && bc.hasParams {
		a := aValueOf(t, p, ageDays, seeders)
		// counted：这个种子是否在数量项里（站点可能只数官种/限体积）。删掉非官种不该
		// 让「每种加成」掉一档，否则用户会看到「删掉损失 0」并以为面板算错了。
		counted := countedForPerSeeding(t, p, bc.officialRe)
		perHour = bonusLossOf(p, bc.sumA, bc.perCount, a, counted)
		sizeGB := float64(t.TotalSize) / float64(1<<30)
		if sizeGB > 0 {
			perGb = perHour / sizeGB
		}
		// 曲线形态与 A 值形态都要写进口径：同一列「时魔·GB」在 atan/log 曲线、
		// 原版/对数周龄 A 下都不是一回事，不说清就会有人拿两边数字横向比。
		curve := "NPHP atan"
		if state.BonusCurveOf(p) == state.BonusCurveLog {
			curve = "log 曲线"
		}
		if state.BonusAModelOf(p) == state.BonusAModelLog {
			// f(S) 未公开：写明「按体积假设」，否则用户会拿这个数去和页面 A 对账
			curve += "·对数周龄 A，f 按体积假设"
		}
		return perHour, perGb,
			"估算 · 删掉这个种子损失 " + f3(perHour) + "/h（口径：" + curve + "；精确差值，站点页可对账 A = " + f3(bc.sumA) + perCountNote(p, bc) + "）",
			MethodNPHP
	}
	if p.BonusPerSeeding > 0 {
		// 只有「每种固定加成」这一类常数时不套整条公式——那样会假装周龄/体积/稀有度都算进去了。
		sizeGB := float64(t.TotalSize) / float64(1<<30)
		// 口径也要一致：站点若只给官种（或限体积）算数量项，非官种这一项本来就是 0，
		// 不能按「每种加成」给一个它其实拿不到的数。
		if !countedForPerSeeding(t, p, bc.officialRe) {
			return 0, 0, "估算 · 这个种子不在数量项口径内（该站只数官种/限体积），公式常数又不全，按 0 计", MethodStale
		}
		perHour = p.BonusPerSeeding
		if sizeGB > 0 {
			perGb = perHour / sizeGB
		}
		return perHour, perGb, "估算 · 每种固定加成（公式常数不全，周龄/体积/稀有度未计入）", MethodStale
	}
	return 0, 0, "未配置公式常数（在规则卡里录一次该站魔力公式的参数）", MethodNone
}

// growthOf 周龄乘子 (1 - 0.1^(周龄/T0))，T0<=0 时按 0 处理（不猜分母）。
func growthOf(ageDays, t0 float64) float64 {
	if t0 <= 0 {
		return 0
	}
	weeks := ageDays / 7
	if weeks < 0 {
		weeks = 0
	}
	return 1 - math.Pow(0.1, weeks/t0)
}

// logAgeOf 对数周龄乘子 0.25 + 0.32·ln(1+周龄)（BonusAModelLog）。
//
// 与原版 growthOf 的区别是**没有上限**：原版在 T0 周后接近 1 就不再涨，这条每翻一倍周龄
// 只加 0.32×ln2≈0.22（1 周 0.47、10 周 1.02、100 周 1.72）。所以老种在这种站上
// 的边际值比原版口径高得多，两种形态不能互相替代。
func logAgeOf(ageDays float64) float64 {
	weeks := ageDays / 7
	if weeks < 0 {
		weeks = 0
	}
	return 0.25 + 0.32*math.Log1p(weeks)
}

// steppedRarityOf 分档稀缺乘子 0.12 + 2.88·10^(−⌈(做种人数−1)/5⌉/(N0−1))（BonusAModelLog）。
//
// 做种人数按 5 人一档向上取整后再进指数：1 人拿满 3.0，6 人掉到第二档，
// 人数越多越趋近 0.12（不是 0）。原版是连续指数（1 + √2×10^(−(N−1)/(N0−1))），
// 两者在 N=1 处接近（2.41 vs 3.0），但档位一过就分开——照原版算会把多人数种子估高。
func steppedRarityOf(seeders int, n0 float64) float64 {
	n := seeders
	if n < 1 {
		n = 1 // 缺失按 1 人（与原版一致）：宁可高估，也不把稀缺种算便宜
	}
	if n0 <= 1 {
		n0 = 2 // 避免除零；分母退化时按常数处理
	}
	step := math.Ceil(float64(n-1) / 5)
	return 0.12 + 2.88*math.Pow(10, -step/(n0-1))
}

// rarityOf 稀有度乘子 (1 + √2 × 0.1^((做种人数-1)/(N0-1)))；N0<=1 时退化取常数。
func rarityOf(seeders, n0 float64) float64 {
	n := seeders
	if n < 1 {
		n = 1
	}
	if n0 <= 1 {
		n0 = 2 // 避免除零；分母退化时稀有度项按常数处理
	}
	return 1 + math.Sqrt2*math.Pow(0.1, (n-1)/(n0-1))
}

// bonusCap 该站的时魔每小时上限与它用的引擎。Configured=false = 这个站没配口径。
//
// nexusphp（atan 曲线）返回公式的理论上限 bzero + perseeding×maxseeding；
// nexusphp（log 曲线）**没有上限**，返回 0——调用方据此只写「估 X/h」而不写「上限 Y」。
// 编一个假的封顶值比留空更糟：atan 站的上限是真实存在的数，log 站的 B 跟着 A 一直涨。
// manual 返回抄录值本身（它是实测读数，不是什么上限，界面只需要一个可显示的量级，
// 已在引擎标签上区分）。
func bonusCap(p state.SiteProfile) (cap float64, engine string, configured bool) {
	switch state.BonusEngineOf(p) {
	case state.BonusEngineNexusPHP:
		if _, ok := bonusParamsOf(p); !ok {
			return 0, state.BonusEngineNexusPHP, false
		}
		if state.BonusCurveOf(p) == state.BonusCurveLog {
			return 0, state.BonusEngineNexusPHP, true
		}
		// 上限也要乘捐赠倍数：donor 的「N 倍时魔」作用于整个公式
		return (p.BonusB0 + p.BonusPerSeeding*p.BonusMaxSeeding) * extraOf(p) * donorTimesOf(p), state.BonusEngineNexusPHP, true
	case state.BonusEngineManual:
		if p.BonusManualPerHour <= 0 {
			return 0, state.BonusEngineManual, false
		}
		// manual 是站点页面抄来的实测值：捐赠倍数已经含在里面，不再乘
		return p.BonusManualPerHour, state.BonusEngineManual, true
	default:
		return 0, state.BonusEngineNone, false
	}
}

// f3 把浮点写成给人看的短串：最多 3 位小数、去掉尾随零（1.0 → "1"，0.500 → "0.5"）。
// 口径文案里要出现周龄、稀有度、每小时点数这类中间量，直接 %v 会带一串浮点尾巴。
func f3(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// releaseSafety 判断这颗种子「现在放掉安不安全」。
//
// 两个条件同时成立才安全：
//  1. 义务已满足（规则里的最短做种天数 / H&R 小时数都过了）；
//  2. swarm 不稀缺（做种人数充足）。
//
// 第 2 条来自 ptool 的判据：做种人数少的种子即使义务到期也该继续保——它不是「我的义务」，
// 是「这个种可能断种」。稀缺与未知一律判不安全，宁可少建议一次，不给错建议。
func releaseSafety(obligationMet bool, swarm Swarm, seeders int, hasRule bool) (safe bool, note string) {
	if !hasRule {
		return false, "缺站点规则，无法判断义务"
	}
	if !obligationMet {
		return false, "义务未满足"
	}
	switch swarm {
	case SwarmScarce:
		return false, "做种仅 " + itoa(seeders) + " 人，建议继续保"
	case SwarmUnknown:
		return false, "无做种人数数据，保守按需保处理"
	default:
		return true, "义务已满足 · 做种 " + itoa(seeders) + " 人"
	}
}

// swarmCounts 取主 tracker 的做种/下载人数。私有站常只对 scrape 有权限，
// 取不到就是 0（未知）——上层据此判 unknown，绝不当成「没人做种」。
func swarmCounts(t *models.Torrent) (seeders, leechers int) {
	for _, ts := range t.TrackerStats {
		if ts.SeederCount > 0 || ts.LeecherCount > 0 {
			return int(ts.SeederCount), int(ts.LeecherCount)
		}
	}
	return 0, 0
}

// obligationMet 做种义务是否已满足。与释放日历同一口径（规则里的最短天数与
// H&R 小时取更晚的那个），再叠一条 H&R 豁免：上传量 ÷ 种子体积 达到豁免分享率也算达标。
// 注意分母是 **种子体积**，不是下载量——照抄下载量会把 H&R 算松。
func obligationMet(t *models.Torrent, p state.SiteProfile, hasProfile bool) bool {
	if !hasProfile || seedBase(t) <= 0 {
		return false
	}
	seededHours := float64(t.SecondsSeeding) / 3600
	needHours := p.MinSeedDays * 24
	if p.HRSeedHours > needHours {
		needHours = p.HRSeedHours
	}
	if needHours <= 0 {
		return false // 没有任何义务可判
	}
	if seededHours >= needHours {
		return true
	}
	if p.HRAmnestyRatio > 0 && t.TotalSize > 0 {
		if float64(t.UploadedEver)/float64(t.TotalSize) >= p.HRAmnestyRatio {
			return true
		}
	}
	return false
}

// hrCountdown 返回 H&R 的剩余观察窗小时数与是否已超窗未达标。
// 只用于提示（「还有 X 小时必须做够」/「已超窗」），不触发任何动作。
func hrCountdown(t *models.Torrent, p state.SiteProfile, now time.Time) (leftHours float64, overdue bool) {
	if p.HRInspectHours <= 0 || t.DoneDate <= 0 {
		return 0, false
	}
	full := float64(t.DoneDate) + p.HRInspectHours*3600
	leftHours = (full - float64(now.Unix())) / 3600
	return leftHours, leftHours < 0
}

// itoa 整数转字符串（提示文案里用，省得调用处到处 import strconv）。
func itoa(v int) string { return strconv.Itoa(v) }

// Eviction 淘汰建议的响应（GET /pt/eviction）。
type Eviction struct {
	Trigger    EvictionTrigger     `json:"trigger"`
	Candidates []EvictionCandidate `json:"candidates"`
	TotalBytes int64               `json:"totalBytes"`
	Note       string              `json:"note"`
}

// EvictionTrigger 触发条件：磁盘剩余低于目标时 low=true，needBytes 是还差多少。
type EvictionTrigger struct {
	FreeBytes    int64   `json:"freeBytes"`
	TargetFreeGB float64 `json:"targetFreeGb"`
	NeedBytes    int64   `json:"needBytes"`
	Low          bool    `json:"low"`
}

// EvictionCandidate 一条淘汰建议。cumulativeBytes 是「删到这一行为止能腾出多少」，
// 让界面能直接说「删到第 3 行可腾出 120 GB」，而不必让用户自己加。
type EvictionCandidate struct {
	Hash       string  `json:"hash"`
	Name       string  `json:"name"`
	Site       string  `json:"site"`
	Size       int64   `json:"size"`
	Seeders    int     `json:"seeders"`
	Scarcity   int     `json:"scarcity"`
	BonusPerGb float64 `json:"bonusPerGb"`
	// BonusEff 该站在候选集里的相对位置（0=本站最该先放，1=本站最后才放）。
	// 排序用它而不是 BonusPerGb 的绝对值：各站货币不可通约，绝对值只在站内有意义，
	// 跨站混排会把「馒头的点」和「小站的猫粮」比大小。BonusPerGb 仍原样带出供人看；
	// 不需要时省略（0 是合法值，所以用指针）。
	BonusEff        *float64 `json:"bonusEff,omitempty"`
	Efficiency      float64  `json:"efficiency"`
	SafeToRelease   bool     `json:"safeToRelease"`
	Reason          string   `json:"reason"`
	CumulativeBytes int64    `json:"cumulativeBytes"`
}

// sortForEviction 淘汰排序：**站内相对时魔**升序（同站里最不划算的先放）→ 稀缺度降序
// （先放不稀缺的）→ 体积降序（一次腾得多）→ 做种人数降序（留人多的）。
//
// 为什么按站内相对值而不是 BonusPerGb 的绝对值：各站的「点」不可通约（见
// docs/pt-site-rules-research.md 1.6.1），混在一起排会拿馒头的积分去和别站的猫粮比大小。
// 站内排序回答的才是用户真正的问题——「同一个站里，哪几颗先放最不亏」。
// 只排序，不做决定：真正的判断在 releaseSafety。
func sortForEviction(rows []TorrentRef) {
	order, _ := evictionOrder(rows)
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		// 查不到位置（hash 为空）的行按「最不该先放」处理：map 缺失键会返回 0，
		// 若直接比较就会把无法排序的行顶到最前面，正好和意图相反。
		ea, oka := order[a.Hash]
		if !oka {
			ea = 1
		}
		eb, okb := order[b.Hash]
		if !okb {
			eb = 1
		}
		if ea != eb {
			return ea < eb
		}
		if a.Scarcity != b.Scarcity {
			return a.Scarcity > b.Scarcity
		}
		if a.Size != b.Size {
			return a.Size > b.Size
		}
		return a.Seeders > b.Seeders
	})
}

// evictionOrder 算每颗种子在**本站候选集**里的相对位置，并报告「这个位置有没有区分度」。
//
//	order[hash]：排序用。0 = 本站最该先放，1 = 本站最后才放，
//	             **0.5 = 本站内无法区分**（不做断言，交给稀缺度/体积去决定先后）。
//	known[hash]：位置能不能拿去跟用户说。只有本站确实存在两个以上不同时魔·GB 时才为 true。
//
// 为什么要区分「无法区分」：抄录档（manual）把站点时魔按做种体积均摊，同一站内每颗的
// 时魔·GB 完全相同——若照 0..1 的排名硬算，它们会全落到 0，界面就会对每一行都说
// 「本站里时魔最不划算」，那是个假断言。只有一颗候选的站同理：名次概念不成立。
//
// 分母用「本站候选数 - 1」而不是全体候选数：站点只有 2 颗时也能给出稳定的 0/1，
// 不会被别的站的规模带偏。同分并列给相同位置值（stable 排序据此保留原输入顺序）。
func evictionOrder(rows []TorrentRef) (order map[string]float64, known map[string]bool) {
	bySite := map[string][]float64{}
	for _, r := range rows {
		if r.Hash == "" {
			continue
		}
		bySite[r.Site] = append(bySite[r.Site], r.BonusPerGb)
	}
	order = make(map[string]float64, len(rows))
	known = make(map[string]bool, len(rows))
	for site, vals := range bySite {
		sorted := append([]float64(nil), vals...)
		sort.Float64s(sorted)
		n := len(sorted)
		// 有区分度 = 至少两颗，且最大值与最小值不同
		informative := n > 1 && sorted[0] != sorted[n-1]
		for _, r := range rows {
			if r.Hash == "" || r.Site != site {
				continue
			}
			if !informative {
				order[r.Hash] = 0.5
				known[r.Hash] = false
				continue
			}
			order[r.Hash] = float64(rankOf(sorted, r.BonusPerGb)) / float64(n-1)
			known[r.Hash] = true
		}
	}
	return order, known
}

// rankOf 在**已升序**的切片里取第一个相等元素的下标：并列同分给同一个位置，
// 而不是各给一个不同名次（那样同分的两颗会因为输入顺序被排出先后）。
func rankOf(sorted []float64, v float64) int {
	for i, x := range sorted {
		if x == v {
			return i
		}
	}
	return len(sorted)
}
