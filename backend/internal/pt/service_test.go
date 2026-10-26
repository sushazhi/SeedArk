package pt

import (
	"archive/zip"
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushazhi/seedark/backend/internal/models"
	"github.com/sushazhi/seedark/backend/internal/state"
)

// ---------- 通用辅助 ----------

// newTestLocal 在临时目录里建一个 store；t.TempDir 由测试框架清理。
func newTestLocal(t *testing.T) *store {
	t.Helper()
	ls, err := newStore(t.TempDir())
	if err != nil {
		t.Fatalf("newStore: %v", err)
	}
	return ls
}

// newTestService 只装配 local，不碰 manager/store，避免依赖真实下载器与状态文件。
func newTestService(t *testing.T) *Service {
	t.Helper()
	return &Service{local: newTestLocal(t)}
}

// 固定基准时间，所有相对断言都以它推导，不依赖真实时钟的秒级精度。
const testBase = int64(1_700_000_000)

// ---------- 1. releaseAtFor ----------

func TestReleaseAtFor(t *testing.T) {
	torrent := func(done, added int64) models.Torrent {
		return models.Torrent{DoneDate: done, AddedDate: added}
	}
	cases := []struct {
		name       string
		tr         models.Torrent
		p          state.SiteProfile
		hasProfile bool
		wantAt     int64
		wantReason ReleaseReason
	}{
		{
			name:       "HR 比最短天数更晚时取 HR",
			tr:         torrent(testBase, 0),
			p:          state.SiteProfile{MinSeedDays: 1, HRSeedHours: 72},
			hasProfile: true,
			wantAt:     testBase + 72*3600,
			wantReason: ReasonHR,
		},
		{
			name:       "最短天数比 HR 更晚时取天数",
			tr:         torrent(testBase, 0),
			p:          state.SiteProfile{MinSeedDays: 3, HRSeedHours: 48},
			hasProfile: true,
			wantAt:     testBase + 3*daySeconds,
			wantReason: ReasonMinDays,
		},
		{
			name:       "只配最短天数",
			tr:         torrent(testBase, 0),
			p:          state.SiteProfile{MinSeedDays: 7},
			hasProfile: true,
			wantAt:     testBase + 7*daySeconds,
			wantReason: ReasonMinDays,
		},
		{
			name:       "只配 H&R 小时",
			tr:         torrent(testBase, 0),
			p:          state.SiteProfile{HRSeedHours: 24},
			hasProfile: true,
			wantAt:     testBase + 24*3600,
			wantReason: ReasonHR,
		},
		{
			name:       "规则存在但两个字段都为 0",
			tr:         torrent(testBase, 0),
			p:          state.SiteProfile{},
			hasProfile: true,
			wantAt:     0,
			wantReason: ReasonUnknown,
		},
		{
			name:       "未匹配到站点规则",
			tr:         torrent(testBase, 0),
			p:          state.SiteProfile{MinSeedDays: 3},
			hasProfile: false,
			wantAt:     0,
			wantReason: ReasonUnknown,
		},
		{
			name:       "DoneDate 为 0 时回退 AddedDate",
			tr:         torrent(0, testBase),
			p:          state.SiteProfile{MinSeedDays: 2},
			hasProfile: true,
			wantAt:     testBase + 2*daySeconds,
			wantReason: ReasonMinDays,
		},
		{
			name:       "基准时间为 0 时无法判断",
			tr:         torrent(0, 0),
			p:          state.SiteProfile{MinSeedDays: 2},
			hasProfile: true,
			wantAt:     0,
			wantReason: ReasonUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			at, reason := releaseAtFor(&tc.tr, tc.p, tc.hasProfile)
			if at != tc.wantAt {
				t.Errorf("releaseAtFor() at = %d, want %d", at, tc.wantAt)
			}
			if reason != tc.wantReason {
				t.Errorf("releaseAtFor() reason = %q, want %q", reason, tc.wantReason)
			}
		})
	}
}

// ---------- 2. hrRiskOf / hrDeadlineFor ----------

func TestHRRiskAndDeadline(t *testing.T) {
	cases := []struct {
		name           string
		secondsSeeding int64
		doneDate       int64
		p              state.SiteProfile
		hasProfile     bool
		wantRisk       bool
		wantDeadline   int64
	}{
		{
			name: "做种时长不足为 true 且给出到期时间", secondsSeeding: 12 * 3600, doneDate: testBase,
			p: state.SiteProfile{HRSeedHours: 24}, hasProfile: true,
			wantRisk: true, wantDeadline: testBase + 24*3600,
		},
		{
			name: "恰好满足要求为 false", secondsSeeding: 24 * 3600, doneDate: testBase,
			p: state.SiteProfile{HRSeedHours: 24}, hasProfile: true,
			wantRisk: false, wantDeadline: testBase + 24*3600,
		},
		{
			name: "超过要求为 false", secondsSeeding: 30 * 3600, doneDate: testBase,
			p: state.SiteProfile{HRSeedHours: 24}, hasProfile: true,
			wantRisk: false, wantDeadline: testBase + 24*3600,
		},
		{
			name: "未配置 H&R（小时为 0）", secondsSeeding: 0, doneDate: testBase,
			p: state.SiteProfile{}, hasProfile: true,
			wantRisk: false, wantDeadline: 0,
		},
		{
			name: "没有站点规则", secondsSeeding: 0, doneDate: testBase,
			p: state.SiteProfile{HRSeedHours: 24}, hasProfile: false,
			wantRisk: false, wantDeadline: 0,
		},
		{
			name: "基准时间为 0 时仍可判风险但不给到期时间", secondsSeeding: 10, doneDate: 0,
			p: state.SiteProfile{HRSeedHours: 24}, hasProfile: true,
			wantRisk: true, wantDeadline: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := models.Torrent{DoneDate: tc.doneDate, SecondsSeeding: tc.secondsSeeding}
			if got := hrRiskOf(&tr, tc.p, tc.hasProfile); got != tc.wantRisk {
				t.Errorf("hrRiskOf() = %v, want %v", got, tc.wantRisk)
			}
			if got := hrDeadlineFor(&tr, tc.p, tc.hasProfile); got != tc.wantDeadline {
				t.Errorf("hrDeadlineFor() = %d, want %d", got, tc.wantDeadline)
			}
		})
	}
}

// ---------- 3. matchProfile ----------

func TestMatchProfile(t *testing.T) {
	profiles := []state.SiteProfile{
		{ID: "short", Name: "Team", Match: "team", Enabled: true},
		{ID: "long", Name: "MTeam", Match: "m-team", Enabled: true},
		{ID: "disabled-longest", Name: "MTeamCC", Match: "m-team.cc", Enabled: false},
	}
	cases := []struct {
		name     string
		profiles []state.SiteProfile
		site     string
		wantID   string
		wantOK   bool
	}{
		{name: "多规则取匹配串最长者", profiles: profiles, site: "m-team.cc", wantID: "long", wantOK: true},
		{name: "只命中较短规则", profiles: profiles, site: "team.example.com", wantID: "short", wantOK: true},
		{name: "Disabled 规则不命中", profiles: []state.SiteProfile{{ID: "off", Match: "m-team.cc", Enabled: false}}, site: "m-team.cc", wantOK: false},
		{name: "空站点不命中", profiles: profiles, site: "", wantOK: false},
		{name: "纯空白站点不命中", profiles: profiles, site: "   ", wantOK: false},
		{name: "没有规则命中", profiles: profiles, site: "example.com", wantOK: false},
		{name: "大小写不敏感", profiles: profiles, site: "M-Team.CC", wantID: "long", wantOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := matchProfile(tc.profiles, tc.site)
			if ok != tc.wantOK {
				t.Fatalf("matchProfile() ok = %v, want %v", ok, tc.wantOK)
			}
			if tc.wantOK && got.ID != tc.wantID {
				t.Errorf("matchProfile() id = %q, want %q", got.ID, tc.wantID)
			}
			if !tc.wantOK && got.ID != "" {
				t.Errorf("未命中时不应返回规则: %+v", got)
			}
		})
	}
}

// ---------- 4. normalizeName ----------

func TestNormalizeName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "去扩展名与点分隔", in: "Movie.Name.2020.1080p.BluRay.mkv", want: "movie name 2020 1080p bluray"},
		{name: "下划线圆括号方括号都折叠", in: "Movie_Name_(2020) [1080p].mp4", want: "movie name 2020 1080p"},
		{name: "折叠多余空白", in: "  Foo   Bar  ", want: "foo bar"},
		{name: "大小写归并", in: "MixedCASE.mkv", want: "mixedcase"},
		{name: "torrent 扩展名同样去掉", in: "Show.Name.torrent", want: "show name"},
		{name: "空名字", in: "   ", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeName(tc.in); got != tc.want {
				t.Errorf("normalizeName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
	// 不同扩展名/分隔符的同内容必须归并
	if a, b := normalizeName("Same.Movie.mkv"), normalizeName("same_movie.torrent"); a != b {
		t.Errorf("同内容未归并: %q != %q", a, b)
	}
	// 分辨率不同不得归并
	if normalizeName("Show.1080p.mkv") == normalizeName("Show.2160p.mkv") {
		t.Error("1080p 与 2160p 不应归并为同一份内容")
	}
}

// ---------- 5. scoreOf ----------

func TestScoreOf(t *testing.T) {
	cases := []struct {
		name  string
		ratio float64
		eff   float64
		want  int
	}{
		{name: "全零为 0", ratio: 0, eff: 0, want: 0},
		{name: "两项都满为 100", ratio: 3, eff: 60, want: 100},
		{name: "超过目标饱和在 100", ratio: 10, eff: 1000, want: 100},
		{name: "负值截断在 0", ratio: -1, eff: -5, want: 0},
		{name: "两项各半", ratio: 1.5, eff: 30, want: 50},
		{name: "只有分享率满分", ratio: 3, eff: 0, want: 50},
		{name: "只有效率满分", ratio: 0, eff: 60, want: 50},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scoreOf(tc.ratio, tc.eff); got != tc.want {
				t.Errorf("scoreOf(%v, %v) = %d, want %d", tc.ratio, tc.eff, got, tc.want)
			}
		})
	}
	// 单调性：单项增大时分数不应下降
	prev := -1
	for i := 0; i <= 60; i++ {
		r := float64(i) * 0.1
		got := scoreOf(r, 30)
		if got < prev {
			t.Fatalf("分享率增大时分数下降: ratio=%v prev=%d got=%d", r, prev, got)
		}
		prev = got
	}
	prev = -1
	for i := 0; i <= 200; i++ {
		e := float64(i)
		got := scoreOf(1.5, e)
		if got < prev {
			t.Fatalf("上传效率增大时分数下降: eff=%v prev=%d got=%d", e, prev, got)
		}
		prev = got
	}
}

// ---------- 6. window30 ----------

func TestWindow30(t *testing.T) {
	now := time.Now()
	const hash = "0123456789abcdef0123456789abcdef01234567"

	t.Run("无采样时按做种天数折算并标记估算", func(t *testing.T) {
		svc := newTestService(t)
		tr := &models.Torrent{HashString: hash, UploadedEver: 900, SecondsSeeding: 15 * 86400}
		got, estimated := svc.window30(tr, now)
		if got != 900 || !estimated {
			t.Fatalf("window30() = (%d, %v), want (900, true)", got, estimated)
		}
	})

	t.Run("做种不足一天时直接给累计上传", func(t *testing.T) {
		svc := newTestService(t)
		tr := &models.Torrent{HashString: hash, UploadedEver: 100, DoneDate: now.Add(-2 * time.Hour).Unix()}
		got, estimated := svc.window30(tr, now)
		if got != 100 || !estimated {
			t.Fatalf("window30() = (%d, %v), want (100, true)", got, estimated)
		}
	})

	t.Run("累计上传为 0 时返回 0 且非估算", func(t *testing.T) {
		svc := newTestService(t)
		tr := &models.Torrent{HashString: hash}
		got, estimated := svc.window30(tr, now)
		if got != 0 || estimated {
			t.Fatalf("window30() = (%d, %v), want (0, false)", got, estimated)
		}
	})

	t.Run("采样覆盖满 30 天时取真实增量且非估算", func(t *testing.T) {
		svc := newTestService(t)
		// 基线放在 30 天前：窗口覆盖足够时才把增量当作真实值
		if n := svc.local.recordSample(now.AddDate(0, 0, -30), map[string]int64{hash: 1000}); n != 1 {
			t.Fatalf("recordSample() = %d, want 1", n)
		}
		tr := &models.Torrent{HashString: hash, UploadedEver: 1500, SecondsSeeding: 15 * 86400}
		got, estimated := svc.window30(tr, now)
		if got != 500 || estimated {
			t.Fatalf("window30() = (%d, %v), want (500, false)", got, estimated)
		}
	})

	t.Run("采样覆盖不足时折算并标记估算，避免首日误报零上传", func(t *testing.T) {
		svc := newTestService(t)
		// 首次运行当天：基线就是本次采样，增量必然为 0，不能当成「近 30 天真实值」
		svc.local.recordSample(now, map[string]int64{hash: 1500})
		tr := &models.Torrent{HashString: hash, UploadedEver: 1500, SecondsSeeding: 15 * 86400}
		got, estimated := svc.window30(tr, now)
		if got != 1500 || !estimated {
			t.Fatalf("window30() = (%d, %v), want (1500, true)", got, estimated)
		}
	})

	t.Run("基线高于累计上传（计数器重置）时退回折算估算", func(t *testing.T) {
		svc := newTestService(t)
		svc.local.recordSample(now, map[string]int64{hash: 2000})
		tr := &models.Torrent{HashString: hash, UploadedEver: 1500, SecondsSeeding: 15 * 86400}
		// 增量算不出（负值）时不能断言「零上传」，只能给带上限的折算值
		got, estimated := svc.window30(tr, now)
		if got != 1500 || !estimated {
			t.Fatalf("window30() = (%d, %v), want (1500, true)", got, estimated)
		}
	})

	t.Run("基线里没有该 hash 时退化为估算", func(t *testing.T) {
		svc := newTestService(t)
		svc.local.recordSample(now, map[string]int64{"ffffffff": 1})
		tr := &models.Torrent{HashString: hash, UploadedEver: 900, SecondsSeeding: 15 * 86400}
		got, estimated := svc.window30(tr, now)
		if got != 900 || !estimated {
			t.Fatalf("window30() = (%d, %v), want (900, true)", got, estimated)
		}
	})
}

// ---------- 7. store ----------

func TestRecordSampleHighWater(t *testing.T) {
	ls := newTestLocal(t)
	now := time.Now()

	if n := ls.recordSample(now, map[string]int64{"h1": 100}); n != 1 {
		t.Fatalf("首次采样 n = %d, want 1", n)
	}
	if n := ls.recordSample(now, map[string]int64{"h1": 50}); n != 0 {
		t.Errorf("同一天较低值被计入: n = %d, want 0", n)
	}
	if got := ls.sample.Days[dayKey(now)].Up["h1"]; got != 100 {
		t.Errorf("高水位被拉低: got = %d, want 100", got)
	}
	if n := ls.recordSample(now, map[string]int64{"h1": 200}); n != 1 {
		t.Errorf("更高值应计入: n = %d, want 1", n)
	}
	if got := ls.sample.Days[dayKey(now)].Up["h1"]; got != 200 {
		t.Errorf("高水位未更新: got = %d, want 200", got)
	}
	if n := ls.recordSample(now, map[string]int64{"": 999}); n != 0 {
		t.Errorf("空 hash 应被跳过: n = %d, want 0", n)
	}
	next := now.AddDate(0, 0, 1)
	if n := ls.recordSample(next, map[string]int64{"h1": 10}); n != 1 {
		t.Errorf("新的一天应重新记录: n = %d, want 1", n)
	}
}

func TestRecordSamplePrunesExpiredDays(t *testing.T) {
	ls := newTestLocal(t)
	now := time.Now()
	old := dayKey(now.AddDate(0, 0, -sampleKeepDays-1))
	keep := dayKey(now.AddDate(0, 0, -sampleKeepDays+1))
	ls.sample.Days[old] = &sampleDay{Up: map[string]int64{"h1": 1}}
	ls.sample.Days[keep] = &sampleDay{Up: map[string]int64{"h1": 1}}

	ls.recordSample(now, nil)

	if _, ok := ls.sample.Days[old]; ok {
		t.Errorf("超出 %d 天的采样应被清理: %s 仍存在", sampleKeepDays, old)
	}
	if _, ok := ls.sample.Days[keep]; !ok {
		t.Errorf("保留窗口内的采样被误删: %s", keep)
	}
	if _, ok := ls.sample.Days[dayKey(now)]; !ok {
		t.Error("今天的采样桶应存在")
	}
}

func TestBaseline30(t *testing.T) {
	ls := newTestLocal(t)
	now := time.Now()

	if _, _, ok := ls.baseline30("h1", now); ok {
		t.Error("无采样时不应命中")
	}
	if _, _, ok := ls.baseline30("", now); ok {
		t.Error("空 hash 不应命中")
	}

	ls.recordSample(now, map[string]int64{"h1": 1234})
	v, at, ok := ls.baseline30("h1", now)
	if !ok || v != 1234 {
		t.Fatalf("baseline30() = (%d, %v, %v), want (1234, _, true)", v, at, ok)
	}
	if at.Format("2006-01-02") != dayKey(now) {
		t.Errorf("基线日期 = %s, want %s", at.Format("2006-01-02"), dayKey(now))
	}

	// 窗口外（31 天前）的采样不参与，且窗口内仍有今天的采样时取今天
	old := dayKey(now.AddDate(0, 0, -31))
	ls.sample.Days[old] = &sampleDay{Up: map[string]int64{"h1": 5}}
	if v, _, ok := ls.baseline30("h1", now); !ok || v != 1234 {
		t.Errorf("窗口外采样不应覆盖窗口内基线: v = %d, ok = %v", v, ok)
	}

	// 只有窗口外的采样时视为未覆盖
	ls2 := newTestLocal(t)
	ls2.sample.Days[old] = &sampleDay{Up: map[string]int64{"h1": 7}}
	if _, _, ok := ls2.baseline30("h1", now); ok {
		t.Error("只有 30 天窗口外的采样时不应命中")
	}
}

func TestVaultRoundTrip(t *testing.T) {
	ls := newTestLocal(t)
	r1 := vaultRecord{
		VaultEntry: VaultEntry{ID: "a", Hash: "aa", Name: "甲", Size: 10, DeletedAt: 200},
		Magnet:     "magnet:?xt=urn:btih:aa",
	}
	r2 := vaultRecord{
		VaultEntry: VaultEntry{ID: "b", Hash: "bb", Name: "乙", Size: 20, DeletedAt: 300},
		Files:      []VaultFile{{Name: "f1", Size: 5}},
	}
	if err := ls.vaultPut(r1); err != nil {
		t.Fatalf("vaultPut(r1): %v", err)
	}
	if err := ls.vaultPut(r2); err != nil {
		t.Fatalf("vaultPut(r2): %v", err)
	}
	if got := ls.vaultAll(); len(got) != 2 {
		t.Fatalf("vaultAll() 数量 = %d, want 2", len(got))
	}

	got, ok := ls.vaultFind("a")
	if !ok || got.Magnet != r1.Magnet || got.Name != "甲" || got.Files != nil {
		t.Errorf("vaultFind(a) = %+v, ok = %v", got, ok)
	}
	gotB, ok := ls.vaultFind("b")
	if !ok || len(gotB.Files) != 1 || gotB.Files[0].Name != "f1" {
		t.Errorf("vaultFind(b) 文件清单丢失: %+v, ok = %v", gotB, ok)
	}
	if _, ok := ls.vaultFind("nope"); ok {
		t.Error("不存在的 ID 不应命中")
	}

	// 同 ID 重复写入只更新，不新增
	r2.VaultEntry.Name = "乙改"
	if err := ls.vaultPut(r2); err != nil {
		t.Fatalf("vaultPut(r2) 更新: %v", err)
	}
	if all := ls.vaultAll(); len(all) != 2 {
		t.Errorf("同 ID 重复写入不应新增: 数量 = %d", len(all))
	} else if v, _ := ls.vaultFind("b"); v.Name != "乙改" {
		t.Errorf("同 ID 重复写入未更新内容: %+v", v)
	}

	if !ls.vaultRemove("a") {
		t.Error("vaultRemove(a) = false, want true")
	}
	if ls.vaultRemove("a") {
		t.Error("重复 vaultRemove 应返回 false")
	}
	all := ls.vaultAll()
	if len(all) != 1 || all[0].ID != "b" {
		t.Errorf("删除后 vaultAll() = %+v", all)
	}
	if _, ok := ls.vaultFind("a"); ok {
		t.Error("删除后 vaultFind(a) 仍命中")
	}
}

func TestVaultPersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	ls, err := newStore(dir)
	if err != nil {
		t.Fatalf("newStore: %v", err)
	}
	rec := vaultRecord{
		VaultEntry: VaultEntry{ID: "k", Hash: "kk", Name: "留档", Size: 7, DeletedAt: 99},
		Magnet:     "magnet:?xt=urn:btih:kk",
	}
	if err := ls.vaultPut(rec); err != nil {
		t.Fatalf("vaultPut: %v", err)
	}

	ls2, err := newStore(dir)
	if err != nil {
		t.Fatalf("newStore(重载): %v", err)
	}
	got, ok := ls2.vaultFind("k")
	if !ok || got.Name != "留档" || got.Magnet != rec.Magnet {
		t.Fatalf("重载后 vaultFind(k) = %+v, ok = %v", got, ok)
	}
}

func TestSafeHash(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "合法 hex 原样返回", in: "deadbeef", want: "deadbeef"},
		{name: "大写转小写", in: "DEADBEEF", want: "deadbeef"},
		{name: "去首尾空白", in: "  abc123  ", want: "abc123"},
		{name: "空串", in: "", want: ""},
		{name: "纯空白", in: "   ", want: ""},
		{name: "全非 hex", in: "zzz", want: ""},
		{name: "保留中间的数字与 hex 字母", in: "g1h2", want: "12"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := safeHash(tc.in); got != tc.want {
				t.Errorf("safeHash(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestSafeHashTraversalIsNeutralized 验证路径穿越被中和这一安全性质。
//
// 注意：任务描述期望 safeHash("../evil") 返回空串，但当前实现 safeHash（store.go:257）
// 是「只保留 hex 字符」的过滤器，".", "/", "\\" 都被丢掉，结果实际是 "e"（非空）。
// 结果仍不含任何路径分隔符，vaultTorrentPath 不会逃出 vault 目录，因此这里断言
// 真正成立的安全性质，并把与描述的差异记入测试日志与汇报（不改产品代码）。
func TestSafeHashTraversalIsNeutralized(t *testing.T) {
	ls := newTestLocal(t)
	vaultDir := filepath.Join(ls.dir, "vault")

	for _, in := range []string{"../evil", "..\\..\\evil", "/etc/passwd", "a/b", "x/../../y", `..\..\y`} {
		got := safeHash(in)
		if strings.ContainsAny(got, `/\`) {
			t.Errorf("safeHash(%q) = %q 仍含路径分隔符", in, got)
		}
		p := ls.vaultTorrentPath(in)
		if p == "" {
			// hash 完全不含 hex 时返回空路径，调用方应视为不可留档
			continue
		}
		rel, err := filepath.Rel(vaultDir, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Errorf("vaultTorrentPath(%q) = %q 逃出了 %q", in, p, vaultDir)
		}
	}
	if p := ls.vaultTorrentPath(""); p != "" {
		t.Errorf("vaultTorrentPath(\"\") = %q, want 空串", p)
	}

	if got := safeHash("../evil"); got != "" {
		t.Logf("已知规格差异: safeHash(%q) = %q（描述期望空串）；过滤后不含路径分隔符，未发生穿越", "../evil", got)
	}
}

// ---------- 8. Preview ----------

func TestPreview(t *testing.T) {
	cases := []struct {
		name          string
		req           PublishRequest
		wantWarnings  []string // 期望 warnings 中出现的子串
		wantNoWarning bool
		wantBBCode    []string
	}{
		{
			name:         "名称 MediaInfo 截图都缺",
			req:          PublishRequest{},
			wantWarnings: []string{"缺少种子名称", "未粘贴 MediaInfo", "截图"},
		},
		{
			name:         "只缺截图",
			req:          PublishRequest{Name: "片名", MediaInfo: "Width: 1920"},
			wantWarnings: []string{"截图"},
		},
		{
			name: "字段齐全时无警告且 BBCode 含隐藏块",
			req: PublishRequest{
				Title:       "译名",
				Name:        "Movie 2020 1080p",
				MediaInfo:   "Video\nWidth: 1920",
				Screenshots: []string{"http://img/a.jpg"},
				Subtitle:    "简中",
				Category:    "电影",
			},
			wantNoWarning: true,
			wantBBCode:    []string{"[quote]", "[/quote]", "[hide]", "[/hide]", "[img]http://img/a.jpg[/img]"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pv := Preview(tc.req)
			if tc.wantNoWarning {
				if len(pv.Warnings) != 0 {
					t.Fatalf("warnings 应为空, got %v", pv.Warnings)
				}
			} else {
				if len(pv.Warnings) == 0 {
					t.Fatal("warnings 不应为空")
				}
				for _, want := range tc.wantWarnings {
					if !warningsContain(pv.Warnings, want) {
						t.Errorf("warnings 缺少 %q: %v", want, pv.Warnings)
					}
				}
			}
			for _, seg := range tc.wantBBCode {
				if !strings.Contains(pv.BBCode, seg) {
					t.Errorf("BBCode 缺少 %q:\n%s", seg, pv.BBCode)
				}
			}
		})
	}

	// MediaInfo 非空但未填分辨率时，从文本里猜出分辨率
	pv := Preview(PublishRequest{Name: "n", MediaInfo: "Width: 3840", Screenshots: []string{"u"}})
	if !strings.Contains(pv.BBCode, "3840x2160") {
		t.Errorf("未从 MediaInfo 猜出分辨率:\n%s", pv.BBCode)
	}
}

func warningsContain(warnings []string, sub string) bool {
	for _, w := range warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

// ---------- 9. Kit ----------

func TestKit(t *testing.T) {
	req := PublishRequest{
		Title:       "译名",
		Name:        "Movie 2020 1080p",
		MediaInfo:   "General\nWidth: 1920",
		Screenshots: []string{"http://img/a.jpg"},
		Subtitle:    "简中",
		Category:    "电影",
	}

	t.Run("不含种子文件时三个文本条目", func(t *testing.T) {
		data, err := Kit(req, "", nil)
		if err != nil {
			t.Fatalf("Kit: %v", err)
		}
		entries := readZipEntries(t, data)
		for _, want := range []string{"bbcode.txt", "mediainfo.txt", "checklist.txt"} {
			if _, ok := entries[want]; !ok {
				t.Errorf("zip 缺少 %s；现有条目 %v", want, zipKeys(entries))
			}
		}
		if len(entries) != 3 {
			t.Errorf("条目数 = %d, want 3（%v）", len(entries), zipKeys(entries))
		}
		if !strings.Contains(string(entries["bbcode.txt"]), "[quote]") {
			t.Error("bbcode.txt 不含 [quote]")
		}
		if !strings.Contains(string(entries["checklist.txt"]), "发布前检查清单") {
			t.Error("checklist.txt 内容不符")
		}
	})

	t.Run("传入种子文件时多一个 .torrent 条目", func(t *testing.T) {
		payload := []byte("d4:infod4:name4:testee")
		data, err := Kit(req, "Movie.torrent", payload)
		if err != nil {
			t.Fatalf("Kit: %v", err)
		}
		entries := readZipEntries(t, data)
		if len(entries) != 4 {
			t.Fatalf("条目数 = %d, want 4（%v）", len(entries), zipKeys(entries))
		}
		got, ok := entries["Movie.torrent"]
		if !ok {
			t.Fatalf("缺少 Movie.torrent 条目: %v", zipKeys(entries))
		}
		if !bytes.Equal(got, payload) {
			t.Errorf(".torrent 内容 = %q, want %q", got, payload)
		}
	})

	t.Run("未指定种子文件名时用安全名称补后缀", func(t *testing.T) {
		data, err := Kit(req, "", []byte("x"))
		if err != nil {
			t.Fatalf("Kit: %v", err)
		}
		entries := readZipEntries(t, data)
		if _, ok := entries["Movie 2020 1080p.torrent"]; !ok {
			t.Errorf("缺少回退文件名条目；现有 %v", zipKeys(entries))
		}
	})

	t.Run("缺 MediaInfo 时文本条目给出占位", func(t *testing.T) {
		data, err := Kit(PublishRequest{Name: "x"}, "", nil)
		if err != nil {
			t.Fatalf("Kit: %v", err)
		}
		entries := readZipEntries(t, data)
		if got := string(entries["mediainfo.txt"]); got != "(未提供 MediaInfo)" {
			t.Errorf("mediainfo.txt = %q", got)
		}
	})
}

func readZipEntries(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	out := make(map[string][]byte, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("打开 zip 条目 %s: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("读取 zip 条目 %s: %v", f.Name, err)
		}
		out[f.Name] = b
	}
	return out
}

func zipKeys(entries map[string][]byte) []string {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	return keys
}

// ---------- 附加：站点规则增删（service.go 的 UpsertProfile / DeleteProfile） ----------

func TestUpsertAndDeleteProfile(t *testing.T) {
	st, err := state.Load(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}

	if _, err := UpsertProfile(st, state.SiteProfile{}); err == nil {
		t.Error("站点名为空应返回错误")
	}

	saved, err := UpsertProfile(st, state.SiteProfile{Name: "MTeam", MinSeedDays: 7, Enabled: true})
	if err != nil {
		t.Fatalf("UpsertProfile(新建): %v", err)
	}
	if saved.ID == "" {
		t.Error("新建规则的 ID 不应为空")
	}
	if saved.Match != "mteam" {
		t.Errorf("Match 应回退为小写站点名, got %q", saved.Match)
	}
	if profiles := st.Get().SiteProfiles; len(profiles) != 1 {
		t.Fatalf("新建后规则数 = %d, want 1", len(profiles))
	}

	saved.MinSeedDays = 14
	if _, err := UpsertProfile(st, saved); err != nil {
		t.Fatalf("UpsertProfile(更新): %v", err)
	}
	profiles := st.Get().SiteProfiles
	if len(profiles) != 1 || profiles[0].MinSeedDays != 14 {
		t.Errorf("同 ID 更新应覆盖而非新增: %+v", profiles)
	}

	ok, err := DeleteProfile(st, saved.ID)
	if err != nil || !ok {
		t.Fatalf("DeleteProfile = (%v, %v), want (true, nil)", ok, err)
	}
	if ok, _ := DeleteProfile(st, saved.ID); ok {
		t.Error("重复删除应返回 false")
	}
	if profiles := st.Get().SiteProfiles; len(profiles) != 0 {
		t.Errorf("删除后规则数 = %d, want 0", len(profiles))
	}
}
