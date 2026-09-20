package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/trpanel/backend/internal/config"
	"github.com/trpanel/backend/internal/driver"
	"github.com/trpanel/backend/internal/rpc"
)

// getSettings 获取当前连接配置（不返回明文密码）与 MCP 服务开关。
// MCP 接入令牌明文返回：它只用于外部 AI 客户端接入（能力面小于管理口令），
// 且用户需要复制到 AI 客户端配置里，不回显就无法接入
func (h *Handler) getSettings(c *gin.Context) {
	cred := h.rpc.Credentials()
	var mcpToken string
	if t := h.mcp.Token.Load(); t != nil {
		mcpToken = *t
	}
	respond(c, gin.H{
		"type":              cred.Type.String(),
		"url":               cred.URL,
		"user":              cred.User,
		"pollInterval":      h.hub.getPollInterval().String(),
		"mcpEnabled":        h.mcp.Enabled.Load(),
		"mcpAllowDelete":    h.mcp.AllowDelete.Load(),
		"mcpAllowDangerous": h.mcp.AllowDangerous.Load(),
		"mcpToken":          mcpToken,
		"mcpPort":           h.getMCPPort(),
	})
}

// updateSettings 更新设置：连接配置（带 URL 时）与 MCP 开关（提供字段时）可分别提交，
// 持久化统一合并当前生效值整文件写入 .env.local，两类设置互不覆盖
func (h *Handler) updateSettings(c *gin.Context) {
	var body struct {
		Type              string  `json:"type"`
		URL               string  `json:"url"`
		User              string  `json:"user"`
		Pass              string  `json:"pass"`
		PollInterval      string  `json:"pollInterval"`
		MCPEnabled        *bool   `json:"mcpEnabled"`
		MCPAllowDelete    *bool   `json:"mcpAllowDelete"`
		MCPAllowDangerous *bool   `json:"mcpAllowDangerous"`
		MCPToken          *string `json:"mcpToken"`
		MCPPort           *string `json:"mcpPort"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	if body.URL == "" && body.PollInterval == "" && body.MCPEnabled == nil && body.MCPAllowDelete == nil && body.MCPAllowDangerous == nil && body.MCPToken == nil && body.MCPPort == nil {
		respondError(c, http.StatusBadRequest, "没有要保存的设置")
		return
	}
	if body.MCPToken != nil {
		*body.MCPToken = strings.TrimSpace(*body.MCPToken)
		if err := config.ValidateEnvValue("接入令牌", *body.MCPToken); err != nil {
			respondError(c, http.StatusBadRequest, err.Error())
			return
		}
	}
	if body.MCPPort != nil {
		*body.MCPPort = strings.TrimSpace(*body.MCPPort)
		// "0" 与空等价：都表示关闭直连端口（配置文件里也有人写成 0）
		if *body.MCPPort == "0" {
			*body.MCPPort = ""
		}
		if *body.MCPPort != "" {
			if p, err := strconv.Atoi(*body.MCPPort); err != nil || p < 1 || p > 65535 {
				respondError(c, http.StatusBadRequest, "MCP 直连端口必须是 1-65535 的端口号")
				return
			}
		}
	}

	// 先算出 MCP 各项的「目标值」（未提交的类目沿用当前生效值），在改动内存之前完成校验：
	// 校验失败时不能留下「内存已改、文件未改」的半成品状态
	targetEnabled := h.mcp.Enabled.Load()
	if body.MCPEnabled != nil {
		targetEnabled = *body.MCPEnabled
	}
	targetAllowDelete := h.mcp.AllowDelete.Load()
	if body.MCPAllowDelete != nil {
		targetAllowDelete = *body.MCPAllowDelete
	}
	targetAllowDangerous := h.mcp.AllowDangerous.Load()
	if body.MCPAllowDangerous != nil {
		targetAllowDangerous = *body.MCPAllowDangerous
	}
	var targetToken string
	if t := h.mcp.Token.Load(); t != nil {
		targetToken = *t
	}
	if body.MCPToken != nil {
		targetToken = *body.MCPToken
	}
	targetPort := h.getMCPPort()
	if body.MCPPort != nil {
		targetPort = *body.MCPPort
	}
	// 直连端口不经过宿主网关认证，其唯一边界就是接入令牌：
	// 拒绝「端口开启但无令牌」的组合，否则该端口要么静默不监听（用户以为配好了），
	// 要么在无鉴权状态下暴露完整的管理能力。
	// 仅在本次确实改动了 MCP 端口/令牌时校验，避免历史遗留配置连带卡住连接设置等无关保存
	if (body.MCPPort != nil || body.MCPToken != nil) && targetPort != "" && targetToken == "" {
		respondError(c, http.StatusBadRequest, "MCP 直连端口必须配合接入令牌：请先填写接入令牌，或清空直连端口")
		return
	}

	if body.URL != "" {
		// 这些值会以 KEY=value 写入 .env.local 并被按行解析，含换行即等于注入新的配置键
		for _, f := range []struct{ name, value string }{
			{"地址", body.URL}, {"用户名", body.User}, {"密码", body.Pass},
		} {
			if err := config.ValidateEnvValue(f.name, f.value); err != nil {
				respondError(c, http.StatusBadRequest, err.Error())
				return
			}
		}
	}

	// 校验轮询间隔（可选）
	var pollInterval time.Duration
	if body.PollInterval != "" {
		d, err := time.ParseDuration(body.PollInterval)
		if err != nil || d < 500*time.Millisecond {
			respondError(c, http.StatusBadRequest, "轮询间隔格式无效（如 2s，最小 0.5s）")
			return
		}
		pollInterval = d
	}

	if body.URL != "" {
		// 密码未提供时沿用现有凭据：前端出于安全不回显明文密码，
		// 未修改密码直接保存时 body.Pass 为空，若用它重新探测会导致 502 认证失败
		old := h.rpc.Credentials()
		if body.Pass == "" {
			body.Pass = old.Pass
		}
		// 未提交类型时沿用当前类型：设置界面只在切换下载器时才带该字段
		cred := rpc.Credentials{
			Type: driver.NormalizeKind(body.Type),
			URL:  body.URL,
			User: body.User,
			Pass: body.Pass,
		}
		if body.Type == "" {
			cred.Type = old.Type
		}

		// 先测试新配置连通性（按类型探测对应下载器）
		probe, err := rpc.NewProbe(cred)
		if err != nil {
			respondError(c, http.StatusBadRequest, "配置无效: "+err.Error())
			return
		}
		if _, err := probe.Ping(c.Request.Context()); err != nil {
			respondError(c, http.StatusBadGateway, "无法连接"+cred.Type.Label()+": "+err.Error())
			return
		}

		// 先热更新运行中的客户端（内存切换成功后再持久化，避免 .env.local 与内存不一致）
		if err := h.rpc.Reconfigure(cred); err != nil {
			respondError(c, http.StatusInternalServerError, err.Error())
			return
		}
	}

	// 持久化到数据目录的 .env.local：未提交的类目沿用当前生效值。
	// 直接写入本次算出的目标值（不读内存），落盘成功后再更新内存，
	// 避免写盘失败时留下「内存已改、文件未改」的不一致状态
	cred := h.rpc.Credentials()
	pollStr := body.PollInterval
	if pollStr == "" {
		pollStr = h.hub.getPollInterval().String()
	}
	saved := config.LocalSettings{
		Type:              cred.Type.String(),
		TransmissionURL:   cred.URL,
		User:              cred.User,
		Pass:              cred.Pass,
		PollInterval:      pollStr,
		MCPEnabled:        targetEnabled,
		MCPAllowDelete:    targetAllowDelete,
		MCPAllowDangerous: targetAllowDangerous,
		MCPToken:          targetToken,
		MCPPort:           targetPort,
	}
	if err := config.SaveLocalSettings(h.dataDir, saved); err != nil {
		respondError(c, http.StatusInternalServerError, err.Error())
		return
	}

	// 热更新轮询间隔
	if pollInterval > 0 {
		h.hub.SetPollInterval(pollInterval)
	}
	// 下载器类型可能已变，重新计算聚合成员
	h.SyncAggregateTargets()

	// MCP 开关：落盘成功后统一应用，切换即时生效
	h.mcp.Enabled.Store(targetEnabled)
	h.mcp.AllowDelete.Store(targetAllowDelete)
	h.mcp.AllowDangerous.Store(targetAllowDangerous)
	if targetToken == "" {
		h.mcp.Token.Store(nil)
	} else {
		t := targetToken
		h.mcp.Token.Store(&t)
	}
	// 端口无法热应用（监听器随进程启动绑定），仅更新内存值供回显，重启后生效
	h.setMCPPort(targetPort)

	respond(c, gin.H{"updated": true})
}
