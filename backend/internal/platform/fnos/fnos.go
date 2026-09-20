// Package fnos 飞牛 fnOS 宿主适配：统一网关集成、文件选择器路径、fpk 应用更新。
//
// 本包只在 init 中向 platform 注册自身，业务代码不直接依赖它。
// 主程序通过空导入（_ "…/internal/platform/fnos"）把该宿主接入，
// 不导入时服务自动降级为通用部署，飞牛专属能力全部不可用。
package fnos

import (
	"github.com/gin-gonic/gin"
	"github.com/sushazhi/seedark/backend/internal/platform"
)

// ID 平台标识
const ID = platform.IDFnOS

func init() {
	platform.Register(ID, New)
}

// Platform fnOS 宿主平台
type Platform struct {
	prefix     string
	fileAccess platform.FileAccess
	update     *updateHandler
	paths      *pathHandler
}

// New 构造 fnOS 平台
func New(cfg platform.Config) platform.Platform {
	p := &Platform{
		prefix: cfg.GatewayPrefix,
		// fnOS 文件选择器返回宿主语义路径，默认允许读取；
		// 配置 FILE_ALLOWED_PREFIXES 后可把范围收紧到指定目录，不影响其它能力。
		fileAccess: platform.NewFileAccess(true, cfg.FileAllowedPrefixes),
		update:     newUpdateHandler(),
		paths:      &pathHandler{tc: newTrimPathClient()},
	}
	return p
}

func (p *Platform) ID() string { return ID }

func (p *Platform) GatewayPrefix() string { return p.prefix }

func (p *Platform) SecurityPolicy() platform.SecurityPolicy {
	// 经 fnOS 统一网关部署时被 iframe 嵌入，且 Host 会被网关改写：
	// 放行嵌入并信任 X-Forwarded-Host 以还原真实主机做同源判定，
	// 访问控制由网关统一认证负责。直连部署（无前缀）则保持严格策略。
	gateway := p.prefix != ""
	return platform.SecurityPolicy{
		AllowEmbedding:        gateway,
		TrustForwardedHeaders: gateway,
		AllowedOrigins: []string{
			"http://localhost:5173",
			"http://127.0.0.1:5173",
		},
	}
}

func (p *Platform) FileAccess() platform.FileAccess { return p.fileAccess }

// RegisterRoutes 挂载 fnOS 专属接口：应用更新（GitHub Release + fpk）、语义路径转换
func (p *Platform) RegisterRoutes(g *gin.RouterGroup) {
	p.update.RegisterRoutes(g)
	g.POST("/paths/semantic", p.paths.convert)
}
