package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// trafficReady 流量历史服务是否可用。返回 false 时已写好响应，调用方直接 return。
// 与保种台一样的降级语义：数据目录不可写等情况下面板其余功能不受影响。
func (h *Handler) trafficReady(c *gin.Context) bool {
	if h.trafficSvc == nil {
		respondError(c, http.StatusServiceUnavailable, "流量历史服务未启用")
		return false
	}
	return true
}

// trafficHistory 历史流量折线图：range=24h|7d|30d，缺省 24h
func (h *Handler) trafficHistory(c *gin.Context) {
	if !h.trafficReady(c) {
		return
	}
	snap, err := h.trafficSvc.History(strings.TrimSpace(c.Query("range")))
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	respond(c, snap)
}
