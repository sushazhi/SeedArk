package speedpolicy

import (
	"context"
	"log/slog"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/trpanel/backend/internal/driver"
	"github.com/trpanel/backend/internal/rpc"
	"github.com/trpanel/backend/internal/state"
)

const (
	// tickInterval 引擎节奏：组内限速要求比做种策略更快收敛，
	// 种子开始/完成、用户调整规则后都能在 10s 内重新分配
	tickInterval = 10 * time.Second
	// sitesCacheTTL 站点名映射（一次全量 RPC）的本地缓存时长
	sitesCacheTTL = 60 * time.Second
	// minShareKB 组内单种至少分到的余量。剩余不足均分时给 1KB/s 保活，
	// 避免分配 0 把仍在跑的任务完全掐死
	minShareKB = int64(1)
)

var (
	dirs     = []string{state.SpeedDirectionDown, state.SpeedDirectionUp}
	trueVal  = true
	falseVal = false
)

// Service 组内总限速引擎：把规则设定的组总上限摊到组内运行种子上。
// 用户单独限速过、或跟随全局限速的种子视为「手动固定」：引擎不覆盖它，
// 仅把其当前占用从组预算中扣除；引擎自己写入的限速持久化在状态里，
// 重启后仍能区分「引擎设定」与「用户手动设定」并在需要时安全释放。
type Service struct {
	manager *rpc.Manager
	store   *state.Store

	// tickMu 串行化一轮计算：后台定时 tick 与「立即应用」可能同时触发，
	// 避免并发下发 / 并发写状态文件导致接管表互相覆盖
	tickMu sync.Mutex

	siteMu    sync.Mutex
	siteNames map[int64][]string
	siteAt    time.Time
}

// New 创建组内总限速引擎
func New(manager *rpc.Manager, store *state.Store) *Service {
	return &Service{manager: manager, store: store}
}

// Result 一轮限速计算的统计，供「立即执行」接口回显
type Result struct {
	Enabled  bool `json:"enabled"`  // 引擎开关且存在启用规则
	Matched  int  `json:"matched"`  // 引擎需要接管的种子数（含已一致无需下发的）
	Applied  int  `json:"applied"`  // 实际新下发 / 变更限速的种子数
	Released int  `json:"released"` // 释放了引擎先前限速的种子数
	Failed   int  `json:"failed"`   // 下发失败的种子数
}

// Run 后台循环
func (s *Service) Run(ctx context.Context) {
	slog.Info("组内总限速引擎已启动")
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		_, _ = s.Tick(ctx)
	}
}

// Tick 执行一轮组内限速计算与下发
func (s *Service) Tick(ctx context.Context) (*Result, error) {
	s.tickMu.Lock()
	defer s.tickMu.Unlock()
	st := s.store.Get()
	rules := enabledRules(st.SpeedPolicyRules)
	res := &Result{Enabled: st.SpeedPolicyGuard.Enforce && len(rules) > 0}
	if !res.Enabled {
		// 引擎关闭或没有任何启用规则：把此前引擎写入的限速全部还原，不再接管
		s.releaseAll(ctx, &st)
		return res, nil
	}
	torrents, err := s.manager.GetTorrents(ctx)
	if err != nil {
		slog.Warn("组内限速：获取种子列表失败", "err", err)
		return res, err
	}
	siteNames := s.sites(ctx, rules)

	// 目标：torrent id -> 方向 -> 引擎本轮应设置的限速（KB/s，>0）
	desired := map[int64]map[string]int64{}
	for _, dir := range dirs {
		perDir := s.planDir(dir, rules, torrents, siteNames, &st)
		if len(perDir) == 0 {
			continue
		}
		for id, cap := range perDir {
			if desired[id] == nil {
				desired[id] = map[string]int64{}
			}
			desired[id][dir] = cap
		}
	}
	s.sync(ctx, torrents, desired, &st, res)
	return res, nil
}

// planDir 计算某方向的接管计划：命中规则、正在运行、可被引擎接管的种子各分到多少。
// 规则的某方向上限为 0 表示该方向不限制，引擎跳过；多条规则命中同一种子时
// 取更严的那份（min），保证每个组的总和都不超过各自上限。
func (s *Service) planDir(dir string, rules []*state.SpeedPolicyRule, torrents []*rpc.Torrent,
	siteNames map[int64][]string, st *state.State) map[int64]int64 {
	desired := map[int64]int64{}
	for _, rule := range rules {
		limit := rule.DownLimit
		if dir == state.SpeedDirectionUp {
			limit = rule.UpLimit
		}
		if limit <= 0 {
			continue // 0 = 该方向不限制
		}
		var autos []*rpc.Torrent
		used := int64(0)
		for _, t := range torrents {
			if t == nil || !activeFor(t, dir) || !inScope(t, rule, siteNames[t.ID]) {
				continue
			}
			if isFixed(t, dir, st) {
				used += consumeKB(t, dir)
				continue
			}
			autos = append(autos, t)
		}
		if len(autos) == 0 {
			continue
		}
		budget := limit - used
		if budget <= 0 {
			// 手动固定的种子已占满组预算：自动部分维持现状，
			// 不去动手动种子（那会破坏用户自己的设置）
			continue
		}
		share := budget / int64(len(autos))
		if share < minShareKB {
			share = minShareKB
		}
		for _, t := range autos {
			if cur, ok := desired[t.ID]; !ok || share < cur {
				desired[t.ID] = share
			}
		}
	}
	return desired
}

// activeFor 该方向下「正在产生流量」的种子才参与限速接管：
// 下载只看正在下载（4）；上传同时算正在下载与正在做种（4/6）。
func activeFor(t *rpc.Torrent, dir string) bool {
	if dir == state.SpeedDirectionDown {
		return t.Status == 4
	}
	return t.Status == 4 || t.Status == 6
}

// inScope 种子是否落在规则站点 / 标签 / 名称范围内（口径与做种策略一致）
func inScope(t *rpc.Torrent, r *state.SpeedPolicyRule, sites []string) bool {
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

// isFixed 该种子在某方向是否「不可接管」：自带限速，且不是引擎上一轮写入的值
// （用户手动设置，或改过引擎写入的值）。这类种子的带宽占用会被扣除，
// 但引擎绝不覆盖或释放它。
//
// 跟随全局限速（honorsSessionLimits）不算固定——那恰恰是引擎要接管的常态：
// Transmission 的种子默认跟随全局，此时本种子的限速字段会被忽略，
// 引擎接管时会关掉该标记（并记下原值），释放时再还原。
func isFixed(t *rpc.Torrent, dir string, st *state.State) bool {
	if !limitedOf(t, dir) {
		return false
	}
	applied := st.AppliedCap(t.ID, dir)
	return applied <= 0 || applied != limitOf(t, dir)
}

// consumeKB 手动固定种子当前占用（KB/s）：按其实时速度计，但不超过它自设的限速。
func consumeKB(t *rpc.Torrent, dir string) int64 {
	var rateBps int64
	if dir == state.SpeedDirectionDown {
		rateBps = t.RateDownload
	} else {
		rateBps = t.RateUpload
	}
	rateKB := (rateBps + 1023) / 1024 // 向上取整
	if limitedOf(t, dir) {
		if lim := limitOf(t, dir); lim > 0 && lim < rateKB {
			return lim
		}
	}
	return rateKB
}

// sync 对比本轮目标与「现状 + 引擎上轮记录」：下发变更、还原不再需要的限速，
// 并把最新的「引擎接管表」持久化（还原失败会保留记录，下轮重试）。
func (s *Service) sync(ctx context.Context, torrents []*rpc.Torrent, desired map[int64]map[string]int64,
	st *state.State, res *Result) {
	byID := make(map[int64]*rpc.Torrent, len(torrents))
	for _, t := range torrents {
		if t != nil {
			byID[t.ID] = t
		}
	}

	// 拷贝上轮接管表作为本轮的起点
	newApplied := make(map[int64]state.SpeedApplied, len(st.SpeedPolicyApplied))
	for id, a := range st.SpeedPolicyApplied {
		newApplied[id] = a
	}

	// 同值下发合并成一次 torrent-set：dir + "\x00" + cap -> ids
	setBucket := map[string][]int64{}
	// 还原按「方向 + honors 处理方式」合并：dir + "\x00" + (keep|true|false) -> ids
	relBucket := map[string][]int64{}

	for _, dir := range dirs {
		for _, t := range torrents {
			if t == nil {
				continue
			}
			id := t.ID
			dcap := desired[id][dir]
			curLimited := limitedOf(t, dir)
			curLimit := limitOf(t, dir)
			prev := st.AppliedCap(id, dir)

			if dcap > 0 {
				res.Matched++
				// 需要下发：限速值不一致，或该种子仍在跟随全局限速
				//（honors 为真时 Transmission 会忽略本种子的限速字段）
				if !curLimited || curLimit != dcap || t.HonorsSessionLimits {
					key := dir + "\x00" + strconv.FormatInt(dcap, 10)
					setBucket[key] = append(setBucket[key], id)
					res.Applied++
				}
				a, ok := newApplied[id]
				if !ok {
					// 首次接管：记下接管前的 honors 原值，释放时按它还原
					a = state.SpeedApplied{Honors: t.HonorsSessionLimits}
				}
				if dir == state.SpeedDirectionUp {
					a.Up = dcap
				} else {
					a.Down = dcap
				}
				newApplied[id] = a
				continue
			}
			// 本轮不再接管该方向
			if prev <= 0 {
				continue
			}
			if curLimited && curLimit == prev {
				// 仍是引擎写入的值：还原限速。另一方向仍在接管时，
				// honors 必须保持 false，否则会把仍在生效的那一方向限速顶掉
				honors := "keep"
				if desired[id][otherDir(dir)] <= 0 {
					honors = strconv.FormatBool(newApplied[id].Honors)
				}
				relBucket[dir+"\x00"+honors] = append(relBucket[dir+"\x00"+honors], id)
				continue
			}
			// 用户已改过 / 已关闭该限速：不要动它，仅从接管表中移除
			clearApplied(newApplied, id, dir)
		}
	}
	// 接管表里已不存在的种子（被删除）直接清理
	for id := range newApplied {
		if _, ok := byID[id]; !ok {
			delete(newApplied, id)
		}
	}

	// 下发限速：成功与否的接管记录都已写入 newApplied——
	// 失败时种子仍保持原样，下轮会按「引擎未接管」再次尝试下发
	for key, ids := range setBucket {
		dir, cap := parseCapKey(key)
		payload := limitPayload(dir, cap)
		if err := s.manager.SetTorrent(ctx, ids, payload); err != nil {
			res.Failed += len(ids)
			slog.Warn("组内限速：下发限速失败", "dir", dir, "cap", cap, "ids", len(ids), "err", err)
			continue
		}
	}
	// 还原限速（仅成功者从接管表移除）
	released := 0
	for key, ids := range relBucket {
		dir, honors := parseRelKey(key)
		payload := releasePayload(dir, honors)
		if err := s.manager.SetTorrent(ctx, ids, payload); err != nil {
			res.Failed += len(ids)
			slog.Warn("组内限速：还原限速失败", "dir", dir, "ids", len(ids), "err", err)
			continue
		}
		released += len(ids)
		for _, id := range ids {
			clearApplied(newApplied, id, dir)
		}
	}
	res.Released = released

	// 仅在有实质变化时落盘，避免每 10s 空写一次状态文件
	if len(setBucket)+len(relBucket) > 0 || !reflect.DeepEqual(newApplied, st.SpeedPolicyApplied) {
		_ = s.store.Update(func(st2 *state.State) {
			st2.SpeedPolicyApplied = newApplied
		})
	}
	if res.Applied+res.Released > 0 {
		slog.Info("组内总限速执行完成", "matched", res.Matched, "applied", res.Applied,
			"released", res.Released, "failed", res.Failed)
	}
}

// clearApplied 清除接管表中某方向的值，两个方向都清空时删除整条记录
func clearApplied(m map[int64]state.SpeedApplied, id int64, dir string) {
	a, ok := m[id]
	if !ok {
		return
	}
	if dir == state.SpeedDirectionUp {
		a.Up = 0
	} else {
		a.Down = 0
	}
	if a.Down <= 0 && a.Up <= 0 {
		delete(m, id)
		return
	}
	m[id] = a
}

// releaseAll 引擎关闭或无启用规则时，把引擎上轮写入的限速全部还原。
// 只释放能确认仍是引擎写入值的种子；释放失败的记录保留，下轮重试。
func (s *Service) releaseAll(ctx context.Context, st *state.State) {
	if len(st.SpeedPolicyApplied) == 0 {
		return
	}
	torrents, err := s.manager.GetTorrents(ctx)
	if err != nil {
		slog.Warn("组内限速：关闭还原时获取种子列表失败，下轮重试", "err", err)
		return
	}
	byID := make(map[int64]*rpc.Torrent, len(torrents))
	for _, t := range torrents {
		if t != nil {
			byID[t.ID] = t
		}
	}
	relBucket := map[string][]int64{}
	for id, a := range st.SpeedPolicyApplied {
		t, ok := byID[id]
		if !ok {
			continue // 种子已被删除：直接清记录
		}
		for dir, cap := range map[string]int64{state.SpeedDirectionDown: a.Down, state.SpeedDirectionUp: a.Up} {
			if cap <= 0 {
				continue
			}
			// 值已不是引擎写入的（用户改过 / 关掉）：不动种子，仅丢记录
			if limitedOf(t, dir) && limitOf(t, dir) == cap {
				relBucket[dir+"\x00"+strconv.FormatBool(a.Honors)] = append(relBucket[dir+"\x00"+strconv.FormatBool(a.Honors)], id)
			}
		}
	}
	if len(relBucket) == 0 {
		_ = s.store.Update(func(st2 *state.State) { st2.SpeedPolicyApplied = map[int64]state.SpeedApplied{} })
		return
	}
	// 还原失败的 id 保留接管记录，下轮再试
	kept := map[int64]state.SpeedApplied{}
	for key, ids := range relBucket {
		dir, honors := parseRelKey(key)
		if err := s.manager.SetTorrent(ctx, ids, releasePayload(dir, honors)); err != nil {
			slog.Warn("组内限速：关闭还原失败，下轮重试", "dir", dir, "err", err)
			for _, id := range ids {
				kept[id] = st.SpeedPolicyApplied[id]
			}
			continue
		}
	}
	_ = s.store.Update(func(st2 *state.State) { st2.SpeedPolicyApplied = kept })
	if len(kept) == 0 {
		slog.Info("组内限速引擎已关闭，已还原全部下发限速")
	}
}

// sites 返回站点名映射（含 60s 缓存；无站点条件时不发起 RPC）
func (s *Service) sites(ctx context.Context, rules []*state.SpeedPolicyRule) map[int64][]string {
	need := false
	for _, r := range rules {
		if len(r.Sites) > 0 {
			need = true
			break
		}
	}
	if !need {
		return nil
	}
	s.siteMu.Lock()
	defer s.siteMu.Unlock()
	if s.siteNames != nil && time.Since(s.siteAt) < sitesCacheTTL {
		return s.siteNames
	}
	m, err := s.manager.GetTorrentSites(ctx)
	if err != nil {
		slog.Warn("组内限速：获取站点名失败，本轮仅按 tracker 主机名匹配", "err", err)
		return nil
	}
	s.siteNames = m
	s.siteAt = time.Now()
	return m
}

// enabledRules 返回启用的规则（保留配置顺序）
func enabledRules(all []state.SpeedPolicyRule) []*state.SpeedPolicyRule {
	out := make([]*state.SpeedPolicyRule, 0, len(all))
	for i := range all {
		if all[i].Enabled {
			out = append(out, &all[i])
		}
	}
	return out
}

func limitedOf(t *rpc.Torrent, dir string) bool {
	if dir == state.SpeedDirectionDown {
		return t.DownloadLimited
	}
	return t.UploadLimited
}

func limitOf(t *rpc.Torrent, dir string) int64 {
	if dir == state.SpeedDirectionDown {
		return t.DownloadLimit
	}
	return t.UploadLimit
}

func otherDir(dir string) string {
	if dir == state.SpeedDirectionUp {
		return state.SpeedDirectionDown
	}
	return state.SpeedDirectionUp
}

// limitPayload 下发单种限速。必须同时关掉 honorsSessionLimits：
// 该标记为 true 时 Transmission 直接用全局限速，本种子的限速字段不生效。
func limitPayload(dir string, cap int64) driver.TorrentPatch {
	p := driver.TorrentPatch{HonorsSessionLimits: &falseVal}
	if dir == state.SpeedDirectionDown {
		p.DownloadLimited = &trueVal
		p.DownloadLimit = &cap
	} else {
		p.UploadLimited = &trueVal
		p.UploadLimit = &cap
	}
	return p
}

// releasePayload 还原限速：取消该方向的单种限速。
// honors 为 nil 表示另一方向仍在被引擎接管，不能把「跟随全局」还原回去；
// 非 nil 时按接管前记录的原值还原。
func releasePayload(dir string, honors *bool) driver.TorrentPatch {
	p := driver.TorrentPatch{HonorsSessionLimits: honors}
	if dir == state.SpeedDirectionDown {
		p.DownloadLimited = &falseVal
	} else {
		p.UploadLimited = &falseVal
	}
	return p
}

// parseCapKey 解析 dir + "\x00" + cap
func parseCapKey(key string) (string, int64) {
	idx := strings.IndexByte(key, '\x00')
	if idx < 0 {
		return key, 0
	}
	cap, _ := strconv.ParseInt(key[idx+1:], 10, 64)
	return key[:idx], cap
}

// parseRelKey 解析 dir + "\x00" + (keep|true|false)
func parseRelKey(key string) (string, *bool) {
	idx := strings.IndexByte(key, '\x00')
	if idx < 0 {
		return key, nil
	}
	rest := key[idx+1:]
	if rest == "keep" {
		return key[:idx], nil
	}
	v := rest == "true"
	return key[:idx], &v
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
