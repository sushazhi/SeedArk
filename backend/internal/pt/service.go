package pt

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sushazhi/seedark/backend/internal/models"
	"github.com/sushazhi/seedark/backend/internal/rpc"
	"github.com/sushazhi/seedark/backend/internal/state"
)

// Service PT 中心数据服务。读下载器全量种子，结合站点规则做估算；
// 采样与档案落在 DataDir/pt 下。
type Service struct {
	manager *rpc.Manager
	store   *state.Store
	local   *store

	sampleMu sync.Mutex

	// 站点名映射缓存（与分组限速同源、同 TTL）
	siteMu    sync.Mutex
	siteNames map[int64][]string
	siteAt    time.Time
}

// New 创建 PT 服务
func New(manager *rpc.Manager, st *state.Store, dataDir string) (*Service, error) {
	ls, err := newStore(dataDir)
	if err != nil {
		return nil, err
	}
	return &Service{manager: manager, store: st, local: ls}, nil
}

// Run 后台循环：每 60s 采样一次上传高水位。采样失败只记日志，不影响面板。
func (s *Service) Run(ctx context.Context) {
	slog.Info("PT 中心服务已启动")
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		_, _ = s.Sample(ctx)
		select {
		case <-ctx.Done():
			s.local.flush()
			return
		case <-ticker.C:
		}
	}
}

// Sample 采一次快照，返回本次采到的种子数（recordSample 的高水位更新数只用于内部节流）
func (s *Service) Sample(ctx context.Context) (int, error) {
	s.sampleMu.Lock()
	defer s.sampleMu.Unlock()
	torrents, err := s.manager.GetTorrents(ctx)
	if err != nil {
		return 0, err
	}
	ups := make(map[string]int64, len(torrents))
	for _, t := range torrents {
		if t == nil || t.HashString == "" {
			continue
		}
		ups[t.HashString] = t.UploadedEver
	}
	// 返回值语义要与契约一致：count 是「采到的种子数」，不是「上传量发生变化的种子数」，
	// 否则同一天重复采样会返回 0，看起来像采样失败。
	s.local.recordSample(time.Now(), ups)
	return len(ups), nil
}

// snapshot 一次读取：种子列表（带缓存）+ 站点规则
func (s *Service) snapshot(ctx context.Context) ([]*models.Torrent, []state.SiteProfile, time.Time, error) {
	torrents, err := s.manager.GetTorrents(ctx)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	st := s.store.Get()
	now := time.Now()
	return torrents, st.SiteProfiles, now, nil
}

// calc 组装一次快照的公共计算上下文（契约 + 采样活跃度 + 覆盖率 + 站点健康 + 站点名映射）
func (s *Service) calc(ctx context.Context, now time.Time, torrents []*models.Torrent) *calcCtx {
	lastInc, seen := s.local.lastIncreaseDays(now)
	// hostHealthy：某站点下只要有任意一颗种子宣告成功，就认为该站点可达。
	// 用于 ③ 的防误判护栏（站点临时关站时不要判成「种子被删」）。
	hostHealthy := map[string]bool{}
	for _, t := range torrents {
		if t == nil {
			continue
		}
		for _, ts := range t.TrackerStats {
			if !ts.LastAnnounceSucceeded {
				continue
			}
			if h := cleanHost(ts.Host); h != "" {
				hostHealthy[h] = true
			} else if h := hostFromAnnounce(ts.Announce); h != "" {
				hostHealthy[h] = true
			}
		}
	}
	return &calcCtx{
		now:         now,
		contracts:   s.local.contractAll(),
		lastInc:     lastInc,
		seen:        seen,
		coverage:    s.local.coverage(now),
		hostHealthy: hostHealthy,
		today:       s.local.todayDelta(now),
		siteNames:   s.siteMap(ctx),
	}
}

// buildRefs 计算全部台账行（概览/日历/台账/同内容共用同一份计算）。
// 同时返回该次快照的计算上下文，避免概览为了拿覆盖率再算一遍。
func (s *Service) buildRefs(ctx context.Context) ([]TorrentRef, []state.SiteProfile, time.Time, *calcCtx, error) {
	torrents, profiles, now, err := s.snapshot(ctx)
	if err != nil {
		return nil, nil, time.Time{}, nil, err
	}
	cc := s.calc(ctx, now, torrents)
	refs := make([]TorrentRef, 0, len(torrents))
	for _, t := range torrents {
		if t == nil {
			continue
		}
		refs = append(refs, s.refOf(t, profiles, cc))
	}
	return refs, profiles, now, cc, nil
}

// siteCacheTTL 站点名映射的缓存时长。与自动化「分组限速」用同一个 TTL：
// GetTorrentSites 是一次全量 RPC（只取 id+trackers），不能每次刷新都打一遍。
const siteCacheTTL = 60 * time.Second

// siteMap 种子 ID → 站点名数组，与侧栏「站点」筛选、分组限速同源。
// RPC 失败时返回 nil（调用方退回 tracker 主机名），不把失败当成「没有站点」。
func (s *Service) siteMap(ctx context.Context) map[int64][]string {
	s.siteMu.Lock()
	defer s.siteMu.Unlock()
	if s.siteNames != nil && time.Since(s.siteAt) < siteCacheTTL {
		return s.siteNames
	}
	m, err := s.manager.GetTorrentSites(ctx)
	if err != nil {
		slog.Warn("保种台：获取站点名失败，本轮按 tracker 主机名归属", "err", err)
		return nil
	}
	s.siteNames = m
	s.siteAt = time.Now()
	return m
}

func nowUnix() int64 { return time.Now().Unix() }

// Overview 概览
func (s *Service) Overview(ctx context.Context) (*Overview, error) {
	refs, profiles, now, cc, err := s.buildRefs(ctx)
	if err != nil {
		return nil, err
	}
	o := &Overview{
		GeneratedAt: now.Unix(),
		Sites:       []SiteStat{},
		Upcoming:    []UpcomingDay{},
		Coverage:    cc.coverage,
		Warnings:    []string{},
	}
	// 站点预算按展示名索引（有规则时 ref.Site 就是规则名）
	budgetByName := map[string]int64{}
	for _, p := range profiles {
		if p.Enabled && p.BudgetGB > 0 {
			budgetByName[p.Name] = int64(p.BudgetGB * float64(1<<30))
		}
	}
	var effSum float64
	var effN int
	bySite := map[string]*SiteStat{}
	upcoming := map[string]*UpcomingDay{}
	siteQual := map[string][]DeltaQuality{}
	var allQual []DeltaQuality
	for _, r := range refs {
		o.TorrentCount++
		o.TotalSize += r.Size
		o.Uploaded30 += r.Uploaded30
		if r.Status == StatusSeeding {
			o.SeedCount++
		}
		if r.HRRisk {
			o.HRRiskCount++
		}
		// ② 连续无上传（有采样数据却一直没增长）
		if r.IdleDays >= idleThresholdDays {
			o.IdleCount++
			o.IdleBytes += r.Size
		}
		// ③ 站点已删种
		if r.InvalidSeed {
			o.InvalidCount++
			o.InvalidBytes += r.Size
		}
		// ① 保种契约
		if r.HasContract {
			o.ContractCount++
			if r.ContractBreach {
				o.ContractBreach++
			} else if r.ContractDaysLeft <= 7 {
				o.ContractDueCount++
				o.ContractDueBytes += r.Size
			}
		}
		if r.ReleaseAt > 0 {
			if r.ReleaseAt <= now.Unix() {
				o.ReadyCount++
				o.ReadyBytes += r.Size
			} else {
				key := time.Unix(r.ReleaseAt, 0).Format("2006-01-02")
				if d, ok := upcoming[key]; ok {
					d.Count++
					d.Bytes += r.Size
				} else {
					upcoming[key] = &UpcomingDay{Date: key, Count: 1, Bytes: r.Size}
				}
			}
		}
		if r.Size > 0 {
			effSum += r.Efficiency
			effN++
		}
		site := r.Site
		if site == "" {
			site = "未识别"
		}
		st, ok := bySite[site]
		if !ok {
			st = &SiteStat{Site: site, BudgetBytes: budgetByName[site]}
			bySite[site] = st
		}
		st.Count++
		st.Size += r.Size
		st.Uploaded30 += r.Uploaded30
		st.UploadedToday += r.UploadedToday
		siteQual[site] = append(siteQual[site], r.TodayQuality)
		allQual = append(allQual, r.TodayQuality)
	}
	if effN > 0 {
		o.AvgEfficiency = round1(effSum / float64(effN))
	}
	for _, st := range bySite {
		if st.Size > 0 {
			// 站点分享率用该站总体积与总上传近似（不是站方数据）
			up := st.Uploaded30
			st.Ratio = round2(float64(up) / float64(st.Size))
		}
		// ⑦ 超预算只标记，不触发任何清理
		if st.BudgetBytes > 0 && st.Size > st.BudgetBytes {
			st.OverBudget = true
			o.OverBudgetCount++
			o.OverBudgetBytes += st.Size - st.BudgetBytes
		}
		st.TodayQuality = mergeTodayQuality(siteQual[st.Site])
		o.Sites = append(o.Sites, *st)
	}
	sort.SliceStable(o.Sites, func(i, j int) bool { return o.Sites[i].Size > o.Sites[j].Size })
	// 未来 14 天
	cutoff := now.AddDate(0, 0, 14).Format("2006-01-02")
	for k, v := range upcoming {
		if k <= cutoff {
			o.Upcoming = append(o.Upcoming, *v)
		}
	}
	sort.SliceStable(o.Upcoming, func(i, j int) bool { return o.Upcoming[i].Date < o.Upcoming[j].Date })

	o.VaultCount, o.VaultBytes = s.local.vaultStats()
	m, err := s.Matrix(ctx)
	if err == nil && m != nil {
		o.GroupCount = m.Summary.Groups
		o.DuplicateBytes = m.Summary.DuplicateBytes
	}
	o.Warnings = overviewWarnings(o)
	// 今日增量口径：只在有前一日基线时才算，且明确标注可比性
	o.UploadedToday = cc.today.Total
	o.TodayQuality = mergeTodayQuality(allQual)
	o.TodayNote = todayNote(cc.today, o.TodayQuality)
	return o, nil
}

// todayNote 今日增量的口径说明。增量不可比时必须说清楚原因，
// 而不是给一个看起来像「今天没上传」的 0。
func todayNote(t TodayDelta, q DeltaQuality) string {
	switch q {
	case DeltaExact:
		return fmt.Sprintf("与 %s 逐颗对比得到", t.PrevDay)
	case DeltaPartial:
		return fmt.Sprintf("相对 %s：部分种子当日新加或未被采到，合计偏小", t.PrevDay)
	case DeltaRollback:
		return "计数器读数回退（重置或更换下载器），本次不计算增量"
	default:
		if !t.HasPrev {
			return fmt.Sprintf("缺少 %s 的采样，暂无法计算增量", t.PrevDay)
		}
		return "暂无可比基线，无法计算增量"
	}
}

// overviewWarnings 概览级告警（②③⑦）。空数组而非 null，前端直接 .map。
func overviewWarnings(o *Overview) []string {
	out := []string{}
	if o.Coverage.Days < 29 {
		from := o.Coverage.From
		if from == "" {
			from = "尚未开始采样"
		}
		out = append(out, fmt.Sprintf("采样仅覆盖 %d 天（%s 起），近 30 天上传为折算估算值", o.Coverage.Days, from))
	}
	if o.InvalidCount > 0 {
		out = append(out, fmt.Sprintf("%d 颗种子的站点已判为未注册（无效做种），占 %s", o.InvalidCount, humanGB(o.InvalidBytes)))
	}
	if o.IdleCount > 0 {
		out = append(out, fmt.Sprintf("%d 颗种子连续 %d 天以上没有上传增长，占 %s", o.IdleCount, idleThresholdDays, humanGB(o.IdleBytes)))
	}
	if o.ContractBreach > 0 {
		out = append(out, fmt.Sprintf("%d 颗种子的保种契约已过期未解除", o.ContractBreach))
	}
	if o.OverBudgetCount > 0 {
		out = append(out, fmt.Sprintf("%d 个站点超出保种预算，合计超出 %s", o.OverBudgetCount, humanGB(o.OverBudgetBytes)))
	}
	return out
}

// Ledger 资产台账
func (s *Service) Ledger(ctx context.Context) (*Ledger, error) {
	refs, _, _, _, err := s.buildRefs(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].Score != refs[j].Score {
			return refs[i].Score > refs[j].Score
		}
		return refs[i].Size > refs[j].Size
	})
	l := &Ledger{Rows: refs, Sites: []SiteSize{}}
	var effSum float64
	var effN int
	bySite := map[string]*SiteSize{}
	for _, r := range refs {
		l.Totals.Count++
		l.Totals.Size += r.Size
		l.Totals.Uploaded30 += r.Uploaded30
		if r.Uploaded30 <= 0 {
			l.Totals.ZeroUpload++
		}
		if r.IdleDays >= idleThresholdDays {
			l.Totals.IdleCount++
		}
		if r.InvalidSeed {
			l.Totals.InvalidCount++
		}
		if r.ContractBreach {
			l.Totals.ContractBreach++
		}
		if r.Size > 0 {
			effSum += r.Efficiency
			effN++
		}
		site := r.Site
		if site == "" {
			site = "未识别"
		}
		ss, ok := bySite[site]
		if !ok {
			ss = &SiteSize{Site: site}
			bySite[site] = ss
		}
		ss.Count++
		ss.Size += r.Size
	}
	if effN > 0 {
		l.Totals.AvgEfficiency = round1(effSum / float64(effN))
	}
	for _, ss := range bySite {
		l.Sites = append(l.Sites, *ss)
	}
	sort.SliceStable(l.Sites, func(i, j int) bool { return l.Sites[i].Size > l.Sites[j].Size })
	if l.Rows == nil {
		l.Rows = []TorrentRef{}
	}
	return l, nil
}

// Calendar 释放日历
func (s *Service) Calendar(ctx context.Context, month string) (*Calendar, error) {
	refs, _, now, _, err := s.buildRefs(ctx)
	if err != nil {
		return nil, err
	}
	if month == "" {
		month = now.Format("2006-01")
	}
	loc := now.Location()
	monthStart, err := time.ParseInLocation("2006-01", month, loc)
	if err != nil {
		return nil, fmt.Errorf("月份格式应为 YYYY-MM: %s", month)
	}
	monthEnd := monthStart.AddDate(0, 1, 0)
	today := dayKey(now)
	weekEnd := now.AddDate(0, 0, 7).Format("2006-01-02")

	cal := &Calendar{Month: month, Days: []CalendarDay{}}
	byDay := map[string]*CalendarDay{}
	for _, r := range refs {
		if r.ReleaseAt <= 0 {
			cal.Unknown.Count++
			cal.Unknown.Bytes += r.Size
			continue
		}
		at := time.Unix(r.ReleaseAt, 0).In(loc)
		if at.Before(monthStart) || !at.Before(monthEnd) {
			continue
		}
		key := at.Format("2006-01-02")
		d, ok := byDay[key]
		if !ok {
			d = &CalendarDay{Date: key, Ready: key <= today, Torrents: []CalendarTorrent{}}
			byDay[key] = d
		}
		d.Count++
		d.Bytes += r.Size
		if r.HRRisk {
			d.HRRisk++
			cal.Summary.HRCount++
		}
		d.Torrents = append(d.Torrents, CalendarTorrent{
			ID: r.ID, Name: r.Name, Site: r.Site, Size: r.Size,
			SeedDays: r.SeedDays, HRRisk: r.HRRisk, ReleaseAt: r.ReleaseAt, Reason: r.Reason,
		})
		if key == today {
			cal.Summary.TodayBytes += r.Size
		}
		if key > today && key <= weekEnd {
			cal.Summary.WeekBytes += r.Size
		}
		cal.Summary.MonthBytes += r.Size
	}
	for _, d := range byDay {
		sort.SliceStable(d.Torrents, func(i, j int) bool { return d.Torrents[i].Size > d.Torrents[j].Size })
		cal.Days = append(cal.Days, *d)
	}
	// ① 保种契约到期：单独一层。契约的到期日与「可释放」不是同一天，
	// 所以这里可以为「只有契约到期、没有可释放种子」的日子建桶（Count 保持 0）。
	for _, r := range refs {
		if !r.HasContract || r.ContractUntil <= 0 {
			continue
		}
		if r.ContractBreach {
			cal.Summary.ContractBreach++
		}
		at := time.Unix(r.ContractUntil, 0).In(loc)
		if at.Before(monthStart) || !at.Before(monthEnd) {
			continue
		}
		key := at.Format("2006-01-02")
		d, ok := byDay[key]
		if !ok {
			d = &CalendarDay{Date: key, Ready: key <= today, Torrents: []CalendarTorrent{}}
			byDay[key] = d
		}
		d.ContractDue++
		cal.Summary.ContractDue++
	}
	cal.Days = cal.Days[:0]
	for _, d := range byDay {
		cal.Days = append(cal.Days, *d)
	}
	sort.SliceStable(cal.Days, func(i, j int) bool { return cal.Days[i].Date < cal.Days[j].Date })
	// 已到期的种子在日历里落在到期当天（可能是过去日期），今天的量另按「今天及更早」累计，
	// 否则今天可释放为 0 会与概览的 readyBytes 对不上
	if cal.Summary.TodayBytes == 0 {
		for _, r := range refs {
			if r.ReleaseAt > 0 && r.ReleaseAt <= now.Unix() {
				cal.Summary.TodayBytes += r.Size
			}
		}
	}
	return cal, nil
}

// Matrix 跨站/跨下载器同内容矩阵
func (s *Service) Matrix(ctx context.Context) (*Matrix, error) {
	refs, _, _, _, err := s.buildRefs(ctx)
	if err != nil {
		return nil, err
	}
	groups := map[string]*ContentGroup{}
	for _, r := range refs {
		key := normalizeName(r.Name)
		if key == "" {
			continue
		}
		g, ok := groups[key]
		if !ok {
			g = &ContentGroup{Key: key, Name: r.Name, Copies: []ContentCopy{}}
			groups[key] = g
		}
		if r.Size > g.Size {
			g.Size = r.Size
			g.Name = r.Name
		}
		g.Copies = append(g.Copies, ContentCopy{
			ID: r.ID, Site: r.Site, ServerName: r.ServerName, Kind: r.Kind,
			Status: r.Status, Size: r.Size, Uploaded: r.Uploaded30, Labels: r.Labels,
		})
	}
	m := &Matrix{Groups: []ContentGroup{}}
	for _, g := range groups {
		if len(g.Copies) < 2 {
			continue
		}
		seen := map[string]int{}
		for _, c := range g.Copies {
			g.TotalBytes += c.Size
			seen[c.ServerName+"|"+c.Kind]++
		}
		for _, n := range seen {
			if n > 1 {
				g.SameDownloader = true
				break
			}
		}
		dup := g.TotalBytes - g.Size
		if dup < 0 {
			dup = 0
		}
		g.DuplicateBytes = dup
		sort.SliceStable(g.Copies, func(i, j int) bool { return g.Copies[i].Size > g.Copies[j].Size })
		m.Groups = append(m.Groups, *g)
	}
	sort.SliceStable(m.Groups, func(i, j int) bool { return m.Groups[i].DuplicateBytes > m.Groups[j].DuplicateBytes })
	if len(m.Groups) > 50 {
		m.Groups = m.Groups[:50]
	}
	for _, g := range m.Groups {
		m.Summary.DuplicateBytes += g.DuplicateBytes
	}
	m.Summary.Groups = len(m.Groups)
	return m, nil
}

// DetectedSites 当前快照里的站点分布（用于一键生成规则）
func (s *Service) DetectedSites(ctx context.Context) ([]DetectedSite, error) {
	refs, _, _, _, err := s.buildRefs(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, r := range refs {
		if r.Site == "" {
			continue
		}
		counts[r.Site]++
	}
	out := make([]DetectedSite, 0, len(counts))
	for site, n := range counts {
		out = append(out, DetectedSite{Site: site, Count: n})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out, nil
}

// round1 / round2 只做展示层收敛，避免 JSON 里出现 38.599999999
func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }
func round2(v float64) float64 { return float64(int(v*100+0.5)) / 100 }

// ---------- 保种契约（①） ----------

// 契约是「承诺」，不是「策略」：写在这里只影响台账/日历的展示与告警，
// 永不触发暂停或删除。删除种子时契约随之失效（前端按 hash 匹配，不额外清理）。

// SetContract 设置/覆盖一颗种子的保种契约。
// Days>0 时按「今天起 N 天」换算；否则用 Until（unix 秒）。两者都无效则报错。
func (s *Service) SetContract(req ContractRequest) (Contract, error) {
	hash := strings.ToLower(strings.TrimSpace(req.Hash))
	if hash == "" {
		return Contract{}, fmt.Errorf("缺少种子 hash")
	}
	now := time.Now()
	until := req.Until
	if req.Days > 0 {
		until = now.AddDate(0, 0, req.Days).Unix()
	}
	if until <= 0 {
		return Contract{}, fmt.Errorf("请给出保种天数或到期时间")
	}
	c := Contract{
		Hash:      hash,
		Until:     until,
		Note:      strings.TrimSpace(req.Note),
		CreatedAt: now.Unix(),
	}
	if err := s.local.contractPut(c); err != nil {
		return Contract{}, err
	}
	return c, nil
}

// RemoveContract 解除契约；返回是否确实存在过
func (s *Service) RemoveContract(hash string) (bool, error) {
	return s.local.contractDelete(strings.ToLower(strings.TrimSpace(hash)))
}

// Contracts 全部契约（hash -> 契约），供 MCP 与调试查询
func (s *Service) Contracts() map[string]Contract { return s.local.contractAll() }

// Coverage 当前采样覆盖情况（②），供 MCP 查询
func (s *Service) Coverage() SampleCoverage { return s.local.coverage(time.Now()) }

// ---------- 站点规则辅助 ----------

// UpsertProfile 保存/更新一条站点规则（空 ID 视为新建）
func UpsertProfile(st *state.Store, p state.SiteProfile) (state.SiteProfile, error) {
	if strings.TrimSpace(p.Name) == "" {
		return p, fmt.Errorf("站点名不能为空")
	}
	if strings.TrimSpace(p.Match) == "" {
		p.Match = strings.ToLower(strings.TrimSpace(p.Name))
	}
	if p.ID == "" {
		p.ID = fmt.Sprintf("sp-%d", time.Now().UnixNano())
	}
	var saved state.SiteProfile
	err := st.Update(func(s *state.State) {
		for i := range s.SiteProfiles {
			if s.SiteProfiles[i].ID == p.ID {
				s.SiteProfiles[i] = p
				saved = p
				return
			}
		}
		s.SiteProfiles = append(s.SiteProfiles, p)
		saved = p
	})
	return saved, err
}

// DeleteProfile 删除站点规则
func DeleteProfile(st *state.Store, id string) (bool, error) {
	found := false
	err := st.Update(func(s *state.State) {
		for i := range s.SiteProfiles {
			if s.SiteProfiles[i].ID == id {
				s.SiteProfiles = append(s.SiteProfiles[:i], s.SiteProfiles[i+1:]...)
				found = true
				return
			}
		}
	})
	return found, err
}

// SeedProfiles 为尚未配置规则的站点批量创建占位规则（不猜任何天数）
func SeedProfiles(ctx context.Context, svc *Service, st *state.Store) (int, error) {
	detected, err := svc.DetectedSites(ctx)
	if err != nil {
		return 0, err
	}
	created := 0
	err = st.Update(func(s *state.State) {
		for _, d := range detected {
			if strings.TrimSpace(d.Site) == "" {
				continue
			}
			exists := false
			for _, p := range s.SiteProfiles {
				if strings.EqualFold(p.Name, d.Site) {
					exists = true
					break
				}
				if _, hit := matchProfile([]state.SiteProfile{p}, d.Site); hit {
					exists = true
					break
				}
			}
			if exists {
				continue
			}
			s.SiteProfiles = append(s.SiteProfiles, state.SiteProfile{
				ID:          fmt.Sprintf("sp-%d-%d", time.Now().UnixNano(), created),
				Name:        d.Site,
				Match:       strings.ToLower(d.Site),
				MinSeedDays: 0,
				MinRatio:    0,
				HRSeedHours: 0,
				Enabled:     true,
			})
			created++
		}
	})
	return created, err
}

// ---------- 档案库 ----------

// Capture 在种子被删除前留档。失败只返回错误，由调用方决定是否阻断（默认不阻断）。
func (s *Service) Capture(ctx context.Context, id int64) error {
	t, err := s.manager.GetTorrentDetail(ctx, id)
	if err != nil {
		return err
	}
	if t == nil {
		return fmt.Errorf("种子不存在")
	}
	hash := t.HashString
	entryID := hash
	if entryID == "" {
		entryID = strconv.FormatInt(id, 10)
	}
	site := siteOf(t)
	rec := vaultRecord{
		VaultEntry: VaultEntry{
			ID:           entryID,
			Hash:         hash,
			Name:         t.Name,
			Site:         site,
			Size:         t.TotalSize,
			DeletedAt:    nowUnix(),
			SeedDays:     float64(t.SecondsSeeding) / 86400,
			Ratio:        t.UploadRatio,
			UploadedEver: t.UploadedEver,
			Labels:       nonNilLabels(t.Labels),
			ServerName:   t.ServerName,
			Kind:         t.Kind,
			SavePath:     t.DownloadDir,
			HasTorrent:   false,
			CanRevive:    true,
		},
		Magnet: magnetOf(t),
	}
	for _, f := range t.Files {
		rec.Files = append(rec.Files, VaultFile{Name: f.Name, Size: f.Length})
	}
	// v1 不做 .torrent 导出（驱动接口暂未暴露），留档磁力即可复活
	return s.local.vaultPut(rec)
}

// magnetOf 优先用下载器给的磁力；没有就用 infohash + tracker 重建。
// 重建的磁力可能带 passkey，因此只在档案内部保存，不随列表接口返回。
func magnetOf(t *models.Torrent) string {
	if m := strings.TrimSpace(t.MagnetLink); m != "" {
		return m
	}
	if t.HashString == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("magnet:?xt=urn:btih:")
	b.WriteString(strings.ToLower(t.HashString))
	if t.Name != "" {
		b.WriteString("&dn=")
		b.WriteString(urlQueryEscape(t.Name))
	}
	seen := map[string]bool{}
	add := func(announce string) {
		a := strings.TrimSpace(announce)
		if a == "" || seen[a] {
			return
		}
		seen[a] = true
		b.WriteString("&tr=")
		b.WriteString(urlQueryEscape(a))
	}
	for _, tr := range t.Trackers {
		add(tr.Announce)
	}
	for _, ts := range t.TrackerStats {
		add(ts.Announce)
	}
	return b.String()
}

// urlQueryEscape 只转义磁力参数需要的字符，保留 %3A 之类可读性
func urlQueryEscape(s string) string {
	r := strings.NewReplacer(
		" ", "%20", "&", "%26", "+", "%2B", "#", "%23", "?", "%3F",
	)
	return r.Replace(s)
}

// VaultList 档案列表；q 匹配名称/hash/站点
func (s *Service) VaultList(q string) *VaultList {
	recs := s.local.vaultAll()
	sortRecordsNewest(recs)
	query := strings.ToLower(strings.TrimSpace(q))
	out := &VaultList{Entries: []VaultEntry{}}
	for _, r := range recs {
		if query != "" {
			hay := strings.ToLower(r.Name + " " + r.Hash + " " + r.Site)
			if !strings.Contains(hay, query) {
				continue
			}
		}
		out.Entries = append(out.Entries, r.VaultEntry)
		out.Total++
		out.Bytes += r.Size
	}
	return out
}

// VaultDetail 档案详情（含文件列表）
func (s *Service) VaultDetail(id string) (VaultEntry, []VaultFile, bool) {
	r, ok := s.local.vaultFind(id)
	if !ok {
		return VaultEntry{}, nil, false
	}
	files := r.Files
	if files == nil {
		files = []VaultFile{}
	}
	return r.VaultEntry, files, true
}

// VaultRemove 删除一条档案（连同留档文件）
func (s *Service) VaultRemove(id string) bool { return s.local.vaultRemove(id) }

// VaultTorrentBytes 读取留档的 .torrent（没有则 ok=false）
func (s *Service) VaultTorrentBytes(id string) ([]byte, bool) {
	r, ok := s.local.vaultFind(id)
	if !ok || r.TorrentPath == "" {
		return nil, false
	}
	data, err := os.ReadFile(r.TorrentPath)
	if err != nil {
		return nil, false
	}
	return data, true
}

// Revive 复活档案：优先原始 .torrent，否则用留档磁力重新添加
func (s *Service) Revive(ctx context.Context, id string) (int64, string, error) {
	r, ok := s.local.vaultFind(id)
	if !ok {
		return 0, "", fmt.Errorf("档案不存在")
	}
	if r.TorrentPath != "" {
		if data, err := os.ReadFile(r.TorrentPath); err == nil && len(data) > 0 {
			newID, err := s.manager.AddTorrentByFile(ctx, data, r.SavePath, false, r.Labels, nil, nil, nil)
			if err != nil {
				return 0, "", err
			}
			return newID, "torrent", nil
		}
	}
	if strings.TrimSpace(r.Magnet) == "" {
		return 0, "", fmt.Errorf("该档案没有可用的磁力或种子文件")
	}
	newID, err := s.manager.AddTorrentByURL(ctx, r.Magnet, r.SavePath, false, r.Labels, nil)
	if err != nil {
		return 0, "", err
	}
	return newID, "magnet", nil
}

// ---------- 发种工作台 ----------

// Preview 生成 BBCode（不落盘）。生成前做基本检查，缺失项以 warnings 返回。
// 无告警时返回空数组而非 null：契约里 warnings 是数组，前端会直接做 .length/.map。
func Preview(req PublishRequest) PublishPreview {
	warnings := []string{}
	if strings.TrimSpace(req.Name) == "" {
		warnings = append(warnings, "缺少种子名称")
	}
	if strings.TrimSpace(req.MediaInfo) == "" {
		warnings = append(warnings, "未粘贴 MediaInfo，发布前请补齐")
	}
	if len(req.Screenshots) == 0 {
		warnings = append(warnings, "未填写截图链接，发布前请补齐")
	}
	if strings.TrimSpace(req.Resolution) == "" && strings.TrimSpace(req.MediaInfo) != "" {
		req.Resolution = guessResolution(req.MediaInfo)
	}
	return PublishPreview{BBCode: buildBBCode(req), Warnings: warnings}
}

// guessResolution 从 MediaInfo 文本里猜分辨率，猜不到就留空
func guessResolution(mi string) string {
	low := strings.ToLower(mi)
	switch {
	case strings.Contains(low, "3840") || strings.Contains(low, "2160p"):
		return "3840x2160"
	case strings.Contains(low, "1920") || strings.Contains(low, "1080p"):
		return "1920x1080"
	case strings.Contains(low, "1280") || strings.Contains(low, "720p"):
		return "1280x720"
	}
	return ""
}

func bbcLine(k, v string) string {
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return "[b]" + k + "[/b] " + v + "\n"
}

func buildBBCode(req PublishRequest) string {
	var b strings.Builder
	for _, s := range req.Screenshots {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		b.WriteString("[img]" + s + "[/img]\n")
	}
	if len(req.Screenshots) > 0 {
		b.WriteString("\n")
	}
	b.WriteString("[quote]")
	b.WriteString(bbcLine("译名", req.Title))
	b.WriteString(bbcLine("片名", req.Name))
	if req.Year > 0 {
		b.WriteString(bbcLine("年代", strconv.Itoa(req.Year)))
	}
	b.WriteString(bbcLine("国家", req.Country))
	b.WriteString(bbcLine("类别", req.Genre))
	b.WriteString(bbcLine("分辨率", req.Resolution))
	b.WriteString(bbcLine("视频编码", req.Video))
	b.WriteString(bbcLine("音频", req.Audio))
	b.WriteString(bbcLine("字幕", req.Subtitle))
	if req.SizeBytes > 0 {
		b.WriteString(bbcLine("大小", humanGB(req.SizeBytes)))
	}
	if strings.TrimSpace(req.Description) != "" {
		b.WriteString("\n" + req.Description + "\n")
	}
	b.WriteString("[/quote]\n")
	if strings.TrimSpace(req.MediaInfo) != "" {
		b.WriteString("\n[hide]MediaInfo\n")
		b.WriteString(strings.TrimSpace(req.MediaInfo))
		b.WriteString("\n[/hide]\n")
	}
	return b.String()
}

func humanGB(bytes int64) string {
	gb := float64(bytes) / float64(1<<30)
	if gb >= 100 {
		return strconv.Itoa(int(gb+0.5)) + " GB"
	}
	return strconv.FormatFloat(gb, 'f', 2, 64) + " GB"
}

// Kit 生成发布资料包 zip：bbcode.txt、mediainfo.txt、checklist.txt（有种子时附 .torrent）
func Kit(req PublishRequest, torrentName string, torrentData []byte) ([]byte, error) {
	pv := Preview(req)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name, content string) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(content))
		return err
	}
	if err := write("bbcode.txt", pv.BBCode); err != nil {
		return nil, err
	}
	mi := req.MediaInfo
	if strings.TrimSpace(mi) == "" {
		mi = "(未提供 MediaInfo)"
	}
	if err := write("mediainfo.txt", mi); err != nil {
		return nil, err
	}
	if err := write("checklist.txt", checklist(req, pv)); err != nil {
		return nil, err
	}
	if len(torrentData) > 0 {
		if torrentName == "" {
			torrentName = safeName(req.Name) + ".torrent"
		}
		w, err := zw.Create(path.Base(torrentName))
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(torrentData); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func checklist(req PublishRequest, pv PublishPreview) string {
	var b strings.Builder
	b.WriteString("发布前检查清单\n\n")
	check := func(ok bool, label string) {
		mark := "[ ]"
		if ok {
			mark = "[x]"
		}
		b.WriteString(mark + " " + label + "\n")
	}
	check(strings.TrimSpace(req.Name) != "", "标题/种子名称已填写")
	check(len(req.Screenshots) > 0, fmt.Sprintf("截图已准备（%d 张）", len(req.Screenshots)))
	check(strings.TrimSpace(req.MediaInfo) != "", "MediaInfo 已粘贴")
	check(strings.TrimSpace(req.Subtitle) != "", "字幕信息已填写")
	check(strings.TrimSpace(req.Resolution) != "", "分辨率已填写")
	check(strings.TrimSpace(req.Category) != "", "分类已选择")
	if len(pv.Warnings) > 0 {
		b.WriteString("\n待处理：\n")
		for _, w := range pv.Warnings {
			b.WriteString("- " + w + "\n")
		}
	}
	b.WriteString("\n注意：资料包只生成发布素材，SeedArk 不会自动上传到站点。\n")
	return b.String()
}

func safeName(name string) string {
	bad := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	return bad.Replace(strings.TrimSpace(name))
}
