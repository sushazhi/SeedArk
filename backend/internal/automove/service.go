package automove

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/trpanel/backend/internal/rpc"
	"github.com/trpanel/backend/internal/state"
)

// Service 自动文件管理：已完成种子按规则移动到目标目录
type Service struct {
	manager *rpc.Manager
	store   *state.Store
}

// New 创建自动文件管理服务
func New(manager *rpc.Manager, store *state.Store) *Service {
	return &Service{manager: manager, store: store}
}

// Run 后台循环
func (s *Service) Run(ctx context.Context) {
	slog.Info("自动文件管理服务已启动")
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		s.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Tick 执行一轮规则匹配
func (s *Service) Tick(ctx context.Context) error {
	st := s.store.Get()
	rules := st.AutoMoveRules
	hasEnabled := false
	hasSites := false
	for _, r := range rules {
		if r.Enabled {
			hasEnabled = true
			if len(r.Sites) > 0 {
				hasSites = true
			}
		}
	}
	if !hasEnabled {
		return nil
	}
	torrents, err := s.manager.GetTorrents(ctx)
	if err != nil {
		slog.Warn("自动文件管理：获取种子列表失败", "err", err)
		return err
	}
	moved := 0
	for _, t := range torrents {
		if t == nil || !t.IsFinished {
			continue
		}
		if _, done := st.ProcessedMoves[t.HashString]; done {
			continue
		}
		// 浅拷贝，避免修改 Trackers 污染 GetTorrents 的共享缓存
		tt := *t
		// 站点规则需要 Trackers 信息（列表接口不返回，按需拉取详情）
		if hasSites && len(tt.Trackers) == 0 {
			if detail, err := s.manager.GetTorrentDetail(ctx, tt.ID); err == nil && detail != nil {
				tt.Trackers = detail.Trackers
			}
		}
		rule := findRule(st, &tt)
		if rule == nil {
			continue
		}
		if tt.DownloadDir == rule.TargetDir {
			// 已在目标目录，直接标记避免重复扫描
			_ = s.store.Update(func(st2 *state.State) { st2.ProcessedMoves[tt.HashString] = strconv.FormatInt(state.NowUnix(), 10) })
			continue
		}
		if err := s.manager.SetTorrentLocation(ctx, tt.ID, rule.TargetDir, true); err != nil {
			slog.Warn("自动文件管理：移动失败", "torrent", tt.Name, "to", rule.TargetDir, "err", err)
			continue
		}
		_ = s.store.Update(func(st2 *state.State) { st2.ProcessedMoves[tt.HashString] = strconv.FormatInt(state.NowUnix(), 10) })
		moved++
		slog.Info("自动文件管理：已移动", "torrent", tt.Name, "to", rule.TargetDir, "rule", rule.Name)
	}
	if moved > 0 {
		slog.Info("自动文件管理完成", "moved", moved)
	}
	return nil
}

func findRule(st state.State, t *rpc.Torrent) *state.AutoMoveRule {
	// 规则按配置顺序匹配，命中第一条即返回
	for i := range st.AutoMoveRules {
		r := &st.AutoMoveRules[i]
		if !r.Enabled || r.TargetDir == "" {
			continue
		}
		if matchRule(r, t) {
			return r
		}
	}
	return nil
}

func matchRule(r *state.AutoMoveRule, t *rpc.Torrent) bool {
	// 名称包含
	if r.NameMatch != "" && !strings.Contains(strings.ToLower(t.Name), strings.ToLower(r.NameMatch)) {
		return false
	}
	// 标签（任选其一）
	if len(r.Labels) > 0 {
		ok := false
		for _, l := range r.Labels {
			for _, tl := range t.Labels {
				if l == tl {
					ok = true
					break
				}
			}
			if ok {
				break
			}
		}
		if !ok {
			return false
		}
	}
	// 站点（任选其一）
	if len(r.Sites) > 0 {
		ok := false
		for _, site := range r.Sites {
			for _, tr := range t.Trackers {
				name := tr.SiteName
				if name == "" {
					name = hostOf(tr.Announce)
				}
				if name == site || strings.Contains(name, site) || strings.Contains(hostOf(tr.Announce), site) {
					ok = true
					break
				}
			}
			if ok {
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// hostOf 提取 announce 地址的主机名（如 trackers.m-team.cc）
func hostOf(u string) string {
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	u = strings.TrimPrefix(u, "udp://")
	u = strings.TrimPrefix(u, "wss://")
	u = strings.TrimPrefix(u, "ws://")
	if idx := strings.IndexByte(u, '/'); idx >= 0 {
		u = u[:idx]
	}
	if idx := strings.IndexByte(u, ':'); idx >= 0 {
		u = u[:idx]
	}
	return u
}
