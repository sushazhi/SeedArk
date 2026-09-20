package api

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/trpanel/backend/internal/driver"
	"github.com/trpanel/backend/internal/models"
	"github.com/trpanel/backend/internal/rpc"
)

// getSession 获取会话配置。
// 附带下载器类型与能力自述：界面据此显示当前连接的是 Transmission 还是
// qBittorrent，并隐藏该下载器不支持的入口（如 qBittorrent 无带宽组/黑名单）。
func (h *Handler) getSession(c *gin.Context) {
	sess, err := h.rpc.GetSession(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusBadGateway, "获取会话信息失败: "+err.Error())
		return
	}
	if sess != nil {
		sess.Type = h.rpc.Kind().String()
		caps := h.rpc.Capabilities()
		sess.Caps = &caps
	}
	respond(c, sess)
}

// sessionStatus 连接状态检测
func (h *Handler) sessionStatus(c *gin.Context) {
	caps := h.rpc.Capabilities()
	st := models.SessionStatus{Type: h.rpc.Kind().String(), Caps: &caps}
	if errs := h.rpc.AggregateErrors(); len(errs) > 0 {
		// 聚合成员整体失败会让列表静默变空，必须在状态里暴露出来
		keys := make([]string, 0, len(errs))
		for k := range errs {
			keys = append(keys, strconv.Itoa(k+1))
		}
		sort.Strings(keys)
		for _, k := range keys {
			i, _ := strconv.Atoi(k)
			st.AggregateErrors = append(st.AggregateErrors,
				fmt.Sprintf("服务器%s：%s", k, rpc.SanitizeClientMsg(errs[i-1])))
		}
	}
	st.Aggregate = h.rpc.AggregateEnabled()
	version, err := h.rpc.Ping(c.Request.Context())
	if err != nil {
		// 该接口以 200 返回连接状态，绕过了 respondError 的统一脱敏，需自行抹掉错误里的凭据
		st.Connected = false
		st.Error = rpc.SanitizeClientMsg(err.Error())
		respond(c, st)
		return
	}
	st.Connected = true
	st.Version = version
	respond(c, st)
}

// portTest 测试端口是否开放
func (h *Handler) portTest(c *gin.Context) {
	open, err := h.rpc.TestPort(c.Request.Context())
	if err != nil {
		respondBackendError(c, "端口测试失败", err)
		return
	}
	respond(c, gin.H{"open": open})
}

// blocklistUpdate 更新 Blocklist 规则
func (h *Handler) blocklistUpdate(c *gin.Context) {
	entries, err := h.rpc.UpdateBlocklist(c.Request.Context())
	if err != nil {
		respondBackendError(c, "更新 Blocklist 失败", err)
		return
	}
	respond(c, gin.H{"entries": entries})
}

// systemCommand 系统命令（支持 shutdown / reboot）
func (h *Handler) systemCommand(c *gin.Context) {
	// 破坏性操作：默认配置（无 API_TOKEN）下 Auth 为空操作，而同源写守卫只挡浏览器跨站请求，
	// 局域网内任意 curl 都能停机。因此要求部署本身具备认证边界：启用了令牌鉴权，
	// 或经宿主网关统一认证（fnOS 网关模式）。
	if h.apiToken == "" && !h.plat.SecurityPolicy().AllowEmbedding {
		respondError(c, http.StatusForbidden, "系统命令需认证：请设置 API_TOKEN 启用令牌鉴权，或经宿主网关部署")
		return
	}
	action := c.Param("action")
	if action != "shutdown" && action != "reboot" {
		respondError(c, http.StatusBadRequest, "不支持的系统命令: "+action)
		return
	}
	if err := h.rpc.SystemCommand(c.Request.Context(), action); err != nil {
		respondBackendError(c, "执行失败", err)
		return
	}
	respond(c, gin.H{"action": action})
}

// sessionStats 获取会话统计（累计/当前）
func (h *Handler) sessionStats(c *gin.Context) {
	stats, err := h.rpc.GetSessionStats(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusBadGateway, "获取会话统计失败: "+err.Error())
		return
	}
	respond(c, stats)
}

// freeSpace 查询目录可用空间
func (h *Handler) freeSpace(c *gin.Context) {
	path := c.Query("path")
	if path == "" {
		respondError(c, http.StatusBadRequest, "缺少 path 参数")
		return
	}
	free, total, err := h.rpc.GetFreeSpace(c.Request.Context(), path)
	if err != nil {
		respondBackendError(c, "查询失败", err)
		return
	}
	respond(c, gin.H{"path": path, "freeSpace": free, "totalSize": total})
}

// setSession 更新会话配置
func (h *Handler) setSession(c *gin.Context) {
	var body struct {
		DownloadDir                      *string  `json:"downloadDir"`
		SpeedLimitDown                   *int64   `json:"speedLimitDown"`
		SpeedLimitDownOn                 *bool    `json:"speedLimitDownOn"`
		SpeedLimitUp                     *int64   `json:"speedLimitUp"`
		SpeedLimitUpOn                   *bool    `json:"speedLimitUpOn"`
		AltSpeedDown                     *int64   `json:"altSpeedDown"`
		AltSpeedUp                       *int64   `json:"altSpeedUp"`
		AltSpeedEnabled                  *bool    `json:"altSpeedEnabled"`
		StartAdded                       *bool    `json:"startAdded"`
		PeerLimitGlobal                  *int64   `json:"peerLimitGlobal"`
		PEXEnabled                       *bool    `json:"pexEnabled"`
		DHTEnabled                       *bool    `json:"dhtEnabled"`
		SeedRatioLimit                   *float64 `json:"seedRatioLimit"`
		Encryption                       *string  `json:"encryption"`
		DownloadQueueEnabled             *bool    `json:"downloadQueueEnabled"`
		DownloadQueueSize                *int64   `json:"downloadQueueSize"`
		SeedQueueEnabled                 *bool    `json:"seedQueueEnabled"`
		SeedQueueSize                    *int64   `json:"seedQueueSize"`
		QueueStalledEnabled              *bool    `json:"queueStalledEnabled"`
		QueueStalledMinutes              *int64   `json:"queueStalledMinutes"`
		BlocklistEnabled                 *bool    `json:"blocklistEnabled"`
		BlocklistURL                     *string  `json:"blocklistUrl"`
		PortForwardingEnabled            *bool    `json:"portForwardingEnabled"`
		IncompleteDir                    *string  `json:"incompleteDir"`
		IncompleteDirEnabled             *bool    `json:"incompleteDirEnabled"`
		CacheSizeMB                      *int64   `json:"cacheSizeMB"`
		AltSpeedTimeEnabled              *bool    `json:"altSpeedTimeEnabled"`
		AltSpeedTimeBegin                *int64   `json:"altSpeedTimeBegin"`
		AltSpeedTimeEnd                  *int64   `json:"altSpeedTimeEnd"`
		AltSpeedTimeDay                  *int64   `json:"altSpeedTimeDay"`
		ScriptTorrentAddedEnabled        *bool    `json:"scriptTorrentAddedEnabled"`
		ScriptTorrentAddedFilename       *string  `json:"scriptTorrentAddedFilename"`
		ScriptTorrentDoneEnabled         *bool    `json:"scriptTorrentDoneEnabled"`
		ScriptTorrentDoneFilename        *string  `json:"scriptTorrentDoneFilename"`
		ScriptTorrentDoneSeedingEnabled  *bool    `json:"scriptTorrentDoneSeedingEnabled"`
		ScriptTorrentDoneSeedingFilename *string  `json:"scriptTorrentDoneSeedingFilename"`
		DefaultTrackers                  []string `json:"defaultTrackers"`
		RenamePartialFiles               *bool    `json:"renamePartialFiles"`
		TrashOriginalTorrentFiles        *bool    `json:"trashOriginalTorrentFiles"`
		IdleSeedingLimitEnabled          *bool    `json:"idleSeedingLimitEnabled"`
		IdleSeedingLimit                 *int64   `json:"idleSeedingLimit"`
		PeerPort                         *int64   `json:"peerPort"`
		PeerPortRandomOnStart            *bool    `json:"peerPortRandomOnStart"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}

	// Transmission 会在种子事件时以 Transmission 服务账号权限执行脚本钩子：
	// 任意设置 script-torrent-done-filename 等字段，配合添加种子把恶意可执行文件
	// 下载到磁盘再指定为钩子，可形成「下载即执行」的 RCE 链。与 systemCommand
	// 一致，要求部署具备认证边界（令牌鉴权或宿主网关统一认证）才允许修改脚本字段。
	if h.apiToken == "" && !h.plat.SecurityPolicy().AllowEmbedding {
		if body.ScriptTorrentAddedEnabled != nil || body.ScriptTorrentAddedFilename != nil ||
			body.ScriptTorrentDoneEnabled != nil || body.ScriptTorrentDoneFilename != nil ||
			body.ScriptTorrentDoneSeedingEnabled != nil || body.ScriptTorrentDoneSeedingFilename != nil {
			respondError(c, http.StatusForbidden, "脚本钩子设置需认证：请设置 API_TOKEN 启用令牌鉴权，或经宿主网关部署")
			return
		}
	}

	payload := driver.SessionPatch{
		DownloadDir:                      body.DownloadDir,
		SpeedLimitDown:                   body.SpeedLimitDown,
		SpeedLimitDownOn:                 body.SpeedLimitDownOn,
		SpeedLimitUp:                     body.SpeedLimitUp,
		SpeedLimitUpOn:                   body.SpeedLimitUpOn,
		AltSpeedDown:                     body.AltSpeedDown,
		AltSpeedUp:                       body.AltSpeedUp,
		AltSpeedEnabled:                  body.AltSpeedEnabled,
		StartAdded:                       body.StartAdded,
		PeerLimitGlobal:                  body.PeerLimitGlobal,
		PEXEnabled:                       body.PEXEnabled,
		DHTEnabled:                       body.DHTEnabled,
		SeedRatioLimit:                   body.SeedRatioLimit,
		DownloadQueueEnabled:             body.DownloadQueueEnabled,
		DownloadQueueSize:                body.DownloadQueueSize,
		SeedQueueEnabled:                 body.SeedQueueEnabled,
		SeedQueueSize:                    body.SeedQueueSize,
		QueueStalledEnabled:              body.QueueStalledEnabled,
		QueueStalledMinutes:              body.QueueStalledMinutes,
		BlocklistEnabled:                 body.BlocklistEnabled,
		BlocklistURL:                     body.BlocklistURL,
		PortForwardingEnabled:            body.PortForwardingEnabled,
		IncompleteDir:                    body.IncompleteDir,
		IncompleteDirEnabled:             body.IncompleteDirEnabled,
		CacheSizeMB:                      body.CacheSizeMB,
		AltSpeedTimeEnabled:              body.AltSpeedTimeEnabled,
		AltSpeedTimeBegin:                body.AltSpeedTimeBegin,
		AltSpeedTimeEnd:                  body.AltSpeedTimeEnd,
		AltSpeedTimeDay:                  body.AltSpeedTimeDay,
		ScriptTorrentAddedEnabled:        body.ScriptTorrentAddedEnabled,
		ScriptTorrentAddedFilename:       body.ScriptTorrentAddedFilename,
		ScriptTorrentDoneEnabled:         body.ScriptTorrentDoneEnabled,
		ScriptTorrentDoneFilename:        body.ScriptTorrentDoneFilename,
		ScriptTorrentDoneSeedingEnabled:  body.ScriptTorrentDoneSeedingEnabled,
		ScriptTorrentDoneSeedingFilename: body.ScriptTorrentDoneSeedingFilename,
		DefaultTrackers:                  body.DefaultTrackers,
		RenamePartialFiles:               body.RenamePartialFiles,
		TrashOriginalTorrentFiles:        body.TrashOriginalTorrentFiles,
		IdleSeedingLimitEnabled:          body.IdleSeedingLimitEnabled,
		IdleSeedingLimit:                 body.IdleSeedingLimit,
		PeerPort:                         body.PeerPort,
		PeerPortRandomOnStart:            body.PeerPortRandomOnStart,
	}
	payload.Encryption = body.Encryption

	if err := h.rpc.SetSession(c.Request.Context(), payload); err != nil {
		respondBackendError(c, "更新会话失败", err)
		return
	}
	respond(c, gin.H{"updated": true})
}
