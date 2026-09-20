package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// listGroups 获取带宽组列表（Transmission 4.x group-get）
func (h *Handler) listGroups(c *gin.Context) {
	groups, err := h.rpc.GetSessionGroups(c.Request.Context())
	if err != nil {
		respondBackendError(c, "获取带宽组失败", err)
		return
	}
	respond(c, groups)
}

// saveGroup 创建或更新带宽组（group-set，同名即更新）
func (h *Handler) saveGroup(c *gin.Context) {
	var body struct {
		Name                string `json:"name"`
		DownKB              *int64 `json:"downKB"`
		UpKB                *int64 `json:"upKB"`
		DownEnabled         *bool  `json:"downEnabled"`
		UpEnabled           *bool  `json:"upEnabled"`
		HonorsSessionLimits *bool  `json:"honorsSessionLimits"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效")
		return
	}
	if body.Name == "" {
		respondError(c, http.StatusBadRequest, "缺少组名")
		return
	}
	fields := map[string]any{}
	if body.DownKB != nil {
		fields["speed-limit-down"] = *body.DownKB
	}
	if body.UpKB != nil {
		fields["speed-limit-up"] = *body.UpKB
	}
	if body.DownEnabled != nil {
		fields["speed-limit-down-enabled"] = *body.DownEnabled
	}
	if body.UpEnabled != nil {
		fields["speed-limit-up-enabled"] = *body.UpEnabled
	}
	if body.HonorsSessionLimits != nil {
		fields["honor-session-limits"] = *body.HonorsSessionLimits
	}
	if err := h.rpc.SetSessionGroup(c.Request.Context(), body.Name, fields); err != nil {
		respondBackendError(c, "保存带宽组失败", err)
		return
	}
	respond(c, gin.H{"saved": true})
}
