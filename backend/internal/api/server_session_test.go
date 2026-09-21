package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sushazhi/seedark/backend/internal/driver"
	"github.com/sushazhi/seedark/backend/internal/qbmock"
	"github.com/sushazhi/seedark/backend/internal/rpc"
	"github.com/sushazhi/seedark/backend/internal/state"
)

// memberFixture 两台 qBittorrent 服务器，各自一个 mock 后端：
// 0 号为活动服务器，1 号为聚合成员。两台必须分开，
// 否则「改 1 号不影响 0 号」无从验证。
type memberFixture struct {
	handler *Handler
	engine  *gin.Engine
	mocks   []*httptest.Server // [0]=0 号服务器，[1]=1 号服务器
}

func newMemberFixture(t *testing.T) *memberFixture {
	t.Helper()
	return newMemberFixtureWith(t, 0)
}

// newMemberFixtureWith 与 newMemberFixture 相同，但活动连接（主连接）指向
// primaryIdx 号服务器。primaryIdx 与状态里的 activeServer（固定 0）不一致时，
// 用来模拟「状态文件与运行配置不同步」。
func newMemberFixtureWith(t *testing.T, primaryIdx int) *memberFixture {
	t.Helper()
	ts0 := httptest.NewServer(qbmock.New(qbmock.Options{Seed: 1}))
	t.Cleanup(ts0.Close)
	ts1 := httptest.NewServer(qbmock.New(qbmock.Options{Seed: 1}))
	t.Cleanup(ts1.Close)
	mocks := []*httptest.Server{ts0, ts1}

	dir := t.TempDir()
	st, err := state.Load(filepath.Join(dir, "sa-state.json"))
	if err != nil {
		t.Fatalf("加载状态文件失败: %v", err)
	}
	servers := []state.Server{
		{Name: "本地", Type: "qbittorrent", URL: ts0.URL, Enabled: true},
		{Name: "远端", Type: "qbittorrent", URL: ts1.URL, Enabled: true},
		{Name: "未启用", Type: "qbittorrent", URL: ts1.URL, Enabled: false},
		{Name: "缺地址", Type: "qbittorrent", URL: "", Enabled: true},
	}
	if err := st.Update(func(s *state.State) {
		s.Servers = servers
		s.ActiveServer = 0
	}); err != nil {
		t.Fatalf("写入状态失败: %v", err)
	}

	mgr, err := rpc.NewManager(rpc.Credentials{Type: driver.KindQBittorrent, URL: mocks[primaryIdx].URL})
	if err != nil {
		t.Fatalf("创建 Manager 失败: %v", err)
	}
	targets := make([]rpc.Target, 0, len(servers))
	for i, s := range servers {
		targets = append(targets, rpc.Target{
			Index: i, Kind: driver.KindQBittorrent, URL: s.URL, Enabled: s.Enabled,
		})
	}
	mgr.SetTargets(targets)

	h := &Handler{rpc: mgr, state: st, dataDir: dir}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/servers/:index/session", h.getServerSession)
	r.PUT("/api/servers/:index/session", h.setServerSession)
	r.GET("/api/servers/:index/free-space", h.serverFreeSpace)
	return &memberFixture{handler: h, engine: r, mocks: mocks}
}

// doJSON 发一次请求并返回状态码与解析后的响应体
func (f *memberFixture) doJSON(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)

	var out map[string]any
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("响应不是 JSON: %v (%s)", err, w.Body.String())
		}
	}
	return w.Code, out
}

// sessionOf 取出响应 data.session
func sessionOf(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	data, ok := resp["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 data: %v", resp)
	}
	sess, ok := data["session"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 data.session: %v", resp)
	}
	return sess
}

// 按索引读取：类型、能力、设置字段自述与偏好取值都要带上
func TestServerSessionRead(t *testing.T) {
	f := newMemberFixture(t)
	for _, idx := range []string{"0", "1"} {
		code, resp := f.doJSON(t, "GET", "/api/servers/"+idx+"/session", "")
		if code != http.StatusOK {
			t.Fatalf("GET %s 状态码 = %d, 响应 %v", idx, code, resp)
		}
		data := resp["data"].(map[string]any)
		if data["name"] != "本地" && data["name"] != "远端" {
			t.Errorf("服务器名 = %v", data["name"])
		}
		// active 告诉界面「这个标签是不是当前连接那台」：必须按实际连接判定，
		// 界面据它决定要不要同步全局会话、要不要放开端口测试
		wantActive := idx == "0"
		if data["active"] != wantActive {
			t.Errorf("%s 号 active = %v, 期望 %v", idx, data["active"], wantActive)
		}
		sess := sessionOf(t, resp)
		if sess["type"] != "qbittorrent" {
			t.Errorf("type = %v, 期望 qbittorrent", sess["type"])
		}
		schema, ok := sess["schema"].([]any)
		if !ok || len(schema) == 0 {
			t.Fatalf("schema 为空: %v", sess["schema"])
		}
		// 原生偏好走中性的 prefs 通道（原 qb 键已随去 kind 特判一并更名）
		qb, ok := sess["prefs"].(map[string]any)
		if !ok || len(qb) == 0 {
			t.Fatalf("prefs 偏好通道为空: %v", sess["prefs"])
		}
		// 数值字段按 Scale 换算后回传（dl_limit 原生是 bytes/s）
		if got := qb["dl_limit"]; got != float64(0) {
			t.Errorf("dl_limit = %v, 期望 0", got)
		}
		if got := qb["alt_dl_limit"]; got != float64(10) {
			t.Errorf("alt_dl_limit = %v, 期望 10 (KiB/s)", got)
		}
	}
}

// 按索引写入：只改目标那台，另一台的取值不受影响
func TestServerSessionWriteIsolatedByIndex(t *testing.T) {
	f := newMemberFixture(t)

	// 用旧的 qb 键提交：老前端尚未升级，后端必须继续受理（Prefs/QB 同义）
	code, resp := f.doJSON(t, "PUT", "/api/servers/1/session", `{"qb":{"dl_limit":4096,"preallocate_all":true}}`)
	if code != http.StatusOK {
		t.Fatalf("PUT 状态码 = %d, 响应 %v", code, resp)
	}

	_, resp = f.doJSON(t, "GET", "/api/servers/1/session", "")
	sess := sessionOf(t, resp)
	qb := sess["prefs"].(map[string]any)
	if qb["dl_limit"] != float64(4096) {
		t.Errorf("1 号 dl_limit = %v, 期望 4096", qb["dl_limit"])
	}
	if qb["preallocate_all"] != true {
		t.Errorf("1 号 preallocate_all = %v, 期望 true", qb["preallocate_all"])
	}

	_, resp = f.doJSON(t, "GET", "/api/servers/0/session", "")
	qb0 := sessionOf(t, resp)["prefs"].(map[string]any)
	if qb0["dl_limit"] != float64(0) {
		t.Errorf("0 号 dl_limit = %v, 期望 0（未被越权改动）", qb0["dl_limit"])
	}
	if qb0["preallocate_all"] != false {
		t.Errorf("0 号 preallocate_all = %v, 期望 false（未被越权改动）", qb0["preallocate_all"])
	}
}

// 路由与校验：索引越界、未启用、缺地址分别给出可分辨的错误
func TestServerSessionTargetErrors(t *testing.T) {
	f := newMemberFixture(t)
	cases := []struct {
		name string
		path string
		code int
		want string
	}{
		{"非法索引", "/api/servers/abc/session", http.StatusBadRequest, "无效的服务器索引"},
		{"索引越界", "/api/servers/9/session", http.StatusNotFound, "服务器不存在"},
		{"负索引", "/api/servers/-1/session", http.StatusNotFound, "服务器不存在"},
		{"未启用", "/api/servers/2/session", http.StatusBadRequest, "未启用"},
		{"缺地址", "/api/servers/3/session", http.StatusBadRequest, "未启用"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, resp := f.doJSON(t, "GET", c.path, "")
			if code != c.code {
				t.Fatalf("状态码 = %d, 期望 %d（响应 %v）", code, c.code, resp)
			}
			if msg, _ := resp["message"].(string); !strings.Contains(msg, c.want) {
				t.Errorf("message = %q, 期望包含 %q", msg, c.want)
			}
		})
	}
}

// 逐台查询磁盘余量：路径取该台自己的下载目录（不是活动那台），
// 且 qBittorrent 的总量未知以 0 回传——界面据此隐藏「/ 总量」
func TestServerFreeSpacePerIndex(t *testing.T) {
	f := newMemberFixture(t)
	mockSetPref(t, f.mocks[0].URL, `{"save_path":"/mnt/A"}`)
	mockSetPref(t, f.mocks[1].URL, `{"save_path":"/mnt/B"}`)

	cases := []struct {
		idx      string
		name     string
		wantPath string
	}{
		{"0", "本地", "/mnt/A"},
		{"1", "远端", "/mnt/B"},
	}
	for _, c := range cases {
		code, resp := f.doJSON(t, "GET", "/api/servers/"+c.idx+"/free-space", "")
		if code != http.StatusOK {
			t.Fatalf("GET %s 状态码 = %d, 响应 %v", c.idx, code, resp)
		}
		data := resp["data"].(map[string]any)
		if data["name"] != c.name {
			t.Errorf("%s 号 name = %v, 期望 %v", c.idx, data["name"], c.name)
		}
		if data["path"] != c.wantPath {
			t.Errorf("%s 号 path = %v, 期望 %v（必须是该台自己的下载目录）", c.idx, data["path"], c.wantPath)
		}
		if got := data["freeSpace"]; got != float64(500*1024*1024*1024) {
			t.Errorf("%s 号 freeSpace = %v, 期望 500 GiB", c.idx, got)
		}
		if got := data["totalSize"]; got != float64(0) {
			t.Errorf("%s 号 totalSize = %v, 期望 0（qBittorrent 不提供总容量）", c.idx, got)
		}
	}
}

// 索引不合法 / 越界 / 未启用时应给出可分辨的错误，而不是回落到活动那台
func TestServerFreeSpaceTargetErrors(t *testing.T) {
	f := newMemberFixture(t)
	cases := []struct {
		name string
		path string
		code int
		want string
	}{
		{"非法索引", "/api/servers/abc/free-space", http.StatusBadRequest, "无效的服务器索引"},
		{"索引越界", "/api/servers/9/free-space", http.StatusNotFound, "服务器不存在"},
		{"未启用", "/api/servers/2/free-space", http.StatusBadRequest, "未启用"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, resp := f.doJSON(t, "GET", c.path, "")
			if code != c.code {
				t.Fatalf("状态码 = %d, 期望 %d（响应 %v）", code, c.code, resp)
			}
			if msg, _ := resp["message"].(string); !strings.Contains(msg, c.want) {
				t.Errorf("message = %q, 期望包含 %q", msg, c.want)
			}
		})
	}
}

// 偏好键名/取值不合法时是调用方的问题，返回 400 而不是「上游故障」502
func TestServerSessionInvalidPrefIsBadRequest(t *testing.T) {
	f := newMemberFixture(t)
	code, resp := f.doJSON(t, "PUT", "/api/servers/1/session", `{"qb":{"no_such_pref":1}}`)
	if code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, 期望 400（响应 %v）", code, resp)
	}
	// 校验失败必须整批不生效：合法字段也不能落盘
	_, resp = f.doJSON(t, "GET", "/api/servers/1/session", "")
	qb := sessionOf(t, resp)["prefs"].(map[string]any)
	if qb["preallocate_all"] != false {
		t.Errorf("preallocate_all = %v, 期望 false（校验失败不应部分生效）", qb["preallocate_all"])
	}
}

// 状态文件与运行配置不同步（activeServer 指向 0 号、实际连接却是 1 号）时，
// 0 号标签必须读写 0 号那台：一旦落到「当前连接」，用户在 0 号标签上的
// 一次保存就会改掉另一台下载器的配置。
func TestServerSessionActiveMismatchUsesOwnServer(t *testing.T) {
	f := newMemberFixtureWith(t, 1) // 主连接指向 1 号，状态里 activeServer 仍是 0
	mockSetPref(t, f.mocks[0].URL, `{"save_path":"/mnt/A"}`)
	mockSetPref(t, f.mocks[1].URL, `{"save_path":"/mnt/B"}`)

	code, resp := f.doJSON(t, "GET", "/api/servers/0/session", "")
	if code != http.StatusOK {
		t.Fatalf("GET 状态码 = %d, 期望 200（响应 %v）", code, resp)
	}
	// 索引看似是「活动服务器」，但实际连接指向 1 号：active 必须为 false，
	// 界面才不会把这次修改同步到全局会话（那会串成另一台的设置）
	if data := resp["data"].(map[string]any); data["active"] != false {
		t.Errorf("0 号 active = %v, 期望 false", data["active"])
	}
	// 连接指向 1 号，但 0 号标签读到的必须是 0 号的设置
	if got := sessionOf(t, resp)["prefs"].(map[string]any)["save_path"]; got != "/mnt/A" {
		t.Errorf("0 号 save_path = %v, 期望 /mnt/A", got)
	}

	// 磁盘余量走同一条路由规则：问 0 号的余量，查的必须是 0 号的目录
	code, resp = f.doJSON(t, "GET", "/api/servers/0/free-space", "")
	if code != http.StatusOK {
		t.Fatalf("GET free-space 状态码 = %d, 期望 200（响应 %v）", code, resp)
	}
	if got := resp["data"].(map[string]any)["path"]; got != "/mnt/A" {
		t.Errorf("0 号 free-space path = %v, 期望 /mnt/A（当前连接那台的 /mnt/B 不得被查到）", got)
	}

	code, resp = f.doJSON(t, "PUT", "/api/servers/0/session", `{"qb":{"preallocate_all":true}}`)
	if code != http.StatusOK {
		t.Fatalf("PUT 状态码 = %d, 期望 200（响应 %v）", code, resp)
	}
	if prefs := mockPrefs(t, f.mocks[0].URL); prefs["preallocate_all"] != true {
		t.Errorf("0 号 preallocate_all = %v, 期望 true", prefs["preallocate_all"])
	}
	if prefs := mockPrefs(t, f.mocks[1].URL); prefs["preallocate_all"] != false {
		t.Errorf("1 号 preallocate_all = %v, 期望 false（当前连接那台不得被误改）", prefs["preallocate_all"])
	}
}

// 索引既标着「活动」、又拿不到成员实例（单台部署 / 该台已停用）时：
// 只能报错，绝不能退回到当前连接那台去读改。
func TestServerSessionMismatchWithoutMemberRefused(t *testing.T) {
	f := newMemberFixtureWith(t, 1)
	if err := f.handler.state.Update(func(s *state.State) {
		s.Servers = s.Servers[:1]
		s.Servers[0].Enabled = false
	}); err != nil {
		t.Fatalf("写入状态失败: %v", err)
	}
	f.handler.rpc.SetTargets(nil)

	code, resp := f.doJSON(t, "PUT", "/api/servers/0/session", `{"qb":{"preallocate_all":true}}`)
	if code != http.StatusBadRequest {
		t.Fatalf("PUT 状态码 = %d, 期望 400（响应 %v）", code, resp)
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "不一致") {
		t.Errorf("message = %q, 期望提示连接与服务器不一致", msg)
	}
	if prefs := mockPrefs(t, f.mocks[1].URL); prefs["preallocate_all"] != false {
		t.Errorf("当前连接那台的 preallocate_all = %v, 期望 false（不得被误改）", prefs["preallocate_all"])
	}
}

// mockPrefs 直接从 mock 实例读原始偏好，绕开被测代码
func mockPrefs(t *testing.T, base string) map[string]any {
	t.Helper()
	resp, err := http.Get(base + "/api/v2/app/preferences")
	if err != nil {
		t.Fatalf("读取 mock 偏好失败: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("解析 mock 偏好失败: %v", err)
	}
	return out
}

// mockSetPref 直接改 mock 实例的偏好，用于造出可分辨的两台
func mockSetPref(t *testing.T, base, prefsJSON string) {
	t.Helper()
	form := url.Values{"json": {prefsJSON}}
	req, err := http.NewRequest(http.MethodPost, base+"/api/v2/app/setPreferences", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("写入 mock 偏好失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("写入 mock 偏好状态码 = %d", resp.StatusCode)
	}
}
