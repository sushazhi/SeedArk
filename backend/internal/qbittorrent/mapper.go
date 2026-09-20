package qbittorrent

import (
	"encoding/base64"
	"net/url"
	"strings"

	"github.com/sushazhi/seedark/backend/internal/models"
)

// Transmission 状态码（前端 STATUS_META 的唯一来源，qBittorrent 状态映射到此）
const (
	trStatusStopped    int64 = 0
	trStatusQueuedChk  int64 = 1
	trStatusChecking   int64 = 2
	trStatusQueuedDL   int64 = 3
	trStatusDownload   int64 = 4
	trStatusQueuedSeed int64 = 5
	trStatusSeeding    int64 = 6
)

// mapTorrents 列表映射
func (c *Client) mapTorrents(list []torrentInfo) []*models.Torrent {
	hashes := make([]string, 0, len(list))
	for _, t := range list {
		if t.Hash != "" {
			hashes = append(hashes, t.Hash)
		}
	}
	// 先建立映射，保证下面的 c.IDFor 一次命中
	c.syncIDs(hashes)
	out := make([]*models.Torrent, 0, len(list))
	for _, t := range list {
		out = append(out, c.mapTorrent(t))
	}
	return out
}

// mapTorrent 单条映射。
// 站点名、块位图等需要额外请求的重字段不在这里填充（由详情接口补齐），
// 否则 N 个种子就是 N 次请求，列表会被拖垮。
func (c *Client) mapTorrent(t torrentInfo) *models.Torrent {
	if t.Hash != "" {
		c.rememberCategory(t.Hash, t.Category)
	}
	status, errCode, errText := mapState(t.State)
	labels := splitTags(t.Tags)
	if t.Category != "" {
		labels = append(labels, t.Category)
	}
	eta := t.ETA
	if eta >= 8640000 { // qBittorrent 用 8640000 表示「未知」
		eta = -1
	}
	out := &models.Torrent{
		ID:                 c.IDFor(t.Hash),
		Name:               t.Name,
		HashString:         t.Hash,
		Creator:            t.CreatedBy,
		TotalSize:          t.TotalSize,
		SizeWhenDone:       t.Size,
		PercentDone:        t.Progress,
		Status:             status,
		RateDownload:       t.DlSpeed,
		RateUpload:         t.UpSpeed,
		ETA:                eta,
		UploadedEver:       t.Uploaded,
		DownloadedEver:     t.Downloaded,
		UploadRatio:        t.Ratio,
		SecondsSeeding:     t.SeedingTime,
		Error:              errCode,
		ErrorString:        errText,
		Labels:             labels,
		QueuePosition:      t.Priority,
		PeersConnected:     sumNonNegative(t.NumSeeds) + sumNonNegative(t.NumLeechs),
		PeersSendingToUs:   sumNonNegative(t.NumSeeds),
		PeersGettingFromUs: sumNonNegative(t.NumLeechs),
		DownloadDir:        t.SavePath,
		AddedDate:          t.AddedOn,
		DoneDate:           t.CompletionOn,
		ActivityDate:       t.LastActivity,
		IsFinished:         t.Progress >= 1,
		IsStalled:          strings.Contains(t.State, "stalled"),
		IsPrivate:          t.Private,
		LeftUntilDone:      t.AmountLeft,
		Comment:            t.Comment,
		HaveValid:          t.Completed,
		DownloadLimited:    t.DlLimit > 0,
		DownloadLimit:      t.DlLimit / 1024,
		UploadLimited:      t.UpLimit > 0,
		UploadLimit:        t.UpLimit / 1024,
		SeedRatioLimit:     t.MaxRatio,
		SeedIdleLimit:      t.MaxInactiveSeedingTime,
		SequentialDownload: t.SequentialDownload,
		// qBittorrent 的单种限速是绝对值，不存在「跟随全局」开关，按 false 上报：
		// 组内限速引擎正是靠关闭这个标记来下发单种限速的
		HonorsSessionLimits: false,
	}
	if t.RatioLimit >= 0 {
		out.SeedRatioMode = 1
	} else {
		out.SeedRatioMode = 0
	}
	if t.Tracker != "" {
		out.TrackerStats = []models.TrackerStat{{
			ID:           0,
			Host:         hostOf(t.Tracker),
			Announce:     t.Tracker,
			Tier:         0,
			SeederCount:  sumNonNegative(t.NumComplete),
			LeecherCount: sumNonNegative(t.NumIncomplete),
		}}
	}
	if out.MagnetLink == "" {
		out.MagnetLink = magnetFromHash(t.Hash, t.InfohashV1, t.InfohashV2)
	}
	return out
}

// applyProperties 合并 /torrents/properties 的详情字段
func applyProperties(t *models.Torrent, p torrentProperties) {
	if p.PieceSize > 0 {
		t.PieceSize = p.PieceSize
	}
	if p.PiecesNum > 0 {
		t.PieceCount = p.PiecesNum
	}
	if p.Comment != "" {
		t.Comment = p.Comment
	}
	if p.SavePath != "" {
		t.DownloadDir = p.SavePath
	}
	if p.CreatedBy != "" {
		t.Creator = p.CreatedBy
	}
	if p.NbConnectionsLimit > 0 {
		t.PeerLimit = p.NbConnectionsLimit
	}
	if p.IsPrivate {
		t.IsPrivate = true
	}
	if p.UpLimit > 0 {
		t.UploadLimited = true
		t.UploadLimit = p.UpLimit / 1024
	}
	if p.DlLimit > 0 {
		t.DownloadLimited = true
		t.DownloadLimit = p.DlLimit / 1024
	}
}

// applyFiles 合并文件列表与文件统计
func applyFiles(t *models.Torrent, files []torrentFile) {
	t.FileCount = int64(len(files))
	t.Files = make([]models.FileInfo, 0, len(files))
	t.FileStats = make([]models.FileStat, 0, len(files))
	for _, f := range files {
		done := int64(float64(f.Size) * f.Progress)
		t.Files = append(t.Files, models.FileInfo{
			BytesCompleted: done,
			Length:         f.Size,
			Name:           f.Name,
		})
		t.FileStats = append(t.FileStats, models.FileStat{
			BytesCompleted: done,
			Wanted:         f.Priority != 0,
			Priority:       mapFilePriority(f.Priority),
		})
	}
}

// mapFilePriority qBittorrent 文件优先级 → Transmission 优先级（-1 低 / 0 普通 / 1 高）
func mapFilePriority(p int64) int64 {
	switch p {
	case 0:
		return 0 // 不下载（Transmission 用 fileStats.wanted=false 表达）
	case 2:
		return -1 // 低
	case 6, 7:
		return 1 // 高 / 最高
	default:
		return 0
	}
}

// applyTrackers 合并 Tracker 列表与状态
func applyTrackers(t *models.Torrent, list []torrentTracker) {
	t.Trackers = make([]models.Tracker, 0, len(list))
	t.TrackerStats = make([]models.TrackerStat, 0, len(list))
	for i, tr := range list {
		if tr.URL == "" {
			continue
		}
		t.Trackers = append(t.Trackers, models.Tracker{
			Announce: tr.URL,
			ID:       int64(i),
			Tier:     tr.Tier,
			SiteName: siteName(tr.URL),
		})
		// qBittorrent 的 status：0 禁用 / 1 未联系 / 2 正常 / 3 更新中 /
		// 4 不可用 / 5  Tracker 报错（2.13+）/ 6 不可达（2.13+）
		ok := tr.Status == 2 || tr.Status == 3
		t.TrackerStats = append(t.TrackerStats, models.TrackerStat{
			ID:                    int64(i),
			Host:                  hostOf(tr.URL),
			Announce:              tr.URL,
			AnnounceState:         tr.Status,
			Tier:                  tr.Tier,
			LastAnnounceResult:    tr.Message,
			LastAnnounceSucceeded: ok,
			SeederCount:           tr.NumSeeds,
			LeecherCount:          tr.NumLeechers,
			DownloadCount:         tr.NumDownloaded,
		})
	}
}

// applyPeers 合并 Peer 信息
func applyPeers(t *models.Torrent, peers map[string]torrentPeer) {
	t.Peers = make([]models.Peer, 0, len(peers))
	for _, p := range peers {
		flags := strings.ToLower(p.Flags + p.FlagsDesc)
		t.Peers = append(t.Peers, models.Peer{
			Address:           p.IP,
			ClientName:        p.Client,
			FlagStr:           p.Flags,
			Progress:          p.Progress,
			RateToClient:      p.DlSpeed,
			RateToPeer:        p.UpSpeed,
			IsDownloadingFrom: p.DlSpeed > 0,
			IsUploadingTo:     p.UpSpeed > 0,
			IsEncrypted:       strings.Contains(flags, "e") || strings.Contains(p.Connection, "encrypted"),
			IsIncoming:        strings.Contains(flags, "i"),
			IsUTP:             strings.Contains(p.Connection, "utp"),
			Port:              p.Port,
		})
	}
}

// encodePieces 把 pieceStates（0 未下载 / 1 下载中 / 2 已下载）编码为
// 与 Transmission pieces 一致的 base64 位图（高位在前，1 = 已下载）。
// 下载中（1）按「未下载」处理，避免块位图虚高。
func encodePieces(states []int) string {
	if len(states) == 0 {
		return ""
	}
	buf := make([]byte, (len(states)+7)/8)
	for i, s := range states {
		if s == 2 {
			buf[i/8] |= 1 << (7 - uint(i%8))
		}
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// mapState qBittorrent 状态 → Transmission 状态码 + 错误标记
func mapState(state string) (status int64, errCode int64, errText string) {
	switch state {
	case "downloading", "forcedDL", "metaDL", "stalledDL", "allocating":
		return trStatusDownload, 0, ""
	case "uploading", "forcedUP", "stalledUP":
		return trStatusSeeding, 0, ""
	case "queuedDL", "checkingDL":
		if state == "checkingDL" {
			return trStatusChecking, 0, ""
		}
		return trStatusQueuedDL, 0, ""
	case "queuedUP":
		return trStatusQueuedSeed, 0, ""
	case "checkingUP", "checkingResumeData":
		return trStatusChecking, 0, ""
	case "pausedDL", "pausedUP", "stoppedDL", "stoppedUP":
		return trStatusStopped, 0, ""
	case "moving":
		return trStatusQueuedChk, 0, ""
	case "error":
		return trStatusStopped, 3, "qBittorrent 报告该任务出错"
	case "missingFiles":
		return trStatusStopped, 3, "本地文件缺失"
	default:
		return trStatusStopped, 0, ""
	}
}

// siteNames Tracker URL 列表 → 站点名（去协议、去端口、去路径）
func siteNames(urls []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, u := range urls {
		name := siteName(u)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// siteName 单个 Tracker URL → 站点名
func siteName(raw string) string {
	host := hostOf(raw)
	if host == "" {
		return ""
	}
	// 与 Transmission 侧口径一致：按主机名判定站点，
	// 去掉常见二级前缀（www）让同名站点归并
	return strings.TrimPrefix(host, "www.")
}

// hostOf 取 URL 的主机名（失败时返回原串）
func hostOf(raw string) string {
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	return u.Hostname()
}

// sumNonNegative qBittorrent 用 -1 表示「未知」，求和前必须过滤
func sumNonNegative(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

// magnetFromHash 构造磁力链接（qBittorrent 不一定回传 magnet_uri）。
// 优先用 v1 infohash：v2 哈希长度不同，混合种子的 btih 必须是 v1。
func magnetFromHash(hash, v1, v2 string) string {
	h := strings.ToLower(strings.TrimSpace(v1))
	if h == "" {
		h = strings.ToLower(strings.TrimSpace(hash))
	}
	if h == "" || (len(h) != 40 && len(h) != 64) {
		return ""
	}
	return "magnet:?xt=urn:btih:" + h
}
