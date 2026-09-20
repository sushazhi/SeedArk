// Package qbittorrent 基于 qBittorrent Web API v2（qBittorrent 5.x，WebAPI 2.15+）
// 实现 driver.Backend，让面板用同一套界面管理 qBittorrent。
//
// 认证按官方最新能力双通道支持：
//   - API Key：qBittorrent ≥ 5.2.0（WebAPI ≥ 2.14.1）支持无状态令牌，
//     密码形如 qbt_xxxx（32 位）时直接走 Authorization: Bearer，不再依赖 cookie；
//   - 用户名 + 密码：/api/v2/auth/login 取 SID cookie，过期（403）自动重登一次。
//
// 与 Transmission 的两个关键差异在这里抹平：
//  1. 主键：qBittorrent 用 infohash 寻址，面板统一用 int64 ID，
//     由 hashID 做确定性 hash→ID 映射（进程重启后同一 hash 仍是同一 ID）；
//  2. 单位：面板统一 KB/s，qBittorrent Web API 用 bytes/s，换算集中在 mapper。
package qbittorrent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/trpanel/backend/internal/driver"
	"github.com/trpanel/backend/internal/models"
)

// Client qBittorrent Web API 客户端
type Client struct {
	base   string // 规范化后的根地址（不含 /api/v2）
	user   string
	pass   string
	apiKey string // qBittorrent 5.2+ 的 API Key（qbt_ 前缀）

	http    *http.Client
	loginMu sync.Mutex
	// loggedIn 仅表示「曾成功取到 cookie」；cookie 失效由 403 触发重登
	loggedIn bool

	// hash ↔ 面板 ID 映射（列表刷新时同步）
	idMu     sync.RWMutex
	hashToID map[string]int64
	idToHash map[int64]string

	// 列表缓存（与 Transmission 侧同一策略：写操作后作废）
	listMu     sync.Mutex
	listCache  []*models.Torrent
	listCached time.Time

	// 累计流量缓存（只有 /sync/maindata 提供，代价较高）
	alltimeMu sync.RWMutex
	alltimeDL int64
	alltimeUL int64
	alltimeAt time.Time

	// hash → 分类。标签与分类在面板里统一呈现为 labels，
	// 写回标签时需要知道哪个标签其实是分类，不能一并塞进 tags
	catMu       sync.RWMutex
	categories  map[string]string
	trackerMu   sync.RWMutex
	trackers    map[string][]string
	trackerTime time.Time

	// 版本兼容层缓存（机制与用法见 compat.go）：
	// endpointGone 记忆 404 端点避免重复浪费往返；webAPI* 缓存 WebAPI 版本判定
	goneMu       sync.RWMutex
	endpointGone map[string]time.Time
	compatMu     sync.Mutex
	webAPIMajor  int
	webAPIMinor  int
	webAPIKnown  bool
}

// New 创建 qBittorrent 客户端
func New(rawURL, user, pass string) (*Client, error) {
	base, err := normalizeBase(rawURL)
	if err != nil {
		return nil, err
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("初始化 cookie 容器失败: %w", err)
	}
	c := &Client{
		base:       base,
		user:       user,
		pass:       pass,
		http:       newHTTPClient(jar),
		hashToID:   map[string]int64{},
		idToHash:   map[int64]string{},
		categories: map[string]string{},
		trackers:   map[string][]string{},
	}
	// 密码形如 qbt_ 开头 + 共 32 位 → 按 5.2 的 API Key 处理
	if isAPIKey(pass) {
		c.apiKey = pass
	}
	return c, nil
}

// errEndpointMissing 该 qBittorrent 版本没有此端点（HTTP 404），调用方按版本回退
var errEndpointMissing = errors.New("端点不存在")

// errAlreadyExists HTTP 409：种子已存在
var errAlreadyExists = errors.New("种子已存在")

// isAPIKey 判断是否为 qBittorrent 5.2+ 的 API Key（qbt_ + 28 位，共 32 位）
func isAPIKey(s string) bool {
	if len(s) != 32 || !strings.HasPrefix(s, "qbt_") {
		return false
	}
	// 从 4 开始：跳过 "qbt_" 里的下划线，否则所有合法 Key 都会被误判为非法
	for i := 4; i < len(s); i++ {
		b := s[i]
		if (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') {
			continue
		}
		return false
	}
	return true
}

// normalizeBase 规范化 WebUI 地址：
// 允许 http://host:8080、http://host:8080/、http://host:8080/api/v2 等写法，
// 统一成不带结尾斜杠、不含 /api/v2 的根地址（请求时统一拼接 /api/v2/...）。
func normalizeBase(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("qBittorrent 地址不能为空")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("qBittorrent 地址无效: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("qBittorrent 地址协议必须是 http/https，当前为 %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("qBittorrent 地址缺少主机")
	}
	p := strings.TrimSuffix(u.Path, "/")
	for strings.HasSuffix(p, "/") {
		p = strings.TrimSuffix(p, "/")
	}
	// 去掉用户可能填上的 API 前缀，避免拼成 /api/v2/api/v2/...
	p = strings.TrimSuffix(p, "/api/v2")
	u.Path = p
	u.RawQuery = ""
	u.Fragment = ""
	u.User = nil
	return strings.TrimSuffix(u.String(), "/"), nil
}

// newHTTPClient 构造访问 qBittorrent 的专用 HTTP 客户端（超时 / TLS 下限 / cookie）
func newHTTPClient(jar http.CookieJar) *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Jar:     jar,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
			MaxIdleConns:          16,
			MaxIdleConnsPerHost:   8,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

// Kind 下载器类型
func (c *Client) Kind() driver.Kind { return driver.KindQBittorrent }

// Capabilities qBittorrent 能力自述。
// 没有带宽组、黑名单、端口检测、块位图（有 pieceStates 但要逐块请求，
// 大种子代价过高，故不在列表接口提供）等 Transmission 专属概念。
func (c *Client) Capabilities() driver.Capabilities {
	return driver.Capabilities{
		BandwidthGroups:    false,
		Blocklist:          false,
		FreeSpace:          true,
		PortTest:           false,
		SequentialDownload: true,
		QueueMove:          true,
		RenameFile:         true,
		SystemCommand:      true,
		AltSpeedSchedule:   true,
		TrackerReplace:     true,
		PieceBitmap:        true,
		IncompleteDir:      true,
		ScriptHooks:        false,
		GlobalSeedRatio:    true,
		// qB 无队列停滞判定（只有统一的 max_active 与轮转），
		// 会话设置不映射 uTP / 全局连接数，单种设置不映射带宽优先级与
		// 遵循全局限速，也不支持重命名未完成文件、回收源种子文件
		QueueStalled:     false,
		PeerLimit:        false,
		PerTorrentLimits: false,
		FileHandling:     false,
		UtpToggle:        false,
	}
}

// Ping 连通性检测：返回 qBittorrent 版本（附带 Web API 版本便于排查兼容问题）
func (c *Client) Ping(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var version string
	if err := c.get(ctx, "app/version", nil, &version); err != nil {
		return "", err
	}
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if version == "" {
		return "", fmt.Errorf("qBittorrent 未返回版本信息")
	}
	// 服务器可达且响应正常：端点缺失记忆可能源于网络抖动或对端升级，重置之
	c.clearMissing()
	return version, nil
}

// WebAPIVersion Web API 版本（诊断用）
func (c *Client) WebAPIVersion(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var v string
	if err := c.get(ctx, "app/webapiVersion", nil, &v); err != nil {
		return "", err
	}
	return strings.TrimSpace(v), nil
}

// ---- HTTP 封装 ----

// post 显式 POST（写操作）
func (c *Client) post(ctx context.Context, endpoint string, form url.Values) error {
	_, err := c.raw(ctx, http.MethodPost, c.base+"/api/v2/"+endpoint, form,
		strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", true)
	return err
}

// postMultipart 上传 .torrent 文件（multipart/form-data）
func (c *Client) postMultipart(ctx context.Context, endpoint string, fields map[string]string, files map[string][]byte) ([]byte, error) {
	body, contentType, err := buildMultipart(fields, files)
	if err != nil {
		return nil, err
	}
	return c.raw(ctx, http.MethodPost, c.base+"/api/v2/"+endpoint, nil,
		strings.NewReader(string(body)), contentType, true)
}

// get 读取并把 JSON 响应解析到 out（out 为 nil 时丢弃响应体）
func (c *Client) get(ctx context.Context, endpoint string, params url.Values, out any) error {
	apiURL := c.base + "/api/v2/" + endpoint
	if len(params) > 0 {
		apiURL += "?" + params.Encode()
	}
	data, err := c.raw(ctx, http.MethodGet, apiURL, nil, nil, "", true)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	// app/version、app/webapiVersion 等端点返回纯文本（如 "v5.2.3"），
	// 目标为 *string 时原样写入，其余场景仍要求 JSON
	if s, ok := out.(*string); ok && !looksJSON(data) {
		*s = string(data)
		return nil
	}
	return decodeJSON(data, out)
}

// raw 执行请求并处理认证：401/403 时重新登录重试一次。
// allowPlain 表示允许非 JSON 的纯文本响应（如 "Ok."）。
func (c *Client) raw(ctx context.Context, method, apiURL string, form url.Values, body io.Reader, contentType string, allowPlain bool) ([]byte, error) {
	do := func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, apiURL, body)
		if err != nil {
			return nil, err
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		// qBittorrent 自 4.4 起对写请求做 CSRF 校验：Referer/Origin 必须与 Host 一致，
		// 缺省会被 403。这里对每个请求都带上，跨域反代场景也能过。
		req.Header.Set("Referer", c.base+"/")
		req.Header.Set("Origin", c.base)
		// Web API 2.15+（qBittorrent 5.2）的 torrents/add 带 Accept 头时返回
		// added_torrent_ids JSON 结果，可精确定位新种子；旧版本忽略该头回 "Ok."。
		if endpointOf(apiURL) == "torrents/add" {
			req.Header.Set("Accept", "application/json")
		}
		if c.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}
		return c.http.Do(req)
	}

	if c.apiKey == "" {
		if err := c.ensureLogin(ctx); err != nil {
			return nil, err
		}
	}
	resp, err := do()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if c.apiKey != "" {
			return nil, fmt.Errorf("qBittorrent 拒绝访问（HTTP %d）：API Key 无效或已轮换", resp.StatusCode)
		}
		// cookie 过期或被封禁：重新登录后再试一次
		c.loginMu.Lock()
		c.loggedIn = false
		c.loginMu.Unlock()
		if err := c.ensureLogin(ctx); err != nil {
			return nil, err
		}
		resp, err = do()
		if err != nil {
			return nil, err
		}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		// 端点不存在通常意味着版本过旧（如 4.x 没有 torrents/stop 与 setTags）。
		// 用 sentinel 包装：调用方据此回退到旧端点（errors.Is 判定）
		return nil, fmt.Errorf("qBittorrent 不支持该接口（HTTP 404）：%s：%w", endpointOf(apiURL), errEndpointMissing)
	case resp.StatusCode == http.StatusConflict:
		return nil, fmt.Errorf("qBittorrent 拒绝该请求（HTTP 409）：参数冲突或种子已存在：%w", errAlreadyExists)
	case resp.StatusCode == http.StatusUnsupportedMediaType:
		return nil, errors.New("qBittorrent 拒绝该请求（HTTP 415）：种子文件无效")
	case resp.StatusCode >= 400:
		return nil, fmt.Errorf("qBittorrent 请求失败（HTTP %d）: %s", resp.StatusCode, truncateStr(string(data), 160))
	}
	if !allowPlain && len(data) > 0 && !looksJSON(data) {
		return nil, fmt.Errorf("qBittorrent 返回了非 JSON 响应: %s", truncateStr(string(data), 160))
	}
	return data, nil
}

// ensureLogin 保证持有有效会话（API Key 模式下无需登录）
func (c *Client) ensureLogin(ctx context.Context) error {
	if c.apiKey != "" {
		return nil
	}
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	if c.loggedIn {
		return nil
	}
	// 未配置凭据：依赖 qBittorrent 的「本机免认证」设置，直接放行
	if c.user == "" && c.pass == "" {
		c.loggedIn = true
		return nil
	}
	form := url.Values{"username": {c.user}, "password": {c.pass}}
	apiURL := c.base + "/api/v2/auth/login"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", c.base+"/")
	req.Header.Set("Origin", c.base)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("连接 qBittorrent 失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode == http.StatusForbidden:
		return errors.New("qBittorrent 登录被拒绝（HTTP 403）：IP 因多次失败已被临时封禁")
	case strings.TrimSpace(string(body)) == "Fails.":
		return errors.New("qBittorrent 登录失败：用户名或密码错误")
	case resp.StatusCode >= 400:
		return fmt.Errorf("qBittorrent 登录失败（HTTP %d）", resp.StatusCode)
	}
	// 5.x 在成功时也可能只在 Set-Cookie 里给 SID 而不返回 "Ok."
	if len(resp.Cookies()) == 0 && strings.TrimSpace(string(body)) != "Ok." {
		return errors.New("qBittorrent 登录失败：用户名或密码错误")
	}
	c.loggedIn = true
	slog.Debug("qBittorrent 登录成功", "base", c.base)
	return nil
}

// ---- 通用工具 ----

// decodeJSON 解析响应体。qBittorrent 某些端点返回空 body（如 204），
// 静默成功即可，不该当成解析错误。
func decodeJSON(data []byte, out any) error {
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("解析 qBittorrent 响应失败: %w", err)
	}
	return nil
}

func endpointOf(apiURL string) string {
	if i := strings.Index(apiURL, "/api/v2/"); i >= 0 {
		return apiURL[i+len("/api/v2/"):]
	}
	return apiURL
}

func looksJSON(data []byte) bool {
	s := strings.TrimSpace(string(data))
	return strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[")
}

func truncateStr(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// buildMultipart 构造 multipart 请求体（添加种子用）
func buildMultipart(fields map[string]string, files map[string][]byte) ([]byte, string, error) {
	var buf strings.Builder
	// 手写 multipart：只需支持文本字段与单文件，避免引入 mime/multipart 的临时文件开销
	const boundary = "----trpanelQbBoundary"
	addField := func(name, value string) {
		buf.WriteString("--" + boundary + "\r\n")
		buf.WriteString(fmt.Sprintf("Content-Disposition: form-data; name=%q\r\n\r\n", name))
		buf.WriteString(value + "\r\n")
	}
	for _, k := range sortedKeys(fields) {
		addField(k, fields[k])
	}
	for _, k := range sortedKeysBytes(files) {
		data := files[k]
		buf.WriteString("--" + boundary + "\r\n")
		buf.WriteString(fmt.Sprintf("Content-Disposition: form-data; name=%q; filename=%q\r\n", k, "torrent.torrent"))
		buf.WriteString("Content-Type: application/x-bittorrent\r\n\r\n")
		buf.Write(data)
		buf.WriteString("\r\n")
	}
	buf.WriteString("--" + boundary + "--\r\n")
	return []byte(buf.String()), "multipart/form-data; boundary=" + boundary, nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func sortedKeysBytes(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// ---- 种子 ID 映射 ----

// IDFor 把 infohash 映射为面板使用的 int64 ID。
// 用 FNV-1a 64 确定性计算：进程重启后同一 hash 仍得到同一 ID，
// 持久化在状态文件里的引用（做种策略记录、限速接管表）不会串号。
func (c *Client) IDFor(hash string) int64 {
	if hash == "" {
		return 0
	}
	c.idMu.RLock()
	if id, ok := c.hashToID[hash]; ok {
		c.idMu.RUnlock()
		return id
	}
	c.idMu.RUnlock()

	c.idMu.Lock()
	defer c.idMu.Unlock()
	return c.assignIDLocked(hash)
}

// HashFor 面板 ID → infohash（空串表示未知，调用方应报「种子不存在」）
func (c *Client) HashFor(id int64) string {
	c.idMu.RLock()
	defer c.idMu.RUnlock()
	return c.idToHash[id]
}

// syncIDs 列表刷新时同步映射表
func (c *Client) syncIDs(hashes []string) {
	c.idMu.Lock()
	defer c.idMu.Unlock()
	for _, h := range hashes {
		if h == "" {
			continue
		}
		if _, ok := c.hashToID[h]; ok {
			continue
		}
		c.assignIDLocked(h)
	}
}

// assignIDLocked 为一个 hash 分配（或复用）面板 ID，需持有 idMu 写锁。
// 映射表惰性初始化：直接以零值 Client 调用也必须可用（单元测试与延迟创建场景）。
func (c *Client) assignIDLocked(hash string) int64 {
	if id, ok := c.hashToID[hash]; ok {
		return id
	}
	if c.hashToID == nil {
		c.hashToID = map[string]int64{}
	}
	if c.idToHash == nil {
		c.idToHash = map[int64]string{}
	}
	// 低 40 位对齐聚合编码：高位留给服务器编号
	id := int64(fnv1a64(hash)) & (maxLocalID - 1)
	if id == 0 {
		id = 1
	}
	// 极小概率碰撞：线性探测到空闲槽
	for i := 0; ; i++ {
		cand := id + int64(i)
		if cand >= maxLocalID {
			cand = cand % (maxLocalID - 1)
			if cand == 0 {
				cand = 1
			}
		}
		if other, occupied := c.idToHash[cand]; !occupied || other == hash {
			id = cand
			break
		}
	}
	c.hashToID[hash] = id
	c.idToHash[id] = hash
	return id
}

// fnv1a64 FNV-1a 64 位哈希
func fnv1a64(s string) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
	}
	return h
}

// maxLocalID 本地 ID 上限（与聚合编码的低位宽度一致）
const maxLocalID = int64(1) << 40

// hashesParam 把面板 ID 列表转成 qBittorrent 的 hashes 参数（| 分隔；空表示全部）
func (c *Client) hashesParam(ids []int64) string {
	if len(ids) == 0 {
		return "all"
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		if h := c.HashFor(id); h != "" {
			parts = append(parts, h)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "|")
}

// requireHashes 校验这批 ID 都能解析出 hash
func (c *Client) requireHashes(ids []int64) (string, error) {
	if len(ids) == 0 {
		return "all", nil
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		h := c.HashFor(id)
		if h == "" {
			return "", fmt.Errorf("种子 %d 不存在（可能已删除，请刷新列表）", id)
		}
		parts = append(parts, h)
	}
	return strings.Join(parts, "|"), nil
}

// rememberCategory 记录种子的分类（标签写回时用于剔除分类项）
func (c *Client) rememberCategory(hash, category string) {
	c.catMu.Lock()
	defer c.catMu.Unlock()
	if c.categories == nil {
		c.categories = map[string]string{}
	}
	if category == "" {
		delete(c.categories, hash)
		return
	}
	c.categories[hash] = category
}

// CategoryOf 返回该种子的分类
func (c *Client) CategoryOf(hash string) string {
	c.catMu.RLock()
	defer c.catMu.RUnlock()
	return c.categories[hash]
}

// compile-time 断言：确保实现 driver.Backend
var _ driver.Backend = (*Client)(nil)
