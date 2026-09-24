package qbittorrent

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/sushazhi/seedark/backend/internal/driver"
	"github.com/sushazhi/seedark/backend/internal/models"
)

func TestNormalizeBase(t *testing.T) {
	cases := []struct{ in, want string }{
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080"},
		{"http://127.0.0.1:8080/", "http://127.0.0.1:8080"},
		{"http://127.0.0.1:8080/api/v2", "http://127.0.0.1:8080"},
		{"https://qb.example.com", "https://qb.example.com"},
	}
	for _, c := range cases {
		got, err := normalizeBase(c.in)
		if err != nil {
			t.Fatalf("normalizeBase(%q) 出错: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("normalizeBase(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
	if _, err := normalizeBase(""); err == nil {
		t.Error("空地址应报错")
	}
}

// TestIsAPIKey qBittorrent 5.2+ 的 API Key 形如 qbt_ + 29 位字母数字（共 32 字符）。
// 判定必须严格：把密码误判成 API Key 会导致 Bearer 认证失败且不会回落 cookie。
func TestIsAPIKey(t *testing.T) {
	// qbt_ 前缀 + 28 位字母数字 = 32 字符
	good := "qbt_" + strings.Repeat("a", 28)
	if !isAPIKey(good) {
		t.Error("合法 API Key 未被识别")
	}
	for _, bad := range []string{
		"", "qbt_", "qbt_abc", "adminadmin",
		"qbt_" + strings.Repeat("a", 27),       // 31 位
		"qbt_" + strings.Repeat("a", 29),       // 33 位
		"qbt_" + strings.Repeat("a", 27) + "-", // 含非法字符
	} {
		if isAPIKey(bad) {
			t.Errorf("%q 不应被识别为 API Key", bad)
		}
	}
}

// TestIDMapping 同一 hash 必须稳定映射到同一 ID，且能反查回去。
// 面板用这个 ID 做批量操作，两次列表刷新之间 ID 漂移会让操作打错种子。
func TestIDMapping(t *testing.T) {
	c := &Client{}
	c.syncIDs([]string{"aaa", "bbb", "aaa"})
	a, b := c.IDFor("aaa"), c.IDFor("bbb")
	if a == 0 || b == 0 {
		t.Fatal("ID 不应为 0")
	}
	if a == b {
		t.Fatal("不同 hash 不应映射到同一 ID")
	}
	if c.IDFor("aaa") != a {
		t.Error("同一 hash 的 ID 不稳定")
	}
	if c.HashFor(a) != "aaa" || c.HashFor(b) != "bbb" {
		t.Error("ID → hash 反查失败")
	}
	// ID 必须落在聚合编码预留的 40 位本地 ID 空间内，否则会与服务器编号位冲突
	if a >= maxLocalID {
		t.Errorf("ID %d 超出本地 ID 空间（%d）", a, maxLocalID)
	}
}

func TestMapState(t *testing.T) {
	cases := map[string]int64{
		"downloading": 4, "forcedDL": 4, "stalledDL": 4, "metaDL": 4,
		"uploading": 6, "forcedUP": 6, "stalledUP": 6,
		"queuedDL": 3, "queuedUP": 5,
		"checkingDL": 2, "checkingUP": 2, "checkingResumeData": 2,
		"pausedDL": 0, "pausedUP": 0, "stoppedDL": 0,
		"error": 0, "unknownState": 0,
	}
	for state, want := range cases {
		got, _, _ := mapState(state)
		if got != want {
			t.Errorf("mapState(%q) = %d，期望 %d", state, got, want)
		}
	}
	if _, code, _ := mapState("error"); code == 0 {
		t.Error("error 状态必须带错误码")
	}
	if _, code, _ := mapState("missingFiles"); code == 0 {
		t.Error("missingFiles 状态必须带错误码")
	}
}

// TestEncodePieces 块位图：只有 2（已下载）计为 1，高位在前。
// 前端拿它画进度方块，编码错一位就会显示成花屏。
func TestEncodePieces(t *testing.T) {
	if got := encodePieces(nil); got != "" {
		t.Errorf("空列表应返回空串，得到 %q", got)
	}
	// 前 8 块：已下载、下载中、下载中、已下载……
	got := encodePieces([]int{2, 1, 0, 2, 2, 0, 0, 2})
	raw, err := base64.StdEncoding.DecodeString(got)
	if err != nil {
		t.Fatalf("不是合法 base64: %v", err)
	}
	if len(raw) != 1 || raw[0] != 0b1001_1001 {
		t.Errorf("位图编码错误：%08b，期望 10011001", raw[0])
	}
}

func TestMapFilePriority(t *testing.T) {
	cases := map[int64]int64{0: 0, 1: 0, 2: -1, 6: 1, 7: 1}
	for in, want := range cases {
		if got := mapFilePriority(in); got != want {
			t.Errorf("mapFilePriority(%d) = %d，期望 %d", in, got, want)
		}
	}
}

func TestSiteNames(t *testing.T) {
	got := siteNames([]string{
		"https://tracker.example.com:443/announce",
		"http://tracker.example.com/announce",
		"udp://other.example.org:6969/announce",
		"not a url",
	})
	if len(got) != 2 || got[0] != "tracker.example.com" || got[1] != "other.example.org" {
		t.Errorf("站点名提取错误：%v", got)
	}
}

func TestMapTorrent(t *testing.T) {
	c := &Client{}
	got := c.mapTorrent(torrentInfo{
		Hash:      "deadbeef",
		Name:      "demo",
		State:     "downloading",
		Tags:      "a,b",
		Category:  "movie",
		ETA:       8640000,
		TotalSize: 100,
	})
	if got.ID == 0 {
		t.Error("ID 未生成")
	}
	if got.Status != 4 {
		t.Errorf("状态应为下载(4)，得到 %d", got.Status)
	}
	if len(got.Labels) != 3 {
		t.Errorf("标签应含 a/b/movie，得到 %v", got.Labels)
	}
	if got.ETA != -1 {
		t.Errorf("未知 ETA 应归一为 -1，得到 %d", got.ETA)
	}
}

// TestCapabilities qBittorrent 没有带宽组 / 黑名单 / 端口测试，
// 必须如实声明，否则界面会给出点了必失败死的入口。
func TestCapabilities(t *testing.T) {
	c := &Client{}
	caps := c.Capabilities()
	for name, ok := range map[string]bool{
		"BandwidthGroups":  caps.BandwidthGroups,
		"Blocklist":        caps.Blocklist,
		"PortTest":         caps.PortTest,
		"ScriptHooks":      caps.ScriptHooks,
		"QueueStalled":     caps.QueueStalled,
		"PeerLimit":        caps.PeerLimit,
		"PerTorrentLimits": caps.PerTorrentLimits,
		// 单种限速在 QB 里是绝对值、没有「遵循全局限速」开关，
		// 分组限速引擎靠该语义区分引擎写入与用户手动限速，故必须为 false
		"HonorsSessionLimits": caps.HonorsSessionLimits,
		"FileHandling":        caps.FileHandling,
		"UtpToggle":           caps.UtpToggle,
	} {
		if ok {
			t.Errorf("%s 在 qBittorrent 下应为 false", name)
		}
	}
	if !caps.SequentialDownload || !caps.FreeSpace {
		t.Error("顺序下载与磁盘空间查询在 qBittorrent 下应可用")
	}
}

// TestApplyTrackersLastAnnounceTime 做种策略的 trackerUnreachable() 保护栏按
// 「LastAnnounceTime > 0 且未成功」判定「尝试过但全失败」。qBittorrent 不返回
// announce 时间，若不补该字段，保护栏在 QB 下永不触发 —— 站点没记账的种子
// 会被照常暂停 / 删除（本地分享率不代表真实贡献）。本测试锁住补充逻辑。
func TestApplyTrackersLastAnnounceTime(t *testing.T) {
	const activity = int64(1700000000)
	cases := []struct {
		name          string
		status        int64
		wantOK        bool
		wantAttempted bool // LastAnnounceTime 是否应非零
	}{
		// 0 禁用 / 1 未联系：没尝试过，不算「失败尝试」
		{"禁用", 0, false, false},
		{"未联系", 1, false, false},
		// 2 正常 / 3 更新中：成功，保护栏第一分支就 return false
		{"正常", 2, true, false},
		{"更新中", 3, true, false},
		// 4 不可用 / 5 报错 / 6 不可达：尝试过且未成功 → 必须非零
		{"不可用", 4, false, true},
		{"报错", 5, false, true},
		{"不可达", 6, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := &models.Torrent{ActivityDate: activity, AddedDate: activity - 100}
			applyTrackers(tr, []torrentTracker{{URL: "https://tracker.example/announce", Status: c.status}})
			if len(tr.TrackerStats) != 1 {
				t.Fatalf("TrackerStats 应有 1 项，得到 %d", len(tr.TrackerStats))
			}
			got := tr.TrackerStats[0]
			if got.LastAnnounceSucceeded != c.wantOK {
				t.Errorf("status=%d 时 LastAnnounceSucceeded 应为 %v，得到 %v", c.status, c.wantOK, got.LastAnnounceSucceeded)
			}
			nonZero := got.LastAnnounceTime > 0
			if nonZero != c.wantAttempted {
				t.Errorf("status=%d 时 LastAnnounceTime 非零应为 %v，得到 %d", c.status, c.wantAttempted, got.LastAnnounceTime)
			}
		})
	}
}

// TestApplyTrackersAttemptedFallsBackToAdded 最近活动时间缺失时（部分种子
// 从未产生活动），仍要回退到添加时间，保证保护栏能生效而不是退化成 0。
func TestApplyTrackersAttemptedFallsBackToAdded(t *testing.T) {
	const added = int64(1690000000)
	tr := &models.Torrent{AddedDate: added}
	applyTrackers(tr, []torrentTracker{{URL: "https://t.example/a", Status: 4}})
	if got := tr.TrackerStats[0].LastAnnounceTime; got != added {
		t.Errorf("活动时间缺失时应回退到 AddedDate=%d，得到 %d", added, got)
	}
}

// TestPlanTopLevelRename 右键「重命名」传的是种子名：Transmission 用它重命名
// 顶层目录，但 qBittorrent 的 renameFile/renameFolder 只认种子内相对路径，直接
// 转发必然 404。这里锁住翻译逻辑。
func TestPlanTopLevelRename(t *testing.T) {
	f := func(names ...string) []torrentFile {
		out := make([]torrentFile, 0, len(names))
		for i, n := range names {
			out = append(out, torrentFile{Index: int64(i), Name: n})
		}
		return out
	}

	t.Run("多文件单目录改目录名", func(t *testing.T) {
		got, err := planTopLevelRename(f("Old/a.mkv", "Old/b.srt"), "Old", "New")
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		want := []renameStep{{Old: "Old", New: "New", Folder: true}}
		if len(got) != 1 || got[0] != want[0] {
			t.Errorf("得到 %+v，期望 %+v", got, want)
		}
	})

	t.Run("单文件种子改文件名", func(t *testing.T) {
		got, err := planTopLevelRename(f("movie.mkv"), "movie.mkv", "new.mkv")
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		want := renameStep{Old: "movie.mkv", New: "new.mkv", Folder: false}
		if len(got) != 1 || got[0] != want {
			t.Errorf("得到 %+v，期望 %+v", got, want)
		}
	})

	t.Run("多顶层目录只改匹配的那个", func(t *testing.T) {
		got, err := planTopLevelRename(f("A/1.mkv", "B/2.mkv"), "A", "Z")
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		// 只能改 A，不能碰 B —— 否则会把不属于该名字的内容一起改掉
		if len(got) != 1 || got[0].Old != "A" || got[0].New != "Z" || !got[0].Folder {
			t.Errorf("得到 %+v，期望只改 A→Z", got)
		}
	})

	t.Run("多顶层目录无匹配则报错", func(t *testing.T) {
		if _, err := planTopLevelRename(f("A/1.mkv", "B/2.mkv"), "Missing", "Z"); err == nil {
			t.Error("找不到旧名时应报错，而不是乱改一通")
		}
	})

	t.Run("空文件列表报错", func(t *testing.T) {
		if _, err := planTopLevelRename(nil, "Old", "New"); err == nil {
			t.Error("空文件列表应报错")
		}
	})
}

// TestSeedRatioModeRoundTrip 面板的分享率是三态（0 跟随全局 / 1 单种覆盖 /
// 2 不限），qBittorrent 用 ratio_limit 的 -2 / >=0 / -1 表达。若把 -1 与 -2
// 混为一谈，「不限」会被显示成「跟随全局」，「单种覆盖」还会静默退化成「不限」。
func TestSeedRatioModeRoundTrip(t *testing.T) {
	cases := []struct {
		ratioLimit float64
		wantMode   int64
		desc       string
	}{
		{-2, 0, "跟随全局"},
		{0, 1, "单种覆盖（0 倍）"},
		{1.5, 1, "单种覆盖"},
		{-1, 2, "不限"},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			got := (&Client{}).mapTorrent(torrentInfo{Hash: "abc", RatioLimit: c.ratioLimit})
			if got.SeedRatioMode != c.wantMode {
				t.Errorf("ratio_limit=%v 应回读为 mode %d，得到 %d", c.ratioLimit, c.wantMode, got.SeedRatioMode)
			}
		})
	}
}

// TestUnsupportedAbilities 不支持的能力必须返回 ErrUnsupported（而非 nil），
// 这样 API 层才能翻译成 501 而不是当成上游故障重试。
func TestUnsupportedAbilities(t *testing.T) {
	c := &Client{}
	if _, err := c.UpdateBlocklist(nil); err != driver.ErrUnsupported { //nolint:staticcheck
		t.Errorf("UpdateBlocklist 应返回 ErrUnsupported，得到 %v", err)
	}
	if _, err := c.GetSessionGroups(nil); err != driver.ErrUnsupported { //nolint:staticcheck
		t.Errorf("GetSessionGroups 应返回 ErrUnsupported，得到 %v", err)
	}
	if err := c.SetSessionGroup(nil, "g", nil); err != driver.ErrUnsupported { //nolint:staticcheck
		t.Errorf("SetSessionGroup 应返回 ErrUnsupported，得到 %v", err)
	}
}

// TestInterfaceCompliance 编译期之外的显式断言：驱动必须满足 Backend 接口
func TestInterfaceCompliance(t *testing.T) {
	var _ driver.Backend = (*Client)(nil)
	if (&Client{}).Kind() != driver.KindQBittorrent {
		t.Error("Kind 应为 qbittorrent")
	}
}

func TestNormalizeKind(t *testing.T) {
	cases := map[string]driver.Kind{
		"":             driver.KindTransmission,
		"transmission": driver.KindTransmission,
		"qbittorrent":  driver.KindQBittorrent,
		"qBittorrent":  driver.KindTransmission, // 大小写敏感，避免歧义
		"qbt":          driver.KindTransmission,
	}
	for in, want := range cases {
		if got := driver.NormalizeKind(in); got != want {
			t.Errorf("NormalizeKind(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestInfoHashFromTorrent 从 .torrent 二进制里定位 info 字典并算 sha1。
// 这是「添加种子后回查 ID」的兜底路径，算错就会把操作发到错误的种子上。
func TestInfoHashFromTorrent(t *testing.T) {
	// 夹具按 bencode 规范构造，info 之前故意放了两处坑：comment 里含字面量
	// "4:infod6:lengthi1ee"（骗过按字符串查找的写法），键名与 URL 里的 l/e/d
	// 字母（骗过数 d/l/e 深度的写法）。期望值取自 info 字典原始字节的 sha1。
	raw := "d8:announce40:http://tracker.example/announce?listed=17:comment24:trap 4:infod6:lengthi1ee5:nodesld1:a2:deeli7eee4:infod6:lengthi1048576e4:name22:seedark-测试 delelte12:piece lengthi262144e6:pieces20:0123456789abcdefghije8:url-listl22:http://dl.example/fileee"
	const want = "e3dac9b98cea8fd717ac1940a9a832528995f1e3"
	if got := infoHashFromTorrent([]byte(raw)); got != want {
		t.Errorf("infohash = %q，期望 %q", got, want)
	}
	// 不含 info 字典时应返回空串（调用方据此回退到列表查询）
	if h := infoHashFromTorrent([]byte("d8:announce3:fooe")); h != "" {
		t.Errorf("缺少 info 字典时应返回空串，得到 %q", h)
	}
	// 截断的输入不能被当成合法字典
	if h := infoHashFromTorrent([]byte("d4:infod6:lengthi1e")); h != "" {
		t.Errorf("info 字典不完整时应返回空串，得到 %q", h)
	}
	// 负数整数是合法 bencode（i-1e）：跳不过去就会连 info 字典都找不到。
	// 与「同结构但该整数为正」的对照串比对，免去手算 sha1
	neg := "d8:announce3:foo3:tagi-1e4:infod6:lengthi1048576e4:name1:aee"
	pos := "d8:announce3:foo3:tagi1e4:infod6:lengthi1048576e4:name1:aee"
	if got := infoHashFromTorrent([]byte(neg)); got == "" {
		t.Error("含负整数（i-1e）的种子应能算出 infohash，得到空串")
	} else if want := infoHashFromTorrent([]byte(pos)); got != want {
		t.Errorf("负整数不应影响定位 info：infohash = %q，期望 %q", got, want)
	}
}

// TestMapTorrentSeedIdleMode 空闲做种三态回读。
// inactive_seeding_time_limit 是原始值（-2 跟随 / -1 不限 / >=0 分钟），
// max_inactive_seeding_time 是套用分类/全局后的生效值；模式只能看原始值，
// 否则「不限」会被界面显示成「跟随全局」，保存后静默改变做种行为。
func TestMapTorrentSeedIdleMode(t *testing.T) {
	cases := []struct {
		raw   int64
		want  int64
		limit int64
	}{
		{-2, 0, -2}, // 跟随全局
		{-1, 2, -1}, // 不限
		{60, 1, 60}, // 种子级
	}
	for _, tc := range cases {
		var c Client
		out := c.mapTorrent(torrentInfo{
			Hash:                     "0123456789abcdef0123456789abcdef01234567",
			State:                    "uploading",
			InactiveSeedingTimeLimit: tc.raw,
			MaxInactiveSeedingTime:   tc.limit,
		})
		if out.SeedIdleMode != tc.want {
			t.Errorf("原始值 %d：SeedIdleMode = %d，期望 %d", tc.raw, out.SeedIdleMode, tc.want)
		}
		if out.SeedIdleLimit != tc.limit {
			t.Errorf("原始值 %d：SeedIdleLimit = %d，期望 %d", tc.raw, out.SeedIdleLimit, tc.limit)
		}
	}
}

func TestSessionStatsShape(t *testing.T) {
	// SessionStats 被两个驱动共用，字段语义必须一致（单位：字节、速率 KB/s）
	var s models.SessionStats
	s.DownloadSpeed, s.UploadSpeed = 1, 2
	s.Cumulative.UploadedBytes = 3
	if s.DownloadSpeed != 1 || s.Cumulative.UploadedBytes != 3 {
		t.Error("统计字段读写异常")
	}
}
