package qbittorrent

import (
	"context"
	"errors"
	"testing"

	"github.com/sushazhi/seedark/backend/internal/driver"
	"github.com/sushazhi/seedark/backend/internal/qbmock"
)

// rawPrefs 直接读上游偏好表（绕过驱动的归一化），用于核对落地值
func rawPrefs(t *testing.T, c *Client) map[string]any {
	t.Helper()
	var raw map[string]any
	if err := c.get(context.Background(), "app/preferences", nil, &raw); err != nil {
		t.Fatalf("读取 app/preferences: %v", err)
	}
	return raw
}

// TestSettingsSchemaShape 自述表的结构自检：键唯一、枚举自洽、范围合法
func TestSettingsSchemaShape(t *testing.T) {
	c, _ := New("http://127.0.0.1:1", "", "")
	seen := map[string]string{} // key → 所在分节
	sections := 0
	for _, sec := range c.SettingsSchema() {
		if sec.Key == "" || sec.Label == "" || sec.LabelEn == "" {
			t.Errorf("分节缺少 Key/Label: %+v", sec)
		}
		if len(sec.Fields) == 0 {
			t.Errorf("分节 %s 没有字段", sec.Key)
		}
		sections++
		for _, f := range sec.Fields {
			if f.Key == "" || f.Label == "" || f.LabelEn == "" {
				t.Errorf("[%s] 字段缺少 Key/Label: %+v", sec.Key, f)
			}
			if prev, dup := seen[f.Key]; dup {
				t.Errorf("字段键重复: %s（%s 与 %s）", f.Key, prev, sec.Key)
			}
			seen[f.Key] = sec.Key
			switch f.Type {
			case driver.FieldSelect:
				if len(f.Options) == 0 {
					t.Errorf("[%s] 枚举 %s 没有可选项", sec.Key, f.Key)
				}
				optSeen := map[string]bool{}
				for _, o := range f.Options {
					if o.Value == "" && f.ValueType != driver.FieldInt {
						t.Errorf("[%s] 枚举 %s 有空值项", sec.Key, f.Key)
					}
					if optSeen[o.Value] {
						t.Errorf("[%s] 枚举 %s 取值重复: %q", sec.Key, f.Key, o.Value)
					}
					optSeen[o.Value] = true
					if o.Label == "" || o.LabelEn == "" {
						t.Errorf("[%s] 枚举 %s 的取值 %q 缺少文案", sec.Key, f.Key, o.Value)
					}
				}
				if f.ValueType != "" && f.ValueType != driver.FieldInt && f.ValueType != driver.FieldString && f.ValueType != driver.FieldBool {
					t.Errorf("[%s] 枚举 %s 的 ValueType = %q, 只允许空、int、string 或 bool", sec.Key, f.Key, f.ValueType)
				}
				// 布尔枚举只允许 true/false 两个取值，且必须成对出现
				if f.ValueType == driver.FieldBool {
					if len(f.Options) != 2 || !optSeen["true"] || !optSeen["false"] {
						t.Errorf("[%s] 布尔枚举 %s 必须恰好有 true/false 两个取值", sec.Key, f.Key)
					}
				}
			case driver.FieldInt, driver.FieldFloat:
				if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
					t.Errorf("[%s] %s 的 min(%d) > max(%d)", sec.Key, f.Key, *f.Min, *f.Max)
				}
			}
			if f.Scale != 0 && f.Scale != 1 && f.Type != driver.FieldInt {
				t.Errorf("[%s] %s 只有数值字段才允许换算，当前 type=%s scale=%d", sec.Key, f.Key, f.Type, f.Scale)
			}
			if f.ReadOnly && f.WriteOnly {
				t.Errorf("[%s] %s 不可能既只读又只写", sec.Key, f.Key)
			}
		}
	}
	// 与官方 WebUI 的标签页对齐的 8 个分节
	if sections != 8 {
		t.Errorf("分节数 = %d, 期望 8（行为/下载/连接/速度/BitTorrent/RSS/WebUI/高级）", sections)
	}
	// 虚拟时刻字段也在索引里（读写都要经过白名单）
	for _, key := range []string{"schedule_from", "schedule_to"} {
		if _, ok := qbPrefIndex()[key]; !ok {
			t.Errorf("索引缺少虚拟时刻字段 %s", key)
		}
	}
}

// TestSettingsSchemaCoveredByMock 自述的每个键都必须能在 mock 里读到：
// 缺键说明 mock 的偏好表落后于自述表（面板会显示空白），
// 真机上则意味着该键在目标版本里已被移除，自述表该跟着改。
func TestSettingsSchemaCoveredByMock(t *testing.T) {
	url := startMock(t, qbmock.Options{Seed: 1})
	c, _ := New(url, "", "")
	raw := rawPrefs(t, c)

	for _, sec := range c.SettingsSchema() {
		for _, f := range sec.Fields {
			if f.Type == driver.FieldTime {
				// 时刻字段由 hour/min 四键合成
				for _, suffix := range []string{"_hour", "_min"} {
					if _, ok := raw[f.Key+suffix]; !ok {
						t.Errorf("[%s] mock 缺少 %s%s", sec.Key, f.Key, suffix)
					}
				}
				continue
			}
			if _, ok := raw[f.Key]; !ok {
				t.Errorf("[%s] mock 的偏好表缺少 %s", sec.Key, f.Key)
			}
		}
	}
}

// TestQBReadNormalization 读取侧的键集合与取值归一
func TestQBReadNormalization(t *testing.T) {
	url := startMock(t, qbmock.Options{Seed: 1})
	c, _ := New(url, "", "")
	sess, err := c.GetSession(context.Background())
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	qb := sess.QB
	if len(qb) == 0 {
		t.Fatal("Session.QB 为空")
	}

	// 键集合 = 自述键（只写字段不回显，故排除）
	wantKeys := map[string]bool{}
	for key, f := range qbPrefIndex() {
		if f.WriteOnly {
			continue
		}
		wantKeys[key] = true
		if _, ok := qb[key]; !ok {
			t.Errorf("Session.QB 缺少 %s", key)
		}
	}
	for key := range qb {
		if !wantKeys[key] {
			t.Errorf("Session.QB 出现自述之外的键 %s", key)
		}
	}

	for _, tc := range []struct {
		key  string
		want any
	}{
		{"alt_dl_limit", int64(10)},                  // 10240 bytes/s → 10 KiB/s
		{"dl_limit", int64(0)},                       // 不限速
		{"slow_torrent_dl_rate_threshold", int64(2)}, // 2048 bytes/s → 2 KiB/s
		{"schedule_from", "08:00"},                   // hour/min 合成
		{"schedule_to", "20:00"},
		{"scheduler_days", int64(0)},
		{"bittorrent_protocol", int64(0)},
		{"proxy_type", "None"},       // 字符串枚举
		{"dyndns_service", int64(0)}, // 上游给 -1（未启用）→ 回落到首项
		{"torrent_content_layout", "Original"},
		{"dht", true},
		{"web_ui_port", int64(8080)},
		{"max_ratio", float64(-1)},
	} {
		if got := qb[tc.key]; got != tc.want {
			t.Errorf("Session.QB[%s] = %#v, 期望 %#v", tc.key, got, tc.want)
		}
	}
	// 只写字段（密码）永不回显
	if _, ok := qb["web_ui_password"]; ok {
		t.Error("Session.QB 回显了只写字段 web_ui_password")
	}
}

// TestQBWriteRoundTrip 逐类型写入后回读
func TestQBWriteRoundTrip(t *testing.T) {
	url := startMock(t, qbmock.Options{Seed: 1})
	c, _ := New(url, "", "")
	ctx := context.Background()

	patch := map[string]any{
		"preallocate_all":      true,           // 开关
		"listen_port":          int64(51413),   // 整数
		"dl_limit":             int64(2048),    // 整数 + 换算（KiB/s）
		"max_ratio":            2.5,            // 小数（配对开关自动打开）
		"proxy_type":           "SOCKS5",       // 字符串枚举
		"scheduler_days":       int64(1),       // 数值枚举
		"schedule_from":        "09:30",        // 虚拟时刻（HH:MM）
		"file_log_path":        "/tmp/qb-logs", // 文本
		"add_trackers_enabled": true,
		// 布尔枚举（上游界面是下拉）：真值与字符串两种写法都要能落地
		"auto_tmm_enabled":              true,
		"torrent_changed_tmm_enabled":   false,
		"save_path_changed_tmm_enabled": "true",
	}
	if err := c.SetSession(ctx, driver.SessionPatch{QB: patch}); err != nil {
		t.Fatalf("SetSession: %v", err)
	}

	raw := rawPrefs(t, c)
	for _, tc := range []struct {
		key  string
		want any
	}{
		{"dl_limit", float64(2048 * 1024)}, // 落地为 bytes/s
		{"schedule_from_hour", float64(9)},
		{"schedule_from_min", float64(30)},
		{"max_ratio_enabled", true}, // 只提交值 → 自动补启用位
		{"auto_tmm_enabled", true},
		{"torrent_changed_tmm_enabled", false},
		{"save_path_changed_tmm_enabled", true}, // "true" 字符串也要落地为布尔
	} {
		if got := raw[tc.key]; got != tc.want {
			t.Errorf("上游 %s = %#v, 期望 %#v", tc.key, got, tc.want)
		}
	}

	sess, err := c.GetSession(ctx)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	for _, tc := range []struct {
		key  string
		want any
	}{
		{"preallocate_all", true},
		{"listen_port", int64(51413)},
		{"dl_limit", int64(2048)},
		{"max_ratio", 2.5},
		{"max_ratio_enabled", true},
		{"proxy_type", "SOCKS5"},
		{"scheduler_days", int64(1)},
		{"schedule_from", "09:30"},
		{"file_log_path", "/tmp/qb-logs"},
		{"add_trackers_enabled", true},
		{"auto_tmm_enabled", true},
		{"torrent_changed_tmm_enabled", false},
		{"save_path_changed_tmm_enabled", true},
		// 未提交的键不受影响
		{"scheduler_enabled", false},
		{"save_path", "/downloads"},
	} {
		if got := sess.QB[tc.key]; got != tc.want {
			t.Errorf("回读 %s = %#v, 期望 %#v", tc.key, got, tc.want)
		}
	}
	if sess.DownloadDir != "/downloads" {
		t.Errorf("通用字段被误改: DownloadDir = %q", sess.DownloadDir)
	}
}

// TestQBWriteValidation 非法输入必须报 ErrInvalid 且不落盘
func TestQBWriteValidation(t *testing.T) {
	url := startMock(t, qbmock.Options{Seed: 1})
	c, _ := New(url, "", "")
	ctx := context.Background()

	for _, tc := range []struct {
		name  string
		patch map[string]any
	}{
		{"未知键", map[string]any{"nope": int64(1)}},
		{"只读键", map[string]any{"web_ui_api_key": "qbt_x"}},
		{"整数超范围", map[string]any{"listen_port": int64(70000)}},
		{"整数给文本", map[string]any{"listen_port": "abc"}},
		{"数值枚举越界", map[string]any{"scheduler_days": int64(42)}},
		{"字符串枚举非法", map[string]any{"proxy_type": "SOCKS9"}},
		{"开关给文本", map[string]any{"preallocate_all": "yes"}},
		{"时刻超范围", map[string]any{"schedule_from": "25:00"}},
		{"时刻格式", map[string]any{"schedule_from": "9点半"}},
		{"时刻给数字", map[string]any{"schedule_from": int64(930)}},
	} {
		err := c.SetSession(ctx, driver.SessionPatch{QB: tc.patch})
		if err == nil {
			t.Errorf("%s: 应当报错", tc.name)
			continue
		}
		if !errors.Is(err, driver.ErrInvalid) {
			t.Errorf("%s: 错误应包装 ErrInvalid, 得到 %v", tc.name, err)
		}
	}

	// 一个键非法 → 整批不落地（先校验再提交）
	err := c.SetSession(ctx, driver.SessionPatch{QB: map[string]any{
		"preallocate_all": true,
		"listen_port":     int64(70000),
	}})
	if !errors.Is(err, driver.ErrInvalid) {
		t.Fatalf("混合非法补丁应报 ErrInvalid, 得到 %v", err)
	}
	sess, gerr := c.GetSession(ctx)
	if gerr != nil {
		t.Fatalf("GetSession: %v", gerr)
	}
	if sess.QB["preallocate_all"] != false {
		t.Error("非法补丁导致部分字段落地")
	}
}

// TestQBAndLegacyMerge 通用字段与 QB 通道同时提交：QB 通道后应用，同键以它为准
func TestQBAndLegacyMerge(t *testing.T) {
	url := startMock(t, qbmock.Options{Seed: 1})
	c, _ := New(url, "", "")
	ctx := context.Background()

	down := int64(1000) // 通用通道写 dl_limit = 1000 KiB/s
	if err := c.SetSession(ctx, driver.SessionPatch{
		SpeedLimitDown: &down,
		QB:             map[string]any{"dl_limit": int64(4096)},
	}); err != nil {
		t.Fatalf("SetSession: %v", err)
	}
	sess, err := c.GetSession(ctx)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.QB["dl_limit"] != int64(4096) {
		t.Errorf("QB 通道应覆盖通用字段, 得到 %v", sess.QB["dl_limit"])
	}
	if sess.SpeedLimitDown != 4096 {
		t.Errorf("通用读回 = %d KiB/s, 期望 4096", sess.SpeedLimitDown)
	}
}

// TestQBPairedSwitches 值键与启用键配对：显式提交关闭后不被自动打开
func TestQBPairedSwitches(t *testing.T) {
	url := startMock(t, qbmock.Options{Seed: 1})
	c, _ := New(url, "", "")
	ctx := context.Background()

	// 值 + 显式关闭：最终以显式关闭为准，且值仍写入
	if err := c.SetSession(ctx, driver.SessionPatch{QB: map[string]any{
		"max_seeding_time":         int64(120),
		"max_seeding_time_enabled": false,
	}}); err != nil {
		t.Fatalf("SetSession: %v", err)
	}
	sess, err := c.GetSession(ctx)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.QB["max_seeding_time"] != int64(120) {
		t.Errorf("max_seeding_time = %v, 期望 120", sess.QB["max_seeding_time"])
	}
	if sess.QB["max_seeding_time_enabled"] != false {
		t.Errorf("显式关闭被覆盖: %v", sess.QB["max_seeding_time_enabled"])
	}
}
