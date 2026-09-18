package rpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	trpc "github.com/hekmon/transmissionrpc/v3"
)

// Client Transmission RPC 客户端封装
type Client struct {
	tr         *trpc.Client
	url        string
	user       string
	pass       string
	httpClient *http.Client // 自建 HTTP 客户端（超时/TLS 下限），RawCall 与库共用
	sessionMu  sync.Mutex   // 保护 sessionID 的并发读写
	sessionID  string       // raw RPC 使用的会话 ID

	listMu     sync.Mutex // 保护列表缓存
	listCache  []*Torrent // 列表缓存（共享只读，调用方不得修改元素）
	listCached time.Time
}

// listCacheTTL 列表缓存有效期。590+ 种子时 Transmission 全量响应约 5s，
// 缓存可让 REST 兜底轮询几乎瞬时返回，同时显著降低对 Transmission 的请求压力。
const listCacheTTL = 6 * time.Second

// New 创建 RPC 客户端
func New(transmissionURL, user, pass string) (*Client, error) {
	u, err := url.Parse(transmissionURL)
	if err != nil {
		return nil, fmt.Errorf("Transmission URL 无效: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("Transmission URL 协议必须是 http/https，当前为 %q", u.Scheme)
	}
	if user != "" {
		u.User = url.UserPassword(user, pass)
	}
	httpClient := newHTTPClient()
	tr, err := trpc.New(u, &trpc.Config{CustomClient: httpClient})
	if err != nil {
		return nil, fmt.Errorf("初始化 Transmission 客户端失败: %w", err)
	}
	return &Client{tr: tr, url: transmissionURL, user: user, pass: pass, httpClient: httpClient}, nil
}

// newHTTPClient 构造访问 Transmission RPC 的专用 HTTP 客户端。
// 不复用 http.DefaultClient / 库的缺省客户端：两者都没有整体超时与 TLS 版本下限，
// 上游挂起会拖住请求协程，明文降级到 TLS 1.0/1.1 也无从拒绝。
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
			MaxIdleConns:          16,
			MaxIdleConnsPerHost:   8,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

// Ping 检测连接是否可用并返回版本信息
func (c *Client) Ping(ctx context.Context) (version string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	sess, err := c.tr.SessionArgumentsGetAll(ctx)
	if err != nil {
		return "", err
	}
	if sess.Version != nil {
		return *sess.Version, nil
	}
	return "", fmt.Errorf("会话无版本信息")
}

// 列表接口需要的字段（使用 hekmon 库的 json tag）。
// 刻意排除 files/peers/trackers/pieces 等重字段，590+ 种子全量拉取时能显著提速。
var listTorrentFields = []string{
	"id", "name", "hashString", "creator", "totalSize", "sizeWhenDone", "percentDone",
	"status", "rateDownload", "rateUpload", "eta", "uploadedEver", "downloadedEver",
	"uploadRatio", "secondsSeeding", "error", "errorString", "labels", "queuePosition",
	"peersConnected", "peersSendingToUs", "peersGettingFromUs", "downloadDir",
	"addedDate", "doneDate", "activityDate", "isFinished", "isStalled",
	"isPrivate", "magnetLink", "file-count", "haveValid", "haveUnchecked",
	"leftUntilDone", "comment", "peer-limit", "seedIdleLimit", "seedIdleMode",
	"seedRatioLimit", "seedRatioMode", "bandwidthPriority", "downloadLimited",
	"downloadLimit", "uploadLimited", "uploadLimit", "honorsSessionLimits",
	"trackerStats",
}

// GetTorrents 获取种子列表（仅列表展示字段，不含详情字段）。
// 结果带 TTL 缓存，供 WebSocket 轮询与 REST 兜底共享，减少对 Transmission 的重复全量请求。
func (c *Client) GetTorrents(ctx context.Context) ([]*Torrent, error) {
	return c.getTorrents(ctx, false)
}

// GetTorrentsFresh 忽略 TTL 缓存强制拉取。
// 写操作（添加/删除/改属性）之后的刷新必须走这里，否则会把变更前缓存的旧列表
// 当作最新结果广播出去，界面最长 6 秒都看不到刚才的变更。
func (c *Client) GetTorrentsFresh(ctx context.Context) ([]*Torrent, error) {
	return c.getTorrents(ctx, true)
}

func (c *Client) getTorrents(ctx context.Context, force bool) ([]*Torrent, error) {
	c.listMu.Lock()
	if !force && c.listCache != nil && time.Since(c.listCached) < listCacheTTL {
		out := c.listCache
		c.listMu.Unlock()
		return out, nil
	}
	c.listMu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	torrents, err := c.tr.TorrentGet(ctx, listTorrentFields, nil)
	if err != nil {
		return nil, err
	}
	mapped := mapTorrents(torrents)
	// 合并库未实现的字段（groups 带宽组）。失败不阻塞列表：字段属于增强信息
	if raw, err := c.GetTorrentRawFields(ctx, nil); err == nil {
		for _, t := range mapped {
			if m, ok := raw[t.ID]; ok {
				applyRawTorrent(t, m)
			}
		}
	} else {
		slog.Debug("合并带宽组字段失败", "err", err)
	}
	c.listMu.Lock()
	c.listCache = mapped
	c.listCached = time.Now()
	c.listMu.Unlock()
	return mapped, nil
}

// applyRawTorrent 将 raw RPC 取回的库外字段合并进 Torrent
func applyRawTorrent(t *Torrent, m map[string]any) {
	if v, ok := m["sequentialDownload"].(bool); ok {
		t.SequentialDownload = v
	}
	if arr, ok := m["groups"].([]any); ok {
		groups := make([]string, 0, len(arr))
		for _, g := range arr {
			if s, ok := g.(string); ok {
				groups = append(groups, s)
			}
		}
		t.Groups = groups
	}
}

// GetTorrentDetail 获取单个种子详情（含文件/Peers/Trackers）
func (c *Client) GetTorrentDetail(ctx context.Context, id int64) (*Torrent, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	torrents, err := c.tr.TorrentGetAllFor(ctx, []int64{id})
	if err != nil {
		return nil, err
	}
	if len(torrents) == 0 {
		return nil, fmt.Errorf("种子 %d 不存在", id)
	}
	t := mapTorrent(torrents[0], true)
	// 合并库未实现的字段（sequentialDownload / groups）
	if raw, err := c.GetTorrentRawFields(ctx, []int64{id}); err == nil {
		if m, ok := raw[id]; ok {
			applyRawTorrent(t, m)
		}
	}
	// 合并块位图（pieces / pieceCount / pieceSize）
	if raw, err := c.GetTorrentPieces(ctx, []int64{id}); err == nil {
		if m, ok := raw[id]; ok {
			if v, ok := m["pieces"].(string); ok {
				t.Pieces = v
			}
			if v, ok := m["pieceCount"].(float64); ok {
				t.PieceCount = int64(v)
			}
			if v, ok := m["pieceSize"].(float64); ok {
				t.PieceSize = int64(v)
			}
		}
	}
	return t, nil
}

// AddTorrentByFile 通过文件内容添加种子（filesWanted/filesUnwanted 为文件索引，空=全部下载）
func (c *Client) AddTorrentByFile(ctx context.Context, data []byte, downloadDir string, paused bool, labels []string, priority *int64, filesWanted, filesUnwanted []int64) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	metaInfo := base64Encode(data)
	payload := trpc.TorrentAddPayload{MetaInfo: &metaInfo, Paused: &paused}
	if downloadDir != "" {
		payload.DownloadDir = &downloadDir
	}
	if len(labels) > 0 {
		payload.Labels = labels
	}
	if priority != nil {
		payload.BandwidthPriority = priority
	}
	if len(filesWanted) > 0 {
		payload.FilesWanted = filesWanted
	}
	if len(filesUnwanted) > 0 {
		payload.FilesUnwanted = filesUnwanted
	}
	t, err := c.tr.TorrentAdd(ctx, payload)
	if err != nil {
		return 0, err
	}
	if t.ID == nil {
		return 0, fmt.Errorf("添加成功但未返回 ID")
	}
	return *t.ID, nil
}

// AddTorrentByURL 通过 URL/磁力链接添加种子
func (c *Client) AddTorrentByURL(ctx context.Context, link, downloadDir string, paused bool, labels []string, priority *int64) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	payload := trpc.TorrentAddPayload{Filename: &link, Paused: &paused}
	if downloadDir != "" {
		payload.DownloadDir = &downloadDir
	}
	if len(labels) > 0 {
		payload.Labels = labels
	}
	if priority != nil {
		payload.BandwidthPriority = priority
	}
	t, err := c.tr.TorrentAdd(ctx, payload)
	if err != nil {
		return 0, err
	}
	if t.ID == nil {
		return 0, fmt.Errorf("添加成功但未返回 ID")
	}
	return *t.ID, nil
}

// StartTorrents 开始下载（空切片表示全部）
func (c *Client) StartTorrents(ctx context.Context, ids []int64) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.tr.TorrentStartIDs(ctx, ids)
}

// StopTorrents 暂停下载
func (c *Client) StopTorrents(ctx context.Context, ids []int64) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.tr.TorrentStopIDs(ctx, ids)
}

// StartTorrentsNow 强制立即开始（忽略队列限制）
func (c *Client) StartTorrentsNow(ctx context.Context, ids []int64) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.tr.TorrentStartNowIDs(ctx, ids)
}

// GetSessionStats 获取会话统计（累计/当前）
func (c *Client) GetSessionStats(ctx context.Context) (*SessionStats, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stats, err := c.tr.SessionStats(ctx)
	if err != nil {
		return nil, err
	}
	return &SessionStats{
		ActiveTorrentCount: stats.ActiveTorrentCount,
		DownloadSpeed:      stats.DownloadSpeed,
		PausedTorrentCount: stats.PausedTorrentCount,
		TorrentCount:       stats.TorrentCount,
		UploadSpeed:        stats.UploadSpeed,
		Cumulative: SessionStatsDetails{
			DownloadedBytes: stats.CumulativeStats.DownloadedBytes,
			FilesAdded:      stats.CumulativeStats.FilesAdded,
			SecondsActive:   stats.CumulativeStats.SecondsActive,
			SessionCount:    stats.CumulativeStats.SessionCount,
			UploadedBytes:   stats.CumulativeStats.UploadedBytes,
		},
		Current: SessionStatsDetails{
			DownloadedBytes: stats.CurrentStats.DownloadedBytes,
			FilesAdded:      stats.CurrentStats.FilesAdded,
			SecondsActive:   stats.CurrentStats.SecondsActive,
			SessionCount:    stats.CurrentStats.SessionCount,
			UploadedBytes:   stats.CurrentStats.UploadedBytes,
		},
	}, nil
}

// SessionStats 会话统计数据
type SessionStats struct {
	ActiveTorrentCount int64               `json:"activeTorrentCount"`
	DownloadSpeed      int64               `json:"downloadSpeed"`
	PausedTorrentCount int64               `json:"pausedTorrentCount"`
	TorrentCount       int64               `json:"torrentCount"`
	UploadSpeed        int64               `json:"uploadSpeed"`
	Cumulative         SessionStatsDetails `json:"cumulative"`
	Current            SessionStatsDetails `json:"current"`
}

// SessionStatsDetails 会话统计明细
type SessionStatsDetails struct {
	DownloadedBytes int64 `json:"downloadedBytes"`
	FilesAdded      int64 `json:"filesAdded"`
	SecondsActive   int64 `json:"secondsActive"`
	SessionCount    int64 `json:"sessionCount"`
	UploadedBytes   int64 `json:"uploadedBytes"`
}

// VerifyTorrents 校验种子
func (c *Client) VerifyTorrents(ctx context.Context, ids []int64) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.tr.TorrentVerifyIDs(ctx, ids)
}

// ReannounceTorrents 重新宣告 Tracker
func (c *Client) ReannounceTorrents(ctx context.Context, ids []int64) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.tr.TorrentReannounceIDs(ctx, ids)
}

// RemoveTorrents 删除种子（可同时删除本地数据）
func (c *Client) RemoveTorrents(ctx context.Context, ids []int64, deleteData bool) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.tr.TorrentRemove(ctx, trpc.TorrentRemovePayload{
		IDs:             ids,
		DeleteLocalData: deleteData,
	})
}

// SetTorrent 修改种子属性
func (c *Client) SetTorrent(ctx context.Context, ids []int64, fields trpc.TorrentSetPayload) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	fields.IDs = ids
	return c.tr.TorrentSet(ctx, fields)
}

// SetTorrentLocation 移动种子下载位置
func (c *Client) SetTorrentLocation(ctx context.Context, id int64, location string, move bool) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.tr.TorrentSetLocation(ctx, id, location, move)
}

// QueueMove 移动队列位置（direction: top/up/down/bottom）
func (c *Client) QueueMove(ctx context.Context, ids []int64, direction string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	switch direction {
	case "top":
		return c.tr.QueueMoveTop(ctx, ids)
	case "up":
		return c.tr.QueueMoveUp(ctx, ids)
	case "down":
		return c.tr.QueueMoveDown(ctx, ids)
	case "bottom":
		return c.tr.QueueMoveBottom(ctx, ids)
	default:
		return fmt.Errorf("无效的队列方向: %s", direction)
	}
}

// GetTorrentSites 获取每个种子关联的 Tracker 站点（用于站点维度过滤）。
// 只请求 id + trackers 两个字段：TorrentGetAll 会把全部种子的 files/peers/pieces
// 一并序列化，590 个种子时 transmission 端耗时约 9s（真机实测 2026-09），
// 首屏打开时该接口与列表并发，会把应用打开拖到秒级。
func (c *Client) GetTorrentSites(ctx context.Context) (map[int64][]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	torrents, err := c.tr.TorrentGet(ctx, []string{"id", "trackers"}, nil)
	if err != nil {
		return nil, err
	}
	result := make(map[int64][]string)
	for _, t := range torrents {
		if t.ID == nil {
			continue
		}
		seen := make(map[string]struct{})
		var sites []string
		for _, tracker := range t.Trackers {
			name := tracker.SiteName
			if name == "" {
				name = tracker.Announce
			}
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			sites = append(sites, name)
		}
		if len(sites) > 0 {
			result[*t.ID] = sites
		}
	}
	return result, nil
}

// ReplaceTracker 在所有种子中批量替换/追加 Tracker 地址（含匹配的 URL 替换为新地址或追加），
// 返回受影响的种子数及种子名称列表（供前端预览）。
// 只请求替换所需字段，理由同 GetTorrentSites（TorrentGetAll 全字段在大量种子时极慢）。
func (c *Client) ReplaceTracker(ctx context.Context, from, to string, appendMode bool) (int64, []string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	torrents, err := c.tr.TorrentGet(ctx, []string{"id", "name", "trackers"}, nil)
	if err != nil {
		return 0, nil, err
	}
	// from 支持正则表达式（功能文档约定），非法正则回退为子串匹配
	re, reErr := regexp.Compile(from)
	var affected int64
	var names []string
	for _, t := range torrents {
		if t.ID == nil || len(t.Trackers) == 0 {
			continue
		}
		matched := false
		var urls []string
		seen := make(map[string]struct{})
		for _, tracker := range t.Trackers {
			u := tracker.Announce
			if u == "" {
				continue
			}
			isMatch := false
			if reErr != nil {
				isMatch = strings.Contains(u, from)
			} else if re.MatchString(u) {
				isMatch = true
			}
			if isMatch {
				matched = true
				if appendMode {
					// 追加模式：保留原地址，目标地址若不存在则追加到末尾
					if u == to {
						if _, ok := seen[u]; ok {
							continue
						}
						seen[u] = struct{}{}
						urls = append(urls, u)
						continue
					}
					if _, ok := seen[u]; !ok {
						seen[u] = struct{}{}
						urls = append(urls, u)
					}
					if _, ok := seen[to]; !ok {
						seen[to] = struct{}{}
						urls = append(urls, to)
					}
					continue
				}
				u = to
			}
			if u == "" {
				continue
			}
			if _, ok := seen[u]; ok {
				continue
			}
			seen[u] = struct{}{}
			urls = append(urls, u)
		}
		if matched && len(urls) > 0 {
			if err := c.tr.TorrentSet(ctx, trpc.TorrentSetPayload{IDs: []int64{*t.ID}, TrackerList: urls}); err != nil {
				return affected, names, err
			}
			affected++
			name := ""
			if t.Name != nil {
				name = *t.Name
			}
			names = append(names, name)
		}
	}
	return affected, names, nil
}

// RenameFile 重命名种子文件或目录
func (c *Client) RenameFile(ctx context.Context, id int64, path, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.tr.TorrentRenamePath(ctx, id, path, name)
}

// TestPort 测试监听端口是否开放
func (c *Client) TestPort(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.tr.PortTest(ctx)
}

// UpdateBlocklist 更新 Blocklist 规则
func (c *Client) UpdateBlocklist(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return c.tr.BlocklistUpdate(ctx)
}

// GetSession 获取会话配置
func (c *Client) GetSession(ctx context.Context) (*Session, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	sess, err := c.tr.SessionArgumentsGetAll(ctx)
	if err != nil {
		return nil, err
	}
	return mapSession(sess), nil
}

// SetSession 更新会话配置
func (c *Client) SetSession(ctx context.Context, fields trpc.SessionArguments) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.tr.SessionArgumentsSet(ctx, fields)
}

// GetFreeSpace 查询目录可用空间与总容量
func (c *Client) GetFreeSpace(ctx context.Context, path string) (freeSpace, totalSize int64, err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	free, total, err := c.tr.FreeSpace(ctx, path)
	if err != nil {
		return 0, 0, err
	}
	// cunits 以 bit 存储（ImportInByte 为字节*8），需转回字节，与 mapper 的 toBits 保持一致
	return int64(free) / 8, int64(total) / 8, nil
}
