package api

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/sushazhi/seedark/backend/internal/driver"
	"github.com/sushazhi/seedark/backend/internal/models"
	"github.com/sushazhi/seedark/backend/internal/rpc"
	"github.com/sushazhi/seedark/backend/internal/state"
)

// getSession 获取会话配置。
// 附带下载器类型、能力自述与设置字段自述：界面据此显示当前连接的是
// Transmission 还是 qBittorrent，隐藏该下载器不支持的入口（如 qBittorrent
// 无带宽组/黑名单），并按驱动自述渲染设置面板。
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
		// 未提供自述的驱动（Transmission）留空，界面回退到通用会话表单
		if schema := h.rpc.SettingsSchema(); len(schema) > 0 {
			sess.Schema = schema
		}
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

// freeSpace 查询目录可用空间（活动连接那台）
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
	if total <= 0 {
		st := h.state.Get()
		total = st.DiskTotal(st.ActiveServer)
	}
	respond(c, gin.H{"path": path, "freeSpace": free, "totalSize": total})
}

// serverFreeSpace 查询指定服务器的磁盘剩余空间。
// 聚合视图下侧栏逐台显示余量，而 /session/free-space 只查活动那台；
// 各台的下载目录可能不同，必须回读该台自己的总会话取路径。
func (h *Handler) serverFreeSpace(c *gin.Context) {
	idx, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "无效的服务器索引")
		return
	}
	b, err := h.memberBackend(idx)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errServerNotFound) {
			status = http.StatusNotFound
		}
		respondError(c, status, err.Error())
		return
	}
	sess, err := b.GetSession(c.Request.Context())
	if err != nil {
		respondBackendError(c, "获取会话信息失败", err)
		return
	}
	path := ""
	if sess != nil {
		path = sess.DownloadDir
	}
	if path == "" {
		respondError(c, http.StatusBadRequest, "该服务器未提供下载目录")
		return
	}
	free, total, err := b.GetFreeSpace(c.Request.Context(), path)
	if err != nil {
		respondBackendError(c, "查询失败", err)
		return
	}
	if total <= 0 {
		st := h.state.Get()
		total = st.DiskTotal(idx)
	}
	respond(c, gin.H{
		"index":     idx,
		"name":      h.state.Get().Servers[idx].Name,
		"path":      path,
		"freeSpace": free,
		"totalSize": total,
	})
}

// sessionBody 会话配置修改请求体（/api/session 与 /api/servers/:index/session 共用）。
// 全部字段用指针：区分「未提交」与「提交为零值」，设置面板只回传改动过的键，
// 未提交的键保持上游原值不动。
type sessionBody struct {
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
	// Prefs 该驱动的原生偏好通道：键名即下载器自己的配置键
	//（qBittorrent 为 app/preferences 的原生键），由驱动按键名与取值校验
	//（见 qbittorrent/applyQBPreferences）。
	Prefs map[string]any `json:"prefs"`
	// QB 与 Prefs 同义，保留以兼容仍在发 qb 的旧前端；两者同时提交时以 Prefs 为准
	QB map[string]any `json:"qb"`
}

// patch 转换为驱动层补丁
func (b *sessionBody) patch() driver.SessionPatch {
	p := driver.SessionPatch{
		DownloadDir:                      b.DownloadDir,
		SpeedLimitDown:                   b.SpeedLimitDown,
		SpeedLimitDownOn:                 b.SpeedLimitDownOn,
		SpeedLimitUp:                     b.SpeedLimitUp,
		SpeedLimitUpOn:                   b.SpeedLimitUpOn,
		AltSpeedDown:                     b.AltSpeedDown,
		AltSpeedUp:                       b.AltSpeedUp,
		AltSpeedEnabled:                  b.AltSpeedEnabled,
		StartAdded:                       b.StartAdded,
		PeerLimitGlobal:                  b.PeerLimitGlobal,
		PEXEnabled:                       b.PEXEnabled,
		DHTEnabled:                       b.DHTEnabled,
		SeedRatioLimit:                   b.SeedRatioLimit,
		DownloadQueueEnabled:             b.DownloadQueueEnabled,
		DownloadQueueSize:                b.DownloadQueueSize,
		SeedQueueEnabled:                 b.SeedQueueEnabled,
		SeedQueueSize:                    b.SeedQueueSize,
		QueueStalledEnabled:              b.QueueStalledEnabled,
		QueueStalledMinutes:              b.QueueStalledMinutes,
		BlocklistEnabled:                 b.BlocklistEnabled,
		BlocklistURL:                     b.BlocklistURL,
		PortForwardingEnabled:            b.PortForwardingEnabled,
		IncompleteDir:                    b.IncompleteDir,
		IncompleteDirEnabled:             b.IncompleteDirEnabled,
		CacheSizeMB:                      b.CacheSizeMB,
		AltSpeedTimeEnabled:              b.AltSpeedTimeEnabled,
		AltSpeedTimeBegin:                b.AltSpeedTimeBegin,
		AltSpeedTimeEnd:                  b.AltSpeedTimeEnd,
		AltSpeedTimeDay:                  b.AltSpeedTimeDay,
		ScriptTorrentAddedEnabled:        b.ScriptTorrentAddedEnabled,
		ScriptTorrentAddedFilename:       b.ScriptTorrentAddedFilename,
		ScriptTorrentDoneEnabled:         b.ScriptTorrentDoneEnabled,
		ScriptTorrentDoneFilename:        b.ScriptTorrentDoneFilename,
		ScriptTorrentDoneSeedingEnabled:  b.ScriptTorrentDoneSeedingEnabled,
		ScriptTorrentDoneSeedingFilename: b.ScriptTorrentDoneSeedingFilename,
		DefaultTrackers:                  b.DefaultTrackers,
		RenamePartialFiles:               b.RenamePartialFiles,
		TrashOriginalTorrentFiles:        b.TrashOriginalTorrentFiles,
		IdleSeedingLimitEnabled:          b.IdleSeedingLimitEnabled,
		IdleSeedingLimit:                 b.IdleSeedingLimit,
		PeerPort:                         b.PeerPort,
		PeerPortRandomOnStart:            b.PeerPortRandomOnStart,
		QB:                               b.prefs(),
	}
	p.Encryption = b.Encryption
	return p
}

// prefs 取本次提交的原生偏好通道。优先 Prefs（新名），回退 QB（旧名）：
// 旧前端仍在发 qb，二者语义完全一致，同时给出时新名优先。
func (b *sessionBody) prefs() map[string]any {
	if len(b.Prefs) > 0 {
		return b.Prefs
	}
	return b.QB
}

// touchesScriptHooks 是否提交了种子事件脚本钩子字段
func (b *sessionBody) touchesScriptHooks() bool {
	return b.ScriptTorrentAddedEnabled != nil || b.ScriptTorrentAddedFilename != nil ||
		b.ScriptTorrentDoneEnabled != nil || b.ScriptTorrentDoneFilename != nil ||
		b.ScriptTorrentDoneSeedingEnabled != nil || b.ScriptTorrentDoneSeedingFilename != nil
}

// bindSession 解析会话配置请求体并执行安全门槛检查。
// 返回 false 表示已写出错误响应，调用方直接返回。
func (h *Handler) bindSession(c *gin.Context) (*sessionBody, bool) {
	var body sessionBody
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效: "+err.Error())
		return nil, false
	}
	// Transmission 会在种子事件时以 Transmission 服务账号权限执行脚本钩子：
	// 任意设置 script-torrent-done-filename 等字段，配合添加种子把恶意可执行文件
	// 下载到磁盘再指定为钩子，可形成「下载即执行」的 RCE 链。与 systemCommand
	// 一致，要求部署具备认证边界（令牌鉴权或宿主网关统一认证）才允许修改脚本字段。
	// 按服务器索引的写入口径必须与这里一致，否则前端换一条路径就能绕过门槛。
	if body.touchesScriptHooks() && h.apiToken == "" && !h.plat.SecurityPolicy().AllowEmbedding {
		respondError(c, http.StatusForbidden, "脚本钩子设置需认证：请设置 API_TOKEN 启用令牌鉴权，或经宿主网关部署")
		return nil, false
	}
	return &body, true
}

// setSession 更新会话配置（当前活动服务器）
func (h *Handler) setSession(c *gin.Context) {
	body, ok := h.bindSession(c)
	if !ok {
		return
	}
	if err := h.rpc.SetSession(c.Request.Context(), body.patch()); err != nil {
		respondBackendError(c, "更新会话失败", err)
		return
	}
	respond(c, gin.H{"updated": true})
}

var (
	errServerNotFound    = errors.New("服务器不存在")
	errServerUnavailable = errors.New("该服务器未启用或未填写地址")
)

// sameCred 判断活动连接是否就是状态列表里的这一台（只比驱动与地址，
// 密码不参与：状态文件里可能是掩码值，拿它比对会把同一台判成两台）。
func sameCred(cred rpc.Credentials, s state.Server) bool {
	if driver.NormalizeKind(string(cred.Type)) != driver.NormalizeKind(s.Type) {
		return false
	}
	trim := func(u string) string { return strings.TrimRight(strings.TrimSpace(u), "/") }
	return strings.EqualFold(trim(cred.URL), trim(s.URL)) && cred.User == s.User
}

// memberBackend 解析指定服务器索引对应的下载器后端。
// 当前连接就是这一台时直接返回活动客户端（连接由它持有）；
// 其余取聚合成员实例——成员只在「2 台以上启用」时才建立，
// 所以单台部署的索引会落到上面的活动客户端分支。
func (h *Handler) memberBackend(idx int) (driver.Backend, error) {
	st := h.state.Get()
	if idx < 0 || idx >= len(st.Servers) {
		return nil, errServerNotFound
	}
	if idx == h.activeServerIndex() {
		return h.rpc.Client(), nil
	}
	if b, ok := h.rpc.BackendAt(idx); ok {
		return b, nil
	}
	// 索引标着「活动」但当前连接其实是别的下载器（切服务器时 .env.local 写盘
	// 失败等）：宁可报错也不能读改另一台的配置
	if idx == st.ActiveServer {
		return nil, fmt.Errorf("当前连接与「%s」不一致，请重新切换到该服务器", st.Servers[idx].Name)
	}
	// 成员实例建不起来的原因（不可达、认证失败……）记在聚合错误里，
	// 回显给界面比笼统的「未启用」有用
	if msg, ok := h.rpc.AggregateErrors()[idx]; ok {
		return nil, fmt.Errorf("连接该服务器失败：%s", msg)
	}
	return nil, errServerUnavailable
}

// activeServerIndex 当前连接实际指向的服务器索引（状态列表里没有这一台时返回 -1）。
// 界面据此判断某个标签是不是「就是现在连着的那台」：状态索引与实际连接可能
// 不同步（切换时写盘失败等），所以必须由服务端给出，不能按索引猜。
func (h *Handler) activeServerIndex() int {
	st := h.state.Get()
	cred := h.rpc.Credentials()
	for i, s := range st.Servers {
		if s.URL != "" && sameCred(cred, s) {
			return i
		}
	}
	return -1
}

// getServerSession 读取指定服务器的会话配置。
// 聚合视图下设置面板用顶部标签在服务器之间切换，每台各读各的：
// 只读活动服务器会让另外几台的设置永远显示不出来。
func (h *Handler) getServerSession(c *gin.Context) {
	idx, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "无效的服务器索引")
		return
	}
	st := h.state.Get()
	b, err := h.memberBackend(idx)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errServerNotFound) {
			status = http.StatusNotFound
		}
		respondError(c, status, err.Error())
		return
	}
	sess, err := b.GetSession(c.Request.Context())
	if err != nil {
		respondBackendError(c, "获取会话信息失败", err)
		return
	}
	if sess != nil {
		sess.Type = b.Kind().String()
		caps := b.Capabilities()
		sess.Caps = &caps
		if schema := b.SettingsSchema(); len(schema) > 0 {
			sess.Schema = schema
		}
	}
	respond(c, gin.H{
		"index":   idx,
		"name":    st.Servers[idx].Name,
		"active":  idx == h.activeServerIndex(),
		"session": sess,
	})
}

// setServerSession 更新指定服务器的会话配置
func (h *Handler) setServerSession(c *gin.Context) {
	idx, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "无效的服务器索引")
		return
	}
	b, err := h.memberBackend(idx)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errServerNotFound) {
			status = http.StatusNotFound
		}
		respondError(c, status, err.Error())
		return
	}
	body, ok := h.bindSession(c)
	if !ok {
		return
	}
	if err := b.SetSession(c.Request.Context(), body.patch()); err != nil {
		respondBackendError(c, "更新会话失败", err)
		return
	}
	respond(c, gin.H{"updated": true})
}
