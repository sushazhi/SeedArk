package fnos

import (
	"log/slog"
	"strings"

	"github.com/gin-gonic/gin"
)

// 语义路径展示：把 Transmission 报上来的内部路径转成宿主展示路径。
// 展示属增强能力，任何失败都按「不可用」返回空映射，前端静默回退原始路径，不让 UI 报错。
type pathHandler struct {
	tc *trimPathClient
}

type semanticReq struct {
	Paths    []string `json:"paths"`
	Language string   `json:"language"`
}

const semanticMaxPaths = 256

// normalizeLanguage 前端界面语言（'zh'|'en'）归一化为宿主 locale
func normalizeLanguage(lang string) string {
	switch strings.TrimSpace(strings.ToLower(lang)) {
	case "en", "en-us":
		return "en-US"
	default:
		return "zh-CN"
	}
}

func (h *pathHandler) convert(c *gin.Context) {
	var body semanticReq
	if err := c.ShouldBindJSON(&body); err != nil {
		respond(c, gin.H{"available": false, "map": gin.H{}})
		return
	}
	if len(body.Paths) > semanticMaxPaths {
		// 静默截断会让超出部分的目录永远显示原始 /vol1/... 路径且无任何提示，
		// 排障时表现为「一部分目录转换成功、一部分没有」。
		// 这属于异常输入（正常只有个位数目录），保留 warn。
		// 注意：被截断的目录本批不会有结果，调用方按「未命中」处理即可；
		// 提高上限前需先确认宿主网关能吃下更大的批量。
		slog.Warn("语义路径请求超过上限，超出部分本批不转换",
			"got", len(body.Paths), "max", semanticMaxPaths)
		body.Paths = body.Paths[:semanticMaxPaths]
	}
	m, err := h.tc.Convert(c.Request.Context(), body.Paths, normalizeLanguage(body.Language))
	if err != nil {
		// 降为 debug：飞牛开放平台的 trim.file.convertPath 目前是宿主侧故障
		// （实测任意 payload 均返回 500/200006，qBittorrent、logmanager 调同一端点同样失败，
		// 但它们静默降级所以用户无感）。该端点在每次启动/新目录出现时都会被调用，
		// 保持 warn 只会在日志里持续刷屏；真机排障时把 LOG_LEVEL 调成 debug 即可复现。
		slog.Debug("语义路径转换不可用", "err", err)
		respond(c, gin.H{"available": false, "map": gin.H{}})
		return
	}
	respond(c, gin.H{"available": true, "map": m})
}
