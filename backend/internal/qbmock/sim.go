package qbmock

import (
	"time"
)

// 模拟节奏（秒）：磁力取元数据、本地校验各自耗时
const (
	magnetMetaWait = 5
	checkSeconds   = 12
)

// Step 推进一次模拟，由 cmd/qbmock 的 ticker 周期调用。
// 覆盖真实运行会看到的变化：速率抖动、进度增长、下载完成转做种、
// 磁力元数据到达、校验进度推进、停滞/排队偶尔复活。
// 单元测试直接 New 出的实例不调用它，因此结果保持确定。
func (s *Server) Step(dt time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sec := int64(dt / time.Second)
	if sec < 1 {
		sec = 1
	}
	now := time.Now().Unix()
	var downBytes, upBytes int64

	for _, t := range s.torrents {
		switch {
		case t.State == "metaDL":
			t.metaAge += sec
			if t.metaAge >= magnetMetaWait {
				s.buildMagnetMetadata(t)
			}
		case t.State == "checkingDL" || t.State == "checkingUP":
			s.stepCheck(t, sec, now)
		case isLiveDownload(t.State):
			downBytes += s.stepDownload(t, sec, now)
		case t.State == "uploading" || t.State == "forcedUP":
			upBytes += s.stepUpload(t, sec, now)
		case t.State == "stalledDL" || t.State == "queuedDL" ||
			t.State == "stalledUP" || t.State == "queuedUP":
			s.stepIdleSwarm(t, sec, now)
		default:
			// 停止 / 报错：qBittorrent 仍是 0 速率，ETA 未知
			t.DLSpeed, t.ULSpeed = 0, 0
			t.ETA = etaUnknown
		}
	}

	s.sessionDL += downBytes
	s.sessionUL += upBytes
	s.alltimeDL += downBytes
	s.alltimeUL += upBytes
	s.totalSessionTime += sec
}

// stepDownload 下载中：抖动速率、推进进度、完成后转做种
func (s *Server) stepDownload(t *torrent, sec, now int64) int64 {
	rate := s.clampDownLocked(t)
	t.DLSpeed = rate
	t.ULSpeed = s.clampUpLocked(t, jitter(s.rng, t.baseUp/4))
	added := rate * sec
	if t.Size > 0 {
		t.Downloaded += added
		if t.Downloaded > t.Size {
			t.Downloaded = t.Size
		}
		t.Progress = clamp01(float64(t.Downloaded) / float64(t.Size))
	}
	t.DownloadedSession += added
	t.TimeActive += sec
	t.LastActivity = now
	refreshPieces(t)
	left := t.Size - t.Downloaded
	if rate > 0 && left > 0 {
		t.ETA = left / rate
	} else {
		t.ETA = etaUnknown
	}
	if t.Availability >= 0 && t.Availability < 1 {
		t.Availability = clampFloat(t.Availability+0.02, 0, 1)
	}
	s.churnSwarm(t, true)
	if t.Progress >= 1 {
		s.completeTorrent(t, now)
	}
	return added
}

// stepUpload 做种中：只有上传速率，分享率与做种时长随之增长
func (s *Server) stepUpload(t *torrent, sec, now int64) int64 {
	rate := s.clampUpLocked(t, jitter(s.rng, t.baseUp))
	t.ULSpeed = rate
	t.DLSpeed = 0
	t.Uploaded += rate * sec
	t.UploadedSession += rate * sec
	t.SeedingTime += sec
	t.TimeActive += sec
	t.LastActivity = now
	t.ETA = etaUnknown
	if t.Size > 0 {
		t.Ratio = float64(t.Uploaded) / float64(t.Size)
	}
	s.churnSwarm(t, false)
	return rate * sec
}

// completeTorrent 下载完成：转做种态并补齐完成时间（面板按此判定 IsFinished）
func (s *Server) completeTorrent(t *torrent, now int64) {
	t.Progress = 1
	t.Downloaded = t.Size
	t.CompletionOn = now
	t.State = "uploading"
	t.ForceStart = false
	t.DLSpeed = 0
	t.ULSpeed = s.clampUpLocked(t, jitter(s.rng, t.baseUp))
	t.ETA = etaUnknown
	refreshPieces(t)
}

// stepCheck 校验中：真实 qBittorrent 把 progress 报成校验进度，
// 校验结束后还原为真实完成度
func (s *Server) stepCheck(t *torrent, sec, now int64) {
	t.checkProgress += float64(sec) / float64(checkSeconds)
	t.Progress = clamp01(t.checkProgress)
	t.LastActivity = now
	t.TimeActive += sec
	if t.checkProgress < 1 {
		return
	}
	t.checkProgress = 0
	t.Progress = clamp01(t.trueProgress)
	if t.Progress >= 1 {
		t.State = "uploading"
	} else {
		t.State = "downloading"
	}
	refreshPieces(t)
}

// stepIdleSwarm 停滞 / 排队：速率保持 0，偶尔有可用槽位或对端出现后恢复活动
func (s *Server) stepIdleSwarm(t *torrent, sec, now int64) {
	t.DLSpeed, t.ULSpeed = 0, 0
	t.ETA = etaUnknown
	if s.rng.Float64() > 0.08 {
		return
	}
	switch t.State {
	case "stalledDL", "queuedDL":
		t.State = "downloading"
		t.setSwarm(s.rng, 2, 8, 1, 6)
		t.Peers = genPeers(s.rng, t, now)
	case "stalledUP", "queuedUP":
		t.State = "uploading"
		t.setSwarm(s.rng, 0, 0, 2, 8)
		t.Peers = genPeers(s.rng, t, now)
	}
	_ = sec
}

// churnSwarm 活动种子的对端数量小幅波动，让 Peers 页与连接数列不至于静止
func (s *Server) churnSwarm(t *torrent, downloading bool) {
	if s.rng.Float64() > 0.3 {
		return
	}
	if downloading {
		t.setSwarm(s.rng, 1, 10, 0, 8)
	} else {
		t.setSwarm(s.rng, 0, 0, 1, 10)
	}
	if t.Size > 0 {
		t.Peers = genPeers(s.rng, t, time.Now().Unix())
	}
}

// clampDownLocked 单种限速优先，其次全局限速（qBittorrent：0 表示不限制）
func (s *Server) clampDownLocked(t *torrent) int64 {
	return s.clampRate(t.DLLimit, "dl_limit", jitter(s.rng, t.baseDown))
}

func (s *Server) clampUpLocked(t *torrent, v int64) int64 {
	return s.clampRate(t.ULLimit, "up_limit", v)
}

func (s *Server) clampRate(limit int64, globalKey string, v int64) int64 {
	if limit > 0 && v > limit {
		v = limit
	}
	if g := prefInt64(s.prefs, globalKey); g > 0 && v > g {
		v = g
	}
	return v
}

// rateSumLocked 全局速率（transfer/info 与 maindata 的 dl/up_info_speed）。需持有 s.mu
func (s *Server) rateSumLocked(down bool) int64 {
	var sum int64
	for _, t := range s.torrents {
		if down {
			sum += t.DLSpeed
		} else {
			sum += t.ULSpeed
		}
	}
	return sum
}

// refreshPieces 按当前进度重算块状态与各文件进度（不改名称、尺寸）
func refreshPieces(t *torrent) {
	if len(t.Pieces) == 0 {
		return
	}
	done := int(float64(len(t.Pieces)) * clamp01(t.Progress))
	if done > len(t.Pieces) {
		done = len(t.Pieces)
	}
	for i := range t.Pieces {
		switch {
		case i < done:
			t.Pieces[i] = 2
		case i == done && isLiveDownload(t.State):
			t.Pieces[i] = 1
		default:
			t.Pieces[i] = 0
		}
	}
	t.pieceDone = done
	for i := range t.Files {
		f := &t.Files[i]
		span := f.PieceEnd - f.PieceStart + 1
		if span <= 0 {
			f.Progress = clamp01(t.Progress)
			continue
		}
		f.Progress = clamp01(float64(int64(done)-f.PieceStart) / float64(span))
	}
}
