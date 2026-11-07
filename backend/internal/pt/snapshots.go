package pt

import (
	"context"
	"strings"
	"time"

	"github.com/sushazhi/seedark/backend/internal/state"
)

// 本文件是 v3 的「手工录入站点侧数据」那部分：站点快照、考核目标。
//
// 为什么全部靠用户抄：这些数字只存在于站点账号页里，而本面板按设计不登录站点。
// 参考 PTMyBonusCalc 的先例——它同样要求用户「首次使用时打开站点魔力页取一次参数」。
// 我们把这些录入物只当「外部事实的快照」，不据此推断站点行为，也不跨站比较。

// 站点快照的保留与裁剪交给 snapStore（每站 400 条 / 180 天）。

// AddSnapshot 登记一条站点快照
func (s *Service) AddSnapshot(snap SiteSnapshot) (SiteSnapshot, error) {
	snap.Site = strings.TrimSpace(snap.Site)
	if snap.Site == "" {
		return SiteSnapshot{}, errBadSnapshot("site 必填")
	}
	if snap.At <= 0 {
		snap.At = time.Now().Unix()
	}
	return s.snaps.add(snap), nil
}

// Snapshots 快照列表 + 「本地 vs 站点」偏差
func (s *Service) Snapshots(ctx context.Context) *SnapshotsResponse {
	out := &SnapshotsResponse{Items: s.snaps.all(), Diff: []SnapshotDiff{}}
	latest := s.snaps.latest()
	if len(latest) == 0 {
		return out
	}
	torrents, profiles, now, err := s.snapshot(ctx)
	if err != nil {
		return out
	}
	// 本地累计按站点聚合，与站点侧 uploaded 对比。
	// 站点名走 displaySite 同一口径，否则勾不上站点快照里的 site 键。
	cc := s.calc(ctx, now, torrents)
	localUp := map[string]int64{}
	for _, t := range torrents {
		if t == nil {
			continue
		}
		name, _, _ := displaySite(t, cc, profiles)
		localUp[name] += t.UploadedEver
	}
	// 时魔「精准获取」的两个本地通道：nexusphp 档复刻 B/ΣA（对账），快照差分出实测值。
	// 与手工抄录（manual 档）互相独立——抄录值、复刻值、实测值三者能互为校验。
	bonusBySite := bonusCtxBySite(torrents, profiles, cc, now)
	history := s.snaps.all() // 按时间倒序：第一条匹配是该站最新，第二条是上一条
	for site, snap := range latest {
		d := SnapshotDiff{Site: site, DaysSince: float64(now.Unix()-snap.At) / 86400}
		if snap.Uploaded > 0 {
			if lv := localUp[site]; lv > 0 {
				d.LocalVsSiteRatio = float64(lv) / float64(snap.Uploaded)
			}
		}
		// —— 公式复刻（nexusphp 档）——
		if bc, ok := bonusBySite[site]; ok && bc.hasParams && bc.count > 0 {
			if p := s.profileFor(site, profiles); state.BonusEngineOf(p) == state.BonusEngineNexusPHP {
				d.BonusSumA = bc.sumA
				d.BonusPerHourFormula = bonusTotalFrom(p, bc.sumA, bc.perCount)
				d.BonusASumObserved = p.BonusASumObserved
			}
		}
		// —— 快照差分（与公式无关的实测通道）——
		// 只取带魔力读数（Bonus>0）的快照：没抄魔力的登记不参与，避免把 0 当成读数。
		iCur, iPrev := -1, -1
		for i := range history {
			h := history[i]
			if h.Site != site || h.Bonus <= 0 {
				continue
			}
			if iCur < 0 {
				iCur = i
			} else {
				iPrev = i
				break
			}
		}
		if iCur >= 0 && iPrev >= 0 {
			cur, prev := history[iCur], history[iPrev]
			d.BonusMeasuredHours = float64(cur.At-prev.At) / 3600
			// 魔力下降 = 期间兑换/赠送过，差分不再是纯做种收益 → 不可比；
			// 跨度不足 1 小时噪声太大 → 也不可比。两种情况都如实标注，不给看起来像实测的 0。
			if cur.Bonus >= prev.Bonus && d.BonusMeasuredHours >= 1 {
				d.BonusPerHourMeasured = (cur.Bonus - prev.Bonus) / d.BonusMeasuredHours
				d.BonusMeasuredOk = true
			}
		}
		out.Diff = append(out.Diff, d)
	}
	sortDiffs(out.Diff)
	return out
}

// DeleteSnapshots 清空某站历史
func (s *Service) DeleteSnapshots(site string) bool { return s.snaps.deleteSite(site) }

// Exams 考核列表与进度。
// 进度里所有本地量都是**下界**：本地累计上传 ≠ 站点统计（促销会放大、站点只计有效 peer），
// 所以只能说「至少达到」，不能说「就是这么多」。界面必须带这条口径。
func (s *Service) Exams(ctx context.Context) *ExamResponse {
	out := &ExamResponse{Items: []ExamItem{}}
	exams := s.snaps.exams()
	if len(exams) == 0 {
		return out
	}
	torrents, profiles, now, err := s.snapshot(ctx)
	if err != nil {
		return out
	}
	cc := s.calc(ctx, now, torrents)
	uploadGB := map[string]float64{}
	seedSizeGB := map[string]float64{}
	downloadGB := map[string]float64{}
	for _, t := range torrents {
		if t == nil {
			continue
		}
		site, _, _ := displaySite(t, cc, profiles)
		uploadGB[site] += float64(t.UploadedEver) / float64(1<<30)
		downloadGB[site] += float64(t.DownloadedEver) / float64(1<<30)
		if t.PercentDone >= 1 && t.SecondsSeeding > 0 {
			seedSizeGB[site] += float64(t.TotalSize) / float64(1<<30)
		}
	}
	for _, e := range exams {
		p := ExamProgress{Met: []string{}, Missing: []string{}}
		p.LocalUploadGB = uploadGB[e.Site]
		p.LocalSeedingSizeGB = seedSizeGB[e.Site]
		if downloadGB[e.Site] > 0 {
			p.LocalRatio = uploadGB[e.Site] / downloadGB[e.Site]
		}
		if e.Deadline > 0 {
			p.DaysLeft = float64(e.Deadline-now.Unix()) / 86400
		}
		if e.MinRatio > 0 {
			if p.LocalRatio >= e.MinRatio {
				p.Met = append(p.Met, "分享率")
			} else {
				p.Missing = append(p.Missing, "分享率")
			}
		}
		if e.MinUploadGB > 0 {
			if p.LocalUploadGB >= e.MinUploadGB {
				p.Met = append(p.Met, "上传量")
			} else {
				p.Missing = append(p.Missing, "上传量")
			}
		}
		if e.MinSeedingSizeGB > 0 {
			if p.LocalSeedingSizeGB >= e.MinSeedingSizeGB {
				p.Met = append(p.Met, "做种体积")
			} else {
				p.Missing = append(p.Missing, "做种体积")
			}
		}
		out.Items = append(out.Items, ExamItem{
			Exam:       e,
			Progress:   p,
			LowerBound: true,
			Note:       "本地累计为站点统计的下界（促销会放大、站点只计有效 peer）",
		})
	}
	return out
}

// SaveExam 保存考核目标（站点为键，覆盖式）
func (s *Service) SaveExam(e Exam) (Exam, error) {
	e.Site = strings.TrimSpace(e.Site)
	if e.Site == "" {
		return Exam{}, errBadSnapshot("site 必填")
	}
	return s.snaps.putExam(e), nil
}

// DeleteExam 删除考核目标
func (s *Service) DeleteExam(site string) bool { return s.snaps.deleteExam(site) }

// errBadSnapshot 参数错误（用 error 而不是 panic：接口层要转 400）
type errBadSnapshot string

func (e errBadSnapshot) Error() string { return string(e) }

// sortDiffs 偏差按「站点名」排，保证同一份数据每次输出顺序一致（前端 key 稳定）
func sortDiffs(list []SnapshotDiff) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j].Site < list[j-1].Site; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}
