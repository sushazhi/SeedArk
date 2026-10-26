package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/sushazhi/seedark/backend/internal/pt"
	"github.com/sushazhi/seedark/backend/internal/state"
)

// ptReady PT 服务是否可用。返回 false 时已写好响应，调用方直接 return。
func (h *Handler) ptReady(c *gin.Context) bool {
	if h.ptSvc == nil {
		respondError(c, http.StatusServiceUnavailable, "PT 服务未启用")
		return false
	}
	return true
}

// ptOverview 概览
func (h *Handler) ptOverview(c *gin.Context) {
	if !h.ptReady(c) {
		return
	}
	out, err := h.ptSvc.Overview(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	respond(c, out)
}

// ptLedger 资产台账
func (h *Handler) ptLedger(c *gin.Context) {
	if !h.ptReady(c) {
		return
	}
	out, err := h.ptSvc.Ledger(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	respond(c, out)
}

// ptCalendar 释放日历（month=YYYY-MM，缺省当月）
func (h *Handler) ptCalendar(c *gin.Context) {
	if !h.ptReady(c) {
		return
	}
	out, err := h.ptSvc.Calendar(c.Request.Context(), strings.TrimSpace(c.Query("month")))
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	respond(c, out)
}

// ptVault 档案列表（q=名称/hash/站点）
func (h *Handler) ptVault(c *gin.Context) {
	if !h.ptReady(c) {
		return
	}
	respond(c, h.ptSvc.VaultList(c.Query("q")))
}

// ptVaultDetail 档案详情
func (h *Handler) ptVaultDetail(c *gin.Context) {
	if !h.ptReady(c) {
		return
	}
	id := c.Param("id")
	entry, files, ok := h.ptSvc.VaultDetail(id)
	if !ok {
		respondError(c, http.StatusNotFound, "档案不存在")
		return
	}
	respond(c, gin.H{"entry": entry, "files": files})
}

// ptVaultFile 下载留档的 .torrent（v1 驱动未导出时返回 404）
func (h *Handler) ptVaultFile(c *gin.Context) {
	if !h.ptReady(c) {
		return
	}
	data, ok := h.ptSvc.VaultTorrentBytes(c.Param("id"))
	if !ok {
		respondError(c, http.StatusNotFound, "该档案没有留存的种子文件")
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.torrent"`, safeDownloadName(c.Param("id"))))
	c.Data(http.StatusOK, "application/x-bittorrent", data)
}

// ptVaultRevive 复活档案
func (h *Handler) ptVaultRevive(c *gin.Context) {
	if !h.ptReady(c) {
		return
	}
	id, via, err := h.ptSvc.Revive(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	respond(c, gin.H{"id": id, "via": via})
}

// ptVaultDelete 删除档案
func (h *Handler) ptVaultDelete(c *gin.Context) {
	if !h.ptReady(c) {
		return
	}
	if !h.ptSvc.VaultRemove(c.Param("id")) {
		respondError(c, http.StatusNotFound, "档案不存在")
		return
	}
	respond(c, gin.H{"deleted": true})
}

// ptMatrix 同内容矩阵
func (h *Handler) ptMatrix(c *gin.Context) {
	if !h.ptReady(c) {
		return
	}
	out, err := h.ptSvc.Matrix(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	respond(c, out)
}

// ptPublishPreview 生成 BBCode 预览（不落盘）
func (h *Handler) ptPublishPreview(c *gin.Context) {
	var req pt.PublishRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	respond(c, pt.Preview(req))
}

// ptPublishKit 生成发布资料包 zip（multipart：payload + 可选 torrent）
func (h *Handler) ptPublishKit(c *gin.Context) {
	var req pt.PublishRequest
	if raw := c.PostForm("payload"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			respondError(c, http.StatusBadRequest, "payload 无效: "+err.Error())
			return
		}
	}
	var torrentData []byte
	var torrentName string
	if fh, err := c.FormFile("torrent"); err == nil {
		if fh.Size > 10<<20 {
			respondError(c, http.StatusBadRequest, "种子文件不能超过 10MB")
			return
		}
		f, err := fh.Open()
		if err != nil {
			respondError(c, http.StatusBadRequest, "读取种子文件失败: "+err.Error())
			return
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, 10<<20))
		if err != nil {
			respondError(c, http.StatusBadRequest, "读取种子文件失败: "+err.Error())
			return
		}
		torrentData = data
		torrentName = fh.Filename
	}
	buf, err := pt.Kit(req, torrentName, torrentData)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "生成资料包失败: "+err.Error())
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="seedark-publish-%s.zip"`, safeDownloadName(req.Name)))
	c.Data(http.StatusOK, "application/zip", buf)
}

// ptSites 站点规则 + 当前检测到的站点
func (h *Handler) ptSites(c *gin.Context) {
	profiles := h.state.Get().SiteProfiles
	if profiles == nil {
		profiles = []state.SiteProfile{}
	}
	detected := []pt.DetectedSite{}
	if h.ptSvc != nil {
		if d, err := h.ptSvc.DetectedSites(c.Request.Context()); err == nil {
			detected = d
		}
	}
	respond(c, pt.SitesResponse{Profiles: profiles, Detected: detected})
}

// ptSaveSite 新增/更新站点规则
func (h *Handler) ptSaveSite(c *gin.Context) {
	var p state.SiteProfile
	if err := c.ShouldBindJSON(&p); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	saved, err := pt.UpsertProfile(h.state, p)
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	respond(c, saved)
}

// ptDeleteSite 删除站点规则
func (h *Handler) ptDeleteSite(c *gin.Context) {
	ok, err := pt.DeleteProfile(h.state, c.Param("id"))
	if err != nil {
		respondError(c, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		respondError(c, http.StatusNotFound, "站点规则不存在")
		return
	}
	respond(c, gin.H{"deleted": true})
}

// ptSeedSites 为检测到的站点批量生成占位规则（不猜天数）
func (h *Handler) ptSeedSites(c *gin.Context) {
	if !h.ptReady(c) {
		return
	}
	n, err := pt.SeedProfiles(c.Request.Context(), h.ptSvc, h.state)
	if err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	respond(c, gin.H{"created": n})
}

// ptSample 手动采样
func (h *Handler) ptSample(c *gin.Context) {
	if !h.ptReady(c) {
		return
	}
	n, err := h.ptSvc.Sample(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	respond(c, gin.H{"sampled": true, "count": n})
}

// ptSetContract 设置/覆盖一颗种子的保种契约（①）。只写承诺，不触发任何动作。
func (h *Handler) ptSetContract(c *gin.Context) {
	if !h.ptReady(c) {
		return
	}
	var req pt.ContractRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	saved, err := h.ptSvc.SetContract(req)
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	respond(c, saved)
}

// ptDeleteContract 解除保种契约
func (h *Handler) ptDeleteContract(c *gin.Context) {
	if !h.ptReady(c) {
		return
	}
	ok, err := h.ptSvc.RemoveContract(c.Param("hash"))
	if err != nil {
		respondError(c, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		respondError(c, http.StatusNotFound, "该种子没有保种契约")
		return
	}
	respond(c, gin.H{"deleted": true})
}

// safeDownloadName 收敛下载文件名，去掉路径分隔与引号
func safeDownloadName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "seedark"
	}
	bad := strings.NewReplacer("/", "_", "\\", "_", "\"", "_", "\n", "_", "\r", "_")
	s = bad.Replace(s)
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}
