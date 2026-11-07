// Package pt 提供 PT 中心的数据能力：站点规则估算、保种资产台账、释放日历、
// 种子档案库与复活、发种资料包、跨站同内容分组。
//
// 设计边界（与 MoviePilot 的差异）：本包只读下载器的全量种子并做资产视角的
// 统计与估算，不参与「把内容弄进来」的搜索/订阅/整理链路；所有释放时间都是
// 预计值，永不自动删除、永不自动上传站点。
package pt

import "github.com/sushazhi/seedark/backend/internal/state"

// Status 种子的粗粒度状态（前端据此出文案与配色）
type Status string

const (
	StatusSeeding     Status = "seeding"
	StatusDownloading Status = "downloading"
	StatusPaused      Status = "paused"
	StatusError       Status = "error"
	StatusOther       Status = "other"
)

// ReleaseReason 最早可释放时间的判定依据
type ReleaseReason string

const (
	ReasonMinDays ReleaseReason = "min_days" // 站点最短做种天数
	ReasonHR      ReleaseReason = "hr"       // H&R 最短做种时长
	ReasonRatio   ReleaseReason = "ratio"    // 已满足分享率目标（仅提示，不单独决定日期）
	ReasonUnknown ReleaseReason = "unknown"  // 规则缺失/无法判断
)

// DiskStat 磁盘余量（取活动下载器的保存路径）。
// 只用于显示与「该腾空间了」的提示，不触发任何删除。
type DiskStat struct {
	Path         string  `json:"path"`
	FreeBytes    int64   `json:"freeBytes"`
	TotalBytes   int64   `json:"totalBytes"`
	UsedPercent  float64 `json:"usedPercent"`
	LowSpace     bool    `json:"lowSpace"`
	TargetFreeGB float64 `json:"targetFreeGb"`
}

// ScarcityStat 稀缺汇总：UnknownCount 单列，因为「不知道」和「不稀缺」是两件事，
// 把它并进 scarce 或 abundant 都会误导。
type ScarcityStat struct {
	ScarceCount  int   `json:"scarceCount"`
	ScarceBytes  int64 `json:"scarceBytes"`
	UnknownCount int   `json:"unknownCount"`
}

// BonusCap 时魔上限：**该站**的每小时上限估算，以及它是哪一站。
//
// 各站的时魔口径互不相通（NPHP 的 B 上限、手工抄录的站点值、压根没有时魔的站），
// 所以概览不能把所有站的上限求一个最大值当「你的时魔」——那会把馒头和黄星站比出大小。
// BestSite 记下取得最大值的是哪个站，界面就说「上限最高的是 X 站」，而不是裸给一个数。
//
// Configured=false 表示**一个站都没配口径**（不是「时魔为 0」）；单站算不出时该站缺席。
// 注意「配了口径」与「有上限」是两件事：log 曲线的站（bonusCap 返回 0）没有上限，
// 但 Configured 仍为 true、TotalPerHour 照常有值——所以 PerHour=0 不能反推成「没配」。
type BonusCap struct {
	// PerHour 所有已配站里**最高的那条上限**：atan 站 = B0 + perseeding×maxseeding，
	// manual 站 = 抄录值；**log 站没有上限，不参与取最大值**（全是 log 站时为 0，
	// 此时界面只写「估 X/h」，不写「上限 0」）。
	PerHour    float64 `json:"perHour"`
	Configured bool    `json:"configured"`
	// BestSite 取得该上限的站点展示名（没有站有上限/一个站都没配时为空）
	BestSite string `json:"bestSite,omitempty"`
	// Engine 该站用的引擎（none/nexusphp/manual），界面据此说明这个数怎么来的
	Engine string `json:"engine,omitempty"`
	// ConfigCount 配了时魔口径的站数。配合概览的站点总数，界面能说
	// 「5/8 个站已配口径」，而不是含糊的一句「已配置」。
	ConfigCount int `json:"configCount"`

	// —— v3.1：上限 ≠ 实际。上面 PerHour 是「最高上限」（B0+perseeding×maxseeding），
	// 用户拿它和站点页「你当前每小时能获取 108.345」对不上号，以为面板算错了。
	// 卡片头条改用下面的实际口径，上限退到明细里。 ——
	// TotalPerHour 各配置站**实际每小时**的合计：nexusphp 站 = 本地复刻的
	// B(ΣA, 颗数)（bonusTotalFrom），manual 站 = 用户抄录值。未配口径的站不计入，
	// 也不补 0——「不知道」不能冒充「没有」。
	TotalPerHour float64 `json:"totalPerHour"`
	// Sites 每站明细：界面提示行逐站列「估 X / 上限 Y / ΣA 对账」。
	Sites []BonusSiteCard `json:"sites,omitempty"`
}

// BonusSiteCard 概览时魔卡片的单站明细。
type BonusSiteCard struct {
	Site   string `json:"site"`
	Engine string `json:"engine"`
	// PerHour 实际每小时：nexusphp = 本地复刻 B(ΣA,颗数)；manual = 抄录的站点时魔
	PerHour float64 `json:"perHour"`
	// CapPerHour nexusphp 的理论上限（B0 + perseeding×maxseeding）；manual 与 log 曲线
	// 都没有上限语义（log 的 B 跟着 A 一直涨），此时为 0，界面只写「估 X/h」
	CapPerHour float64 `json:"capPerHour,omitempty"`
	// LocalSumA / Count 本地复刻的 ΣA 与「正在做种」颗数——与站点的 (A = xx.x) 对账
	LocalSumA float64 `json:"localSumA,omitempty"`
	Count     int     `json:"count,omitempty"`
	// ObservedA 用户从站点页抄的 (A = xx.x)；0 = 没抄，无从对账
	ObservedA float64 `json:"observedA,omitempty"`
	// Verified 规则在 90 天内核对过（RuleCheckedAt）。没核对过的站，数字只当参考
	Verified bool `json:"verified"`
}

// Swarm 做种群体健康档。稀缺判据来自 ptool 的 dynamicseeding（做种人数 < 10 视为
// 亟需保种）：稀缺的种子即使做种义务到期也不建议释放——它不是「我的义务」，
// 而是「这个种可能断种」。
type Swarm string

const (
	SwarmScarce   Swarm = "scarce"   // 做种人数 < 阈值，建议继续保
	SwarmNormal   Swarm = "normal"   // 一般
	SwarmAbundant Swarm = "abundant" // 做种者充足
	SwarmUnknown  Swarm = "unknown"  // 拿不到 scrape，按最坏情况处理
)

// 种子年龄来源。时魔公式的周龄要用站点发布时间，用本地添加时间会低估。
const (
	AgeCreated = "created" // 源自种子文件创建时间（准）
	AgeClient  = "client"  // 源自本地添加时间（老种新加会低估）
)

// TorrentRef 台账/日历/同内容共用的单颗种子快照
type TorrentRef struct {
	ID         int64         `json:"id"`
	Name       string        `json:"name"`
	Hash       string        `json:"hash"`
	Site       string        `json:"site"`
	ServerName string        `json:"serverName"`
	Kind       string        `json:"kind"`
	Size       int64         `json:"size"`
	SeedDays   float64       `json:"seedDays"`
	Ratio      float64       `json:"ratio"`
	Uploaded30 int64         `json:"uploaded30d"` // 窗口内实测上传增量
	Efficiency float64       `json:"efficiency"`  // MB/GB·日
	Score      int           `json:"score"`       // 0-100 保种价值分
	LastUpload int64         `json:"lastUpload"`
	Status     Status        `json:"status"`
	Labels     []string      `json:"labels"`
	HRRisk     bool          `json:"hrRisk"`
	HRDeadline int64         `json:"hrDeadline"`
	ReleaseAt  int64         `json:"releaseAt"`
	Reason     ReleaseReason `json:"releaseReason"`
	// WindowDays uploaded30d 实际覆盖的天数（≤30）。只报实测增量，不做折算，
	// 所以就靠它说明这个数统计了几天。0 = 采样不足一天，没有可用数据。
	WindowDays int `json:"windowDays"`
	// —— v3：稀缺度 / 释放安全 / 时魔 ——
	// Seeders / Leechers 取主 tracker 的 scrape 结果；0 = 未知（私有站常不给）
	Seeders  int `json:"seeders"`
	Leechers int `json:"leechers"`
	// Scarcity 0-100，做种人数越少越高；未知按 100（最坏情况）
	Scarcity int   `json:"scarcity"`
	Swarm    Swarm `json:"swarm"`
	// SafeToRelease 义务已满足 且 swarm 不稀缺。scarce/unknown 一律 false。
	SafeToRelease bool   `json:"safeToRelease"`
	ReleaseNote   string `json:"releaseNote"`
	// AgeDays/AgeSource 种子年龄与其起点；时魔周龄取自它
	AgeDays   float64 `json:"ageDays"`
	AgeSource string  `json:"ageSource"`
	// BonusPerHour/BonusPerGb 该颗种子每小时能带来的点数，以及换算到每 GB 的效率。
	// 两条口径都可能出现（口径文本在 BonusMethod）：
	//   - nexusphp：NexusPHP 公式估的单颗**边际** A 值（校不到站方显示的总时魔 B）；
	//   - manual：用户抄录的站点时魔按本站做种体积均摊（唯一能与站方对账的口径）。
	// 未配置/该站没有时魔时为 0，界面显示「—」而不是猜。
	BonusPerHour float64 `json:"bonusPerHour"`
	BonusPerGb   float64 `json:"bonusPerGb"`
	// BonusMethod 上面两个数的口径说明（后端给人话，前端直出）；空串 = 没得算
	BonusMethod string `json:"bonusMethod,omitempty"`
	// BonusMethodKey 口径的机读键（none/nexusphp/manual），仅用于 url 状态与筛选
	BonusMethodKey string `json:"bonusMethodKey,omitempty"`
	// SavePath 保存路径（磁盘统计与「按盘腾空间」要用它聚合）
	SavePath string `json:"savePath,omitempty"`
	// ContentPath 内容在磁盘上的完整路径。同内容分组用它判断
	// 「几个种子是不是指向同一份数据」——这是「删了省不省盘」唯一可靠的依据。
	ContentPath string `json:"contentPath,omitempty"`
	// IdleDays 距最近一次上传增长的连续天数（②）。
	// 0 表示「无采样数据，不判断」，而不是「今天刚上传过」。
	IdleDays int `json:"idleDays"`
	// UploadedToday 今日相对前一自然日的上传增量；TodayQuality 说明它可不可比
	UploadedToday int64        `json:"uploadedToday"`
	TodayQuality  DeltaQuality `json:"todayQuality"`
	// InvalidSeed 站点已删种（③）：Tracker 宣告明确报「未注册/不存在」。
	// 这类种子占盘且不会再有上传，必须与「低价值」区分开。
	InvalidSeed   bool   `json:"invalidSeed"`
	InvalidReason string `json:"invalidReason,omitempty"`
	// 保种契约（①）：用户承诺保到 ContractUntil。
	ContractUntil    int64 `json:"contractUntil"`
	ContractDaysLeft int   `json:"contractDaysLeft"`
	ContractBreach   bool  `json:"contractBreach"`
	HasContract      bool  `json:"hasContract"`
}

// DeltaQuality 增量可信度（对标 SiteStatistic 的口径标注）。
// SeedArk 的累计上传来自下载器的字节级计数器，所以这里不涉及显示精度，
// 只回答「有没有可比基线」。
type DeltaQuality string

const (
	// DeltaExact 与前一自然日逐颗对比得到，可信
	DeltaExact DeltaQuality = "exact"
	// DeltaPartial 部分种子缺可比基线（新加种、当时未采到），合计偏小
	DeltaPartial DeltaQuality = "partial"
	// DeltaUnknown 没有前一自然日的采样，无法计算增量
	DeltaUnknown DeltaQuality = "unknown"
	// DeltaRollback 读数回退（计数器重置/换下载器），已按不补算处理
	DeltaRollback DeltaQuality = "rollback"
)

// SampleCoverage 采样窗口覆盖情况（②）。上传量只报实测增量、不折算，
// 所以 Days 就是「这个数统计了几天」：最早采样日到今天的天数，0 = 不足一天。
type SampleCoverage struct {
	Days       int    `json:"days"`
	From       string `json:"from"` // 窗口内最早一天（无采样时为空）
	To         string `json:"to"`
	LastSample int64  `json:"lastSample"`
	Full       bool   `json:"full"` // 已覆盖满窗口（>=29 天）
}

// Contract 保种契约（①）。纯本地承诺记录，不触发暂停/删除，也不上报站点。
type Contract struct {
	Hash      string `json:"hash"`
	Until     int64  `json:"until"` // 承诺保种到（unix 秒）
	Note      string `json:"note,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

// ContractRequest 设置契约的入参。Until 与 Days 二选一：Days>0 时按「从今天起 N 天」计算
type ContractRequest struct {
	Hash  string `json:"hash"`
	Days  int    `json:"days"`
	Until int64  `json:"until"`
	Note  string `json:"note"`
}

// SiteSize 站点维度的体积/数量统计
type SiteSize struct {
	Site  string `json:"site"`
	Count int    `json:"count"`
	Size  int64  `json:"size"`
}

// SiteStat 站点维度的完整统计（概览用）
type SiteStat struct {
	Site       string  `json:"site"`
	Count      int     `json:"count"`
	Size       int64   `json:"size"`
	Uploaded30 int64   `json:"uploaded30d"`
	Ratio      float64 `json:"ratio"`
	// 保种预算（⑦）：BudgetBytes=0 表示未设预算；OverBudget 只在设了预算且超出时为 true
	BudgetBytes int64 `json:"budgetBytes"`
	OverBudget  bool  `json:"overBudget"`
	// 今日上传增量（对标 SiteStatistic）：严格对前一自然日；TodayQuality 说明可信度
	UploadedToday int64        `json:"uploadedToday"`
	TodayQuality  DeltaQuality `json:"todayQuality"`
	// —— v3.1 时魔列：站点分布表里每站一行「实际每小时」——
	// BonusSet 区分「配了口径但算出 0」和「没配口径」：前者显示 0/h，后者显示「—」，
	// 「不知道」不能冒充「没有」。
	BonusSet        bool    `json:"bonusSet,omitempty"`
	BonusPerHour    float64 `json:"bonusPerHour,omitempty"`
	BonusCapPerHour float64 `json:"bonusCapPerHour,omitempty"`
	BonusEngine     string  `json:"bonusEngine,omitempty"`
}

// UpcomingDay 未来可释放日期（概览用）
type UpcomingDay struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
	Bytes int64  `json:"bytes"`
}

// Overview 概览页数据
type Overview struct {
	TorrentCount  int     `json:"torrentCount"`
	SeedCount     int     `json:"seedCount"`
	TotalSize     int64   `json:"totalSize"`
	Uploaded30    int64   `json:"uploaded30d"`
	AvgEfficiency float64 `json:"avgEfficiency"`
	HRRiskCount   int     `json:"hrRiskCount"`
	ReadyCount    int     `json:"readyCount"`
	ReadyBytes    int64   `json:"readyBytes"`
	VaultCount    int     `json:"vaultCount"`
	VaultBytes    int64   `json:"vaultBytes"`
	GroupCount    int     `json:"groupCount"`
	// ReclaimableBytes 同内容里**真正可省**的体积（只统计能证明是两份独立数据的组）。
	// 原来这里叫 duplicateBytes，按「组内体积 − 最大那份」算——在辅种场景下是虚数：
	// 删种子一字节都不释放，却看着像能省一大块盘。
	ReclaimableBytes int64 `json:"reclaimableBytes"`
	// SharedGroups 辅种组数（多站共用一份数据、不重复占盘）
	SharedGroups int           `json:"sharedGroups"`
	Sites        []SiteStat    `json:"sites"`
	Upcoming     []UpcomingDay `json:"upcoming"`
	GeneratedAt  int64         `json:"generatedAt"`
	// ② 采样覆盖：说明本机采了多少天（与 uploaded30d 的窗口不是一回事：
	// 全库采了 30 天，但种子可能全是刚加的，那样上传量列仍无数据）
	Coverage SampleCoverage `json:"coverage"`
	// WindowDays uploaded30d 这一列实际统计的天数 = 各行的最大值（≤30）。
	// 界面照它写「近 N 天上传」；0 表示没有任何一行有可用窗口。
	WindowDays int `json:"windowDays"`
	// —— v3 ——
	// Disk 磁盘余量快照：释放最真实的触发条件是「盘要满了」，而不是任意阈值。
	Disk DiskStat `json:"disk"`
	// Scarcity 稀缺汇总：有多少颗在做「别人不做我就得保」的活
	Scarcity ScarcityStat `json:"scarcity"`
	// BonusCap 各站时魔上限里最高的那条，以及它是哪一站（Configured=false = 一个站都没配）
	BonusCap BonusCap `json:"bonusCap"`
	// ② 僵尸种子：连续 idleThresholdDays 天没有上传增长（且确有采样数据）
	IdleCount int   `json:"idleCount"`
	IdleBytes int64 `json:"idleBytes"`
	// ③ 站点已删种
	InvalidCount int   `json:"invalidCount"`
	InvalidBytes int64 `json:"invalidBytes"`
	// ① 保种契约
	ContractCount    int   `json:"contractCount"`
	ContractBreach   int   `json:"contractBreach"`
	ContractDueCount int   `json:"contractDueCount"`
	ContractDueBytes int64 `json:"contractDueBytes"`
	// ⑦ 站点保种预算
	OverBudgetCount int   `json:"overBudgetCount"`
	OverBudgetBytes int64 `json:"overBudgetBytes"`
	// Warnings 概览级告警文案（②③⑦ 汇总），空数组而非 null
	Warnings []string `json:"warnings"`
	// 今日上传增量（严格对前一自然日）与可信度
	UploadedToday int64        `json:"uploadedToday"`
	TodayQuality  DeltaQuality `json:"todayQuality"`
	TodayNote     string       `json:"todayNote"`
}

// LedgerTotals 台账汇总
type LedgerTotals struct {
	Count         int     `json:"count"`
	Size          int64   `json:"size"`
	Uploaded30    int64   `json:"uploaded30d"`
	AvgEfficiency float64 `json:"avgEfficiency"`
	// ZeroUpload 窗口内实测为零上传的种子数；没有采样数据（WindowDays=0）的不计入，
	// 那是「还没看到」而不是「没有上传」。
	ZeroUpload int `json:"zeroUpload"`
	// WindowDays 台账这一列实际统计的天数 = 各行 windowDays 的最大值，供表头写「近 N 天」。
	// 取各行的最大值而不是全库采样跨度：种子全是刚加的时候，采样可能已经跑了 30 天，
	// 这一列却一天数据都没有，那时标题不该写「近 30 天」。
	WindowDays int `json:"windowDays"`
	// ② 连续无上传（有采样数据但窗口内没有增长）
	IdleCount int `json:"idleCount"`
	// ③ 站点已删种
	InvalidCount int `json:"invalidCount"`
	// ① 契约违约
	ContractBreach int `json:"contractBreach"`
}

// Ledger 资产台账
type Ledger struct {
	Rows   []TorrentRef `json:"rows"`
	Totals LedgerTotals `json:"totals"`
	Sites  []SiteSize   `json:"sites"`
}

// CalendarTorrent 日历当日清单条目
type CalendarTorrent struct {
	ID        int64         `json:"id"`
	Name      string        `json:"name"`
	Site      string        `json:"site"`
	Size      int64         `json:"size"`
	SeedDays  float64       `json:"seedDays"`
	HRRisk    bool          `json:"hrRisk"`
	ReleaseAt int64         `json:"releaseAt"`
	Reason    ReleaseReason `json:"reason"`
}

// CalendarDay 一个有释放计划的日期
type CalendarDay struct {
	Date     string            `json:"date"`
	Count    int               `json:"count"`
	Bytes    int64             `json:"bytes"`
	Ready    bool              `json:"ready"`
	HRRisk   int               `json:"hrRisk"`
	Torrents []CalendarTorrent `json:"torrents"`
	// ① 当日到期的保种契约数（与「可释放」是两件事：契约是承诺，可释放是站点要求）
	ContractDue int `json:"contractDue"`
}

// CalendarSummary 日历顶部汇总
type CalendarSummary struct {
	TodayBytes int64 `json:"todayBytes"`
	WeekBytes  int64 `json:"weekBytes"`
	MonthBytes int64 `json:"monthBytes"`
	HRCount    int   `json:"hrCount"`
	// ① 本月到期的契约数，以及已违约数
	ContractDue    int `json:"contractDue"`
	ContractBreach int `json:"contractBreach"`
}

// Calendar 释放日历
type Calendar struct {
	Month   string          `json:"month"`
	Days    []CalendarDay   `json:"days"`
	Unknown CountBytes      `json:"unknown"` // 规则缺失的种子（Count/Bytes）
	Summary CalendarSummary `json:"summary"`
}

// VaultFile 档案里的文件条目
type VaultFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// CountBytes 只有数量与体积的统计（日历的规则缺失桶用）
type CountBytes struct {
	Count int   `json:"count"`
	Bytes int64 `json:"bytes"`
}

// VaultEntry 档案条目（对外 DTO）。磁力、留档路径、文件列表属于内部数据，
// 由 store 的 vaultRecord 承载，详情接口单独返回 files。
type VaultEntry struct {
	ID           string   `json:"id"`
	Hash         string   `json:"hash"`
	Name         string   `json:"name"`
	Site         string   `json:"site"`
	Size         int64    `json:"size"`
	DeletedAt    int64    `json:"deletedAt"`
	SeedDays     float64  `json:"seedDays"`
	Ratio        float64  `json:"ratio"`
	UploadedEver int64    `json:"uploadedEver"`
	Labels       []string `json:"labels"`
	ServerName   string   `json:"serverName"`
	Kind         string   `json:"kind"`
	SavePath     string   `json:"savePath"`
	HasTorrent   bool     `json:"hasTorrent"`
	CanRevive    bool     `json:"canRevive"`
	// Reason 删除原因：manual（手动）/ policy（做种策略自动）。事后复盘时
	// 「这颗是被谁删的」必须能回答，否则批量还原无从下手。
	Reason string `json:"reason,omitempty"`
	// ReasonNote 补充说明，例如触发删除的规则名
	ReasonNote string `json:"reasonNote,omitempty"`
}

// VaultList 档案列表响应
type VaultList struct {
	Entries []VaultEntry `json:"entries"`
	Total   int          `json:"total"`
	Bytes   int64        `json:"bytes"`
}

// ContentCopy 同内容分组里的一个副本
type ContentCopy struct {
	ID         int64    `json:"id"`
	Site       string   `json:"site"`
	ServerName string   `json:"serverName"`
	Kind       string   `json:"kind"`
	Status     Status   `json:"status"`
	Size       int64    `json:"size"`
	Uploaded   int64    `json:"uploaded"`
	Labels     []string `json:"labels"`
}

// MatrixVerdict 这一组「到底省不省盘」的结论。
//
// 只看名称与体积只能算出一个虚的「冗余体积」：同内容在 PT 里绝大多数是**辅种**——
// 磁盘上只有一份数据、几个站的种子都指向它，删掉其中一个种子**一字节都不会释放**，
// 只会丢掉那个站的义务。所以必须先判断「副本是否指向同一份数据」（比 contentPath），
// 再决定给什么结论。
type MatrixVerdict string

const (
	// VerdictShared 全部副本指向同一份数据（辅种）：删种子不省盘
	VerdictShared MatrixVerdict = "shared"
	// VerdictDuplicate 同一台下载器内存在多份独立数据：删掉多余副本连同数据才能省
	VerdictDuplicate MatrixVerdict = "duplicate"
	// VerdictUnknown 跨下载器且路径不同：既不能证明共用，也不能证明重复
	VerdictUnknown MatrixVerdict = "unknown"
)

// ContentGroup 同一个内容在多个站/多台下载器上的副本集合
type ContentGroup struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	TotalBytes int64  `json:"totalBytes"`
	// ReclaimableBytes 真正能省下的体积。**只有 VerdictDuplicate 才非 0**：
	// 其余情形置 0 而不是给个虚数，免得用户拿它去清理。
	ReclaimableBytes int64         `json:"reclaimableBytes"`
	Verdict          MatrixVerdict `json:"verdict"`
	// Sites 这一组压着哪几个站的义务（风险视角：这份数据一旦丢，几站会同时违约）
	Sites []string `json:"sites"`
	// SameDownloader 同一台下载器里出现多个副本（真重复的线索）
	SameDownloader bool `json:"sameDownloader"`
	// MultiServer 组内副本跨多台下载器：跨机路径体系不同，未做共用/重复比较
	MultiServer bool          `json:"multiServer"`
	Copies      []ContentCopy `json:"copies"`
}

// MatrixSummary 同内容汇总
type MatrixSummary struct {
	Groups int `json:"groups"`
	// ReclaimableBytes 全部分组里真正可省的合计（只累加 VerdictDuplicate）
	ReclaimableBytes int64 `json:"reclaimableBytes"`
	// SharedGroups 辅种组数（共用一份数据、不重复占盘）
	SharedGroups int `json:"sharedGroups"`
}

// Matrix 同内容矩阵
type Matrix struct {
	Groups  []ContentGroup `json:"groups"`
	Summary MatrixSummary  `json:"summary"`
}

// PublishRequest 发种资料包的输入
type PublishRequest struct {
	Title       string   `json:"title"`
	Name        string   `json:"name"`
	Site        string   `json:"site"`
	Category    string   `json:"category"`
	SizeBytes   int64    `json:"sizeBytes"`
	MediaInfo   string   `json:"mediaInfo"`
	Screenshots []string `json:"screenshots"`
	Subtitle    string   `json:"subtitle"`
	Video       string   `json:"video"`
	Audio       string   `json:"audio"`
	Resolution  string   `json:"resolution"`
	Year        int      `json:"year"`
	Country     string   `json:"country"`
	Genre       string   `json:"genre"`
	Description string   `json:"description"`
	Lang        string   `json:"lang"` // zh / en，影响资料包内文本语言，缺省 zh
}

// PublishPreview BBCode 预览结果
type PublishPreview struct {
	BBCode   string   `json:"bbcode"`
	Warnings []string `json:"warnings"`
}

// DetectedSite 当前快照里检测到的站点（用于一键生成规则）
type DetectedSite struct {
	Site  string `json:"site"`
	Count int    `json:"count"`
}

// SitesResponse 站点规则响应
type SitesResponse struct {
	Profiles []state.SiteProfile `json:"profiles"`
	Detected []DetectedSite      `json:"detected"`
}
