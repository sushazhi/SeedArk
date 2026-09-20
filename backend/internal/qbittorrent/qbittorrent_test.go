package qbittorrent

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/trpanel/backend/internal/driver"
	"github.com/trpanel/backend/internal/models"
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
		"BandwidthGroups": caps.BandwidthGroups,
		"Blocklist":       caps.Blocklist,
		"PortTest":        caps.PortTest,
	} {
		if ok {
			t.Errorf("%s 在 qBittorrent 下应为 false", name)
		}
	}
	if !caps.SequentialDownload || !caps.FreeSpace {
		t.Error("顺序下载与磁盘空间查询在 qBittorrent 下应可用")
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
	// d8:announce... 后面接 4:infod6:lengthi1e4:name1:a12:piece lengthi1e6:pieces1:xee
	raw := []byte("d8:announce20:http://t.example/a4:infod6:lengthi1e4:name1:a12:piece lengthi1e6:pieces1:xeee")
	got := infoHashFromTorrent(raw)
	if len(got) != 40 {
		t.Errorf("infohash 应为 40 位十六进制，得到 %q（%d 位）", got, len(got))
	}
	// 不含 info 字典时应返回空串（调用方据此回退到列表查询）
	if h := infoHashFromTorrent([]byte("d8:announce3:fooe")); h != "" {
		t.Errorf("缺少 info 字典时应返回空串，得到 %q", h)
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
