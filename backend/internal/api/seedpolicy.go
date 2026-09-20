package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sushazhi/seedark/backend/internal/state"
)

// listSeedPolicy 返回做种策略规则、全局保护栏与执行记录
func (h *Handler) listSeedPolicy(c *gin.Context) {
	st := h.state.Get()
	logs := st.SeedPolicyLogs
	if logs == nil {
		logs = []state.SeedPolicyLog{}
	}
	respond(c, gin.H{
		"rules": st.SeedPolicyRules,
		"guard": st.SeedPolicyGuard,
		"logs":  logs,
	})
}

// saveSeedPolicyRule 新增或更新一条做种策略规则
func (h *Handler) saveSeedPolicyRule(c *gin.Context) {
	var rule state.SeedPolicyRule
	if err := c.ShouldBindJSON(&rule); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	if rule.Action == "" {
		rule.Action = state.PolicyActionPause
	}
	if rule.MinRatio < 0 {
		rule.MinRatio = 0
	}
	if rule.MinSeedDays < 0 {
		rule.MinSeedDays = 0
	}
	if rule.MinUploadGB < 0 {
		rule.MinUploadGB = 0
	}
	rule.Sites = cleanList(rule.Sites)
	rule.Labels = cleanList(rule.Labels)
	if err := validateSeedPolicyRule(&rule); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if rule.ID == "" {
		rule.ID = newID("sp")
	}
	resp := gin.H{"id": rule.ID}
	if w := h.persistState(func(st *state.State) {
		replaced := false
		for i := range st.SeedPolicyRules {
			if st.SeedPolicyRules[i].ID == rule.ID {
				st.SeedPolicyRules[i] = rule
				replaced = true
				break
			}
		}
		if !replaced {
			st.SeedPolicyRules = append(st.SeedPolicyRules, rule)
		}
	}); w != "" {
		resp["warning"] = w
	}
	respond(c, resp)
}

// validateSeedPolicyRule 校验规则可执行，避免误配成「无条件全站删除」
func validateSeedPolicyRule(rule *state.SeedPolicyRule) error {
	switch rule.Action {
	case state.PolicyActionPause, state.PolicyActionDelete, state.PolicyActionDeleteData:
	default:
		return errors.New("动作无效，仅支持 pause / delete / deleteData")
	}
	// 无任何达标条件时，规则会命中范围内所有已完成种子，删除动作尤其危险
	if rule.MinRatio <= 0 && rule.MinSeedDays <= 0 && rule.MinUploadGB <= 0 {
		return errors.New("至少设置一个达标条件（分享率 / 做种天数 / 上传量）")
	}
	return nil
}

// deleteSeedPolicyRule 删除规则，并清理其已处理标记
func (h *Handler) deleteSeedPolicyRule(c *gin.Context) {
	id := c.Param("id")
	var deleted bool
	err := h.state.Update(func(st *state.State) {
		for i := range st.SeedPolicyRules {
			if st.SeedPolicyRules[i].ID == id {
				st.SeedPolicyRules = append(st.SeedPolicyRules[:i], st.SeedPolicyRules[i+1:]...)
				deleted = true
				break
			}
		}
		prefix := id + "\x00"
		for k := range st.ProcessedPolicy {
			if strings.HasPrefix(k, prefix) {
				delete(st.ProcessedPolicy, k)
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

// saveSeedPolicyGuard 更新全局保护栏
func (h *Handler) saveSeedPolicyGuard(c *gin.Context) {
	var guard state.SeedPolicyGuard
	if err := c.ShouldBindJSON(&guard); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	if guard.MinSeedHours < 0 {
		guard.MinSeedHours = 0
	}
	guard.ExcludeSites = cleanList(guard.ExcludeSites)
	guard.ExcludeLabels = cleanList(guard.ExcludeLabels)
	resp := gin.H{"saved": true}
	if w := h.persistState(func(st *state.State) { st.SeedPolicyGuard = guard }); w != "" {
		resp["warning"] = w
	}
	respond(c, resp)
}

// runSeedPolicy 手动执行一轮做种策略
func (h *Handler) runSeedPolicy(c *gin.Context) {
	res, err := h.seedpolicy.Tick(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusBadGateway, "执行失败: "+err.Error())
		return
	}
	respond(c, gin.H{
		"result": res,
		"at":     time.Now().Unix(),
	})
}

// resetSeedPolicy 清空已处理标记，让所有种子重新参与达标判定
func (h *Handler) resetSeedPolicy(c *gin.Context) {
	ruleID := c.Query("rule")
	var cleared int
	err := h.state.Update(func(st *state.State) {
		prefix := ""
		if ruleID != "" {
			prefix = ruleID + "\x00"
		}
		for k := range st.ProcessedPolicy {
			if prefix == "" || strings.HasPrefix(k, prefix) {
				delete(st.ProcessedPolicy, k)
				cleared++
			}
		}
	})
	if err != nil {
		slog.Warn("状态持久化失败", "err", err)
	}
	resp := gin.H{"cleared": cleared}
	if err != nil {
		resp["warning"] = "已重置，但写入磁盘失败：" + err.Error()
	}
	respond(c, resp)
}

// clearSeedPolicyLogs 清空执行记录
func (h *Handler) clearSeedPolicyLogs(c *gin.Context) {
	resp := gin.H{"cleared": true}
	if w := h.persistState(func(st *state.State) { st.SeedPolicyLogs = []state.SeedPolicyLog{} }); w != "" {
		resp["warning"] = w
	}
	respond(c, resp)
}

// cleanList 去掉空白项，避免界面输入 "a, , b" 时留下无意义条件
func cleanList(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
