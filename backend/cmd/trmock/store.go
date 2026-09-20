package main

import (
	"math/rand"
	"sort"
	"sync"
	"time"
)

// Transmission 状态码（与 rpc-spec 一致）
const (
	stStopped      = 0
	stCheckWait    = 1
	stCheck        = 2
	stDownloadWait = 3
	stDownload     = 4
	stSeedWait     = 5
	stSeed         = 6
	stIsolated     = 7
)

// mockFile / mockFileStat / mockTracker / mockTrackerStat / mockPeer 为详情子结构，
// 字段名在 rpc.go 的 wire 构造里映射为 Transmission RPC 的对应键。
type mockFile struct {
	Name           string
	Length         int64
	BytesCompleted int64
}

type mockFileStat struct {
	BytesCompleted int64
	Wanted         bool
	Priority       int64
}

type mockTracker struct {
	ID       int64
	Announce string
	Scrape   string
	SiteName string
	Tier     int64
}

type mockTrackerStat struct {
	ID                    int64
	Host                  string
	Announce              string
	AnnounceState         int64
	Tier                  int64
	IsBackup              bool
	LastAnnounceResult    string
	LastAnnounceSucceeded bool
	LastAnnounceTimedOut  bool
	LastAnnounceTime      int64
	LastAnnouncePeerCount int64
	NextAnnounceTime      int64
	ScrapeState           int64
	LastScrapeResult      string
	LastScrapeSucceeded   bool
	LastScrapeTime        int64
	NextScrapeTime        int64
	SeederCount           int64
	LeecherCount          int64
	DownloadCount         int64
}

type mockPeer struct {
	Address           string
	ClientName        string
	FlagStr           string
	Progress          float64
	RateToClient      int64
	RateToPeer        int64
	IsDownloadingFrom bool
	IsUploadingTo     bool
	IsEncrypted       bool
	IsIncoming        bool
	IsUTP             bool
	Port              int64
}

// mockTorrent 单个种子的内部状态。尺寸一律字节、日期一律 unix 秒，
// 与 Transmission RPC wire 语义一致（见 rpc.go）。
type mockTorrent struct {
	ID                  int64
	Name                string
	HashString          string
	Creator             string
	TotalSize           int64 // 字节
	PercentDone         float64
	Status              int64
	RateDownload        int64 // B/s
	RateUpload          int64 // B/s
	ETA                 int64 // 秒；完成为 -1，未知为 -2
	UploadedEver        int64
	DownloadedEver      int64
	UploadRatio         float64
	SecondsSeeding      int64
	SecondsDownloading  int64
	Error               int64
	ErrorString         string
	Labels              []string
	QueuePosition       int64
	PeersConnected      int64
	PeersSendingToUs    int64
	PeersGettingFromUs  int64
	DownloadDir         string
	AddedDate           int64
	DoneDate            int64
	ActivityDate        int64
	StartDate           int64
	IsFinished          bool
	IsStalled           bool
	IsPrivate           bool
	MagnetLink          string
	FileCount           int64
	HaveValid           int64
	HaveUnchecked       int64
	Comment             string
	PeerLimit           int64
	Sequential          bool
	SeedIdleLimit       int64 // 分钟
	SeedIdleMode        int64
	SeedRatioLimit      float64
	SeedRatioMode       int64
	BandwidthPriority   int64
	DownloadLimited     bool
	DownloadLimit       int64
	UploadLimited       bool
	UploadLimit         int64
	HonorsSessionLimits bool
	RecheckProgress     float64

	Files        []mockFile
	FileStats    []mockFileStat
	Trackers     []mockTracker
	TrackerStats []mockTrackerStat
	Peers        []mockPeer

	PieceCount int64
	PieceSize  int64 // 字节

	// 模拟参数：目标速度（B/s），tick 时围绕其抖动
	baseDown int64
	baseUp   int64
}

// 派生尺寸字段
func (t *mockTorrent) haveValid() int64     { return int64(float64(t.TotalSize) * t.PercentDone) }
func (t *mockTorrent) leftUntilDone() int64 { return t.TotalSize - t.haveValid() }
func (t *mockTorrent) sizeWhenDone() int64  { return t.TotalSize }

// mockSession 会话配置（仅保留后端会消费的键）
type mockSession struct {
	DownloadDir          string
	SpeedLimitDown       int64
	SpeedLimitDownOn     bool
	SpeedLimitUp         int64
	SpeedLimitUpOn       bool
	AltSpeedDown         int64
	AltSpeedUp           int64
	AltSpeedEnabled      bool
	PeerLimitGlobal      int64
	PeerPort             int64
	PEXEnabled           bool
	DHTEnabled           bool
	LPDEnabled           bool
	UTPEnabled           bool
	Encryption           string
	SeedRatioLimit       float64
	StartAdded           bool
	DownloadQueueSize    int64
	DownloadQueueEnabled bool
	SeedQueueSize        int64
	SeedQueueEnabled     bool
	BlocklistEnabled     bool
	BlocklistSize        int64
	CacheSizeMB          int64
	// Extra 记录 session-set 收到的全部键值（含未在结构体中建模的），
	// session-get 时叠加回显，保证设置改动可往返验证。
	Extra map[string]any
}

type statsDetails struct {
	DownloadedBytes int64
	UploadedBytes   int64
	FilesAdded      int64
	SecondsActive   int64
	SessionCount    int64
}

// Store mock 的全部可变状态；所有读写经 mu 串行化。
type Store struct {
	mu         sync.Mutex
	torrents   []*mockTorrent
	nextID     int64
	session    mockSession
	cumulative statsDetails
	current    statsDetails
	rng        *rand.Rand
	startedAt  time.Time
}

// NewStore 生成初始种子并返回就绪的 Store。
func NewStore(seedCount int) *Store {
	s := &Store{
		nextID:    1,
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())),
		startedAt: time.Now(),
	}
	s.session = defaultSession()
	s.torrents = seedTorrents(s, seedCount)
	s.current.SessionCount = 1
	s.cumulative.SessionCount = 1
	return s
}

func defaultSession() mockSession {
	return mockSession{
		DownloadDir:          "/downloads",
		SpeedLimitDown:       1000,
		SpeedLimitUp:         500,
		AltSpeedDown:         200,
		AltSpeedUp:           100,
		PeerLimitGlobal:      200,
		PeerPort:             51413,
		PEXEnabled:           true,
		DHTEnabled:           true,
		LPDEnabled:           true,
		UTPEnabled:           true,
		Encryption:           "preferred",
		SeedRatioLimit:       2.0,
		StartAdded:           true,
		DownloadQueueSize:    5,
		DownloadQueueEnabled: true,
		SeedQueueSize:        10,
		SeedQueueEnabled:     false,
		BlocklistSize:        12345,
		CacheSizeMB:          64,
		Extra:                map[string]any{},
	}
}

// Step 推进一次模拟（由 ticker 周期调用）。
func (s *Store) Step(dt time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sec := int64(dt / time.Second)
	if sec < 1 {
		sec = 1
	}
	now := time.Now().Unix()
	var downBytes, upBytes int64
	for _, t := range s.torrents {
		switch t.Status {
		case stDownload:
			rate := jitter(s.rng, t.baseDown)
			if t.DownloadLimited && t.DownloadLimit > 0 && rate > t.DownloadLimit*1024 {
				rate = t.DownloadLimit * 1024
			}
			t.RateDownload = rate
			t.RateUpload = jitter(s.rng, t.baseUp/4)
			added := rate * sec
			t.DownloadedEver += added
			t.HaveValid += added
			t.SecondsDownloading += sec
			t.ActivityDate = now
			if t.TotalSize > 0 {
				t.PercentDone = clamp01(float64(t.HaveValid) / float64(t.TotalSize))
			}
			left := t.leftUntilDone()
			if rate > 0 {
				t.ETA = left / rate
			} else {
				t.ETA = -2
			}
			if t.PercentDone >= 1 {
				t.PercentDone = 1
				t.HaveValid = t.TotalSize
				t.Status = stSeed
				t.DoneDate = now
				t.IsFinished = true
				t.ETA = -1
				t.RateDownload = 0
			}
			downBytes += added
			upBytes += t.RateUpload * sec
		case stSeed:
			rate := jitter(s.rng, t.baseUp)
			t.RateUpload = rate
			t.RateDownload = 0
			t.UploadedEver += rate * sec
			t.SecondsSeeding += sec
			t.ActivityDate = now
			if t.TotalSize > 0 {
				t.UploadRatio = float64(t.UploadedEver) / float64(t.TotalSize)
			}
			t.ETA = -1
			upBytes += rate * sec
		case stCheck:
			t.RecheckProgress += float64(sec) / 10.0
			t.ActivityDate = now
			if t.RecheckProgress >= 1 {
				t.RecheckProgress = 0
				if t.PercentDone >= 1 {
					t.Status = stSeed
				} else {
					t.Status = stDownload
				}
			}
		default:
			t.RateDownload = 0
			t.RateUpload = 0
		}
	}
	// 会话统计
	s.current.DownloadedBytes += downBytes
	s.current.UploadedBytes += upBytes
	s.current.SecondsActive += sec
	s.cumulative.DownloadedBytes += downBytes
	s.cumulative.UploadedBytes += upBytes
	s.cumulative.SecondsActive += sec
}

// jitter 在基准速度上下 ±30% 抖动，返回非负值。
func jitter(r *rand.Rand, base int64) int64 {
	if base <= 0 {
		return 0
	}
	f := 0.7 + r.Float64()*0.6
	return int64(float64(base) * f)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// ---- 变更操作（真实改状态）----

// findByIDs 返回命中的种子；ids 为空表示全部。
func (s *Store) findByIDs(ids []int64) []*mockTorrent {
	if len(ids) == 0 {
		return s.torrents
	}
	set := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	var out []*mockTorrent
	for _, t := range s.torrents {
		if _, ok := set[t.ID]; ok {
			out = append(out, t)
		}
	}
	return out
}

func (s *Store) Start(ids []int64, _ bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.findByIDs(ids) {
		if t.Error > 0 {
			t.Error = 0
			t.ErrorString = ""
		}
		if t.PercentDone >= 1 {
			t.Status = stSeed
		} else {
			t.Status = stDownload
			if t.ETA < 0 {
				t.ETA = 0
			}
		}
		t.StartDate = time.Now().Unix()
	}
}

func (s *Store) Stop(ids []int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.findByIDs(ids) {
		t.Status = stStopped
		t.RateDownload = 0
		t.RateUpload = 0
		t.ETA = -1
	}
}

func (s *Store) Verify(ids []int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.findByIDs(ids) {
		t.Status = stCheck
		t.RecheckProgress = 0
	}
}

func (s *Store) Reannounce(ids []int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()
	for _, t := range s.findByIDs(ids) {
		for i := range t.TrackerStats {
			t.TrackerStats[i].LastAnnounceTime = now
			t.TrackerStats[i].LastAnnounceSucceeded = true
			t.TrackerStats[i].LastAnnounceResult = "Success"
		}
	}
}

func (s *Store) Remove(ids []int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	kept := s.torrents[:0]
	for _, t := range s.torrents {
		if _, ok := set[t.ID]; !ok {
			kept = append(kept, t)
		}
	}
	s.torrents = kept
}

// Add 新建一个种子并返回它。
func (s *Store) Add(name, downloadDir string, paused bool, labels []string) *mockTorrent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addLocked(name, downloadDir, paused, labels)
}

func (s *Store) addLocked(name, downloadDir string, paused bool, labels []string) *mockTorrent {
	now := time.Now().Unix()
	id := s.nextID
	s.nextID++
	size := int64(500*1024*1024) + s.rng.Int63n(4*1024*1024*1024)
	t := &mockTorrent{
		ID:            id,
		Name:          name,
		HashString:    fakeHash(s.rng),
		TotalSize:     size,
		Status:        stDownload,
		DownloadDir:   downloadDir,
		AddedDate:     now,
		ActivityDate:  now,
		StartDate:     now,
		Labels:        labels,
		QueuePosition: int64(len(s.torrents)),
		PeerLimit:     60,
		baseDown:      2*1024*1024 + s.rng.Int63n(8*1024*1024),
		baseUp:        256*1024 + s.rng.Int63n(1024*1024),
		PieceCount:    size/(16*1024*1024) + 1,
		PieceSize:     16 * 1024 * 1024,
	}
	if downloadDir == "" {
		t.DownloadDir = s.session.DownloadDir
	}
	if paused {
		t.Status = stStopped
	}
	t.Files = []mockFile{{Name: t.Name, Length: size, BytesCompleted: 0}}
	t.FileStats = []mockFileStat{{BytesCompleted: 0, Wanted: true, Priority: 0}}
	t.FileCount = int64(len(t.Files))
	s.torrents = append(s.torrents, t)
	s.current.FilesAdded++
	s.cumulative.FilesAdded++
	return t
}

// AddWire 新增种子并在锁内返回用于 torrent-added 的精简 wire。
func (s *Store) AddWire(name, downloadDir string, paused bool, labels []string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.addLocked(name, downloadDir, paused, labels)
	return torrentWire(t, []string{"id", "name", "hashString", "totalSize", "status"})
}

// TorrentsWire 按 ids 过滤（空为全部）并在锁内构造 torrent-get 的 wire 列表。
func (s *Store) TorrentsWire(ids []int64, fields []string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.findByIDs(ids)
	out := make([]map[string]any, 0, len(list))
	for _, t := range list {
		out = append(out, torrentWire(t, fields))
	}
	return out
}

// SessionWire 在锁内复制 session（含 Extra 浅拷贝）后构造 session-get 的 wire。
func (s *Store) SessionWire(sessionID string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	extra := make(map[string]any, len(s.session.Extra))
	for k, v := range s.session.Extra {
		extra[k] = v
	}
	return sessionWire(&s.session, extra, sessionID)
}

// Set 应用 torrent-set 的可识别字段。
func (s *Store) Set(ids []int64, fields map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.findByIDs(ids) {
		if v, ok := fields["labels"].([]any); ok {
			t.Labels = toStringSlice(v)
		}
		if v, ok := numField(fields, "queuePosition"); ok {
			t.QueuePosition = v
		}
		if v, ok := strField(fields, "location"); ok {
			t.DownloadDir = v
		}
		if v, ok := numField(fields, "bandwidthPriority"); ok {
			t.BandwidthPriority = v
		}
		if v, ok := numField(fields, "downloadLimit"); ok {
			t.DownloadLimit = v
		}
		if v, ok := boolField(fields, "downloadLimited"); ok {
			t.DownloadLimited = v
		}
		if v, ok := numField(fields, "uploadLimit"); ok {
			t.UploadLimit = v
		}
		if v, ok := boolField(fields, "uploadLimited"); ok {
			t.UploadLimited = v
		}
		if v, ok := boolField(fields, "honorsSessionLimits"); ok {
			t.HonorsSessionLimits = v
		}
		if v, ok := boolField(fields, "sequentialDownload"); ok {
			t.Sequential = v
		}
		if v, ok := numField(fields, "peer-limit"); ok {
			t.PeerLimit = v
		}
		if v, ok := floatField(fields, "seedRatioLimit"); ok {
			t.SeedRatioLimit = v
		}
		if v, ok := numField(fields, "seedRatioMode"); ok {
			t.SeedRatioMode = v
		}
		if v, ok := strField(fields, "trackerList"); ok {
			t.Trackers = trackersFromList(v)
			t.TrackerStats = trackerStatsFromTrackers(t.Trackers)
		}
	}
}

func (s *Store) SetLocation(ids []int64, location string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.findByIDs(ids) {
		t.DownloadDir = location
	}
}

func (s *Store) RenamePath(ids []int64, path, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.findByIDs(ids) {
		for i := range t.Files {
			if t.Files[i].Name == path {
				t.Files[i].Name = name
			}
		}
		if t.Name == path {
			t.Name = name
		}
	}
}

// QueueMove 调整队列位置：按当前队列顺序整体重排后重新编号，
// 与真实 Transmission 的语义一致（top/bottom 归边，up/down 与相邻非目标位互换）。
func (s *Store) QueueMove(ids []int64, direction string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(ids) == 0 {
		return
	}
	targets := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		targets[id] = struct{}{}
	}
	ordered := make([]*mockTorrent, len(s.torrents))
	copy(ordered, s.torrents)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].QueuePosition < ordered[j].QueuePosition
	})

	switch direction {
	case "top", "bottom":
		var rest, picked []*mockTorrent
		for _, t := range ordered {
			if _, ok := targets[t.ID]; ok {
				picked = append(picked, t)
			} else {
				rest = append(rest, t)
			}
		}
		if direction == "top" {
			ordered = append(picked, rest...)
		} else {
			ordered = append(rest, picked...)
		}
	case "up", "down":
		moved := make([]bool, len(ordered))
		for i, t := range ordered {
			if _, ok := targets[t.ID]; ok {
				moved[i] = true
			}
		}
		if direction == "up" {
			for i := 1; i < len(ordered); i++ {
				if moved[i] && !moved[i-1] {
					ordered[i], ordered[i-1] = ordered[i-1], ordered[i]
					moved[i], moved[i-1] = moved[i-1], moved[i]
				}
			}
		} else {
			for i := len(ordered) - 2; i >= 0; i-- {
				if moved[i] && !moved[i+1] {
					ordered[i], ordered[i+1] = ordered[i+1], ordered[i]
					moved[i], moved[i+1] = moved[i+1], moved[i]
				}
			}
		}
	default:
		return
	}

	for i, t := range ordered {
		t.QueuePosition = int64(i)
	}
}

// sessionReadOnlyKeys 为只读/派生键：session-set 不应改变它们的回显。
var sessionReadOnlyKeys = map[string]struct{}{
	"fields": {}, "session-id": {}, "version": {}, "rpc-version": {},
	"rpc-version-semver": {}, "rpc-version-minimum": {}, "config-dir": {},
	"units": {}, "blocklist-size": {},
}

// SetSession 应用 session-set 的可识别键；全部键值另存入 Extra 供 session-get 回显。
func (s *Store) SetSession(fields map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := strField(fields, "download-dir"); ok {
		s.session.DownloadDir = v
	}
	if v, ok := numField(fields, "speed-limit-down"); ok {
		s.session.SpeedLimitDown = v
	}
	if v, ok := boolField(fields, "speed-limit-down-enabled"); ok {
		s.session.SpeedLimitDownOn = v
	}
	if v, ok := numField(fields, "speed-limit-up"); ok {
		s.session.SpeedLimitUp = v
	}
	if v, ok := boolField(fields, "speed-limit-up-enabled"); ok {
		s.session.SpeedLimitUpOn = v
	}
	if v, ok := boolField(fields, "alt-speed-enabled"); ok {
		s.session.AltSpeedEnabled = v
	}
	if v, ok := numField(fields, "peer-limit-global"); ok {
		s.session.PeerLimitGlobal = v
	}
	if v, ok := numField(fields, "peer-port"); ok {
		s.session.PeerPort = v
	}
	if v, ok := boolField(fields, "blocklist-enabled"); ok {
		s.session.BlocklistEnabled = v
	}
	if s.session.Extra == nil {
		s.session.Extra = map[string]any{}
	}
	for k, v := range fields {
		if _, ro := sessionReadOnlyKeys[k]; ro {
			continue
		}
		s.session.Extra[k] = v
	}
}

// BlocklistUpdate 返回一个增长后的规则数。
func (s *Store) BlocklistUpdate() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.session.BlocklistSize += 1000
	return s.session.BlocklistSize
}

// ---- 小工具 ----

func toStringSlice(v []any) []string {
	out := make([]string, 0, len(v))
	for _, x := range v {
		if str, ok := x.(string); ok {
			out = append(out, str)
		}
	}
	return out
}

func numField(m map[string]any, key string) (int64, bool) {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int64(n), true
		case int64:
			return n, true
		case int:
			return int64(n), true
		}
	}
	return 0, false
}

func floatField(m map[string]any, key string) (float64, bool) {
	if v, ok := m[key]; ok {
		if f, ok := v.(float64); ok {
			return f, true
		}
	}
	return 0, false
}

func boolField(m map[string]any, key string) (bool, bool) {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b, true
		}
	}
	return false, false
}

func strField(m map[string]any, key string) (string, bool) {
	if v, ok := m[key]; ok {
		if str, ok := v.(string); ok {
			return str, true
		}
	}
	return "", false
}
