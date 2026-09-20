package rpc

import (
	"encoding/base64"
	"time"

	"github.com/hekmon/cunits/v2"
	trpc "github.com/hekmon/transmissionrpc/v3"
	"github.com/sushazhi/seedark/backend/internal/models"
)

// 类型别名，使 rpc 包直接使用 models 中的结构
type (
	Torrent = models.Torrent
	Session = models.Session
)

func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// toDurMin 时长转分钟（cunits/transmissionrpc 的 time.Duration 语义）
func toDurMin(p *time.Duration) int64 {
	if p != nil {
		return int64(*p / time.Minute)
	}
	return 0
}

// toDurSec 时长转秒（做种时间等）
func toDurSec(p *time.Duration) int64 {
	if p != nil {
		return int64(*p / time.Second)
	}
	return 0
}

func mapTorrents(list []trpc.Torrent) []*Torrent {
	out := make([]*Torrent, 0, len(list))
	for _, t := range list {
		out = append(out, mapTorrent(t, false))
	}
	return out
}

func mapTorrent(t trpc.Torrent, detail bool) *Torrent {
	toI64 := func(p *int64) int64 {
		if p != nil {
			return *p
		}
		return 0
	}
	toStr := func(p *string) string {
		if p != nil {
			return *p
		}
		return ""
	}
	toF64 := func(p *float64) float64 {
		if p != nil {
			return *p
		}
		return 0
	}
	toBool := func(p *bool) bool {
		if p != nil {
			return *p
		}
		return false
	}
	// cunits 以 bit 存储，需要转回字节（与 Transmission RPC 的 bytes 语义一致）
	toBits := func(p *cunits.Bits) int64 {
		if p != nil {
			return int64(*p) / 8
		}
		return 0
	}
	toStatus := func(p *trpc.TorrentStatus) int64 {
		if p != nil {
			return int64(*p)
		}
		return 0
	}
	toSRM := func(p *trpc.SeedRatioMode) int64 {
		if p != nil {
			return int64(*p)
		}
		return 0
	}
	toUnix := func(p *time.Time) int64 {
		if p != nil {
			return p.Unix()
		}
		return 0
	}

	out := &Torrent{
		ID:                  toI64(t.ID),
		Name:                toStr(t.Name),
		HashString:          toStr(t.HashString),
		Creator:             toStr(t.Creator),
		TotalSize:           toBits(t.TotalSize),
		SizeWhenDone:        toBits(t.SizeWhenDone),
		PercentDone:         toF64(t.PercentDone),
		Status:              toStatus(t.Status),
		RateDownload:        toI64(t.RateDownload),
		RateUpload:          toI64(t.RateUpload),
		ETA:                 toI64(t.ETA),
		UploadedEver:        toI64(t.UploadedEver),
		DownloadedEver:      toI64(t.DownloadedEver),
		UploadRatio:         toF64(t.UploadRatio),
		SecondsSeeding:      toDurSec(t.TimeSeeding),
		Error:               toI64(t.Error),
		ErrorString:         toStr(t.ErrorString),
		Labels:              t.Labels,
		QueuePosition:       toI64(t.QueuePosition),
		PeersConnected:      toI64(t.PeersConnected),
		PeersSendingToUs:    toI64(t.PeersSendingToUs),
		PeersGettingFromUs:  toI64(t.PeersGettingFromUs),
		DownloadDir:         toStr(t.DownloadDir),
		AddedDate:           toUnix(t.AddedDate),
		DoneDate:            toUnix(t.DoneDate),
		ActivityDate:        toUnix(t.ActivityDate),
		IsFinished:          toBool(t.IsFinished),
		IsStalled:           toBool(t.IsStalled),
		IsPrivate:           toBool(t.IsPrivate),
		MagnetLink:          toStr(t.MagnetLink),
		FileCount:           toI64(t.FileCount),
		HaveValid:           toI64(t.HaveValid),
		HaveUnchecked:       toI64(t.HaveUnchecked),
		LeftUntilDone:       toI64(t.LeftUntilDone),
		Comment:             toStr(t.Comment),
		PeerLimit:           toI64(t.PeerLimit),
		SeedIdleLimit:       toDurMin(t.SeedIdleLimit),
		SeedIdleMode:        toI64(t.SeedIdleMode),
		SeedRatioLimit:      toF64(t.SeedRatioLimit),
		SeedRatioMode:       toSRM(t.SeedRatioMode),
		BandwidthPriority:   toI64(t.BandwidthPriority),
		DownloadLimited:     toBool(t.DownloadLimited),
		DownloadLimit:       toI64(t.DownloadLimit),
		UploadLimited:       toBool(t.UploadLimited),
		UploadLimit:         toI64(t.UploadLimit),
		HonorsSessionLimits: toBool(t.HonorsSessionLimits),
	}

	// TrackerStats 列表与详情均返回（列表列显示主 Tracker 主机名）
	out.TrackerStats = make([]models.TrackerStat, 0, len(t.TrackerStats))
	for _, ts := range t.TrackerStats {
		out.TrackerStats = append(out.TrackerStats, models.TrackerStat{
			ID:                    ts.ID,
			Host:                  ts.Host,
			Announce:              ts.Announce,
			AnnounceState:         ts.AnnounceState,
			Tier:                  ts.Tier,
			IsBackup:              ts.IsBackup,
			LastAnnounceResult:    ts.LastAnnounceResult,
			LastAnnounceSucceeded: ts.LastAnnounceSucceeded,
			LastAnnounceTimedOut:  ts.LastAnnounceTimedOut,
			LastAnnounceTime:      ts.LastAnnounceTime.Unix(),
			LastAnnouncePeerCount: ts.LastAnnouncePeerCount,
			NextAnnounceTime:      ts.NextAnnounceTime.Unix(),
			ScrapeState:           ts.ScrapeState,
			LastScrapeResult:      ts.LastScrapeResult,
			LastScrapeSucceeded:   ts.LastScrapeSucceeded,
			LastScrapeTime:        ts.LastScrapeTime.Unix(),
			NextScrapeTime:        ts.NextScrapeTime.Unix(),
			SeederCount:           ts.SeederCount,
			LeecherCount:          ts.LeecherCount,
			DownloadCount:         ts.DownloadCount,
		})
	}

	if detail {
		out.Files = make([]models.FileInfo, 0, len(t.Files))
		for _, f := range t.Files {
			out.Files = append(out.Files, models.FileInfo{
				BytesCompleted: f.BytesCompleted,
				Length:         f.Length,
				Name:           f.Name,
			})
		}
		out.FileStats = make([]models.FileStat, 0, len(t.FileStats))
		for _, fs := range t.FileStats {
			out.FileStats = append(out.FileStats, models.FileStat{
				BytesCompleted: fs.BytesCompleted,
				Wanted:         fs.Wanted,
				Priority:       fs.Priority,
			})
		}
		out.Trackers = make([]models.Tracker, 0, len(t.Trackers))
		for _, tr := range t.Trackers {
			out.Trackers = append(out.Trackers, models.Tracker{
				Announce: tr.Announce,
				ID:       tr.ID,
				Scrape:   tr.Scrape,
				SiteName: tr.SiteName,
				Tier:     tr.Tier,
			})
		}
		out.Peers = make([]models.Peer, 0, len(t.Peers))
		for _, p := range t.Peers {
			out.Peers = append(out.Peers, models.Peer{
				Address:           p.Address,
				ClientName:        p.ClientName,
				FlagStr:           p.FlagStr,
				Progress:          p.Progress,
				RateToClient:      p.RateToClient,
				RateToPeer:        p.RateToPeer,
				IsDownloadingFrom: p.IsDownloadingFrom,
				IsUploadingTo:     p.IsUploadingTo,
				IsEncrypted:       p.IsEncrypted,
				IsIncoming:        p.IsIncoming,
				IsUTP:             p.IsUTP,
				Port:              p.Port,
			})
		}
	}
	return out
}

func mapSession(s trpc.SessionArguments) *Session {
	toI64 := func(p *int64) int64 {
		if p != nil {
			return *p
		}
		return 0
	}
	toStr := func(p *string) string {
		if p != nil {
			return *p
		}
		return ""
	}
	toF64 := func(p *float64) float64 {
		if p != nil {
			return *p
		}
		return 0
	}
	toBool := func(p *bool) bool {
		if p != nil {
			return *p
		}
		return false
	}
	toStrP := func(p *trpc.Encryption) string {
		if p != nil {
			return string(*p)
		}
		return ""
	}

	return &Session{
		Version:                          toStr(s.Version),
		RPCVersion:                       toI64(s.RPCVersion),
		DownloadDir:                      toStr(s.DownloadDir),
		SpeedLimitDown:                   toI64(s.SpeedLimitDown),
		SpeedLimitDownOn:                 toBool(s.SpeedLimitDownEnabled),
		SpeedLimitUp:                     toI64(s.SpeedLimitUp),
		SpeedLimitUpOn:                   toBool(s.SpeedLimitUpEnabled),
		AltSpeedDown:                     toI64(s.AltSpeedDown),
		AltSpeedUp:                       toI64(s.AltSpeedUp),
		AltSpeedEnabled:                  toBool(s.AltSpeedEnabled),
		PeerLimitGlobal:                  toI64(s.PeerLimitGlobal),
		PeerPort:                         toI64(s.PeerPort),
		PeerPortRandomOnStart:            toBool(s.PeerPortRandomOnStart),
		PEXEnabled:                       toBool(s.PEXEnabled),
		DHTEnabled:                       toBool(s.DHTEnabled),
		LPDEnabled:                       toBool(s.LPDEnabled),
		UTPEnabled:                       toBool(s.UTPEnabled),
		Encryption:                       toStrP(s.Encryption),
		SeedRatioLimit:                   toF64(s.SeedRatioLimit),
		StartAdded:                       toBool(s.StartAddedTorrents),
		IncompleteDir:                    toStr(s.IncompleteDir),
		DownloadQueueSize:                toI64(s.DownloadQueueSize),
		DownloadQueueEnabled:             toBool(s.DownloadQueueEnabled),
		SeedQueueSize:                    toI64(s.SeedQueueSize),
		SeedQueueEnabled:                 toBool(s.SeedQueueEnabled),
		QueueStalledEnabled:              toBool(s.QueueStalledEnabled),
		QueueStalledMinutes:              toI64(s.QueueStalledMinutes),
		BlocklistEnabled:                 toBool(s.BlocklistEnabled),
		BlocklistURL:                     toStr(s.BlocklistURL),
		BlocklistSize:                    toI64(s.BlocklistSize),
		PortForwardingEnabled:            toBool(s.PortForwardingEnabled),
		IncompleteDirEnabled:             toBool(s.IncompleteDirEnabled),
		CacheSizeMB:                      toI64(s.CacheSizeMB),
		AltSpeedTimeEnabled:              toBool(s.AltSpeedTimeEnabled),
		AltSpeedTimeBegin:                toI64(s.AltSpeedTimeBegin),
		AltSpeedTimeEnd:                  toI64(s.AltSpeedTimeEnd),
		AltSpeedTimeDay:                  toI64(s.AltSpeedTimeDay),
		ScriptTorrentAddedEnabled:        toBool(s.ScriptTorrentAddedEnabled),
		ScriptTorrentAddedFilename:       toStr(s.ScriptTorrentAddedFilename),
		ScriptTorrentDoneEnabled:         toBool(s.ScriptTorrentDoneEnabled),
		ScriptTorrentDoneFilename:        toStr(s.ScriptTorrentDoneFilename),
		ScriptTorrentDoneSeedingEnabled:  toBool(s.ScriptTorrentDoneSeedingEnabled),
		ScriptTorrentDoneSeedingFilename: toStr(s.ScriptTorrentDoneSeedingFilename),
		DefaultTrackers:                  s.DefaultTrackers,
		RenamePartialFiles:               toBool(s.RenamePartialFiles),
		TrashOriginalTorrentFiles:        toBool(s.TrashOriginalTorrentFiles),
		IdleSeedingLimitEnabled:          toBool(s.IdleSeedingLimitEnabled),
		IdleSeedingLimit:                 toI64(s.IdleSeedingLimit),
	}
}
