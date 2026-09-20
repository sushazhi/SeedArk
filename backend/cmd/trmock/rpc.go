package main

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// defaultListFields 客户端未显式给出 fields 时的兜底（真实调用总会带 fields）。
var defaultListFields = []string{
	"id", "name", "hashString", "totalSize", "percentDone", "status",
	"rateDownload", "rateUpload", "eta", "error", "errorString", "labels",
	"downloadDir", "addedDate", "uploadRatio", "queuePosition",
}

// torrentWire 按请求的 fields 逐项构造 wire map。
// 键名沿用客户端请求的原始拼写，值由 field() 解析，确保与
// hekmon/transmissionrpc 的 json tag 完全对齐。
func torrentWire(t *mockTorrent, fields []string) map[string]any {
	if len(fields) == 0 {
		fields = defaultListFields
	}
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		if v, ok := t.field(f); ok {
			out[f] = v
		}
	}
	// id 恒返回，便于客户端建立映射
	if _, ok := out["id"]; !ok {
		out["id"] = t.ID
	}
	return out
}

// field 把请求字段名（不区分大小写/连字符别名）映射到内部值。
// 尺寸返回字节、日期返回 unix 秒、时长返回相应单位，均为纯数字，
// 与 Transmission RPC wire 语义一致。
func (t *mockTorrent) field(raw string) (any, bool) {
	switch normalizeField(raw) {
	case "id":
		return t.ID, true
	case "name":
		return t.Name, true
	case "hashstring":
		return t.HashString, true
	case "creator":
		return t.Creator, true
	case "comment":
		return t.Comment, true
	case "totalsize":
		return t.TotalSize, true
	case "sizewhendone":
		return t.sizeWhenDone(), true
	case "percentdone", "percentcomplete":
		return t.PercentDone, true
	case "status":
		return t.Status, true
	case "ratedownload":
		return t.RateDownload, true
	case "rateupload":
		return t.RateUpload, true
	case "eta":
		return t.ETA, true
	case "uploadedever":
		return t.UploadedEver, true
	case "downloadedever":
		return t.DownloadedEver, true
	case "uploadratio":
		return t.UploadRatio, true
	case "secondsseeding":
		return t.SecondsSeeding, true
	case "secondsdownloading":
		return t.SecondsDownloading, true
	case "error":
		return t.Error, true
	case "errorstring":
		return t.ErrorString, true
	case "labels":
		return nonNilStrings(t.Labels), true
	case "queueposition":
		return t.QueuePosition, true
	case "peersconnected":
		return t.PeersConnected, true
	case "peerssendingtous":
		return t.PeersSendingToUs, true
	case "peersgettingfromus":
		return t.PeersGettingFromUs, true
	case "downloaddir":
		return t.DownloadDir, true
	case "addeddate":
		return t.AddedDate, true
	case "donedate":
		return t.DoneDate, true
	case "activitydate":
		return t.ActivityDate, true
	case "startdate":
		return t.StartDate, true
	case "isfinished":
		return t.IsFinished, true
	case "isstalled":
		return t.IsStalled, true
	case "isprivate":
		return t.IsPrivate, true
	case "magnetlink":
		return t.MagnetLink, true
	case "file-count":
		return t.FileCount, true
	case "havevalid":
		return t.HaveValid, true
	case "haveunchecked":
		return t.HaveUnchecked, true
	case "leftuntildone":
		return t.leftUntilDone(), true
	case "peer-limit":
		return t.PeerLimit, true
	case "seedidlelimit":
		return t.SeedIdleLimit, true
	case "seedidlemode":
		return t.SeedIdleMode, true
	case "seedratiolimit":
		return t.SeedRatioLimit, true
	case "seedratiomode":
		return t.SeedRatioMode, true
	case "bandwidthpriority":
		return t.BandwidthPriority, true
	case "downloadlimited":
		return t.DownloadLimited, true
	case "downloadlimit":
		return t.DownloadLimit, true
	case "uploadlimited":
		return t.UploadLimited, true
	case "uploadlimit":
		return t.UploadLimit, true
	case "honorsessionlimits":
		return t.HonorsSessionLimits, true
	case "sequentialdownload":
		return t.Sequential, true
	case "recheckprogress":
		return t.RecheckProgress, true
	case "files":
		return filesWire(t), true
	case "filestats":
		return fileStatsWire(t), true
	case "trackers":
		return trackersWire(t), true
	case "trackerstats":
		return trackerStatsWire(t), true
	case "peers":
		return peersWire(t), true
	case "pieces":
		return piecesBase64(t), true
	case "piececount":
		return t.PieceCount, true
	case "piecesize":
		return t.PieceSize, true
	case "maxconnectedpeers":
		return t.PeerLimit, true
	case "metadatapercentcomplete":
		return 1.0, true
	case "webseeds":
		return []string{}, true
	default:
		return nil, false
	}
}

// normalizeField 统一字段名：小写、去连字符；保留 file-count / peer-limit 这类
// 特殊键的识别（先小写再按原样比较即可命中上面的 case）。
func normalizeField(f string) string {
	return strings.ToLower(strings.TrimSpace(f))
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// ---- 详情子结构 wire ----

func filesWire(t *mockTorrent) []map[string]any {
	out := make([]map[string]any, 0, len(t.Files))
	for _, f := range t.Files {
		out = append(out, map[string]any{
			"bytesCompleted": f.BytesCompleted,
			"length":         f.Length,
			"name":           f.Name,
		})
	}
	return out
}

func fileStatsWire(t *mockTorrent) []map[string]any {
	out := make([]map[string]any, 0, len(t.FileStats))
	for _, fs := range t.FileStats {
		out = append(out, map[string]any{
			"bytesCompleted": fs.BytesCompleted,
			"wanted":         fs.Wanted,
			"priority":       fs.Priority,
		})
	}
	return out
}

func trackersWire(t *mockTorrent) []map[string]any {
	out := make([]map[string]any, 0, len(t.Trackers))
	for _, tr := range t.Trackers {
		out = append(out, map[string]any{
			"announce": tr.Announce,
			"id":       tr.ID,
			"scrape":   tr.Scrape,
			"sitename": tr.SiteName,
			"tier":     tr.Tier,
		})
	}
	return out
}

func trackerStatsWire(t *mockTorrent) []map[string]any {
	out := make([]map[string]any, 0, len(t.TrackerStats))
	for _, ts := range t.TrackerStats {
		out = append(out, map[string]any{
			"id":                    ts.ID,
			"announce":              ts.Announce,
			"scrape":                strings.Replace(ts.Announce, "announce", "scrape", 1),
			"host":                  ts.Host,
			"sitename":              ts.Host,
			"tier":                  ts.Tier,
			"announceState":         ts.AnnounceState,
			"isBackup":              ts.IsBackup,
			"lastAnnounceResult":    ts.LastAnnounceResult,
			"lastAnnounceSucceeded": ts.LastAnnounceSucceeded,
			"lastAnnounceTimedOut":  ts.LastAnnounceTimedOut,
			"lastAnnounceTime":      ts.LastAnnounceTime,
			"lastAnnounceStartTime": ts.LastAnnounceTime,
			"lastAnnouncePeerCount": ts.LastAnnouncePeerCount,
			"nextAnnounceTime":      ts.NextAnnounceTime,
			"scrapeState":           ts.ScrapeState,
			"lastScrapeResult":      ts.LastScrapeResult,
			"lastScrapeSucceeded":   ts.LastScrapeSucceeded,
			"lastScrapeTime":        ts.LastScrapeTime,
			"lastScrapeStartTime":   ts.LastScrapeTime,
			"lastScrapeTimedOut":    false,
			"nextScrapeTime":        ts.NextScrapeTime,
			"seederCount":           ts.SeederCount,
			"leecherCount":          ts.LeecherCount,
			"downloadCount":         ts.DownloadCount,
			"hasAnnounced":          ts.LastAnnounceSucceeded,
			"hasScraped":            ts.LastScrapeSucceeded,
		})
	}
	return out
}

func peersWire(t *mockTorrent) []map[string]any {
	out := make([]map[string]any, 0, len(t.Peers))
	for _, p := range t.Peers {
		out = append(out, map[string]any{
			"address":            p.Address,
			"clientName":         p.ClientName,
			"flagStr":            p.FlagStr,
			"isDownloadingFrom":  p.IsDownloadingFrom,
			"isEncrypted":        p.IsEncrypted,
			"isIncoming":         p.IsIncoming,
			"isUploadingTo":      p.IsUploadingTo,
			"isUTP":              p.IsUTP,
			"port":               p.Port,
			"progress":           p.Progress,
			"rateToClient":       p.RateToClient,
			"rateToPeer":         p.RateToPeer,
			"clientIsChoked":     false,
			"clientIsInterested": true,
			"peerIsChoked":       false,
			"peerIsInterested":   true,
		})
	}
	return out
}

// piecesBase64 按当前进度生成块位图的 base64（前 percentDone 的块置 1）。
func piecesBase64(t *mockTorrent) string {
	if t.PieceCount <= 0 {
		return ""
	}
	n := (t.PieceCount + 7) / 8
	buf := make([]byte, n)
	done := int64(t.PercentDone * float64(t.PieceCount))
	for i := int64(0); i < done && i < t.PieceCount; i++ {
		buf[i/8] |= 1 << (7 - uint(i%8))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// ---- 会话 / 统计 / 其它 ----

// sessionWire 输出 session-get 的连字符键名（与库 SessionArguments 对齐）。
// extra 为 session-set 以来的运行时覆盖；default-trackers 在 set/get 两侧
// 都是换行拼接的字符串（hekmon 库与真实 Transmission 均如此），此处归一。
func sessionWire(s *mockSession, extra map[string]any, sessionID string) map[string]any {
	wire := map[string]any{
		"version":                      "4.0.5 (trmock)",
		"rpc-version":                  17,
		"rpc-version-semver":           "5.3.0",
		"rpc-version-minimum":          14,
		"session-id":                   sessionID,
		"config-dir":                   "/config/transmission",
		"download-dir":                 s.DownloadDir,
		"incomplete-dir":               s.DownloadDir + "/incomplete",
		"incomplete-dir-enabled":       false,
		"speed-limit-down":             s.SpeedLimitDown,
		"speed-limit-down-enabled":     s.SpeedLimitDownOn,
		"speed-limit-up":               s.SpeedLimitUp,
		"speed-limit-up-enabled":       s.SpeedLimitUpOn,
		"alt-speed-down":               s.AltSpeedDown,
		"alt-speed-up":                 s.AltSpeedUp,
		"alt-speed-enabled":            s.AltSpeedEnabled,
		"alt-speed-time-enabled":       false,
		"alt-speed-time-begin":         540,
		"alt-speed-time-end":           1020,
		"alt-speed-time-day":           127,
		"peer-limit-global":            s.PeerLimitGlobal,
		"peer-limit-per-torrent":       60,
		"peer-port":                    s.PeerPort,
		"peer-port-random-on-start":    false,
		"pex-enabled":                  s.PEXEnabled,
		"dht-enabled":                  s.DHTEnabled,
		"lpd-enabled":                  s.LPDEnabled,
		"utp-enabled":                  s.UTPEnabled,
		"encryption":                   s.Encryption,
		"seedRatioLimit":               s.SeedRatioLimit,
		"seedRatioLimited":             true,
		"start-added-torrents":         s.StartAdded,
		"trash-original-torrent-files": false,
		"download-queue-enabled":       s.DownloadQueueEnabled,
		"download-queue-size":          s.DownloadQueueSize,
		"seed-queue-enabled":           s.SeedQueueEnabled,
		"seed-queue-size":              s.SeedQueueSize,
		"queue-stalled-enabled":        true,
		"queue-stalled-minutes":        30,
		"blocklist-enabled":            s.BlocklistEnabled,
		"blocklist-url":                "http://www.example.com/blocklist",
		"blocklist-size":               s.BlocklistSize,
		"port-forwarding-enabled":      true,
		"cache-size-mb":                s.CacheSizeMB,
		"rename-partial-files":         true,
		"idle-seeding-limit-enabled":   false,
		"idle-seeding-limit":           30,
		"default-trackers":             "",
		"units": map[string]any{
			"speed-units":  []string{"kB/s", "MB/s", "GB/s", "TB/s"},
			"speed-bytes":  1000,
			"size-units":   []string{"kB", "MB", "GB", "TB"},
			"size-bytes":   1000,
			"memory-units": []string{"KiB", "MiB", "GiB", "TiB"},
			"memory-bytes": 1024,
		},
	}
	// 叠加 session-set 的运行时变更
	for k, v := range extra {
		if _, ok := wire[k]; !ok {
			continue
		}
		if k == "default-trackers" {
			if arr, ok := v.([]any); ok {
				v = strings.Join(toStringSlice(arr), "\n")
			}
		}
		wire[k] = v
	}
	return wire
}

// statsWire 实时计算 session-stats。
func (s *Store) statsWire() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	var active, paused, down, up int64
	for _, t := range s.torrents {
		switch t.Status {
		case stDownload, stSeed, stCheck:
			active++
		case stStopped:
			paused++
		}
		down += t.RateDownload
		up += t.RateUpload
	}
	details := func(d statsDetails) map[string]any {
		return map[string]any{
			"downloadedBytes": d.DownloadedBytes,
			"uploadedBytes":   d.UploadedBytes,
			"filesAdded":      d.FilesAdded,
			"secondsActive":   d.SecondsActive,
			"sessionCount":    d.SessionCount,
		}
	}
	return map[string]any{
		"activeTorrentCount": active,
		"downloadSpeed":      down,
		"pausedTorrentCount": paused,
		"torrentCount":       int64(len(s.torrents)),
		"uploadSpeed":        up,
		"cumulative-stats":   details(s.cumulative),
		"current-stats":      details(s.current),
	}
}

// freeSpaceWire 返回一个稳定的假可用空间（800GB 总量、按路径派生余量）。
func freeSpaceWire(path string) map[string]any {
	const total int64 = 800 * 1024 * 1024 * 1024
	free := total * 62 / 100
	return map[string]any{
		"path":       path,
		"size-bytes": free,
		"total_size": total,
	}
}

func fmtIDs(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("%d", id)
	}
	return strings.Join(parts, ",")
}
