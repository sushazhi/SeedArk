// Package platform 把「部署环境相关的能力」从核心业务中剥离，
// 使本项目既能作为飞牛 fnOS 应用运行，也能直接跑在 Docker / 物理机 / 任意 Linux 上。
//
// 核心业务（RPC、自动文件管理、做种策略、WebSocket 等）只依赖本包定义的接口，
// 具体宿主在子包中实现接口并通过 Register 注册；未注册的平台一律回退到
// 最小权限的通用实现（generic），新增一套宿主不需要改动任何业务代码。
package platform

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

// 内置平台标识
const (
	IDGeneric = "generic" // 通用部署：Docker / 物理机 / 任意 Linux 直连
	IDFnOS    = "fnos"    // 飞牛 fnOS 应用：统一网关 + 文件选择器 + fpk 更新
)

// hostRe 仅接受标准主机名/域名（可带端口），避免把请求头内容拼进响应头
var hostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,252})(:[0-9]{1,5})?$`)

// Config 平台初始化参数。
// 平台自身不读环境变量，全部由主程序从服务配置传入，便于测试与复用。
type Config struct {
	// GatewayPrefix 宿主反向网关挂载的 URL 前缀（如 /app/transmission），直连部署为空
	GatewayPrefix string
	// FileAllowedPrefixes 允许「按路径添加种子」读取的目录前缀（含数据目录）。
	// 为空表示关闭该能力，避免接口沦为任意文件读取入口。
	FileAllowedPrefixes []string
	// DataDir 服务数据目录（默认 ~/.seedark）。平台专属的落盘（如更新包）
	// 必须放在这里：系统临时目录所有本机用户可写，固定文件名 + 可预置软链
	// 会让服务以自身权限覆盖任意文件。
	DataDir string
}

// SecurityPolicy 安全响应头与同源判定策略。
// 不同宿主的部署形态不同：直连部署应当严格；经宿网关转发时需放行嵌入并信任转发头。
type SecurityPolicy struct {
	// AllowEmbedding 允许被 iframe 嵌入（宿主通过网关 / iframe 集成时开启）
	AllowEmbedding bool
	// TrustForwardedHeaders 信任 X-Forwarded-Host。
	// 请求经宿主网关或可信反代转发时 Host 会被改写，真实主机只能从转发头取。
	TrustForwardedHeaders bool
	// AllowedOrigins 额外放行的 Origin 完整值（如开发前端源 http://localhost:5173）
	AllowedOrigins []string
}

// RequestHost 返回本次请求对外可见的主机（含端口）；缺失或格式异常时返回空串
func (p SecurityPolicy) RequestHost(r *http.Request) string {
	if p.TrustForwardedHeaders {
		// 仅网关/可信反代会设置 X-Forwarded-Host，取最左侧一级
		if xfh := r.Header.Get("X-Forwarded-Host"); xfh != "" {
			if host := strings.TrimSpace(strings.Split(xfh, ",")[0]); hostRe.MatchString(host) {
				return host
			}
		}
	}
	if hostRe.MatchString(r.Host) {
		return r.Host
	}
	return ""
}

// IsSameSiteOrigin 判断浏览器 Origin 是否指向本服务自身（或白名单中的开发前端源）
func (p SecurityPolicy) IsSameSiteOrigin(r *http.Request, origin string) bool {
	for _, allowed := range p.AllowedOrigins {
		if strings.EqualFold(origin, allowed) {
			return true
		}
	}
	host := p.RequestHost(r)
	if host == "" || origin == "" {
		return false
	}
	// Origin 形如 scheme://host（无路径），逐字符比较前先剥掉协议
	_, after, ok := strings.Cut(origin, "://")
	if !ok {
		return false
	}
	originHost := after
	if i := strings.IndexByte(originHost, '/'); i >= 0 {
		originHost = originHost[:i]
	}
	return strings.EqualFold(originHost, host)
}

// ContentSecurityPolicy 生成 CSP；host 为本次请求对外可见主机（可能来自 X-Forwarded-Host）
func (p SecurityPolicy) ContentSecurityPolicy(host string) string {
	parts := []string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
		"font-src 'self' https://fonts.gstatic.com data:",
		"img-src 'self' data: blob:",
		"manifest-src 'self'",
		"base-uri 'self'",
		"form-action 'self'",
		"object-src 'none'",
	}
	// connect-src 追加同源 ws/wss：CSP3 下 'self' 已涵盖同源 WebSocket，
	// 但为兼容旧浏览器显式列出同源主机，避免退化成放行任意主机的 ws:/wss: 。
	if host != "" {
		parts = append(parts, "connect-src 'self' ws://"+host+" wss://"+host)
	} else {
		parts = append(parts, "connect-src 'self'")
	}
	if !p.AllowEmbedding {
		parts = append(parts, "frame-ancestors 'none'")
	}
	return strings.Join(parts, "; ")
}

// FileAccess 「按路径添加种子」与「后端建种」的本地文件读取策略。
// 默认实现拒绝一切读取，避免把服务变成任意文件读取接口。
type FileAccess interface {
	// Enabled 宿主是否支持按路径读取种子文件
	Enabled() bool
	// AllowRead 校验是否允许读取该路径，返回解析过符号链接后的真实路径。
	// 调用方必须打开返回的路径而非原始入参，否则校验的是一个路径、读取的是另一个路径。
	// 仅允许普通文件。
	AllowRead(path string) (string, error)
	// AllowReadDir 校验是否允许以目录为单位读取（后端建种场景），
	// 返回解析过符号链接后的真实目录路径。仅允许普通目录。
	AllowReadDir(path string) (string, error)
}

// Platform 宿主平台。新增宿主只需实现本接口并在 init 中调用 Register。
type Platform interface {
	// ID 平台标识
	ID() string
	// GatewayPrefix 宿网关挂载的 URL 前缀，直连部署为空
	GatewayPrefix() string
	// SecurityPolicy 安全响应头与同源判定策略
	SecurityPolicy() SecurityPolicy
	// FileAccess 本地文件读取策略
	FileAccess() FileAccess
	// RegisterRoutes 注册平台专属路由（如 fnOS 的应用更新），无专属能力时为空实现
	RegisterRoutes(g *gin.RouterGroup)
}
