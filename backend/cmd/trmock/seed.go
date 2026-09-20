package main

import (
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// 多站点 tracker，用于站点维度过滤演示
var seedSites = []struct {
	Site     string
	Announce string
}{
	{" example-tracker.org", "https://tracker.example-tracker.org/announce"},
	{" open-mirror.net", "udp://open-mirror.net:6969/announce"},
	{" private-hd.club", "https://private-hd.club/tracker.php"},
	{" public.pushep.com", "udp://public.pushep.com:1337/announce"},
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

var sampleLabels = [][]string{
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

// seedTorrents 生成覆盖各状态/体量/标签/站点/目录/错误的初始数据。
func seedTorrents(s *Store, n int) []*mockTorrent {
	rng := rand.New(rand.NewSource(time.Now().UnixNano() + 1))
	dirs := []string{"/downloads", "/downloads/media", "/volume1/video"}
	now := time.Now()
	var out []*mockTorrent

	for i := 0; i < n; i++ {
		name := sampleNames[i%len(sampleNames)]
		size := int64(200*1024*1024) + rng.Int63n(30*1024*1024*1024)
		t := &mockTorrent{
			ID:                  s.nextID,
			Name:                name,
			HashString:          fakeHash(rng),
			Creator:             "Transmission " + fmt.Sprintf("%d.%d", 4, rng.Intn(2)),
			TotalSize:           size,
			AddedDate:           now.Add(-time.Duration(rng.Intn(60*24)) * time.Hour).Unix(),
			DownloadDir:         dirs[rng.Intn(len(dirs))],
			Labels:              append([]string(nil), sampleLabels[i%len(sampleLabels)]...),
			QueuePosition:       int64(i),
			PeerLimit:           60,
			Comment:             "mock torrent for development",
			HonorsSessionLimits: true,
			baseDown:            1*1024*1024 + rng.Int63n(10*1024*1024),
			baseUp:              128*1024 + rng.Int63n(2*1024*1024),
			PieceCount:          size/(16*1024*1024) + 1,
			PieceSize:           16 * 1024 * 1024,
			IsPrivate:           rng.Intn(4) == 0,
		}
		s.nextID++

		// 站点分布
		siteIdx := rng.Intn(len(seedSites))
		site := seedSites[siteIdx]
		t.Trackers = []mockTracker{{
			ID: 0, Announce: site.Announce,
			Scrape:   strings.Replace(site.Announce, "announce", "scrape", 1),
			SiteName: strings.TrimSpace(site.Site), Tier: 0,
		}}
		t.TrackerStats = trackerStatsFromTrackers(t.Trackers)
		t.Peers = genPeers(rng)

		// 按索引分配状态，确保覆盖全部 8 种
		assignState(t, i%8, rng, now)

		// 少量错误样本
		if i%9 == 7 {
			t.Error = 2
			t.ErrorString = "could not connect to tracker"
		}
		if i%11 == 9 {
			t.IsStalled = true
		}

		buildFiles(t, rng)
		out = append(out, t)
	}
	return out
}

// assignState 按状态码布置进度/速率/时间字段。
func assignState(t *mockTorrent, status int, rng *rand.Rand, now time.Time) {
	t.Status = int64(status)
	switch status {
	case stStopped:
		t.PercentDone = clamp01(rng.Float64() * 0.9)
		t.HaveValid = int64(float64(t.TotalSize) * t.PercentDone)
		t.DownloadedEver = t.HaveValid
		t.ETA = -1
	case stCheckWait, stCheck:
		t.PercentDone = clamp01(rng.Float64())
		t.HaveValid = int64(float64(t.TotalSize) * t.PercentDone)
		t.DownloadedEver = t.HaveValid
		if status == stCheck {
			t.RecheckProgress = rng.Float64() * 0.5
		}
	case stDownloadWait, stDownload:
		t.PercentDone = clamp01(rng.Float64() * 0.85)
		t.HaveValid = int64(float64(t.TotalSize) * t.PercentDone)
		t.DownloadedEver = t.HaveValid
		if status == stDownload {
			t.RateDownload = jitter(rng, t.baseDown)
			t.RateUpload = jitter(rng, t.baseUp/4)
			t.PeersSendingToUs = int64(3 + rng.Intn(12))
			t.PeersConnected = t.PeersSendingToUs + int64(rng.Intn(6))
			left := t.leftUntilDone()
			if t.RateDownload > 0 {
				t.ETA = left / t.RateDownload
			}
		} else {
			t.ETA = -2
		}
	case stSeedWait, stSeed:
		t.PercentDone = 1
		t.HaveValid = t.TotalSize
		t.DownloadedEver = t.TotalSize
		t.DoneDate = now.Add(-time.Duration(rng.Intn(72)) * time.Hour).Unix()
		t.IsFinished = true
		t.ETA = -1
		t.UploadedEver = int64(float64(t.TotalSize) * rng.Float64() * 3)
		t.SecondsSeeding = int64(rng.Intn(50 * 3600))
		if t.TotalSize > 0 {
			t.UploadRatio = float64(t.UploadedEver) / float64(t.TotalSize)
		}
		if status == stSeed {
			t.RateUpload = jitter(rng, t.baseUp)
			t.PeersGettingFromUs = int64(2 + rng.Intn(10))
			t.PeersConnected = t.PeersGettingFromUs + int64(rng.Intn(4))
		}
	case stIsolated:
		t.PercentDone = clamp01(rng.Float64())
		t.HaveValid = int64(float64(t.TotalSize) * t.PercentDone)
		t.DownloadedEver = t.HaveValid
		t.ETA = -2
	}
	t.ActivityDate = now.Unix()
}

func buildFiles(t *mockTorrent, rng *rand.Rand) {
	count := 1 + rng.Intn(4)
	var files []mockFile
	var stats []mockFileStat
	remaining := t.TotalSize
	for i := 0; i < count; i++ {
		var flen int64
		if i == count-1 {
			flen = remaining
		} else {
			flen = remaining / int64(count-i)
			remaining -= flen
		}
		files = append(files, mockFile{
			Name:           fmt.Sprintf("%s/file-%02d.bin", t.Name, i+1),
			Length:         flen,
			BytesCompleted: int64(float64(flen) * t.PercentDone),
		})
		stats = append(stats, mockFileStat{
			BytesCompleted: int64(float64(flen) * t.PercentDone),
			Wanted:         true,
			Priority:       0,
		})
	}
	t.Files = files
	t.FileStats = stats
	t.FileCount = int64(len(files))
}

func genPeers(rng *rand.Rand) []mockPeer {
	// 至少 1 个 peer：活动种子语义上必有连接（PeersConnected >= 3），
	// 明细为空会让按 id 拉详情的集成测试偶发落空（约 1/5 概率）
	n := 1 + rng.Intn(5)
	clients := []string{"qBittorrent/4.6.2", "Transmission/4.0", "Deluge/2.1", "libtorrent/2.0"}
	var peers []mockPeer
	for i := 0; i < n; i++ {
		peers = append(peers, mockPeer{
			Address:           fmt.Sprintf("192.168.1.%d", 2+rng.Intn(250)),
			ClientName:        clients[rng.Intn(len(clients))],
			FlagStr:           "UEH",
			Progress:          rng.Float64(),
			RateToClient:      int64(rng.Intn(500 * 1024)),
			RateToPeer:        int64(rng.Intn(200 * 1024)),
			IsDownloadingFrom: true,
			IsEncrypted:       rng.Intn(2) == 0,
			IsIncoming:        rng.Intn(2) == 0,
			Port:              int64(51413 + rng.Intn(100)),
		})
	}
	return peers
}

// fakeHash 生成 40 位十六进制伪哈希。
func fakeHash(rng *rand.Rand) string {
	const hex = "0123456789abcdef"
	b := make([]byte, 40)
	for i := range b {
		b[i] = hex[rng.Intn(len(hex))]
	}
	return string(b)
}

// trackersFromList 解析 torrent-set 的 trackerList（每行一个，空行分隔 tier）。
func trackersFromList(list string) []mockTracker {
	var out []mockTracker
	var tier int64
	id := int64(0)
	for _, line := range strings.Split(list, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			tier++
			continue
		}
		out = append(out, mockTracker{
			ID: id, Announce: line,
			Scrape:   strings.Replace(line, "announce", "scrape", 1),
			SiteName: hostOf(line), Tier: tier,
		})
		id++
	}
	return out
}

func trackerStatsFromTrackers(trackers []mockTracker) []mockTrackerStat {
	now := time.Now().Unix()
	var out []mockTrackerStat
	for _, tr := range trackers {
		out = append(out, mockTrackerStat{
			ID:                    tr.ID,
			Host:                  tr.SiteName,
			Announce:              tr.Announce,
			AnnounceState:         1,
			Tier:                  tr.Tier,
			LastAnnounceResult:    "Success",
			LastAnnounceSucceeded: true,
			LastAnnounceTime:      now - 300,
			LastAnnouncePeerCount: 25,
			NextAnnounceTime:      now + 1500,
			ScrapeState:           1,
			LastScrapeResult:      "Success",
			LastScrapeSucceeded:   true,
			LastScrapeTime:        now - 300,
			NextScrapeTime:        now + 1500,
			SeederCount:           12,
			LeecherCount:          7,
			DownloadCount:         128,
		})
	}
	return out
}

func hostOf(announce string) string {
	u := announce
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if i := strings.IndexAny(u, ":/"); i >= 0 {
		u = u[:i]
	}
	return u
}
