package pt

import (
	"context"

	"github.com/sushazhi/seedark/backend/internal/models"
)

// diskStat 取磁盘余量。路径取「种子最集中的那个保存目录」：多盘机器上总有一个盘
// 在扛绝大多数做种，那个盘才是真正决定「该不该腾空间」的地方。
//
// 查不到（下载器不支持 / 路径不存在）时返回零值：界面据此不显示磁盘块，
// 也绝不把它当成「空间充足」。宁可少一条信息，不给错结论。
func (s *Service) diskStat(ctx context.Context, refs []TorrentRef) DiskStat {
	settings := s.ptSettings()
	out := DiskStat{TargetFreeGB: settings.ReleaseTargetFreeGB}
	if s.manager == nil {
		return out
	}
	byPath := map[string]int64{}
	for _, r := range refs {
		if r.SavePath == "" {
			continue
		}
		byPath[r.SavePath] += r.Size
	}
	path := ""
	var biggest int64
	for p, sz := range byPath {
		if sz > biggest {
			path, biggest = p, sz
		}
	}
	if path == "" {
		return out
	}
	free, total, err := s.manager.GetFreeSpace(ctx, path)
	if err != nil || free < 0 {
		return out
	}
	out.Path = path
	out.FreeBytes = free
	out.TotalBytes = total
	if total > 0 {
		out.UsedPercent = float64(total-free) / float64(total) * 100
	}
	if settings.ReleaseTargetFreeGB > 0 {
		out.LowSpace = float64(free) < settings.ReleaseTargetFreeGB*float64(1<<30)
	}
	return out
}

// Eviction 淘汰建议：磁盘紧张时「先放哪几颗」。只产出建议，不删任何种子。
//
// 三层纪律（每一层都能被用户看懂，也能被关掉）：
//  1. 触发：剩余空间低于目标才建议清理（freeGb 可覆盖设置值）；
//  2. 候选：只收 safeToRelease 的种子——义务未满足、做种稀缺、有未到期保种契约的一律不进；
//  3. 排序：**站内相对时魔**升序 → 稀缺度降序 → 体积降序 → 做种人数降序。
//
// 第 3 层为什么是「站内相对」而不是时魔·GB 的绝对值：各站的时魔口径互不相通
// （NPHP 公式估的单颗边际、抄录的站点时魔、没有时魔的站），绝对值混着排等于
// 拿馒头的积分和别站的猫粮比大小。站内排序回答的才是用户真问题——「同一个站里先放哪颗最不亏」。
func (s *Service) Eviction(ctx context.Context, freeGb float64, limit int) (*Eviction, error) {
	refs, _, _, _, err := s.buildRefs(ctx)
	if err != nil {
		return nil, err
	}
	target := freeGb
	if target <= 0 {
		target = s.ptSettings().ReleaseTargetFreeGB
	}
	disk := s.diskStat(ctx, refs)
	trig := EvictionTrigger{FreeBytes: disk.FreeBytes, TargetFreeGB: target}
	if target > 0 {
		trig.NeedBytes = int64(target*float64(1<<30)) - disk.FreeBytes
		trig.Low = trig.NeedBytes > 0
	}

	// 候选集只留「放掉也安全」的。刻意不凑数：候选不足时界面应显示
	// 「可安全释放的不够」，而不是怂恿用户删掉该保的种。
	cands := make([]TorrentRef, 0, len(refs))
	for _, r := range refs {
		if r.SafeToRelease {
			cands = append(cands, r)
		}
	}
	sortForEviction(cands)
	// 排序要用的同一个位置表，这里再取一次给界面显示「本站第几便宜」。
	// 纯函数、确定性一致：同一份输入两次调用结果相同，不会和排序结论打架。
	// known 为 false 时（本站内时魔·GB 无区分度）不给这一行任何位置断言。
	eff, effKnown := evictionOrder(cands)
	if limit <= 0 {
		limit = 50
	}
	if len(cands) > limit {
		cands = cands[:limit]
	}

	out := &Eviction{
		Trigger:    trig,
		Candidates: []EvictionCandidate{},
		Note:       "按 站内相对时魔 ↓ → 稀缺度 ↑ → 体积 ↓ 排序（跨站不比绝对值）；仅建议，本面板不会删除任何种子",
	}
	var cum int64
	for _, r := range cands {
		cum += r.Size
		out.TotalBytes = cum
		var pos *float64
		if v, ok := eff[r.Hash]; ok && effKnown[r.Hash] {
			p := v
			pos = &p
		}
		out.Candidates = append(out.Candidates, EvictionCandidate{
			Hash:            r.Hash,
			Name:            r.Name,
			Site:            r.Site,
			Size:            r.Size,
			Seeders:         r.Seeders,
			Scarcity:        r.Scarcity,
			BonusPerGb:      r.BonusPerGb,
			BonusEff:        pos,
			Efficiency:      r.Efficiency,
			SafeToRelease:   r.SafeToRelease,
			Reason:          evictionReason(r, pos),
			CumulativeBytes: cum,
		})
	}
	return out, nil
}

// evictionReason 给一行淘汰建议一句人话解释，用户不必自己推断「为什么先放这颗」。
// pos 是该颗在**本站**候选里的相对位置（0 = 本站最该先放），nil 表示没算出来（拿不到口径）。
func evictionReason(r TorrentRef, pos *float64) string {
	why := "义务已满足"
	switch r.Swarm {
	case SwarmAbundant:
		why += " · 做种 " + itoa(r.Seeders) + " 人（充足）"
	case SwarmNormal:
		why += " · 做种 " + itoa(r.Seeders) + " 人"
	}
	if pos != nil {
		switch {
		case *pos <= 0.001:
			why += " · 本站里时魔最不划算"
		case *pos >= 0.999:
			why += " · 本站里时魔最划算（排在候选末尾）"
		default:
			why += " · 本站相对时魔 " + itoa(int(*pos*100+0.5)) + "%"
		}
	}
	if r.Efficiency > 0 && r.Efficiency < 1 {
		why += " · 近窗口效率低"
	}
	return why
}

// savePathOf 台账行要带保存路径：磁盘统计与「按盘腾空间」都按它聚合。
// 用客户端的 DownloadDir 原始值；不同下载器的路径映射各自维护，
// 这里不做跨机归一——把两台机的路径混成一盘会让余量判断失真。
func savePathOf(t *models.Torrent) string {
	if t == nil {
		return ""
	}
	return t.DownloadDir
}
