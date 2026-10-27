// Package traffic 记录全局上下行流量的历史，供统计面板画折线图。
//
// 数据来源只能是下载器自己的累计计数器（Transmission / qBittorrent 都不提供
// 历史流量），所以这里按固定周期读一次全量种子，把「相对上次的正增量」累加进
// 时间桶。计数器只增不减、种子又随时会被删/重加，两个方向都得处理：
// 首见与计数器回退都只重置基线、不计增量，避免把累计量当成一次性流量造出假尖峰。
package traffic

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/sushazhi/seedark/backend/internal/models"
)

// 采样周期：60s 与保种台采样一致，5 分钟桶每个装 5 次采样。
const sampleInterval = 60 * time.Second

// Source 采样来源：读一次全量种子（*rpc.Manager 满足）。
// 只依赖这一个方法，测试里可以喂固定快照。
type Source interface {
	GetTorrents(ctx context.Context) ([]*models.Torrent, error)
}

// Point 一个时间桶内的流量（桶起点 unix 秒 + 该桶内的字节数）
type Point struct {
	T    int64 `json:"t"`
	Down int64 `json:"down"`
	Up   int64 `json:"up"`
}

// Totals 一段时间内的合计或峰值
type Totals struct {
	Down int64 `json:"down"`
	Up   int64 `json:"up"`
}

// Coverage 采样覆盖情况：Since 为 0 表示本机还没有采到过一次。
// 「历史从本机跑起来那天开始」这件事必须让用户看得见，否则第一天开面板
// 会以为功能坏了（与保种台的采样覆盖是同一种诚实）。
type Coverage struct {
	Since   int64 `json:"since"`
	Samples int   `json:"samples"`
}

// Snapshot 一段时间的流量历史。Points 在整段时间上连续（没有流量的桶补 0），
// 前端因此可以等距画折线，不用自己补空档。
type Snapshot struct {
	Range         string   `json:"range"`
	BucketSeconds int      `json:"bucketSeconds"`
	Start         int64    `json:"start"`
	End           int64    `json:"end"`
	Points        []Point  `json:"points"`
	Totals        Totals   `json:"totals"`
	Peak          Totals   `json:"peak"`
	Coverage      Coverage `json:"coverage"`
}

// Service 流量历史服务
type Service struct {
	src   Source
	local *store

	// 与保种台采样同理：两次采样并发只会在同一桶上重复累加，串起来最省事
	mu sync.Mutex
}

// New 创建流量历史服务，数据落在 DataDir/traffic 下
func New(src Source, dataDir string) (*Service, error) {
	ls, err := newStore(dataDir)
	if err != nil {
		return nil, err
	}
	return &Service{src: src, local: ls}, nil
}

// Run 后台循环：每 60s 采一次，退出前落盘。
// 采样失败只记日志——下载器暂时连不上不该让面板里的其余数字一起消失。
func (s *Service) Run(ctx context.Context) {
	slog.Info("流量历史服务已启动")
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()
	for {
		_ = s.Sample(ctx)
		select {
		case <-ctx.Done():
			s.local.flush()
			return
		case <-ticker.C:
		}
	}
}

// Sample 采一次：把每颗种子的计数器增量累加进当前时间桶
func (s *Service) Sample(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	torrents, err := s.src.GetTorrents(ctx)
	if err != nil {
		return err
	}
	s.local.record(time.Now(), torrents)
	return nil
}

// History 取一段时间的流量历史。rng：24h / 7d / 30d，空串按 24h。
func (s *Service) History(rng string) (Snapshot, error) {
	if rng == "" {
		rng = range24h.name
	}
	return s.local.history(rng, time.Now())
}

// Flush 落盘（进程退出或测试收尾用）
func (s *Service) Flush() { s.local.flush() }

// 三档时间范围：24 小时看 5 分钟桶的细节，7/30 天看小时桶的趋势。
// 桶数与保留期对齐，取回来的点总是满的，前端不用处理残缺的尾部。
var (
	range24h = spec{name: "24h", bucket: Bucket5m, count: 24 * 60 / 5}
	range7d  = spec{name: "7d", bucket: Bucket1h, count: 7 * 24}
	range30d = spec{name: "30d", bucket: Bucket1h, count: 30 * 24}
	ranges   = map[string]spec{range24h.name: range24h, range7d.name: range7d, range30d.name: range30d}
)

// spec 一档范围：桶大小与桶数
type spec struct {
	name   string
	bucket int
	count  int
}

// ErrBadRange 不支持的时间范围（接口按 400 回，提示可用值）
func ErrBadRange(rng string) error {
	return fmt.Errorf("不支持的时间范围：%s（可用 24h / 7d / 30d）", rng)
}
