package qbittorrent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sushazhi/seedark/backend/internal/driver"
	"github.com/sushazhi/seedark/backend/internal/models"
)

// appPreferences /api/v2/app/preferences（只声明面板会读写的字段，
// 其余偏好原样保留——setPreferences 是整体覆盖，未列出的字段不会被动到）
type appPreferences struct {
	SavePath              string  `json:"save_path"`
	TempPath              string  `json:"temp_path"`
	TempPathEnabled       bool    `json:"temp_path_enabled"`
	DlLimit               int64   `json:"dl_limit"`
	UpLimit               int64   `json:"up_limit"`
	AltDlLimit            int64   `json:"alt_dl_limit"`
	AltUpLimit            int64   `json:"alt_up_limit"`
	SchedulerEnabled      bool    `json:"scheduler_enabled"`
	ScheduleFromHour      int64   `json:"schedule_from_hour"`
	ScheduleFromMin       int64   `json:"schedule_from_min"`
	ScheduleToHour        int64   `json:"schedule_to_hour"`
	ScheduleToMin         int64   `json:"schedule_to_min"`
	SchedulerDays         int64   `json:"scheduler_days"`
	DHT                   bool    `json:"dht"`
	Pex                   bool    `json:"pex"`
	Lsd                   bool    `json:"lsd"`
	Encryption            int64   `json:"encryption"`
	ListenPort            int64   `json:"listen_port"`
	RandomPort            bool    `json:"random_port"`
	UPnP                  bool    `json:"upnp"`
	MaxActiveDownloads    int64   `json:"max_active_downloads"`
	MaxActiveUploads      int64   `json:"max_active_uploads"`
	MaxActiveTorrents     int64   `json:"max_active_torrents"`
	QueueingEnabled       bool    `json:"queueing_enabled"`
	MaxRatio              float64 `json:"max_ratio"`
	MaxRatioEnabled       bool    `json:"max_ratio_enabled"`
	MaxRatioAct           int64   `json:"max_ratio_act"`
	MaxSeedingTime        int64   `json:"max_seeding_time"`
	MaxSeedingTimeEnabled bool    `json:"max_seeding_time_enabled"`
	DiskCache             int64   `json:"disk_cache"`
	AddTrackersEnabled    bool    `json:"add_trackers_enabled"`
	AddTrackers           string  `json:"add_trackers"`
	StartPausedEnabled    bool    `json:"start_paused_enabled"`
	PreallocateAll        bool    `json:"preallocate_all"`
	IncompleteFilesExt    bool    `json:"incomplete_files_ext"`
	ProxyType             any     `json:"proxy_type"`
}

// transferInfo /api/v2/transfer/info
type transferInfo struct {
	DlInfoSpeed      int64  `json:"dl_info_speed"`
	UpInfoSpeed      int64  `json:"up_info_speed"`
	DlInfoData       int64  `json:"dl_info_data"`
	UpInfoData       int64  `json:"up_info_data"`
	DlRateLimit      int64  `json:"dl_rate_limit"`
	UpRateLimit      int64  `json:"up_rate_limit"`
	DHTNodes         int64  `json:"dht_nodes"`
	ConnectionStatus string `json:"connection_status"`
}

// mainData /api/v2/sync/maindata（只取 server_state，
// 全量 torrents 部分体积大，这里不解析）
type mainData struct {
	ServerState map[string]any `json:"server_state"`
}

// GetSession 获取会话配置（偏好 + 传输信息）
func (c *Client) GetSession(ctx context.Context) (*models.Session, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// 原始偏好表既供下方通用字段映射，又按自述清单原样交给设置面板
	// （后者要的就是原生键，加一层结构体只会丢字段）
	var rawPrefs map[string]any
	if err := c.get(ctx, "app/preferences", nil, &rawPrefs); err != nil {
		return nil, err
	}
	prefs, err := decodePrefs(rawPrefs)
	if err != nil {
		return nil, err
	}
	var info transferInfo
	if err := c.get(ctx, "transfer/info", nil, &info); err != nil {
		return nil, err
	}
	version := ""
	_ = c.get(ctx, "app/version", nil, &version)
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")

	sess := &models.Session{
		Version:              version,
		DownloadDir:          prefs.SavePath,
		IncompleteDir:        prefs.TempPath,
		IncompleteDirEnabled: prefs.TempPathEnabled,
		SpeedLimitDown:       prefs.DlLimit / 1024,
		SpeedLimitDownOn:     prefs.DlLimit > 0,
		SpeedLimitUp:         prefs.UpLimit / 1024,
		SpeedLimitUpOn:       prefs.UpLimit > 0,
		AltSpeedDown:         prefs.AltDlLimit / 1024,
		AltSpeedUp:           prefs.AltUpLimit / 1024,
		// qBittorrent 没有「备用限速开关」的读取端点，用调度开关近似：
		// 定时调度开启即视为备用限速按计划生效
		AltSpeedEnabled:         false,
		AltSpeedTimeEnabled:     prefs.SchedulerEnabled,
		AltSpeedTimeBegin:       prefs.ScheduleFromHour*60 + prefs.ScheduleFromMin,
		AltSpeedTimeEnd:         prefs.ScheduleToHour*60 + prefs.ScheduleToMin,
		AltSpeedTimeDay:         prefs.SchedulerDays,
		PeerPort:                prefs.ListenPort,
		PeerPortRandomOnStart:   prefs.RandomPort,
		PEXEnabled:              prefs.Pex,
		DHTEnabled:              prefs.DHT,
		LPDEnabled:              prefs.Lsd,
		UTPEnabled:              true, // qBittorrent 默认启用 uTP，偏好里无独立开关
		Encryption:              mapEncryption(prefs.Encryption),
		SeedRatioLimit:          prefs.MaxRatio,
		StartAdded:              !prefs.StartPausedEnabled,
		DownloadQueueSize:       prefs.MaxActiveDownloads,
		DownloadQueueEnabled:    prefs.QueueingEnabled,
		SeedQueueSize:           prefs.MaxActiveUploads,
		SeedQueueEnabled:        prefs.QueueingEnabled,
		PortForwardingEnabled:   prefs.UPnP,
		CacheSizeMB:             prefs.DiskCache,
		IdleSeedingLimitEnabled: prefs.MaxSeedingTimeEnabled,
		IdleSeedingLimit:        prefs.MaxSeedingTime,
		DefaultTrackers:         splitTrackers(prefs.AddTrackers),
		Prefs:                   readQBPreferences(rawPrefs),
	}
	// 兼容尚未迁移的调用方（既有测试走 Session.QB）
	sess.QB = sess.Prefs
	// 传输限速：优先用 transfer/info 的实时值（备用限速生效时它会变）
	if info.DlRateLimit > 0 {
		sess.SpeedLimitDown = info.DlRateLimit / 1024
		sess.SpeedLimitDownOn = true
	}
	if info.UpRateLimit > 0 {
		sess.SpeedLimitUp = info.UpRateLimit / 1024
		sess.SpeedLimitUpOn = true
	}
	return sess, nil
}

// SetSession 更新会话配置（只下发本次提交的字段，其余偏好不动）
func (c *Client) SetSession(ctx context.Context, patch driver.SessionPatch) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	prefs := map[string]any{}
	put := func(k string, v any) { prefs[k] = v }

	if patch.DownloadDir != nil {
		put("save_path", *patch.DownloadDir)
	}
	if patch.IncompleteDir != nil {
		put("temp_path", *patch.IncompleteDir)
	}
	if patch.IncompleteDirEnabled != nil {
		put("temp_path_enabled", *patch.IncompleteDirEnabled)
	}
	if patch.SpeedLimitDown != nil {
		put("dl_limit", *patch.SpeedLimitDown*1024)
	}
	if patch.SpeedLimitUp != nil {
		put("up_limit", *patch.SpeedLimitUp*1024)
	}
	if patch.AltSpeedDown != nil {
		put("alt_dl_limit", *patch.AltSpeedDown*1024)
	}
	if patch.AltSpeedUp != nil {
		put("alt_up_limit", *patch.AltSpeedUp*1024)
	}
	if patch.AltSpeedTimeEnabled != nil {
		put("scheduler_enabled", *patch.AltSpeedTimeEnabled)
	}
	if patch.AltSpeedTimeBegin != nil {
		put("schedule_from_hour", *patch.AltSpeedTimeBegin/60)
		put("schedule_from_min", *patch.AltSpeedTimeBegin%60)
	}
	if patch.AltSpeedTimeEnd != nil {
		put("schedule_to_hour", *patch.AltSpeedTimeEnd/60)
		put("schedule_to_min", *patch.AltSpeedTimeEnd%60)
	}
	if patch.AltSpeedTimeDay != nil {
		put("scheduler_days", *patch.AltSpeedTimeDay)
	}
	if patch.DHTEnabled != nil {
		put("dht", *patch.DHTEnabled)
	}
	if patch.PEXEnabled != nil {
		put("pex", *patch.PEXEnabled)
	}
	if patch.LPDEnabled != nil {
		put("lsd", *patch.LPDEnabled)
	}
	if patch.Encryption != nil {
		put("encryption", unmapEncryption(*patch.Encryption))
	}
	if patch.PeerPort != nil {
		put("listen_port", *patch.PeerPort)
	}
	if patch.PeerPortRandomOnStart != nil {
		put("random_port", *patch.PeerPortRandomOnStart)
	}
	if patch.PortForwardingEnabled != nil {
		put("upnp", *patch.PortForwardingEnabled)
	}
	if patch.CacheSizeMB != nil {
		put("disk_cache", *patch.CacheSizeMB)
	}
	if patch.DownloadQueueSize != nil {
		put("max_active_downloads", *patch.DownloadQueueSize)
	}
	if patch.SeedQueueSize != nil {
		put("max_active_uploads", *patch.SeedQueueSize)
	}
	// qBittorrent 只有一个队列总开关，下载/做种两路共用（读取侧把它同时映射给
	// DownloadQueueEnabled 与 SeedQueueEnabled，见 GetSession）。原先恒写 true，
	// 用户在界面上关掉任一队列都关不掉总开关；按补丁里给出的值写回。
	if patch.DownloadQueueEnabled != nil || patch.SeedQueueEnabled != nil {
		enabled := false
		if patch.DownloadQueueEnabled != nil && *patch.DownloadQueueEnabled {
			enabled = true
		}
		if patch.SeedQueueEnabled != nil && *patch.SeedQueueEnabled {
			enabled = true
		}
		put("queueing_enabled", enabled)
	}
	if patch.SeedRatioLimit != nil {
		put("max_ratio", *patch.SeedRatioLimit)
		put("max_ratio_enabled", true)
	}
	if patch.SeedRatioLimited != nil {
		put("max_ratio_enabled", *patch.SeedRatioLimited)
	}
	if patch.IdleSeedingLimit != nil {
		put("max_seeding_time", *patch.IdleSeedingLimit)
	}
	if patch.IdleSeedingLimitEnabled != nil {
		put("max_seeding_time_enabled", *patch.IdleSeedingLimitEnabled)
	}
	if patch.StartAdded != nil {
		put("start_paused_enabled", !*patch.StartAdded)
	}
	if patch.DefaultTrackers != nil {
		put("add_trackers", strings.Join(patch.DefaultTrackers, "\n"))
		put("add_trackers_enabled", len(patch.DefaultTrackers) > 0)
	}
	// Transmission 专属、qBittorrent 无对应能力：忽略（黑名单、脚本钩子、
	// 未完成文件重命名、回收源文件、队列停滞、全局连接数等）
	// QB 通道：设置面板直投的原生偏好键，逐个按键值校验（含枚举范围）后并入
	if len(patch.QB) > 0 {
		qbPrefs, err := applyQBPreferences(patch.QB)
		if err != nil {
			return err
		}
		for k, v := range qbPrefs {
			prefs[k] = v
		}
	}
	if len(prefs) == 0 {
		return nil
	}
	raw, err := json.Marshal(prefs)
	if err != nil {
		return err
	}
	return c.post(ctx, "app/setPreferences", url.Values{"json": {string(raw)}})
}

// GetSessionStats 会话统计。
// 当前速率与会话量取 /transfer/info（轻量），累计量只有 /sync/maindata 的
// server_state 才有，故单独取一次并缓存 5 分钟——种子多时 maindata 首帧很大，
// 不能每次统计都拉一遍。
func (c *Client) GetSessionStats(ctx context.Context) (*models.SessionStats, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var info transferInfo
	if err := c.get(ctx, "transfer/info", nil, &info); err != nil {
		return nil, err
	}
	var list []torrentInfo
	if err := c.get(ctx, "torrents/info", nil, &list); err != nil {
		return nil, err
	}
	var active, paused int64
	for _, t := range list {
		if isStoppedState(t.State) {
			paused++
			continue
		}
		if t.Progress < 1 || strings.HasPrefix(t.State, "download") || t.State == "metaDL" {
			active++
		}
	}
	alltimeDL, alltimeUL := c.alltime(ctx)
	return &models.SessionStats{
		ActiveTorrentCount: active,
		PausedTorrentCount: paused,
		TorrentCount:       int64(len(list)),
		DownloadSpeed:      info.DlInfoSpeed,
		UploadSpeed:        info.UpInfoSpeed,
		Cumulative: models.SessionStatsDetails{
			DownloadedBytes: alltimeDL,
			UploadedBytes:   alltimeUL,
		},
		Current: models.SessionStatsDetails{
			DownloadedBytes: info.DlInfoData,
			UploadedBytes:   info.UpInfoData,
		},
	}, nil
}

// alltime 累计上传/下载量（带 5 分钟缓存）
func (c *Client) alltime(ctx context.Context) (dl, ul int64) {
	c.alltimeMu.RLock()
	if !c.alltimeAt.IsZero() && time.Since(c.alltimeAt) < 5*time.Minute {
		dl, ul = c.alltimeDL, c.alltimeUL
		c.alltimeMu.RUnlock()
		return dl, ul
	}
	c.alltimeMu.RUnlock()

	var md mainData
	if err := c.get(ctx, "sync/maindata", url.Values{"rid": {"0"}}, &md); err != nil {
		return 0, 0
	}
	dl = int64From(md.ServerState["alltime_dl"])
	ul = int64From(md.ServerState["alltime_ul"])
	c.alltimeMu.Lock()
	c.alltimeDL, c.alltimeUL, c.alltimeAt = dl, ul, time.Now()
	c.alltimeMu.Unlock()
	return dl, ul
}

// GetFreeSpace 查询目录剩余空间（qBittorrent 只在 maindata 里给出，
// 且是针对默认下载目录的全局值，无法按任意路径查询）
func (c *Client) GetFreeSpace(ctx context.Context, path string) (int64, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var md mainData
	if err := c.get(ctx, "sync/maindata", url.Values{"rid": {"0"}}, &md); err != nil {
		return 0, 0, err
	}
	free := int64From(md.ServerState["free_space_on_disk"])
	if free <= 0 {
		return 0, 0, fmt.Errorf("qBittorrent 未提供剩余空间信息")
	}
	// 总容量 Web API 不提供，返回 0 表示未知：界面只显示剩余空间，
	// 或在服务器配置里手填总容量后由 API 层补上（见 api 包的 free-space 处理器）
	return free, 0, nil
}

// TestPort qBittorrent 无端口检测接口，用连接状态近似：
// connected = 监听端口外网可达
func (c *Client) TestPort(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var info transferInfo
	if err := c.get(ctx, "transfer/info", nil, &info); err != nil {
		return false, err
	}
	return info.ConnectionStatus == "connected", nil
}

// UpdateBlocklist qBittorrent 无黑名单更新接口（IP 过滤依赖外部文件）
func (c *Client) UpdateBlocklist(ctx context.Context) (int64, error) {
	return 0, driver.ErrUnsupported
}

// GetSessionGroups qBittorrent 无带宽组
func (c *Client) GetSessionGroups(ctx context.Context) ([]models.BandwidthGroup, error) {
	return nil, driver.ErrUnsupported
}

// SetSessionGroup qBittorrent 无带宽组
func (c *Client) SetSessionGroup(ctx context.Context, name string, fields map[string]any) error {
	return driver.ErrUnsupported
}

// SystemCommand 关闭 qBittorrent（reboot 无对应能力）
func (c *Client) SystemCommand(ctx context.Context, action string) error {
	switch action {
	case "shutdown":
		return c.post(ctx, "app/shutdown", url.Values{})
	case "reboot":
		return fmt.Errorf("qBittorrent 不支持重启，仅支持关闭")
	default:
		return fmt.Errorf("不支持的系统命令: %s", action)
	}
}

// ---- 工具 ----

// mapEncryption qBittorrent 加密策略（0 允许 / 1 强制 / 2 禁用）→ Transmission 语义
func mapEncryption(v int64) string {
	switch v {
	case 1:
		return "required"
	case 2:
		return "tolerated"
	default:
		return "preferred"
	}
}

// unmapEncryption Transmission 语义 → qBittorrent 数值
func unmapEncryption(v string) int64 {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "required":
		return 1
	case "tolerated":
		return 2
	default:
		return 0
	}
}

func splitTrackers(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if v := strings.TrimSpace(line); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func isStoppedState(state string) bool {
	switch state {
	case "pausedDL", "pausedUP", "stoppedDL", "stoppedUP", "error", "missingFiles":
		return true
	default:
		return false
	}
}

// int64From 从 maindata 的 any 取值（JSON 数字统一为 float64）
func int64From(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	default:
		return 0
	}
}
