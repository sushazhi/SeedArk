package api

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sushazhi/seedark/backend/internal/state"
)

// listSpeedPolicy 返回组内总限速规则与引擎开关
func (h *Handler) listSpeedPolicy(c *gin.Context) {
	st := h.state.Get()
	respond(c, gin.H{
		"rules": st.SpeedPolicyRules,
		"guard": st.SpeedPolicyGuard,
	})
}

// saveSpeedPolicyRule 新增或更新一条组内总限速规则
func (h *Handler) saveSpeedPolicyRule(c *gin.Context) {
	var rule state.SpeedPolicyRule
	if err := c.ShouldBindJSON(&rule); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	rule.Sites = cleanList(rule.Sites)
	rule.Labels = cleanList(rule.Labels)
	if err := validateSpeedPolicyRule(&rule); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if rule.ID == "" {
		rule.ID = newID("sl")
	}
	resp := gin.H{"id": rule.ID}
	if w := h.persistState(func(st *state.State) {
		replaced := false
		for i := range st.SpeedPolicyRules {
			if st.SpeedPolicyRules[i].ID == rule.ID {
				st.SpeedPolicyRules[i] = rule
				replaced = true
				break
			}
		}
		if !replaced {
			st.SpeedPolicyRules = append(st.SpeedPolicyRules, rule)
		}
	}); w != "" {
		resp["warning"] = w
	}
	respond(c, resp)
}

// validateSpeedPolicyRule 校验规则可执行
func validateSpeedPolicyRule(rule *state.SpeedPolicyRule) error {
	if rule.DownLimit < 0 {
		rule.DownLimit = 0
	}
	if rule.UpLimit < 0 {
		rule.UpLimit = 0
	}
	// 两个方向都是 0 = 该规则不限制任何方向，没有意义
	if rule.DownLimit <= 0 && rule.UpLimit <= 0 {
		return errors.New("下载与上传上限至少设置一个（0 = 该方向不限制）")
	}
	if rule.Sites == nil {
		rule.Sites = []string{}
	}
	if rule.Labels == nil {
		rule.Labels = []string{}
	}
	return nil
}

// deleteSpeedPolicyRule 删除规则。引擎在下一轮会自动释放只属于该规则的种子
func (h *Handler) deleteSpeedPolicyRule(c *gin.Context) {
	id := c.Param("id")
	var deleted bool
	err := h.state.Update(func(st *state.State) {
		for i := range st.SpeedPolicyRules {
			if st.SpeedPolicyRules[i].ID == id {
				st.SpeedPolicyRules = append(st.SpeedPolicyRules[:i], st.SpeedPolicyRules[i+1:]...)
				deleted = true
				break
			}
		}
	})
	if err != nil {
		slog.Warn("状态持久化失败", "err", err)
	}
	if !deleted {
		respondError(c, http.StatusBadRequest, "规则不存在")
		return
	}
	resp := gin.H{"deleted": true}
	if err != nil {
		resp["warning"] = "已删除，但写入磁盘失败：" + err.Error()
	}
	respond(c, resp)
}

// saveSpeedPolicyGuard 更新引擎开关
func (h *Handler) saveSpeedPolicyGuard(c *gin.Context) {
	var guard state.SpeedPolicyGuard
	if err := c.ShouldBindJSON(&guard); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	resp := gin.H{"saved": true}
	if w := h.persistState(func(st *state.State) { st.SpeedPolicyGuard = guard }); w != "" {
		resp["warning"] = w
	}
	respond(c, resp)
}

// runSpeedPolicy 手动执行一轮组内限速分配（保存规则后无需等待下个 tick 生效）
func (h *Handler) runSpeedPolicy(c *gin.Context) {
	res, err := h.speedpolicy.Tick(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusBadGateway, "执行失败: "+err.Error())
		return
	}
	respond(c, gin.H{
		"result": res,
		"at":     time.Now().Unix(),
	})
}
