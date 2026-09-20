package seedpolicy

import (
	"testing"

	"github.com/sushazhi/seedark/backend/internal/models"
	"github.com/sushazhi/seedark/backend/internal/rpc"
	"github.com/sushazhi/seedark/backend/internal/state"
)

func okTracker(host string) models.TrackerStat {
	return models.TrackerStat{
		Host:                  host,
		Announce:              "https://" + host + "/announce",
		LastAnnounceSucceeded: true,
		LastAnnounceTime:      1700000000,
	}
}

func brokenTracker(host string) models.TrackerStat {
	return models.TrackerStat{
		Host:             host,
		Announce:         "https://" + host + "/announce",
		LastAnnounceTime: 1700000000,
	}
}

func seedingTorrent(trackerStats ...models.TrackerStat) *rpc.Torrent {
	return &rpc.Torrent{
		ID:             7,
		Name:           "Some.Show.S01.2160p",
		HashString:     "hash1",
		IsFinished:     true,
		UploadRatio:    6.0,
		SecondsSeeding: int64(40 * 24 * 3600),
		UploadedEver:   80 * int64(1<<30),
		TrackerStats:   trackerStats,
	}
}

func ratioRule(action string, minRatio, minDays float64) *state.SeedPolicyRule {
	return &state.SeedPolicyRule{
		ID:          "r1",
		Name:        "规则一",
		Enabled:     true,
		MinRatio:    minRatio,
		MinSeedDays: minDays,
		Action:      action,
	}
}

func evalWith(guard state.SeedPolicyGuard, rules []*state.SeedPolicyRule, t *rpc.Torrent) (planItem, bool) {
	return evalWithNames(guard, rules, t, nil)
}

func evalWithNames(guard state.SeedPolicyGuard, rules []*state.SeedPolicyRule, t *rpc.Torrent, names map[int64][]string) (planItem, bool) {
	st := &state.State{SeedPolicyGuard: guard}
	return evaluate(st, rules, t, names)
}

func TestEvaluateHitsWhenReached(t *testing.T) {
	item, ok := evalWith(state.SeedPolicyGuard{}, []*state.SeedPolicyRule{ratioRule(state.PolicyActionPause, 5, 30)}, seedingTorrent(okTracker("kp.m-team.cc")))
	if !ok {
		t.Fatal("分享率 6.0 ≥ 5.0 且做种 40d ≥ 30d，应命中")
	}
	if item.rule.Action != state.PolicyActionPause {
		t.Errorf("动作应为 pause，得到 %s", item.rule.Action)
	}
	if item.site != "kp.m-team.cc" {
		t.Errorf("执行记录站点应为 tracker 主机名，得到 %q", item.site)
	}
	if len(item.reason) == 0 {
		t.Error("命中项应带达标依据供界面回看")
	}
}

func TestEvaluateRequiresEveryConfiguredCondition(t *testing.T) {
	// 分享率达标但做种时长不足：PT 站点常同时考核 ratio 与时间，缺一即不动作
	seed := seedingTorrent(okTracker("kp.m-team.cc"))
	seed.SecondsSeeding = 10 * 24 * 3600
	if _, ok := evalWith(state.SeedPolicyGuard{}, []*state.SeedPolicyRule{ratioRule(state.PolicyActionDelete, 5, 30)}, seed); ok {
		t.Error("做种 10d < 30d 时不应命中删除规则")
	}
}

func TestEvaluateSkipsUnsafeTorrents(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*rpc.Torrent)
	}{
		{"未完成", func(t *rpc.Torrent) { t.IsFinished = false }},
		{"本地报错", func(t *rpc.Torrent) { t.Error = 5 }},
		{"tracker 全挂", func(t *rpc.Torrent) { t.TrackerStats = []models.TrackerStat{brokenTracker("kp.m-team.cc")} }},
		{"备用 tracker 失败不阻塞", func(t *rpc.Torrent) {
			t.TrackerStats = []models.TrackerStat{okTracker("kp.m-team.cc"), {Host: "backup.example.com", IsBackup: true}}
		}},
	}
	for _, tc := range cases {
		seed := seedingTorrent(okTracker("kp.m-team.cc"))
		tc.mutate(seed)
		_, ok := evalWith(state.SeedPolicyGuard{}, []*state.SeedPolicyRule{ratioRule(state.PolicyActionDelete, 5, 30)}, seed)
		shouldHit := tc.name == "备用 tracker 失败不阻塞"
		if ok != shouldHit {
			t.Errorf("%s：期望命中=%v，实际命中=%v", tc.name, shouldHit, ok)
		}
	}
}

func TestGuardBlocksActions(t *testing.T) {
	rules := []*state.SeedPolicyRule{ratioRule(state.PolicyActionDelete, 5, 0)}
	t.Run("最低做种时长", func(t *testing.T) {
		seed := seedingTorrent(okTracker("kp.m-team.cc"))
		seed.SecondsSeeding = 3600
		if _, ok := evalWith(state.SeedPolicyGuard{MinSeedHours: 72}, rules, seed); ok {
			t.Error("做种 1h 低于全局 72h 下限时不应命中")
		}
	})
	t.Run("排除站点", func(t *testing.T) {
		if _, ok := evalWith(state.SeedPolicyGuard{ExcludeSites: []string{"m-team"}}, rules, seedingTorrent(okTracker("kp.m-team.cc"))); ok {
			t.Error("排除名单内按简称匹配也应命中排除")
		}
	})
	t.Run("排除标签", func(t *testing.T) {
		seed := seedingTorrent(okTracker("kp.m-team.cc"))
		seed.Labels = []string{"永久保种"}
		if _, ok := evalWith(state.SeedPolicyGuard{ExcludeLabels: []string{"永久保种"}}, rules, seed); ok {
			t.Error("带排除标签的种子不应被处理")
		}
	})
}

func TestRuleScopeMatchesSiteAndLabel(t *testing.T) {
	rule := ratioRule(state.PolicyActionPause, 5, 0)
	rule.Sites = []string{"ptsbao"}
	rule.Labels = []string{"HD"}
	if _, ok := evalWith(state.SeedPolicyGuard{}, []*state.SeedPolicyRule{rule}, seedingTorrent(okTracker("kp.m-team.cc"))); ok {
		t.Error("站点不在规则范围内时不应命中")
	}
	seed := seedingTorrent(okTracker("track.btschool.org"))
	seed.Labels = []string{"HD"}
	rule2 := ratioRule(state.PolicyActionPause, 5, 0)
	rule2.Labels = []string{"HD"}
	if _, ok := evalWith(state.SeedPolicyGuard{}, []*state.SeedPolicyRule{rule2}, seed); !ok {
		t.Error("标签命中且站点范围为空时应命中")
	}
}

// 站点规则用中文站点名（与自动文件管理同一口径），中文名来自按需拉取的站点映射。
// 注意 matchesAny 是双向子串匹配，用例里的中文名不能与域名有子串关系，否则测不出差异
func TestRuleScopeMatchesChineseSiteName(t *testing.T) {
	rule := ratioRule(state.PolicyActionPause, 5, 0)
	rule.Sites = []string{"馒头站"}
	// 未传站点映射时只有主机名/announce，中文站点名匹配不上
	if _, ok := evalWith(state.SeedPolicyGuard{}, []*state.SeedPolicyRule{rule}, seedingTorrent(okTracker("kp.m-team.cc"))); ok {
		t.Error("没有站点名映射时不应按中文名命中")
	}
	names := map[int64][]string{7: {"馒头站", "另一站"}}
	if _, ok := evalWithNames(state.SeedPolicyGuard{}, []*state.SeedPolicyRule{rule}, seedingTorrent(okTracker("kp.m-team.cc")), names); !ok {
		t.Error("站点映射里包含该中文名时应命中")
	}
	// 保护栏的排除站点同样吃这份映射
	guard := state.SeedPolicyGuard{ExcludeSites: []string{"馒头站"}}
	if _, ok := evalWithNames(guard, []*state.SeedPolicyRule{ratioRule(state.PolicyActionPause, 5, 0)}, seedingTorrent(okTracker("kp.m-team.cc")), names); ok {
		t.Error("命中排除站点时不应进入任何规则")
	}
}

func TestFirstMatchingRuleWins(t *testing.T) {
	loose := ratioRule(state.PolicyActionDeleteData, 2, 0)
	loose.ID = "loose"
	loose.Name = "宽松"
	strict := ratioRule(state.PolicyActionPause, 5, 0)
	strict.ID = "strict"
	strict.Name = "严格"
	item, ok := evalWith(state.SeedPolicyGuard{}, []*state.SeedPolicyRule{loose, strict}, seedingTorrent(okTracker("kp.m-team.cc")))
	if !ok || item.rule.ID != "loose" {
		t.Errorf("应按配置顺序命中第一条，得到 %+v", item.rule)
	}
}

func TestRuleWithoutConditionNeverHits(t *testing.T) {
	// 三个达标条件全 0 等于「命中所有已完成种子」，引擎侧直接拒绝，接口层也会拦
	if _, ok := reached(ratioRule(state.PolicyActionDelete, 0, 0), seedingTorrent(okTracker("x"))); ok {
		t.Error("无达标条件的规则不应命中")
	}
}

func TestPreviewEqualIgnoresTime(t *testing.T) {
	previewOf := func(ts int64, actual float64) []state.SeedPolicyLog {
		return []state.SeedPolicyLog{{
			Time: ts, Rule: "规则一", Torrent: "A", Site: "kp.m-team.cc",
			Action: state.PolicyActionPause, DryRun: true,
			Reason: []state.SeedPolicyReasonPart{{Kind: "ratio", Actual: actual, Target: 5}},
		}}
	}
	if !previewEqual(previewOf(100, 6), previewOf(200, 6)) {
		t.Error("仅写入时间不同应视为清单未变，避免每分钟无意义写盘")
	}
	if previewEqual(previewOf(100, 6), append(previewOf(200, 6), state.SeedPolicyLog{Torrent: "B", DryRun: true})) {
		t.Error("待办清单增加条目应判定为已变化")
	}
	if previewEqual(previewOf(100, 6), previewOf(100, 7)) {
		t.Error("达标依据变化应判定为已变化")
	}
	if previewEqual([]state.SeedPolicyLog{{Torrent: "A", Action: state.PolicyActionPause}}, previewOf(100, 6)) {
		t.Error("历史记录不属于预览清单，应判定为已变化")
	}
}

func TestAppendLogsReplacesPreviewOnly(t *testing.T) {
	old := []state.SeedPolicyLog{{Torrent: "历史执行", Action: state.PolicyActionPause}}
	preview1 := []state.SeedPolicyLog{{Torrent: "预览A", DryRun: true}}
	afterPreview := appendLogs(old, preview1, true)
	if len(afterPreview) != 2 || afterPreview[1].Torrent != "历史执行" {
		t.Fatalf("预览应保留历史记录并追加新预览，得到 %+v", afterPreview)
	}
	afterPreview2 := appendLogs(afterPreview, []state.SeedPolicyLog{{Torrent: "预览B", DryRun: true}}, true)
	for _, l := range afterPreview2 {
		if l.Torrent == "预览A" {
			t.Error("新一轮预览应替换旧预览，避免每分钟堆积")
		}
	}
	executed := appendLogs(afterPreview2, []state.SeedPolicyLog{{Torrent: "真执行", Action: state.PolicyActionDelete}}, false)
	if executed[0].Torrent != "真执行" {
		t.Errorf("执行记录应置于队首，得到 %+v", executed[0])
	}
	if len(executed) != len(afterPreview2)+1 {
		t.Error("非预览轮次不应丢弃已有记录")
	}
}

func TestAppendLogsTrims(t *testing.T) {
	var old []state.SeedPolicyLog
	for i := 0; i < logLimit; i++ {
		old = append(old, state.SeedPolicyLog{Torrent: "x"})
	}
	out := appendLogs(old, []state.SeedPolicyLog{{Torrent: "new"}}, false)
	if len(out) != logLimit {
		t.Fatalf("记录条数应封顶 %d，得到 %d", logLimit, len(out))
	}
	if out[0].Torrent != "new" {
		t.Error("截断应保留最新记录")
	}
}
