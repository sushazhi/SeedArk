package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/sushazhi/seedark/backend/internal/pt"
	"github.com/sushazhi/seedark/backend/internal/state"
)

// v3 的 PT 接口：淘汰建议、站点快照、考核目标、PT 全局阈值。
//
// 全是「只读输出建议」或「记录用户录入」两类，没有一条会删种、暂停或上站。

// ptEviction 淘汰建议：磁盘紧张时先放哪几颗。
// 参数 freeGb 可覆盖设置里的释放目标；limit 默认 50。
func (h *Handler) ptEviction(c *gin.Context) {
	if !h.ptReady(c) {
		return
	}
	freeGb, _ := strconv.ParseFloat(c.Query("freeGb"), 64)
	limit, _ := strconv.Atoi(c.Query("limit"))
	out, err := h.ptSvc.Eviction(c.Request.Context(), freeGb, limit)
	if err != nil {
		respondError(c, http.StatusBadGateway, "计算淘汰建议失败: "+err.Error())
		return
	}
	respond(c, out)
}

// ptActivity 活跃热力图（7×24，近 14 天）
func (h *Handler) ptActivity(c *gin.Context) {
	if !h.ptReady(c) {
		return
	}
	out, err := h.ptSvc.Activity(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusBadGateway, "计算活跃热力图失败: "+err.Error())
		return
	}
	respond(c, out)
}

// ptLone 下载目录里「有文件但没在做种」的条目（只读提示，不删）
func (h *Handler) ptLone(c *gin.Context) {
	if !h.ptReady(c) {
		return
	}
	out, err := h.ptSvc.Lone(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusBadGateway, "扫描未做种内容失败: "+err.Error())
		return
	}
	respond(c, out)
}

// ptSnapshots 站点侧快照列表与「本地 vs 站点」偏差
func (h *Handler) ptSnapshots(c *gin.Context) {
	if !h.ptReady(c) {
		return
	}
	respond(c, h.ptSvc.Snapshots(c.Request.Context()))
}

// ptAddSnapshot 手工登记一条站点快照
func (h *Handler) ptAddSnapshot(c *gin.Context) {
	if !h.ptReady(c) {
		return
	}
	var snap pt.SiteSnapshot
	if err := c.ShouldBindJSON(&snap); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	saved, err := h.ptSvc.AddSnapshot(snap)
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	respond(c, saved)
}

// ptDeleteSnapshots 清空某站快照历史
func (h *Handler) ptDeleteSnapshots(c *gin.Context) {
	if !h.ptReady(c) {
		return
	}
	if !h.ptSvc.DeleteSnapshots(c.Param("site")) {
		respondError(c, http.StatusNotFound, "该站点没有快照")
		return
	}
	respond(c, gin.H{"deleted": true})
}

// ptExams 考核目标与进度
func (h *Handler) ptExams(c *gin.Context) {
	if !h.ptReady(c) {
		return
	}
	respond(c, h.ptSvc.Exams(c.Request.Context()))
}

// ptSaveExam 保存考核目标
func (h *Handler) ptSaveExam(c *gin.Context) {
	if !h.ptReady(c) {
		return
	}
	var e pt.Exam
	if err := c.ShouldBindJSON(&e); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	saved, err := h.ptSvc.SaveExam(e)
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	respond(c, saved)
}

// ptDeleteExam 删除考核目标
func (h *Handler) ptDeleteExam(c *gin.Context) {
	if !h.ptReady(c) {
		return
	}
	if !h.ptSvc.DeleteExam(c.Param("site")) {
		respondError(c, http.StatusNotFound, "该站点没有考核目标")
		return
	}
	respond(c, gin.H{"deleted": true})
}

// ptGetSettings 读取 PT 全局阈值
func (h *Handler) ptGetSettings(c *gin.Context) {
	st := h.state.Get().PTSettings
	if st.ScarceSeeders <= 0 {
		st.ScarceSeeders = 10 // 与 pt.defaultScarceSeeders 一致：未配置时的默认稀缺阈值
	}
	respond(c, st)
}

// ptSaveSettings 保存 PT 全局阈值。
// ScarceSeeders 为 0 时按默认值处理；ReleaseTargetFreeGB 为 0 表示不做磁盘触发。
func (h *Handler) ptSaveSettings(c *gin.Context) {
	var in state.PTSettings
	if err := c.ShouldBindJSON(&in); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	if in.ScarceSeeders < 0 || in.ReleaseTargetFreeGB < 0 {
		respondError(c, http.StatusBadRequest, "阈值不能为负数")
		return
	}
	if err := h.state.Update(func(s *state.State) { s.PTSettings = in }); err != nil {
		respondError(c, http.StatusInternalServerError, err.Error())
		return
	}
	respond(c, in)
}
