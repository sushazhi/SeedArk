// Package qbmock 提供一个内存态的 qBittorrent WebAPI 模拟服务器，
// 对齐 qBittorrent 5.2.3（Web API v2.15.1）：
//   - API Key 认证（qbt_ 前缀，Authorization: Bearer；auth 端点拒绝 Bearer）
//   - cookie 认证（POST /api/v2/auth/login 下发 SID；失败回 "Fails."）
//   - CSRF 校验（POST 必须带与 Host 一致的 Referer/Origin，与 4.4+ 行为一致）
//   - torrents/add 返回 {"added_torrent_ids":[...],"success_count":..}（5.2+ JSON 响应）
//   - torrents/start|stop（5.0+）与 pause/resume/setForceStart（4.x 起）
//   - setTags（5.1+）、toggleSequentialDownload、filePrio、setShareLimits 等
//
// -compat 4.x 模式会隐藏 5.x 专属端点（start/stop/setTags 返回 404），
// 用于验证驱动的旧版本回退逻辑。
package qbmock

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
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

// torrent 模拟的种子
type torrent struct {
	Hash       string
	Name       string
	State      string // downloading / stoppedDL / uploading ...
	Size       int64
	Progress   float64
	DLSpeed    int64
	ULSpeed    int64
	ETA        int64
	NumSeeds   int64
	NumLeechs  int64
	Ratio      float64
	Category   string
	Tags       []string
	SavePath   string
	Downloaded int64
	Uploaded   int64
	AddedOn    int64
	Priority   int64 // 队列位置，0 = 不在队列
	SeqDL      bool
	DLLimit    int64 // bytes/s，-1 = 全局
	ULLimit    int64
	RatioLimit float64 // -2 = 全局
	Files      []file
	Trackers   []tracker
	Pieces     []int
}

type file struct {
	Index    int64  `json:"index"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Progress float64
	Priority int64 `json:"priority"`
}

type tracker struct {
	URL      string `json:"url"`
	Status   int64  `json:"status"`
	Tier     int64  `json:"tier"`
	NumPeers int64  `json:"num_peers"`
	Msg      string `json:"msg"`
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
	torrents map[string]*torrent
	prefs    map[string]any
	cats     map[string]string // 分类名 → 保存路径
	sid      string
	dlRate   int64
	ulRate   int64
	shutdown bool
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
		torrents: map[string]*torrent{},
		cats:     map[string]string{},
		sid:      randToken(),
		prefs: map[string]any{
			"save_path":                              "/downloads",
			"temp_path_enabled":                      false,
			"temp_path":                              "",
			"alt_dl_limit":                           10240,
			"alt_up_limit":                           10240,
			"dl_limit":                               int64(0),
			"up_limit":                               int64(0),
			"max_active_downloads":                   3,
			"max_active_seeds":                       5,
			"max_active_torrents":                    8,
			"max_connec":                             500,
			"max_uploads":                            20,
			"listen_port":                            6881,
			"upnp":                                   true,
			"random_port":                            false,
			"dht":                                    true,
			"pex":                                    true,
			"ltas_lt_tex":                            true,
			"encryption":                             0,
			"start_paused_enabled":                   false,
			"queueing_enabled":                       true,
			"ratio_limit":                            float64(-1),
			"ratio_limit_enabled":                    false,
			"scheduler_enabled":                      false,
			"alt_speed_enabled":                      false,
			"schedule_from_hour":                     8,
			"schedule_to_hour":                       22,
			"schedule_days":                          0,
			"web_ui_clickjacking_protection_enabled": false,
		},
	}
	for i := 0; i < n; i++ {
		s.seedTorrent(i)
	}
	return s
}

// randToken 生成随机 SID
func randToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// seedTorrent 造一个模拟种子
func (s *Server) seedTorrent(i int) {
	hash := fmt.Sprintf("%064x", i+1)
	t := &torrent{
		Hash:       hash,
		Name:       fmt.Sprintf("qbmock-%02d.iso", i+1),
		State:      "downloading",
		Size:       int64(1024*1024*1024) * int64(i+1),
		Progress:   0.5,
		DLSpeed:    int64(1024 * 100),
		ULSpeed:    int64(1024 * 10),
		ETA:        3600,
		NumSeeds:   12,
		NumLeechs:  3,
		Ratio:      0.8,
		SavePath:   "/downloads",
		Downloaded: 512 * 1024 * 1024,
		Uploaded:   400 * 1024 * 1024,
		AddedOn:    time.Now().Unix() - 3600,
		Priority:   int64(i + 1),
		DLLimit:    -1,
		ULLimit:    -1,
		RatioLimit: -2,
		Files: []file{
			{Index: 0, Name: fmt.Sprintf("qbmock-%02d/part1.bin", i+1), Size: 512 * 1024 * 1024, Progress: 0.5, Priority: 1},
			{Index: 1, Name: fmt.Sprintf("qbmock-%02d/part2.bin", i+1), Size: 512 * 1024 * 1024, Progress: 0.5, Priority: 1},
		},
		Trackers: []tracker{
			{URL: "https://tracker.example.com/announce", Status: 2, Tier: 0},
		},
		Pieces: []int{2, 2, 1, 0, 2, 1},
	}
	if i%3 == 0 {
		t.State = "stoppedUP" // 5.x 的停止做种（4.x 为 pausedUP）
		t.Progress = 1
	}
	s.torrents[hash] = t
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
			"dl_info_speed":     s.dlRate,
			"dl_info_data":      int64(12345678),
			"up_info_speed":     s.ulRate,
			"up_info_data":      int64(87654321),
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
		s.withTorrent(w, r, func(t *torrent) any { return t.Files })
	case "torrents/trackers":
		s.withTorrent(w, r, func(t *torrent) any { return t.Trackers })
	case "torrents/peers":
		s.withTorrent(w, r, func(*torrent) any { return map[string]any{} })
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

// sameOrigin POST 请求的 Origin/Referer 必须与 Host 一致（CSRF）
func sameOrigin(r *http.Request) bool {
	host := r.Host
	for _, h := range []string{"Origin", "Referer"} {
		v := r.Header.Get(h)
		if v == "" {
			continue
		}
		u, err := url.Parse(v)
		if err != nil {
			return false
		}
		if u.Host == host {
			return true
		}
	}
	return false
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

// handleSetPreferences 模拟 POST /app/setPreferences（json=form-encoded JSON）
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
		s.prefs[k] = v
	}
	s.mu.Unlock()
	writeText(w, "")
}

// handleMaindata 模拟 /sync/maindata
func (s *Server) handleMaindata(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rid, _ := strconv.Atoi(r.FormValue("rid"))
	torrents := map[string]any{}
	for hash, t := range s.torrents {
		torrents[hash] = s.torrentInfo(t)
	}
	writeJSON(w, map[string]any{
		"rid":         rid + 1,
		"full_update": true,
		"torrents":    torrents,
		"categories":  s.cats,
		"tags":        []string{},
		"server_state": map[string]any{
			"dl_info_data":       int64(1024 * 1024 * 1024 * 100),
			"up_info_data":       int64(1024 * 1024 * 1024 * 50),
			"alltime_dl":         int64(1024 * 1024 * 1024 * 200),
			"alltime_ul":         int64(1024 * 1024 * 1024 * 80),
			"free_space_on_disk": int64(500 * 1024 * 1024 * 1024),
			"connection_status":  "connected",
		},
	})
}

// handleTorrentsInfo 模拟 /torrents/info
func (s *Server) handleTorrentsInfo(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []any{}
	for _, t := range s.torrents {
		if h := q.Get("hash"); h != "" && !strings.EqualFold(h, t.Hash) {
			continue
		}
		if name := q.Get("name"); name != "" && !strings.Contains(t.Name, name) {
			continue
		}
		out = append(out, s.torrentInfo(t))
	}
	writeJSON(w, out)
}

// torrentInfo 组装 torrents/info 单条记录（WebAPI 2.15 字段名）
func (s *Server) torrentInfo(t *torrent) map[string]any {
	return map[string]any{
		"hash":           t.Hash,
		"name":           t.Name,
		"state":          t.State,
		"size":           t.Size,
		"progress":       t.Progress,
		"dlspeed":        t.DLSpeed,
		"upspeed":        t.ULSpeed,
		"eta":            t.ETA,
		"num_seeds":      t.NumSeeds,
		"num_complete":   t.NumSeeds,
		"num_leechs":     t.NumLeechs,
		"num_incomplete": t.NumLeechs,
		"ratio":          t.Ratio,
		"category":       t.Category,
		"tags":           strings.Join(t.Tags, ", "),
		"save_path":      t.SavePath,
		"downloaded":     t.Downloaded,
		"uploaded":       t.Uploaded,
		"added_on":       t.AddedOn,
		"priority":       t.Priority,
		"dl_limit":       t.DLLimit,
		"up_limit":       t.ULLimit,
		"seq_dl":         t.SeqDL,
		"tracker":        firstTrackerHost(t),
	}
}

func firstTrackerHost(t *torrent) string {
	if len(t.Trackers) == 0 {
		return ""
	}
	return t.Trackers[0].URL
}

// handleProperties 模拟 /torrents/properties
func (s *Server) handleProperties(w http.ResponseWriter, r *http.Request) {
	s.withTorrent(w, r, func(t *torrent) any {
		return map[string]any{
			"save_path":        t.SavePath,
			"seeding_time":     int64(7200),
			"time_elapsed":     int64(9000),
			"total_downloaded": t.Downloaded,
			"total_uploaded":   t.Uploaded,
			"share_ratio":      t.Ratio,
			"piece_size":       int64(262144),
			"pieces_have":      int64(len(t.Pieces) / 2),
			"pieces_num":       int64(len(t.Pieces)),
			"created_by":       "qbmock",
			"addition_date":    t.AddedOn,
		}
	})
}

// withTorrent 按 hash 参数取出种子并把返回值写为 JSON
func (s *Server) withTorrent(w http.ResponseWriter, r *http.Request, fn func(*torrent) any) {
	hash := r.URL.Query().Get("hash")
	s.mu.Lock()
	t, ok := s.torrents[strings.ToLower(hash)]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
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
		"renameFile", "renameFolder", "setLocation",
		"addTrackers", "removeTrackers", "replaceTracker", "editTracker",
		"toggleSequentialDownload":
		return name, true
	}
	return "", false
}

// handleHashAction 处理全部 hashes 动作
func (s *Server) handleHashAction(w http.ResponseWriter, r *http.Request, action string) {
	_ = r.ParseForm()
	hashParam := r.Form.Get("hashes")
	if hashParam == "" {
		hashParam = r.Form.Get("hash")
	}
	hashes := splitHashes(hashParam)
	if action != "createCategory" && len(hashes) == 0 {
		http.Error(w, "missing hashes", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch action {
	case "start", "resume":
		for _, h := range hashes {
			if t, ok := s.torrents[h]; ok {
				t.State = "downloading"
			}
		}
	case "stop", "pause":
		for _, h := range hashes {
			if t, ok := s.torrents[h]; ok {
				t.State = "stoppedDL"
				t.DLSpeed = 0
			}
		}
	case "setForceStart":
		for _, h := range hashes {
			if t, ok := s.torrents[h]; ok {
				t.State = "forcedDL"
			}
		}
	case "recheck":
		for _, h := range hashes {
			if t, ok := s.torrents[h]; ok {
				t.State = "checkingDL"
				t.Progress = 0
			}
		}
	case "reannounce":
		// no-op
	case "delete":
		deleteFiles := r.Form.Get("delete_files") == "true"
		for _, h := range hashes {
			delete(s.torrents, h)
		}
		_ = deleteFiles
	case "addTags", "removeTags", "setTags":
		tags := splitList(r.Form.Get("tags"))
		for _, h := range hashes {
			t, ok := s.torrents[h]
			if !ok {
				continue
			}
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
		for _, h := range hashes {
			if t, ok := s.torrents[h]; ok {
				t.Category = cat
			}
		}
	case "createCategory":
		name := r.Form.Get("category")
		if name != "" {
			s.cats[name] = r.Form.Get("savePath")
		}
	case "setDownloadLimit":
		limit, _ := strconv.ParseInt(r.Form.Get("limit"), 10, 64)
		for _, h := range hashes {
			if t, ok := s.torrents[h]; ok {
				t.DLLimit = limit
			}
		}
	case "setUploadLimit":
		limit, _ := strconv.ParseInt(r.Form.Get("limit"), 10, 64)
		for _, h := range hashes {
			if t, ok := s.torrents[h]; ok {
				t.ULLimit = limit
			}
		}
	case "setShareLimits":
		ratio, _ := strconv.ParseFloat(r.Form.Get("ratioLimit"), 64)
		for _, h := range hashes {
			if t, ok := s.torrents[h]; ok {
				t.RatioLimit = ratio
			}
		}
	case "filePrio":
		prio, _ := strconv.ParseInt(r.Form.Get("priority"), 10, 64)
		for _, h := range hashes {
			t, ok := s.torrents[h]
			if !ok {
				continue
			}
			for _, idx := range splitList(r.Form.Get("id")) {
				for i := range t.Files {
					if strconv.FormatInt(t.Files[i].Index, 10) == idx {
						t.Files[i].Priority = prio
					}
				}
			}
		}
	case "topPrio":
		base := int64(0)
		for _, h := range hashes {
			if t, ok := s.torrents[h]; ok {
				base++
				t.Priority = base
			}
		}
	case "bottomPrio":
		for _, h := range hashes {
			if t, ok := s.torrents[h]; ok {
				t.Priority = int64(len(s.torrents)) + 100
			}
		}
	case "increasePrio", "decreasePrio":
		// no-op：队列顺序细节对驱动验证无影响
	case "renameFile":
		t, ok := s.torrents[hashes[0]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		oldPath, newPath := r.Form.Get("oldPath"), r.Form.Get("newPath")
		for i := range t.Files {
			if t.Files[i].Name == oldPath {
				t.Files[i].Name = newPath
			}
		}
	case "renameFolder":
		// no-op：驱动先 renameFile 失败再走 renameFolder，两个端点都要存在
	case "setLocation":
		loc := r.Form.Get("location")
		for _, h := range hashes {
			if t, ok := s.torrents[h]; ok {
				t.SavePath = loc
			}
		}
	case "addTrackers":
		t, ok := s.torrents[hashes[0]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		for _, u := range splitList(r.Form.Get("urls")) {
			t.Trackers = append(t.Trackers, tracker{URL: u, Status: 2})
		}
	case "removeTrackers":
		t, ok := s.torrents[hashes[0]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		rm := map[string]bool{}
		for _, u := range splitList(r.Form.Get("urls")) {
			rm[u] = true
		}
		kept := t.Trackers[:0]
		for _, tr := range t.Trackers {
			if !rm[tr.URL] {
				kept = append(kept, tr)
			}
		}
		t.Trackers = kept
	case "replaceTracker", "editTracker":
		t, ok := s.torrents[hashes[0]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		oldURL, newURL := r.Form.Get("origTrackerUrl"), r.Form.Get("newTrackerUrl")
		for i := range t.Trackers {
			if t.Trackers[i].URL == oldURL {
				t.Trackers[i].URL = newURL
			}
		}
	case "toggleSequentialDownload":
		for _, h := range hashes {
			if t, ok := s.torrents[h]; ok {
				t.SeqDL = !t.SeqDL
			}
		}
	}
	writeText(w, "")
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

	// 磁力 / URL
	for _, link := range splitList(form.Get("urls")) {
		hash := magnetHash(link)
		if hash == "" {
			hash = randToken() // 非磁力 URL：mock 造一个
		}
		if _, exists := s.torrents[hash]; exists {
			continue
		}
		name := "magnet-" + hash[:8]
		if dn := magnetName(link); dn != "" {
			name = dn
		}
		s.torrents[hash] = newMockTorrent(hash, name, paused, savePath, tags, category)
		added = append(added, hash)
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
		s.torrents[hash] = newMockTorrent(hash, name, paused, savePath, tags, category)
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

func newMockTorrent(hash, name string, paused bool, savePath string, tags []string, category string) *torrent {
	t := &torrent{
		Hash: hash, Name: name, Size: 1 << 30, Progress: 0,
		SavePath: "/downloads", DLLimit: -1, ULLimit: -1, RatioLimit: -2,
		Tags: tags, Category: category, Priority: 1,
		Pieces: []int{0, 0, 0},
		Files: []file{
			{Index: 0, Name: name + "/data.bin", Size: 1 << 30, Priority: 1},
		},
		Trackers: []tracker{{URL: "https://tracker.example.com/announce", Status: 2}},
	}
	if savePath != "" {
		t.SavePath = savePath
	}
	if paused {
		t.State = "stoppedDL"
	} else {
		t.State = "downloading"
	}
	return t
}

// parseBody 解析 multipart（torrents 文件）或 urlencoded 表单
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

// splitHashes hashes 参数按 | 分隔并统一小写
func splitHashes(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
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
