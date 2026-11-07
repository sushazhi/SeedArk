package state

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Server 一台下载器服务端的连接配置。
// Type 为下载器类型（transmission / qbittorrent），空值按 Transmission 处理，
// 这样既有部署的状态文件无需迁移即可继续工作。
// Pass 在 qBittorrent 下既可以是密码，也可以是 5.2+ 的 API Key（qbt_ 前缀）。
type Server struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	URL     string `json:"url"`
	User    string `json:"user"`
	Pass    string `json:"pass"`
	Enabled bool   `json:"enabled"`
	// DiskTotal 该盘总容量（字节，界面按 GB 录入）。0 表示未知：
	// qBittorrent Web API 只给剩余空间，界面无从自动取得总容量（Transmission
	// 的 free-space 接口两者都给），故允许手填，用于侧栏的占比环。
	DiskTotal int64 `json:"diskTotal,omitempty"`
}

// AutoMoveRule 自动文件管理规则
type AutoMoveRule struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Enabled   bool     `json:"enabled"`
	Sites     []string `json:"sites"`     // Tracker 站点（主机名）
	Labels    []string `json:"labels"`    // 标签
	NameMatch string   `json:"nameMatch"` // 名称包含
	TargetDir string   `json:"targetDir"`
}

// 做种策略动作
const (
	PolicyActionPause      = "pause"
	PolicyActionDelete     = "delete"
	PolicyActionDeleteData = "deleteData"
)

// SeedPolicyRule 做种策略：按站点/标签/名称圈定范围，达到分享率等目标后执行动作。
// MinRatio 等达标条件填 0 表示不参与判断，但至少需配置一个，否则规则无意义。
type SeedPolicyRule struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Enabled     bool     `json:"enabled"`
	Sites       []string `json:"sites"`     // Tracker 站点（主机名）
	Labels      []string `json:"labels"`    // 标签
	NameMatch   string   `json:"nameMatch"` // 名称包含
	MinRatio    float64  `json:"minRatio"`
	MinSeedDays float64  `json:"minSeedDays"`
	MinUploadGB float64  `json:"minUploadGB"`
	Action      string   `json:"action"` // pause / delete / deleteData
	// InheritSite 开启后，本规则留 0 的阈值字段取该种子所属站点的 SiteProfile 值，
	// 规则自身填了值的字段仍以规则为准（用于标签/名称级覆盖）。
	// 这样「站点要求保多少天、分享率多少」只需在站点规则里配一次，
	// 不必在策略规则里再抄一遍——两处抄两遍正是日历与策略会互相矛盾的根源。
	// 默认 false：既有规则行为完全不变。
	InheritSite bool `json:"inheritSite"`
}

// SeedPolicyGuard 做种策略全局安全保护，对所有规则生效
type SeedPolicyGuard struct {
	// Enforce 需显式开启，策略才会真正动作；关闭时只写预览记录。
	// 取这个方向是为了让「从未配置」落在安全的一侧。
	Enforce       bool     `json:"enforce"`
	MinSeedHours  float64  `json:"minSeedHours"`  // 全局最低做种时长（小时），0 表示不设下限
	ExcludeSites  []string `json:"excludeSites"`  // 排除的站点
	ExcludeLabels []string `json:"excludeLabels"` // 排除的标签
}

// SeedPolicyReasonPart 一条达标依据的结构化片段，由界面按语言渲染成文案
type SeedPolicyReasonPart struct {
	Kind   string  `json:"kind"`   // ratio / days / upload
	Actual float64 `json:"actual"` // 实际值（分享率 / 做种天数 / 上传 GB）
	Target float64 `json:"target"` // 规则设定的目标值
}

// SeedPolicyLog 策略执行记录，供界面回看引擎对哪些种子做了什么
type SeedPolicyLog struct {
	Time    int64                  `json:"time"`
	Rule    string                 `json:"rule"`
	Torrent string                 `json:"torrent"`
	Site    string                 `json:"site"`
	Action  string                 `json:"action"`
	Reason  []SeedPolicyReasonPart `json:"reason"`
	DryRun  bool                   `json:"dryRun"`
}

// 组内总限速方向
const (
	SpeedDirectionDown = "down" // 下载
	SpeedDirectionUp   = "up"   // 上传
)

// SpeedPolicyRule 分组限速规则：命中站点/标签/名称的一组种子共享总速度上限。
// 下载与上传可同时设置；填 0 表示该方向不限制（引擎不接管该方向）。
// 引擎每轮把「上限减去组内手动限速种子实际占用」后的余量均分给
// 组内运行中且未单独限速的种子，达到整组总和封顶的效果。
type SpeedPolicyRule struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Enabled   bool     `json:"enabled"`
	DownLimit int64    `json:"downLimit"` // KB/s，组内下载总上限；0 = 不限
	UpLimit   int64    `json:"upLimit"`   // KB/s，组内上传总上限；0 = 不限
	Sites     []string `json:"sites"`     // Tracker 站点（与做种策略同口径）
	Labels    []string `json:"labels"`    // 标签
	NameMatch string   `json:"nameMatch"` // 名称包含
}

// SpeedPolicyGuard 组内总限速引擎开关
type SpeedPolicyGuard struct {
	Enforce bool `json:"enforce"` // 关闭时引擎不触碰任何种子
}

// SpeedApplied 引擎对某个种子的接管记录。
// Honors 保存接管前该种子「跟随全局限速」的原值：下发单种限速前必须关掉它
// （honors 为 true 时 Transmission 会忽略本种子的限速字段），释放时再按原值还原，
// 否则会把原本跟随全局限速的种子改成完全不限速。
type SpeedApplied struct {
	Down   int64 `json:"down"`   // KB/s，0 表示该方向未接管
	Up     int64 `json:"up"`     // KB/s
	Honors bool  `json:"honors"` // 接管前 honorsSessionLimits 的值
}

// 时魔引擎（SiteProfile.BonusEngine）。它回答的是「这个站的时魔从哪来、口径是什么」，
// 而不是「公式长什么样」——后者是 BonusT0Weeks 那组常数的事。
//
// 参考 PT-depiler 的分层：引擎给默认来源，站点覆写差异项（见 docs/pt-site-rules-research.md 1.6.1）。
// 我们比它少一层「登录抓页面」，所以只剩「按公式估」与「用户抄一个站点值」两条路。
const (
	// BonusEngineNone 不估算：显示「—」。Gazelle 这类**本来就没有时魔**的站选它，
	// 这样界面能区分「没录」和「这站没有」，而不是一律显示 0。
	BonusEngineNone = "none"
	// BonusEngineNexusPHP 按 NexusPHP mybonus.php 的闭式公式估**单颗边际** A 值。
	// 它校不到站方显示的总时魔 B（B 依赖全站做种集合，拿不到），所以口径必须写清是边际值。
	BonusEngineNexusPHP = "nexusphp"
	// BonusEngineManual 用户从站点魔力页抄一个「当前每小时能获取」的数。
	// 覆盖所有站（含 Unit3D / 魔改站），是唯一能与站方对账的口径。
	BonusEngineManual = "manual"
)

// 时魔公式的**曲线形态**（SiteProfile.BonusCurve）。同一档引擎（nexusphp）下，
// 各站魔改的差异主要落在这一条曲线上：常数不同只是换参数，曲线不同则是换函数。
const (
	// BonusCurveAtan 原版 NexusPHP mybonus.php：B = B0×(2/π)×atan(A/L)。有上限 B0，
	// A 趋无穷才逼近它。
	BonusCurveAtan = "atan"
	// BonusCurveLog 魔改版（Springsunday/春天 实测）：B = B0×ln(1+A/L)。无上限，
	// A≪L 时近似线性 B≈B0·A/L；此时 B0 不再是「封顶值」而是「对数的尺度」。
	// 判据：把页面上的 A 代进这条式子能算中页面 B 值，代进 atan 式会差 30% 以上。
	BonusCurveLog = "log"
)

// BonusCurveOf 归一化曲线取值：空串按 atan 处理（v3 配置只有这一条公式，升上来语义
// 不变），未知取值也按 atan——宁可按原版算，也不猜一条没见过的曲线。
func BonusCurveOf(p SiteProfile) string {
	if p.BonusCurve == BonusCurveLog {
		return BonusCurveLog
	}
	return BonusCurveAtan
}

// 时魔公式里**单颗 A 值的形态**（SiteProfile.BonusAModel）。曲线管「A 怎么变成 B」，
// 这里管「A 怎么由体积/周龄/做种人数算出来」——两层都会被站点魔改，必须能分别选。
const (
	// BonusAModelClassic 原版 NexusPHP mybonus.php：
	//
	//	A_t = (1 − 10^(−周龄/T0)) × 体积GB × (1 + √2 × 10^(−(做种人数−1)/(N0−1)))
	BonusAModelClassic = "classic"
	// BonusAModelLog 对数周龄 + 分档稀缺（Springsunday 2026-11 站点页实测）：
	//
	//	A_t = f(S_i) × (0.25 + 0.32×ln(1+周龄))
	//	              × (0.12 + 2.88×10^(−⌈(做种人数−1)/5⌉/(N0−1)))
	//
	// 与 classic 的三处差别：时间项没有上限、做种人数按 5 人一档向上取整、体积项换成 f(S_i)。
	// 0.25/0.32/0.12/2.88/5 是该形态的定义（站点页本来就印这几个数），不是站点可调参数。
	//
	// **f(S_i) 站点明确「定义不公开」**（Springsunday 魔力说明原文），本地只能取 f=体积GB
	// 这个假设口径。用该站 2026-11 对账表验算：A=46.962 ÷ 869.2GB ≈ 0.054/GB，而公式里
	// 时间项×稀缺项的下界是 0.25×0.12=0.030——只有「全部种子刚发布且每颗 80+ 人做种」才凑得出，
	// 说明 f 不是线性体积，按 f=GB 算出的 A 会高一个数量级以上。因此本形态在 f 未公开的站上
	// **只能用于站内相对排序，不能当绝对值**；需要与站点对账的站一律用 manual 抄录。
	BonusAModelLog = "log"
)

// BonusAModelOf 归一化 A 值形态：空串/未知按 classic（老配置只有原版公式，语义不变）。
// 不认识的值不猜，否则会拿一条没见过的形态去算出一个看似精确的 A。
func BonusAModelOf(p SiteProfile) string {
	if p.BonusAModel == BonusAModelLog {
		return BonusAModelLog
	}
	return BonusAModelClassic
}

// BonusEngineOf 归一化引擎取值：空串按 NexusPHP 处理（v3 老配置只有公式常数，
// 升上来时语义不变），未知取值一律当 none——宁可不显示，也不套错公式。
func BonusEngineOf(p SiteProfile) string {
	switch p.BonusEngine {
	case BonusEngineNexusPHP, BonusEngineManual, BonusEngineNone:
		return p.BonusEngine
	case "":
		return BonusEngineNexusPHP
	default:
		return BonusEngineNone
	}
}

// SiteProfile PT 站点规则：把「哪个站的种子要保多久、分享率目标多少、
// H&R 要求多少小时」收敛成一条可编辑配置，供 PT 中心的释放日历/资产台账估算。
// MinSeedDays 与 HRSeedHours 都为 0 时视为规则缺失，估算一律显示「待定」而不是猜。
//
// v3 起它同时承载「站点规则卡」：保号/H&R 参数、时魔公式常数、等级阶梯与规则来源。
// 这些值站点各改各的、且会变（HDSky 2025-03-01 就换过等级表），所以一律由用户录入，
// 代码里不内置任何站点阈值——内置的那份迟早是错的，还会让人以为面板算错了。
type SiteProfile struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`        // 展示名
	Match       string  `json:"match"`       // Tracker 主机名/站点名的子串（大小写不敏感）
	MinSeedDays float64 `json:"minSeedDays"` // 站点要求的最短做种天数，0 = 不设
	MinRatio    float64 `json:"minRatio"`    // 目标分享率，0 = 不设
	HRSeedHours float64 `json:"hrSeedHours"` // H&R 要求的最短做种小时数，0 = 无 H&R
	// BudgetGB 该站保种体积预算（GB），0 = 不设。
	// 只用于在概览里标出「这台站占了多少盘、超没超预算」，不触发任何自动清理。
	BudgetGB float64 `json:"budgetGb"`
	Enabled  bool    `json:"enabled"`

	// —— H&R 四参数（NexusPHP HitAndRunRepository）——
	// HRAmnestyRatio 分享率达标即豁免，判定式是「上传量 ÷ 种子体积」。
	// 注意分母是 **种子体积**，不是下载量——照抄下载量会把 H&R 算松。
	HRAmnestyRatio float64 `json:"hrAmnestyRatio,omitempty"`
	// HRInspectHours 观察窗：距下载完成超过这么多小时仍未达标，记一次未达标。
	HRInspectHours float64 `json:"hrInspectHours,omitempty"`
	// HRBanCount 累计未达标几次封号；只用于提示「还剩几次」，不参与任何动作。
	HRBanCount int `json:"hrBanCount,omitempty"`

	// —— 删号线（NexusPHP $ACCOUNT），0 = 未配置 ——
	DeleteUnpackedDays   float64 `json:"deleteUnpackedDays,omitempty"`   // 未封存，N 天未登录
	DeletePackedDays     float64 `json:"deletePackedDays,omitempty"`     // 已封存，N 天未登录
	DeleteNoTransferDays float64 `json:"deleteNoTransferDays,omitempty"` // 注册后 N 天无流量

	// —— 时魔公式常数（NexusPHP mybonus.php 的 $BONUS）——
	// 全部为 0 表示未配置，此时不估算时魔（显示「—」），而不是拿内核默认值假装准确。
	BonusT0Weeks    float64 `json:"bonusT0Weeks,omitempty"`
	BonusN0         float64 `json:"bonusN0,omitempty"`
	BonusB0         float64 `json:"bonusB0,omitempty"`
	BonusL          float64 `json:"bonusL,omitempty"`
	BonusPerSeeding float64 `json:"bonusPerSeeding,omitempty"`
	BonusMaxSeeding float64 `json:"bonusMaxSeeding,omitempty"`

	// BonusOfficialPattern 官种匹配正则（对种子名匹配，大小写不敏感）。
	// 空 = 不区分官种，**所有在做种的种子**都计入「每种加成」的计数——原版 NexusPHP 就是这样。
	//
	// 为什么要有它：有的站把数量项限死在官种上（Springsunday 写的是
	// 「0.4 × 做种数，做种数最多计 50 个不小于 3GB 的官种」）。拿全部做种颗数去算，
	// 这一项会系统性偏高（实测该站 24 颗做种里只有 22 颗被计入，多算 0.8/小时）。
	// 各站官种命名各有一套，所以由用户录一次正则（docs/pt-site-rules-research.md 1.7
	// 有 M-Team / HDSky / CHDBits 的现成串），代码里不内置任何站点规则。
	BonusOfficialPattern string `json:"bonusOfficialPattern,omitempty"`
	// BonusOfficialMinGB 计入数量项的最小体积（GB）；0 = 不限体积。
	// Springsunday 的「不小于 3GB 的官种」→ 填 3。
	BonusOfficialMinGB float64 `json:"bonusOfficialMinGb,omitempty"`

	// BonusEngine 该站的时魔怎么算。空串 = 按 BonusNexusPHP 处理（v3 只有一条公式，
	// 老配置升上来时语义不变）。
	//
	// 为什么要有这个字段：站点内核不止 NexusPHP 一种（Unit3D / Gazelle / 二次开发魔改），
	// 时魔的**来源与口径**因此不同——NPHP 是「周龄×体积×稀有度」的闭式公式，
	// Unit3D 要走 /users/{name}/earnings 才看得到，Gazelle 压根没有这个概念。
	// 这些差异在 PT-depiler 里是「引擎默认 + 逐站覆写」两层（见
	// docs/pt-site-rules-research.md 1.6.1），这里用一档枚举把它显式化，
	// 好处是界面能直接说清「这个数是怎么来的」，而不是给所有站套一条又一条公式。
	BonusEngine string `json:"bonusEngine,omitempty"`

	// BonusCurve 上面那档引擎里，公式用的是哪条曲线：空/atan = 原版 NexusPHP 的
	// B0×(2/π)×atan(A/L)；log = 魔改站（Springsunday）的 B0×ln(1+A/L)。
	//
	// 为什么曲线要单独的字段而不是再开一档引擎：引擎回答「这个站的时魔从哪来」
	// （公式估 / 抄录 / 没有），曲线回答「同一套 A→B 的换算长什么样」。混成一档
	// 会让「估算」这个口径分裂成互斥的两种，界面上就说不清「同样是估算，为什么
	// 这个站没有上限」。
	BonusCurve string `json:"bonusCurve,omitempty"`

	// BonusAModel 单颗 A 值的形态：空/classic = 原版 NPHP 的「周龄乘子 × 体积 × 稀有乘子」；
	// log = 魔改站的「体积 × (0.25+0.32·ln(1+周龄)) × 分档稀缺」。
	//
	// 为什么与 BonusCurve 分开：曲线回答「A 怎么变成 B」，形态回答「A 怎么由体积/周龄/
	// 做种人数算出来」。站点两层都可能魔改——只换曲线不换形态，页面上的 (A = xx.x)
	// 就永远对不上，而 A 恰恰是最灵敏的对账锚点（曲线会把 A 的差压小）。
	BonusAModel string `json:"bonusAModel,omitempty"`

	// BonusManualPerHour 手工抄录的「站点侧时魔」（点/小时）：站点魔力页上那句
	// 「你当前每小时能获取 X」。
	//
	// 这是唯一能和站方对账的读数，也是 PT-depiler 实际显示的那个数；本面板拿不到登录态，
	// 所以只能由用户抄一次。按本站做种体积均摊后即「每 GB 每小时换多少点」。
	BonusManualPerHour float64 `json:"bonusManualPerHour,omitempty"`

	// BonusManualAt 上面那个数的抄录时刻（unix 秒），界面据此标「3 天前抄的」。
	BonusManualAt int64 `json:"bonusManualAt,omitempty"`

	// BonusExtraPct 站点页上「等级奖励 / 二级验证 / 持续在线」等**加成百分比之和**。
	// M-Team 这类站把基本奖励之外的部分按百分比**加法**叠加（如 府尹 6% + 二级验证 10%
	// → 填 16），面板把基本值乘 (1 + 16%) 才等于站点显示的合计。0 = 无加成。
	// 捐赠者的 2 倍单独走 BonusDonorTimes（它是乘法，且站点页单列一行）。
	BonusExtraPct float64 `json:"bonusExtraPct,omitempty"`
	// BonusDonorTimes 捐赠者「N 倍时魔」（NexusPHP $donortimes_bonus）。
	// 默认 0 = 不是捐赠者（按 1 倍算）；是捐赠者就照站点页面写 2（或其他）。
	// 刻意不给默认 2——PT-depiler 的教训：默认 2 之后忘改回 1 的站会双倍（其「常见坑」第 9 条）。
	BonusDonorTimes float64 `json:"bonusDonorTimes,omitempty"`

	// BonusASumObserved 从站点 mybonus.php 页面抄的 `(A = xx.x)` 实测值。
	// 页面把它直接打印在时魔旁边，是**最灵敏**的对账锚点（曲线项会把 A 的差压小，
	// atan 的饱和尤其明显）：本地复刻的 ΣA 与它一致 → 公式参数与做种集合都对；
	// 差得远 → 该站改过公式或数据缺失。
	BonusASumObserved float64 `json:"bonusASumObserved,omitempty"`

	// —— 规则来源与核对时间（对应 tracker-tracker 的适配状态公开标注）——
	RuleSource    string `json:"ruleSource,omitempty"`    // 规则页 URL
	RuleCheckedAt int64  `json:"ruleCheckedAt,omitempty"` // 最后核对时间（unix 秒）

	// Levels 等级阶梯。Alternatives 非空表示「任一满足即可」（如 PTer 的
	// 分享率路径 或 做种积分+发布数路径），这是各站差异最大的一处，必须能表达。
	Levels []SiteLevel `json:"levels,omitempty"`
}

// SiteLevel 站点等级阶梯的一级。全部字段都是「达到本级所需」的门槛。
type SiteLevel struct {
	Name         string      `json:"name"`
	Weeks        float64     `json:"weeks"`               // 注册周数
	DownloadedGb float64     `json:"downloadedGb"`        // 下载量下限
	Ratio        float64     `json:"ratio"`               // 分享率下限
	Bonus        float64     `json:"bonus,omitempty"`     // 魔力/猫粮等货币下限
	SeedBonus    float64     `json:"seedBonus,omitempty"` // 做种积分下限
	Uploads      float64     `json:"uploads,omitempty"`   // 发布数下限
	Kept         bool        `json:"kept,omitempty"`      // 该级封存后不删号
	Alternatives []SiteLevel `json:"alternatives,omitempty"`
}

// PTSettings PT 中心的全局阈值。放宽在 SiteProfile 之外：它们描述的是
// 「这台 NAS 怎么想」，而不是「某个站怎么要求」。
type PTSettings struct {
	// ScarceSeeders 做种人数低于它即视为「稀缺」。默认 10 取自 ptool dynamicseeding
	// 的经验值（<10 视为亟需保种），是可改的参考值而不是站点规则。
	ScarceSeeders int `json:"scarceSeeders"`
	// ReleaseTargetFreeGB 释放目标：磁盘剩余低于它才建议清理，0 = 不判断。
	ReleaseTargetFreeGB float64 `json:"releaseTargetFreeGb"`
}

// MatchSiteProfile 在站点规则里找最匹配的一条。
//
// 全站唯一的站点规则匹配实现：保种台（释放日历/台账）与做种策略都走它。
// 两处各写一份匹配逻辑，就会出现「日历说可释放、策略却继续攥着」这类互相矛盾的结论。
// 取「匹配串最长」的一条，避免 "m-team" 与 "team" 同时命中时选错；
// 双向子串匹配，规则里写站点简称也能命中完整 tracker 主机名。
func MatchSiteProfile(profiles []SiteProfile, site string) (SiteProfile, bool) {
	s := strings.ToLower(strings.TrimSpace(site))
	if s == "" {
		return SiteProfile{}, false
	}
	var best SiteProfile
	bestLen := -1
	for _, p := range profiles {
		if !p.Enabled {
			continue
		}
		m := strings.ToLower(strings.TrimSpace(p.Match))
		if m == "" {
			continue
		}
		if !strings.Contains(s, m) && !strings.Contains(m, s) {
			continue
		}
		if len(m) > bestLen {
			best = p
			bestLen = len(m)
		}
	}
	if bestLen < 0 {
		return SiteProfile{}, false
	}
	return best, true
}

// State 持久化状态
type State struct {
	Servers          []Server          `json:"servers"`
	ActiveServer     int               `json:"activeServer"`
	AutoMoveRules    []AutoMoveRule    `json:"autoMoveRules"`
	SeedPolicyRules  []SeedPolicyRule  `json:"seedPolicyRules"`
	SeedPolicyGuard  SeedPolicyGuard   `json:"seedPolicyGuard"`
	SeedPolicyLogs   []SeedPolicyLog   `json:"seedPolicyLogs"`
	SpeedPolicyRules []SpeedPolicyRule `json:"speedPolicyRules"`
	SpeedPolicyGuard SpeedPolicyGuard  `json:"speedPolicyGuard"`
	// SiteProfiles PT 站点规则（PT 中心用；与做种策略规则相互独立）
	SiteProfiles []SiteProfile `json:"siteProfiles"`
	// PTSettings PT 中心的全局阈值（稀缺人数、释放目标剩余空间）
	PTSettings PTSettings `json:"ptSettings"`
	// SpeedPolicyApplied 引擎最近一轮下发给各种子（方向 down/up）的限速 KB/s，
	// 以及接管前是否跟随全局限速。持久化保存：服务重启后仍能区分
	// 「引擎设定」与「用户手动设定」的限速，只在确认是引擎写入时才释放。
	SpeedPolicyApplied map[int64]SpeedApplied `json:"speedPolicyApplied"`
	ProcessedMoves     map[string]string      `json:"processedMoves"`  // hashString -> time
	ProcessedPolicy    map[string]string      `json:"processedPolicy"` // ruleID+"\x00"+hashString -> time
}

// Applied 返回该种子的接管记录（未接管过返回 ok=false）
func (st *State) Applied(id int64) (SpeedApplied, bool) {
	if st.SpeedPolicyApplied == nil {
		return SpeedApplied{}, false
	}
	a, ok := st.SpeedPolicyApplied[id]
	return a, ok
}

// DiskTotal 第 idx 台服务器手填的磁盘总容量（字节）；越界或未填返回 0。
// API 与 MCP 都拿它补下载器自述里缺失的总容量，判定口径必须一致。
func (st *State) DiskTotal(idx int) int64 {
	if idx < 0 || idx >= len(st.Servers) {
		return 0
	}
	return st.Servers[idx].DiskTotal
}

// AppliedCap 返回引擎在某方向下发给该种子的限速（0 表示该方向未接管）
func (st *State) AppliedCap(id int64, dir string) int64 {
	a, ok := st.Applied(id)
	if !ok {
		return 0
	}
	if dir == SpeedDirectionUp {
		return a.Up
	}
	return a.Down
}

// SetAppliedCap 记录某方向的下发值。种子首次被接管时，honors 写入接管前的原值；
// 已有记录时保留原值不动（后面每轮看到的都是引擎自己关掉的 false）。
func (st *State) SetAppliedCap(id int64, dir string, cap int64, honors bool) {
	if st.SpeedPolicyApplied == nil {
		st.SpeedPolicyApplied = map[int64]SpeedApplied{}
	}
	a, ok := st.SpeedPolicyApplied[id]
	if !ok {
		a = SpeedApplied{Honors: honors}
	}
	if dir == SpeedDirectionUp {
		a.Up = cap
	} else {
		a.Down = cap
	}
	st.SpeedPolicyApplied[id] = a
}

// ClearAppliedDir 清除某方向的接管记录；两个方向都清空时删除整条记录
func (st *State) ClearAppliedDir(id int64, dir string) {
	a, ok := st.Applied(id)
	if !ok {
		return
	}
	if dir == SpeedDirectionUp {
		a.Up = 0
	} else {
		a.Down = 0
	}
	if a.Down <= 0 && a.Up <= 0 {
		delete(st.SpeedPolicyApplied, id)
		return
	}
	st.SpeedPolicyApplied[id] = a
}

// Store JSON 文件存储（线程安全）
type Store struct {
	mu   sync.RWMutex
	path string
	data *State
}

// Load 加载状态文件，不存在时创建默认空状态。
// 文件损坏时不拒绝启动：改名留档后按默认状态继续，用户可自行修好 JSON 再改回原名。
// 直接退出会让 NAS 上整个面板下线，而损坏多半只是磁盘写入中断或手改出错。
func Load(path string) (*Store, error) {
	s := &Store{path: path}
	if data, err := os.ReadFile(path); err == nil {
		var st State
		if err := json.Unmarshal(data, &st); err != nil {
			backup := corruptBackupPath(path)
			if rerr := os.Rename(path, backup); rerr != nil {
				slog.Error("状态文件解析失败，且改名留档失败，已改用默认状态", "file", path, "err", err, "renameErr", rerr)
			} else {
				slog.Error("状态文件解析失败，已改名留档并改用默认状态", "file", path, "backup", backup, "err", err)
			}
		} else {
			st.normalize()
			s.data = &st
			return s, nil
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	s.data = &State{
		Servers:          []Server{},
		AutoMoveRules:    []AutoMoveRule{},
		SeedPolicyRules:  []SeedPolicyRule{},
		SpeedPolicyRules: []SpeedPolicyRule{},
		SiteProfiles:     []SiteProfile{},
		ProcessedMoves:   map[string]string{},
		ProcessedPolicy:  map[string]string{},
	}
	_ = s.save()
	return s, nil
}

// corruptBackupPath 损坏状态文件的留档名。已有留档时追加时间戳，
// 不覆盖上一次的损坏文件——那里面可能有用户还想抢救的配置
func corruptBackupPath(path string) string {
	backup := path + ".corrupt"
	if _, err := os.Stat(backup); err == nil {
		backup = backup + "." + time.Now().Format("20060102150405")
	}
	return backup
}

// normalize 补齐反序列化后可能缺失的映射表，并把下线动作降级到安全一侧
func (st *State) normalize() {
	if st.ProcessedMoves == nil {
		st.ProcessedMoves = map[string]string{}
	}
	if st.ProcessedPolicy == nil {
		st.ProcessedPolicy = map[string]string{}
	}
	if st.SpeedPolicyRules == nil {
		st.SpeedPolicyRules = []SpeedPolicyRule{}
	}
	if st.SpeedPolicyApplied == nil {
		st.SpeedPolicyApplied = map[int64]SpeedApplied{}
	}
	if st.SiteProfiles == nil {
		st.SiteProfiles = []SiteProfile{}
	}
	// 归档（move）已下线：旧规则改成暂停，避免被引擎静默跳过
	for i := range st.SeedPolicyRules {
		if st.SeedPolicyRules[i].Action == "move" {
			st.SeedPolicyRules[i].Action = PolicyActionPause
		}
	}
}

// Get 返回当前状态的深拷贝（调用方可自由修改，不影响内部数据）
func (s *Store) Get() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return *s.data.clone()
}

// clone 通过 JSON 往返复制状态（内部持有读锁时调用）
func (st *State) clone() *State {
	data, err := json.Marshal(st)
	if err != nil {
		return st
	}
	var cp State
	if err := json.Unmarshal(data, &cp); err != nil {
		return st
	}
	cp.normalize()
	return &cp
}

// Update 在锁内修改状态并持久化
func (s *Store) Update(fn func(*State)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s.data)
	return s.save()
}

// PersistUpdate 与 Update 相同，但把落盘失败记入日志并返回 false。
// 供后台引擎使用：它们没有可回显错误的响应，静默吞掉失败会让「内存已改、
// 文件未改」一直不被发现（磁盘满 / 只读时重启后表现为重复执行或状态回退）
func (s *Store) PersistUpdate(fn func(*State), what string) bool {
	if err := s.Update(fn); err != nil {
		slog.Warn(what, "err", err)
		return false
	}
	return true
}

func (s *Store) save() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(s.path); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	// 原子写入：先写临时文件再改名，避免进程中断导致损坏
	// 状态文件含服务器明文密码，权限收紧为仅属主可读写
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// DefaultStatePath 返回默认状态文件路径
func DefaultStatePath(dir string) string {
	if dir != "" {
		return filepath.Join(dir, "sa-state.json")
	}
	return "sa-state.json"
}

// PolicyKey 构造做种策略已处理标记键
func PolicyKey(ruleID, hashString string) string {
	return ruleID + "\x00" + hashString
}

// PolicyKeyHash 从做种策略已处理标记键中还原 hash（键格式异常时 ok=false）
func PolicyKeyHash(key string) (string, bool) {
	_, hash, ok := strings.Cut(key, "\x00")
	return hash, ok
}

// PruneHashMap 删除「关联种子已不存在」的标记（live 为当前全部种子的 hash 集合，
// keyHash 从键中取出关联 hash），返回删除条数。
// 已处理标记只为「避免重复处理已存在的种子」而存在，种子删除后标记再无读者，
// 不清会在状态文件里随种子增删无限堆积，而每次落盘都要重写全表。
func PruneHashMap(m map[string]string, live map[string]struct{}, keyHash func(string) (string, bool)) int {
	removed := 0
	for k := range m {
		h, ok := keyHash(k)
		if !ok {
			continue // 键格式异常：留给人工排查，不猜测
		}
		if _, exist := live[h]; !exist {
			delete(m, k)
			removed++
		}
	}
	return removed
}

// NowUnix 当前 Unix 时间戳
func NowUnix() int64 {
	return time.Now().Unix()
}
