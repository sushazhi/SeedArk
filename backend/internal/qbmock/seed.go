package qbmock

import (
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// 站点池与 cmd/trmock 用同一批假站点：qB / TR 两个 mock 同时挂载时，
// 聚合视图里的「站点」维度能对得上，方便对照两个驱动的渲染差异。
// siteSpec 假站点：announce URL + 是否私有（私有站拿不到 swarm 统计）
type siteSpec struct {
	Site     string
	Announce string
	Private  bool
}

var seedSites = []siteSpec{
	{"tracker.example-tracker.org", "https://tracker.example-tracker.org/announce", false},
	{"open-mirror.net", "udp://open-mirror.net:6969/announce", false},
	{"private-hd.club", "https://private-hd.club/tracker.php", true},
	{"public.pushep.com", "udp://public.pushep.com:1337/announce", false},
}

// 额外 announce 池：公共种子常挂多级 tracker，用于验证 tier 与多 tracker 展示
var extraTrackers = []string{
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://tracker.torrent.eu.org:451/announce",
	"http://tracker.public.pushep.com:80/announce",
}

var sampleNames = []string{
	"ubuntu-24.04-desktop-amd64.iso",
	"Big.Buck.Bunny.2008.1080p.BluRay.x264",
	"The.Wire.S01E01.720p.WEB-DL",
	"debian-12.5.0-amd64-DVD-1.iso",
	"archlinux-2024.06.01-x86_64.iso",
	"Nature.Documentary.Oceans.2160p.HDR",
	"fedora-workstation-live-40.iso",
	"Indie.Game.Soundtrack.FLAC",
	"openSUSE-Tumbleweed-DVD-x86_64.iso",
	"Blender.Foundation.Sintel.4K",
	"KDE.Plasma.Wallpapers.Pack",
	"LibreOffice.Help.Docs.All.Langs",
	"Vintage.Film.Collection.Remastered",
	"Machine.Learning.Course.Notes.PDF",
}

// qBittorrent 侧标签（tags），与 trmock 的 labels 同源
var sampleTags = [][]string{
	{"linux", "iso"},
	{"video", "hd"},
	{"video"},
	{"iso"},
	{"linux"},
	{"video", "4k"},
	nil,
	{"audio", "flac"},
	{"docs"},
	nil,
	{"wallpapers"},
	{"docs", "iso"},
	{"video"},
	{"docs"},
}

// qBittorrent 独有维度：分类（category）。面板把分类并入 labels 展示，
// 因此这里刻意让部分种子带分类，验证驱动的标签/分类拆分逻辑
var sampleCategories = []string{"", "", "movies", "linux", "tv", "", "docs"}

var sampleDirs = []string{"/downloads", "/downloads/media", "/volume1/video", "/downloads/linux"}

// stateCycle 按序号轮转布置状态：任意种子数量下都能同时看到下载、做种、
// 停滞、排队、强制、停止、磁力等场景，覆盖驱动 mapState 的全部分支
var stateCycle = []string{
	"downloading",
	"uploading",
	"stalledDL",
	"stalledUP",
	"queuedDL",
	"queuedUP",
	"forcedUP",
	"stoppedUP",
	"stoppedDL",
	"metaDL",
}

// etaUnknown qBittorrent 表示「未知剩余时间」的哨兵值（驱动据此还原为 -1）
const etaUnknown int64 = 8640000

// newRand 模拟数据用的随机源。无需密码学强度（假 hash、抖动速率），
// 单独取时间种子以便多次启动看到不同布局
func newRand() *rand.Rand {
	return rand.New(rand.NewSource(time.Now().UnixNano()))
}

// seedTorrents 生成初始种子集，并注册分类表（qBittorrent 的分类是独立对象）
func (s *Server) seedTorrents(n int) {
	rng := s.rng
	now := time.Now().Unix()
	for i := 0; i < n; i++ {
		site := seedSites[i%len(seedSites)]
		name := sampleNames[i%len(sampleNames)]
		state := stateCycle[i%len(stateCycle)]
		if i%13 == 12 {
			state = "error" // 少量报错样本，验证面板的错误列与红色状态
		}
		if s.opts.Compat == "4.x" {
			// 4.x 只有 pausedDL/pausedUP（stopped* 是 5.0 引入的）
			state = strings.ReplaceAll(strings.ReplaceAll(state, "stoppedUP", "pausedUP"), "stoppedDL", "pausedDL")
		}

		category := sampleCategories[i%len(sampleCategories)]
		dir := sampleDirs[i%len(sampleDirs)]
		if category != "" {
			dir = dir + "/" + category
		}
		t := &torrent{
			Hash:         randHex(rng),
			Name:         name,
			State:        state,
			Size:         guessSize(rng, name),
			SavePath:     dir,
			Category:     category,
			Tags:         append([]string(nil), sampleTags[i%len(sampleTags)]...),
			Private:      site.Private || rng.Intn(5) == 0,
			AutoTMM:      rng.Intn(4) != 0,
			CreatedBy:    fmt.Sprintf("qBittorrent v%d.%d.%d", 4+rng.Intn(2), rng.Intn(7), rng.Intn(4)),
			CreationDate: now - int64(90+rng.Intn(600))*24*3600,
			// 添加时间按序号递增：列表默认按 added_on 排序，
			// 让测试里 list[0] 稳定落在 downloading 状态
			AddedOn:                  now - int64(n-i)*48*3600 + int64(rng.Intn(6*3600)),
			Comment:                  "mock torrent for development (qbmock)",
			HasMetadata:              true,
			DLLimit:                  -1,
			ULLimit:                  -1,
			RatioLimit:               -2,
			SeedingTimeLimit:         -2,
			InactiveSeedingTimeLimit: -2,
			Priority:                 int64(i + 1),
			SeqDL:                    i%7 == 3,
			Reannounce:               int64(300 + rng.Intn(1200)),
			baseDown:                 1024*1024 + rng.Int63n(12*1024*1024),
			baseUp:                   128*1024 + rng.Int63n(3*1024*1024),
		}
		t.InfohashV1 = t.Hash
		t.MagnetURI = "magnet:?xt=urn:btih:" + t.Hash + "&dn=" + strings.ReplaceAll(name, " ", "%20")
		t.Trackers = genTrackers(rng, site, t.Private, state == "error")
		t.Availability = 1
		s.assignTorrentState(t, rng, now)
		s.torrents[t.Hash] = t
		if category != "" {
			if _, ok := s.cats[category]; !ok {
				s.cats[category] = dir
			}
		}
	}
}

// assignTorrentState 按状态布置进度、速率、连接数与时间字段
func (s *Server) assignTorrentState(t *torrent, rng *rand.Rand, now int64) {
	switch t.State {
	case "downloading", "forcedDL":
		t.Progress = 0.05 + rng.Float64()*0.8
		t.setSwarm(rng, 2, 10, 1, 8)
		t.dlTick(rng, now)
	case "stalledDL":
		// 停滞 = 已连接但无人可传，速率归零
		t.Progress = 0.1 + rng.Float64()*0.7
		t.NumSeeds, t.NumLeechs = 0, 0
		t.idle()
	case "queuedDL":
		t.Progress = rng.Float64() * 0.5
		t.NumSeeds, t.NumLeechs = 0, 0
		t.idle()
	case "uploading", "forcedUP":
		t.completed(now, rng)
		t.setSwarm(rng, 0, 0, 2, 10)
		t.ULSpeed = jitter(rng, t.baseUp)
		t.ETA = etaUnknown
	case "stalledUP":
		t.completed(now, rng)
		t.setSwarm(rng, 0, 0, 0, 0)
		t.idle()
	case "queuedUP":
		t.completed(now, rng)
		t.setSwarm(rng, 0, 0, 0, 0)
		t.idle()
	case "metaDL":
		// 磁力尚未拿到元数据：无体积、无文件、连接数未知（-1）
		t.HasMetadata = false
		t.Size = 0
		t.Progress = 0
		t.NumSeeds, t.NumLeechs = -1, -1
		t.NumComplete, t.NumIncomplete = -1, -1
		t.Availability = -1
		t.idle()
	case "error":
		t.Progress = rng.Float64() * 0.6
		t.setSwarm(rng, 0, 0, 0, 0)
		t.idle()
	default: // stoppedUP / stoppedDL / pausedUP / pausedDL
		if t.State == "stoppedUP" || t.State == "pausedUP" {
			t.completed(now, rng)
		} else {
			t.Progress = rng.Float64() * 0.9
		}
		t.setSwarm(rng, 0, 0, 0, 0)
		t.idle()
	}
	t.SeedingTimeLimit = -2
	if t.Progress >= 1 {
		t.SeedingTime = int64(rng.Intn(60 * 3600))
	}
	t.TimeActive = now - t.AddedOn - int64(rng.Intn(3600))
	if t.TimeActive < 0 {
		t.TimeActive = 0
	}
	if t.State == "metaDL" {
		t.metaAge = int64(rng.Intn(4))
	}
	// Downloaded / Uploaded 是 Step 推进的基准，必须与 Progress 对齐，
	// 否则第一帧就会把进度拽回 0
	if t.Progress < 1 && t.Size > 0 {
		t.Downloaded = int64(float64(t.Size) * t.Progress)
	}
	if t.Uploaded == 0 {
		t.Uploaded = int64(float64(t.Downloaded) * rng.Float64() * 0.6)
	}
	if t.Downloaded > 0 {
		t.Ratio = float64(t.Uploaded) / float64(t.Downloaded)
	}
	fillContent(t, rng)
	if t.HasMetadata {
		t.Peers = genPeers(rng, t, now)
	}
}

// dlTick 按下载速率刷新一帧速率与 ETA
func (t *torrent) dlTick(rng *rand.Rand, now int64) {
	t.DLSpeed = jitter(rng, t.baseDown)
	t.ULSpeed = jitter(rng, t.baseUp/4)
	left := t.Size - int64(float64(t.Size)*t.Progress)
	if t.DLSpeed > 0 && left > 0 {
		t.ETA = left / t.DLSpeed
	} else {
		t.ETA = etaUnknown
	}
	t.LastActivity = now
}

// completed 布置「已下载完」的字段
func (t *torrent) completed(now int64, rng *rand.Rand) {
	t.Progress = 1
	t.CompletionOn = now - int64(rng.Intn(72*3600))
	t.Downloaded = t.Size
	t.Uploaded = int64(float64(t.Size) * (0.3 + rng.Float64()*4))
	t.LastActivity = now
}

// idle 静止帧：速率归零、ETA 未知、活动时间是最近一次心跳
func (t *torrent) idle() {
	t.DLSpeed = 0
	t.ULSpeed = 0
	t.ETA = etaUnknown
	t.LastActivity = time.Now().Unix() - int64(600)
}

// setSwarm 布置已连接做种者/下载者与全群数量（私有站点的总数 qB 拿不到，报 -1）
func (t *torrent) setSwarm(rng *rand.Rand, seedLo, seedHi, leechLo, leechHi int) {
	t.NumSeeds = randInt(rng, seedLo, seedHi)
	t.NumLeechs = randInt(rng, leechLo, leechHi)
	if t.Private {
		t.NumComplete, t.NumIncomplete = -1, -1
		return
	}
	t.NumComplete = t.NumSeeds + randInt(rng, 3, 200)
	t.NumIncomplete = t.NumLeechs + randInt(rng, 0, 60)
}

func randInt(rng *rand.Rand, lo, hi int) int64 {
	if hi <= lo {
		return int64(lo)
	}
	return int64(lo + rng.Intn(hi-lo+1))
}

// genTrackers 主 tracker + 公共种子的额外 tracker
func genTrackers(rng *rand.Rand, site siteSpec, private bool, broken bool) []tracker {
	mk := func(u string, tier int64) tracker {
		tr := tracker{
			URL: u, Tier: tier, Status: 2, Msg: "Working",
			NumPeers: randInt(rng, 5, 80),
		}
		if private {
			// 私有站不返回 swarm 统计，qB 报 0
			tr.NumSeeds, tr.NumLeechers, tr.NumDownloaded = 0, 0, 0
		} else {
			tr.NumSeeds = randInt(rng, 3, 300)
			tr.NumLeechers = randInt(rng, 0, 80)
			tr.NumDownloaded = randInt(rng, 10, 5000)
		}
		return tr
	}
	out := []tracker{mk(site.Announce, 0)}
	if broken {
		out[0].Status = 5 // WebAPI 2.13+：tracker 报错
		out[0].Msg = "qBittorrent 无法连接该 tracker"
		out[0].NumPeers, out[0].NumSeeds, out[0].NumLeechers = 0, 0, 0
		return out
	}
	if !private {
		extra := append([]string(nil), extraTrackers...)
		for i := 0; i < rng.Intn(3); i++ {
			out = append(out, mk(extra[i%len(extra)], int64(i+1)))
		}
	}
	return out
}

// guessSize 按名称线索给出量级合理的体积：ISO 数 GB、4K 数十 GB、文档几十 MB
func guessSize(rng *rand.Rand, name string) int64 {
	const mb = int64(1024 * 1024)
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, ".iso"):
		return 2048*mb + rng.Int63n(4096*mb)
	case strings.Contains(lower, "2160p"), strings.Contains(lower, "4k"):
		return 8192*mb + rng.Int63n(18*1024*mb)
	case strings.Contains(lower, "1080p"), strings.Contains(lower, "720p"):
		return 2048*mb + rng.Int63n(8*1024*mb)
	case strings.Contains(lower, "flac"):
		return 200*mb + rng.Int63n(600*mb)
	case strings.Contains(lower, "wallpaper"):
		return 60*mb + rng.Int63n(400*mb)
	case strings.Contains(lower, "pdf"), strings.Contains(lower, "docs"):
		return 8*mb + rng.Int63n(180*mb)
	default:
		return 200*mb + rng.Int63n(6*1024*mb)
	}
}

// pieceSizeFor qBittorrent 的自动分块策略：体积越大块越大，
// 同时把块数压在一两万以内，避免 mock 常驻内存过大
func pieceSizeFor(size int64) int64 {
	const (
		kib = int64(1024)
		mib = 1024 * kib
	)
	switch {
	case size >= 16*1024*mib:
		return 16 * mib
	case size >= 4*1024*mib:
		return 8 * mib
	case size >= 1024*mib:
		return 4 * mib
	case size >= 128*mib:
		return 1 * mib
	default:
		return 256 * kib
	}
}

// fillContent 依据当前进度生成文件列表与块状态，保证三者自洽：
// 面板详情同时读 files.progress、pieceStates 与 completed，
// 任一处漂移都会让「文件进度条 / 块视图 / 总量」互相对不上。
func fillContent(t *torrent, rng *rand.Rand) {
	if t.Size <= 0 {
		t.Files = nil
		t.Pieces = nil
		t.pieceDone = 0
		return
	}
	ps := pieceSizeFor(t.Size)
	pieces := int(t.Size / ps)
	if pieces < 1 {
		pieces = 1
	}
	t.Pieces = make([]int, pieces)
	done := int(float64(pieces) * clamp01(t.Progress))
	if done > pieces {
		done = pieces
	}
	for i := range t.Pieces {
		if i < done {
			t.Pieces[i] = 2
		}
	}
	if done < pieces && isLiveDownload(t.State) {
		t.Pieces[done] = 1 // 正在下载的块
	}
	t.pieceDone = done

	names := fileNames(t, rng)
	t.Files = splitFiles(t, rng, names, ps, pieces, done)
	// content_path：多文件指向顶层目录，单文件指向文件本身
	if i := strings.Index(names[0], "/"); i > 0 {
		t.ContentPath = t.SavePath + "/" + names[0][:i]
	} else {
		t.ContentPath = t.SavePath + "/" + names[0]
	}
}

// fileNames 生成与名称气质相符的文件名：单文件直接叫种子名，
// 多文件则落在同名目录下（qBittorrent 的 content_path 语义）
func fileNames(t *torrent, rng *rand.Rand) []string {
	folder := strings.TrimSuffix(t.Name, pathExt(t.Name))
	lower := strings.ToLower(t.Name)
	switch {
	case strings.Contains(lower, ".iso"), rng.Intn(3) == 0:
		return []string{t.Name}
	case strings.Contains(lower, "flac"):
		return []string{folder + "/disc.flac", folder + "/.cue", folder + "/.m3u"}
	case strings.Contains(lower, "pdf"), strings.Contains(lower, "docs"):
		return []string{folder + "/notes.pdf", folder + "/README.txt"}
	default:
		return []string{
			folder + "/" + folder + ".mkv",
			folder + "/" + folder + ".en.srt",
			folder + "/sample.nfo",
		}
	}
}

// splitFiles 按块区间切分文件，并按已下载块数计算每个文件的进度
func splitFiles(t *torrent, rng *rand.Rand, names []string, ps int64, pieces, done int) []file {
	shares := make([]float64, len(names))
	total := 0.0
	for i := range names {
		// 主文件占大头，附带文件小
		shares[i] = 1.0
		if i > 0 {
			shares[i] = 0.02 + rng.Float64()*0.1
		}
		total += shares[i]
	}
	out := make([]file, 0, len(names))
	var usedSize, usedPieces int64
	for i, n := range names {
		size := int64(float64(t.Size) * shares[i] / total)
		if i == len(names)-1 {
			size = t.Size - usedSize
		}
		if size < 1 {
			size = 1
		}
		cnt := int(size / ps)
		if i == len(names)-1 {
			cnt = pieces - int(usedPieces)
		}
		if cnt < 1 {
			cnt = 1
		}
		start := usedPieces
		end := start + int64(cnt) - 1
		if end > int64(pieces-1) {
			end = int64(pieces - 1)
		}
		have := clampFloat(float64(int64(done)-start), 0, float64(end-start+1))
		prog := 0.0
		if end >= start {
			prog = have / float64(end-start+1)
		}
		usedSize += size
		usedPieces = end + 1
		out = append(out, file{
			Index:        int64(i),
			Name:         n,
			Size:         size,
			Progress:     clamp01(prog),
			Priority:     1,
			PieceStart:   start,
			PieceEnd:     end,
			Availability: availOf(t),
		})
	}
	return out
}

func availOf(t *torrent) float64 {
	if t.Availability < 0 {
		return -1
	}
	if t.Progress >= 1 {
		return 1
	}
	return 0.7
}

// pathExt 取扩展名（含点），无扩展名返回空串
func pathExt(p string) string {
	if i := strings.LastIndex(p, "."); i > strings.LastIndex(p, "/") {
		return p[i:]
	}
	return ""
}

func isLiveDownload(state string) bool {
	return state == "downloading" || state == "forcedDL"
}

// newAddedTorrent 通过 .torrent 文件或 http(s) 链接添加的种子：
// 元数据即时可用，直接落到低进度的下载态（paused 时为停止态）
func (s *Server) newAddedTorrent(hash, name string, paused bool, savePath string, tags []string, category string) *torrent {
	now := time.Now().Unix()
	rng := s.rng
	if name == "" {
		name = "torrent-" + hash[:8]
	}
	t := &torrent{
		Hash:                     hash,
		InfohashV1:               hash,
		Name:                     name,
		Size:                     guessSize(rng, name),
		SavePath:                 s.resolveSavePath(savePath, category),
		Category:                 category,
		Tags:                     append([]string(nil), tags...),
		AddedOn:                  now,
		TimeActive:               0,
		HasMetadata:              true,
		AutoTMM:                  savePath == "",
		Availability:             1,
		Reannounce:               int64(300 + rng.Intn(1200)),
		CreatedBy:                "qBittorrent v5.2.3",
		CreationDate:             now - int64(24*3600),
		Comment:                  "added via qbmock",
		MagnetURI:                "magnet:?xt=urn:btih:" + hash,
		DLLimit:                  -1,
		ULLimit:                  -1,
		RatioLimit:               -2,
		SeedingTimeLimit:         -2,
		InactiveSeedingTimeLimit: -2,
		Priority:                 int64(len(s.torrents) + 1),
		baseDown:                 1024*1024 + rng.Int63n(8*1024*1024),
		baseUp:                   128*1024 + rng.Int63n(2*1024*1024),
	}
	site := seedSites[len(s.torrents)%len(seedSites)]
	t.Trackers = genTrackers(rng, site, t.Private, false)
	if paused {
		s.stopLocked(t)
	} else {
		t.State = "downloading"
	}
	t.setSwarm(rng, 1, 8, 0, 4)
	fillContent(t, rng)
	t.Peers = genPeers(rng, t, now)
	return t
}

// newMagnetTorrent 磁力链接：停留在 metaDL，等 Step 模拟元数据到达
func (s *Server) newMagnetTorrent(hash, name string, paused bool, savePath string, tags []string, category string) *torrent {
	now := time.Now().Unix()
	t := &torrent{
		Hash:                     hash,
		InfohashV1:               hash,
		Name:                     name,
		State:                    "metaDL",
		SavePath:                 s.resolveSavePath(savePath, category),
		Category:                 category,
		Tags:                     append([]string(nil), tags...),
		AddedOn:                  now,
		HasMetadata:              false,
		AutoTMM:                  savePath == "",
		Availability:             -1,
		MagnetURI:                "magnet:?xt=urn:btih:" + hash,
		DLLimit:                  -1,
		ULLimit:                  -1,
		RatioLimit:               -2,
		SeedingTimeLimit:         -2,
		InactiveSeedingTimeLimit: -2,
		Priority:                 int64(len(s.torrents) + 1),
		NumSeeds:                 -1,
		NumLeechs:                -1,
		NumComplete:              -1,
		NumIncomplete:            -1,
		ETA:                      etaUnknown,
		metaAge:                  0,
	}
	if paused {
		s.stopLocked(t)
	}
	return t
}

// resolveSavePath 添加时的保存路径：显式路径 > 分类路径 > 全局默认
func (s *Server) resolveSavePath(savePath, category string) string {
	if savePath != "" {
		return strings.TrimSuffix(savePath, "/")
	}
	if category != "" {
		if cp := s.cats[category]; cp != "" {
			return cp
		}
	}
	if sp, ok := s.prefs["save_path"].(string); ok && sp != "" {
		return sp
	}
	return "/downloads"
}

// buildMagnetMetadata 磁力元数据到达：补齐体积/名称/文件/块/tracker，
// 与真实 qBittorrent 收到 metadata 后的表现一致
func (s *Server) buildMagnetMetadata(t *torrent) {
	rng := s.rng
	if t.Name == "" || strings.HasPrefix(t.Name, "magnet-") {
		t.Name = sampleNames[rng.Intn(len(sampleNames))]
	}
	t.Size = guessSize(rng, t.Name)
	t.HasMetadata = true
	t.Availability = 0.6 + rng.Float64()*0.4
	t.CreatedBy = "qBittorrent v5.2.3"
	t.CreationDate = time.Now().Unix() - int64(30*24*3600)
	t.Comment = "metadata resolved by qbmock"
	site := seedSites[rng.Intn(len(seedSites))]
	t.Private = site.Private
	t.Trackers = genTrackers(rng, site, t.Private, false)
	t.State = "downloading"
	t.setSwarm(rng, 1, 8, 1, 6)
	t.baseDown = 1024*1024 + rng.Int63n(8*1024*1024)
	t.baseUp = 128*1024 + rng.Int63n(2*1024*1024)
	fillContent(t, rng)
	t.Peers = genPeers(rng, t, time.Now().Unix())
}

// genPeers 生成对端表。键为 "ip:port"，与 torrents/peers 的响应结构一致；
// connection 字段带 utp / encrypted 线索，驱动的 IsUTP / IsEncrypted 靠它判定
func genPeers(rng *rand.Rand, t *torrent, now int64) map[string]peer {
	n := int(t.NumSeeds + t.NumLeechs)
	if n <= 0 {
		n = 1 + rng.Intn(3)
	}
	if n > 12 {
		n = 12
	}
	clients := []string{"qBittorrent/5.2.3", "qBittorrent/4.6.5", "Transmission/4.0.6", "Deluge/2.1.1", "libtorrent/2.0.10"}
	countries := []string{"China", "United States", "Germany", "Japan", "Netherlands", "Brazil"}
	out := make(map[string]peer, n)
	for i := 0; i < n; i++ {
		ip := fmt.Sprintf("%d.%d.%d.%d", rng.Intn(200)+10, rng.Intn(255), rng.Intn(255), rng.Intn(250)+2)
		port := int64(1024 + rng.Intn(64000))
		utp := rng.Intn(2) == 0
		enc := rng.Intn(3) != 0
		conn := "tcp"
		if utp {
			conn = "utp"
		}
		if enc {
			conn += ", encrypted"
		}
		down := t.State == "downloading" || t.State == "forcedDL" || t.State == "metaDL"
		flags := "D"
		if down {
			flags = "DL"
		}
		if enc {
			flags += "E"
		}
		if rng.Intn(3) == 0 {
			flags += "H"
		}
		key := fmt.Sprintf("%s:%d", ip, port)
		out[key] = peer{
			IP: ip, Port: port,
			Client:     clients[rng.Intn(len(clients))],
			Connection: conn,
			Country:    countries[rng.Intn(len(countries))],
			CountryCod: "CN",
			Flags:      flags,
			FlagsDesc:  "Downloading, Interested, Hashed",
			Progress:   rng.Float64(),
			DLSpeed:    int64(rng.Intn(800 * 1024)),
			UPSpeed:    int64(rng.Intn(400 * 1024)),
			Downloaded: int64(rng.Intn(500 * 1024 * 1024)),
			Uploaded:   int64(rng.Intn(200 * 1024 * 1024)),
			Relevance:  rng.Float64(),
		}
	}
	return out
}

// ---- 通用小工具（seed / sim 共用）----

// randHex 生成 40 位十六进制伪 infohash
func randHex(rng *rand.Rand) string {
	const digits = "0123456789abcdef"
	b := make([]byte, 40)
	for i := range b {
		b[i] = digits[rng.Intn(len(digits))]
	}
	return string(b)
}

// jitter 在基准速率上下 ±30% 抖动（与 trmock 同口径）
func jitter(rng *rand.Rand, base int64) int64 {
	if base <= 0 {
		return 0
	}
	return int64(float64(base) * (0.7 + rng.Float64()*0.6))
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

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
