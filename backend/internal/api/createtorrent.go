package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/trpanel/backend/internal/rpc"
	"github.com/trpanel/backend/internal/torrentcreate"
)

// createJob 一次后端建种任务的运行时状态（内存态，进程重启即失效）。
// 后台构建协程写入、HTTP 查询协程读取，故除 Processed（原子计数）外的字段
// 一律经 mu 保护；CreatedAt 在入表前写入，之后只读，无需加锁。
type createJob struct {
	mu        sync.Mutex
	status    string // running | done | error
	total     int64
	processed atomic.Int64
	name      string
	errMsg    string
	data      []byte
	autoAdded bool
	CreatedAt time.Time
}

// progress 更新进度（total 与其它字段一样受 mu 保护，processed 用原子计数）
func (j *createJob) progress(processed, total int64) {
	j.mu.Lock()
	j.total = total
	j.mu.Unlock()
	j.processed.Store(processed)
}

// finish 标记任务结束（成功或失败）
func (j *createJob) finish(name string, data []byte, errMsg string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if errMsg != "" {
		j.status = "error"
		j.errMsg = errMsg
		return
	}
	j.name, j.data = name, data
	j.status = "done"
}

// markAutoAdded 记录自动添加结果（不改变任务状态）
func (j *createJob) markAutoAdded(errMsg string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if errMsg != "" {
		j.errMsg = errMsg
		return
	}
	j.autoAdded = true
}

// snapshot 读取任务当前状态
func (j *createJob) snapshot() (status, name, errMsg string, data []byte, total int64, autoAdded bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.status, j.name, j.errMsg, j.data, j.total, j.autoAdded
}

// createJobTTL 任务结果保留时长，过期后在下次创建时惰性清理
const createJobTTL = time.Hour

// createTorrent 提交一次后端建种任务（服务器路径 → 多线程哈希 → .torrent）。
// 路径合法性（白名单、符号链接逃逸）由宿主 FileAccess 策略校验。
func (h *Handler) createTorrent(c *gin.Context) {
	var body struct {
		Path         string   `json:"path"`
		Announce     string   `json:"announce"`
		AnnounceList []string `json:"announceList"` // 每行一个 tier，行内逗号分隔
		Comment      string   `json:"comment"`
		Private      bool     `json:"private"`
		PieceLength  int64    `json:"pieceLength"`
		WebSeeds     []string `json:"webSeeds"`
		AutoAdd      bool     `json:"autoAdd"`
		DownloadDir  string   `json:"downloadDir"`
		Paused       bool     `json:"paused"`
		Labels       []string `json:"labels"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	if strings.TrimSpace(body.Path) == "" {
		respondError(c, http.StatusBadRequest, "缺少源路径")
		return
	}
	fa := h.plat.FileAccess()
	if !fa.Enabled() {
		respondError(c, http.StatusForbidden, "当前部署未启用按路径读取文件（需配置 TORRENT_PATH_ROOTS）")
		return
	}
	// 目录或文件分别校验；目录建种时逐文件再过 AllowRead 防软链逃逸白名单
	fi, err := os.Stat(body.Path)
	var root string
	if err == nil && fi.IsDir() {
		root, err = fa.AllowReadDir(body.Path)
	} else {
		root, err = fa.AllowRead(body.Path)
	}
	if err != nil {
		slog.Warn("后端建种路径被拒", "path", body.Path, "err", err)
		respondError(c, http.StatusForbidden, "源路径不可用")
		return
	}

	opts := torrentcreate.Options{
		Announce:     body.Announce,
		AnnounceList: parseAnnounceTiers(body.AnnounceList),
		Comment:      body.Comment,
		Private:      body.Private,
		CreatedBy:    "trpanel",
		WebSeeds:     body.WebSeeds,
		PieceLength:  body.PieceLength,
	}

	job := &createJob{status: "running", CreatedAt: time.Now()}
	jobID, err := h.storeCreateJob(job)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "创建任务失败")
		return
	}

	// 后台执行：请求立即返回 jobId，前端轮询进度
	go func() {
		cancel := make(chan struct{})
		data, name, err := torrentcreate.Build(root, opts, job.progress, cancel)
		if err != nil {
			job.finish("", nil, rpc.SanitizeClientMsg(err.Error()))
			slog.Error("后端建种失败", "path", body.Path, "err", err)
			return
		}
		job.finish(name, data, "")
		if body.AutoAdd {
			// 建种完成后自动添加到 Transmission（暂停态可选）
			ctx, cancelAdd := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancelAdd()
			if _, err := h.rpc.AddTorrentByFile(ctx, data, body.DownloadDir, body.Paused, body.Labels, nil, nil, nil); err != nil {
				slog.Error("后端建种后自动添加失败", "err", err)
				job.markAutoAdded("种子已生成，但自动添加失败: " + rpc.SanitizeClientMsg(err.Error()))
			} else {
				job.markAutoAdded("")
				h.hub.Bump()
			}
		}
	}()

	respond(c, gin.H{"jobId": jobID})
}

// createTorrentStatus 查询建种任务进度
func (h *Handler) createTorrentStatus(c *gin.Context) {
	job := h.getCreateJob(c.Param("jobId"))
	if job == nil {
		respondError(c, http.StatusNotFound, "任务不存在或已过期")
		return
	}
	status, name, errMsg, _, total, autoAdded := job.snapshot()
	respond(c, gin.H{
		"status":    status,
		"processed": job.processed.Load(),
		"total":     total,
		"name":      name,
		"error":     errMsg,
		"autoAdded": autoAdded,
	})
}

// createTorrentFile 下载已生成的 .torrent 文件
func (h *Handler) createTorrentFile(c *gin.Context) {
	job := h.getCreateJob(c.Param("jobId"))
	if job == nil {
		respondError(c, http.StatusNotFound, "任务不存在、未完成或已过期")
		return
	}
	status, name, _, data, _, _ := job.snapshot()
	if status != "done" || len(data) == 0 {
		respondError(c, http.StatusNotFound, "任务不存在、未完成或已过期")
		return
	}
	if name == "" {
		name = "torrent"
	}
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + ".torrent"}))
	c.Data(http.StatusOK, "application/x-bittorrent", data)
}

// ---- 任务存储 ----

func (h *Handler) storeCreateJob(job *createJob) (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	id := hex.EncodeToString(buf)
	h.createMu.Lock()
	// 惰性清理过期任务
	now := time.Now()
	for k, j := range h.createJobs {
		if now.Sub(j.CreatedAt) > createJobTTL {
			delete(h.createJobs, k)
		}
	}
	h.createJobs[id] = job
	h.createMu.Unlock()
	return id, nil
}

func (h *Handler) getCreateJob(id string) *createJob {
	h.createMu.Lock()
	defer h.createMu.Unlock()
	return h.createJobs[id]
}

// parseAnnounceTiers 解析多 tracker 分层输入：每行一个 tier，行内逗号分隔
func parseAnnounceTiers(lines []string) [][]string {
	var tiers [][]string
	for _, line := range lines {
		var tier []string
		for _, part := range strings.Split(line, ",") {
			if u := strings.TrimSpace(part); u != "" {
				tier = append(tier, u)
			}
		}
		if len(tier) > 0 {
			tiers = append(tiers, tier)
		}
	}
	return tiers
}
