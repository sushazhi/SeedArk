// Package qbmock 提供一个内存态的 qBittorrent WebAPI 模拟服务器，
// 对齐 qBittorrent 5.2.3（Web API v2.15.1）：
//   - API Key 认证（qbt_ 前缀，Authorization: Bearer；auth 端点拒绝 Bearer）
//   - cookie 认证（POST /api/v2/auth/login 下发 SID；失败回 "Fails."）
//   - CSRF 校验（POST 必须带与 Host 一致的 Referer/Origin，与 4.4+ 行为一致）
//   - torrents/add 返回 {"added_torrent_ids":[...],"success_count":..}（5.2+ JSON 响应）
//   - torrents/start|stop（5.0+）与 pause/resume/setForceStart（4.x 起）
//   - setTags（5.1+）、toggleSequentialDownload、filePrio、setShareLimits 等
//
// 数据与动态行为对齐 cmd/trmock：真实名称/站点池的种子样本、覆盖全部状态、
// Step() 周期推进速率抖动/进度增长/完成转做种/校验恢复/磁力元数据到达，
// 由 cmd/qbmock 的 ticker 驱动（-static 关闭）；测试里直接 New 的实例保持静态，
// 结果确定。
//
// -compat 4.x 模式会隐藏 5.x 专属端点（start/stop/setTags 返回 404），
// 并把停止态命名回退为 pausedDL/pausedUP，用于验证驱动的旧版本回退逻辑。
package qbmock

import (
	crand "crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	mrand "math/rand"
)

// Options qbmock 配置
type Options struct {
	User    string // 空 = 不校验用户名
	Pass    string // 空 = 不校验密码
	APIKey  string // qbt_ 开头共 32 位；空 = 生成一个
	Compat  string // "5.x"（默认）| "4.x"（隐藏 5.x 专属端点）
	Version string // app/version 回显，默认 v5.2.3
	WebAPI  string // webapiVersion 回显，默认 2.15.1
	Seed    int    // 初始种子数量，默认 6
}

// peer 模拟的对端（字段名与 torrents/peers 的 wire 键一致）
type peer struct {
	IP         string  `json:"ip"`
	Port       int64   `json:"port"`
	Client     string  `json:"client"`
	Connection string  `json:"connection"`
	Country    string  `json:"country"`
	CountryCod string  `json:"country_code"`
	Flags      string  `json:"flags"`
	FlagsDesc  string  `json:"flags_desc"`
	Progress   float64 `json:"progress"`
	DLSpeed    int64   `json:"dl_speed"`
	UPSpeed    int64   `json:"up_speed"`
	Downloaded int64   `json:"downloaded"`
	Uploaded   int64   `json:"uploaded"`
	Relevance  float64 `json:"relevance"`
}

// torrent 模拟的种子。速率字段单位 bytes/s，时间字段为 unix 秒，
// 与 Web API v2 语义一致；-1/-2 等哨兵值（限速、分享率上限）按官方约定。
type torrent struct {
	Hash                     string
	InfohashV1               string
	Name                     string
	State                    string // downloading / uploading / stalledDL / queuedDL / metaDL / error / stoppedUP ...
	Size                     int64
	Progress                 float64
	DLSpeed                  int64
	ULSpeed                  int64
	ETA                      int64 // 秒；qBittorrent 用 8640000 表示未知
	NumSeeds                 int64 // 已连接的做种者
	NumLeechs                int64 // 已连接的下载者
	NumComplete              int64 // 全群做种者；私有站点为 -1（未知）
	NumIncomplete            int64
	Ratio                    float64
	Category                 string
	Tags                     []string
	SavePath                 string
	ContentPath              string
	Downloaded               int64
	Uploaded                 int64
	DownloadedSession        int64
	UploadedSession          int64
	AddedOn                  int64
	CompletionOn             int64
	LastActivity             int64
	TimeActive               int64
	SeedingTime              int64
	Priority                 int64 // 队列位置，1 起
	SeqDL                    bool
	ForceStart               bool
	Private                  bool
	AutoTMM                  bool
	HasMetadata              bool
	Availability             float64
	Reannounce               int64
	Comment                  string
	CreatedBy                string
	CreationDate             int64
	MagnetURI                string
	DLLimit                  int64 // bytes/s，-1 = 全局
	ULLimit                  int64
	RatioLimit               float64 // max_ratio，-2 = 全局 / -1 = 不限
	SeedingTimeLimit         int64
	InactiveSeedingTimeLimit int64
	Files                    []file
	Trackers                 []tracker
	Peers                    map[string]peer
	Pieces                   []int // 0 未下载 / 1 下载中 / 2 已下载

	// 模拟内部字段
	baseDown      int64   // 目标下载速率（B/s），tick 时围绕其抖动
	baseUp        int64   // 目标上传速率（B/s）
	pieceDone     int     // 已校验通过的块数
	checkProgress float64 // 校验进度 0~1（真实 qB 此时把 progress 报成校验进度）
	trueProgress  float64 // 校验期间的真实进度，校验结束后还原
	prevState     string  // checking 结束后恢复
	metaAge       int64   // metaDL 已等待秒数
}

type file struct {
	Index        int64
	Name         string
	Size         int64
	Progress     float64
	Priority     int64
	PieceStart   int64
	PieceEnd     int64
	Availability float64
}

type tracker struct {
	URL           string
	Status        int64 // 2 正常 / 5 报错（WebAPI 2.13+）
	Tier          int64
	NumPeers      int64
	NumSeeds      int64
	NumLeechers   int64
	NumDownloaded int64
	Msg           string
}

// Version 返回模拟的 qBittorrent 版本（如 v5.2.3）
func (s *Server) Version() string { return s.opts.Version }

// WebAPIVersion 返回模拟的 WebAPI 版本（如 2.15.1）
func (s *Server) WebAPIVersion() string { return s.opts.WebAPI }

// APIKey 返回生效的 API Key（空 = 未启用）
func (s *Server) APIKey() string { return s.opts.APIKey }

// Server qbmock 实例
type Server struct {
	opts     Options
	noAuth   bool // 未配置任何凭据：模拟「本机连接免认证」
	mu       sync.Mutex
	rng      *mrand.Rand
	torrents map[string]*torrent
	prefs    map[string]any
	cats     map[string]string // 分类名 → 保存路径
	sid      string

	// 统计（Step 累积；基线值模拟「开机以来」）
	sessionDL, sessionUL int64
	alltimeDL, alltimeUL int64
	totalSessionTime     int64
	shutdown             bool
}

// New 创建 qbmock 处理器
func New(opts Options) *Server {
	if opts.Compat == "" {
		opts.Compat = "5.x"
	}
	if opts.Version == "" {
		if opts.Compat == "4.x" {
			opts.Version = "v4.6.7"
		} else {
			opts.Version = "v5.2.3"
		}
	}
	if opts.WebAPI == "" {
		if opts.Compat == "4.x" {
			opts.WebAPI = "2.11.2"
		} else {
			opts.WebAPI = "2.15.1"
		}
	}
	// 调用方未显式提供任何凭据（含 API Key）时视为匿名模式，
	// 对应 qBittorrent 的「Bypass authentication for clients on localhost」
	noAuth := opts.User == "" && opts.Pass == "" && opts.APIKey == ""
	if opts.APIKey == "" {
		opts.APIKey = "qbt_" + strings.Repeat("a", 28)
	}
	n := opts.Seed
	if n == 0 {
		n = 6
	}
	s := &Server{
		opts:     opts,
		noAuth:   noAuth,
		rng:      newRand(),
		torrents: map[string]*torrent{},
		cats:     map[string]string{},
		sid:      randToken(),
		// 基线累计量：与旧版 mock 常量一致，保证 e2e 断言（200GiB）不漂移
		alltimeDL: 200 * 1024 * 1024 * 1024,
		alltimeUL: 80 * 1024 * 1024 * 1024,
		prefs:     defaultPrefs(opts.APIKey),
	}
	s.seedTorrents(n)
	return s
}

// randToken 生成随机 SID
func randToken() string {
	b := make([]byte, 16)
	_, _ = crand.Read(b)
	return hex.EncodeToString(b)
}

// ---- HTTP 层 ----

// ServeHTTP 实现 http.Handler
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if !strings.HasPrefix(path, "/api/v2/") {
		http.NotFound(w, r)
		return
	}
	endpoint := strings.TrimPrefix(path, "/api/v2/")

	// shutdown 之后拒绝一切
	s.mu.Lock()
	down := s.shutdown
	s.mu.Unlock()
	if down {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if endpoint == "auth/login" {
		s.handleLogin(w, r)
		return
	}

	// 认证：API Key（Bearer）或 SID cookie。API Key 不能访问 auth 端点（上面已返回）
	if !s.authed(r) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "Fails.")
		return
	}

	// CSRF：POST 必须带与 Host 一致的 Origin/Referer（qBittorrent 4.4+ 行为）
	if r.Method == http.MethodPost && !sameOrigin(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "Fails.")
		return
	}

	if s.opts.Compat == "4.x" && isFiveOnly(endpoint) {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	switch endpoint {
	case "app/version":
		writeText(w, s.opts.Version)
	case "app/webapiVersion":
		writeText(w, s.opts.WebAPI)
	case "app/preferences":
		if r.Method == http.MethodGet {
			s.mu.Lock()
			defer s.mu.Unlock()
			writeJSON(w, s.prefs)
			return
		}
		s.handleSetPreferences(w, r)
	case "app/setPreferences":
		// 写入端点（GET app/preferences 仅用于读取）
		s.handleSetPreferences(w, r)
	case "app/shutdown":
		s.mu.Lock()
		s.shutdown = true
		s.mu.Unlock()
		writeText(w, "")
	case "transfer/info":
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, map[string]any{
			"dl_info_speed":     s.rateSumLocked(true),
			"dl_info_data":      s.sessionDL,
			"up_info_speed":     s.rateSumLocked(false),
			"up_info_data":      s.sessionUL,
			"dl_rate_limit":     prefInt64(s.prefs, "dl_limit"),
			"up_rate_limit":     prefInt64(s.prefs, "up_limit"),
			"dht_nodes":         int64(217),
			"connection_status": "connected",
		})
	case "sync/maindata":
		s.handleMaindata(w, r)
	case "torrents/info":
		s.handleTorrentsInfo(w, r)
	case "torrents/add":
		s.handleAdd(w, r)
	case "torrents/count":
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, len(s.torrents))
	case "torrents/properties":
		s.handleProperties(w, r)
	case "torrents/files":
		s.handleFiles(w, r)
	case "torrents/trackers":
		s.handleTrackers(w, r)
	case "torrents/peers":
		s.handlePeers(w, r)
	case "torrents/pieceStates":
		s.withTorrent(w, r, func(t *torrent) any { return t.Pieces })
	default:
		if action, ok := hashAction(endpoint); ok {
			s.handleHashAction(w, r, action)
			return
		}
		http.NotFound(w, r)
	}
}

// isFiveOnly 5.x 专属端点（4.x 兼容模式下返回 404）
func isFiveOnly(endpoint string) bool {
	switch endpoint {
	case "torrents/start", "torrents/stop", "torrents/setTags":
		return true
	}
	return false
}

// authed 是否通过认证（API Key 或 SID cookie）
func (s *Server) authed(r *http.Request) bool {
	// 未配置任何凭据：免认证放行（对应「本机连接免认证」）
	if s.noAuth {
		return true
	}
	if h := r.Header.Get("Authorization"); h != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(h, prefix) && h[len(prefix):] == s.opts.APIKey {
			return true
		}
		return false
	}
	cookie, err := r.Cookie("SID")
	return err == nil && cookie.Value == s.sid
}

// sameOrigin 与 qBittorrent 4.4+ 一致：Origin / Referer 至少有一个，
// 且任何一个与实际 Host 不一致都判为跨站（只比对不提供也拒绝）
func sameOrigin(r *http.Request) bool {
	seen := false
	for _, h := range []string{"Origin", "Referer"} {
		v := r.Header.Get(h)
		if v == "" {
			continue
		}
		u, err := url.Parse(v)
		if err != nil || u.Host != r.Host {
			return false
		}
		seen = true
	}
	return seen
}

// handleLogin 模拟 /auth/login
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		// 与官方一致：API Key 不能访问 auth 端点
		w.WriteHeader(http.StatusForbidden)
		return
	}
	_ = r.ParseForm()
	if (s.opts.User != "" && r.Form.Get("username") != s.opts.User) ||
		(s.opts.Pass != "" && r.Form.Get("password") != s.opts.Pass) {
		// 与官方一致：失败也是 200 + "Fails."
		_, _ = io.WriteString(w, "Fails.")
		return
	}
	s.mu.Lock()
	s.sid = randToken()
	sid := s.sid
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "SID", Value: sid, Path: "/", HttpOnly: true})
	_, _ = io.WriteString(w, "Ok.")
}

// handleSetPreferences 模拟 POST /app/setPreferences（json=form-encoded JSON）。
// 与真实实例一致：只认自己知道的键，未知键静默忽略（写不进去的东西
// 一旦被静默接受，驱动侧的键名笔误就查不出来了）。
func (s *Server) handleSetPreferences(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	raw := r.Form.Get("json")
	var patch map[string]any
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &patch); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
	}
	s.mu.Lock()
	for k, v := range patch {
		if knownPrefKeys()[k] {
			s.prefs[k] = v
		}
	}
	s.mu.Unlock()
	writeText(w, "")
}

// handleMaindata 模拟 /sync/maindata。
// 简化为每帧全量（full_update），但 server_state 与各 torrent 均为实时值。
func (s *Server) handleMaindata(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rid, _ := strconv.Atoi(r.FormValue("rid"))
	torrents := map[string]any{}
	tags := map[string]struct{}{}
	for hash, t := range s.torrents {
		torrents[hash] = s.torrentInfo(t)
		for _, tag := range t.Tags {
			tags[tag] = struct{}{}
		}
	}
	tagList := make([]string, 0, len(tags))
	for tag := range tags {
		tagList = append(tagList, tag)
	}
	sort.Strings(tagList)
	var peers int64
	for _, t := range s.torrents {
		peers += t.NumSeeds + t.NumLeechs
	}
	writeJSON(w, map[string]any{
		"rid":         rid + 1,
		"full_update": true,
		"torrents":    torrents,
		"categories":  s.cats,
		"tags":        tagList,
		"server_state": map[string]any{
			"dl_info_speed":      s.rateSumLocked(true),
			"up_info_speed":      s.rateSumLocked(false),
			"dl_info_data":       s.sessionDL,
			"up_info_data":       s.sessionUL,
			"alltime_dl":         s.alltimeDL,
			"alltime_ul":         s.alltimeUL,
			"free_space_on_disk": int64(500 * 1024 * 1024 * 1024),
			"total_session_time": s.totalSessionTime,
			"peer_connections":   peers,
			"dht_nodes":          int64(217),
			"total_buffer_size":  int64(4 * 1024 * 1024),
			"connection_status":  "connected",
			// 备用限速的运行态开关：真实实例由工具栏按钮或调度器切换，
			// 这里用调度开关近似（驱动的会话映射同样按调度开关理解）
			"use_alt_speed_limits": prefBool(s.prefs, "scheduler_enabled"),
			"refresh_interval":     int64(2000),
		},
	})
}

// handleTorrentsInfo 模拟 /torrents/info。
// 支持官方过滤参数：hash（单个）、hashes（| 分隔或 all）、filter、limit/offset；
// 输出按 added_on 升序，保证 map 存储下顺序确定。
func (s *Server) handleTorrentsInfo(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	want := map[string]bool{}
	if h := q.Get("hash"); h != "" {
		want[strings.ToLower(h)] = true
	}
	if hs := q.Get("hashes"); hs != "" && !strings.EqualFold(hs, "all") {
		for _, h := range strings.Split(hs, "|") {
			if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
				want[h] = true
			}
		}
	}
	filter := q.Get("filter")

	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]*torrent, 0, len(s.torrents))
	for _, t := range s.torrents {
		if len(want) > 0 && !want[strings.ToLower(t.Hash)] {
			continue
		}
		if !matchFilter(filter, t) {
			continue
		}
		list = append(list, t)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].AddedOn < list[j].AddedOn })
	if lim := q.Get("limit"); lim != "" {
		if n, err := strconv.Atoi(lim); err == nil && n >= 0 && n < len(list) {
			list = list[:n]
		}
	}
	out := make([]any, 0, len(list))
	for _, t := range list {
		out = append(out, s.torrentInfo(t))
	}
	writeJSON(w, out)
}

// matchFilter qBittorrent 的 filter 取值：
// all / downloading / seeding / completed / paused(含 stopped) / active / inactive
func matchFilter(filter string, t *torrent) bool {
	switch filter {
	case "", "all":
		return true
	case "downloading":
		return t.State == "downloading" || t.State == "forcedDL" || t.State == "metaDL" ||
			t.State == "stalledDL" || t.State == "queuedDL" || t.State == "checkingDL"
	case "seeding":
		return t.State == "uploading" || t.State == "forcedUP" || t.State == "stalledUP" ||
			t.State == "queuedUP" || t.State == "checkingUP"
	case "completed":
		return t.Progress >= 1
	case "paused":
		return isStoppedStateName(t.State)
	case "active":
		return t.DLSpeed > 0 || t.ULSpeed > 0
	case "inactive":
		return t.DLSpeed == 0 && t.ULSpeed == 0
	}
	return true
}

// torrentInfo 组装 torrents/info 单条记录（WebAPI 2.15 字段名）。需持有 s.mu。
func (s *Server) torrentInfo(t *torrent) map[string]any {
	amountLeft := t.Size - int64(float64(t.Size)*t.Progress)
	if amountLeft < 0 {
		amountLeft = 0
	}
	return map[string]any{
		"hash":                        t.Hash,
		"infohash_v1":                 t.InfohashV1,
		"infohash_v2":                 "",
		"name":                        t.Name,
		"state":                       t.State,
		"save_path":                   t.SavePath,
		"download_path":               t.SavePath,
		"content_path":                t.ContentPath,
		"size":                        t.Size,
		"total_size":                  t.Size,
		"progress":                    t.Progress,
		"completed":                   int64(float64(t.Size) * t.Progress),
		"amount_left":                 amountLeft,
		"dlspeed":                     t.DLSpeed,
		"upspeed":                     t.ULSpeed,
		"eta":                         t.ETA,
		"num_seeds":                   t.NumSeeds,
		"num_complete":                t.NumComplete,
		"num_leechs":                  t.NumLeechs,
		"num_incomplete":              t.NumIncomplete,
		"ratio":                       t.Ratio,
		"category":                    t.Category,
		"tags":                        strings.Join(t.Tags, ", "),
		"downloaded":                  t.Downloaded,
		"uploaded":                    t.Uploaded,
		"downloaded_session":          t.DownloadedSession,
		"uploaded_session":            t.UploadedSession,
		"added_on":                    t.AddedOn,
		"completion_on":               t.CompletionOn,
		"last_activity":               t.LastActivity,
		"time_elapsed":                time.Now().Unix() - t.AddedOn,
		"time_active":                 t.TimeActive,
		"seeding_time":                t.SeedingTime,
		"priority":                    t.Priority,
		"dl_limit":                    t.DLLimit,
		"up_limit":                    t.ULLimit,
		"ratio_limit":                 t.RatioLimit,
		"max_ratio":                   t.RatioLimit,
		"seeding_time_limit":          t.SeedingTimeLimit,
		"max_seeding_time":            t.SeedingTimeLimit,
		"inactive_seeding_time_limit": t.InactiveSeedingTimeLimit,
		"max_inactive_seeding_time":   t.InactiveSeedingTimeLimit,
		"seq_dl":                      t.SeqDL,
		"force_start":                 t.ForceStart,
		"private":                     t.Private,
		"auto_tmm":                    t.AutoTMM,
		"has_metadata":                t.HasMetadata,
		"availability":                t.Availability,
		"reannounce":                  t.Reannounce,
		"comment":                     t.Comment,
		"created_by":                  t.CreatedBy,
		"creation_date":               t.CreationDate,
		"magnet_uri":                  t.MagnetURI,
		"trackers_count":              int64(len(t.Trackers)),
		"tracker":                     primaryTrackerURL(t),
	}
}

// primaryTrackerInfo 取首个非 DHT tracker 的 URL（qBittorrent 的 tracker 字段语义）
func primaryTrackerURL(t *torrent) string {
	if len(t.Trackers) == 0 {
		return ""
	}
	return t.Trackers[0].URL
}

// handleProperties 模拟 /torrents/properties。需持有 s.mu（经 withTorrent）。
func (s *Server) handleProperties(w http.ResponseWriter, r *http.Request) {
	s.withTorrent(w, r, func(t *torrent) any {
		connected := t.NumSeeds + t.NumLeechs
		return map[string]any{
			"save_path":            t.SavePath,
			"download_path":        t.SavePath,
			"content_path":         t.ContentPath,
			"name":                 t.Name,
			"comment":              t.Comment,
			"total_size":           t.Size,
			"total_selected":       t.Size,
			"completed":            int64(float64(t.Size) * t.Progress),
			"progress":             t.Progress,
			"dlspeed":              t.DLSpeed,
			"up_speed":             t.ULSpeed,
			"shared_ratio":         t.Ratio,
			"share_ratio":          t.Ratio,
			"downloaded":           t.Downloaded,
			"uploaded":             t.Uploaded,
			"total_downloaded":     t.Downloaded,
			"total_uploaded":       t.Uploaded,
			"total_wasted":         int64(0),
			"reannounce":           t.Reannounce,
			"addition_date":        t.AddedOn,
			"completion_date":      t.CompletionOn,
			"creation_date":        t.CreationDate,
			"created_by":           t.CreatedBy,
			"qb_product_name":      "qBittorrent",
			"qb_version":           strings.TrimPrefix(s.opts.Version, "v"),
			"last_seen":            t.LastActivity,
			"nb_connections":       connected,
			"nb_connections_limit": prefInt64(s.prefs, "max_connec"),
			"seeds":                t.NumSeeds,
			"seeds_total":          maxInt64(t.NumComplete, 0),
			"peers":                t.NumLeechs,
			"peers_total":          maxInt64(t.NumIncomplete, 0),
			"time_elapsed":         time.Now().Unix() - t.AddedOn,
			"seeding_time":         t.SeedingTime,
			"active_time":          t.TimeActive,
			"finished_time":        t.TimeActive - t.SeedingTime,
			"dl_limit":             t.DLLimit,
			"up_limit":             t.ULLimit,
			"piece_size":           pieceSize(t),
			"pieces_have":          int64(t.pieceDone),
			"pieces_num":           int64(len(t.Pieces)),
			"is_private":           t.Private,
			"auto_tmm":             t.AutoTMM,
			"availability":         t.Availability,
			"saving_mode":          int64(0),
		}
	})
}

// handleFiles 模拟 /torrents/files
func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	s.withTorrent(w, r, func(t *torrent) any {
		out := make([]map[string]any, 0, len(t.Files))
		for _, f := range t.Files {
			out = append(out, map[string]any{
				"index":        f.Index,
				"name":         f.Name,
				"size":         f.Size,
				"progress":     f.Progress,
				"priority":     f.Priority,
				"piece_range":  []int64{f.PieceStart, f.PieceEnd},
				"availability": f.Availability,
				"is_seed":      t.Progress >= 1,
			})
		}
		return out
	})
}

// handleTrackers 模拟 /torrents/trackers
func (s *Server) handleTrackers(w http.ResponseWriter, r *http.Request) {
	s.withTorrent(w, r, func(t *torrent) any {
		out := make([]map[string]any, 0, len(t.Trackers))
		for _, tr := range t.Trackers {
			out = append(out, map[string]any{
				"url":            tr.URL,
				"status":         tr.Status,
				"tier":           tr.Tier,
				"num_peers":      tr.NumPeers,
				"num_seeds":      tr.NumSeeds,
				"num_leeches":    tr.NumLeechers,
				"num_downloaded": tr.NumDownloaded,
				"msg":            tr.Msg,
			})
		}
		return out
	})
}

// handlePeers 模拟 /torrents/peers：官方响应是 {"peers": {"ip:port": {...}}}，
// 直接返回裸 map 会让驱动的 json 解析拿不到任何对端。
func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
	s.withTorrent(w, r, func(t *torrent) any {
		peers := make(map[string]any, len(t.Peers))
		for k, p := range t.Peers {
			peers[k] = p
		}
		return map[string]any{"peers": peers}
	})
}

// withTorrent 按 hash 参数取出种子并把返回值写为 JSON
func (s *Server) withTorrent(w http.ResponseWriter, r *http.Request, fn func(*torrent) any) {
	hash := r.URL.Query().Get("hash")
	s.mu.Lock()
	t, ok := s.torrents[strings.ToLower(hash)]
	if !ok {
		s.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	defer s.mu.Unlock()
	writeJSON(w, fn(t))
}

// hashAction 全部以 hashes 为入参的动作端点
func hashAction(endpoint string) (string, bool) {
	if !strings.HasPrefix(endpoint, "torrents/") {
		return "", false
	}
	name := strings.TrimPrefix(endpoint, "torrents/")
	switch name {
	case "start", "stop", "resume", "pause", "setForceStart", "recheck", "reannounce",
		"delete", "addTags", "removeTags", "setTags", "setCategory", "createCategory",
		"setDownloadLimit", "setUploadLimit", "setShareLimits", "filePrio",
		"topPrio", "bottomPrio", "increasePrio", "decreasePrio",
		"renameFile", "renameFolder", "setLocation", "setName",
		"addTrackers", "removeTrackers", "replaceTracker", "editTracker",
		"toggleSequentialDownload":
		return name, true
	}
	return "", false
}

// targets 解析 hashes 参数（| 分隔；"all" = 全部），返回命中的种子
func (s *Server) targets(hashParam string) []*torrent {
	if strings.EqualFold(strings.TrimSpace(hashParam), "all") || hashParam == "" {
		all := make([]*torrent, 0, len(s.torrents))
		for _, t := range s.torrents {
			all = append(all, t)
		}
		return all
	}
	var out []*torrent
	for _, h := range splitPipes(hashParam) {
		if t, ok := s.torrents[h]; ok {
			out = append(out, t)
		}
	}
	return out
}

// handleHashAction 处理全部 hashes 动作。需持有 s.mu（进入后统一加锁）。
func (s *Server) handleHashAction(w http.ResponseWriter, r *http.Request, action string) {
	_ = r.ParseForm()
	hashParam := r.Form.Get("hashes")
	if hashParam == "" {
		hashParam = r.Form.Get("hash")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// createCategory 不针对种子；其余动作按 targets 解析（含 "all"）
	if action != "createCategory" && hashParam == "" &&
		action != "delete" && action != "setCategory" {
		http.Error(w, "missing hashes", http.StatusBadRequest)
		return
	}
	list := s.targets(hashParam)
	if action != "createCategory" && len(list) == 0 {
		http.Error(w, "torrents not found", http.StatusNotFound)
		return
	}

	switch action {
	case "start", "resume":
		for _, t := range list {
			s.startLocked(t)
		}
	case "stop", "pause":
		for _, t := range list {
			s.stopLocked(t)
		}
	case "setForceStart":
		enable := !strings.EqualFold(r.Form.Get("value"), "false")
		for _, t := range list {
			t.ForceStart = enable
			switch {
			case !enable && (t.State == "forcedDL" || t.State == "forcedUP"):
				t.State = s.activeStateFor(t)
			case enable && t.Progress >= 1:
				t.State = "forcedUP"
			case enable:
				t.State = "forcedDL"
			}
		}
	case "recheck":
		for _, t := range list {
			if t.State == "error" {
				t.State = s.activeStateFor(t)
			}
			t.prevState = t.State
			t.trueProgress = t.Progress
			if t.Progress >= 1 {
				t.State = "checkingUP"
			} else {
				t.State = "checkingDL"
			}
			t.checkProgress = 0
			// 真实 qBittorrent 校验期间把 progress 报成校验进度
			t.Progress = 0
			t.DLSpeed, t.ULSpeed = 0, 0
		}
	case "reannounce":
		now := time.Now().Unix()
		for _, t := range list {
			for i := range t.Trackers {
				tr := &t.Trackers[i]
				if tr.Status == 5 {
					continue // 报错 tracker 重宣告仍失败
				}
				tr.NumPeers = 10 + int64(s.rng.Intn(40))
				tr.Msg = "Working"
			}
			t.Reannounce = 900
		}
		_ = now
	case "delete":
		// 官方参数名 deleteFiles（旧）/ delete_files（新别名），两种都收
		deleteFiles := strings.EqualFold(r.Form.Get("deleteFiles"), "true") ||
			r.Form.Get("delete_files") == "true"
		for _, t := range list {
			delete(s.torrents, t.Hash)
		}
		_ = deleteFiles
	case "addTags", "removeTags", "setTags":
		tags := splitList(r.Form.Get("tags"))
		for _, t := range list {
			switch action {
			case "setTags":
				t.Tags = append([]string{}, tags...)
			case "addTags":
				t.Tags = mergeTags(t.Tags, tags)
			case "removeTags":
				t.Tags = dropTags(t.Tags, tags)
			}
		}
	case "setCategory":
		cat := r.Form.Get("category")
		for _, t := range list {
			t.Category = cat
			if cat != "" {
				if _, ok := s.cats[cat]; !ok {
					s.cats[cat] = ""
				}
			}
		}
	case "createCategory":
		name := r.Form.Get("category")
		if name != "" {
			if sp := r.Form.Get("savePath"); sp != "" {
				s.cats[name] = sp
			} else if _, ok := s.cats[name]; !ok {
				s.cats[name] = ""
			}
		}
	case "setDownloadLimit":
		limit := parseLimit(r.Form.Get("limit"))
		for _, t := range list {
			t.DLLimit = limit
		}
	case "setUploadLimit":
		limit := parseLimit(r.Form.Get("limit"))
		for _, t := range list {
			t.ULLimit = limit
		}
	case "setShareLimits":
		ratio := parseLimitF(r.Form.Get("ratioLimit"), -2)
		seeding := parseLimit(r.Form.Get("seedingTimeLimit"))
		if seeding == 0 {
			seeding = -2
		}
		inactive := parseLimit(r.Form.Get("inactiveSeedingTimeLimit"))
		if inactive == 0 {
			inactive = -2
		}
		for _, t := range list {
			t.RatioLimit = ratio
			t.SeedingTimeLimit = seeding
			t.InactiveSeedingTimeLimit = inactive
		}
	case "filePrio":
		prio, _ := strconv.ParseInt(r.Form.Get("priority"), 10, 64)
		// 官方 id 以 | 分隔（驱动按规范拼接），兼容逗号
		ids := map[string]struct{}{}
		for _, part := range strings.FieldsFunc(r.Form.Get("id"), func(c rune) bool { return c == '|' || c == ',' }) {
			ids[strings.TrimSpace(part)] = struct{}{}
		}
		for _, t := range list {
			for i := range t.Files {
				if _, ok := ids[strconv.FormatInt(t.Files[i].Index, 10)]; ok {
					t.Files[i].Priority = prio
				}
			}
		}
	case "topPrio", "bottomPrio", "increasePrio", "decreasePrio":
		s.queueMove(list, strings.TrimSuffix(action, "Prio"))
	case "renameFile":
		t := list[0]
		oldPath, newPath := r.Form.Get("oldPath"), r.Form.Get("newPath")
		found := false
		for i := range t.Files {
			if t.Files[i].Name == oldPath {
				t.Files[i].Name = newPath
				found = true
			}
		}
		if !found {
			// 官方行为：文件不存在时失败，驱动据此回退 renameFolder
			http.NotFound(w, r)
			return
		}
		s.refreshContentPath(t)
	case "renameFolder":
		t := list[0]
		oldPath, newPath := r.Form.Get("oldPath"), r.Form.Get("newPath")
		renamed := false
		for i := range t.Files {
			if strings.HasPrefix(t.Files[i].Name, oldPath+"/") {
				t.Files[i].Name = newPath + t.Files[i].Name[len(oldPath):]
				renamed = true
			}
		}
		// 与官方一致：目录名对不上文件列表就失败（种子显示名取自元数据，不跟随改名）
		if !renamed {
			http.NotFound(w, r)
			return
		}
		s.refreshContentPath(t)
	case "setName":
		t := list[0]
		if newName := r.Form.Get("name"); newName != "" {
			t.Name = newName
		}
	case "setLocation":
		loc := r.Form.Get("location")
		for _, t := range list {
			if loc != "" {
				t.SavePath = strings.TrimSuffix(loc, "/")
				s.refreshContentPath(t)
			}
		}
	case "addTrackers":
		t := list[0]
		// 官方：urls 按行分隔（驱动 replaceTrackerList 用 \n 拼接）
		for _, u := range splitLines(r.Form.Get("urls")) {
			if containsURL(t.Trackers, u) {
				continue
			}
			t.Trackers = append(t.Trackers, tracker{
				URL: u, Status: 2, Tier: 0, Msg: "Working",
				NumPeers: 10 + int64(s.rng.Intn(40)),
			})
		}
	case "removeTrackers":
		t := list[0]
		// 官方：urls 以 | 分隔
		rm := map[string]struct{}{}
		for _, u := range splitPipes(r.Form.Get("urls")) {
			rm[u] = struct{}{}
		}
		kept := t.Trackers[:0]
		for _, tr := range t.Trackers {
			if _, ok := rm[tr.URL]; !ok {
				kept = append(kept, tr)
			}
		}
		t.Trackers = kept
	case "replaceTracker", "editTracker":
		t := list[0]
		oldURL, newURL := r.Form.Get("origTrackerUrl"), r.Form.Get("newTrackerUrl")
		for i := range t.Trackers {
			if t.Trackers[i].URL == oldURL {
				t.Trackers[i].URL = newURL
			}
		}
	case "toggleSequentialDownload":
		for _, t := range list {
			t.SeqDL = !t.SeqDL
		}
	}
	writeText(w, "")
}

// startLocked 恢复种子：完成态转做种、未完成转下载（对齐真实 qBittorrent）
func (s *Server) startLocked(t *torrent) {
	if t.State == "error" {
		t.State = s.activeStateFor(t)
		return
	}
	if isStoppedStateName(t.State) {
		t.State = s.activeStateFor(t)
	}
}

// stopLocked 停止种子：按完成度选 stoppedUP / stoppedDL（4.x 为 paused*）
func (s *Server) stopLocked(t *torrent) {
	if t.Progress >= 1 {
		if s.opts.Compat == "4.x" {
			t.State = "pausedUP"
		} else {
			t.State = "stoppedUP"
		}
	} else {
		if s.opts.Compat == "4.x" {
			t.State = "pausedDL"
		} else {
			t.State = "stoppedDL"
		}
	}
	t.DLSpeed, t.ULSpeed = 0, 0
	t.ETA = etaUnknown
}

// activeStateFor 按完成度给出「运行中」状态名
func (s *Server) activeStateFor(t *torrent) string {
	if t.Progress >= 1 {
		return "uploading"
	}
	return "downloading"
}

// queueMove 队列重排：top/bottom 归边，increase/decrease 与相邻位互换，
// 之后按序重发 1..n 的 priority（对齐 trmock QueueMove）。需持有 s.mu。
func (s *Server) queueMove(list []*torrent, verb string) {
	targets := make(map[*torrent]struct{}, len(list))
	for _, t := range list {
		targets[t] = struct{}{}
	}
	ordered := make([]*torrent, 0, len(s.torrents))
	for _, t := range s.torrents {
		ordered = append(ordered, t)
	}
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Priority < ordered[j].Priority })

	switch verb {
	case "top", "bottom":
		var rest, picked []*torrent
		for _, t := range ordered {
			if _, ok := targets[t]; ok {
				picked = append(picked, t)
			} else {
				rest = append(rest, t)
			}
		}
		if verb == "top" {
			ordered = append(picked, rest...)
		} else {
			ordered = append(rest, picked...)
		}
	case "increase": // 上移一位
		for i := 1; i < len(ordered); i++ {
			if _, ok := targets[ordered[i]]; ok {
				if _, moved := targets[ordered[i-1]]; !moved {
					ordered[i], ordered[i-1] = ordered[i-1], ordered[i]
				}
			}
		}
	case "decrease": // 下移一位
		for i := len(ordered) - 2; i >= 0; i-- {
			if _, ok := targets[ordered[i]]; ok {
				if _, moved := targets[ordered[i+1]]; !moved {
					ordered[i], ordered[i+1] = ordered[i+1], ordered[i]
				}
			}
		}
	default:
		return
	}
	for i, t := range ordered {
		t.Priority = int64(i + 1)
	}
}

// refreshContentPath 依据当前文件列表重算 content_path。
// 文件路径是相对 save_path 的，改名 / 迁移目录后必须跟着重算，
// 否则面板的「位置」与文件树会对不上。
func (s *Server) refreshContentPath(t *torrent) {
	if len(t.Files) == 0 {
		return
	}
	first := t.Files[0].Name
	if i := strings.Index(first, "/"); i > 0 {
		// 多文件：content_path 指向顶层目录
		t.ContentPath = t.SavePath + "/" + first[:i]
		return
	}
	t.ContentPath = t.SavePath + "/" + first
}

// handleAdd 模拟 /torrents/add（multipart 或 form）
func (s *Server) handleAdd(w http.ResponseWriter, r *http.Request) {
	form, files, err := parseBody(r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	paused := form.Get("paused") == "true" || form.Get("stopped") == "true"
	savePath := form.Get("savepath")
	tags := splitList(form.Get("tags"))
	category := form.Get("category")

	added := []string{}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !paused && prefBool(s.prefs, "start_paused_enabled") {
		paused = true
	}
	if savePath == "" && category != "" {
		if cp, ok := s.cats[category]; ok && cp != "" {
			savePath = cp
		}
	}

	// 磁力 / URL
	for _, link := range splitLines(form.Get("urls")) {
		for _, one := range strings.Split(link, "|") {
			one = strings.TrimSpace(one)
			if one == "" {
				continue
			}
			hash := magnetHash(one)
			if hash == "" {
				hash = randToken() // 非磁力 URL：mock 造一个
			}
			if _, exists := s.torrents[hash]; exists {
				continue
			}
			name := "magnet-" + hash[:8]
			if dn := magnetName(one); dn != "" {
				name = dn
			}
			if strings.HasPrefix(one, "magnet:") {
				// 磁力先停留在 metaDL（拿不到元数据前没有文件信息），
				// 由 Step 模拟几秒后元数据到达转入下载
				s.torrents[hash] = s.newMagnetTorrent(hash, name, paused, savePath, tags, category)
			} else {
				s.torrents[hash] = s.newAddedTorrent(hash, name, paused, savePath, tags, category)
			}
			added = append(added, hash)
		}
	}
	// .torrent 文件（mock 不解析 bencode，用文件内容哈希定位）
	for name, data := range files {
		hash := contentHash(data)
		if _, exists := s.torrents[hash]; exists {
			continue
		}
		if name == "" {
			name = "torrent-" + hash[:8]
		}
		s.torrents[hash] = s.newAddedTorrent(hash, strings.TrimSuffix(name, ".torrent"), paused, savePath, tags, category)
		added = append(added, hash)
	}

	// 与 5.2 一致：Accept: application/json 时返回结构化结果；
	// 4.x 不认识该头，永远回纯文本，用于验证驱动的 hash 回退路径
	if s.opts.Compat != "4.x" && strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"added_torrent_ids": added,
			"success_count":     len(added),
			"pending_count":     0,
			"failure_count":     0,
		}
		writeJSON(w, resp)
		return
	}
	if len(added) == 0 {
		_, _ = io.WriteString(w, "Fails.")
		return
	}
	_, _ = io.WriteString(w, "Ok.")
}

// parseBody 解析请求体：multipart（.torrent 上传）或普通表单
func parseBody(r *http.Request) (*url.Values, map[string][]byte, error) {
	ct := r.Header.Get("Content-Type")
	mediaType, params, _ := mime.ParseMediaType(ct)
	out := &url.Values{}
	files := map[string][]byte{}
	switch mediaType {
	case "multipart/form-data":
		mr := multipart.NewReader(r.Body, params["boundary"])
		frm, err := mr.ReadForm(32 << 20)
		if err != nil {
			return nil, nil, err
		}
		for k, vs := range frm.Value {
			for _, v := range vs {
				out.Add(k, v)
			}
		}
		for _, fh := range frm.File["torrents"] {
			f, err := fh.Open()
			if err != nil {
				continue
			}
			data, err := io.ReadAll(f)
			_ = f.Close()
			if err != nil {
				continue
			}
			files[fh.Filename] = data
		}
	default:
		if err := r.ParseForm(); err != nil {
			return nil, nil, err
		}
		*out = r.PostForm
	}
	return out, files, nil
}

// ---- 小工具 ----

func writeText(w http.ResponseWriter, s string) {
	w.Header().Set("Content-Type", "text/plain")
	_, _ = io.WriteString(w, s)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}

// splitPipes 竖线分隔参数（hashes / removeTrackers urls / filePrio id）
func splitPipes(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, "|") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// splitLines 换行分隔参数（addTrackers urls / add 的多条 links）
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, "\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// splitList 逗号分隔参数（tags 等）
func splitList(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func mergeTags(cur, add []string) []string {
	seen := map[string]bool{}
	for _, t := range cur {
		seen[t] = true
	}
	for _, t := range add {
		if !seen[t] {
			cur = append(cur, t)
			seen[t] = true
		}
	}
	return cur
}

func dropTags(cur, rm []string) []string {
	rmSet := map[string]bool{}
	for _, t := range rm {
		rmSet[t] = true
	}
	out := cur[:0]
	for _, t := range cur {
		if !rmSet[t] {
			out = append(out, t)
		}
	}
	return out
}

func containsURL(list []tracker, u string) bool {
	for _, tr := range list {
		if tr.URL == u {
			return true
		}
	}
	return false
}

// isStoppedStateName 停止态命名（5.x stopped* / 4.x paused*）
func isStoppedStateName(state string) bool {
	switch state {
	case "stoppedDL", "stoppedUP", "pausedDL", "pausedUP":
		return true
	}
	return false
}

// parseLimit 限速参数：0 表示取消限速（官方语义），-1 全局
func parseLimit(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// parseLimitF 分享率上限参数（-1 不限 / -2 全局），缺省 fallback
func parseLimitF(s string, fallback float64) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fallback
	}
	return f
}

// prefInt64 / prefBool 读取 prefs 中的数值（setPreferences 写入的是 float64）
func prefInt64(prefs map[string]any, key string) int64 {
	switch v := prefs[key].(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	case int:
		return int64(v)
	}
	return 0
}

func prefBool(prefs map[string]any, key string) bool {
	v, _ := prefs[key].(bool)
	return v
}

// pieceSize 派生分块大小：优先建模值，回退按尺寸估算
func pieceSize(t *torrent) int64 {
	if t.Size > 0 && len(t.Pieces) > 0 {
		return t.Size / int64(len(t.Pieces))
	}
	return 262144
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// magnetHash 从磁力链接提取 btih
func magnetHash(link string) string {
	u, err := url.Parse(link)
	if err != nil || !strings.HasPrefix(link, "magnet:") {
		return ""
	}
	for _, xt := range u.Query()["xt"] {
		if h, ok := strings.CutPrefix(xt, "urn:btih:"); ok {
			return strings.ToLower(h)
		}
	}
	return ""
}

func magnetName(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	return u.Query().Get("dn")
}

// sha1Sum 标准 SHA-1 摘要（contentHash 用它模拟 infohash）
func sha1Sum(data []byte) [20]byte { return sha1.Sum(data) }

// contentHash 用 sha1 前 40 位十六进制模拟 infohash（mock 无需真实 bencode 解析）
func contentHash(data []byte) string {
	sum := sha1Sum(data)
	return hex.EncodeToString(sum[:])
}
