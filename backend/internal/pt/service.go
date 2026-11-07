package pt

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
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
	// snaps 站点快照与考核目标（手工录入的站点侧数据）
	snaps *snapStore

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
	return &Service{
		manager: manager,
		store:   st,
		local:   ls,
		snaps:   newSnapStore(filepath.Join(dataDir, "pt")),
	}, nil
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
		now:           now,
		contracts:     s.local.contractAll(),
		lastInc:       lastInc,
		seen:          seen,
		coverage:      s.local.coverage(now),
		hostHealthy:   hostHealthy,
		today:         s.local.todayDelta(now),
		siteNames:     s.siteMap(ctx),
		scarceSeeders: s.ptSettings().ScarceSeeders,
	}
}

// ptSettings 取 PT 全局阈值；未配置时给默认值（稀缺阈值 10，取自 ptool 的经验值）。
func (s *Service) ptSettings() state.PTSettings {
	st := s.store.Get().PTSettings
	if st.ScarceSeeders <= 0 {
		st.ScarceSeeders = defaultScarceSeeders
	}
	return st
}

// buildRefs 计算全部台账行（概览/日历/台账/同内容共用同一份计算）。
// 同时返回该次快照的计算上下文，避免概览为了拿覆盖率再算一遍。
func (s *Service) buildRefs(ctx context.Context) ([]TorrentRef, []state.SiteProfile, time.Time, *calcCtx, error) {
	torrents, profiles, now, err := s.snapshot(ctx)
	if err != nil {
		return nil, nil, time.Time{}, nil, err
	}
	cc := s.calc(ctx, now, torrents)
	// 先按展示名把「本站集合量」预计算一遍，再逐颗快照：nexusphp 档的 ΣA/颗数、
	// manual 档的做种体积都必须来自同一遍遍历——分散到每行现算，先算的行会把
	// 后算的行漏进或重复算进分母，B 就对不上账了。
	bonusBySite := bonusCtxBySite(torrents, profiles, cc, now)
	cc.bonus = bonusBySite
	refs := make([]TorrentRef, 0, len(torrents))
	for _, t := range torrents {
		if t == nil {
			continue
		}
		refs = append(refs, s.refOf(t, profiles, cc, bonusBySite))
	}
	return refs, profiles, now, cc, nil
}

// bonusCtxBySite 按展示名预计算每站的时魔集合量（nexusphp 的 ΣA/颗数 + manual 的做种体积）。
//
// 口径与站方对齐：mybonus.php 只给 `peers.seeder='yes'` 的 peer 计时魔——
// 下载中（left>0）不是 seeder，暂停的 peer 不在线也不算。所以这里只统计
// statusOf==seeding 的种子，与 bonusOf 单颗行的「未在做种给 0」同一把尺子。
//
// ΣA 用 aValueOf（不带 perseeding 的 A_t）：perseeding 只出现在 B 的第二项，
// 混进 ΣA 会把 atan(ΣA/L) 整体放大——这是复刻公式最容易犯的错。
// bonusActualOf 单站的「实际每小时」时魔：nexusphp = 本地复刻 B(ΣA,颗数)（需参数齐全），
// manual = 抄录值。ok=false 表示该站配了却算不出（参数不齐）或没配——
// 调用方据此显示「—」，绝不把「不知道」冒充 0。
// 概览的时魔卡片（BonusCap.Sites）与站点分布表的时魔列共用这一个实现，
// 两处数字不同就是 bug。
func (s *Service) bonusActualOf(p state.SiteProfile, cc *calcCtx) (perHour, capPerHour float64, engine string, ok bool) {
	engine = state.BonusEngineOf(p)
	switch engine {
	case state.BonusEngineNexusPHP:
		bc, has := cc.bonus[p.Name]
		if !has || !bc.hasParams {
			return 0, 0, engine, false
		}
		perHour = bonusTotalFrom(p, bc.sumA, bc.perCount)
		if c, _, cok := bonusCap(p); cok {
			capPerHour = c
		}
		return perHour, capPerHour, engine, true
	case state.BonusEngineManual:
		if p.BonusManualPerHour <= 0 {
			return 0, 0, engine, false
		}
		return p.BonusManualPerHour, 0, engine, true
	default:
		return 0, 0, engine, false
	}
}

func bonusCtxBySite(torrents []*models.Torrent, profiles []state.SiteProfile, cc *calcCtx, now time.Time) map[string]bonusCtx {
	out := map[string]bonusCtx{}
	for _, p := range profiles {
		if !p.Enabled {
			continue
		}
		_, ok := bonusParamsOf(p)
		// 官种正则每站编译一次（每颗现编译会拖慢全量遍历）。编译不过就降级成「不区分官种」，
		// 并在 bonusCtx 上留标记——静默按 0 颗算会让用户以为站点改了规则。
		re, bad := compileOfficialPattern(p.BonusOfficialPattern)
		out[p.Name] = bonusCtx{hasParams: ok, officialRe: re, officialBad: bad}
	}
	for _, t := range torrents {
		if t == nil || t.TotalSize <= 0 {
			continue
		}
		name, p, _ := displaySite(t, cc, profiles)
		if name == "" {
			continue
		}
		bc, seen := out[name]
		if !seen {
			bc = bonusCtx{}
		}
		if statusOf(t) == StatusSeeding {
			if bc.hasParams {
				seeders, _ := swarmCounts(t)
				ageDays, _ := ageOf(t, now)
				bc.sumA += aValueOf(t, p, ageDays, seeders)
				bc.count++
				// 数量项按站点口径计数：站点只数官种/限体积时这一步会小于 count
				if countedForPerSeeding(t, p, bc.officialRe) {
					bc.perCount++
				}
			}
			bc.siteSeedBytes += t.TotalSize
		}
		out[name] = bc
	}
	return out
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

// profileFor 按展示名找回该站点的规则。
// refOf 内部已按 tracker 匹配过一次，但概览/淘汰建议只拿到 ref，需要这条回来取
// 公式参数与义务阈值；名字对不上时退回按展示名再匹配一次。
func (s *Service) profileFor(site string, profiles []state.SiteProfile) state.SiteProfile {
	for _, p := range profiles {
		if p.Enabled && p.Name == site {
			return p
		}
	}
	if p, ok := matchProfile(profiles, site); ok {
		return p
	}
	return state.SiteProfile{}
}

// siteBonus 概览汇总用的「单站时魔上限 + 该站引擎」。上限取 bonusCap 的返回值：
// atan 站 = B0 + perseeding×maxseeding，manual 站 = 抄录值，**log 站 = 0（它没有上限）**。
type siteBonus struct {
	perHour float64
	engine  string
}

// bonusCapsOf 把各站上限汇成概览卡片要的那几个量：最高的那条上限 + 站名 + 引擎，
// 以及「有几个站配了口径」。
//
// 「配了口径」与「有上限」必须分开判：log 曲线的站没有上限（perHour=0），
// 若拿 0 去参与「取最大值」的比较，Configured 会永远停在 false —— 界面就会在
// 已经算出 TotalPerHour 的情况下说「还没有站点配时魔口径」，而同一屏的站点分布表
// 又是正常数字。Configured 的语义只是「有没有配」，与上限是否存在无关。
func bonusCapsOf(sites map[string]siteBonus) (perHour float64, configured bool, bestSite, engine string, count int) {
	for site, b := range sites {
		count++
		if b.perHour > perHour {
			perHour = b.perHour
			bestSite = site
			engine = b.engine
		}
	}
	return perHour, count > 0, bestSite, engine, count
}

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
	// 时魔按**站**收集，不在这里合并：各站的「点」不可通约（NPHP 公式上限、抄录的站点
	// 时魔、压根没有时魔的站），求一个全局最大值等于把馒头和黄星站比大小。
	// 循环结束后只挑「最高的那条」并把站名一起带出去，界面就能说清这个数是哪站的。
	bonusBySite := map[string]siteBonus{}
	ratioSize := map[string]float64{}
	upcoming := map[string]*UpcomingDay{}
	siteQual := map[string][]DeltaQuality{}
	var allQual []DeltaQuality
	for _, r := range refs {
		o.TorrentCount++
		o.TotalSize += r.Size
		o.Uploaded30 += r.Uploaded30
		// 上传量列的窗口取各行的最大值：种子全是刚加的时采样可能已跑满 30 天，
		// 但这一列一天数据都没有，标题不能写「近 30 天」
		if r.WindowDays > o.WindowDays {
			o.WindowDays = r.WindowDays
		}
		// v3 稀缺汇总：unknown 单独计，不并进 scarce 也不并进 abundant
		switch r.Swarm {
		case SwarmScarce:
			o.Scarcity.ScarceCount++
			o.Scarcity.ScarceBytes += r.Size
		case SwarmUnknown:
			o.Scarcity.UnknownCount++
		}
		// 时魔：只收本行所属站那条口径（拿不到规则时不收，不等于 0）
		if p := s.profileFor(r.Site, profiles); p.Name != "" {
			if _, seen := bonusBySite[p.Name]; !seen {
				if perHour, engine, ok := bonusCap(p); ok {
					bonusBySite[p.Name] = siteBonus{perHour: perHour, engine: engine}
				}
			}
		}
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
		// 站点累计分享率 = Σ(ratio_i×size_i)/Σsize_i = ΣuploadedEver/Σsize。
		// r.Ratio 是客户端的**累计**分享率（uploadedEver/size），体积加权后就是
		// 该站在客户端口径下的真实分享率——比原来用「近30天增量÷体积」近似靠谱：
		// 采样刚起步时那个式子只会算出假 0.00，用户还以为时魔列前面挂了个多余的数。
		ratioSize[site] += r.Ratio * float64(r.Size)
		siteQual[site] = append(siteQual[site], r.TodayQuality)
		allQual = append(allQual, r.TodayQuality)
	}
	if effN > 0 {
		o.AvgEfficiency = round1(effSum / float64(effN))
	}
	// 时魔上限：取各站里最高的那条，并把站名与引擎一起带出。
	o.BonusCap.PerHour, o.BonusCap.Configured, o.BonusCap.BestSite, o.BonusCap.Engine, o.BonusCap.ConfigCount =
		bonusCapsOf(bonusBySite)
	// 实际口径：上限（PerHour）是「一颗不多正好打满」的数，用户拿它跟站点页
	// 「你当前每小时能获取 108.345」对不上就会以为面板错了。这里按站算
	// 实际值（nexusphp = 本地复刻 B(ΣA,颗数)；manual = 抄录值）求合计，
	// 上限与 ΣA 对账退到每站明细里。
	for i := range profiles {
		p := profiles[i]
		if !p.Enabled {
			continue
		}
		sc := BonusSiteCard{Site: p.Name, Verified: p.RuleCheckedAt > 0}
		if bc, ok := cc.bonus[p.Name]; ok {
			sc.Count = bc.count
			sc.LocalSumA = bc.sumA
		}
		ph, cap, eng, ok := s.bonusActualOf(p, cc)
		if !ok {
			continue // 参数不齐/未配置：算不出实际值，宁可缺席也不编
		}
		sc.Engine = eng
		sc.PerHour = ph
		sc.CapPerHour = cap
		if p.BonusASumObserved > 0 {
			sc.ObservedA = p.BonusASumObserved
		}
		o.BonusCap.TotalPerHour += sc.PerHour
		o.BonusCap.Sites = append(o.BonusCap.Sites, sc)
	}
	for _, st := range bySite {
		if st.Size > 0 {
			// 站点分享率：客户端口径的**累计**分享率（ΣuploadedEver/Σsize，非站方数据）。
			// 客户端说没传过就是 0.00——这是真实的 0，不是采样不足的假 0。
			st.Ratio = round2(ratioSize[st.Site] / float64(st.Size))
		}
		// v3.1 时魔列：站点分布表每站一行「实际每小时」。
		// 注意 cc.bonus 以**规则名**为键，profileFor 可能返回名字对不上的规则
		// （站点 m-team ↔ 规则名 mt），所以这里必须按 p.Name 取，不能按站点名。
		if p := s.profileFor(st.Site, profiles); p.Name != "" {
			if ph, cap, engine, ok := s.bonusActualOf(p, cc); ok {
				st.BonusSet = true
				st.BonusPerHour = ph
				st.BonusCapPerHour = cap
				st.BonusEngine = engine
			}
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
		o.ReclaimableBytes = m.Summary.ReclaimableBytes
		o.SharedGroups = m.Summary.SharedGroups
	}
	o.Warnings = overviewWarnings(o)
	// v3 磁盘余量：取活动下载器里种子最集中的那个保存路径。
	// 查不到就留零值（界面不显示），不能因为「查不到」就当成「空间充足」。
	o.Disk = s.diskStat(ctx, refs)
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
		return fmt.Sprintf("与 %s 逐个对比得到", t.PrevDay)
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
		switch {
		case from == "":
			out = append(out, "还未开始采样，上传量暂无数据（采样满 1 天后开始统计）")
		case o.Coverage.Days == 0:
			out = append(out, fmt.Sprintf("采样不足 1 天（%s 起），上传量暂无数据", from))
		default:
			out = append(out, fmt.Sprintf("采样仅覆盖 %d 天（%s 起），上传量最多统计这 %d 天", o.Coverage.Days, from, o.Coverage.Days))
		}
	}
	if o.InvalidCount > 0 {
		out = append(out, fmt.Sprintf("%d 个种子的站点已判为未注册（站点已删种），占 %s", o.InvalidCount, humanBytes(o.InvalidBytes)))
	}
	if o.IdleCount > 0 {
		out = append(out, fmt.Sprintf("%d 个种子连续 %d 天以上没有上传增长，占 %s", o.IdleCount, idleThresholdDays, humanBytes(o.IdleBytes)))
	}
	if o.ContractBreach > 0 {
		out = append(out, fmt.Sprintf("%d 个种子的保种契约已过期未解除", o.ContractBreach))
	}
	if o.OverBudgetCount > 0 {
		out = append(out, fmt.Sprintf("%d 个站点超出保种预算，合计超出 %s", o.OverBudgetCount, humanBytes(o.OverBudgetBytes)))
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
		// 表头写「近 N 天」用的天数取各行最大值：这一列实际有多少天数据就写多少天
		if r.WindowDays > l.Totals.WindowDays {
			l.Totals.WindowDays = r.WindowDays
		}
		// 零上传只统计真正看到过的窗口：WindowDays=0 是「刚开始采样、还没看到」，
		// 把它算成零上传会让新装用户整列飘红
		if r.WindowDays > 0 && r.Uploaded30 <= 0 {
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
	// 分组与判定逻辑在 matrix.go（纯函数，可单测）
	return matrixOf(refs), nil
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
			Reason:       "manual",
		},
		Magnet: magnetOf(t),
	}
	for _, f := range t.Files {
		rec.Files = append(rec.Files, VaultFile{Name: f.Name, Size: f.Length})
	}
	// v1 不做 .torrent 导出（驱动接口暂未暴露），留档磁力即可复活
	return s.local.vaultPut(rec)
}

// CaptureReason 删种前留档并记录删除原因（做种策略自动删种用）。
//
// 策略此前直接 RemoveTorrents，被自动删掉的种子根本不进档案库——而自动删
// 往往一次几十颗，恰恰最需要「后悔药」。留档失败只返回错误、由调用方决定
// 是否仍然删除（与手动删种的「不阻断」策略一致）。
func (s *Service) CaptureReason(ctx context.Context, id int64, reason, note string) error {
	if err := s.Capture(ctx, id); err != nil {
		return err
	}
	if reason == "" {
		return nil
	}
	t, err := s.manager.GetTorrentDetail(ctx, id)
	if err != nil || t == nil || t.HashString == "" {
		return nil
	}
	return s.local.vaultSetReason(t.HashString, reason, note)
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
		b.WriteString(bbcLine("大小", humanBytes(req.SizeBytes)))
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

// humanBytes 体积的人类可读写法：与前端 formatBytes 同一套口径——1024 进制、
// 自动升到「装得下它的最小单位」、小于 100 保留两位小数、大于等于 100 取整。
//
// 曾用名 humanGB 且恒以 GB 输出，于是预警里出现「占 21180 GB」这种读不出量级的数字
// （其实是 20.68 TB）。文案层面的单位必须和台账/概览的数字同口径，否则同一份数据
// 在两处显示成两种量级。
func humanBytes(bytes int64) string {
	if bytes <= 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	v := float64(bytes)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	// 四舍五入可能把显示值顶到 1024（1023.6 GB →「1024 GB」），再进一档，
	// 保证单位始终是「装得下这个值的最小单位」（与前端同一处修正）。
	if i > 0 && i < len(units)-1 && v >= 1023.5 {
		v /= 1024
		i++
	}
	if i == 0 || v >= 100 {
		return strconv.Itoa(int(v+0.5)) + " " + units[i]
	}
	return strconv.FormatFloat(v, 'f', 2, 64) + " " + units[i]
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
