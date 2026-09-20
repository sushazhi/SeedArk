package rpc

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/trpanel/backend/internal/driver"
	"github.com/trpanel/backend/internal/models"
	"github.com/trpanel/backend/internal/qbittorrent"
)

// idShift 聚合模式下种子 ID 的服务器编号位移。
//
// Transmission 与 qBittorrent 各自的 ID 空间互相独立，两台服务器上同为 3 号
// 的种子在合并列表里必须可区分，否则批量暂停会误伤另一台的种子。
// 约定：低 40 位存放「服务器本地 ID」，高位移部分存放「服务器序号 + 1」。
// 40 位 = 1.1 万亿，远超任何一台下载器的种子数；当前活动服务器的种子
// 保持原始 ID（不编码），既有链接、自动化与状态文件都不受影响。
const idShift = 40

// maxLocalID 服务器本地 ID 上限（qBittorrent 的 hash→ID 映射必须落在此范围内）
const maxLocalID = int64(1) << idShift

// Credentials 当前活动连接的凭据。
// Type 决定用哪个驱动创建后端；既有配置没有该字段，缺省即 Transmission。
type Credentials struct {
	Type driver.Kind
	URL  string
	User string
	Pass string
}

// Target 一台可聚合的下载器（来自持久化的服务器列表）
type Target struct {
	Index   int
	Kind    driver.Kind
	URL     string
	User    string
	Pass    string
	Enabled bool
}

// member 聚合成员：一个服务器索引对应一个后端实例。
// backend 惰性创建并复用（qBittorrent 每次新建都要重新登录，
// 频繁重建会打爆它的失败登录计数进而封 IP）。
type member struct {
	target  Target
	backend driver.Backend
}

// Manager 管理下载器后端实例，支持运行期热更新连接配置与多服务器聚合。
type Manager struct {
	mu       sync.RWMutex
	client   driver.Backend
	cred     Credentials
	members  map[int]*member // 服务器索引 → 后端（含活动服务器）
	aggMu    sync.RWMutex
	aggIndex []int // 参与聚合的服务器索引（有序）
	lastErr  map[int]string
	lastSeen map[int]time.Time
	// 聚合列表缓存（见 router.go 的 GetTorrents）
	aggList []*models.Torrent
	aggAt   time.Time
}

// NewManager 创建 Manager 并初始化活动后端
func NewManager(cred Credentials) (*Manager, error) {
	c, err := newBackend(cred)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		client:   c,
		cred:     cred,
		members:  map[int]*member{},
		lastErr:  map[int]string{},
		lastSeen: map[int]time.Time{},
	}
	m.aggIndex = []int{}
	return m, nil
}

// NewProbe 按凭据构造一个一次性后端，用于「保存前先探测连通性」。
// 探测成功即可丢弃，不进入 Manager 的连接池。
func NewProbe(cred Credentials) (driver.Backend, error) {
	return newBackend(cred)
}

// newBackend 按类型构造驱动实例
func newBackend(cred Credentials) (driver.Backend, error) {
	switch driver.NormalizeKind(string(cred.Type)) {
	case driver.KindQBittorrent:
		return qbittorrent.New(cred.URL, cred.User, cred.Pass)
	default:
		return New(cred.URL, cred.User, cred.Pass)
	}
}

// Client 获取当前活动后端
func (m *Manager) Client() driver.Backend {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.client
}

// Reconfigure 使用新配置重建活动后端
func (m *Manager) Reconfigure(cred Credentials) error {
	c, err := newBackend(cred)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.client = c
	m.cred = cred
	// 凭据变了，缓存的成员实例整体作废（下次聚合时按新目标重建）
	m.members = map[int]*member{}
	m.mu.Unlock()
	return nil
}

// Credentials 返回当前活动连接配置
func (m *Manager) Credentials() Credentials {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cred
}

// SetTargets 同步参与聚合的服务器列表。
// 由 API 层在服务器增删改、切换活动服务器后调用；索引变化或凭据变化的条目
// 会重建实例，其余沿用（避免每次都重新登录 qBittorrent）。
func (m *Manager) SetTargets(targets []Target) {
	next := make(map[int]*member, len(targets))
	index := make([]int, 0, len(targets))
	m.mu.Lock()
	for _, t := range targets {
		if !t.Enabled || t.URL == "" {
			continue
		}
		idx := t.Index
		index = append(index, idx)
		if old, ok := m.members[idx]; ok && old.target == t {
			next[idx] = old
			continue
		}
		b, err := newBackend(Credentials{Type: t.Kind, URL: t.URL, User: t.User, Pass: t.Pass})
		if err != nil {
			// 单台不可达不影响其它成员：记下原因，聚合时跳过
			m.lastErr[idx] = err.Error()
			continue
		}
		delete(m.lastErr, idx)
		next[idx] = &member{target: t, backend: b}
	}
	m.members = next
	m.mu.Unlock()

	sort.Ints(index)
	m.aggMu.Lock()
	m.aggIndex = index
	m.aggMu.Unlock()
}

// AggregateEnabled 是否开启了聚合视图
func (m *Manager) AggregateEnabled() bool {
	m.aggMu.RLock()
	defer m.aggMu.RUnlock()
	return len(m.aggIndex) > 0
}

// AggregateIndexes 参与聚合的服务器索引（有序）
func (m *Manager) AggregateIndexes() []int {
	m.aggMu.RLock()
	defer m.aggMu.RUnlock()
	out := make([]int, len(m.aggIndex))
	copy(out, m.aggIndex)
	return out
}

// AggregateErrors 各成员最近一次准备失败的原因（供界面提示）
func (m *Manager) AggregateErrors() map[int]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[int]string, len(m.lastErr))
	for k, v := range m.lastErr {
		out[k] = v
	}
	return out
}

// EncodeID 把某台服务器上的本地 ID 编码为面板全局 ID。
// idx 为服务器索引（与 SetTargets 传入的一致）。
func EncodeID(idx int, localID int64) int64 {
	return int64(idx+1)<<idShift | (localID & (maxLocalID - 1))
}

// DecodeID 把面板全局 ID 还原为「服务器索引 + 本地 ID」。
// 未编码的 ID（低于位移量）返回 idx=-1，表示属于当前活动服务器。
func DecodeID(id int64) (idx int, localID int64) {
	if id < maxLocalID {
		return -1, id
	}
	return int(id>>idShift) - 1, id & (maxLocalID - 1)
}

// BackendFor 按种子 ID 路由到对应后端，并返回该后端视角的本地 ID。
// 聚合关闭时永远是活动后端。
func (m *Manager) BackendFor(id int64) (driver.Backend, int64) {
	idx, localID := DecodeID(id)
	if idx < 0 {
		return m.Client(), localID
	}
	m.mu.RLock()
	mb, ok := m.members[idx]
	m.mu.RUnlock()
	if !ok {
		return m.Client(), localID
	}
	return mb.backend, localID
}

// GroupIDs 把一批 ID 按目标后端分组（批量操作一次分发到各服务器）。
// 返回顺序与输入一致，调用方据此拼装响应。
func (m *Manager) GroupIDs(ids []int64) []IDGroup {
	if len(ids) == 0 {
		return nil
	}
	// 保持输入顺序，避免响应里的 id 列表与请求错位
	order := make(map[driver.Backend]int)
	var groups []IDGroup
	for _, id := range ids {
		b, local := m.BackendFor(id)
		i, ok := order[b]
		if !ok {
			i = len(groups)
			order[b] = i
			groups = append(groups, IDGroup{Backend: b})
		}
		groups[i].IDs = append(groups[i].IDs, local)
	}
	return groups
}

// IDGroup 同一后端上的一批本地 ID
type IDGroup struct {
	Backend driver.Backend
	IDs     []int64
}

// AggregateTorrents 并发拉取所有聚合成员的种子并合并为一张列表。
// 非活动服务器的种子 ID 会被编码（EncodeID），保证跨服务器唯一。
// 单台失败只记日志并跳过，不让一台挂掉拖垮整个列表。
func (m *Manager) AggregateTorrents(ctx context.Context) ([]*models.Torrent, error) {
	indexes := m.AggregateIndexes()
	if len(indexes) == 0 {
		return m.Client().GetTorrentsFresh(ctx)
	}
	type result struct {
		idx      int
		torrents []*models.Torrent
		err      error
	}
	results := make([]result, len(indexes))
	var wg sync.WaitGroup
	for i, idx := range indexes {
		m.mu.RLock()
		mb, ok := m.members[idx]
		m.mu.RUnlock()
		if !ok {
			results[i] = result{idx: idx, err: fmt.Errorf("服务器 %d 未就绪", idx+1)}
			continue
		}
		wg.Add(1)
		go func(i, idx int, b driver.Backend) {
			defer wg.Done()
			list, err := b.GetTorrentsFresh(ctx)
			// 就地改写 ID：列表仅本协程持有，无需加锁
			tagTorrentIDs(list, idx)
			results[i] = result{idx: idx, torrents: list, err: err}
		}(i, idx, mb.backend)
	}
	wg.Wait()

	m.mu.Lock()
	out := make([]*models.Torrent, 0, 128)
	for _, r := range results {
		if r.err != nil {
			m.lastErr[r.idx] = r.err.Error()
			slog.Warn("聚合拉取失败，已跳过该服务器", "server", r.idx+1, "err", r.err)
			continue
		}
		delete(m.lastErr, r.idx)
		m.lastSeen[r.idx] = time.Now()
		out = append(out, r.torrents...)
	}
	m.mu.Unlock()
	return out, nil
}

// tagTorrentIDs 给非活动服务器的种子 ID 打上服务器编号前缀
func tagTorrentIDs(list []*models.Torrent, idx int) {
	for _, t := range list {
		t.ID = EncodeID(idx, t.ID)
	}
}
