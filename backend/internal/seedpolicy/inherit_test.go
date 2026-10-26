package seedpolicy

import (
	"testing"

	"github.com/sushazhi/seedark/backend/internal/state"
)

// 站点要求只在 SiteProfile 里配一次，规则留 0 的字段继承它——
// 这条约束是「日历说可释放、策略却仍不达标」不再出现的保证。
func TestEffectiveRuleInheritsSiteProfile(t *testing.T) {
	profiles := []state.SiteProfile{
		{Name: "M-Team", Match: "m-team", MinSeedDays: 7, MinRatio: 2.5, Enabled: true},
	}
	sites := []string{"tracker.m-team.io"}

	// ① 规则自身不填阈值 + 开启继承 → 全部取站点值
	rule := &state.SeedPolicyRule{InheritSite: true, Action: "pause"}
	er := effectiveRule(rule, profiles, sites)
	if er.MinSeedDays != 7 || er.MinRatio != 2.5 {
		t.Fatalf("应继承站点要求，得到 days=%v ratio=%v", er.MinSeedDays, er.MinRatio)
	}
	// 不得改动持久化配置（effectiveRule 返回副本）
	if rule.MinSeedDays != 0 || rule.MinRatio != 0 {
		t.Error("effectiveRule 不得写回原始规则")
	}

	// ② 规则自身填了值 → 规则优先，未填的字段仍继承
	rule2 := &state.SeedPolicyRule{InheritSite: true, MinRatio: 1.0}
	er2 := effectiveRule(rule2, profiles, sites)
	if er2.MinRatio != 1.0 || er2.MinSeedDays != 7 {
		t.Fatalf("规则值应优先，得到 ratio=%v days=%v", er2.MinRatio, er2.MinSeedDays)
	}

	// ③ 未开启继承 → 变化前行为（0 就是「该条件不参与判断」）
	rule3 := &state.SeedPolicyRule{}
	if got := effectiveRule(rule3, profiles, sites); got.MinSeedDays != 0 || got.MinRatio != 0 {
		t.Error("未开启继承时不应改动阈值，既有规则行为必须不变")
	}

	// ④ 站点匹配不上 → 不继承
	rule4 := &state.SeedPolicyRule{InheritSite: true}
	if got := effectiveRule(rule4, profiles, []string{"other.example.org"}); got.MinSeedDays != 0 {
		t.Error("站点不匹配时不应继承")
	}

	// ⑤ 停用的站点规则不参与（与 MatchSiteProfile 一致）
	off := []state.SiteProfile{{Name: "Off", Match: "m-team", MinSeedDays: 9, Enabled: false}}
	if got := effectiveRule(rule4, off, sites); got.MinSeedDays != 0 {
		t.Error("停用的站点规则不应被继承")
	}
}

// 站点规则的匹配必须只有一处实现：保种台与做种策略走同一个函数。
func TestMatchSiteProfilePrefersLongestMatch(t *testing.T) {
	profiles := []state.SiteProfile{
		{Name: "team", Match: "team", MinSeedDays: 1, Enabled: true},
		{Name: "M-Team", Match: "m-team", MinSeedDays: 7, Enabled: true},
	}
	p, ok := state.MatchSiteProfile(profiles, "tracker.m-team.io")
	if !ok || p.Name != "M-Team" {
		t.Fatalf("应取匹配串最长的一条，得到 %+v ok=%v", p, ok)
	}
}
