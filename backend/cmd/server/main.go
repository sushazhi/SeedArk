package main

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	static "github.com/sushazhi/seedark/backend"
	"github.com/sushazhi/seedark/backend/internal/api"
	"github.com/sushazhi/seedark/backend/internal/automove"
	"github.com/sushazhi/seedark/backend/internal/config"
	"github.com/sushazhi/seedark/backend/internal/driver"
	"github.com/sushazhi/seedark/backend/internal/mcpserver"
	"github.com/sushazhi/seedark/backend/internal/middleware"
	"github.com/sushazhi/seedark/backend/internal/platform"
	// 空导入即完成 fnOS 平台注册；不导入时服务自动降级为通用部署
	_ "github.com/sushazhi/seedark/backend/internal/platform/fnos"
	"github.com/sushazhi/seedark/backend/internal/rpc"
	"github.com/sushazhi/seedark/backend/internal/seedpolicy"
	"github.com/sushazhi/seedark/backend/internal/speedpolicy"
	"github.com/sushazhi/seedark/backend/internal/state"
)

func main() {
	// 加载配置
	cfg, err := config.Load()
	if err != nil {
		slog.Error("加载配置失败", "err", err)
		os.Exit(1)
	}
	setupLogLevel(cfg.LogLevel)

	// 创建下载器管理器（支持运行期热更新连接与多服务器聚合）
	kind := driver.NormalizeKind(cfg.TransmissionType)
	manager, err := rpc.NewManager(rpc.Credentials{
		Type: kind,
		URL:  cfg.TransmissionURL,
		User: cfg.User,
		Pass: cfg.Password,
	})
	if err != nil {
		slog.Error("初始化下载器客户端失败", "kind", kind, "err", err)
		os.Exit(1)
	}

	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()

	// 启动时检测连接
	if version, err := manager.Ping(ctx); err != nil {
		slog.Warn("下载器连接失败，请检查 SA_TYPE/SA_URL/SA_USER/SA_PASS", "kind", kind, "err", err)
	} else {
		slog.Info("已连接下载器", "kind", kind, "version", version)
	}

	// 宿主平台：把网关前缀、安全策略、文件访问白名单等环境差异收敛到一处。
	// 未显式指定时按 GATEWAY_PREFIX / 宿主注入的环境变量推断，默认通用部署。
	gatewayPrefix := cfg.GatewayPrefix
	if gatewayPrefix != "" && !gatewayPrefixRe.MatchString(gatewayPrefix) {
		slog.Error("GATEWAY_PREFIX 非法：必须是以 / 开头的纯路径段", "value", gatewayPrefix)
		os.Exit(1)
	}
	plat := platform.Detect(cfg.Platform, platform.Config{
		GatewayPrefix: gatewayPrefix,
		// 「按路径添加种子」允许读取的根目录：配置项 + 数据目录
		FileAllowedPrefixes: append(append([]string{}, cfg.TorrentPathRoots...), cfg.DataDir),
		// 平台落盘（更新包等）统一放数据目录，避免系统临时目录被预置软链
		DataDir: cfg.DataDir,
	})
	slog.Info("宿主平台已就绪", "platform", plat.ID(),
		"gatewayPrefix", gatewayPrefix, "registered", strings.Join(platform.Registered(), ","))

	// Gin 引擎
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	// 仅信任本机回环代理（fnOS 网关/反向代理均从回环接入）：直连部署下
	// 客户端伪造 X-Forwarded-For 即可污染审计日志中的客户端 IP
	_ = r.SetTrustedProxies([]string{"127.0.0.1", "::1"})
	r.Use(gin.Recovery(), middleware.Logger(), middleware.SecurityHeaders(plat), middleware.CORS(plat))

	// GeoIP 服务：优先加载手动放置的 mmdb/GeoLite2-City.mmdb；缺失时后台自动
	// 下载到数据目录（仅首次，约 60MB），下载完成前归属地显示为 -
	geo := api.NewGeoService("mmdb/GeoLite2-City.mmdb")
	go geo.EnsureAvailable(ctx, filepath.Join(cfg.DataDir, "mmdb", "GeoLite2-City.mmdb"))

	// 持久化状态（多服务器/自动文件管理/做种策略）
	store, err := state.Load(state.DefaultStatePath(cfg.DataDir))
	if err != nil {
		slog.Error("加载状态文件失败", "err", err)
		os.Exit(1)
	}

	// 注册 API 与 WebSocket
	hub := api.NewHub(manager, cfg.PollInterval, plat)
	moveSvc := automove.New(manager, store)
	policySvc := seedpolicy.New(manager, store)
	speedSvc := speedpolicy.New(manager, store)
	// MCP 运行期开关：设置界面修改后即时生效，下次启动的初值来自配置
	mcpCtl := &api.McpControl{}
	mcpCtl.Enabled.Store(cfg.MCPEnabled)
	mcpCtl.AllowDelete.Store(cfg.MCPAllowDelete)
	mcpCtl.AllowDangerous.Store(cfg.MCPAllowDangerous)
	if cfg.MCPToken != "" {
		t := cfg.MCPToken
		mcpCtl.Token.Store(&t)
	}
	handler := api.NewHandler(manager, hub, geo, store, moveSvc, policySvc, speedSvc, cfg, plat, mcpCtl)
	handler.Register(r, gatewayPrefix)
	// 按持久化的服务器列表建立聚合成员：配置了 2 台以上启用服务器时，
	// 面板进入聚合视图，Transmission 与 qBittorrent 可同时被管理
	handler.SyncAggregateTargets()
	hub.Start(ctx)
	go moveSvc.Run(ctx)
	go policySvc.Run(ctx)
	go speedSvc.Run(ctx)

	// MCP：把种子管理能力以工具形式暴露给 AI 客户端（streamable HTTP + 令牌鉴权）。
	// 路由常驻注册，gate 在鉴权之前拦截关闭状态——按 404 处理，不暴露端点存在性；
	// 接入令牌与 API_TOKEN 相互独立，仅作用于 /mcp，可在设置界面热更新
	mcpHandler := mcpserver.New(manager, policySvc, store, plat, &mcpCtl.AllowDelete, &mcpCtl.AllowDangerous, hub.Bump).Handler()
	mcpGuards := []gin.HandlerFunc{func(c *gin.Context) {
		if !mcpCtl.Enabled.Load() {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Next()
	}, middleware.SameOriginWriteGuard(plat), middleware.DynamicAuth(func() string {
		if t := mcpCtl.Token.Load(); t != nil {
			return *t
		}
		return ""
	})}
	r.Any(gatewayPrefix+"/mcp", append(mcpGuards, gin.WrapH(mcpHandler))...)
	if cfg.MCPEnabled {
		slog.Info("MCP 服务已启用", "endpoint", gatewayPrefix+"/mcp",
			"tokenAuth", cfg.MCPToken != "", "allowDelete", cfg.MCPAllowDelete,
			"allowDangerous", cfg.MCPAllowDangerous)
	}
	if cfg.MCPToken == "" && cfg.MCPEnabled {
		slog.Warn("MCP 未启用令牌鉴权，任何可达本服务的客户端均可通过 MCP 工具控制 Transmission")
	}

	// 内嵌前端静态资源（SPA）
	serveStatic(r, plat.GatewayPrefix())

	// HTTP 服务：默认 TCP 监听（SERVER_HOST/SERVER_PORT 控制）；
	// 网关模式通过 SERVER_SOCKET 指定 Unix socket（fnOS 统一网关入口）
	socketPath := os.Getenv("SERVER_SOCKET")
	srv := &http.Server{
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		// 只收紧 keep-alive 空闲连接；不设 ReadTimeout/WriteTimeout——
		// WebSocket 升级 hijack 连接后，残留的读写 deadline 会掐断长连接
		IdleTimeout: 120 * time.Second,
	}
	if socketPath != "" {
		_ = os.Remove(socketPath)
		ln, err := net.Listen("unix", socketPath)
		if err != nil {
			slog.Error("Unix socket 监听失败", "err", err)
			os.Exit(1)
		}
		// socket 权限：默认沿用 0666 以免网关以其它身份接入时断连，
		// 确认过属主/组关系后可用 SERVER_SOCKET_MODE=0660 收紧
		mode := socketMode()
		if mode&0o077 != 0 {
			slog.Warn("Unix socket 权限过宽，本机任意用户均可调用管理接口", "sock", socketPath, "mode", fmt.Sprintf("%04o", mode))
		}
		if err := os.Chmod(socketPath, mode); err != nil {
			slog.Warn("设置 socket 权限失败", "sock", socketPath, "err", err)
		}
		go func() {
			if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
				slog.Error("HTTP 服务启动失败", "err", err)
				os.Exit(1)
			}
		}()
		slog.Info("服务已启动", "sock", socketPath)
	} else {
		host := cfg.Host
		if !middleware.IsLoopback(host) && cfg.APIToken == "" && os.Getenv("ALLOW_INSECURE_REMOTE") != "1" {
			slog.Error("拒绝在非回环地址上无鉴权监听",
				"host", host,
				"hint", "本服务默认可被任意调用；如需对外暴露，请设置 API_TOKEN 启用令牌鉴权，或显式设置 ALLOW_INSECURE_REMOTE=1 确认自担风险")
			os.Exit(1)
		}
		if cfg.APIToken == "" {
			slog.Warn("未启用令牌鉴权，任何可达本端口的客户端均可完整控制 Transmission", "host", host)
		}
		srv.Addr = net.JoinHostPort(host, cfg.Port)
		go func() {
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("HTTP 服务启动失败", "err", err)
				os.Exit(1)
			}
		}()
		slog.Info("服务已启动", "addr", srv.Addr)
	}

	// MCP 专用直连端口：socket 部署下服务不监听 TCP，外部 AI 客户端又无法经宿主网关
	// 访问 /mcp；该端口单独暴露 MCP 端点（复用同一套开关与令牌守卫），不扩大界面 API 的暴露面
	var mcpSrv *http.Server
	if cfg.MCPPort != "" && cfg.MCPPort != "0" {
		if port, err := strconv.Atoi(cfg.MCPPort); err != nil || port < 1 || port > 65535 {
			slog.Error("MCP_PORT 非法：必须是 1-65535 的端口号", "value", cfg.MCPPort)
			os.Exit(1)
		}
		// 直连端口不经过宿主网关统一认证，其唯一边界就是接入令牌。
		// 无令牌时不监听该端口（而不是拒绝启动整个服务，避免把面板一起锁死），
		// 仅记错误日志给出指引，界面仍可用以便用户去补配令牌
		if mcpCtl.Token.Load() == nil {
			slog.Error("MCP 直连端口未开启：该端口必须配合令牌鉴权",
				"addr", net.JoinHostPort(cfg.Host, cfg.MCPPort),
				"hint", "请先在「设置 → 自动化 → MCP 服务」配置接入令牌，或清空 MCP_PORT")
		} else {
			mcpRouter := gin.New()
			mcpRouter.Use(gin.Recovery(), middleware.Logger())
			// 令牌可在设置界面被清空，故每次请求都要求令牌非空（而非仅在启动时检查一次）
			directGuards := make([]gin.HandlerFunc, 0, len(mcpGuards)+1)
			directGuards = append(directGuards, func(c *gin.Context) {
				if t := mcpCtl.Token.Load(); t == nil || strings.TrimSpace(*t) == "" {
					c.AbortWithStatus(http.StatusForbidden)
					return
				}
				c.Next()
			})
			directGuards = append(directGuards, mcpGuards...)
			mcpRouter.Any("/mcp", append(directGuards, gin.WrapH(mcpHandler))...)
			mcpSrv = &http.Server{Handler: mcpRouter, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
			mcpAddr := net.JoinHostPort(cfg.Host, cfg.MCPPort)
			mcpLn, err := net.Listen("tcp", mcpAddr)
			if err != nil {
				slog.Error("MCP 端口监听失败", "addr", mcpAddr, "err", err)
				os.Exit(1)
			}
			go func() {
				if err := mcpSrv.Serve(mcpLn); err != nil && err != http.ErrServerClosed {
					slog.Error("MCP 直连服务启动失败", "err", err)
				}
			}()
			slog.Info("MCP 直连端口已开启（强制令牌鉴权）", "addr", mcpLn.Addr().String())
		}
	}

	// 优雅退出
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("正在关闭服务...")
	// 通知后台任务停止（WS 轮询 / 自动文件管理 / 做种策略）
	cancelCtx()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	_ = srv.Shutdown(shutdownCtx)
	if mcpSrv != nil {
		_ = mcpSrv.Shutdown(shutdownCtx)
	}
}

// gatewayPrefixRe 网关前缀白名单：以 / 开头的纯路径段。
// 该值会被写入 index.html 的属性与重写后的资源 URL，故禁止引号、尖括号、空白与控制字符。
var gatewayPrefixRe = regexp.MustCompile(`^/[A-Za-z0-9._~/-]*$`)

// setupLogLevel 按 LOG_LEVEL 配置全局 slog（此前该配置项无人消费）
func setupLogLevel(level string) {
	var lvl slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	case "", "info":
		lvl = slog.LevelInfo
	default:
		slog.Warn("LOG_LEVEL 无法识别，按 info 处理", "value", level)
		lvl = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})))
}

// socketMode 解析 SERVER_SOCKET_MODE（八进制），缺省 0666 以兼容既有网关部署
func socketMode() fs.FileMode {
	raw := strings.TrimSpace(os.Getenv("SERVER_SOCKET_MODE"))
	if raw == "" {
		return 0o666
	}
	v, err := strconv.ParseUint(raw, 8, 32)
	if err != nil || v > 0o777 {
		slog.Warn("SERVER_SOCKET_MODE 非法，按默认值 0666 处理", "value", raw)
		return 0o666
	}
	return fs.FileMode(v)
}

// serveStatic 通过 embed 提供前端静态文件，并支持 SPA 路由回退
func serveStatic(r *gin.Engine, gatewayPrefix string) {
	sub, err := fs.Sub(static.StaticFiles, "web/dist")
	if err != nil {
		slog.Error("内嵌静态资源加载失败", "err", err)
		return
	}
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		slog.Error("读取 index.html 失败", "err", err)
		return
	}
	// 网关模式：注入前端基础路径（API/WS 拼接前缀使用）并重写 HTML 中的资源路径。
	// Vite base='./' 下 index.html 的 script/link 是相对路径（./assets/...），
	// 一旦文档 URL 不带结尾斜杠（/app/transmission）浏览器就会解析到上一级导致 404，
	// 因此两种写法（/assets 与 ./assets）都要显式拼上网关前缀。
	if gatewayPrefix != "" {
		// 前缀已在入口做过字符白名单校验，不可能出现引号/尖括号，故直接写入属性值
		inject := `<meta name="app-base" content="` + gatewayPrefix + `">`
		index = bytes.Replace(index, []byte("<head>"), []byte("<head>"+inject), 1)
		for _, attr := range []string{`src="`, `href="`} {
			// 顺序不可调换：先绝对路径再相对路径，否则后一轮会命中上一轮的产出而重复加前缀
			for _, val := range []string{`/`, `./`} {
				index = bytes.ReplaceAll(index, []byte(attr+val), []byte(attr+gatewayPrefix+`/`))
			}
		}
	}
	r.NoRoute(func(c *gin.Context) {
		// 网关模式下静态资源/SPA 路径带前缀，剥离后再按 embed 文件处理
		p := c.Request.URL.Path
		if gatewayPrefix != "" && strings.HasPrefix(p, gatewayPrefix) {
			p = strings.TrimPrefix(p, gatewayPrefix)
			if p == "" {
				p = "/"
			}
		}
		// API 未命中返回 JSON 404（避免前端拿到 HTML 造成解析错误）
		if strings.HasPrefix(p, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "接口不存在"})
			return
		}
		path := strings.TrimPrefix(p, "/")
		if path == "" {
			path = "index.html"
		}
		// 首页 / SPA 回退：一律使用重写后的 index（网关模式已注入前缀），
		// 否则会从 embed 返回原始文件导致资源路径无前缀而 404
		if path == "index.html" {
			c.Data(http.StatusOK, "text/html; charset=utf-8", index)
			return
		}
		if data, err := fs.ReadFile(sub, path); err == nil {
			ct := mime.TypeByExtension(filepath.Ext(path))
			if strings.HasSuffix(path, ".webmanifest") {
				ct = "application/manifest+json; charset=utf-8"
			} else if ct == "" {
				ct = "application/octet-stream"
			}
			c.Data(http.StatusOK, ct, data)
			return
		}
		// 静态资源未命中必须返回真 404，绝不能落到下面的 SPA 回退。
		//
		// 这是移动端"刷新即白屏"的成因：发布新版本后 index.html 里的资源哈希
		// 全部变化，持有旧 HTML 的客户端（SW 缓存 / HTTP 缓存 / 已装 PWA）仍会
		// 请求已删除的 /assets/index-OLDHASH.js。若此处返回 200 + HTML，
		// 浏览器把 HTML 当作 ES module 解析必然失败，#root 永远为空；
		// 更糟的是 SW 会把这份 200 的 HTML 当作该 JS 的合法响应写进缓存
		// （见 frontend/public/sw.js），此后即使服务端已修复，本地依然永久白屏。
		if isStaticAssetPath(path) {
			c.Status(http.StatusNotFound)
			return
		}
		// SPA 路由回退：仅对"看起来像前端路由"的路径生效（如 /torrents/123）
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	})
}

// isStaticAssetPath 判断路径是否属于构建产物（带哈希的资源、图标、清单等）。
// 这类路径都是文件而非前端路由，未命中即为真 404，不能回退到 index.html。
func isStaticAssetPath(p string) bool {
	if strings.HasPrefix(p, "assets/") {
		return true
	}
	// 其余带扩展名的请求同样按静态文件处理（favicon、sw.js、pwa-register.js…）；
	// 前端路由不含扩展名，因此这个判据不会误伤 SPA 深链
	return filepath.Ext(p) != ""
}
