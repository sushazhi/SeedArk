package rpc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/trpanel/backend/internal/driver"
	"github.com/trpanel/backend/internal/models"
)

// 本文件为 Manager 提供一层「路由包装」：
//
// 面板开启多服务器聚合后，列表里的种子 ID 是编码过的全局 ID（见 EncodeID）。
// 上层（API / 做种策略 / 限速引擎 / MCP）拿到的 ID 可能是任意一台服务器的，
// 直接丢给活动后端会找不到种子甚至误伤同名 ID。这里把所有按 ID 寻址的操作
// 统一按 ID 分组后分发到各自后端；聚合关闭时行为与改造前完全一致
// （DecodeID 返回 idx=-1，全部走活动后端）。
//
// 会话级操作（限速、全局设置……）只对「当前活动服务器」生效，不做分发：
// 聚合视图是只读的合并列表，写全局配置必须显式切换活动服务器，
// 否则一次保存会静默改掉所有下载器的配置。

// aggListTTL 聚合列表缓存时长。策略引擎与 WebSocket 都会反复拉列表，
// 不加缓存会把每台服务器都打成轮询热点。
const aggListTTL = 5 * time.Second

// ---- 会话级：直通活动后端 ----

// Kind 当前活动下载器类型
func (m *Manager) Kind() driver.Kind { return m.Client().Kind() }

// Capabilities 当前活动下载器的能力自述
func (m *Manager) Capabilities() driver.Capabilities { return m.Client().Capabilities() }

// Ping 检测活动下载器连通性
func (m *Manager) Ping(ctx context.Context) (string, error) { return m.Client().Ping(ctx) }

// GetSession 获取活动下载器会话配置
func (m *Manager) GetSession(ctx context.Context) (*models.Session, error) {
	return m.Client().GetSession(ctx)
}

// SetSession 修改活动下载器会话配置
func (m *Manager) SetSession(ctx context.Context, patch driver.SessionPatch) error {
	return m.Client().SetSession(ctx, patch)
}

// TestPort 检测活动下载器端口可达性
func (m *Manager) TestPort(ctx context.Context) (bool, error) { return m.Client().TestPort(ctx) }

// UpdateBlocklist 更新活动下载器黑名单
func (m *Manager) UpdateBlocklist(ctx context.Context) (int64, error) {
	return m.Client().UpdateBlocklist(ctx)
}

// GetFreeSpace 查询活动下载器磁盘剩余空间
func (m *Manager) GetFreeSpace(ctx context.Context, path string) (int64, int64, error) {
	return m.Client().GetFreeSpace(ctx, path)
}

// GetSessionGroups 获取活动下载器带宽组
func (m *Manager) GetSessionGroups(ctx context.Context) ([]models.BandwidthGroup, error) {
	return m.Client().GetSessionGroups(ctx)
}

// SetSessionGroup 修改活动下载器带宽组
func (m *Manager) SetSessionGroup(ctx context.Context, name string, fields map[string]any) error {
	return m.Client().SetSessionGroup(ctx, name, fields)
}

// SystemCommand 对活动下载器执行系统命令
func (m *Manager) SystemCommand(ctx context.Context, action string) error {
	return m.Client().SystemCommand(ctx, action)
}

// AddTorrentByFile 向活动下载器添加种子文件
func (m *Manager) AddTorrentByFile(ctx context.Context, data []byte, downloadDir string, paused bool, labels []string, priority *int64, filesWanted, filesUnwanted []int64) (int64, error) {
	return m.Client().AddTorrentByFile(ctx, data, downloadDir, paused, labels, priority, filesWanted, filesUnwanted)
}

// AddTorrentByURL 向活动下载器添加链接 / 磁力
func (m *Manager) AddTorrentByURL(ctx context.Context, link, downloadDir string, paused bool, labels []string, priority *int64) (int64, error) {
	return m.Client().AddTorrentByURL(ctx, link, downloadDir, paused, labels, priority)
}

// GetSessionStats 会话统计。聚合模式下把各成员速率与计数相加，
// 让用户看到「所有服务器合计」而不是只有活动服务器。
func (m *Manager) GetSessionStats(ctx context.Context) (*models.SessionStats, error) {
	if !m.AggregateEnabled() {
		return m.Client().GetSessionStats(ctx)
	}
	type res struct {
		idx int
		st  *models.SessionStats
		err error
	}
	indexes := m.AggregateIndexes()
	results := make([]res, len(indexes))
	var wg sync.WaitGroup
	for i, idx := range indexes {
		m.mu.RLock()
		mb, ok := m.members[idx]
		m.mu.RUnlock()
		if !ok {
			continue
		}
		wg.Add(1)
		go func(i, idx int, b driver.Backend) {
			defer wg.Done()
			st, err := b.GetSessionStats(ctx)
			results[i] = res{idx: idx, st: st, err: err}
		}(i, idx, mb.backend)
	}
	wg.Wait()

	sum := &models.SessionStats{}
	var errs []string
	for _, r := range results {
		if r.err != nil || r.st == nil {
			if r.err != nil {
				errs = append(errs, fmt.Sprintf("服务器%d: %v", r.idx+1, r.err))
			}
			continue
		}
		sum.ActiveTorrentCount += r.st.ActiveTorrentCount
		sum.PausedTorrentCount += r.st.PausedTorrentCount
		sum.TorrentCount += r.st.TorrentCount
		sum.DownloadSpeed += r.st.DownloadSpeed
		sum.UploadSpeed += r.st.UploadSpeed
		sum.Cumulative.DownloadedBytes += r.st.Cumulative.DownloadedBytes
		sum.Cumulative.UploadedBytes += r.st.Cumulative.UploadedBytes
		sum.Cumulative.FilesAdded += r.st.Cumulative.FilesAdded
		sum.Cumulative.SessionCount += r.st.Cumulative.SessionCount
		if r.st.Cumulative.SecondsActive > sum.Cumulative.SecondsActive {
			sum.Cumulative.SecondsActive = r.st.Cumulative.SecondsActive
		}
		sum.Current.DownloadedBytes += r.st.Current.DownloadedBytes
		sum.Current.UploadedBytes += r.st.Current.UploadedBytes
		sum.Current.FilesAdded += r.st.Current.FilesAdded
		sum.Current.SessionCount += r.st.Current.SessionCount
		if r.st.Current.SecondsActive > sum.Current.SecondsActive {
			sum.Current.SecondsActive = r.st.Current.SecondsActive
		}
	}
	if len(errs) == len(results) && len(results) > 0 {
		return nil, errors.New(strings.Join(errs, "; "))
	}
	return sum, nil
}

// ---- 种子级：按 ID 路由 ----

// RawCall 透传 Transmission RPC 方法（仅供 MCP 的原始 RPC 工具）。
// qBittorrent 走的是 REST 而非 JSON-RPC，语义无法一一对应，
// 这里明确拒绝而不是硬塞一个假实现。
func (m *Manager) RawCall(ctx context.Context, method string, args map[string]any) (map[string]any, error) {
	raw, ok := m.Client().(interface {
		RawCall(context.Context, string, map[string]any) (map[string]any, error)
	})
	if !ok {
		return nil, fmt.Errorf("原始 RPC 透传仅支持 Transmission，当前连接的是 %s", m.Client().Kind().Label())
	}
	return raw.RawCall(ctx, method, args)
}

// dropAggCache 写操作后丢弃聚合列表缓存，避免界面 5 秒内读回旧数据
func (m *Manager) dropAggCache() {
	m.mu.Lock()
	m.aggList = nil
	m.mu.Unlock()
}

// GetTorrents 种子列表（读缓存）。聚合模式下返回合并列表。
func (m *Manager) GetTorrents(ctx context.Context) ([]*models.Torrent, error) {
	if !m.AggregateEnabled() {
		return m.Client().GetTorrents(ctx)
	}
	m.mu.RLock()
	list, at := m.aggList, m.aggAt
	m.mu.RUnlock()
	if len(list) > 0 && time.Since(at) < aggListTTL {
		return list, nil
	}
	fresh, err := m.AggregateTorrents(ctx)
	if err != nil {
		return fresh, err
	}
	m.mu.Lock()
	m.aggList, m.aggAt = fresh, time.Now()
	m.mu.Unlock()
	return fresh, nil
}

// GetTorrentsFresh 强制刷新种子列表（写操作后的刷新必须走这里）
func (m *Manager) GetTorrentsFresh(ctx context.Context) ([]*models.Torrent, error) {
	if !m.AggregateEnabled() {
		return m.Client().GetTorrentsFresh(ctx)
	}
	fresh, err := m.AggregateTorrents(ctx)
	if err != nil {
		return fresh, err
	}
	m.mu.Lock()
	m.aggList, m.aggAt = fresh, time.Now()
	m.mu.Unlock()
	return fresh, nil
}

// GetTorrentDetail 种子详情（按 ID 路由到所属服务器）
func (m *Manager) GetTorrentDetail(ctx context.Context, id int64) (*models.Torrent, error) {
	b, local := m.BackendFor(id)
	t, err := b.GetTorrentDetail(ctx, local)
	if err == nil && t != nil {
		t.ID = id
	}
	return t, err
}

// GetTorrentSites 种子 → Tracker 站点映射。聚合模式下合并各成员结果并编码 ID。
func (m *Manager) GetTorrentSites(ctx context.Context) (map[int64][]string, error) {
	if !m.AggregateEnabled() {
		return m.Client().GetTorrentSites(ctx)
	}
	indexes := m.AggregateIndexes()
	out := make(map[int64][]string, 256)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, idx := range indexes {
		m.mu.RLock()
		mb, ok := m.members[idx]
		m.mu.RUnlock()
		if !ok {
			continue
		}
		wg.Add(1)
		go func(idx int, b driver.Backend) {
			defer wg.Done()
			sub, err := b.GetTorrentSites(ctx)
			if err != nil {
				return
			}
			mu.Lock()
			for id, sites := range sub {
				out[EncodeID(idx, id)] = sites
			}
			mu.Unlock()
		}(idx, mb.backend)
	}
	wg.Wait()
	return out, nil
}

// StartTorrents 开始
func (m *Manager) StartTorrents(ctx context.Context, ids []int64) error {
	return m.eachIDs(ctx, ids, func(b driver.Backend, local []int64) error { return b.StartTorrents(ctx, local) })
}

// StartTorrentsNow 强制开始
func (m *Manager) StartTorrentsNow(ctx context.Context, ids []int64) error {
	return m.eachIDs(ctx, ids, func(b driver.Backend, local []int64) error { return b.StartTorrentsNow(ctx, local) })
}

// StopTorrents 暂停
func (m *Manager) StopTorrents(ctx context.Context, ids []int64) error {
	return m.eachIDs(ctx, ids, func(b driver.Backend, local []int64) error { return b.StopTorrents(ctx, local) })
}

// VerifyTorrents 校验
func (m *Manager) VerifyTorrents(ctx context.Context, ids []int64) error {
	return m.eachIDs(ctx, ids, func(b driver.Backend, local []int64) error { return b.VerifyTorrents(ctx, local) })
}

// ReannounceTorrents 重新通告
func (m *Manager) ReannounceTorrents(ctx context.Context, ids []int64) error {
	return m.eachIDs(ctx, ids, func(b driver.Backend, local []int64) error { return b.ReannounceTorrents(ctx, local) })
}

// RemoveTorrents 删除（可选删除数据）
func (m *Manager) RemoveTorrents(ctx context.Context, ids []int64, deleteData bool) error {
	return m.eachIDs(ctx, ids, func(b driver.Backend, local []int64) error { return b.RemoveTorrents(ctx, local, deleteData) })
}

// SetTorrent 修改种子属性
func (m *Manager) SetTorrent(ctx context.Context, ids []int64, patch driver.TorrentPatch) error {
	return m.eachIDs(ctx, ids, func(b driver.Backend, local []int64) error { return b.SetTorrent(ctx, local, patch) })
}

// SetTorrentFlags 修改 Transmission 专有开关（顺序下载 / 分组）
func (m *Manager) SetTorrentFlags(ctx context.Context, ids []int64, flags driver.TorrentFlagPatch) error {
	return m.eachIDs(ctx, ids, func(b driver.Backend, local []int64) error { return b.SetTorrentFlags(ctx, local, flags) })
}

// QueueMove 队列排序
func (m *Manager) QueueMove(ctx context.Context, ids []int64, direction string) error {
	return m.eachIDs(ctx, ids, func(b driver.Backend, local []int64) error { return b.QueueMove(ctx, local, direction) })
}

// SetTorrentLocation 迁移存储位置
func (m *Manager) SetTorrentLocation(ctx context.Context, id int64, location string, move bool) error {
	b, local := m.BackendFor(id)
	return b.SetTorrentLocation(ctx, local, location, move)
}

// RenameFile 重命名种子内文件 / 目录
func (m *Manager) RenameFile(ctx context.Context, id int64, path, name string) error {
	b, local := m.BackendFor(id)
	return b.RenameFile(ctx, local, path, name)
}

// ReplaceTracker 批量替换 Tracker。聚合模式下在所有成员上执行并累加结果，
// 因为 Tracker 是跨服务器统一维护的（站点过滤 / 换域都是全局动作）。
func (m *Manager) ReplaceTracker(ctx context.Context, from, to string, appendMode bool) (int64, []string, error) {
	if !m.AggregateEnabled() {
		return m.Client().ReplaceTracker(ctx, from, to, appendMode)
	}
	var total int64
	var names []string
	var errs []string
	for _, b := range m.memberBackends() {
		n, ns, err := b.ReplaceTracker(ctx, from, to, appendMode)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		total += n
		names = append(names, ns...)
	}
	if len(errs) == len(m.memberBackends()) && len(errs) > 0 {
		return 0, nil, errors.New(strings.Join(errs, "; "))
	}
	return total, dedupNames(names), nil
}

// eachIDs 把 ID 按后端分组后并发下发。
// 空 ids 在 Transmission 语义下是「全部种子」：聚合模式下对每台服务器下发，
// 否则只对活动服务器下发（与改造前一致）。
func (m *Manager) eachIDs(ctx context.Context, ids []int64, fn func(driver.Backend, []int64) error) error {
	if len(ids) == 0 && !m.AggregateEnabled() {
		return fn(m.Client(), nil)
	}
	var backends []driver.Backend
	if len(ids) == 0 {
		backends = m.memberBackends()
		if len(backends) == 0 {
			backends = []driver.Backend{m.Client()}
		}
		var errs []string
		for _, b := range backends {
			if err := fn(b, nil); err != nil {
				errs = append(errs, err.Error())
			}
		}
		if len(errs) > 0 {
			return errors.New(strings.Join(errs, "; "))
		}
		m.dropAggCache()
		return nil
	}
	groups := m.GroupIDs(ids)
	if len(groups) == 1 {
		err := fn(groups[0].Backend, groups[0].IDs)
		if err == nil {
			m.dropAggCache()
		}
		return err
	}
	var mu sync.Mutex
	var errs []string
	var wg sync.WaitGroup
	for _, g := range groups {
		wg.Add(1)
		go func(b driver.Backend, local []int64) {
			defer wg.Done()
			if err := fn(b, local); err != nil {
				mu.Lock()
				errs = append(errs, err.Error())
				mu.Unlock()
			}
		}(g.Backend, g.IDs)
	}
	wg.Wait()
	if len(errs) == 0 {
		m.dropAggCache()
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// memberBackends 当前参与聚合的后端实例（有序）
func (m *Manager) memberBackends() []driver.Backend {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]driver.Backend, 0, len(m.members))
	for _, idx := range m.aggIndex {
		if mb, ok := m.members[idx]; ok && mb.backend != nil {
			out = append(out, mb.backend)
		}
	}
	return out
}

// dedupNames 合并多台服务器返回的名字并去重
func dedupNames(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, n := range in {
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}
