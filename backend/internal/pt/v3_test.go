package pt

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/sushazhi/seedark/backend/internal/models"
	"github.com/sushazhi/seedark/backend/internal/state"
)

// v3 口径的单测：稀缺度、时魔、义务判定、淘汰排序。
// 这些数字会被用户拿去决定「删哪颗」，所以每一个分档边界都要钉住。

func TestSwarmOf(t *testing.T) {
	cases := []struct {
		seeders   int
		threshold int
		want      Swarm
	}{
		{0, 10, SwarmUnknown}, // 拿不到 scrape：未知，不能当稀缺也不能当充足
		{-1, 10, SwarmUnknown},
		{1, 10, SwarmScarce},
		{9, 10, SwarmScarce},  // 阈值是「低于即稀缺」
		{10, 10, SwarmNormal}, // 刚好等于阈值 → 一般
		{49, 10, SwarmNormal},
		{50, 10, SwarmAbundant}, // 5 倍阈值起算充足
		{500, 10, SwarmAbundant},
		{3, 0, SwarmScarce}, // 阈值未配置时退回默认 10
	}
	for _, c := range cases {
		if got := swarmOf(c.seeders, c.threshold); got != c.want {
			t.Errorf("swarmOf(%d, %d) = %s, want %s", c.seeders, c.threshold, got, c.want)
		}
	}
}

func TestScarcityOf(t *testing.T) {
	if got := scarcityOf(0); got != 100 {
		t.Errorf("未知应给满分风险 100，got %d", got)
	}
	if a, b := scarcityOf(2), scarcityOf(20); a <= b {
		t.Errorf("做种少的稀缺度应更高：2 人=%d, 20 人=%d", a, b)
	}
	if a, b := scarcityOf(80), scarcityOf(83); a != b && a-b > 2 {
		t.Errorf("人多之后斜率应趋平：80 人=%d, 83 人=%d", a, b)
	}
	if got := scarcityOf(1); got != 100 {
		// 1 人：100/(1+1/3) ≈ 75，不该到 100（100 是「未知」专用）
		t.Logf("1 人稀缺度 = %d", got)
	}
}

func TestAgeOf(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	// 站点发布早于本地添加：取发布日期，来源标 created
	tr := &models.Torrent{DateCreated: now.Unix() - 30*86400, AddedDate: now.Unix() - 2*86400}
	days, src := ageOf(tr, now)
	if src != AgeCreated {
		t.Errorf("来源 = %s, want %s", src, AgeCreated)
	}
	if days < 29.9 || days > 30.1 {
		t.Errorf("年龄 = %.2f 天, want ≈30", days)
	}
	// 本地添加早于发布日期（少见：客户端记录更早）→ 取更早那个并标 client
	tr2 := &models.Torrent{DateCreated: now.Unix() - 2*86400, AddedDate: now.Unix() - 30*86400}
	days2, src2 := ageOf(tr2, now)
	if src2 != AgeClient || days2 < 29.9 {
		t.Errorf("ageOf(added 更早) = (%.2f, %s), want (≈30, client)", days2, src2)
	}
	// 都没有 → 退到已做种时长
	tr3 := &models.Torrent{SecondsSeeding: 5 * 86400}
	if days3, src3 := ageOf(tr3, now); src3 != AgeClient || days3 != 5 {
		t.Errorf("ageOf(无时间戳) = (%.2f, %s), want (5, client)", days3, src3)
	}
}

// seedTr 构造一颗「正在做种」的种子：statusOf 只在 PercentDone≥1 且 Status≠0
// 且（做过种或有上传速率）时才判 seeding——差值口径对非做种状态一律给 0，
// 所以测试里的种子必须先过这道门。
func seedTr(size int64) *models.Torrent {
	return &models.Torrent{TotalSize: size, PercentDone: 1, Status: 6, SecondsSeeding: 86400}
}

func TestBonusOf(t *testing.T) {
	// 未配置任何口径：数值一律 0，绝不拿内核默认值假装准确；
	// 但口径文案要给一句「去哪儿录」——空文案会让界面只剩一个「—」，用户不知道该怎么修
	p := state.SiteProfile{}
	tr := seedTr(10 << 30)
	if h, g, m, k := bonusOf(tr, p, 30, 5, bonusCtx{}); h != 0 || g != 0 || m == "" || k != MethodNone {
		t.Errorf("未配置时应为 0 且带说明文案/none，got (%v, %v, %q, %q)", h, g, m, k)
	}

	// nexusphp：没有公式常数但录了「每种固定加成」——只算那一项，并明说没算别的
	pFixed := state.SiteProfile{BonusEngine: state.BonusEngineNexusPHP, BonusPerSeeding: 2}
	hFixed, gFixed, _, kFixed := bonusOf(seedTr(10<<30), pFixed, 30, 5, bonusCtx{})
	if hFixed != 2 || kFixed != MethodStale || gFixed <= 0 {
		t.Errorf("只录每种加成时 = (%v, %v, %s)，want (2, >0, %s)", hFixed, gFixed, kFixed, MethodStale)
	}

	// 非做种状态（暂停）：站方 SQL 是 peers.seeder='yes'，站点不给它计时魔，
	// 面板也不给「假如它在做种会值多少」的虚数
	paused := &models.Torrent{TotalSize: 10 << 30} // Status=0 → paused
	pFull := state.SiteProfile{BonusT0Weeks: 4, BonusN0: 7, BonusB0: 100, BonusL: 300, BonusPerSeeding: 1, BonusMaxSeeding: 7}
	if h, _, m, k := bonusOf(paused, pFull, 30, 5, bonusCtx{hasParams: true, sumA: 1, count: 1, perCount: 1}); h != 0 || k != MethodNone || m == "" {
		t.Errorf("暂停的种子应 0 且带说明，got (%v, %q, %s)", h, m, k)
	}

	// 单颗站（count=1，且这颗就在数量项里）：loss = 曲线项 + perseeding，随周龄/体积/稀有度单调
	ctxOf := func(tr *models.Torrent, age float64, seeders int) bonusCtx {
		return bonusCtx{hasParams: true, sumA: aValueOf(tr, pFull, age, seeders), count: 1, perCount: 1}
	}
	young, _, _, _ := bonusOf(seedTr(10<<30), pFull, 1, 5, ctxOf(seedTr(10<<30), 1, 5))
	old, _, _, _ := bonusOf(seedTr(10<<30), pFull, 200, 5, ctxOf(seedTr(10<<30), 200, 5))
	if !(old > young) {
		t.Errorf("老种的删种损失应更高：young=%.4f old=%.4f", young, old)
	}
	small, _, _, _ := bonusOf(seedTr(1<<30), pFull, 30, 5, ctxOf(seedTr(1<<30), 30, 5))
	big, _, _, _ := bonusOf(seedTr(100<<30), pFull, 30, 5, ctxOf(seedTr(100<<30), 30, 5))
	if !(big > small) {
		t.Errorf("大体积的删种损失应更高：small=%.4f big=%.4f", small, big)
	}
	few, _, _, _ := bonusOf(seedTr(10<<30), pFull, 30, 1, ctxOf(seedTr(10<<30), 30, 1))
	many, _, _, _ := bonusOf(seedTr(10<<30), pFull, 30, 50, ctxOf(seedTr(10<<30), 30, 50))
	if !(few > many) {
		t.Errorf("做种少的删种损失应更高：few=%.4f many=%.4f", few, many)
	}
	// 口径必须写清是「删掉损失」并带对账锚点（A 值），不能让人当成站方总时魔
	if _, _, method, key := bonusOf(seedTr(10<<30), pFull, 30, 5, ctxOf(seedTr(10<<30), 30, 5)); key != MethodNPHP || method == "" {
		t.Errorf("NPHP 档应给出口径文案与键，got (%q, %q)", method, key)
	}
}

// TestBonusOfManual 手工抄录档：站点时魔按本站做种体积均摊。
// 这是唯一能与站方对账的口径，也是「每站规则不一样」时所有站的兜底。
func TestBonusOfManual(t *testing.T) {
	p := state.SiteProfile{BonusEngine: state.BonusEngineManual, BonusManualPerHour: 60}
	tr := &models.Torrent{TotalSize: 10 << 30}

	// 本站做种 300GB → 60/h ÷ 300GB = 0.2 点/GB·h
	hour, gb, method, key := bonusOf(tr, p, 30, 5, bonusCtx{siteSeedBytes: 300 << 30})
	if hour != 60 {
		t.Errorf("每小时应等于抄录值 60，got %v", hour)
	}
	if gb < 0.199 || gb > 0.201 {
		t.Errorf("时魔·GB 应约 0.2，got %v", gb)
	}
	if method == "" || key != MethodSite {
		t.Errorf("口径应为 site 档，got (%q, %q)", method, key)
	}

	// 拿不到本站做种体积：不编分母。抄录的 60/h 是**站点总量**，摊不到单颗，
	// 所以这里两个数都给 0，只在口径文案里说明原因——否则按行返回 60
	// 会被下游（MCP 工具、表格求和）当成「每颗 60」，累加出荒唐的值。
	// 站点总量本身在概览的 bonusCap（perHour + engine）里如实显示。
	hour2, gb2, method2, key2 := bonusOf(tr, p, 30, 5, bonusCtx{})
	if hour2 != 0 || gb2 != 0 || key2 != MethodSite || method2 == "" {
		t.Errorf("无体积时应不给单颗数（口径里说明原因），got (%v, %v, %q, %s)", hour2, gb2, method2, key2)
	}

	// 没抄录 = 没得算
	if _, _, _, k := bonusOf(tr, state.SiteProfile{BonusEngine: state.BonusEngineManual}, 30, 5, bonusCtx{siteSeedBytes: 300 << 30}); k != MethodNone {
		t.Errorf("未抄录时应为 none，got %s", k)
	}
}

// TestBonusEngineNone 「该站没有时魔」与「没录」要能区分，但都不出数。
func TestBonusEngineNone(t *testing.T) {
	p := state.SiteProfile{BonusEngine: state.BonusEngineNone, BonusPerSeeding: 3}
	if h, g, method, key := bonusOf(seedTr(10<<30), p, 30, 5, bonusCtx{siteSeedBytes: 300 << 30}); h != 0 || g != 0 || method == "" || key != MethodNone {
		t.Errorf("none 档应为 0 且带说明，got (%v, %v, %q, %q)", h, g, method, key)
	}
	// 未知引擎值一律当 none（宁可不显示，也不套错公式）
	if got := state.BonusEngineOf(state.SiteProfile{BonusEngine: "unit3d"}); got != state.BonusEngineNone {
		t.Errorf("未知引擎应回落 none，got %s", got)
	}
	// v3 老配置（没写引擎）：语义不变，仍按 NPHP
	if got := state.BonusEngineOf(state.SiteProfile{BonusT0Weeks: 4}); got != state.BonusEngineNexusPHP {
		t.Errorf("空引擎应回落 nexusphp，got %s", got)
	}
}

func TestBonusCap(t *testing.T) {
	if _, _, ok := bonusCap(state.SiteProfile{}); ok {
		t.Error("未配置时 Configured 应为 false")
	}
	p := state.SiteProfile{BonusT0Weeks: 4, BonusN0: 7, BonusB0: 100, BonusL: 300, BonusPerSeeding: 1, BonusMaxSeeding: 7}
	cap, engine, ok := bonusCap(p)
	if !ok || cap != 107 || engine != state.BonusEngineNexusPHP {
		t.Errorf("bonusCap = (%v, %v, %v), want (107, nexusphp, true)", cap, engine, ok)
	}
	// manual 档：抄录值本身就是可显示的量级（捐赠倍数已含在抄录值里，不再乘）
	m := state.SiteProfile{BonusEngine: state.BonusEngineManual, BonusManualPerHour: 42}
	if cap, engine, ok := bonusCap(m); !ok || cap != 42 || engine != state.BonusEngineManual {
		t.Errorf("manual bonusCap = (%v, %v, %v), want (42, manual, true)", cap, engine, ok)
	}
	// 捐赠者：公式档上限整体乘 N
	d := p
	d.BonusDonorTimes = 2
	if cap, _, _ := bonusCap(d); cap != 214 {
		t.Errorf("捐赠者 bonusCap = %v, want 214", cap)
	}
}

// TestBonusExtraPct 加成百分比必须是**加法**叠加：M-Team 页面实测
// 基本 41.178 + 府尹 6%(2.471) + 二級驗證 10%(4.117) = 47.767。
// 如果误写成乘法 (1.06×1.10)=1.166，会得到 48.01，对不上账。
func TestBonusExtraPct(t *testing.T) {
	p := state.SiteProfile{BonusT0Weeks: 4, BonusN0: 7, BonusB0: 100, BonusL: 300, BonusPerSeeding: 1, BonusMaxSeeding: 7}
	p.BonusExtraPct = 16
	if got := bonusTotalFrom(p, 300, 1); math.Abs(got-51*1.16) > 1e-9 {
		t.Errorf("加成 16%% 应得 %.4f, got %.4f（是不是写成乘法了？）", 51*1.16, got)
	}
	// 边际差值同样吃加成：删一颗损失的时魔也按加成后的口径算
	p0 := p
	p0.BonusExtraPct = 0
	a := 10.0
	loss0 := bonusLossOf(p0, 300, 1, a, true)
	loss16 := bonusLossOf(p, 300, 1, a, true)
	if math.Abs(loss16-loss0*1.16) > 1e-9 {
		t.Errorf("边际损失也应 ×1.16: %.6f vs %.6f", loss16, loss0*1.16)
	}
	// 上限同样乘加成
	if cap, _, _ := bonusCap(p); math.Abs(cap-107*1.16) > 1e-9 {
		t.Errorf("上限应 ×1.16 = %.4f, got %.4f", 107*1.16, cap)
	}
}

// TestBonusMTeamReplay 用 M-Team 的真实参数复现页面上的那一行：
//
//	基本獎勵 41.178 = atan 项 36.178 + 每种加成 0.1×50＝5
//	府尹 6% → 2.471、二級驗證 10% → 4.117，合计 47.767
//
// 这个用例同时钉住「为什么预估看起来对不上」的两个坑：
//  1. 基本獎勵**含** perseeding×maxseeding，拿纯 atan 项去比会差正好 5 点；
//  2. 加成是加法再乘基本值，写成 1.06×1.10 会得到 48.01（多 0.5%）。
func TestBonusMTeamReplay(t *testing.T) {
	// 站点页能抄到的值（2026-11 实测），A 值页面显示 (A = xx.x)
	const (
		pageBasic = 41.178 // 「基本獎勵」
		pageTotal = 47.767 // 「您目前每小時合計可獲得」
		observedA = 646.9  // (A = xx.x) 的抄录值
		tolerance = 0.05   // 页面只给 3 位小数，且 A 抄到 0.1，允许 0.05 的绝对差
	)
	p := state.SiteProfile{
		BonusB0: 50, BonusL: 300, BonusT0Weeks: 4, BonusN0: 7,
		BonusPerSeeding: 0.1, BonusMaxSeeding: 50,
		BonusExtraPct: 16,
	}
	// ① 基本奖励 = atan 项 + 0.1×50：先验证「不含加成」的那层。
	//    颗数必须按 ≥maxseeding 传，否则只加 0.1 而不是 5。
	base := p
	base.BonusExtraPct = 0
	basePerHour := bonusTotalFrom(base, observedA, 50)
	if math.Abs(basePerHour-pageBasic) > tolerance {
		t.Errorf("基本獎勵复刻 = %.3f, want ≈%.3f（差得远说明 L 或 perseeding 录错了）", basePerHour, pageBasic)
	}
	// ② 加成后的合计
	if got := bonusTotalFrom(p, observedA, 50); math.Abs(got-pageTotal) > tolerance {
		t.Errorf("加成 16%% 后 = %.3f, want ≈%.3f", got, pageTotal)
	}
	// ③ 乘法式加成会明显偏高 —— 「对不上账」最常见的误配
	if mul := basePerHour * 1.06 * 1.10; mul-pageTotal < 0.2 {
		t.Errorf("1.06×1.10 应明显高于页面值 47.767，got %.3f", mul)
	}
	// ④ 基本獎勵里那 5 点就是 perseeding×maxseeding，不能漏
	atanOnly := p.BonusB0 * (2 / math.Pi) * math.Atan(observedA/p.BonusL)
	if diff := pageBasic - atanOnly; math.Abs(diff-5) > tolerance {
		t.Errorf("基本獎勵 − atan 项 = %.3f, want ≈5（= perseeding×maxseeding）", diff)
	}
}

// TestBonusTotalFrom 复刻站点总时魔 B：这是能与 mybonus.php 页面显示值直接对账的量。
func TestBonusTotalFrom(t *testing.T) {
	p := state.SiteProfile{BonusT0Weeks: 4, BonusN0: 7, BonusB0: 100, BonusL: 300, BonusPerSeeding: 1, BonusMaxSeeding: 7}
	// ΣA 恰等于 L：atan(1)=π/4 → B = 100×(2/π)×(π/4) + 1 = 51
	if got := bonusTotalFrom(p, 300, 1); math.Abs(got-51) > 1e-9 {
		t.Errorf("B(ΣA=300, c=1) = %v, want 51", got)
	}
	// atan 饱和：ΣA 巨大时 B 贴着 B0 + perseeding×c，永不超上限
	if got := bonusTotalFrom(p, 1e12, 3); got > 103.001 || got < 102.999 {
		t.Errorf("B 应饱和在 103 附近，got %v", got)
	}
	// 颗数超 maxseeding：perseeding 只计满 7 颗——第 8 颗起对时魔零贡献
	if got := bonusTotalFrom(p, 0, 99); math.Abs(got-7) > 1e-9 {
		t.Errorf("B(0, 99) = %v, want 7（perseeding 计满 7 颗）", got)
	}
	// 捐赠倍数只作用于公式档的整体输出
	d := p
	d.BonusDonorTimes = 2
	if got := bonusTotalFrom(d, 300, 1); math.Abs(got-102) > 1e-9 {
		t.Errorf("捐赠者 B(300,1) = %v, want 102", got)
	}
}

// TestBonusLossOf 「删掉这颗损失多少」的精确差值——单颗时魔唯一站得住的读法。
func TestBonusLossOf(t *testing.T) {
	p := state.SiteProfile{BonusT0Weeks: 4, BonusN0: 7, BonusB0: 100, BonusL: 300, BonusPerSeeding: 1, BonusMaxSeeding: 7}
	a := 40.0
	// 站内只有这一颗：删掉它 = 时魔归零，损失应恰好等于全部 B
	loss := bonusLossOf(p, a, 1, a, true)
	if b := bonusTotalFrom(p, a, 1); math.Abs(loss-b) > 1e-9 {
		t.Errorf("单颗站的删种损失应等于全部时魔：loss=%v B=%v", loss, b)
	}
	// 颗数超上限后，删一颗不再损失 perseeding 项（只损失 atan 项），差恰好是 1
	withPer := bonusLossOf(p, a, 7, a, true)
	noPer := bonusLossOf(p, a, 8, a, true)
	if math.Abs((withPer-noPer)-1) > 1e-9 {
		t.Errorf("超上限前后删种损失应差 perseeding=1：withPer=%v noPer=%v", withPer, noPer)
	}
	// 数量项口径：站点只数官种时，删掉一颗**非官种**不损失「每种加成」
	if off := bonusLossOf(p, a, 3, a, false); math.Abs(off-(withPer-1)) > 1e-9 {
		t.Errorf("非官种的删种损失不该含 perseeding：got %v want %v", off, withPer-1)
	}
	// 多颗时：损失落在 (0, B) 内——atan 饱和把边际压小，但它恒为正
	big := bonusLossOf(p, 3000, 5, a, true)
	if b := bonusTotalFrom(p, 3000, 5); !(big > 0 && big < b) {
		t.Errorf("边际损失应落在 (0, B) 内：loss=%v B=%v", big, b)
	}
	// 捐赠倍数整体乘
	d := p
	d.BonusDonorTimes = 2
	if math.Abs(bonusLossOf(d, a, 1, a, true)-2*loss) > 1e-9 {
		t.Errorf("捐赠者的删种损失应翻倍：got %v want %v", bonusLossOf(d, a, 1, a, true), 2*loss)
	}
}

// TestBonusLogCurve Springsunday（春天）用的不是原版 atan 曲线，而是 log —— 2026-11 实测。
//
// 证据是同一组常数下的两条独立用户行，只有 log 能同时命中：
//
//	我的数据      A = 46.962   → B值 5.280（页面）
//	全站前 5%     A = 5120.463 → B值 131.180（页面）
//
// 代进 atan 式分别得 3.53 / 48.72，差 33% / 63%——所以这站不能按 nexusphp 默认曲线算
// （B0=51.5 的 atan 项上限就是 51.5，A 再大也到不了页面上的 131.180）。
// 顺带钉住站点页上另外两条与内核不同之处：每种加成只数「≥3GB 的官种」（基础积分
// 8.800 = 0.4×22，而官种有 23 颗），加成 16% 是加法叠加后再乘。
func TestBonusLogCurve(t *testing.T) {
	// 站点页给的是 N0=12、E=51.5、D=435：E 是曲线尺度（落在 B0 槽位），
	// D 是 A 的刻度（落在 L 槽位）；T0 页面没印，按内核默认 4 周（需用 A 对账复核）。
	p := state.SiteProfile{
		BonusEngine:     state.BonusEngineNexusPHP,
		BonusCurve:      state.BonusCurveLog,
		BonusT0Weeks:    4,
		BonusN0:         12,
		BonusB0:         51.5,
		BonusL:          435,
		BonusPerSeeding: 0.4,
		BonusMaxSeeding: 50,
		BonusExtraPct:   16,
	}
	// ① 曲线项逐点命中页面「B值」
	for _, c := range []struct{ sumA, want float64 }{
		{46.962, 5.280},
		{5120.463, 131.180},
	} {
		if got := curveTerm(p, c.sumA); math.Abs(got-c.want) > 0.005 {
			t.Errorf("log 曲线 B(%.3f) = %.4f, want ≈%.3f", c.sumA, got, c.want)
		}
	}
	// ② 整行复刻：基础 0.4×22 = 8.8，加曲线项 5.2797 → 14.0797，×1.16 = 16.3325
	//    （页面印 16.341：站点内部用的是未四舍五入的 A 与 B，差 0.05%）
	if got := bonusTotalFrom(p, 46.962, 22); math.Abs(got-16.3325) > 0.002 {
		t.Errorf("整行复刻 = %.4f, want ≈16.3325（8.8 + 5.2797 再 ×1.16；页面印 16.341）", got)
	}
	// ③ 同一条 A 代进 atan 曲线算不中：说明两条曲线不可互替，不是「参数没调好」
	atanP := p
	atanP.BonusCurve = state.BonusCurveAtan
	if got := curveTerm(atanP, 46.962); math.Abs(got-5.280) < 1 {
		t.Errorf("atan 曲线竟也得到 %.3f ≈ 5.280，本用例失去区分力", got)
	}
	// ④ log 无上限：A 到 10×L 时曲线项已超过 B0（atan 曲线在 A=L 时恰好 B0/2、
	//    10L 时 0.936×B0，但无论 A 多大都越不过 B0）
	if got := curveTerm(p, 10*p.BonusL); got <= p.BonusB0 {
		t.Errorf("log 曲线应能超过 B0（无上限）：B=%.3f B0=%.1f", got, p.BonusB0)
	}
	// ⑤ 上限字段必须如实留空：log 站没有「封顶值」，编一个数比不写更糟
	if cap, engine, ok := bonusCap(p); !ok || cap != 0 || engine != state.BonusEngineNexusPHP {
		t.Errorf("log 站的 bonusCap 应为 (0, nexusphp, true)，got (%v, %v, %v)", cap, engine, ok)
	}
}

// TestBonusAModelLog Springsunday 的 A 值形态：时间项 0.25+0.32·ln(1+周龄)、
// 稀缺项 0.12+2.88·10^(−⌈(做种人数−1)/5⌉/(N0−1))（2026-11 站点页公式）。
//
// 页面 A=46.962 是「我在做种的那些种子」的求和，单测里没有那份集合，无法直接复现；
// 所以这里钉住每个乘子的取值与档位边界，整集合对账仍走 BonusASumObserved。
func TestBonusAModelLog(t *testing.T) {
	// ① 时间项：0 周 = 0.25，1 周 = 0.25+0.32·ln2，100 周 = 0.25+0.32·ln101
	for _, c := range []struct{ days, want float64 }{
		{0, 0.25},
		{7, 0.25 + 0.32*math.Ln2},
		{700, 0.25 + 0.32*math.Log(101)},
	} {
		if got := logAgeOf(c.days); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("logAgeOf(%.0f 天) = %.6f, want %.6f", c.days, got, c.want)
		}
	}
	// 无上限：1000 周仍在增长（原版那一项早就贴住 1 了），但增长很慢
	if logAgeOf(7000) <= logAgeOf(700) || logAgeOf(7000) > 3 {
		t.Errorf("周龄项应继续增长且涨得慢：100 周=%.4f 1000 周=%.4f", logAgeOf(700), logAgeOf(7000))
	}

	// ② 稀缺项：1 人拿满 3.0；档位按 1/6/11…（⌈(N−1)/5⌉），档内相同、过档才降
	if got := steppedRarityOf(1, 12); math.Abs(got-3) > 1e-9 {
		t.Errorf("1 人做种应拿满 3.0，got %.6f", got)
	}
	if steppedRarityOf(2, 12) != steppedRarityOf(6, 12) {
		t.Errorf("2 人与 6 人在同一档，应相等：%.6f vs %.6f", steppedRarityOf(2, 12), steppedRarityOf(6, 12))
	}
	if steppedRarityOf(6, 12) == steppedRarityOf(7, 12) {
		t.Errorf("6→7 人应跨档（⌈5/5⌉=1 → ⌈6/5⌉=2）")
	}
	if got := steppedRarityOf(600, 12); got <= 0.12 || got > 0.13 {
		t.Errorf("人极多时应趋近 0.12，got %.6f", got)
	}

	p := state.SiteProfile{
		BonusEngine:     state.BonusEngineNexusPHP,
		BonusCurve:      state.BonusCurveLog,
		BonusAModel:     state.BonusAModelLog,
		BonusN0:         12,
		BonusB0:         51.5,
		BonusL:          435,
		BonusPerSeeding: 0.4,
		BonusMaxSeeding: 50,
	}
	// ③ 单颗 A：10GB、刚发布、1 人做种 → 10 × 0.25 × 3.0 = 7.5
	tr := &models.Torrent{TotalSize: 10 << 30}
	if got := aValueOf(tr, p, 0, 1); math.Abs(got-7.5) > 1e-9 {
		t.Errorf("单颗 A = %.6f, want 7.5", got)
	}
	// ④ 与 classic 形态必须分得开：同样累积做种 100 周、5 人，对数形态更高（时间项不封顶）
	pc := p
	pc.BonusAModel = state.BonusAModelClassic
	pc.BonusT0Weeks = 4
	old := &models.Torrent{TotalSize: 10 << 30, SecondsSeeding: 700 * 86400}
	if aValueOf(old, p, 700, 5) <= aValueOf(old, pc, 700, 5) {
		t.Errorf("对数形态的老种 A 应更高：log=%.4f classic=%.4f", aValueOf(old, p, 700, 5), aValueOf(old, pc, 700, 5))
	}

	// ⑤ 时间口径：站点原文说 Ti 是「该种子的累积做种时间」，不是发布至今。
	//    所以 log 形态取 SecondsSeeding（7 天 = 1 周），不受 ageDays 影响。
	oneWeek := &models.Torrent{TotalSize: 10 << 30, SecondsSeeding: 7 * 86400}
	if got, want := aValueOf(oneWeek, p, 0, 1), 10*logAgeOf(7)*3; math.Abs(got-want) > 1e-9 {
		t.Errorf("log 形态应按 SecondsSeeding 计时：got %.6f want %.6f", got, want)
	}
	if aValueOf(oneWeek, p, 9999, 1) != aValueOf(oneWeek, p, 0, 1) {
		t.Error("log 形态不该受「发布至今」的 ageDays 影响")
	}

	// ⑥ T0 只在 classic 形态需要：对数形态 T0=0 仍算参数齐全，classic 缺 T0 则不算
	noT0 := p
	noT0.BonusT0Weeks = 0
	if _, ok := bonusParamsOf(noT0); !ok {
		t.Error("对数形态不需要 T0，参数应算齐全")
	}
	classicNoT0 := pc
	classicNoT0.BonusT0Weeks = 0
	if _, ok := bonusParamsOf(classicNoT0); ok {
		t.Error("classic 形态缺 T0 不应算参数齐全")
	}

	// ⑦ 只换 A 形态不该动曲线：同一 ΣA 下 B 仍按 log 曲线算
	//    （这里不设加成%，曲线项 5.2797 + 0.4×22 = 14.0797）
	if got := bonusTotalFrom(p, 46.962, 22); math.Abs(got-14.0797) > 0.002 {
		t.Errorf("A 形态不影响曲线项，整行应仍为 ≈14.0797，got %.4f", got)
	}

	// ⑧ 口径文案要写明用的是对数周龄 A、且 f 是假设，否则用户会拿它和页面 A 对账
	seed := seedTr(10 << 30)
	ctx := bonusCtx{hasParams: true, sumA: aValueOf(seed, p, 30, 5), count: 1, perCount: 1}
	if _, _, msg, key := bonusOf(seed, p, 30, 5, ctx); key != MethodNPHP || !strings.Contains(msg, "对数周龄") || !strings.Contains(msg, "f 按体积假设") {
		t.Errorf("口径应标出对数周龄 A 与 f 假设，got (%s) %q", key, msg)
	}
}

// TestBonusCapsOfConfigured 「配了口径」与「有上限」是两件事：log 曲线没有上限
// （bonusCap 返回 0），但站确实配了。拿 0 去参与取最大值的比较，Configured 会永远
// 停在 false —— 界面就会在已经算出 TotalPerHour 的情况下说「还没有站点配时魔口径」，
// 而同一屏的站点分布表又是正常数字。
func TestBonusCapsOfConfigured(t *testing.T) {
	// ① 只配了一个 log 站：没有上限，但必须算「配了」
	perHour, configured, _, _, count := bonusCapsOf(map[string]siteBonus{
		"springsunday": {perHour: 0, engine: state.BonusEngineNexusPHP},
	})
	if !configured || count != 1 || perHour != 0 {
		t.Errorf("只配 log 站时应为 (上限 0, configured true, 1 站)，got (%v, %v, %v)", perHour, configured, count)
	}
	// ② log 站与 atan 站混在一起：上限取 atan 那条，配站数仍是 2
	perHour, configured, best, _, count := bonusCapsOf(map[string]siteBonus{
		"logsite":  {perHour: 0, engine: state.BonusEngineNexusPHP},
		"atansite": {perHour: 63.8, engine: state.BonusEngineNexusPHP},
	})
	if !configured || count != 2 || perHour != 63.8 || best != "atansite" {
		t.Errorf("混配时应取 atansite 的 63.8，got (%v, %v, %v, %v)", perHour, configured, best, count)
	}
	// ③ manual 站的上限就是抄录值
	perHour, _, best, engine, _ := bonusCapsOf(map[string]siteBonus{
		"a": {perHour: 42, engine: state.BonusEngineManual},
		"b": {perHour: 107, engine: state.BonusEngineNexusPHP},
	})
	if perHour != 107 || best != "b" || engine != state.BonusEngineNexusPHP {
		t.Errorf("应取 b 的 107/nexusphp，got (%v, %v, %v)", perHour, best, engine)
	}
	// ④ 一个站都没配：Configured 必须是 false（「不知道」不能冒充「0」）
	if _, configured, _, _, count := bonusCapsOf(nil); configured || count != 0 {
		t.Errorf("没配口径时不该 Configured，got (%v, %v)", configured, count)
	}
}

// TestBonusOfficialPerCount 数量项口径：站点可以只数官种、并且限体积
// （Springsunday 写的是「0.4 × 做种数，做种数最多计 50 个不小于 3GB 的官种」）。
// 计数必须按同一口径算，否则本地 B 会系统性偏高——实测那站 24 颗做种里只有 22 颗被计入。
func TestBonusOfficialPerCount(t *testing.T) {
	plain := state.SiteProfile{BonusPerSeeding: 0.4, BonusMaxSeeding: 50}
	big := &models.Torrent{Name: "Movie.2020.2160p-GROUP", TotalSize: 40 << 30}
	small := &models.Torrent{Name: "Clip.mkv", TotalSize: 1 << 30}

	// ① 没录口径 = 原版 NexusPHP：全部做种颗数都算
	if !countedForPerSeeding(big, plain, nil) || !countedForPerSeeding(small, plain, nil) {
		t.Error("没录官种口径时应数全部做种")
	}

	// ② 只数官种：正则匹配种子名，且大小写不敏感
	re, bad := compileOfficialPattern(`[@-]\s?(SUB|SUBWEB)`)
	if bad || re == nil {
		t.Fatalf("正则应能编译：bad=%v", bad)
	}
	official := &models.Torrent{Name: "Movie.2020.1080p-sub", TotalSize: 5 << 30}
	notOfficial := &models.Torrent{Name: "Movie.2020.1080p-WEB", TotalSize: 5 << 30}
	if !countedForPerSeeding(official, plain, re) {
		t.Error("匹配官种正则的应计入")
	}
	if countedForPerSeeding(notOfficial, plain, re) {
		t.Error("不匹配的不该计入")
	}

	// ③ 再叠体积门槛：不足 3GB 的官种也不算（站点那句「不小于 3GB 的官种」）
	withMin := plain
	withMin.BonusOfficialMinGB = 3
	smallOfficial := &models.Torrent{Name: "Short.2020-SUB", TotalSize: 2 << 30}
	if countedForPerSeeding(smallOfficial, withMin, re) {
		t.Error("不足 3GB 的官种不该计入")
	}
	if !countedForPerSeeding(official, withMin, re) {
		t.Error("够大的官种应计入")
	}

	// ④ 正则写错：按「不区分官种」降级 + 给出 bad 标记（静默当成 0 颗会让用户以为站点改了规则）
	if got, bad := compileOfficialPattern("(["); !bad || got != nil {
		t.Errorf("非法正则应返回 (nil, true)，got (%v, %v)", got, bad)
	}
	if got, bad := compileOfficialPattern("   "); bad || got != nil {
		t.Errorf("空串应返回 (nil, false)，got (%v, %v)", got, bad)
	}

	// ⑤ 整站复刻：24 颗做种里只有 22 颗被计入时，正好差 perseeding×2×加成
	p := state.SiteProfile{
		BonusCurve: state.BonusCurveLog, BonusT0Weeks: 4, BonusN0: 12,
		BonusB0: 51.5, BonusL: 435, BonusPerSeeding: 0.4, BonusMaxSeeding: 50,
		BonusExtraPct: 16, BonusOfficialMinGB: 3,
	}
	allSeeds := bonusTotalFrom(p, 46.962, 24)
	officialOnly := bonusTotalFrom(p, 46.962, 22)
	if math.Abs((allSeeds-officialOnly)-0.8*1.16) > 1e-9 {
		t.Errorf("两种口径应差 0.4×2×1.16 = %.4f，got %.4f", 0.8*1.16, allSeeds-officialOnly)
	}
	if math.Abs(officialOnly-16.3325) > 0.002 {
		t.Errorf("官种口径应复刻页面 16.341（本地 16.3325），got %.4f", officialOnly)
	}
}

func TestObligationMet(t *testing.T) {
	p := state.SiteProfile{MinSeedDays: 7}
	// 做够 7 天
	if !obligationMet(&models.Torrent{DoneDate: 1, SecondsSeeding: 7 * 86400}, p, true) {
		t.Error("做够 7 天应判定达标")
	}
	// 没做够、也没有豁免
	if obligationMet(&models.Torrent{DoneDate: 1, SecondsSeeding: 86400}, p, true) {
		t.Error("只做 1 天不该达标")
	}
	// H&R 豁免：上传量 ÷ **种子体积** 达到豁免分享率
	p2 := state.SiteProfile{MinSeedDays: 7, HRAmnestyRatio: 1.0}
	tr := &models.Torrent{DoneDate: 1, SecondsSeeding: 86400, TotalSize: 1 << 30, UploadedEver: 1 << 30}
	if !obligationMet(tr, p2, true) {
		t.Error("上传量达到种子体积的 1.0 倍应豁免")
	}
	// 无站点规则一律不达标（宁可显示「待定」，不乱说满足）
	if obligationMet(&models.Torrent{DoneDate: 1, SecondsSeeding: 999 * 86400}, p, false) {
		t.Error("无规则时不该判定达标")
	}
}

func TestReleaseSafety(t *testing.T) {
	if ok, note := releaseSafety(false, SwarmAbundant, 100, true); ok {
		t.Errorf("义务未满足不该安全，note=%s", note)
	}
	if ok, _ := releaseSafety(true, SwarmScarce, 3, true); ok {
		t.Error("稀缺种子即使义务满足也不该建议释放")
	}
	if ok, _ := releaseSafety(true, SwarmUnknown, 0, true); ok {
		t.Error("未知做种人数应保守处理")
	}
	if ok, _ := releaseSafety(true, SwarmAbundant, 100, false); ok {
		t.Error("缺规则时不该建议释放")
	}
	if ok, _ := releaseSafety(true, SwarmAbundant, 100, true); !ok {
		t.Error("义务满足且充足应可安全释放")
	}
}

func TestSortForEviction(t *testing.T) {
	// 跨站不可比：A 站的最小值（0.5）比 B 站的最大值（0.40）大得多，但 A 在**自己站内**
	// 是唯一一颗、B 站里 C 最便宜 —— 所以 A 必须先出。若按绝对值混排就会反过来。
	rows := []TorrentRef{
		{Hash: "A", Site: "A站", BonusPerGb: 0.5, Scarcity: 10, Size: 10, Seeders: 5},
		{Hash: "B", Site: "B站", BonusPerGb: 0.40, Scarcity: 20, Size: 50, Seeders: 5},
		{Hash: "C", Site: "B站", BonusPerGb: 0.10, Scarcity: 30, Size: 10, Seeders: 5},
		{Hash: "D", Site: "B站", BonusPerGb: 0.20, Scarcity: 5, Size: 99, Seeders: 5},
	}
	sortForEviction(rows)
	// 站内相对时魔升序：A(0) → C(0) → D(0.5) → B(1)；
	// A 与 C 同为 0 时比稀缺度：C(30) > A(10)，所以 C 在前。
	want := []string{"C", "A", "D", "B"}
	for i, h := range want {
		if rows[i].Hash != h {
			t.Fatalf("第 %d 位 = %s, want %s（顺序：%v）", i, rows[i].Hash, h, hashesOf(rows))
		}
	}
	// 位置表本身也要钉住：并列同分给同一个位置值，不会被输入顺序排出先后
	eff, known := evictionOrder(rows)
	if eff["C"] != 0 || eff["D"] != 0.5 || eff["B"] != 1 {
		t.Errorf("evictionOrder 错了：%v", eff)
	}
	// A 站只有一颗候选 → 站内名次概念不成立：给中性位置，且**不许对外断言**
	if eff["A"] != 0.5 || known["A"] {
		t.Errorf("单颗候选的站应无区分度，got (%v, %v)", eff["A"], known["A"])
	}

	// 同一站内时魔·GB 完全相同时（抄录档按体积均摊的典型情形）也不许断言名次，
	// 否则界面会对每一行都说「本站里时魔最不划算」——那是个假断言。
	tied := []TorrentRef{
		{Hash: "T1", Site: "T", BonusPerGb: 5.45, Scarcity: 10, Size: 1},
		{Hash: "T2", Site: "T", BonusPerGb: 5.45, Scarcity: 20, Size: 2},
	}
	effT, knownT := evictionOrder(tied)
	if knownT["T1"] || knownT["T2"] || effT["T1"] != 0.5 {
		t.Errorf("同站同值应无区分度，got (%v, %v)", effT, knownT)
	}
	// 无区分度时仍要能排出先后：退回稀缺度 → 体积
	sortForEviction(tied)
	if tied[0].Hash != "T2" {
		t.Fatalf("同值时应按稀缺度/体积决胜，顺序：%v", hashesOf(tied))
	}

	// 没有 hash 的行排不进位置表：map 缺失键会返回 0，若直接拿它比较就会把这行
	// 顶到最前面（最该删），与意图相反。
	noHash := []TorrentRef{
		{Hash: "", Site: "S", BonusPerGb: 0, Scarcity: 50, Size: 100},
		{Hash: "Z1", Site: "S", BonusPerGb: 9, Scarcity: 1, Size: 1},
		{Hash: "Z2", Site: "S", BonusPerGb: 0.01, Scarcity: 1, Size: 1},
	}
	sortForEviction(noHash)
	if noHash[0].Hash != "Z2" {
		t.Fatalf("站内最低的应先出，顺序：%v", hashesOf(noHash))
	}
}

func hashesOf(rows []TorrentRef) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Hash
	}
	return out
}

// TestHumanBytes 体积文案必须自动升档：预警里出现「占 21180 GB」时读者无法判断量级
// （其实是 20.68 TB）。进位边界（1023.6 GB → 1.00 TB）也要钉住——
// 否则会出现「1024 GB」这种既不升档也不好看的值。
func TestHumanBytes(t *testing.T) {
	const gb = int64(1) << 30
	cases := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1536, "1.50 KB"},
		{99 * gb, "99.00 GB"},
		{100 * gb, "100 GB"},
		{21180 * gb, "20.68 TB"},
		// 1023.6 GB 四舍五入会顶到 1024，应进档成 1.00 TB
		{102360 * gb / 100, "1.00 TB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.bytes); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.bytes, got, c.want)
		}
	}
}
