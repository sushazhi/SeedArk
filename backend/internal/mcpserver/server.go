// Package mcpserver 把 trpanel 的种子管理能力以 MCP（Model Context Protocol）工具
// 暴露给 AI 客户端：streamable HTTP 传输，鉴权复用与 REST 相同的令牌机制（中间件层处理）。
package mcpserver

import (
	"net/http"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/trpanel/backend/internal/driver"
	"github.com/trpanel/backend/internal/platform"
	"github.com/trpanel/backend/internal/rpc"
	"github.com/trpanel/backend/internal/seedpolicy"
	"github.com/trpanel/backend/internal/state"
)

// Server MCP 工具服务：复用 RPC 管理器、做种策略引擎与文件访问白名单
type Server struct {
	manager *rpc.Manager
	policy  *seedpolicy.Service
	store   *state.Store
	plat    platform.Platform
	// allowDelete 控制删除类工具是否可用（高危操作，默认关闭）；
	// 指向共享的原子开关，设置界面修改后无需重启即生效
	allowDelete *atomic.Bool
	// allowDangerous 控制移动 / 重命名 / 立即执行做种策略等其它高危工具（默认关闭）
	allowDangerous *atomic.Bool
	// onMutation 写操作成功后回调（触发 WebSocket 立即刷新），可为 nil
	onMutation func()
	// mu 保护 cache
	mu sync.Mutex
	// cache 按驱动签名缓存工具服务实例（见 server()）
	cache map[driverSig]*mcp.Server
}

// New 创建 MCP 服务
func New(manager *rpc.Manager, policy *seedpolicy.Service, store *state.Store, plat platform.Platform, allowDelete, allowDangerous *atomic.Bool, onMutation func()) *Server {
	return &Server{
		manager:        manager,
		policy:         policy,
		store:          store,
		plat:           plat,
		allowDelete:    allowDelete,
		allowDangerous: allowDangerous,
		onMutation:     onMutation,
		cache:          map[driverSig]*mcp.Server{},
	}
}

// Handler 返回可挂载到任意 HTTP 路由的 streamable HTTP 处理器。
// 采用 stateless + JSON 响应：本服务只提供工具调用、无服务端主动通知，
// 无会话握手对简单客户端与调试最友好（GET 长连接 / DELETE 会话返回 405 属预期）。
// 工具清单随当前下载器自适应，而 stateless 模式下 SDK 每个请求都要向工厂取一次
// 实例，故这里走 server() 取实例而不是复用固定的一份
func (s *Server) Handler() http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.server() }, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	})
}

// driverSig 工具注册依赖的驱动特征：下载器类型 + 能力自述（均为可比较值，直接作缓存键）
type driverSig struct {
	kind driver.Kind
	caps driver.Capabilities
}

// server 取当前驱动对应的工具服务实例。
// 注册工具要为每个入参结构体反射生成 JSON Schema，而 stateless 模式下每次请求都会
// 重新取实例，不缓存等于每次工具调用都重算一遍全量工具定义；切换下载器后签名变化，
// 自动按新驱动的能力重建清单
func (s *Server) server() *mcp.Server {
	sig := driverSig{kind: s.manager.Kind(), caps: s.manager.Capabilities()}
	s.mu.Lock()
	defer s.mu.Unlock()
	if srv, ok := s.cache[sig]; ok {
		return srv
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "trpanel", Version: version()}, nil)
	registerTools(srv, s, sig)
	s.cache[sig] = srv
	return srv
}

// bump 通知前端立即刷新（写操作之后）
func (s *Server) bump() {
	if s.onMutation != nil {
		s.onMutation()
	}
}

// version 取模块版本（go build 携带模块信息时有效），兜底 dev
func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
