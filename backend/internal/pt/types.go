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
	Uploaded30 int64         `json:"uploaded30d"`
	Efficiency float64       `json:"efficiency"` // MB/GB·日
	Score      int           `json:"score"`      // 0-100 保种价值分
	LastUpload int64         `json:"lastUpload"`
	Status     Status        `json:"status"`
	Labels     []string      `json:"labels"`
	HRRisk     bool          `json:"hrRisk"`
	HRDeadline int64         `json:"hrDeadline"`
	ReleaseAt  int64         `json:"releaseAt"`
	Reason     ReleaseReason `json:"releaseReason"`
	Estimated  bool          `json:"estimated"` // uploaded30d 是否为折算估算
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

// SampleCoverage 采样窗口覆盖情况（②）。界面据此说明「≈」是怎么来的：
// Days 不足 30 时 uploaded30d 是折算值，而不是真实 30 天增量。
type SampleCoverage struct {
	Days       int    `json:"days"`
	From       string `json:"from"` // 窗口内最早一天（无采样时为空）
	To         string `json:"to"`
	LastSample int64  `json:"lastSample"`
	Full       bool   `json:"full"` // 已覆盖满窗口（>=29 天），uploaded30d 可信
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
}

// UpcomingDay 未来可释放日期（概览用）
type UpcomingDay struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
	Bytes int64  `json:"bytes"`
}

// Overview 概览页数据
type Overview struct {
	TorrentCount   int           `json:"torrentCount"`
	SeedCount      int           `json:"seedCount"`
	TotalSize      int64         `json:"totalSize"`
	Uploaded30     int64         `json:"uploaded30d"`
	AvgEfficiency  float64       `json:"avgEfficiency"`
	HRRiskCount    int           `json:"hrRiskCount"`
	ReadyCount     int           `json:"readyCount"`
	ReadyBytes     int64         `json:"readyBytes"`
	VaultCount     int           `json:"vaultCount"`
	VaultBytes     int64         `json:"vaultBytes"`
	GroupCount     int           `json:"groupCount"`
	DuplicateBytes int64         `json:"duplicateBytes"`
	Sites          []SiteStat    `json:"sites"`
	Upcoming       []UpcomingDay `json:"upcoming"`
	GeneratedAt    int64         `json:"generatedAt"`
	// ② 采样覆盖：解释 uploaded30d 的可信度
	Coverage SampleCoverage `json:"coverage"`
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
	ZeroUpload    int     `json:"zeroUpload"`
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

// ContentGroup 同一个内容在多个站/多台下载器上的副本集合
type ContentGroup struct {
	Key            string        `json:"key"`
	Name           string        `json:"name"`
	Size           int64         `json:"size"`
	TotalBytes     int64         `json:"totalBytes"`
	DuplicateBytes int64         `json:"duplicateBytes"`
	SameDownloader bool          `json:"sameDownloader"`
	Copies         []ContentCopy `json:"copies"`
}

// MatrixSummary 同内容汇总
type MatrixSummary struct {
	Groups         int   `json:"groups"`
	DuplicateBytes int64 `json:"duplicateBytes"`
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
