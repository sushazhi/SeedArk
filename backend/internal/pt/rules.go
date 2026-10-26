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

// window30 返回近 30 天上传与是否为估算。
//
// 采样基线只在真正覆盖到 30 天窗口时才当作「真实值」：首次运行当天，基线就是本次
// 采样本身，增量恒为 0，会把刚上传过的种子误报成零上传（正是要避免的误导）。
// 因此覆盖不足 30 天时按覆盖天数把增量折算到 30 天，并标记估算；折算后仍为 0
// （例如刚开始采样且确实没有新增上传）就退回「累计上传 ÷ 做种天数 × 30」，
// 宁可给带 ≈ 的估算，也不显示一个不带标注的 0。
func (s *Service) window30(t *models.Torrent, now time.Time) (int64, bool) {
	seedDays := float64(t.SecondsSeeding) / 86400
	if seedDays < 1 && t.DoneDate > 0 {
		seedDays = float64(now.Unix()-t.DoneDate) / 86400
	}
	if base, at, ok := s.local.baseline30(t.HashString, now); ok && t.UploadedEver >= base {
		delta := t.UploadedEver - base
		coverage := now.Sub(at).Hours() / 24
		if coverage >= 29 {
			return delta, false
		}
		if coverage < 1 {
			coverage = 1
		}
		est := float64(delta) / coverage * 30
		if est > float64(t.UploadedEver) {
			est = float64(t.UploadedEver)
		}
		if est > 0 {
			return int64(est), true
		}
	}
	// 两种情况都落到累计折算：①基线高于累计（计数器被重置、采样错位）；
	// ②覆盖不足且增量确为 0。都不能拿「0」当事实断言，只能给带 ≈ 的估算。
	if t.UploadedEver <= 0 {
		return 0, false
	}
	if seedDays < 1 {
		// 刚开始做种：累计上传就是目前全部，折算成 30 天会严重高估，直接按原值
		return t.UploadedEver, true
	}
	est := float64(t.UploadedEver) / seedDays * 30
	if est > float64(t.UploadedEver) && seedDays < 30 {
		// 做种不足 30 天时，上限是「累计上传」，不能再往上折算
		est = float64(t.UploadedEver)
	}
	return int64(est), true
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
	// siteNames 种子 ID → 该种子的站点名数组，取自 manager.GetTorrentSites。
	// 与侧栏「站点」筛选、自动化「分组限速」同源：三处的站点名必须字面一致，
	// 否则用户在同一颗种子上会看到两个不同的站名，没法对齐。
	// 取不到（RPC 失败）时为 nil，此时退回 siteOf 的 tracker 主机名。
	siteNames map[int64][]string
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
func (s *Service) refOf(t *models.Torrent, profiles []state.SiteProfile, cc *calcCtx) TorrentRef {
	now := cc.now
	display, p, hasProfile := displaySite(t, cc, profiles)
	up30, estimated := s.window30(t, now)
	seedDays := float64(t.SecondsSeeding) / 86400
	if seedDays < 1 && t.DoneDate > 0 {
		seedDays = float64(now.Unix()-t.DoneDate) / 86400
	}
	if seedDays < 0 {
		seedDays = 0
	}
	eff := efficiencyOf(up30, t.TotalSize, 30)
	rel, reason := releaseAtFor(t, p, hasProfile)
	invalid, invalidReason := invalidSeedOf(t, cc)
	cUntil, cLeft, cBreach, hasC := contractOf(t.HashString, cc)
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
		Uploaded30:       up30,
		Efficiency:       eff,
		Score:            scoreOf(t.UploadRatio, eff),
		LastUpload:       t.ActivityDate,
		Status:           statusOf(t),
		Labels:           nonNilLabels(t.Labels),
		HRRisk:           hrRiskOf(t, p, hasProfile),
		HRDeadline:       hrDeadlineFor(t, p, hasProfile),
		ReleaseAt:        rel,
		Reason:           reason,
		Estimated:        estimated,
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
