package traffic

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/sushazhi/seedark/backend/internal/models"
)

// fakeSource 固定快照来源：测试里换掉它就能模拟「种子增减、计数器回退」
type fakeSource struct {
	torrents []*models.Torrent
	err      error
}

func (f *fakeSource) GetTorrents(context.Context) ([]*models.Torrent, error) {
	return f.torrents, f.err
}

func torrent(hash string, up, down int64) *models.Torrent {
	return &models.Torrent{HashString: hash, UploadedEver: up, DownloadedEver: down}
}

func newTestService(t *testing.T) (*Service, *fakeSource) {
	t.Helper()
	src := &fakeSource{}
	svc, err := New(src, t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc, src
}

// 两次采样之间计数器增长的差值要落进同一个 5 分钟桶
func TestRecordAccumulatesDeltas(t *testing.T) {
	svc, src := newTestService(t)
	base := time.Date(2026, 10, 27, 12, 0, 0, 0, time.Local)

	src.torrents = []*models.Torrent{torrent("a", 1000, 2000)}
	svc.local.record(base, src.torrents)
	// 第一次采样只立基线，不能把累计量算成这一分钟的流量
	if got := svc.local.f.Bucket5m[base.Unix()-base.Unix()%Bucket5m]; got.Up != 0 || got.Down != 0 {
		t.Fatalf("首次采样不应计入增量，得到 %+v", got)
	}

	later := base.Add(60 * time.Second)
	src.torrents = []*models.Torrent{torrent("a", 1500, 2400)}
	svc.local.record(later, src.torrents)

	key := later.Unix() - later.Unix()%Bucket5m
	got := svc.local.f.Bucket5m[key]
	if got.Up != 500 || got.Down != 400 {
		t.Fatalf("增量应为 up=500 down=400，得到 %+v", got)
	}
	// 同一时刻的小时桶也要有同样的量
	if h := svc.local.f.Bucket1h[later.Unix()-later.Unix()%Bucket1h]; h.Up != 500 || h.Down != 400 {
		t.Fatalf("小时桶应为 up=500 down=400，得到 %+v", h)
	}
}

// 新加的种子首见即基线：不该把它已有的累计量当成一次流量尖峰
func TestNewTorrentDoesNotSpike(t *testing.T) {
	svc, _ := newTestService(t)
	now := time.Now()
	svc.local.record(now, []*models.Torrent{torrent("a", 100, 100)})
	svc.local.record(now.Add(60*time.Second), []*models.Torrent{torrent("a", 200, 100), torrent("b", 900_000, 500_000)})

	key := now.Add(60 * time.Second).Unix()
	key -= key % Bucket5m
	got := svc.local.f.Bucket5m[key]
	if got.Up != 100 || got.Down != 0 {
		t.Fatalf("新种子应只贡献 0，得到 up=%d down=%d", got.Up, got.Down)
	}
}

// 种子被删（计数器消失）不应记成负数；计数器回退同理
func TestCounterRollbackIgnored(t *testing.T) {
	svc, _ := newTestService(t)
	now := time.Now()
	svc.local.record(now, []*models.Torrent{torrent("a", 5000, 5000)})
	// 回退：重新添加同一颗种子，计数器从 0 开始
	svc.local.record(now.Add(60*time.Second), []*models.Torrent{torrent("a", 10, 20)})

	key := now.Add(60 * time.Second).Unix()
	key -= key % Bucket5m
	if got := svc.local.f.Bucket5m[key]; got.Up != 0 || got.Down != 0 {
		t.Fatalf("计数器回退不应计入流量，得到 %+v", got)
	}
	// 基线要跟着回退，否则下一次正常增长会被当成负数丢掉
	svc.local.record(now.Add(120*time.Second), []*models.Torrent{torrent("a", 60, 70)})
	key2 := now.Add(120 * time.Second).Unix()
	key2 -= key2 % Bucket5m
	if got := svc.local.f.Bucket5m[key2]; got.Up != 50 || got.Down != 50 {
		t.Fatalf("回退后应继续正常累计，得到 %+v", got)
	}
}

// 点列必须连续覆盖整段范围、桶数固定，前端才能等距画线
func TestHistoryIsContinuous(t *testing.T) {
	svc, _ := newTestService(t)
	now := time.Now()
	svc.local.record(now, []*models.Torrent{torrent("a", 0, 0)})
	svc.local.record(now.Add(time.Minute), []*models.Torrent{torrent("a", 1234, 4321)})

	snap, err := svc.History("24h")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(snap.Points) != range24h.count {
		t.Fatalf("24h 应为 %d 个点，得到 %d", range24h.count, len(snap.Points))
	}
	if snap.BucketSeconds != Bucket5m {
		t.Fatalf("24h 桶大小应为 %d，得到 %d", Bucket5m, snap.BucketSeconds)
	}
	for i := 1; i < len(snap.Points); i++ {
		if snap.Points[i].T-snap.Points[i-1].T != Bucket5m {
			t.Fatalf("点列不连续：%d 与 %d", snap.Points[i-1].T, snap.Points[i].T)
		}
	}
	if snap.Points[len(snap.Points)-1].T != snap.End {
		t.Fatalf("最后一个点应落在 End 上")
	}
	if snap.Totals.Up != 1234 || snap.Totals.Down != 4321 {
		t.Fatalf("合计应为 up=1234 down=4321，得到 %+v", snap.Totals)
	}
	if snap.Peak.Up != 1234 || snap.Coverage.Since == 0 || snap.Coverage.Samples != 1 {
		t.Fatalf("峰值/覆盖不对：peak=%+v coverage=%+v", snap.Peak, snap.Coverage)
	}
}

// 空串按 24h；不认识的档位报错（接口层按 400 回）
func TestHistoryRangeParsing(t *testing.T) {
	svc, _ := newTestService(t)
	if snap, err := svc.History(""); err != nil || snap.Range != "24h" {
		t.Fatalf("空串应回落到 24h：range=%q err=%v", snap.Range, err)
	}
	if _, err := svc.History("3h"); err == nil {
		t.Fatal("不支持的档位应报错")
	}
	for _, r := range []string{"7d", "30d"} {
		snap, err := svc.History(r)
		if err != nil {
			t.Fatalf("History(%s): %v", r, err)
		}
		if snap.BucketSeconds != Bucket1h {
			t.Fatalf("%s 应用小时桶，得到 %d", r, snap.BucketSeconds)
		}
	}
}

// 超出保留期的桶与长期没见过的基线要被清掉，文件不会无限长大
func TestPruneDropsOldData(t *testing.T) {
	svc, _ := newTestService(t)
	now := time.Now()
	svc.local.record(now, []*models.Torrent{torrent("a", 0, 0)})

	svc.local.mu.Lock()
	svc.local.f.Bucket5m[now.Add(-keep5m-time.Hour).Unix()] = bucket{Up: 1}
	svc.local.f.Bucket1h[now.Add(-keep1h-time.Hour).Unix()] = bucket{Up: 1}
	svc.local.f.Baselines["gone"] = baseline{At: now.Add(-baseKeep - time.Hour).Unix()}
	svc.local.pruneLocked(now)
	_, has5m := svc.local.f.Bucket5m[now.Add(-keep5m-time.Hour).Unix()]
	_, has1h := svc.local.f.Bucket1h[now.Add(-keep1h-time.Hour).Unix()]
	_, hasBase := svc.local.f.Baselines["gone"]
	svc.local.mu.Unlock()

	if has5m || has1h || hasBase {
		t.Fatalf("过期数据未被清理：5m=%v 1h=%v base=%v", has5m, has1h, hasBase)
	}
}

// 落盘后重启能读回同一份历史（面板重启不该丢曲线）
func TestPersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := &fakeSource{torrents: []*models.Torrent{torrent("a", 0, 0)}}
	svc, err := New(src, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	now := time.Now()
	svc.local.record(now, src.torrents)
	src.torrents = []*models.Torrent{torrent("a", 777, 888)}
	svc.local.record(now.Add(time.Minute), src.torrents)
	svc.Flush()

	again, err := New(&fakeSource{}, dir)
	if err != nil {
		t.Fatalf("重新打开: %v", err)
	}
	snap, err := again.History("24h")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if snap.Totals.Up != 777 || snap.Totals.Down != 888 {
		t.Fatalf("重启后合计应为 up=777 down=888，得到 %+v", snap.Totals)
	}
	if _, err := filepath.Abs(dir); err != nil {
		t.Fatalf("dir: %v", err)
	}
}

// Sample 走 Source：取种子失败时返回错误，但不影响已有历史
func TestSamplePropagatesSourceError(t *testing.T) {
	svc, src := newTestService(t)
	src.err = context.DeadlineExceeded
	if err := svc.Sample(context.Background()); err == nil {
		t.Fatal("来源报错时应把错误抛给调用方")
	}
}
