package qbmock

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// ---- 测试脚手架：直连 HTTP，与驱动走同一套 wire 协议 ----

type cli struct {
	t    *testing.T
	c    *http.Client
	base string
	sid  string
	key  string
}

func newCLI(t *testing.T, opts Options) (*cli, *Server) {
	t.Helper()
	srv := New(opts)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &cli{t: t, c: ts.Client(), base: ts.URL + "/api/v2", key: opts.APIKey}, srv
}

func (c *cli) req(method, endpoint string, form url.Values) *http.Request {
	target := c.base + "/" + endpoint
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		c.t.Fatalf("构造请求失败: %v", err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	// 与真实 WebUI 一致：写请求带同源 Origin/Referer
	host := strings.TrimPrefix(c.base, "http://")
	if i := strings.Index(host, "/"); i > 0 {
		host = host[:i]
	}
	req.Header.Set("Origin", "http://"+host)
	req.Header.Set("Referer", "http://"+host+"/")
	if c.sid != "" {
		req.AddCookie(&http.Cookie{Name: "SID", Value: c.sid})
	}
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	return req
}

// doRaw 发请求，返回状态码与响应体
func (c *cli) doRaw(method, endpoint string, form url.Values) (int, string) {
	c.t.Helper()
	resp, err := c.c.Do(c.req(method, endpoint, form))
	if err != nil {
		c.t.Fatalf("%s %s 请求失败: %v", method, endpoint, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

// mustGet 取 JSON 并解析到 out
func (c *cli) mustGet(endpoint string, params url.Values, out any) {
	c.t.Helper()
	target := c.base + "/" + endpoint
	if len(params) > 0 {
		target += "?" + params.Encode()
	}
	resp, err := c.c.Get(target)
	if err != nil {
		c.t.Fatalf("GET %s: %v", target, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("GET %s = %d: %s", target, resp.StatusCode, data)
	}
	if out == nil {
		return
	}
	if err := json.Unmarshal(data, out); err != nil {
		c.t.Fatalf("解析 %s 响应失败: %v（body=%s）", endpoint, err, data)
	}
}

// mustPost 表单 POST，期望 2xx
func (c *cli) mustPost(endpoint string, form url.Values) string {
	c.t.Helper()
	status, body := c.doRaw(http.MethodPost, endpoint, form)
	if status >= 300 {
		c.t.Fatalf("POST %s = %d: %s", endpoint, status, body)
	}
	return body
}

// infoOne 按 hash 取一条 torrents/info
func (c *cli) infoOne(hash string) map[string]any {
	c.t.Helper()
	var list []map[string]any
	c.mustGet("torrents/info", url.Values{"hashes": {hash}}, &list)
	if len(list) != 1 {
		c.t.Fatalf("torrents/info?hashes=%s 返回 %d 条，期望 1", hash, len(list))
	}
	return list[0]
}

// hashes 列出全部 hash（按 added_on 升序）
func (c *cli) hashes() []string {
	c.t.Helper()
	var list []map[string]any
	c.mustGet("torrents/info", nil, &list)
	out := make([]string, 0, len(list))
	for _, m := range list {
		h, _ := m["hash"].(string)
		out = append(out, h)
	}
	return out
}

// login 走 cookie 认证（清空 API Key 头，避免 auth 端点拒绝 Bearer）
func (c *cli) login(user, pass string) {
	c.t.Helper()
	c.key = ""
	status, body := c.doRaw(http.MethodPost, "auth/login",
		url.Values{"username": {user}, "password": {pass}})
	if status != http.StatusOK || body != "Ok." {
		c.t.Fatalf("登录失败: %d %q", status, body)
	}
	req, err := http.NewRequest(http.MethodPost, c.base+"/auth/login",
		strings.NewReader((url.Values{"username": {user}, "password": {pass}}).Encode()))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.c.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	for _, ck := range resp.Cookies() {
		if ck.Name == "SID" {
			c.sid = ck.Value
		}
	}
	if c.sid == "" {
		c.t.Fatal("登录后未拿到 SID cookie")
	}
}

// ---- 认证 / CSRF ----

func TestAuthModes(t *testing.T) {
	// cookie 模式：错密码回 "Fails."，正确密码下发 SID
	c, _ := newCLI(t, Options{Seed: 1, User: "admin", Pass: "secret", APIKey: "qbt_" + strings.Repeat("a", 28)})
	c.key = "" // 登录端点不接受 Bearer，这里只测 cookie 链路
	if status, body := c.doRaw(http.MethodPost, "auth/login",
		url.Values{"username": {"admin"}, "password": {"nope"}}); status != http.StatusOK || body != "Fails." {
		t.Fatalf("错误密码 = (%d,%q), 期望 (200,\"Fails.\")", status, body)
	}
	c.login("admin", "secret")
	if _, body := c.doRaw(http.MethodPost, "torrents/reannounce", url.Values{"hashes": {"all"}}); body == "Fails." {
		t.Fatal("SID cookie 未被接受")
	}

	// API Key 模式：Bearer 直连；auth 端点拒绝 Bearer（与官方一致）
	k, _ := newCLI(t, Options{Seed: 1, APIKey: "qbt_" + strings.Repeat("b", 28)})
	if status, _ := k.doRaw(http.MethodGet, "app/version", nil); status != http.StatusOK {
		t.Fatalf("Bearer 读取 app/version = %d, 期望 200", status)
	}
	k.key = "Bearer-bad"
	if status, _ := k.doRaw(http.MethodGet, "app/version", nil); status != http.StatusForbidden {
		t.Fatalf("错误 Bearer = %d, 期望 403", status)
	}

	// CSRF：跨站 POST 必须被拒
	c.key = ""
	req := c.req(http.MethodPost, "torrents/addTags", url.Values{"hashes": {"x"}, "tags": {"y"}})
	req.Header.Set("Origin", "http://evil.example")
	resp, err := c.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("跨站 POST = %d, 期望 401", resp.StatusCode)
	}
}

// ---- 数据自洽：files / pieceStates / properties ----

func TestTorrentDataSelfConsistent(t *testing.T) {
	c, _ := newCLI(t, Options{Seed: 9})
	for _, h := range c.hashes() {
		info := c.infoOne(h)
		size, _ := info["size"].(float64)
		if info["state"] == "metaDL" {
			if size != 0 {
				t.Fatalf("%s metaDL 的 size = %v, 期望 0（元数据未到）", h, size)
			}
			continue
		}
		var files []map[string]any
		c.mustGet("torrents/files", url.Values{"hash": {h}}, &files)
		var sum float64
		for _, f := range files {
			fs, _ := f["size"].(float64)
			sum += fs
		}
		if size > 0 && int64(sum) != int64(size) {
			t.Errorf("%s 文件体积合计 %v != size %v", h, sum, size)
		}
		var pieces []int
		c.mustGet("torrents/pieceStates", url.Values{"hash": {h}}, &pieces)
		if len(pieces) == 0 {
			t.Fatalf("%s 无块状态", h)
		}
		done := 0
		for _, s := range pieces {
			if s < 0 || s > 2 {
				t.Fatalf("%s 块状态 %d 越界", h, s)
			}
			if s == 2 {
				done++
			}
		}
		var props map[string]any
		c.mustGet("torrents/properties", url.Values{"hash": {h}}, &props)
		if have, _ := props["pieces_have"].(float64); int64(have) != int64(done) {
			t.Errorf("%s pieces_have=%v 与实际已下载块数 %d 不符", h, have, done)
		}
		if num, _ := props["pieces_num"].(float64); int64(num) != int64(len(pieces)) {
			t.Errorf("%s pieces_num=%v != len(pieceStates)=%d", h, num, len(pieces))
		}
		// 块数 × 块大小 ≈ 体积（允许整除误差）
		ps, _ := props["piece_size"].(float64)
		if int64(ps)*int64(len(pieces)) > int64(size)+int64(ps) {
			t.Errorf("%s piece_size*pieces=%v 超过 size %v", h, int64(ps)*int64(len(pieces)), size)
		}
	}
}

// ---- torrents/info 参数 ----

func TestTorrentsInfoParams(t *testing.T) {
	c, _ := newCLI(t, Options{Seed: 10})
	all := c.hashes()
	if len(all) != 10 {
		t.Fatalf("默认列表 %d 条, 期望 10", len(all))
	}
	var limited []map[string]any
	c.mustGet("torrents/info", url.Values{"limit": {"3"}}, &limited)
	if len(limited) != 3 {
		t.Errorf("limit=3 返回 %d 条", len(limited))
	}
	var two []map[string]any
	c.mustGet("torrents/info", url.Values{"hashes": {strings.Join(all[:2], "|")}}, &two)
	if len(two) != 2 {
		t.Errorf("hashes 双选返回 %d 条, 期望 2", len(two))
	}
	var seeded, paused []map[string]any
	c.mustGet("torrents/info", url.Values{"filter": {"seeding"}}, &seeded)
	c.mustGet("torrents/info", url.Values{"filter": {"paused"}}, &paused)
	if len(seeded) == 0 || len(paused) == 0 {
		t.Errorf("filter 覆盖不足: seeding=%d paused=%d", len(seeded), len(paused))
	}
	for _, m := range seeded {
		if m["progress"].(float64) < 1 {
			t.Errorf("seeding 过滤漏进未完成种子 %v", m["progress"])
		}
	}
	for _, m := range paused {
		st, _ := m["state"].(string)
		if !isStoppedStateName(st) {
			t.Errorf("paused 过滤漏进 %s", st)
		}
	}
	// 4.x 命名回退
	c4, _ := newCLI(t, Options{Seed: 9, Compat: "4.x"})
	var list4 []map[string]any
	c4.mustGet("torrents/info", nil, &list4)
	found := false
	for _, m := range list4 {
		st, _ := m["state"].(string)
		if strings.HasPrefix(st, "stopped") {
			t.Errorf("4.x 模式不应出现 stopped* 状态: %v", st)
		}
		if st == "pausedUP" || st == "pausedDL" {
			found = true
		}
	}
	if !found {
		t.Error("4.x 模式未布置 paused* 停止态样本")
	}
}

// ---- 状态动作 ----

func TestStartStopAreCompletionAware(t *testing.T) {
	c, _ := newCLI(t, Options{Seed: 9})
	var list []map[string]any
	c.mustGet("torrents/info", nil, &list)
	var seedHash, dlHash string
	for _, m := range list {
		if m["progress"].(float64) >= 1 && (m["state"] == "stoppedUP" || m["state"] == "pausedUP") {
			seedHash, _ = m["hash"].(string)
		}
		if m["progress"].(float64) < 1 && (m["state"] == "stoppedDL" || m["state"] == "pausedDL") {
			dlHash, _ = m["hash"].(string)
		}
	}
	if seedHash == "" || dlHash == "" {
		t.Fatal("缺少 stoppedUP / stoppedDL 样本")
	}
	// 完成态恢复 → 做种；未完成 → 下载
	c.mustPost("torrents/start", url.Values{"hashes": {seedHash + "|" + dlHash}})
	if st := c.infoOne(seedHash)["state"]; st != "uploading" {
		t.Errorf("完成态 start 后 = %v, 期望 uploading", st)
	}
	if st := c.infoOne(dlHash)["state"]; st != "downloading" {
		t.Errorf("未完成 start 后 = %v, 期望 downloading", st)
	}
	c.mustPost("torrents/stop", url.Values{"hashes": {seedHash + "|" + dlHash}})
	if st := c.infoOne(seedHash)["state"]; st != "stoppedUP" {
		t.Errorf("完成态 stop 后 = %v, 期望 stoppedUP", st)
	}
	if st := c.infoOne(dlHash)["state"]; st != "stoppedDL" {
		t.Errorf("未完成 stop 后 = %v, 期望 stoppedDL", st)
	}
	if eta := c.infoOne(dlHash)["eta"]; eta != float64(etaUnknown) {
		t.Errorf("停止后 eta = %v, 期望 %d", eta, etaUnknown)
	}
	// 4.x 命名
	c4, _ := newCLI(t, Options{Seed: 1, Compat: "4.x"})
	h := c4.hashes()[0]
	c4.mustPost("torrents/pause", url.Values{"hashes": {h}})
	if st := c4.infoOne(h)["state"]; st != "pausedDL" {
		t.Errorf("4.x pause 后 = %v, 期望 pausedDL", st)
	}
}

func TestCompat4xHidesFiveOnlyEndpoints(t *testing.T) {
	c4, _ := newCLI(t, Options{Seed: 2, Compat: "4.x"})
	h := c4.hashes()[0]
	for _, ep := range []string{"torrents/start", "torrents/stop", "torrents/setTags"} {
		if status, _ := c4.doRaw(http.MethodPost, ep, url.Values{"hashes": {h}}); status != http.StatusNotFound {
			t.Errorf("4.x %s = %d, 期望 404", ep, status)
		}
	}
	// 磁力添加在 4.x 只回纯文本，不带 JSON 结果
	body := c4.mustPost("torrents/add", url.Values{
		"urls": {"magnet:?xt=urn:btih:" + strings.Repeat("1a", 20) + "&dn=legacy"}})
	if body != "Ok." {
		t.Errorf("4.x 添加响应 = %q, 期望 \"Ok.\"", body)
	}
}

func TestTagCategoryAndQueueActions(t *testing.T) {
	c, _ := newCLI(t, Options{Seed: 4})
	all := c.hashes()
	c.mustPost("torrents/setTags", url.Values{"hashes": {all[0]}, "tags": {"alpha, beta"}})
	c.mustPost("torrents/addTags", url.Values{"hashes": {all[0]}, "tags": {"gamma"}})
	c.mustPost("torrents/removeTags", url.Values{"hashes": {all[0]}, "tags": {"alpha"}})
	tags, _ := c.infoOne(all[0])["tags"].(string)
	if tags != "beta, gamma" {
		t.Errorf("tags = %q, 期望 \"beta, gamma\"（逗号+空格连接）", tags)
	}

	c.mustPost("torrents/createCategory", url.Values{"category": {"shows"}, "savePath": {"/tv"}})
	c.mustPost("torrents/setCategory", url.Values{"hashes": {all[1]}, "category": {"shows"}})
	var main map[string]any
	c.mustGet("sync/maindata", url.Values{"rid": {"0"}}, &main)
	cats, _ := main["categories"].(map[string]any)
	if cats["shows"] != "/tv" {
		t.Errorf("maindata categories = %v, 期望含 shows→/tv", cats)
	}
	if got := c.infoOne(all[1])["category"]; got != "shows" {
		t.Errorf("category = %v, 期望 shows", got)
	}
	// 带分类的新种子继承分类保存路径
	c.mustPost("torrents/add", url.Values{
		"urls": {"magnet:?xt=urn:btih:" + strings.Repeat("2b", 20)}, "category": {"shows"}})
	var added []map[string]any
	c.mustGet("torrents/info", url.Values{"hashes": {strings.Repeat("2b", 20)}}, &added)
	if len(added) != 1 || added[0]["save_path"] != "/tv" {
		t.Fatalf("分类保存路径未生效: %+v", added)
	}

	// 队列：bottomPrio 把队首挪到队尾，topPrio 再挪回队首
	last := all[len(all)-1]
	c.mustPost("torrents/topPrio", url.Values{"hashes": {last}})
	var after []map[string]any
	c.mustGet("torrents/info", nil, &after)
	if got := c.infoOne(last)["priority"]; got != float64(1) {
		t.Errorf("topPrio 后 priority = %v, 期望 1", got)
	}
	c.mustPost("torrents/bottomPrio", url.Values{"hashes": {last}})
	if got := c.infoOne(last)["priority"]; int64(got.(float64)) != int64(len(after)) {
		t.Errorf("bottomPrio 后 priority = %v, 期望 %d", got, len(after))
	}
}

func TestSequentialAndRename(t *testing.T) {
	c, srv := newCLI(t, Options{Seed: 1})
	hash := c.hashes()[0]
	c.mustPost("torrents/toggleSequentialDownload", url.Values{"hashes": {hash}})
	if got := c.infoOne(hash)["seq_dl"]; got != true {
		t.Errorf("seq_dl = %v, 期望 true", got)
	}

	// 磁力 + 1080p 名称 → 元数据到达后是多文件（顶层目录），
	// 正好覆盖驱动的 renameFile 失败 → renameFolder 回退链。
	// 种子数据里非 iso 名称有 1/3 概率是单文件（刻意保留的形态差异），
	// 所以逐个换磁力哈希，直到抽到多文件样本
	var (
		magnet string
		folder string
	)
	for i := 0; i < 8; i++ {
		magnet = strings.Repeat("3c", 19) + fmt.Sprintf("%02x", i)
		c.mustPost("torrents/add", url.Values{
			"urls": {"magnet:?xt=urn:btih:" + magnet + "&dn=Movie.2024.1080p.WEB"}})
		for j := 0; j < 4; j++ {
			srv.Step(2 * time.Second)
		}
		var files []map[string]any
		c.mustGet("torrents/files", url.Values{"hash": {magnet}}, &files)
		if len(files) < 2 {
			continue
		}
		first, _ := files[0]["name"].(string)
		if k := strings.Index(first, "/"); k > 0 {
			folder = first[:k]
			break
		}
	}
	if folder == "" {
		t.Fatal("8 次抽样都没拿到多文件样本")
	}
	// 目录名不是文件名：renameFile 必须失败，否则驱动永远走不到 renameFolder
	if status, _ := c.doRaw(http.MethodPost, "torrents/renameFile", url.Values{
		"hash": {magnet}, "oldPath": {folder}, "newPath": {"Renamed.2024"}}); status != http.StatusNotFound {
		t.Fatalf("renameFile 对目录 = %d, 期望 404", status)
	}
	c.mustPost("torrents/renameFolder", url.Values{
		"hash": {magnet}, "oldPath": {folder}, "newPath": {"Renamed.2024"}})
	names := filePaths(c, magnet)
	for _, name := range names {
		if !strings.HasPrefix(name, "Renamed.2024/") {
			t.Errorf("renameFolder 后文件仍为 %q", name)
		}
	}
	if cp := c.infoOne(magnet)["content_path"]; !strings.Contains(cp.(string), "Renamed.2024") {
		t.Errorf("content_path = %v, 期望跟随目录改名", cp)
	}
	// 真正的单文件改名走 renameFile
	c.mustPost("torrents/renameFile", url.Values{
		"hash": {magnet}, "oldPath": {names[0]}, "newPath": {"Renamed.2024/primary.mkv"}})
	if got := filePaths(c, magnet)[0]; got != "Renamed.2024/primary.mkv" {
		t.Errorf("renameFile 后 = %q, 期望 Renamed.2024/primary.mkv", got)
	}

	// setLocation 迁移保存目录，content_path 跟随
	c.mustPost("torrents/setLocation", url.Values{"hashes": {magnet}, "location": {"/mnt/nas/dl"}})
	info := c.infoOne(magnet)
	if sp, _ := info["save_path"].(string); sp != "/mnt/nas/dl" {
		t.Errorf("save_path = %q, 期望 /mnt/nas/dl", sp)
	}
	if cp, _ := info["content_path"].(string); !strings.HasPrefix(cp, "/mnt/nas/dl/") {
		t.Errorf("content_path = %q, 期望挂在新目录下", cp)
	}
}

// filePaths 取种子文件路径列表（保持索引序）
func filePaths(c *cli, hash string) []string {
	c.t.Helper()
	var files []map[string]any
	c.mustGet("torrents/files", url.Values{"hash": {hash}}, &files)
	out := make([]string, 0, len(files))
	for _, f := range files {
		n, _ := f["name"].(string)
		out = append(out, n)
	}
	return out
}

// ---- Step 动态模拟 ----

func TestStepProgressesAndCompletes(t *testing.T) {
	c, srv := newCLI(t, Options{Seed: 1})
	hash := c.hashes()[0]
	before := c.infoOne(hash)
	if before["state"] != "downloading" {
		t.Fatalf("初始状态 = %v, 期望 downloading", before["state"])
	}
	p0, _ := before["progress"].(float64)
	// 步长跑满「下载完」为止：单帧 10 分钟，几十帧足够任何体积收尾
	for i := 0; i < 40; i++ {
		srv.Step(10 * time.Minute)
		if p, _ := c.infoOne(hash)["progress"].(float64); p >= 1 {
			break
		}
	}
	after := c.infoOne(hash)
	p1, _ := after["progress"].(float64)
	if p1 <= p0 {
		t.Fatalf("Step 后进度未推进: %v → %v", p0, p1)
	}
	if p1 < 1 {
		t.Fatalf("进度 = %v, 期望推进到完成", p1)
	}
	if after["state"] != "uploading" {
		t.Errorf("完成后状态 = %v, 期望 uploading", after["state"])
	}
	if done, _ := after["completion_on"].(float64); done <= 0 {
		t.Errorf("completion_on = %v, 期望已写入", done)
	}
	if up, _ := after["uploaded"].(float64); up <= 0 {
		t.Errorf("做种后 uploaded = %v, 期望 > 0", up)
	}
	if ratio, _ := after["ratio"].(float64); ratio <= 0 {
		t.Errorf("做种后 ratio = %v, 期望 > 0", ratio)
	}
}

func TestStepResolvesMagnetMetadata(t *testing.T) {
	c, srv := newCLI(t, Options{Seed: 1})
	hash := strings.Repeat("4d", 20)
	c.mustPost("torrents/add", url.Values{"urls": {"magnet:?xt=urn:btih:" + hash + "&dn=Magnet.Show.S01E01"}})
	info := c.infoOne(hash)
	if info["state"] != "metaDL" {
		t.Fatalf("磁力初始状态 = %v, 期望 metaDL", info["state"])
	}
	if hm, _ := info["has_metadata"].(bool); hm {
		t.Fatal("元数据不应在取到前就为 true")
	}
	srv.Step(2 * time.Second)
	srv.Step(2 * time.Second)
	srv.Step(2 * time.Second)
	after := c.infoOne(hash)
	if after["state"] == "metaDL" {
		t.Fatal("Step 后仍停留在 metaDL")
	}
	if size, _ := after["size"].(float64); size <= 0 {
		t.Errorf("元数据到达后 size = %v, 期望 > 0", size)
	}
	if after["name"] != "Magnet.Show.S01E01" {
		t.Errorf("name = %v, 期望保留 dn 参数", after["name"])
	}
	var files []map[string]any
	c.mustGet("torrents/files", url.Values{"hash": {hash}}, &files)
	if len(files) == 0 {
		t.Error("元数据到达后应有文件列表")
	}
}

func TestStepCheckingReportsVerifyProgress(t *testing.T) {
	c, srv := newCLI(t, Options{Seed: 1})
	hash := c.hashes()[0]
	before := c.infoOne(hash)
	p0, _ := before["progress"].(float64)
	c.mustPost("torrents/recheck", url.Values{"hashes": {hash}})
	during := c.infoOne(hash)
	if during["state"] != "checkingDL" {
		t.Fatalf("校验中状态 = %v, 期望 checkingDL", during["state"])
	}
	if p, _ := during["progress"].(float64); p >= p0 {
		t.Errorf("校验中 progress = %v, 真实 qB 此时报的是校验进度（应低于 %v）", p, p0)
	}
	for i := 0; i < 10; i++ {
		srv.Step(2 * time.Second)
	}
	after := c.infoOne(hash)
	if after["state"] == "checkingDL" || after["state"] == "checkingUP" {
		t.Fatalf("校验未在预期步数内结束: %v", after["state"])
	}
	if p, _ := after["progress"].(float64); p < p0*0.9 {
		t.Errorf("校验后 progress = %v, 期望还原到校验前的 %v 附近", p, p0)
	}
}

func TestStepStatsAndLimits(t *testing.T) {
	c, srv := newCLI(t, Options{Seed: 3})
	var first map[string]any
	c.mustGet("transfer/info", nil, &first)
	baseDL, _ := first["dl_info_data"].(float64)
	var state map[string]any
	c.mustGet("sync/maindata", url.Values{"rid": {"0"}}, &state)
	ss, _ := state["server_state"].(map[string]any)
	all0, _ := ss["alltime_dl"].(float64)
	if free, _ := ss["free_space_on_disk"].(float64); int64(free) != 500*1024*1024*1024 {
		t.Errorf("free_space_on_disk = %v", free)
	}
	for i := 0; i < 5; i++ {
		srv.Step(2 * time.Second)
	}
	var after map[string]any
	c.mustGet("transfer/info", nil, &after)
	if dl, _ := after["dl_info_data"].(float64); dl <= baseDL {
		t.Errorf("会话下载量未累积: %v → %v", baseDL, dl)
	}
	if speed, _ := after["dl_info_speed"].(float64); speed <= 0 {
		t.Errorf("会话速率 = %v, 期望 > 0", speed)
	}
	c.mustGet("sync/maindata", url.Values{"rid": {"0"}}, &state)
	ss, _ = state["server_state"].(map[string]any)
	if all1, _ := ss["alltime_dl"].(float64); all1 <= all0 {
		t.Errorf("累计下载未增长: %v → %v", all0, all1)
	}
	// 全局限速下调后，速率必须被压到限额内
	c.mustPost("app/setPreferences", url.Values{"json": {`{"dl_limit":1048576,"totally_unknown_key":1}`}})
	var dl float64
	for i := 0; i < 10; i++ {
		srv.Step(2 * time.Second)
		var info map[string]any
		c.mustGet("transfer/info", nil, &info)
		dl, _ = info["dl_info_speed"].(float64)
	}
	if dl > 3*1024*1024 {
		t.Errorf("全局限速 1MiB/s 下总速率 %v 未受约束", dl)
	}
	var prefs map[string]any
	c.mustGet("app/preferences", nil, &prefs)
	if _, ok := prefs["totally_unknown_key"]; ok {
		t.Error("未知偏好键被静默接受，真实 qB 会忽略")
	}
	if v, _ := prefs["dl_limit"].(float64); int64(v) != 1048576 {
		t.Errorf("dl_limit = %v, 期望 1048576", prefs["dl_limit"])
	}
	// 单种限速：更小的那一份
	c.mustPost("torrents/setDownloadLimit", url.Values{"hashes": {c.hashes()[0]}, "limit": {"10240"}})
	for i := 0; i < 3; i++ {
		srv.Step(2 * time.Second)
	}
	if sp, _ := c.infoOne(c.hashes()[0])["dlspeed"].(float64); sp > 10240*1.4 {
		t.Errorf("单种限速 10KiB/s 下 dlspeed = %v", sp)
	}
}

// 关闭后一切请求失败，模拟 app/shutdown 后的实例
func TestShutdownStopsServing(t *testing.T) {
	c, _ := newCLI(t, Options{Seed: 1})
	c.mustPost("app/shutdown", url.Values{})
	if status, _ := c.doRaw(http.MethodGet, "app/version", nil); status != http.StatusBadRequest {
		t.Fatalf("shutdown 后 app/version = %d, 期望 400", status)
	}
}

// 删除与计数
func TestDeleteRemovesTorrents(t *testing.T) {
	c, _ := newCLI(t, Options{Seed: 3})
	all := c.hashes()
	c.mustPost("torrents/delete", url.Values{"hashes": {all[0]}, "deleteFiles": {"true"}})
	var n int
	c.mustGet("torrents/count", nil, &n)
	if n != 2 {
		t.Fatalf("删除后 count = %d, 期望 2", n)
	}
	if status, _ := c.doRaw(http.MethodPost, "torrents/reannounce", url.Values{"hashes": {all[0]}}); status != http.StatusNotFound {
		t.Errorf("对已删除种子操作 = %d, 期望 404", status)
	}
	// hashes=all 命中全部
	c.mustPost("torrents/reannounce", url.Values{"hashes": {"all"}})
}

// peers 响应必须是 {"peers": {...}} 信封，键为 ip:port
func TestPeersEnvelope(t *testing.T) {
	c, _ := newCLI(t, Options{Seed: 1})
	hash := c.hashes()[0]
	var resp map[string]map[string]map[string]any
	c.mustGet("torrents/peers", url.Values{"hash": {hash}}, &resp)
	peers, ok := resp["peers"]
	if !ok || len(peers) == 0 {
		t.Fatalf("peers 响应缺少信封或对端: %v", resp)
	}
	for key, p := range peers {
		if !strings.Contains(key, ":") {
			t.Errorf("peer 键 %q 不是 ip:port 形式", key)
		}
		if p["ip"] == nil || p["client"] == nil {
			t.Errorf("peer %q 缺字段: %v", key, p)
		}
	}
	// trackers 响应含 status/tier/msg
	var trackers []map[string]any
	c.mustGet("torrents/trackers", url.Values{"hash": {hash}}, &trackers)
	if len(trackers) == 0 {
		t.Fatal("trackers 为空")
	}
	for _, tr := range trackers {
		if _, ok := tr["status"]; !ok {
			t.Errorf("tracker 缺 status: %v", tr)
		}
	}
}
