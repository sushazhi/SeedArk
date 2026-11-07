package pt

import (
	"net/url"
	"strings"
	"time"

	"github.com/sushazhi/seedark/backend/internal/models"
	"github.com/sushazhi/seedark/backend/internal/state"
)

const daySeconds = int64(86400)

// siteOf 从种子的 Tracker 信息里取站点标识。
// 列表接口已带 trackerStats，无需为每颗种子单独拉详情；取不到时回退 trackers。
func siteOf(t *models.Torrent) string {
	for _, ts := range t.TrackerStats {
		if h := cleanHost(ts.Host); h != "" {
			return h
		}
		if h := hostFromAnnounce(ts.Announce); h != "" {
			return h
		}
	}
	for _, tr := range t.Trackers {
		if h := hostFromAnnounce(tr.Announce); h != "" {
			return h
		}
	}
	return ""
}

func hostFromAnnounce(announce string) string {
	u, err := url.Parse(strings.TrimSpace(announce))
	if err != nil || u.Host == "" {
		return ""
	}
	return cleanHost(u.Host)
}

func cleanHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	if i := strings.LastIndex(h, ":"); i > 0 {
		h = h[:i]
	}
	h = strings.TrimPrefix(h, "www.")
	return h
}

// matchProfile 在当前站点规则里找最匹配的一条。
// 实现已收敛到 state.MatchSiteProfile —— 做种策略走同一个函数，
// 否则两处对「这颗种子属于哪个站」会得出不同答案（日历说可释放、策略仍不达标）。
func matchProfile(profiles []state.SiteProfile, site string) (state.SiteProfile, bool) {
	return state.MatchSiteProfile(profiles, site)
}

// seedBase 做种要求的时间起点：下载完成时间；磁力/未完成时退化为添加时间
func seedBase(t *models.Torrent) int64 {
	if t.DoneDate > 0 {
		return t.DoneDate
	}
	return t.AddedDate
}

// releaseAtFor 计算最早可释放时间。两个字段都为 0 表示规则缺失 → 0/unknown。
// 依据取 minSeedDays 与 hrSeedHours 中更晚的一个（两者可能同时存在）。
func releaseAtFor(t *models.Torrent, p state.SiteProfile, hasProfile bool) (int64, ReleaseReason) {
	if !hasProfile {
		return 0, ReasonUnknown
	}
	base := seedBase(t)
	if base <= 0 {
		return 0, ReasonUnknown
	}
	var at int64
	reason := ReasonUnknown
	if p.MinSeedDays > 0 {
		at = base + int64(p.MinSeedDays*86400)
		reason = ReasonMinDays
	}
	if p.HRSeedHours > 0 {
		hr := base + int64(p.HRSeedHours*3600)
		if hr > at {
			at = hr
			reason = ReasonHR
		}
	}
	if at == 0 {
		return 0, ReasonUnknown
	}
	return at, reason
}

// hrDeadlineFor H&R 到期时间；未配置返回 0
func hrDeadlineFor(t *models.Torrent, p state.SiteProfile, hasProfile bool) int64 {
	if !hasProfile || p.HRSeedHours <= 0 {
		return 0
	}
	base := seedBase(t)
	if base <= 0 {
		return 0
	}
	return base + int64(p.HRSeedHours*3600)
}

// hrRiskOf 是否仍有 H&R 风险：已配置时长要求，且实际做种时长尚未满足
func hrRiskOf(t *models.Torrent, p state.SiteProfile, hasProfile bool) bool {
	if !hasProfile || p.HRSeedHours <= 0 {
		return false
	}
	return float64(t.SecondsSeeding) < p.HRSeedHours*3600
}

// statusOf 粗粒度状态。Transmission 的状态码里 0=停止、4=下载、6=做种；
// qBittorrent 由驱动映射到同一套语义，因此这里按「进度 + 状态码 + 做种时长」
// 三者兼容判断，而不是硬编码某一家的枚举。
func statusOf(t *models.Torrent) Status {
	if t.Error != 0 {
		return StatusError
	}
	if t.PercentDone < 1 {
		if t.Status == 0 {
			return StatusPaused
		}
		return StatusDownloading
	}
	if t.Status == 0 {
		return StatusPaused
	}
	if t.SecondsSeeding > 0 || t.RateUpload > 0 {
		return StatusSeeding
	}
	return StatusOther
}

// normalizeName 归一化名称用于同内容分组：去掉扩展名、下划线/点号等分隔，
// 折叠空白。保留分辨率与编码，避免把 1080p 与 2160p 误判成同一份内容。
func normalizeName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	for _, ext := range []string{".mkv", ".mp4", ".ts", ".iso", ".m2ts", ".avi", ".torrent"} {
		s = strings.TrimSuffix(s, ext)
	}
	repl := strings.NewReplacer(".", " ", "_", " ", "-", " ", "[", " ", "]", " ", "(", " ", ")", " ", "{", " ", "}", " ")
	s = repl.Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// efficiencyOf 上传效率（MB/GB·日）：窗口内上传字节 ÷ 体积(GB) ÷ 天数，
// 再换算成 MB。体积为 0 时返回 0（无处可上传，效率无意义）。
func efficiencyOf(uploadedBytes, sizeBytes int64, days float64) float64 {
	if uploadedBytes <= 0 || sizeBytes <= 0 || days <= 0 {
		return 0
	}
	sizeGB := float64(sizeBytes) / float64(1<<30)
	uploadedMB := float64(uploadedBytes) / float64(1<<20)
	return uploadedMB / sizeGB / days
}

// scoreOf 保种价值分 0-100：分享率与上传效率各占一半，各自饱和到目标值。
// 分享率 3.0、效率 60 MB/GB·日 视为满分；这是相对排序用的启发式，不是站点评分。
func scoreOf(ratio, eff float64) int {
	r := ratio / 3.0
	if r > 1 {
		r = 1
	}
	if r < 0 {
		r = 0
	}
	e := eff / 60.0
	if e > 1 {
		e = 1
	}
	if e < 0 {
		e = 0
	}
	return int(50*r + 50*e + 0.5)
}

// windowUpload 返回窗口内的上传量与这个量实际覆盖的天数。
//
// 只报实测：拿采样里该种子最早一天的上传高水位当基线，今天的累计上传减去基线，
// 就是这段窗口里真实上传的字节数。**不折算、不外推**——采样只有 5 天就只报这 5 天，
// 由界面把天数标出来，而不是把 5 天的量乘成「30 天」。
//
// days=0 表示还没有一个完整自然日的采样（首次运行当天、或这颗种子刚进台账）：
// 此时上传量无意义（增量恒为 0，却是「刚开始看」而不是「没有上传」），
// 调用方要显示「—」而不是 0。计数器回退（重置/换下载器）同样按无数据处理。
func (s *Service) windowUpload(t *models.Torrent, now time.Time) (up int64, days int) {
	if t.HashString == "" {
		return 0, 0
	}
	base, at, ok := s.local.baseline30(t.HashString, now)
	if !ok || t.UploadedEver < base {
		return 0, 0
	}
	days = int(now.Sub(at).Hours() / 24)
	if days < 1 {
		return 0, 0
	}
	if days > 30 {
		days = 30
	}
	return t.UploadedEver - base, days
}

// invalidAnnouncePatterns 「站点已删种」的宣告原文特征。
//
// 各站措辞不统一（Transmission 常见 "Torrent not registered with this tracker"，
// qBittorrent 转发站点原文），因此只匹配明确的「未注册/不存在」措辞，
// 并且要求宣告确实是失败的、且不是超时——超时属于网络问题，
// 把它算成「被删种」会误导用户去删掉一颗本来好好的种子。
var invalidAnnouncePatterns = []string{
	"torrent not registered",
	"not registered with this tracker",
	"unregistered torrent",
	"torrent not found",
	"unknown torrent",
	"torrent is not registered",
	"种子未注册",
	"未注册",
	"种子不存在",
}

// invalidSeedOf 判断是否是「站点已删种」的无效做种，返回原因原文（供界面展示）。
//
// 防误判护栏（照 MoviePilot CleanInvalidSeed 的做法）：只有当该站点下**还有别的
// 种子宣告成功**时才敢判定。站点临时关站、换域名、被墙时，全站种子都会一起报错，
// 那种情况是「站点不可达」，不是「种子被删」——把它算成无效做种会诱导用户删掉
// 本来好好的种子。宁可漏报，不可误报。
func invalidSeedOf(t *models.Torrent, cc *calcCtx) (bool, string) {
	if cc == nil {
		return false, ""
	}
	site := siteOf(t)
	if site != "" && !cc.hostHealthy[site] {
		return false, ""
	}
	for _, ts := range t.TrackerStats {
		if ts.LastAnnounceSucceeded || ts.LastAnnounceTimedOut {
			continue
		}
		raw := strings.TrimSpace(ts.LastAnnounceResult)
		if raw == "" {
			continue
		}
		low := strings.ToLower(raw)
		for _, p := range invalidAnnouncePatterns {
			if strings.Contains(low, strings.ToLower(p)) {
				return true, raw
			}
		}
	}
	return false, ""
}

// idleThresholdDays 概览里判为「僵尸种子」的连续静默天数门槛（②）
const idleThresholdDays = 7

// calcCtx 一次快照内的公共计算上下文：契约表、采样活跃度、覆盖率。
// 这些数据每颗种子都要用，逐颗现取会把 O(种子数 × 天数) 变成 O(种子数²)。
type calcCtx struct {
	now       time.Time
	contracts map[string]Contract
	lastInc   map[string]string
	seen      map[string]bool
	coverage  SampleCoverage
	// hostHealthy 站点下是否有至少一颗种子宣告成功（③ 的防误判护栏）
	hostHealthy map[string]bool
	// today 今日相对前一自然日的上传增量与可比性
	today TodayDelta
	// bonus 按展示名预计算好的时魔集合量（ΣA/颗数/做种体积），由 buildRefs 在
	// 逐行计算前填入。概览的「实际每小时」卡片直接读它，不再按行反推——
	// 集合量必须来自同一遍遍历，散在行里现算就会前后不一致。
	bonus map[string]bonusCtx
	// siteNames 种子 ID → 该种子的站点名数组，取自 manager.GetTorrentSites。
	// 与侧栏「站点」筛选、自动化「分组限速」同源：三处的站点名必须字面一致，
	// 否则用户在同一颗种子上会看到两个不同的站名，没法对齐。
	// 取不到（RPC 失败）时为 nil，此时退回 siteOf 的 tracker 主机名。
	siteNames map[int64][]string
	// scarceSeeders 稀缺判据阈值（做种人数低于它视为稀缺），来自 PT 设置
	scarceSeeders int
}

// displaySite 站点展示名。
//
// 名字取自与侧栏「站点」筛选、自动化「分组限速」同源的映射（manager.GetTorrentSites），
// 保证三处字面一致——用户在分组限速里挑的那个站名，在保种台里就是同一个字符串。
//
// 但「名字来源」与「归属给哪个站」是两件事：同一颗种子常同时挂一个公开 tracker，
// 若直接取列表第一个，义务就会被算到公开 tracker 上（那里没有保种义务）。
// 因此归属优先级为：① 命中站点规则的那个 → ② 正在生效的 tracker（trackerStats 主机名）
// → ③ 映射里的第一个。映射整体取不到时退回 siteOf。
func displaySite(t *models.Torrent, cc *calcCtx, profiles []state.SiteProfile) (string, state.SiteProfile, bool) {
	names := cc.siteNames[t.ID]
	if len(names) > 0 {
		for _, n := range names {
			if p, ok := matchProfile(profiles, n); ok {
				return p.Name, p, true
			}
		}
		// ② 优先认「实际在宣告的那个 tracker」，它才代表这颗种子真正记在哪个站
		if active := siteOf(t); active != "" {
			for _, n := range names {
				if strings.EqualFold(strings.TrimSpace(n), active) {
					return n, state.SiteProfile{}, false
				}
			}
		}
		for _, n := range names {
			if s := strings.TrimSpace(n); s != "" {
				return s, state.SiteProfile{}, false
			}
		}
	}
	site := siteOf(t)
	p, ok := matchProfile(profiles, site)
	if ok && p.Name != "" {
		return p.Name, p, true
	}
	return site, p, ok
}

// todayQualityOf 单颗种子的今日增量可信度
func todayQualityOf(hash string, cc *calcCtx) DeltaQuality {
	if cc == nil || hash == "" {
		return DeltaUnknown
	}
	if _, ok := cc.today.Known[hash]; ok {
		return DeltaExact
	}
	if cc.today.Rollback[hash] {
		return DeltaRollback
	}
	return DeltaUnknown
}

// mergeTodayQuality 把若干颗种子的可信度收敛成一个站点/总量级的口径：
// 全部可比才算 exact；有可比也有不可比算 partial；全部不可比时区分回退与缺基线。
func mergeTodayQuality(qs []DeltaQuality) DeltaQuality {
	if len(qs) == 0 {
		return DeltaUnknown
	}
	exact, unknown, rollback := 0, 0, 0
	for _, q := range qs {
		switch q {
		case DeltaExact:
			exact++
		case DeltaRollback:
			rollback++
		default:
			unknown++
		}
	}
	switch {
	case exact > 0 && unknown == 0 && rollback == 0:
		return DeltaExact
	case exact > 0:
		return DeltaPartial
	case rollback > 0:
		return DeltaRollback
	default:
		return DeltaUnknown
	}
}

// idleDaysOf 连续无上传天数。没有采样记录时返回 0（不判断），
// 而不是把新种子误报成僵尸。
func idleDaysOf(hash string, cc *calcCtx) int {
	if cc == nil || hash == "" || !cc.seen[hash] {
		return 0
	}
	day, ok := cc.lastInc[hash]
	if !ok {
		// 窗口内出现过、但从未增长 → 整个窗口都算静默
		return cc.coverage.Days
	}
	d, err := time.ParseInLocation("2006-01-02", day, cc.now.Location())
	if err != nil {
		return 0
	}
	n := int(cc.now.Sub(d).Hours() / 24)
	if n < 0 {
		return 0
	}
	return n
}

// contractOf 取该 hash 的契约状态（①）
func contractOf(hash string, cc *calcCtx) (until int64, daysLeft int, breach, has bool) {
	if cc == nil || hash == "" {
		return 0, 0, false, false
	}
	c, ok := cc.contracts[hash]
	if !ok || c.Until <= 0 {
		return 0, 0, false, false
	}
	left := int((c.Until - cc.now.Unix()) / 86400)
	return c.Until, left, cc.now.Unix() > c.Until, true
}

// refOf 把一颗种子 + 当前站点规则快照成台账行。
// 站点名走 displaySite：优先与侧栏/分组限速同源的映射，保证三处字面一致。
// bonusBySite 是同一份快照预计算好的每站集合量（ΣA/颗数/做种体积），
// 由 buildRefs 传入——单颗的时魔差值必须除以「全集」，不能在行内现算。
func (s *Service) refOf(t *models.Torrent, profiles []state.SiteProfile, cc *calcCtx, bonusBySite map[string]bonusCtx) TorrentRef {
	now := cc.now
	display, p, hasProfile := displaySite(t, cc, profiles)
	up, days := s.windowUpload(t, now)
	seedDays := float64(t.SecondsSeeding) / 86400
	if seedDays < 1 && t.DoneDate > 0 {
		seedDays = float64(now.Unix()-t.DoneDate) / 86400
	}
	if seedDays < 0 {
		seedDays = 0
	}
	// 效率按实际覆盖天数算，而不是固定 30 天：窗口只有 5 天时拿 30 当分母会把效率压低 6 倍
	eff := efficiencyOf(up, t.TotalSize, float64(days))
	rel, reason := releaseAtFor(t, p, hasProfile)
	invalid, invalidReason := invalidSeedOf(t, cc)
	cUntil, cLeft, cBreach, hasC := contractOf(t.HashString, cc)

	// —— v3：稀缺度 / 年龄 / 时魔 / 释放安全 ——
	seeders, leechers := swarmCounts(t)
	threshold := cc.scarceSeeders
	swarm := swarmOf(seeders, threshold)
	ageDays, ageSource := ageOf(t, now)
	// 时魔按引擎分派：nexusphp 档给「删掉这颗损失多少」的精确差值（需要本站 ΣA/颗数），
	// manual 档把抄录的站点时魔摊到体积，none 不显示。口径文案一并带回：
	// 同一列「时魔·GB」在不同站是不同东西，必须写清是哪一种。
	bonusHour, bonusGb, bonusMethod, bonusKey := bonusOf(t, p, ageDays, seeders, bonusBySite[display])
	// 义务是否满足：与释放日历同一判据（规则里的早晚两项都过），
	// 外加 H&R 豁免分享率（上传量 ÷ 种子体积）
	met := obligationMet(t, p, hasProfile)
	safe, note := releaseSafety(met, swarm, seeders, hasProfile)
	// 保种契约未到期的一律不建议释放：用户自己承诺过的事优先于任何估算
	if hasC && cLeft > 0 {
		safe = false
		note = "保种契约还剩 " + itoa(cLeft) + " 天"
	}

	return TorrentRef{
		ID:               t.ID,
		Name:             t.Name,
		Hash:             t.HashString,
		Site:             display,
		ServerName:       t.ServerName,
		Kind:             t.Kind,
		Size:             t.TotalSize,
		SeedDays:         seedDays,
		Ratio:            t.UploadRatio,
		Uploaded30:       up,
		Efficiency:       eff,
		Score:            scoreOf(t.UploadRatio, eff),
		LastUpload:       t.ActivityDate,
		Status:           statusOf(t),
		Labels:           nonNilLabels(t.Labels),
		HRRisk:           hrRiskOf(t, p, hasProfile),
		HRDeadline:       hrDeadlineFor(t, p, hasProfile),
		ReleaseAt:        rel,
		Reason:           reason,
		WindowDays:       days,
		Seeders:          seeders,
		Leechers:         leechers,
		Scarcity:         scarcityOf(seeders),
		Swarm:            swarm,
		SafeToRelease:    safe,
		ReleaseNote:      note,
		AgeDays:          ageDays,
		AgeSource:        ageSource,
		BonusPerHour:     bonusHour,
		BonusPerGb:       bonusGb,
		BonusMethod:      bonusMethod,
		BonusMethodKey:   bonusKey,
		SavePath:         savePathOf(t),
		ContentPath:      t.ContentPath,
		IdleDays:         idleDaysOf(t.HashString, cc),
		UploadedToday:    cc.today.Known[t.HashString],
		TodayQuality:     todayQualityOf(t.HashString, cc),
		InvalidSeed:      invalid,
		InvalidReason:    invalidReason,
		ContractUntil:    cUntil,
		ContractDaysLeft: cLeft,
		ContractBreach:   cBreach,
		HasContract:      hasC,
	}
}

func nonNilLabels(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}
