package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/trpanel/backend/internal/driver"
	"github.com/trpanel/backend/internal/rpc"
)

// maxTorrentFileSize 上传种子文件大小上限（10MB）
const maxTorrentFileSize = 10 << 20

// getIDParam 解析路径中的 :id
func getIDParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondError(c, http.StatusBadRequest, "无效的种子 ID")
		return 0, false
	}
	return id, true
}

// parseCSV 解析逗号分隔的字符串（multipart 表单中的标签字段）
func parseCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// torrentSites 获取每个种子的 Tracker 站点（站点过滤维度）
func (h *Handler) torrentSites(c *gin.Context) {
	sites, err := h.rpc.GetTorrentSites(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusInternalServerError, "获取站点列表失败: "+err.Error())
		return
	}
	respond(c, sites)
}

// replaceTracker 批量替换/追加所有匹配种子的 Tracker 地址
func (h *Handler) replaceTracker(c *gin.Context) {
	defer h.hub.Bump()
	var body struct {
		From   string `json:"from"`
		To     string `json:"to"`
		Append bool   `json:"append"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效")
		return
	}
	if body.From == "" || body.To == "" {
		respondError(c, http.StatusBadRequest, "查找地址与替换地址不能为空")
		return
	}
	affected, names, err := h.rpc.ReplaceTracker(c.Request.Context(), body.From, body.To, body.Append)
	if err != nil {
		respondError(c, http.StatusBadGateway, "替换 Tracker 失败: "+err.Error())
		return
	}
	respond(c, gin.H{"affected": affected, "names": names})
}

// listTorrents 获取种子列表
func (h *Handler) listTorrents(c *gin.Context) {
	torrents, err := h.rpc.GetTorrents(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusBadGateway, "获取种子列表失败: "+err.Error())
		return
	}
	respond(c, torrents)
}

// getTorrent 获取单个种子详情（含文件/Peers/Trackers）
func (h *Handler) getTorrent(c *gin.Context) {
	id, ok := getIDParam(c)
	if !ok {
		return
	}
	t, err := h.rpc.GetTorrentDetail(c.Request.Context(), id)
	if err != nil {
		respondError(c, http.StatusNotFound, err.Error())
		return
	}
	respond(c, t)
}

// addTorrent 添加种子（multipart 文件上传 或 JSON url/magnet）
func (h *Handler) addTorrent(c *gin.Context) {
	defer h.hub.Bump()
	ctx := c.Request.Context()

	// 方式一：multipart 文件上传
	if fileHeader, err := c.FormFile("file"); err == nil {
		if fileHeader.Size > maxTorrentFileSize {
			respondError(c, http.StatusBadRequest, "种子文件过大（上限 10MB）")
			return
		}
		f, err := fileHeader.Open()
		if err != nil {
			respondError(c, http.StatusInternalServerError, "读取上传文件失败: "+err.Error())
			return
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, maxTorrentFileSize))
		if err != nil {
			respondError(c, http.StatusInternalServerError, "读取上传文件失败: "+err.Error())
			return
		}
		downloadDir := c.PostForm("downloadDir")
		paused := c.PostForm("paused") == "true"
		verify := c.PostForm("verify") == "true"
		labels := parseCSV(c.PostForm("labels"))
		var priority *int64
		if v, err := strconv.ParseInt(c.PostForm("bandwidthPriority"), 10, 64); err == nil {
			priority = &v
		}
		// 文件选择：JSON 数组索引（不传=全部下载）
		var filesWanted, filesUnwanted []int64
		if raw := c.PostForm("filesWanted"); raw != "" {
			_ = json.Unmarshal([]byte(raw), &filesWanted)
		}
		if raw := c.PostForm("filesUnwanted"); raw != "" {
			_ = json.Unmarshal([]byte(raw), &filesUnwanted)
		}
		id, err := h.rpc.AddTorrentByFile(ctx, data, downloadDir, paused, labels, priority, filesWanted, filesUnwanted)
		if err != nil {
			respondError(c, http.StatusBadGateway, "添加种子失败: "+err.Error())
			return
		}
		if verify {
			if err := h.rpc.VerifyTorrents(ctx, []int64{id}); err != nil {
				respondError(c, http.StatusBadGateway, "校验种子失败: "+err.Error())
				return
			}
		}
		respond(c, gin.H{"id": id})
		return
	}

	// 方式二：JSON 提供 URL / 磁力链接 / NAS 路径（飞牛文件选择器选中路径）
	var body struct {
		URL               string   `json:"url"`
		Magnet            string   `json:"magnet"`
		Path              string   `json:"path"`
		DownloadDir       string   `json:"downloadDir"`
		Paused            bool     `json:"paused"`
		Verify            bool     `json:"verify"`
		Labels            []string `json:"labels"`
		BandwidthPriority *int64   `json:"bandwidthPriority"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效，需要 multipart 文件或 JSON {url|magnet|path}")
		return
	}
	if body.Path != "" {
		// 从 NAS 路径读取 .torrent 内容添加（飞牛文件选择器返回语义化路径）
		data, err := h.readTorrentFile(body.Path)
		if err != nil {
			slog.Warn("按路径添加种子被拒", "path", body.Path, "err", err)
			respondError(c, http.StatusForbidden, "种子路径不可用")
			return
		}
		id, err := h.rpc.AddTorrentByFile(ctx, data, body.DownloadDir, body.Paused, body.Labels, body.BandwidthPriority, nil, nil)
		if err != nil {
			respondError(c, http.StatusBadGateway, "添加种子失败: "+err.Error())
			return
		}
		if body.Verify {
			if err := h.rpc.VerifyTorrents(ctx, []int64{id}); err != nil {
				respondError(c, http.StatusBadGateway, "校验种子失败: "+err.Error())
				return
			}
		}
		respond(c, gin.H{"id": id})
		return
	}
	link := strings.TrimSpace(body.URL)
	if link == "" {
		link = strings.TrimSpace(body.Magnet)
	}
	if link == "" {
		respondError(c, http.StatusBadRequest, "缺少 URL、磁力链接或种子路径")
		return
	}
	if !validTorrentLink(link) {
		respondError(c, http.StatusBadRequest, "URL 仅支持 http(s) 链接或磁力链接")
		return
	}
	id, err := h.rpc.AddTorrentByURL(ctx, link, body.DownloadDir, body.Paused, body.Labels, body.BandwidthPriority)
	if err != nil {
		respondError(c, http.StatusBadGateway, "添加种子失败: "+err.Error())
		return
	}
	if body.Verify {
		if err := h.rpc.VerifyTorrents(ctx, []int64{id}); err != nil {
			respondError(c, http.StatusBadGateway, "校验种子失败: "+err.Error())
			return
		}
	}
	respond(c, gin.H{"id": id})
}

// validTorrentLink 校验 URL/磁力链接的 scheme（实现见 rpc.ValidTorrentLink，
// 与 MCP 的 add_torrent 共用同一份判定，避免只在一侧校验留下绕过通道）。
func validTorrentLink(link string) bool {
	return rpc.ValidTorrentLink(link)
}

// validSeedIdleLimit 校验做种空闲上限（分钟）：int64 分钟 × 60e9 纳秒（time.Minute）
// 对异常大值会环绕成负数 Duration 传给 Transmission
func validSeedIdleLimit(limit *int64) bool {
	return limit == nil || (*limit >= 0 && *limit <= 1_000_000)
}

// readTorrentFile 按宿主机路径读取种子内容（如 fnOS 文件选择器返回的路径）。
// 是否允许读取、允许哪些目录由宿主平台的 FileAccess 策略决定：
// 通用部署按 TORRENT_PATH_ROOTS 白名单限制，避免该接口沦为任意文件读取入口。
func (h *Handler) readTorrentFile(path string) ([]byte, error) {
	target, err := h.plat.FileAccess().AllowRead(path)
	if err != nil {
		return nil, err
	}
	// 读校验后解析出的真实路径，避免「按软链路径校验通过、却读到软链指向的敏感文件」
	return os.ReadFile(target)
}

// addTorrentBatch 批量添加多个URL/磁力链接。
// 单项失败不中断整批，而是收集到 failed 一并返回：前端一次调用即可拿到成败明细，
// 无需在批量接口失败后逐条重试——那样会把已成功添加的链接重复插入。
func (h *Handler) addTorrentBatch(c *gin.Context) {
	defer h.hub.Bump()
	ctx := c.Request.Context()
	var body struct {
		URLs              []string `json:"urls"`
		DownloadDir       string   `json:"downloadDir"`
		Paused            bool     `json:"paused"`
		Verify            bool     `json:"verify"`
		Labels            []string `json:"labels"`
		BandwidthPriority *int64   `json:"bandwidthPriority"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效")
		return
	}
	if len(body.URLs) == 0 {
		respondError(c, http.StatusBadRequest, "urls 不能为空")
		return
	}
	type batchFailure struct {
		URL   string `json:"url"`
		Error string `json:"error"`
	}
	ids := make([]int64, 0, len(body.URLs))
	var failed []batchFailure
	for _, raw := range body.URLs {
		url := strings.TrimSpace(raw)
		if !validTorrentLink(url) {
			failed = append(failed, batchFailure{URL: url, Error: "仅支持 http(s) 链接或磁力链接"})
			continue
		}
		id, err := h.rpc.AddTorrentByURL(ctx, url, body.DownloadDir, body.Paused, body.Labels, body.BandwidthPriority)
		if err != nil {
			failed = append(failed, batchFailure{URL: url, Error: rpc.SanitizeClientMsg(err.Error())})
			continue
		}
		ids = append(ids, id)
	}
	if body.Verify && len(ids) > 0 {
		if err := h.rpc.VerifyTorrents(ctx, ids); err != nil {
			respondError(c, http.StatusBadGateway, "校验种子失败: "+err.Error())
			return
		}
	}
	out := gin.H{"ids": ids, "failed": failed}
	if len(failed) > 0 {
		out["warning"] = strconv.Itoa(len(failed)) + " 个链接添加失败"
	}
	respond(c, out)
}

// startTorrent 启动单个种子
func (h *Handler) startTorrent(c *gin.Context) {
	defer h.hub.Bump()
	id, ok := getIDParam(c)
	if !ok {
		return
	}
	if err := h.rpc.StartTorrents(c.Request.Context(), []int64{id}); err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	respond(c, gin.H{"id": id})
}

// startNowTorrent 强制立即开始单个种子
func (h *Handler) startNowTorrent(c *gin.Context) {
	defer h.hub.Bump()
	id, ok := getIDParam(c)
	if !ok {
		return
	}
	if err := h.rpc.StartTorrentsNow(c.Request.Context(), []int64{id}); err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	respond(c, gin.H{"id": id})
}

// stopTorrent 暂停单个种子
func (h *Handler) stopTorrent(c *gin.Context) {
	defer h.hub.Bump()
	id, ok := getIDParam(c)
	if !ok {
		return
	}
	if err := h.rpc.StopTorrents(c.Request.Context(), []int64{id}); err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	respond(c, gin.H{"id": id})
}

// verifyTorrent 校验单个种子
func (h *Handler) verifyTorrent(c *gin.Context) {
	defer h.hub.Bump()
	id, ok := getIDParam(c)
	if !ok {
		return
	}
	if err := h.rpc.VerifyTorrents(c.Request.Context(), []int64{id}); err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	respond(c, gin.H{"id": id})
}

// reannounceTorrent 重新宣告 Tracker
func (h *Handler) reannounceTorrent(c *gin.Context) {
	defer h.hub.Bump()
	id, ok := getIDParam(c)
	if !ok {
		return
	}
	if err := h.rpc.ReannounceTorrents(c.Request.Context(), []int64{id}); err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	respond(c, gin.H{"id": id})
}

// renameTorrent 重命名种子文件或目录
func (h *Handler) renameTorrent(c *gin.Context) {
	defer h.hub.Bump()
	id, ok := getIDParam(c)
	if !ok {
		return
	}
	var body struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效")
		return
	}
	if body.Name == "" {
		respondError(c, http.StatusBadRequest, "缺少新的名称")
		return
	}
	if err := h.rpc.RenameFile(c.Request.Context(), id, body.Path, body.Name); err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	respond(c, gin.H{"id": id})
}

// moveTorrent 移动种子下载位置
func (h *Handler) moveTorrent(c *gin.Context) {
	defer h.hub.Bump()
	id, ok := getIDParam(c)
	if !ok {
		return
	}
	var body struct {
		Location string `json:"location"`
		Move     *bool  `json:"move"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效")
		return
	}
	if body.Location == "" {
		respondError(c, http.StatusBadRequest, "缺少 location")
		return
	}
	move := true
	if body.Move != nil {
		move = *body.Move
	}
	if err := h.rpc.SetTorrentLocation(c.Request.Context(), id, body.Location, move); err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	respond(c, gin.H{"id": id})
}

// queueMove 调整队列位置
func (h *Handler) queueMove(c *gin.Context) {
	defer h.hub.Bump()
	id, ok := getIDParam(c)
	if !ok {
		return
	}
	var body struct {
		Direction string `json:"direction"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效")
		return
	}
	if err := h.rpc.QueueMove(c.Request.Context(), []int64{id}, body.Direction); err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	respond(c, gin.H{"id": id})
}

// torrentUpdateBody 种子属性修改请求体（单条/批量共用）
type torrentUpdateBody struct {
	Location            *string   `json:"location"`
	Move                *bool     `json:"move"`
	SequentialDownload  *bool     `json:"sequentialDownload"`
	Groups              *[]string `json:"groups"`
	Labels              []string  `json:"labels"`
	BandwidthPriority   *int64    `json:"bandwidthPriority"`
	TrackerList         []string  `json:"trackerList"`
	DownloadLimit       *int64    `json:"downloadLimit"`
	DownloadLimited     *bool     `json:"downloadLimited"`
	UploadLimit         *int64    `json:"uploadLimit"`
	UploadLimited       *bool     `json:"uploadLimited"`
	HonorsSessionLimits *bool     `json:"honorsSessionLimits"`
	PeerLimit           *int64    `json:"peerLimit"`
	SeedRatioLimit      *float64  `json:"seedRatioLimit"`
	SeedRatioMode       *int64    `json:"seedRatioMode"`
	QueuePosition       *int64    `json:"queuePosition"`
	FilesWanted         []int64   `json:"filesWanted"`
	FilesUnwanted       []int64   `json:"filesUnwanted"`
	PriorityHigh        []int64   `json:"priorityHigh"`
	PriorityLow         []int64   `json:"priorityLow"`
	PriorityNormal      []int64   `json:"priorityNormal"`
	SeedIdleMode        *int64    `json:"seedIdleMode"`
	SeedIdleLimit       *int64    `json:"seedIdleLimit"`
}

// buildTorrentSetPayload 将请求体转换为下载器无关的修改请求
func buildTorrentSetPayload(body *torrentUpdateBody) driver.TorrentPatch {
	payload := driver.TorrentPatch{
		Labels:              body.Labels,
		BandwidthPriority:   body.BandwidthPriority,
		TrackerList:         body.TrackerList,
		DownloadLimit:       body.DownloadLimit,
		DownloadLimited:     body.DownloadLimited,
		UploadLimit:         body.UploadLimit,
		UploadLimited:       body.UploadLimited,
		HonorsSessionLimits: body.HonorsSessionLimits,
		PeerLimit:           body.PeerLimit,
		SeedRatioLimit:      body.SeedRatioLimit,
		QueuePosition:       body.QueuePosition,
		FilesWanted:         body.FilesWanted,
		FilesUnwanted:       body.FilesUnwanted,
		PriorityHigh:        body.PriorityHigh,
		PriorityLow:         body.PriorityLow,
		PriorityNormal:      body.PriorityNormal,
		SeedIdleMode:        body.SeedIdleMode,
	}
	if body.SeedIdleLimit != nil {
		payload.SeedIdleLimitMin = body.SeedIdleLimit
	}
	if body.SeedRatioMode != nil {
		payload.SeedRatioMode = body.SeedRatioMode
	}
	return payload
}

// applyRawFields 应用语义通用、但 Transmission RPC 库未封装的开关
// （sequentialDownload / groups）。qBittorrent 驱动会把 groups 映射为分类。
func (h *Handler) applyRawFields(ctx context.Context, ids []int64, body *torrentUpdateBody) error {
	flags := driver.TorrentFlagPatch{
		SequentialDownload: body.SequentialDownload,
	}
	if body.Groups != nil {
		groups := *body.Groups
		flags.Groups = groups
	}
	if flags.Empty() {
		return nil
	}
	err := h.rpc.SetTorrentFlags(ctx, ids, flags)
	if err != nil {
		// 顺序下载 / 带宽组为 Transmission 4.x 字段，旧版本不识别：告警降级而非整体失败
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "unrecognized") || strings.Contains(msg, "unknown key") || errors.Is(err, driver.ErrUnsupported) {
			slog.Warn("当前下载器不支持的种子字段已跳过", "err", err)
			return nil
		}
		return err
	}
	return nil
}

// updateTorrent 修改单个种子属性（路径/标签/优先级/Tracker/限速等）
func (h *Handler) updateTorrent(c *gin.Context) {
	defer h.hub.Bump()
	id, ok := getIDParam(c)
	if !ok {
		return
	}
	var body torrentUpdateBody
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	if !validSeedIdleLimit(body.SeedIdleLimit) {
		respondError(c, http.StatusBadRequest, "做种空闲上限超出合理范围（0-1000000 分钟）")
		return
	}
	// 若同时提供 location 且未单独走 move 接口，则移动位置
	if body.Location != nil {
		move := true
		if body.Move != nil {
			move = *body.Move
		}
		if err := h.rpc.SetTorrentLocation(c.Request.Context(), id, *body.Location, move); err != nil {
			respondError(c, http.StatusBadGateway, err.Error())
			return
		}
	}
	if err := h.rpc.SetTorrent(c.Request.Context(), []int64{id}, buildTorrentSetPayload(&body)); err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	if err := h.applyRawFields(c.Request.Context(), []int64{id}, &body); err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	respond(c, gin.H{"id": id})
}

// updateTorrents 批量修改种子属性
func (h *Handler) updateTorrents(c *gin.Context) {
	defer h.hub.Bump()
	var body struct {
		IDs []int64 `json:"ids"`
		torrentUpdateBody
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	if len(body.IDs) == 0 {
		respondError(c, http.StatusBadRequest, "缺少 ids")
		return
	}
	if !validSeedIdleLimit(body.SeedIdleLimit) {
		respondError(c, http.StatusBadRequest, "做种空闲上限超出合理范围（0-1000000 分钟）")
		return
	}
	if body.Location != nil {
		move := true
		if body.Move != nil {
			move = *body.Move
		}
		for _, id := range body.IDs {
			if err := h.rpc.SetTorrentLocation(c.Request.Context(), id, *body.Location, move); err != nil {
				respondError(c, http.StatusBadGateway, err.Error())
				return
			}
		}
	}
	if err := h.rpc.SetTorrent(c.Request.Context(), body.IDs, buildTorrentSetPayload(&body.torrentUpdateBody)); err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	if err := h.applyRawFields(c.Request.Context(), body.IDs, &body.torrentUpdateBody); err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	respond(c, gin.H{"ids": body.IDs})
}

// moveTorrents 批量移动种子下载位置
func (h *Handler) moveTorrents(c *gin.Context) {
	defer h.hub.Bump()
	var body struct {
		IDs      []int64 `json:"ids"`
		Location string  `json:"location"`
		Move     *bool   `json:"move"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效")
		return
	}
	if len(body.IDs) == 0 {
		respondError(c, http.StatusBadRequest, "缺少 ids")
		return
	}
	if body.Location == "" {
		respondError(c, http.StatusBadRequest, "缺少 location")
		return
	}
	move := true
	if body.Move != nil {
		move = *body.Move
	}
	for _, id := range body.IDs {
		if err := h.rpc.SetTorrentLocation(c.Request.Context(), id, body.Location, move); err != nil {
			respondError(c, http.StatusBadGateway, err.Error())
			return
		}
	}
	respond(c, gin.H{"ids": body.IDs})
}

// deleteTorrent 删除单个种子（query: deleteData=true 同时删除本地数据）
func (h *Handler) deleteTorrent(c *gin.Context) {
	defer h.hub.Bump()
	id, ok := getIDParam(c)
	if !ok {
		return
	}
	deleteData := c.Query("deleteData") == "true"
	if err := h.rpc.RemoveTorrents(c.Request.Context(), []int64{id}, deleteData); err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	respond(c, gin.H{"id": id})
}

// parseIDsBody 解析批量操作的 ids 请求体
func parseIDsBody(c *gin.Context) ([]int64, bool) {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效: "+err.Error())
		return nil, false
	}
	if len(body.IDs) == 0 {
		respondError(c, http.StatusBadRequest, "缺少 ids")
		return nil, false
	}
	return body.IDs, true
}

// startTorrents 批量启动
func (h *Handler) startTorrents(c *gin.Context) {
	defer h.hub.Bump()
	ids, ok := parseIDsBody(c)
	if !ok {
		return
	}
	if err := h.rpc.StartTorrents(c.Request.Context(), ids); err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	respond(c, gin.H{"ids": ids})
}

// startNowTorrents 批量强制立即开始
func (h *Handler) startNowTorrents(c *gin.Context) {
	defer h.hub.Bump()
	ids, ok := parseIDsBody(c)
	if !ok {
		return
	}
	if err := h.rpc.StartTorrentsNow(c.Request.Context(), ids); err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	respond(c, gin.H{"ids": ids})
}

// stopTorrents 批量暂停
func (h *Handler) stopTorrents(c *gin.Context) {
	defer h.hub.Bump()
	ids, ok := parseIDsBody(c)
	if !ok {
		return
	}
	if err := h.rpc.StopTorrents(c.Request.Context(), ids); err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	respond(c, gin.H{"ids": ids})
}

// deleteTorrents 批量删除（可同时删除本地数据）
func (h *Handler) deleteTorrents(c *gin.Context) {
	defer h.hub.Bump()
	var body struct {
		IDs        []int64 `json:"ids"`
		DeleteData bool    `json:"deleteData"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	if len(body.IDs) == 0 {
		respondError(c, http.StatusBadRequest, "缺少 ids")
		return
	}
	if err := h.rpc.RemoveTorrents(c.Request.Context(), body.IDs, body.DeleteData); err != nil {
		respondError(c, http.StatusBadGateway, err.Error())
		return
	}
	respond(c, gin.H{"ids": body.IDs})
}

// startAllTorrents 批量启动所有种子（传入空 ids 表示全部）
func (h *Handler) startAllTorrents(c *gin.Context) {
	defer h.hub.Bump()
	if err := h.rpc.StartTorrents(c.Request.Context(), []int64{}); err != nil {
		respondError(c, http.StatusBadGateway, "启动失败: "+err.Error())
		return
	}
	respond(c, gin.H{})
}

// pauseAllTorrents 批量暂停所有种子（传入空 ids 表示全部）
func (h *Handler) pauseAllTorrents(c *gin.Context) {
	defer h.hub.Bump()
	if err := h.rpc.StopTorrents(c.Request.Context(), []int64{}); err != nil {
		respondError(c, http.StatusBadGateway, "暂停失败: "+err.Error())
		return
	}
	respond(c, gin.H{})
}

// reannounceAllTorrents 对所有种子重新宣告 Tracker
func (h *Handler) reannounceAllTorrents(c *gin.Context) {
	defer h.hub.Bump()
	if err := h.rpc.ReannounceTorrents(c.Request.Context(), []int64{}); err != nil {
		respondError(c, http.StatusBadGateway, "重新宣告失败: "+err.Error())
		return
	}
	respond(c, gin.H{})
}
