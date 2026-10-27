package traffic

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sushazhi/seedark/backend/internal/models"
)

// 桶粒度与保留期。两级桶是刻意的：24 小时看细节用 5 分钟桶，
// 7/30 天看趋势用小时桶，文件大小只跟保留期有关、与采样频率无关
// （小时桶 90 天也就 2160 条），不必做更复杂的降采样。
const (
	Bucket5m = 300
	Bucket1h = 3600

	keep5m = 7 * 24 * time.Hour
	keep1h = 90 * 24 * time.Hour

	// 计数器基线只对「最近见过」的种子保留：消失很久的种子回来时直接重新起
	// 基线（记 0），而不是把期间的累计量当成一次性流量
	baseKeep = 48 * time.Hour

	// 写盘节流：采样一分钟一次，每次都写盘没必要
	saveEvery = 5 * time.Minute
)

// bucket 一个时间桶内的上下行字节数
type bucket struct {
	Down int64 `json:"down"`
	Up   int64 `json:"up"`
}

// baseline 某颗种子上次见到的计数器读数
type baseline struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
	At   int64 `json:"at"`
}

// file 流量历史的落盘结构（独立文件，不挤进 sa-state.json：
// 它是高频追加的时间序列，与配置类状态的写入节奏完全不同）
type file struct {
	Bucket5m  map[int64]bucket    `json:"bucket5m"`
	Bucket1h  map[int64]bucket    `json:"bucket1h"`
	Baselines map[string]baseline `json:"baselines"`
	StartedAt int64               `json:"startedAt"`
	UpdatedAt int64               `json:"updatedAt"`
}

type store struct {
	mu       sync.Mutex
	path     string
	f        *file
	lastSave time.Time
	dirty    bool
}

func emptyFile() *file {
	return &file{
		Bucket5m:  map[int64]bucket{},
		Bucket1h:  map[int64]bucket{},
		Baselines: map[string]baseline{},
	}
}

func newStore(dataDir string) (*store, error) {
	dir := filepath.Join(dataDir, "traffic")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	st := &store{path: filepath.Join(dir, "history.json"), f: emptyFile()}

	buf, err := os.ReadFile(st.path)
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return nil, err
	}
	// 解析失败不让整个服务起不来：从空历史重新开始，日志里留痕。
	// 这份文件是统计数据，丢了最多是曲线短一截，不值得让面板打不开
	if err := json.Unmarshal(buf, st.f); err != nil {
		slog.Warn("流量历史文件解析失败，已从空历史重新开始", "path", st.path, "err", err)
		st.f = emptyFile()
		return st, nil
	}
	// 旧文件/异常文件可能缺 map，补齐后再用
	if st.f.Bucket5m == nil {
		st.f.Bucket5m = map[int64]bucket{}
	}
	if st.f.Bucket1h == nil {
		st.f.Bucket1h = map[int64]bucket{}
	}
	if st.f.Baselines == nil {
		st.f.Baselines = map[string]baseline{}
	}
	return st, nil
}

// record 把本次采样相对上次的增量累加进当前时间桶。
// 首见（新种子）与计数器回退（种子被重加、换过下载器）都只重置基线不计增量：
// 否则一次重加就会把几百 GB 的累计量记成「这一分钟传了这么多」。
func (s *store) record(now time.Time, torrents []*models.Torrent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var dDown, dUp int64
	for _, t := range torrents {
		if t == nil || t.HashString == "" {
			continue
		}
		if prev, seen := s.f.Baselines[t.HashString]; seen &&
			t.UploadedEver >= prev.Up && t.DownloadedEver >= prev.Down {
			dUp += t.UploadedEver - prev.Up
			dDown += t.DownloadedEver - prev.Down
		} else if seen {
			// 计数器回退：换下载器、种子被重加。只重置基线，不补一个负数
			slog.Debug("上下行计数器回退，已重置基线", "hash", t.HashString)
		}
		s.f.Baselines[t.HashString] = baseline{Up: t.UploadedEver, Down: t.DownloadedEver, At: now.Unix()}
	}

	if s.f.StartedAt == 0 {
		// 第一次采样就记下起点：即使这一分钟没有流量，覆盖范围也是从此刻算
		s.f.StartedAt = now.Unix()
	}
	if dUp != 0 || dDown != 0 {
		key5m := now.Unix() - now.Unix()%Bucket5m
		key1h := now.Unix() - now.Unix()%Bucket1h
		b5 := s.f.Bucket5m[key5m]
		b5.Down += dDown
		b5.Up += dUp
		s.f.Bucket5m[key5m] = b5
		b1h := s.f.Bucket1h[key1h]
		b1h.Down += dDown
		b1h.Up += dUp
		s.f.Bucket1h[key1h] = b1h
	}
	s.f.UpdatedAt = now.Unix()
	s.dirty = true
	s.pruneLocked(now)
	if now.Sub(s.lastSave) >= saveEvery {
		s.saveLocked()
	}
}

// pruneLocked 丢掉超出保留期的桶与长期没见过的基线
func (s *store) pruneLocked(now time.Time) {
	cut5m := now.Add(-keep5m).Unix()
	for k := range s.f.Bucket5m {
		if k < cut5m {
			delete(s.f.Bucket5m, k)
		}
	}
	cut1h := now.Add(-keep1h).Unix()
	for k := range s.f.Bucket1h {
		if k < cut1h {
			delete(s.f.Bucket1h, k)
		}
	}
	cutBase := now.Add(-baseKeep).Unix()
	for k, v := range s.f.Baselines {
		if v.At < cutBase {
			delete(s.f.Baselines, k)
		}
	}
}

func (s *store) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return
	}
	s.saveLocked()
}

// saveLocked 原子写：临时文件 + rename，权限 0600（调用方已持锁）
func (s *store) saveLocked() {
	buf, err := json.Marshal(s.f)
	if err != nil {
		slog.Warn("流量历史序列化失败", "err", err)
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		slog.Warn("流量历史写入失败", "path", tmp, "err", err)
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		slog.Warn("流量历史落盘失败", "path", s.path, "err", err)
		return
	}
	s.lastSave = time.Now()
	s.dirty = false
}

// history 按范围组装连续的点列。桶缺失补 0，前端不用自己补空档
func (s *store) history(rng string, now time.Time) (Snapshot, error) {
	sp, ok := ranges[rng]
	if !ok {
		return Snapshot{}, ErrBadRange(rng)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	end := now.Unix() - now.Unix()%int64(sp.bucket)
	start := end - int64(sp.count-1)*int64(sp.bucket)
	src := s.f.Bucket5m
	if sp.bucket == Bucket1h {
		src = s.f.Bucket1h
	}

	pts := make([]Point, 0, sp.count)
	var totals, peak Totals
	sampled := 0
	for i := 0; i < sp.count; i++ {
		t := start + int64(i)*int64(sp.bucket)
		b := src[t]
		pts = append(pts, Point{T: t, Down: b.Down, Up: b.Up})
		totals.Down += b.Down
		totals.Up += b.Up
		if b.Down > peak.Down {
			peak.Down = b.Down
		}
		if b.Up > peak.Up {
			peak.Up = b.Up
		}
		if b.Down != 0 || b.Up != 0 {
			sampled++
		}
	}
	return Snapshot{
		Range:         sp.name,
		BucketSeconds: sp.bucket,
		Start:         start,
		End:           end,
		Points:        pts,
		Totals:        totals,
		Peak:          peak,
		Coverage:      Coverage{Since: s.f.StartedAt, Samples: sampled},
	}, nil
}
