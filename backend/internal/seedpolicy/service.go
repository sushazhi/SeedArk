package seedpolicy

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sushazhi/seedark/backend/internal/rpc"
	"github.com/sushazhi/seedark/backend/internal/state"
)

const (
	logLimit = 200
	gibBytes = int64(1) << 30
)

// Service 做种策略引擎：按站点维度的分享率/做种时长目标，达标后暂停或删除种子
type Service struct {
	manager *rpc.Manager
	store   *state.Store

	// tickMu 串行化一轮执行：后台定时 tick 与「立即执行」接口可能同时触发，
	// 并发跑会让同一批种子被下发两次删除、已处理标记互相覆盖
	tickMu sync.Mutex
}

// New 创建做种策略服务
func New(manager *rpc.Manager, store *state.Store) *Service {
	return &Service{manager: manager, store: store}
}

// Result 一轮执行的统计，供「立即执行」接口回显
type Result struct {
	Matched   int `json:"matched"`
	Paused    int `json:"paused"`
	Deleted   int `json:"deleted"`
	Previewed int `json:"previewed"`
	Failed    int `json:"failed"`
}

// planItem 一条已达标且通过保护栏的种子及其待执行动作
type planItem struct {
	torrent *rpc.Torrent
	rule    *state.SeedPolicyRule
	site    string
	reason  []state.SeedPolicyReasonPart
}

// Run 后台循环
func (s *Service) Run(ctx context.Context) {
	slog.Info("做种策略服务已启动")
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		_, _ = s.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// PlanEntry 一条达标计划的对外只读快照（MCP 报告 / 预览接口用）
type PlanEntry struct {
	TorrentID   int64                        `json:"torrentId"`
	TorrentName string                       `json:"torrentName"`
	Hash        string                       `json:"hash"`
	Site        string                       `json:"site"`
	Rule        string                       `json:"rule"`
	Action      string                       `json:"action"`
	Reason      []state.SeedPolicyReasonPart `json:"reason"`
}

// Tick 执行一轮策略评估。列表接口已带 trackerStats，站点判定无需逐种拉详情。
func (s *Service) Tick(ctx context.Context) (*Result, error) {
	s.tickMu.Lock()
	defer s.tickMu.Unlock()
	plan, torrents, err := s.plan(ctx)
	if err != nil {
		return nil, err
	}
	result := &Result{Matched: len(plan)}
	// 只取一次状态快照，供保护栏判断与预览落盘复用，避免对同一份状态多次 JSON 深拷贝
	st := s.store.Get()
	// 预览路径不写状态，清理已删除种子的已处理标记只能在这里做
	s.pruneProcessed(&st, torrents)
	if len(plan) == 0 {
		return result, nil
	}
	if !st.SeedPolicyGuard.Enforce {
		result.Previewed = len(plan)
		s.persistPreview(plan, &st)
		return result, nil
	}
	s.execute(ctx, plan, result)
	return result, nil
}

// Preview 只读评估：返回当前已达标且尚未被策略处理的种子，不执行动作、不落盘。
// enforce 表示保护栏开关（true 时执行 Tick 会真正动作），供调用方一并展示。
func (s *Service) Preview(ctx context.Context) ([]PlanEntry, bool, error) {
	plan, _, err := s.plan(ctx)
	if err != nil {
		return nil, false, err
	}
	entries := make([]PlanEntry, 0, len(plan))
	for _, it := range plan {
		entries = append(entries, PlanEntry{
			TorrentID:   it.torrent.ID,
			TorrentName: it.torrent.Name,
			Hash:        it.torrent.HashString,
			Site:        it.site,
			Rule:        it.rule.Name,
			Action:      it.rule.Action,
			Reason:      it.reason,
		})
	}
	return entries, s.store.Get().SeedPolicyGuard.Enforce, nil
}

// plan 计算当前已达标且未处理的种子清单（不执行、不落盘）。
// 同时返回本轮拉到的全量种子列表，供 Tick 清理失效标记用。
func (s *Service) plan(ctx context.Context) ([]planItem, []*rpc.Torrent, error) {
	st := s.store.Get()
	rules := enabledRules(st.SeedPolicyRules)
	if len(rules) == 0 {
		return nil, nil, nil
	}
	// 动作按 ID 批量下发，必须用最新列表：缓存里已被用户删掉的种子会让整批 RPC 失败
	torrents, err := s.manager.GetTorrentsFresh(ctx)
	if err != nil {
		slog.Warn("做种策略：获取种子列表失败", "err", err)
		return nil, nil, err
	}
	// 站点规则用中文名选站（与侧边栏/自动文件管理同一口径），但列表接口只带
	// trackerStats 主机名，站点名在详情的 trackers 里；这里复用同一份站点映射一次
	// RPC 取全量，失败则退回仅按主机名匹配
	siteNames := map[int64][]string{}
	if needsSites(&st, rules) {
		if m, err := s.manager.GetTorrentSites(ctx); err == nil {
			siteNames = m
		} else {
			slog.Warn("做种策略：获取站点列表失败，本轮仅按 tracker 主机名匹配", "err", err)
		}
	}
	var plan []planItem
	for _, t := range torrents {
		if t == nil {
			continue
		}
		item, ok := evaluate(&st, rules, t, siteNames)
		if !ok {
			continue
		}
		if _, done := st.ProcessedPolicy[state.PolicyKey(item.rule.ID, t.HashString)]; done {
			continue
		}
		plan = append(plan, item)
	}
	return plan, torrents, nil
}

// pruneProcessed 清理已不存在种子的已处理标记。
// 标记只对仍存在的种子有意义：种子被删除后标记再无读者，不清会在状态文件里
// 随增删无限堆积（每次落盘都要重写全表）。暂停类种子的标记仍在 live 集合中，
// 不受影响。列表为空（异常态）时不清理，避免把全部标记一次性抹掉。
func (s *Service) pruneProcessed(st *state.State, torrents []*rpc.Torrent) {
	live := make(map[string]struct{}, len(torrents))
	for _, t := range torrents {
		if t != nil && t.HashString != "" {
			live[t.HashString] = struct{}{}
		}
	}
	if len(live) == 0 {
		return
	}
	stale := false
	for k := range st.ProcessedPolicy {
		if h, ok := state.PolicyKeyHash(k); ok {
			if _, exist := live[h]; !exist {
				stale = true
				break
			}
		}
	}
	if !stale {
		return
	}
	removed := 0
	_ = s.store.Update(func(st2 *state.State) {
		removed = state.PruneHashMap(st2.ProcessedPolicy, live, state.PolicyKeyHash)
	})
	if removed > 0 {
		slog.Info("做种策略：清理已删除种子的已处理标记", "count", removed)
	}
}

func enabledRules(all []state.SeedPolicyRule) []*state.SeedPolicyRule {
	out := make([]*state.SeedPolicyRule, 0, len(all))
	for i := range all {
		if all[i].Enabled {
			out = append(out, &all[i])
		}
	}
	return out
}

// evaluate 依次套用保护栏与规则（按配置顺序命中第一条），返回待执行动作及达标依据
func evaluate(st *state.State, rules []*state.SeedPolicyRule, t *rpc.Torrent, siteNames map[int64][]string) (planItem, bool) {
	// 只处理下载完成的种子，否则会把做种目标误伤成下载中断
	if !t.IsFinished {
		return planItem{}, false
	}
	// 本地报错（多为文件缺失）时任何动作都可能放大损失
	if t.Error != 0 {
		return planItem{}, false
	}
	// announce 全部失败时站点侧并未记账，本地分享率不代表真实贡献
	if trackerUnreachable(t) {
		return planItem{}, false
	}
	guard := st.SeedPolicyGuard
	if guard.MinSeedHours > 0 && float64(t.SecondsSeeding)/3600 < guard.MinSeedHours {
		return planItem{}, false
	}
	sites := siteKeys(t, siteNames[t.ID])
	if matchesAny(guard.ExcludeSites, sites) || matchesAny(guard.ExcludeLabels, t.Labels) {
		return planItem{}, false
	}
	for _, r := range rules {
		if reason, ok := reached(r, t); ok && matchesRuleScope(r, t, sites) {
			return planItem{torrent: t, rule: r, site: primarySite(t), reason: reason}, true
		}
	}
	return planItem{}, false
}

// matchesRuleScope 判断种子是否落在规则的站点 / 标签 / 名称范围内
func matchesRuleScope(r *state.SeedPolicyRule, t *rpc.Torrent, sites []string) bool {
	if r.NameMatch != "" && !strings.Contains(strings.ToLower(t.Name), strings.ToLower(r.NameMatch)) {
		return false
	}
	if len(r.Labels) > 0 && !matchesAny(r.Labels, t.Labels) {
		return false
	}
	if len(r.Sites) > 0 && !matchesAny(r.Sites, sites) {
		return false
	}
	return true
}

// reached 判断达标条件：已配置的条件需全部满足，并返回结构化达标依据
func reached(r *state.SeedPolicyRule, t *rpc.Torrent) ([]state.SeedPolicyReasonPart, bool) {
	if r.MinRatio <= 0 && r.MinSeedDays <= 0 && r.MinUploadGB <= 0 {
		return nil, false
	}
	var parts []state.SeedPolicyReasonPart
	if r.MinRatio > 0 {
		if t.UploadRatio < r.MinRatio {
			return nil, false
		}
		parts = append(parts, state.SeedPolicyReasonPart{Kind: "ratio", Actual: t.UploadRatio, Target: r.MinRatio})
	}
	if r.MinSeedDays > 0 {
		days := float64(t.SecondsSeeding) / 86400
		if days < r.MinSeedDays {
			return nil, false
		}
		parts = append(parts, state.SeedPolicyReasonPart{Kind: "days", Actual: days, Target: r.MinSeedDays})
	}
	if r.MinUploadGB > 0 {
		upGB := float64(t.UploadedEver) / float64(gibBytes)
		if upGB < r.MinUploadGB {
			return nil, false
		}
		parts = append(parts, state.SeedPolicyReasonPart{Kind: "upload", Actual: upGB, Target: r.MinUploadGB})
	}
	return parts, true
}

// reasonText 服务端日志用的可读文本；界面侧由前端按语言自行渲染
func reasonText(parts []state.SeedPolicyReasonPart) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		switch p.Kind {
		case "days":
			out = append(out, fmt.Sprintf("做种 %.1fd≥%.1fd", p.Actual, p.Target))
		case "upload":
			out = append(out, fmt.Sprintf("上传 %.1fGB≥%.1fGB", p.Actual, p.Target))
		default:
			out = append(out, fmt.Sprintf("分享率 %.2f≥%.2f", p.Actual, p.Target))
		}
	}
	return strings.Join(out, "，")
}

// execute 按规则分组批量下发动作；失败项不落已处理标记，留待下轮重试
func (s *Service) execute(ctx context.Context, plan []planItem, result *Result) {
	byRule := make(map[string][]planItem)
	for _, it := range plan {
		byRule[it.rule.ID] = append(byRule[it.rule.ID], it)
	}
	ids := make([]int64, 0, len(plan))
	for _, items := range byRule {
		rule := items[0].rule
		ids = ids[:0]
		for _, it := range items {
			ids = append(ids, it.torrent.ID)
		}
		// 动作白名单：未知取值绝不落入删除分支。手改状态文件或历史遗留的
		// 笔误动作（曾按「非 pause 即删除」处理）会因此变成删种事故。
		var err error
		switch rule.Action {
		case state.PolicyActionPause:
			err = s.manager.StopTorrents(ctx, ids)
		case state.PolicyActionDelete:
			err = s.manager.RemoveTorrents(ctx, ids, false)
		case state.PolicyActionDeleteData:
			err = s.manager.RemoveTorrents(ctx, ids, true)
		default:
			result.Failed += len(items)
			slog.Warn("做种策略：规则动作无法识别，本轮跳过", "rule", rule.Name, "action", rule.Action)
			continue
		}
		if err != nil {
			result.Failed += len(items)
			slog.Warn("做种策略：动作失败", "rule", rule.Name, "action", rule.Action, "err", err)
			continue
		}
		if rule.Action == state.PolicyActionPause {
			result.Paused += len(items)
		} else {
			result.Deleted += len(items)
		}
		s.record(items, false)
	}
	if result.Paused+result.Deleted > 0 {
		slog.Info("做种策略执行完成", "paused", result.Paused, "deleted", result.Deleted)
	}
}

// record 写入已处理标记与执行记录
func (s *Service) record(items []planItem, dryRun bool) {
	if len(items) == 0 {
		return
	}
	now := state.NowUnix()
	logs := make([]state.SeedPolicyLog, 0, len(items))
	for _, it := range items {
		logs = append(logs, state.SeedPolicyLog{
			Time:    now,
			Rule:    it.rule.Name,
			Torrent: it.torrent.Name,
			Site:    it.site,
			Action:  it.rule.Action,
			Reason:  it.reason,
			DryRun:  dryRun,
		})
		// 命令行部署看不到界面，删除/暂停必须能从 journal 里追溯原因
		slog.Info("做种策略", "rule", it.rule.Name, "action", it.rule.Action, "torrent", it.torrent.Name,
			"site", it.site, "reason", reasonText(it.reason), "dryRun", dryRun)
	}
	_ = s.store.Update(func(st *state.State) {
		for _, it := range items {
			st.ProcessedPolicy[state.PolicyKey(it.rule.ID, it.torrent.HashString)] = strconv.FormatInt(now, 10)
		}
		st.SeedPolicyLogs = appendLogs(st.SeedPolicyLogs, logs, dryRun)
	})
}

// persistPreview 预览只刷新待办清单，不落已处理标记。
// 未开启自动执行时每分钟都会评估一轮，清单未变则不写盘。
// st 为调用方已取的状态快照，复用其 SeedPolicyLogs 避免再次深拷贝。
func (s *Service) persistPreview(plan []planItem, st *state.State) {
	now := state.NowUnix()
	logs := make([]state.SeedPolicyLog, 0, len(plan))
	for _, it := range plan {
		logs = append(logs, state.SeedPolicyLog{
			Time:    now,
			Rule:    it.rule.Name,
			Torrent: it.torrent.Name,
			Site:    it.site,
			Action:  it.rule.Action,
			Reason:  it.reason,
			DryRun:  true,
		})
	}
	if previewEqual(st.SeedPolicyLogs, logs) {
		return
	}
	_ = s.store.Update(func(st *state.State) {
		st.SeedPolicyLogs = appendLogs(st.SeedPolicyLogs, logs, true)
	})
}

// previewEqual 比对待办清单是否变化，忽略写入时间
func previewEqual(old, added []state.SeedPolicyLog) bool {
	var cur []state.SeedPolicyLog
	for _, l := range old {
		if l.DryRun {
			cur = append(cur, l)
		}
	}
	if len(cur) != len(added) {
		return false
	}
	for i := range cur {
		if !sameLog(cur[i], added[i]) {
			return false
		}
	}
	return true
}

// sameLog 比较两条记录（Reason 为切片，结构体不能再直接用 ==）
func sameLog(a, b state.SeedPolicyLog) bool {
	if a.Rule != b.Rule || a.Torrent != b.Torrent || a.Site != b.Site || a.Action != b.Action || a.DryRun != b.DryRun {
		return false
	}
	if len(a.Reason) != len(b.Reason) {
		return false
	}
	for i := range a.Reason {
		if a.Reason[i] != b.Reason[i] {
			return false
		}
	}
	return true
}

// appendLogs 新记录置于头部并截断；预览记录只保留最新一批，避免每分钟重复刷屏
func appendLogs(old, added []state.SeedPolicyLog, replacePreview bool) []state.SeedPolicyLog {
	kept := make([]state.SeedPolicyLog, 0, len(old))
	for _, l := range old {
		if !replacePreview || !l.DryRun {
			kept = append(kept, l)
		}
	}
	out := make([]state.SeedPolicyLog, 0, len(added)+len(kept))
	out = append(out, added...)
	out = append(out, kept...)
	if len(out) > logLimit {
		out = out[:logLimit]
	}
	return out
}

// siteKeys 返回种子用于规则匹配的 tracker 标识（中文站点名、主机名与 announce 地址）。
// 站点名与侧边栏站点分组、自动文件管理同一口径，规则里选中文站点名即可命中；
// 兼容旧配置：填主机名或简称（双向子串）依然生效
func siteKeys(t *rpc.Torrent, siteNames []string) []string {
	seen := make(map[string]struct{}, len(t.TrackerStats)*2+len(siteNames))
	out := make([]string, 0, len(t.TrackerStats)*2+len(siteNames))
	add := func(v string) {
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	for _, ts := range t.TrackerStats {
		add(ts.Host)
		add(ts.Announce)
	}
	for _, name := range siteNames {
		add(name)
	}
	return out
}

// needsSites 规则或保护栏按站点收窄时，才值得多花一次 RPC 拉站点名映射
func needsSites(st *state.State, rules []*state.SeedPolicyRule) bool {
	if len(st.SeedPolicyGuard.ExcludeSites) > 0 {
		return true
	}
	for _, r := range rules {
		if len(r.Sites) > 0 {
			return true
		}
	}
	return false
}

// primarySite 取主 tracker 主机名，仅用于执行记录展示
func primarySite(t *rpc.Torrent) string {
	for _, ts := range t.TrackerStats {
		if !ts.IsBackup && ts.Host != "" {
			return ts.Host
		}
	}
	if len(t.TrackerStats) > 0 {
		return t.TrackerStats[0].Host
	}
	return ""
}

// trackerUnreachable 所有非备用 tracker 都没有成功 announce 过（判定口径与详情面板一致：
// 有过 announce 时间但未成功即视为失败），此时站点侧很可能没记录到上传贡献
func trackerUnreachable(t *rpc.Torrent) bool {
	attempted := false
	for _, ts := range t.TrackerStats {
		if ts.IsBackup {
			continue
		}
		if ts.LastAnnounceSucceeded {
			return false
		}
		if ts.LastAnnounceTime > 0 {
			attempted = true
		}
	}
	return attempted
}

// matchesAny 双向子串匹配：规则里写站点简称（如 m-team）也能命中完整 tracker 主机名
func matchesAny(needles, haystack []string) bool {
	for _, n := range needles {
		nn := strings.ToLower(strings.TrimSpace(n))
		if nn == "" {
			continue
		}
		for _, h := range haystack {
			hh := strings.ToLower(h)
			if nn == hh || strings.Contains(hh, nn) || strings.Contains(nn, hh) {
				return true
			}
		}
	}
	return false
}
