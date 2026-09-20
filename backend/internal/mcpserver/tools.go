package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/trpanel/backend/internal/driver"
	"github.com/trpanel/backend/internal/models"
	"github.com/trpanel/backend/internal/rpc"
)

// registerTools 按当前下载器注册工具。
// 分级原则：只读工具全量提供；添加/启停默认开放；删除必须显式配置 mcp_allow_delete。
// 当前下载器没有的能力（见 driver.Capabilities）不注册：AI 客户端看不到用不上的工具，
// 而不是调用后才收到「不支持」。描述中的「下载器」指当前连接的 Transmission / qBittorrent，
// 行为有差异的工具按驱动追加说明（见 qbNote）
func registerTools(srv *mcp.Server, s *Server, sig driverSig) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_torrents",
		Description: "列出下载器中的种子，支持按关键词 / 状态 / 站点过滤；返回摘要字段，尺寸单位为字节",
	}, s.listTorrents)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_torrent",
		Description: "获取单个种子详情，含文件列表 / Peers / Tracker 状态",
	}, s.getTorrent)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_stats",
		Description: "获取下载器会话统计：版本、种子数量、当前与累计上传 / 下载量",
	}, s.getStats)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_seed_policy_report",
		Description: "trpanel 做种策略报告：列出已配置规则，以及当前已达标但尚未处理的种子与达标依据；只读评估，不会执行暂停或删除",
	}, s.getSeedPolicyReport)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "add_torrent",
		Description: "添加种子：磁力链接 / .torrent 的 http(s) URL / 下载器所在主机上的 .torrent 路径（受文件白名单限制）。默认以暂停状态添加",
	}, s.addTorrent)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "start_torrents",
		Description: "按 ID 批量开始种子（下载或做种）",
	}, s.startTorrents)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "stop_torrents",
		Description: "按 ID 批量暂停种子",
	}, s.stopTorrents)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "remove_torrents",
		Description: "按 ID 批量删除种子；deleteData=true 时同时删除本地文件（不可恢复）。需服务端显式开启",
	}, s.removeTorrents)

	// 只读：会话与诊断
	addToolIf(srv, sig.caps.FreeSpace, &mcp.Tool{
		Name: "get_free_space",
		Description: "查询下载器主机某目录的剩余空间与总容量；不传 path 时查询全局下载目录（添加种子前预判磁盘是否够用）" +
			qbNote(sig, "qBittorrent 只提供全局下载目录的剩余空间：path 参数被忽略且拿不到总容量"),
	}, s.getFreeSpace)
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_session_config",
		Description: "获取下载器会话配置摘要：版本、下载目录、速度限制、队列、加密、端口与黑名单等" +
			qbNote(sig, "qBittorrent 没有黑名单 / 脚本钩子 / 全局连接数等概念，对应字段返回空值或近似值（如备用限速开关由定时调度近似）"),
	}, s.getSessionConfig)
	addToolIf(srv, sig.caps.PortTest, &mcp.Tool{
		Name:        "test_port",
		Description: "测试 Transmission 监听端口能否从外网访问（排查下载无连接）",
	}, s.testPort)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_torrent_sites",
		Description: "列出全部种子的站点归属：站点名到种子数汇总，以及每个种子对应的站点列表",
	}, s.getTorrentSites)

	// 写操作（默认开放）
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "verify_torrents",
		Description: "按 ID 批量触发本地数据校验（排查下载卡住 / 数据损坏）",
	}, s.verifyTorrents)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "reannounce_torrents",
		Description: "按 ID 批量向 Tracker 重新宣告（排查 Tracker 连接失败）",
	}, s.reannounceTorrents)
	mcp.AddTool(srv, &mcp.Tool{
		Name: "set_torrent_labels",
		Description: "按 ID 批量设置种子标签（整体覆盖现有标签）" +
			qbNote(sig, "qBittorrent 下标签即其「标签」概念，分类不受影响"),
	}, s.setTorrentLabels)
	mcp.AddTool(srv, &mcp.Tool{
		Name: "set_torrent_limits",
		Description: "按 ID 批量设置种子限速：上传 / 下载上限（KB/s）及是否启用、是否遵循全局限速" +
			qbNote(sig, "qBittorrent 无带宽优先级，且忽略 honorsSessionLimits（跟随全局限速）"),
	}, s.setTorrentLimits)
	addToolIf(srv, sig.caps.QueueMove, &mcp.Tool{
		Name:        "queue_move",
		Description: "按 ID 批量调整队列顺序：top / up / down / bottom",
	}, s.queueMove)
	addToolIf(srv, sig.caps.Blocklist, &mcp.Tool{
		Name:        "update_blocklist",
		Description: "更新黑名单规则（需 Transmission 已启用黑名单），返回规则数",
	}, s.updateBlocklist)
	mcp.AddTool(srv, &mcp.Tool{
		Name: "set_session_config",
		Description: "修改下载器会话设置：下载目录（仅影响之后新添加的种子）、限速（全局 / 备用含定时）、队列、网络（端口 / 加密 / DHT / PEX 等）、做种策略默认值、黑名单、磁盘缓存等；未提供的字段保持不变" +
			qbNote(sig, "qBittorrent 下黑名单、脚本钩子、未完成文件重命名、回收源文件、队列停滞、全局连接数、uTP 开关等 Transmission 专属项会被忽略"),
	}, s.setSessionConfig)

	// 高危操作（需服务端显式开启「允许通过 MCP 执行高危操作」）
	mcp.AddTool(srv, &mcp.Tool{
		Name: "move_torrents",
		Description: "按 ID 批量移动种子数据到新目录（可实际搬移本地文件，影响较大）。需服务端显式开启" +
			qbNote(sig, "qBittorrent 只能连同文件一起搬移，不支持 move=false 仅更新指向"),
	}, s.moveTorrents)
	addToolIf(srv, sig.caps.RenameFile, &mcp.Tool{
		Name:        "rename_file",
		Description: "重命名种子内的文件或目录（影响做种完整性，慎用）。需服务端显式开启",
	}, s.renameFile)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "execute_seed_policy",
		Description: "立即执行一轮做种策略：对已达标种子按规则暂停或删除（含删数据）。需服务端显式开启",
	}, s.executeSeedPolicy)

	// 通用透传：Transmission 官方 RPC 方法白名单（qBittorrent 走 REST，语义无法一一对应，不注册）。
	// 只读方法直接可用；写方法受「允许通过 MCP 执行高危操作」管控；敏感方法（关停会话 / 宿主机脚本）一律屏蔽
	addToolIf(srv, sig.kind == driver.KindTransmission, &mcp.Tool{
		Name:        "transmission_api_request",
		Description: "Transmission RPC 通用透传：直接调用官方 RPC 白名单方法（含客户端库未封装的能力），返回原始 arguments。只读方法（torrent-get / session-get 等）直接可用；写方法（torrent-set / torrent-add / session-set 等）需服务端开启「允许通过 MCP 执行高危操作」；关停会话与脚本类配置永久屏蔽",
	}, s.transmissionAPIRequest)
}

// addToolIf 按当前下载器是否具备该能力决定是否注册。
// 不注册的工具不会出现在工具清单里，AI 客户端也就不会去调用一个注定返回「不支持」的工具
func addToolIf[In any](srv *mcp.Server, supported bool, tool *mcp.Tool, fn func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, any, error)) {
	if !supported {
		return
	}
	mcp.AddTool(srv, tool, fn)
}

// qbNote 追加 qBittorrent 下的行为差异说明；Transmission 下返回空串（其描述本身即完整语义）
func qbNote(sig driverSig, text string) string {
	if sig.kind != driver.KindQBittorrent {
		return ""
	}
	return "。" + text
}

// emptyIn 无入参工具的占位
type emptyIn struct{}

// ---- list_torrents ----

type listTorrentsIn struct {
	Search string `json:"search,omitempty" jsonschema:"关键词过滤：大小写不敏感的子串，匹配名称 / 哈希 / 磁力链接"`
	Status string `json:"status,omitempty" jsonschema:"状态过滤：downloading(下载中) / seeding(做种中) / paused(已暂停) / checking(校验中) / finished(已完成) / error(出错)"`
	Site   string `json:"site,omitempty" jsonschema:"按 tracker 主机名过滤，大小写不敏感子串匹配"`
	Limit  int    `json:"limit,omitempty" jsonschema:"最多返回条数，默认 50，上限 500"`
}

// statusGroups 状态过滤到 Transmission 状态码的映射
var statusGroups = map[string][]int64{
	"downloading": {3, 4},
	"seeding":     {5, 6},
	"paused":      {0},
	"checking":    {1, 2},
}

// statusText Transmission 状态码的可读文本
var statusText = map[int64]string{
	0: "stopped", 1: "checkWait", 2: "checking",
	3: "downloadWait", 4: "downloading", 5: "seedWait", 6: "seeding",
}

func (s *Server) listTorrents(ctx context.Context, _ *mcp.CallToolRequest, in listTorrentsIn) (*mcp.CallToolResult, any, error) {
	status := strings.ToLower(strings.TrimSpace(in.Status))
	if status != "" && status != "finished" && status != "error" && statusGroups[status] == nil {
		return nil, nil, fmt.Errorf("status 无效: %q，可选 downloading/seeding/paused/checking/finished/error", in.Status)
	}
	torrents, err := s.manager.GetTorrents(ctx)
	if err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	search := strings.ToLower(strings.TrimSpace(in.Search))
	siteNeedle := strings.ToLower(strings.TrimSpace(in.Site))
	total := 0
	briefs := make([]map[string]any, 0, limit)
	for _, t := range torrents {
		if t == nil || !torrentMatches(t, status, search, siteNeedle) {
			continue
		}
		total++
		if len(briefs) < limit {
			briefs = append(briefs, torrentBrief(t))
		}
	}
	out := map[string]any{"total": total, "returned": len(briefs), "torrents": briefs}
	if total > len(briefs) {
		out["note"] = fmt.Sprintf("匹配 %d 条，仅返回前 %d 条；可用 search/status 收窄或调大 limit", total, limit)
	}
	return nil, out, nil
}

// torrentMatches 依次套用状态 / 关键词 / 站点过滤，空条件视为不限制
func torrentMatches(t *models.Torrent, status, search, site string) bool {
	switch {
	case status == "finished":
		if !t.IsFinished {
			return false
		}
	case status == "error":
		if t.Error == 0 {
			return false
		}
	default:
		if group := statusGroups[status]; group != nil {
			ok := false
			for _, v := range group {
				if t.Status == v {
					ok = true
					break
				}
			}
			if !ok {
				return false
			}
		}
	}
	if search != "" &&
		!strings.Contains(strings.ToLower(t.Name), search) &&
		!strings.Contains(strings.ToLower(t.HashString), search) &&
		!strings.Contains(strings.ToLower(t.MagnetLink), search) {
		return false
	}
	if site != "" {
		hit := false
		for _, ts := range t.TrackerStats {
			if strings.Contains(strings.ToLower(ts.Host), site) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

// torrentBrief 单个种子的摘要输出
func torrentBrief(t *models.Torrent) map[string]any {
	return map[string]any{
		"id": t.ID, "name": t.Name, "hash": t.HashString,
		"status": statusText[t.Status], "percentDone": t.PercentDone,
		"totalSize": t.TotalSize, "leftUntilDone": t.LeftUntilDone,
		"rateDownload": t.RateDownload, "rateUpload": t.RateUpload,
		"uploadedEver": t.UploadedEver, "uploadRatio": t.UploadRatio,
		"secondsSeeding": t.SecondsSeeding, "eta": t.ETA,
		"isFinished": t.IsFinished, "isStalled": t.IsStalled, "isPrivate": t.IsPrivate,
		"error": t.Error, "errorString": t.ErrorString,
		"labels": t.Labels, "queuePosition": t.QueuePosition,
		"downloadDir": t.DownloadDir, "site": primaryHost(t.TrackerStats),
		"addedDate": t.AddedDate, "doneDate": t.DoneDate,
	}
}

// primaryHost 取主 tracker 主机名
func primaryHost(stats []models.TrackerStat) string {
	for _, ts := range stats {
		if !ts.IsBackup && ts.Host != "" {
			return ts.Host
		}
	}
	if len(stats) > 0 {
		return stats[0].Host
	}
	return ""
}

// ---- get_torrent ----

type idIn struct {
	ID int64 `json:"id" jsonschema:"种子 ID（list_torrents 返回的 id）"`
}

func (s *Server) getTorrent(ctx context.Context, _ *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, any, error) {
	t, err := s.manager.GetTorrentDetail(ctx, in.ID)
	if err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	// 块位图是 base64 大字段，对 AI 决策无意义
	t.Pieces = ""
	return nil, t, nil
}

// ---- get_stats ----

func (s *Server) getStats(ctx context.Context, _ *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, any, error) {
	stats, err := s.manager.GetSessionStats(ctx)
	if err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	sess, err := s.manager.GetSession(ctx)
	if err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	return nil, map[string]any{
		"version":            sess.Version,
		"torrentCount":       stats.TorrentCount,
		"activeTorrentCount": stats.ActiveTorrentCount,
		"pausedTorrentCount": stats.PausedTorrentCount,
		"downloadSpeed":      stats.DownloadSpeed,
		"uploadSpeed":        stats.UploadSpeed,
		"cumulative":         stats.Cumulative,
		"current":            stats.Current,
	}, nil
}

// ---- get_seed_policy_report ----

func (s *Server) getSeedPolicyReport(ctx context.Context, _ *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, any, error) {
	entries, enforce, err := s.policy.Preview(ctx)
	if err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	st := s.store.Get()
	mode := "preview"
	if enforce {
		mode = "execute"
	}
	rules := make([]map[string]any, 0, len(st.SeedPolicyRules))
	for _, r := range st.SeedPolicyRules {
		rules = append(rules, map[string]any{
			"id": r.ID, "name": r.Name, "enabled": r.Enabled, "action": r.Action,
			"minRatio": r.MinRatio, "minSeedDays": r.MinSeedDays, "minUploadGB": r.MinUploadGB,
			"sites": r.Sites, "labels": r.Labels, "nameMatch": r.NameMatch,
		})
	}
	out := map[string]any{"mode": mode, "rules": rules, "matched": len(entries), "items": entries}
	if len(st.SeedPolicyRules) == 0 {
		out["note"] = "尚未配置做种策略规则"
	}
	return nil, out, nil
}

// ---- add_torrent ----

type addTorrentIn struct {
	Link        string   `json:"link,omitempty" jsonschema:"磁力链接或 .torrent 的 http(s) URL；与 path 二选一"`
	Path        string   `json:"path,omitempty" jsonschema:"Transmission 所在主机上 .torrent 文件的绝对路径（受文件读取白名单限制）；与 link 二选一"`
	DownloadDir string   `json:"downloadDir,omitempty" jsonschema:"下载目录，留空使用 Transmission 全局默认目录"`
	Paused      *bool    `json:"paused,omitempty" jsonschema:"是否以暂停状态添加；默认 true（安全默认），添加后用 start_torrents 启动"`
	Labels      []string `json:"labels,omitempty" jsonschema:"标签列表"`
}

func (s *Server) addTorrent(ctx context.Context, _ *mcp.CallToolRequest, in addTorrentIn) (*mcp.CallToolResult, any, error) {
	if in.Link != "" && in.Path != "" {
		return nil, nil, errors.New("link 与 path 只能提供一个")
	}
	paused := true
	if in.Paused != nil {
		paused = *in.Paused
	}
	var (
		id  int64
		err error
	)
	switch {
	case in.Path != "":
		data, readErr := s.readTorrentFile(in.Path)
		if readErr != nil {
			slog.Warn("MCP 按路径添加种子被拒", "path", in.Path, "err", readErr)
			return nil, nil, errors.New("种子路径不可用")
		}
		id, err = s.manager.AddTorrentByFile(ctx, data, in.DownloadDir, paused, in.Labels, nil, nil, nil)
	case in.Link != "":
		link := strings.TrimSpace(in.Link)
		// 与 REST 的按链接添加一致：Transmission 的 filename 支持本地路径，
		// 不限制 scheme 即可借它的权限读任意文件、绕过文件白名单
		if !rpc.ValidTorrentLink(link) {
			return nil, nil, errors.New("link 仅支持 http(s) 链接或磁力链接")
		}
		id, err = s.manager.AddTorrentByURL(ctx, link, in.DownloadDir, paused, in.Labels, nil)
	default:
		return nil, nil, errors.New("缺少 link 或 path")
	}
	if err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	s.bump()
	out := map[string]any{"id": id, "paused": paused}
	if paused {
		out["note"] = "种子已按暂停添加，需调用 start_torrents 启动"
	}
	return nil, out, nil
}

// readTorrentFile 按宿主机路径读取种子内容，白名单口径与 REST 的按路径添加一致
func (s *Server) readTorrentFile(path string) ([]byte, error) {
	target, err := s.plat.FileAccess().AllowRead(path)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(target)
}

// ---- start / stop / remove ----

type idsIn struct {
	IDs []int64 `json:"ids" jsonschema:"种子 ID 列表（来自 list_torrents）"`
}

func (s *Server) startTorrents(ctx context.Context, _ *mcp.CallToolRequest, in idsIn) (*mcp.CallToolResult, any, error) {
	if len(in.IDs) == 0 {
		return nil, nil, errors.New("缺少 ids")
	}
	if err := s.manager.StartTorrents(ctx, in.IDs); err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	s.bump()
	return nil, map[string]any{"ids": in.IDs, "action": "started"}, nil
}

func (s *Server) stopTorrents(ctx context.Context, _ *mcp.CallToolRequest, in idsIn) (*mcp.CallToolResult, any, error) {
	if len(in.IDs) == 0 {
		return nil, nil, errors.New("缺少 ids")
	}
	if err := s.manager.StopTorrents(ctx, in.IDs); err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	s.bump()
	return nil, map[string]any{"ids": in.IDs, "action": "stopped"}, nil
}

type removeTorrentsIn struct {
	IDs        []int64 `json:"ids" jsonschema:"种子 ID 列表"`
	DeleteData bool    `json:"deleteData,omitempty" jsonschema:"true 时同时删除已下载的本地文件（不可恢复）"`
}

func (s *Server) removeTorrents(ctx context.Context, _ *mcp.CallToolRequest, in removeTorrentsIn) (*mcp.CallToolResult, any, error) {
	if !s.allowDelete.Load() {
		return nil, nil, errors.New("删除功能未开放：需在本产品的设置界面开启「允许通过 MCP 删除种子」")
	}
	if len(in.IDs) == 0 {
		return nil, nil, errors.New("缺少 ids")
	}
	if err := s.manager.RemoveTorrents(ctx, in.IDs, in.DeleteData); err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	s.bump()
	slog.Info("MCP 删除种子", "ids", in.IDs, "deleteData", in.DeleteData)
	return nil, map[string]any{"ids": in.IDs, "deleted": true, "deleteData": in.DeleteData}, nil
}

// ---- get_free_space ----

type getFreeSpaceIn struct {
	Path string `json:"path,omitempty" jsonschema:"要查询的目录；留空使用全局下载目录"`
}

func (s *Server) getFreeSpace(ctx context.Context, _ *mcp.CallToolRequest, in getFreeSpaceIn) (*mcp.CallToolResult, any, error) {
	path := strings.TrimSpace(in.Path)
	if path == "" {
		sess, err := s.manager.GetSession(ctx)
		if err != nil {
			return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
		}
		if sess.DownloadDir == "" {
			return nil, nil, errors.New("未提供 path，且全局下载目录为空")
		}
		path = sess.DownloadDir
	}
	free, total, err := s.manager.GetFreeSpace(ctx, path)
	if err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	out := map[string]any{"path": path, "freeSpace": free, "totalSize": total}
	// qBittorrent 忽略 path（只有全局下载目录的剩余空间），如实说明而不是让它看起来像按目录查的
	if in.Path != "" && s.manager.Kind() == driver.KindQBittorrent {
		out["note"] = "qBittorrent 只提供全局下载目录的剩余空间，该结果与传入 path 无关"
	}
	return nil, out, nil
}

// ---- get_session_config ----

func (s *Server) getSessionConfig(ctx context.Context, _ *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, any, error) {
	sess, err := s.manager.GetSession(ctx)
	if err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	return nil, map[string]any{
		"version":               sess.Version,
		"downloadDir":           sess.DownloadDir,
		"incompleteDir":         sess.IncompleteDir,
		"incompleteDirEnabled":  sess.IncompleteDirEnabled,
		"speedLimitUp":          sess.SpeedLimitUp,
		"speedLimitUpOn":        sess.SpeedLimitUpOn,
		"speedLimitDown":        sess.SpeedLimitDown,
		"speedLimitDownOn":      sess.SpeedLimitDownOn,
		"altSpeedUp":            sess.AltSpeedUp,
		"altSpeedDown":          sess.AltSpeedDown,
		"altSpeedEnabled":       sess.AltSpeedEnabled,
		"peerLimitGlobal":       sess.PeerLimitGlobal,
		"peerPort":              sess.PeerPort,
		"portForwardingEnabled": sess.PortForwardingEnabled,
		"encryption":            sess.Encryption,
		"downloadQueueEnabled":  sess.DownloadQueueEnabled,
		"downloadQueueSize":     sess.DownloadQueueSize,
		"seedQueueEnabled":      sess.SeedQueueEnabled,
		"seedQueueSize":         sess.SeedQueueSize,
		"blocklistEnabled":      sess.BlocklistEnabled,
		"blocklistURL":          sess.BlocklistURL,
		"blocklistSize":         sess.BlocklistSize,
		"startAdded":            sess.StartAdded,
	}, nil
}

// ---- test_port ----

func (s *Server) testPort(ctx context.Context, _ *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, any, error) {
	open, err := s.manager.TestPort(ctx)
	if err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	return nil, map[string]any{"open": open}, nil
}

// ---- get_torrent_sites ----

func (s *Server) getTorrentSites(ctx context.Context, _ *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, any, error) {
	m, err := s.manager.GetTorrentSites(ctx)
	if err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	counts := map[string]int{}
	byTorrent := make(map[int64][]string, len(m))
	for id, sites := range m {
		byTorrent[id] = sites
		for _, site := range sites {
			counts[site]++
		}
	}
	return nil, map[string]any{"counts": counts, "byTorrent": byTorrent}, nil
}

// ---- verify / reannounce ----

func (s *Server) verifyTorrents(ctx context.Context, _ *mcp.CallToolRequest, in idsIn) (*mcp.CallToolResult, any, error) {
	if len(in.IDs) == 0 {
		return nil, nil, errors.New("缺少 ids")
	}
	if err := s.manager.VerifyTorrents(ctx, in.IDs); err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	s.bump()
	return nil, map[string]any{"ids": in.IDs, "action": "verify"}, nil
}

func (s *Server) reannounceTorrents(ctx context.Context, _ *mcp.CallToolRequest, in idsIn) (*mcp.CallToolResult, any, error) {
	if len(in.IDs) == 0 {
		return nil, nil, errors.New("缺少 ids")
	}
	if err := s.manager.ReannounceTorrents(ctx, in.IDs); err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	s.bump()
	return nil, map[string]any{"ids": in.IDs, "action": "reannounce"}, nil
}

// ---- set_torrent_labels ----

type setLabelsIn struct {
	IDs    []int64  `json:"ids" jsonschema:"种子 ID 列表（来自 list_torrents）"`
	Labels []string `json:"labels" jsonschema:"标签列表（整体覆盖现有标签；传空数组即清空）"`
}

func (s *Server) setTorrentLabels(ctx context.Context, _ *mcp.CallToolRequest, in setLabelsIn) (*mcp.CallToolResult, any, error) {
	if len(in.IDs) == 0 {
		return nil, nil, errors.New("缺少 ids")
	}
	if err := s.manager.SetTorrent(ctx, in.IDs, driver.TorrentPatch{Labels: in.Labels}); err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	s.bump()
	return nil, map[string]any{"ids": in.IDs, "labels": in.Labels}, nil
}

// ---- set_torrent_limits ----

type setLimitsIn struct {
	IDs                 []int64 `json:"ids" jsonschema:"种子 ID 列表（来自 list_torrents）"`
	UploadLimit         *int64  `json:"uploadLimit,omitempty" jsonschema:"上传速度上限（KB/s）"`
	DownloadLimit       *int64  `json:"downloadLimit,omitempty" jsonschema:"下载速度上限（KB/s）"`
	UploadLimited       *bool   `json:"uploadLimited,omitempty" jsonschema:"是否启用上传限速"`
	DownloadLimited     *bool   `json:"downloadLimited,omitempty" jsonschema:"是否启用下载限速"`
	HonorsSessionLimits *bool   `json:"honorsSessionLimits,omitempty" jsonschema:"是否遵循全局速度限制"`
}

func (s *Server) setTorrentLimits(ctx context.Context, _ *mcp.CallToolRequest, in setLimitsIn) (*mcp.CallToolResult, any, error) {
	if len(in.IDs) == 0 {
		return nil, nil, errors.New("缺少 ids")
	}
	if in.UploadLimit == nil && in.DownloadLimit == nil && in.UploadLimited == nil &&
		in.DownloadLimited == nil && in.HonorsSessionLimits == nil {
		return nil, nil, errors.New("至少提供一项要修改的限速字段")
	}
	payload := driver.TorrentPatch{
		UploadLimit:         in.UploadLimit,
		DownloadLimit:       in.DownloadLimit,
		UploadLimited:       in.UploadLimited,
		DownloadLimited:     in.DownloadLimited,
		HonorsSessionLimits: in.HonorsSessionLimits,
	}
	if err := s.manager.SetTorrent(ctx, in.IDs, payload); err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	s.bump()
	return nil, map[string]any{"ids": in.IDs, "action": "set-limits"}, nil
}

// ---- queue_move ----

type queueMoveIn struct {
	IDs       []int64 `json:"ids" jsonschema:"种子 ID 列表（来自 list_torrents）"`
	Direction string  `json:"direction" jsonschema:"移动方向：top / up / down / bottom"`
}

func (s *Server) queueMove(ctx context.Context, _ *mcp.CallToolRequest, in queueMoveIn) (*mcp.CallToolResult, any, error) {
	if len(in.IDs) == 0 {
		return nil, nil, errors.New("缺少 ids")
	}
	dir := strings.ToLower(strings.TrimSpace(in.Direction))
	switch dir {
	case "top", "up", "down", "bottom":
	default:
		return nil, nil, fmt.Errorf("direction 无效: %q，可选 top/up/down/bottom", in.Direction)
	}
	if err := s.manager.QueueMove(ctx, in.IDs, dir); err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	s.bump()
	return nil, map[string]any{"ids": in.IDs, "direction": dir}, nil
}

// ---- update_blocklist ----

func (s *Server) updateBlocklist(ctx context.Context, _ *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, any, error) {
	n, err := s.manager.UpdateBlocklist(ctx)
	if err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	return nil, map[string]any{"ruleCount": n}, nil
}

// ---- set_session_config ----

// setSessionIn 覆盖 session-set 的常用可写项（刻意排除只读字段与脚本类字段）。
// 全部可选，未提供的字段不会下发，保持 Transmission 现值不变
type setSessionIn struct {
	// 下载目录与新增行为
	DownloadDir          string `json:"downloadDir,omitempty" jsonschema:"全局下载目录（仅影响之后新添加的种子；已有种子移动目录请用 move_torrents）"`
	IncompleteDir        string `json:"incompleteDir,omitempty" jsonschema:"未完成任务临时目录（需 incompleteDirEnabled=true 才生效）"`
	IncompleteDirEnabled *bool  `json:"incompleteDirEnabled,omitempty" jsonschema:"是否启用未完成临时目录"`
	RenamePartialFiles   *bool  `json:"renamePartialFiles,omitempty" jsonschema:"未完成的文件是否追加 .part 后缀"`
	StartAdded           *bool  `json:"startAdded,omitempty" jsonschema:"新种子添加后是否自动开始"`
	TrashOriginalTorrent *bool  `json:"trashOriginalTorrent,omitempty" jsonschema:"添加后是否删除原始 .torrent 文件"`
	// 限速（KB/s）：全局与备用（乌龟模式）
	SpeedLimitUp        *int64 `json:"speedLimitUp,omitempty" jsonschema:"全局上传限速（KB/s），需 speedLimitUpOn=true 生效"`
	SpeedLimitUpOn      *bool  `json:"speedLimitUpOn,omitempty" jsonschema:"是否启用全局上传限速"`
	SpeedLimitDown      *int64 `json:"speedLimitDown,omitempty" jsonschema:"全局下载限速（KB/s），需 speedLimitDownOn=true 生效"`
	SpeedLimitDownOn    *bool  `json:"speedLimitDownOn,omitempty" jsonschema:"是否启用全局下载限速"`
	AltSpeedUp          *int64 `json:"altSpeedUp,omitempty" jsonschema:"备用限速上传（KB/s）"`
	AltSpeedDown        *int64 `json:"altSpeedDown,omitempty" jsonschema:"备用限速下载（KB/s）"`
	AltSpeedEnabled     *bool  `json:"altSpeedEnabled,omitempty" jsonschema:"是否启用备用限速（乌龟模式）"`
	AltSpeedTimeEnabled *bool  `json:"altSpeedTimeEnabled,omitempty" jsonschema:"是否按时间段自动切换备用限速"`
	AltSpeedTimeBegin   *int64 `json:"altSpeedTimeBegin,omitempty" jsonschema:"备用限速开始时间（午夜起分钟数 0-1439）"`
	AltSpeedTimeEnd     *int64 `json:"altSpeedTimeEnd,omitempty" jsonschema:"备用限速结束时间（午夜起分钟数 0-1439）"`
	AltSpeedTimeDay     *int64 `json:"altSpeedTimeDay,omitempty" jsonschema:"备用限速生效日（位掩码，全部为 127）"`
	// 队列
	DownloadQueueEnabled *bool  `json:"downloadQueueEnabled,omitempty" jsonschema:"是否限制同时下载数"`
	DownloadQueueSize    *int64 `json:"downloadQueueSize,omitempty" jsonschema:"最大同时下载数"`
	SeedQueueEnabled     *bool  `json:"seedQueueEnabled,omitempty" jsonschema:"是否限制同时做种数"`
	SeedQueueSize        *int64 `json:"seedQueueSize,omitempty" jsonschema:"最大同时做种数"`
	QueueStalledEnabled  *bool  `json:"queueStalledEnabled,omitempty" jsonschema:"是否把长时间无流量的种子视为停滞（不计入队列）"`
	QueueStalledMinutes  *int64 `json:"queueStalledMinutes,omitempty" jsonschema:"判定停滞的空闲分钟数"`
	// 网络与隐私
	PeerLimitGlobal       *int64 `json:"peerLimitGlobal,omitempty" jsonschema:"全局最大 Peer 数"`
	PeerLimitPerTorrent   *int64 `json:"peerLimitPerTorrent,omitempty" jsonschema:"单个种子最大 Peer 数"`
	PeerPort              *int64 `json:"peerPort,omitempty" jsonschema:"Peer 监听端口（1-65535）"`
	PeerPortRandomOnStart *bool  `json:"peerPortRandomOnStart,omitempty" jsonschema:"启动时是否随机选择端口"`
	PortForwardingEnabled *bool  `json:"portForwardingEnabled,omitempty" jsonschema:"是否启用 UPnP / NAT-PMP 端口转发"`
	PEXEnabled            *bool  `json:"pexEnabled,omitempty" jsonschema:"是否启用 PEX（私有种子无效）"`
	DHTEnabled            *bool  `json:"dhtEnabled,omitempty" jsonschema:"是否启用 DHT（私有种子无效）"`
	LPDEnabled            *bool  `json:"lpdEnabled,omitempty" jsonschema:"是否启用本地 Peer 发现（私有种子无效）"`
	UTPEnabled            *bool  `json:"utpEnabled,omitempty" jsonschema:"是否启用 uTP"`
	Encryption            string `json:"encryption,omitempty" jsonschema:"加密策略：required / preferred / tolerated"`
	// 做种策略默认值
	SeedRatioLimit          *float64 `json:"seedRatioLimit,omitempty" jsonschema:"全局做种分享率上限，需 seedRatioLimited=true 生效"`
	SeedRatioLimited        *bool    `json:"seedRatioLimited,omitempty" jsonschema:"是否启用全局做种分享率上限"`
	IdleSeedingLimit        *int64   `json:"idleSeedingLimit,omitempty" jsonschema:"做种空闲多少分钟后自动停止，需 idleSeedingLimitEnabled=true 生效"`
	IdleSeedingLimitEnabled *bool    `json:"idleSeedingLimitEnabled,omitempty" jsonschema:"是否启用做种空闲停止"`
	// 黑名单与性能
	BlocklistEnabled *bool  `json:"blocklistEnabled,omitempty" jsonschema:"是否启用黑名单"`
	BlocklistURL     string `json:"blocklistURL,omitempty" jsonschema:"黑名单规则文件地址"`
	CacheSizeMB      *int64 `json:"cacheSizeMB,omitempty" jsonschema:"磁盘缓存大小（MB）"`
}

func (s *Server) setSessionConfig(ctx context.Context, _ *mcp.CallToolRequest, in setSessionIn) (*mcp.CallToolResult, any, error) {
	payload := driver.SessionPatch{
		DownloadDir:               optStr(in.DownloadDir),
		IncompleteDir:             optStr(in.IncompleteDir),
		IncompleteDirEnabled:      in.IncompleteDirEnabled,
		RenamePartialFiles:        in.RenamePartialFiles,
		StartAdded:                in.StartAdded,
		TrashOriginalTorrentFiles: in.TrashOriginalTorrent,
		SpeedLimitUp:              in.SpeedLimitUp,
		SpeedLimitUpOn:            in.SpeedLimitUpOn,
		SpeedLimitDown:            in.SpeedLimitDown,
		SpeedLimitDownOn:          in.SpeedLimitDownOn,
		AltSpeedUp:                in.AltSpeedUp,
		AltSpeedDown:              in.AltSpeedDown,
		AltSpeedEnabled:           in.AltSpeedEnabled,
		AltSpeedTimeEnabled:       in.AltSpeedTimeEnabled,
		AltSpeedTimeBegin:         in.AltSpeedTimeBegin,
		AltSpeedTimeEnd:           in.AltSpeedTimeEnd,
		AltSpeedTimeDay:           in.AltSpeedTimeDay,
		DownloadQueueEnabled:      in.DownloadQueueEnabled,
		DownloadQueueSize:         in.DownloadQueueSize,
		SeedQueueEnabled:          in.SeedQueueEnabled,
		SeedQueueSize:             in.SeedQueueSize,
		QueueStalledEnabled:       in.QueueStalledEnabled,
		QueueStalledMinutes:       in.QueueStalledMinutes,
		PeerLimitGlobal:           in.PeerLimitGlobal,
		PeerLimitPerTorrent:       in.PeerLimitPerTorrent,

		PeerPort:                in.PeerPort,
		PeerPortRandomOnStart:   in.PeerPortRandomOnStart,
		PortForwardingEnabled:   in.PortForwardingEnabled,
		PEXEnabled:              in.PEXEnabled,
		DHTEnabled:              in.DHTEnabled,
		LPDEnabled:              in.LPDEnabled,
		UTPEnabled:              in.UTPEnabled,
		SeedRatioLimit:          in.SeedRatioLimit,
		SeedRatioLimited:        in.SeedRatioLimited,
		IdleSeedingLimit:        in.IdleSeedingLimit,
		IdleSeedingLimitEnabled: in.IdleSeedingLimitEnabled,
		BlocklistEnabled:        in.BlocklistEnabled,
		BlocklistURL:            optStr(in.BlocklistURL),
		CacheSizeMB:             in.CacheSizeMB,
	}
	if in.Encryption != "" {
		enc := strings.ToLower(strings.TrimSpace(in.Encryption))
		switch enc {
		case "required", "preferred", "tolerated":
			payload.Encryption = &enc
		default:
			return nil, nil, fmt.Errorf("encryption 无效: %q，可选 required/preferred/tolerated", in.Encryption)
		}
	}
	if in.PeerPort != nil && (*in.PeerPort < 1 || *in.PeerPort > 65535) {
		return nil, nil, errors.New("peerPort 必须在 1-65535 之间")
	}
	if !anyFieldSet(in) {
		return nil, nil, errors.New("至少提供一项要修改的配置")
	}
	if err := s.manager.SetSession(ctx, payload); err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	s.bump()
	return nil, map[string]any{"updated": true}, nil
}

// optStr 空串转 nil，避免空值覆盖 Transmission 现有配置
func optStr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// anyFieldSet 入参里是否有任一字段提供了非零值（指针非 nil / 字符串非空）。
// 入参结构体含切片字段不可用 == 比较，故用反射实现
func anyFieldSet(v any) bool {
	rv := reflect.ValueOf(v)
	for i := 0; i < rv.NumField(); i++ {
		f := rv.Field(i)
		switch f.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func, reflect.Interface:
			if !f.IsNil() {
				return true
			}
		default:
			if !f.IsZero() {
				return true
			}
		}
	}
	return false
}

// ---- 高危操作（需 allowDangerous）----

// requireDangerous 校验高危操作开关；返回空串表示放行
func (s *Server) requireDangerous() error {
	if !s.allowDangerous.Load() {
		return errors.New("高危操作未开放：需在本产品的设置界面开启「允许通过 MCP 执行高危操作」")
	}
	return nil
}

type moveTorrentsIn struct {
	IDs      []int64 `json:"ids" jsonschema:"种子 ID 列表（来自 list_torrents）"`
	Location string  `json:"location" jsonschema:"目标目录（Transmission 主机上的绝对路径）"`
	Move     *bool   `json:"move,omitempty" jsonschema:"true 时把本地文件搬移到新目录（默认 true）；false 仅更新种子记录的路径"`
}

func (s *Server) moveTorrents(ctx context.Context, _ *mcp.CallToolRequest, in moveTorrentsIn) (*mcp.CallToolResult, any, error) {
	if err := s.requireDangerous(); err != nil {
		return nil, nil, err
	}
	if len(in.IDs) == 0 {
		return nil, nil, errors.New("缺少 ids")
	}
	if strings.TrimSpace(in.Location) == "" {
		return nil, nil, errors.New("缺少 location")
	}
	move := true
	if in.Move != nil {
		move = *in.Move
	}
	failed := make([]int64, 0)
	for _, id := range in.IDs {
		if err := s.manager.SetTorrentLocation(ctx, id, in.Location, move); err != nil {
			slog.Warn("MCP 移动种子失败", "id", id, "err", err)
			failed = append(failed, id)
		}
	}
	s.bump()
	slog.Info("MCP 移动种子", "ids", in.IDs, "location", in.Location, "move", move, "failed", failed)
	out := map[string]any{"ids": in.IDs, "location": in.Location, "move": move, "failed": failed}
	if len(failed) > 0 {
		out["note"] = "部分种子移动失败，见 failed 列表"
	}
	return nil, out, nil
}

type renameFileIn struct {
	ID   int64  `json:"id" jsonschema:"种子 ID（list_torrents 返回的 id）"`
	Path string `json:"path" jsonschema:"要重命名的文件 / 目录在种子内的相对路径；空串表示重命名整个种子根目录"`
	Name string `json:"name" jsonschema:"新的名称（不含路径分隔符）"`
}

func (s *Server) renameFile(ctx context.Context, _ *mcp.CallToolRequest, in renameFileIn) (*mcp.CallToolResult, any, error) {
	if err := s.requireDangerous(); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(in.Name) == "" || strings.ContainsAny(in.Name, "/\\") {
		return nil, nil, errors.New("name 不能为空且不能包含路径分隔符")
	}
	if err := s.manager.RenameFile(ctx, in.ID, in.Path, in.Name); err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	s.bump()
	slog.Info("MCP 重命名种子文件", "id", in.ID, "path", in.Path, "name", in.Name)
	return nil, map[string]any{"id": in.ID, "path": in.Path, "name": in.Name}, nil
}

func (s *Server) executeSeedPolicy(ctx context.Context, _ *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, any, error) {
	if err := s.requireDangerous(); err != nil {
		return nil, nil, err
	}
	enforced := s.store.Get().SeedPolicyGuard.Enforce
	result, err := s.policy.Tick(ctx)
	if err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	slog.Info("MCP 执行做种策略", "enforced", enforced, "result", result)
	out := map[string]any{"enforced": enforced, "result": result}
	if !enforced {
		out["note"] = "做种策略保护栏未开启，本轮仅评估并刷新待办清单，未执行任何动作"
	} else if result.Deleted > 0 {
		out["note"] = "本轮已按规则删除种子（含按规则配置的删数据），详情见执行记录"
	}
	return nil, out, nil
}

// ---- transmission_api_request（通用透传）----

// transmissionReadMethods Transmission 官方 RPC 中的只读方法：免开关直接可用
var transmissionReadMethods = map[string]bool{
	"torrent-get":   true,
	"session-get":   true,
	"session-stats": true,
	"free-space":    true,
	"port-test":     true,
	"group-get":     true,
}

// transmissionWriteMethods Transmission 官方 RPC 中的写方法：需 allowDangerous 高危开关
var transmissionWriteMethods = map[string]bool{
	"torrent-add":          true,
	"torrent-set":          true,
	"torrent-start":        true,
	"torrent-start-now":    true,
	"torrent-stop":         true,
	"torrent-remove":       true,
	"torrent-reannounce":   true,
	"torrent-verify":       true,
	"torrent-set-location": true,
	"torrent-rename-path":  true,
	"session-set":          true,
	"group-set":            true,
	"blocklist-update":     true,
	"queue-move-top":       true,
	"queue-move-up":        true,
	"queue-move-down":      true,
	"queue-move-bottom":    true,
}

// sessionSetBlockedKeys session-set 中可在 Transmission 宿主机执行任意命令的脚本类键，透传时屏蔽
var sessionSetBlockedKeys = map[string]bool{
	"script-torrent-added-filename":        true,
	"script-torrent-done-filename":         true,
	"script-torrent-done-seeding-filename": true,
}

type apiRequestIn struct {
	Method string         `json:"method" jsonschema:"Transmission RPC 方法名（官方白名单），如 torrent-get / session-get / torrent-set / queue-move-top"`
	Args   map[string]any `json:"args,omitempty" jsonschema:"RPC arguments 对象，键使用 Transmission 官方下划线命名（如 ids / fields / download-dir）；留空表示无参数"`
}

func (s *Server) transmissionAPIRequest(ctx context.Context, _ *mcp.CallToolRequest, in apiRequestIn) (*mcp.CallToolResult, any, error) {
	if s.manager.Kind() != driver.KindTransmission {
		return nil, nil, fmt.Errorf("原始 RPC 透传仅支持 Transmission，当前连接的是 %s", s.manager.Kind().Label())
	}
	method := strings.ToLower(strings.TrimSpace(in.Method))
	switch {
	case method == "":
		return nil, nil, errors.New("缺少 method")
	case method == "session-close":
		return nil, nil, errors.New("session-close 属敏感操作（关停 Transmission 会话），已永久屏蔽")
	case transmissionReadMethods[method]:
		// 只读放行
	case transmissionWriteMethods[method]:
		if err := s.requireDangerous(); err != nil {
			return nil, nil, err
		}
		if method == "session-set" {
			for k := range in.Args {
				if sessionSetBlockedKeys[strings.ToLower(k)] {
					return nil, nil, fmt.Errorf("session-set 的 %q 属敏感配置（可在宿主机执行脚本），已屏蔽", k)
				}
			}
		}
	default:
		return nil, nil, fmt.Errorf("不支持的 RPC 方法: %q（仅允许官方白名单方法，详见工具描述）", in.Method)
	}
	args := in.Args
	if args == nil {
		args = map[string]any{}
	}
	out, err := s.manager.RawCall(ctx, method, args)
	if err != nil {
		return nil, nil, errors.New(rpc.SanitizeClientMsg(err.Error()))
	}
	if !transmissionReadMethods[method] {
		slog.Info("MCP 透传 RPC 写操作", "method", method, "args", args)
		s.bump()
	}
	return nil, map[string]any{"method": method, "result": sanitizeRPCResult(out)}, nil
}

// sanitizeRPCResult 递归裁剪对 AI 决策无意义的大字段（torrent-get 的 pieces 为 base64 块位图）
func sanitizeRPCResult(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if k == "pieces" {
				t[k] = "<base64 块位图已省略>"
				continue
			}
			t[k] = sanitizeRPCResult(val)
		}
		return t
	case []any:
		for i, item := range t {
			t[i] = sanitizeRPCResult(item)
		}
		return t
	default:
		return v
	}
}
