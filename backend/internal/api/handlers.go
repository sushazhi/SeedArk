package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"github.com/sushazhi/seedark/backend/internal/automove"
	"github.com/sushazhi/seedark/backend/internal/config"
	"github.com/sushazhi/seedark/backend/internal/driver"
	"github.com/sushazhi/seedark/backend/internal/middleware"
	"github.com/sushazhi/seedark/backend/internal/models"
	"github.com/sushazhi/seedark/backend/internal/platform"
	"github.com/sushazhi/seedark/backend/internal/rpc"
	"github.com/sushazhi/seedark/backend/internal/seedpolicy"
	"github.com/sushazhi/seedark/backend/internal/speedpolicy"
	"github.com/sushazhi/seedark/backend/internal/state"
)

// McpControl MCP 服务的运行期开关：/mcp 路由常驻注册，由此控制是否服务请求，
// 设置界面修改后立即生效；下次启动的初值仍来自配置文件/环境变量。
// Token 为 MCP 接入令牌（nil 或空 = 不启用鉴权），设置界面可热更新
type McpControl struct {
	Enabled        atomic.Bool
	AllowDelete    atomic.Bool
	AllowDangerous atomic.Bool
	Token          atomic.Pointer[string]
}

// Handler API 处理器
type Handler struct {
	rpc          *rpc.Manager
	hub          *Hub
	geo          *GeoService
	state        *state.Store
	automove     *automove.Service
	seedpolicy   *seedpolicy.Service
	speedpolicy  *speedpolicy.Service
	plat         platform.Platform
	dataDir      string
	apiToken     string
	mcp          *McpControl
	pathMappings []models.PathMapping
	// wsTickets 一次性 WebSocket 握手票据（见 wsticket.go）
	wsTickets *wsTicketStore

	// MCP 直连端口：设置界面可改，读写跨请求并发，必须加锁
	mcpPortMu sync.RWMutex
	mcpPort   string

	// 后端建种任务表（见 createtorrent.go）
	createMu   sync.Mutex
	createJobs map[string]*createJob
}

// NewHandler 创建处理器。
// plat 提供宿主平台能力：本地文件读取白名单、同源判定策略、宿主专属路由（如 fnOS 应用更新）。
// mcp 为 MCP 服务的运行期开关，与 main 中 /mcp 路由的 gate 共享同一实例。
func NewHandler(manager *rpc.Manager, hub *Hub, geo *GeoService, st *state.Store, moveSvc *automove.Service, policySvc *seedpolicy.Service, speedSvc *speedpolicy.Service, cfg *config.Config, plat platform.Platform, mcp *McpControl) *Handler {
	return &Handler{
		rpc:          manager,
		hub:          hub,
		geo:          geo,
		state:        st,
		automove:     moveSvc,
		seedpolicy:   policySvc,
		speedpolicy:  speedSvc,
		plat:         plat,
		dataDir:      cfg.DataDir,
		apiToken:     cfg.APIToken,
		mcpPort:      strings.TrimSpace(cfg.MCPPort),
		mcp:          mcp,
		pathMappings: parsePathMappings(cfg.PathMappings),
		wsTickets:    newWSTicketStore(),
		createJobs:   make(map[string]*createJob),
	}
}

// getMCPPort / setMCPPort 读写 MCP 直连端口配置（读请求与设置保存并发）。
// "0" 与空等价，统一按空返回，避免前端把它当有效端口拼地址
func (h *Handler) getMCPPort() string {
	h.mcpPortMu.RLock()
	defer h.mcpPortMu.RUnlock()
	if h.mcpPort == "0" {
		return ""
	}
	return h.mcpPort
}

func (h *Handler) setMCPPort(port string) {
	h.mcpPortMu.Lock()
	h.mcpPort = port
	h.mcpPortMu.Unlock()
}

// credentialsOf 把持久化的一台服务器条目转成连接凭据
func credentialsOf(s state.Server) rpc.Credentials {
	return rpc.Credentials{
		Type: driver.NormalizeKind(s.Type),
		URL:  s.URL,
		User: s.User,
		Pass: s.Pass,
	}
}

// syncAggregateTargets 按持久化的服务器列表刷新聚合成员。
// 只要有 2 台以上启用的服务器，面板就进入「聚合视图」：种子列表与统计为
// 所有服务器合并结果，写操作按种子 ID 路由回各自的服务器。
func (h *Handler) SyncAggregateTargets() {
	st := h.state.Get()
	targets := make([]rpc.Target, 0, len(st.Servers))
	for i, s := range st.Servers {
		if !s.Enabled || s.URL == "" {
			continue
		}
		targets = append(targets, rpc.Target{
			Index:   i,
			Kind:    driver.NormalizeKind(s.Type),
			Name:    s.Name,
			URL:     s.URL,
			User:    s.User,
			Pass:    s.Pass,
			Enabled: true,
		})
	}
	// 只配了一台时关闭聚合：此时没有跨服务器冲突可言，
	// 沿用「单服务器」路径可以省掉一次并发拉取并保持原有 ID 语义
	if len(targets) < 2 {
		targets = nil
	}
	h.rpc.SetTargets(targets)
	if h.rpc.AggregateEnabled() {
		slog.Info("已启用多下载器聚合视图", "servers", len(h.rpc.AggregateIndexes()))
	}
}

// currentLocalSettings 汇总当前生效的连接与 MCP 配置。
// .env.local 是整文件重写，任何保存入口都必须携带全部受管键；集中在此构造可
// 避免某个入口漏传（切换服务器曾漏传 MCP_TOKEN，导致令牌被静默清空）。
func (h *Handler) currentLocalSettings(cred rpc.Credentials, poll string) config.LocalSettings {
	var token string
	if t := h.mcp.Token.Load(); t != nil {
		token = *t
	}
	return config.LocalSettings{
		Type:              cred.Type.String(),
		TransmissionURL:   cred.URL,
		User:              cred.User,
		Pass:              cred.Pass,
		PollInterval:      poll,
		MCPEnabled:        h.mcp.Enabled.Load(),
		MCPAllowDelete:    h.mcp.AllowDelete.Load(),
		MCPAllowDangerous: h.mcp.AllowDangerous.Load(),
		MCPToken:          token,
		MCPPort:           h.getMCPPort(),
	}
}

// parsePathMappings 解析「远端路径=本地路径」映射配置，非法项忽略并告警
func parsePathMappings(raw []string) []models.PathMapping {
	var out []models.PathMapping
	for _, item := range raw {
		from, to, found := strings.Cut(item, "=")
		from, to = strings.TrimSpace(from), strings.TrimSpace(to)
		if !found || from == "" || to == "" {
			slog.Warn("PATH_MAPPINGS 配置项非法，已忽略（应为 远端路径=本地路径）", "item", item)
			continue
		}
		out = append(out, models.PathMapping{From: from, To: to})
	}
	return out
}

// maxAPIBodySize /api 请求体整体上限（20MB）：最大单项是 10MB 的种子文件，留出 multipart 元数据余量
const maxAPIBodySize = 20 << 20

// Register 注册所有路由。
// 身份认证默认交由宿主网关或反向代理承担，但本服务仍强制校验写请求的同源性
// （否则任意网页都能以「简单请求」直接增删种子），并在配置了 API_TOKEN 时启用令牌鉴权。
// prefix：网关部署时传入（如 /app/transmission），路由直接挂在带前缀路径下；
// Gin 在中间件执行前即按原始 URL.Path 匹配路由，故不能在中间件里改前缀。
func (h *Handler) Register(r *gin.Engine, prefix string) {
	guard := []gin.HandlerFunc{middleware.BodyLimit(maxAPIBodySize), middleware.SameOriginWriteGuard(h.plat)}
	if h.apiToken != "" {
		guard = append(guard, middleware.Auth(h.apiToken))
	}
	api := r.Group(prefix+"/api", guard...)
	{
		// 种子
		api.GET("/torrents", h.listTorrents)
		api.GET("/torrents/sites", h.torrentSites)
		api.GET("/torrents/:id", h.getTorrent)
		api.POST("/torrents/add", h.addTorrent)
		api.POST("/torrents/add-batch", h.addTorrentBatch)
		// 后端建种（服务器路径 + 多线程哈希）。独立前缀，避免与 /torrents/:id 路由冲突
		api.POST("/torrent-create", h.createTorrent)
		api.GET("/torrent-create/:jobId", h.createTorrentStatus)
		api.GET("/torrent-create/:jobId/file", h.createTorrentFile)
		api.POST("/torrents/replace-tracker", h.replaceTracker)
		api.POST("/torrents/:id/start", h.startTorrent)
		api.POST("/torrents/:id/start-now", h.startNowTorrent)
		api.POST("/torrents/:id/stop", h.stopTorrent)
		api.POST("/torrents/:id/verify", h.verifyTorrent)
		api.POST("/torrents/:id/reannounce", h.reannounceTorrent)
		api.POST("/torrents/:id/move", h.moveTorrent)
		api.POST("/torrents/:id/rename", h.renameTorrent)
		api.POST("/torrents/:id/queue", h.queueMove)
		api.PUT("/torrents/:id", h.updateTorrent)
		api.DELETE("/torrents/:id", h.deleteTorrent)
		// 批量
		api.POST("/torrents/start", h.startTorrents)
		api.POST("/torrents/start-now", h.startNowTorrents)
		api.POST("/torrents/stop", h.stopTorrents)
		api.POST("/torrents/move", h.moveTorrents)
		api.POST("/torrents/update", h.updateTorrents)
		api.DELETE("/torrents", h.deleteTorrents)
		// 全部操作
		api.POST("/torrents/start-all", h.startAllTorrents)
		api.POST("/torrents/pause-all", h.pauseAllTorrents)
		api.POST("/torrents/reannounce-all", h.reannounceAllTorrents)
		// 会话
		api.GET("/session", h.getSession)
		api.PUT("/session", h.setSession)
		api.GET("/session/status", h.sessionStatus)
		api.GET("/session/stats", h.sessionStats)
		api.GET("/session/port-test", h.portTest)
		api.POST("/session/blocklist/update", h.blocklistUpdate)
		api.GET("/session/free-space", h.freeSpace)
		// 带宽组（Transmission 4.x group-get / group-set）
		api.GET("/session/groups", h.listGroups)
		api.PUT("/session/groups", h.saveGroup)
		// 连接配置（界面设置）
		api.GET("/settings", h.getSettings)
		api.PUT("/settings", h.updateSettings)
		// 多服务器管理
		api.GET("/servers", h.listServers)
		api.POST("/servers", h.saveServers)
		api.DELETE("/servers/:index", h.deleteServer)
		api.POST("/servers/switch", h.switchServer)
		// 指定服务器的会话配置（聚合视图下设置面板按服务器标签各读各的）
		api.GET("/servers/:index/session", h.getServerSession)
		api.PUT("/servers/:index/session", h.setServerSession)
		// 指定服务器的磁盘余量（聚合视图下侧栏逐台显示）
		api.GET("/servers/:index/free-space", h.serverFreeSpace)
		// 指定服务器的带宽组（「其他属性」按种子所属服务器读组列表）
		api.GET("/servers/:index/groups", h.serverGroups)
		// 自动文件管理
		api.GET("/automove", h.listAutoMoveRules)
		api.POST("/automove", h.saveAutoMoveRule)
		api.DELETE("/automove/:id", h.deleteAutoMoveRule)
		api.POST("/automove/run", h.runAutoMove)
		// 做种策略（按站点的分享率目标与达标动作）
		api.GET("/seedpolicy", h.listSeedPolicy)
		api.POST("/seedpolicy", h.saveSeedPolicyRule)
		api.DELETE("/seedpolicy/:id", h.deleteSeedPolicyRule)
		api.POST("/seedpolicy/guard", h.saveSeedPolicyGuard)
		api.POST("/seedpolicy/run", h.runSeedPolicy)
		api.POST("/seedpolicy/reset", h.resetSeedPolicy)
		api.POST("/seedpolicy/clear-logs", h.clearSeedPolicyLogs)
		// 组内总限速（按站点/标签/名称分组的带宽上限）
		api.GET("/speedpolicy", h.listSpeedPolicy)
		api.POST("/speedpolicy", h.saveSpeedPolicyRule)
		api.DELETE("/speedpolicy/:id", h.deleteSpeedPolicyRule)
		api.POST("/speedpolicy/guard", h.saveSpeedPolicyGuard)
		api.POST("/speedpolicy/run", h.runSpeedPolicy)
		// Peer 地理位置
		api.POST("/peers/geo", h.lookupPeers)
		// 远端→本地路径映射（打开目录/复制路径时的展示转换）
		api.GET("/paths/map", h.listPathMappings)
		// 系统命令
		api.POST("/system/:action", h.systemCommand)
		// WebSocket 握手票据：用已鉴权的普通请求换取，避免长期令牌出现在握手查询串
		api.POST("/ws-ticket", h.issueWsTicket)
	}
	// 宿主平台专属接口（如 fnOS 的应用更新；通用平台为空实现，不挂载任何路由）
	h.plat.RegisterRoutes(api)
	// WebSocket：浏览器无法为握手设置自定义请求头，鉴权走一次性票据（见 wsGuard）
	r.GET(prefix+"/ws", h.wsGuard(), h.hub.HandleWS)
}

// respond 成功响应
func respond(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, models.OK(data))
}

// respondError 失败响应。
// 统一经 rpc.SanitizeClientMsg 处理：上游 RPC 错误可能内嵌带凭据的地址，不做脱敏即等于泄露密码。
func respondError(c *gin.Context, status int, msg string) {
	c.JSON(status, models.Error(rpc.SanitizeClientMsg(msg)))
}

// respondBackendError 下载器调用失败的统一响应。
// 「该下载器不支持此能力」是能力差异，不是上游故障：用 501 并给出说明，
// 让界面能据此隐藏入口，而不是弹一个看起来像连接断开的红色错误。
// 「提交的参数不合法」（如偏好键名拼错、取值越界）同理，用 400 直说。
func respondBackendError(c *gin.Context, action string, err error) {
	if errors.Is(err, driver.ErrUnsupported) {
		c.JSON(http.StatusNotImplemented, models.Error(rpc.SanitizeClientMsg(action+"："+err.Error())))
		return
	}
	if errors.Is(err, driver.ErrInvalid) {
		respondError(c, http.StatusBadRequest, action+"："+err.Error())
		return
	}
	respondError(c, http.StatusBadGateway, action+": "+err.Error())
}

// persistState 执行状态修改并持久化。
// 磁盘写入失败（磁盘满、IO 错误等）时内存中的修改依然生效，但重启后会丢失：
// 记录告警并返回提示文案（空串表示成功），由调用方把 warning 附加到响应，
// 避免接口照常返回成功、用户误以为规则已可靠保存。
func (h *Handler) persistState(fn func(*state.State)) string {
	if err := h.state.Update(fn); err != nil {
		slog.Warn("状态持久化失败，重启后将丢失本次修改", "err", err)
		return "修改已生效，但写入磁盘失败：" + err.Error()
	}
	return ""
}
