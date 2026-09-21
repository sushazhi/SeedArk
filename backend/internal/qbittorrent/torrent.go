package qbittorrent

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sushazhi/seedark/backend/internal/driver"
	"github.com/sushazhi/seedark/backend/internal/models"
)

// torrentInfo /api/v2/torrents/info 的单条记录（Web API v2，qBittorrent 5.x）
type torrentInfo struct {
	AddedOn                int64   `json:"added_on"`
	AmountLeft             int64   `json:"amount_left"`
	AutoTMM                bool    `json:"auto_tmm"`
	Availability           float64 `json:"availability"`
	Category               string  `json:"category"`
	Comment                string  `json:"comment"`
	Completed              int64   `json:"completed"`
	CompletionOn           int64   `json:"completion_on"`
	ContentPath            string  `json:"content_path"`
	CreatedBy              string  `json:"created_by"`
	DlLimit                int64   `json:"dl_limit"`
	DlSpeed                int64   `json:"dlspeed"`
	DownloadPath           string  `json:"download_path"`
	Downloaded             int64   `json:"downloaded"`
	DownloadedSession      int64   `json:"downloaded_session"`
	ETA                    int64   `json:"eta"`
	FirstLastPiecePrio     bool    `json:"f_l_piece_prio"`
	ForceStart             bool    `json:"force_start"`
	Hash                   string  `json:"hash"`
	HasMetadata            bool    `json:"has_metadata"`
	InfohashV1             string  `json:"infohash_v1"`
	InfohashV2             string  `json:"infohash_v2"`
	LastActivity           int64   `json:"last_activity"`
	MagnetURI              string  `json:"magnet_uri"`
	MaxInactiveSeedingTime int64   `json:"max_inactive_seeding_time"`
	MaxRatio               float64 `json:"max_ratio"`
	MaxSeedingTime         int64   `json:"max_seeding_time"`
	Name                   string  `json:"name"`
	NumComplete            int64   `json:"num_complete"`
	NumIncomplete          int64   `json:"num_incomplete"`
	NumLeechs              int64   `json:"num_leechs"`
	NumSeeds               int64   `json:"num_seeds"`
	Private                bool    `json:"private"`
	Priority               int64   `json:"priority"`
	Progress               float64 `json:"progress"`
	Ratio                  float64 `json:"ratio"`
	RatioLimit             float64 `json:"ratio_limit"`
	Reannounce             int64   `json:"reannounce"`
	SavePath               string  `json:"save_path"`
	SeedingTime            int64   `json:"seeding_time"`
	SeedingTimeLimit       int64   `json:"seeding_time_limit"`
	SeenComplete           int64   `json:"seen_complete"`
	SequentialDownload     bool    `json:"seq_dl"`
	Size                   int64   `json:"size"`
	State                  string  `json:"state"`
	SuperSeeding           bool    `json:"super_seeding"`
	Tags                   string  `json:"tags"`
	TimeActive             int64   `json:"time_active"`
	TotalSize              int64   `json:"total_size"`
	Tracker                string  `json:"tracker"`
	TrackersCount          int64   `json:"trackers_count"`
	UpLimit                int64   `json:"up_limit"`
	Uploaded               int64   `json:"uploaded"`
	UploadedSession        int64   `json:"uploaded_session"`
	UpSpeed                int64   `json:"upspeed"`
}

// torrentProperties /api/v2/torrents/properties
type torrentProperties struct {
	AdditionDate       int64   `json:"addition_date"`
	Comment            string  `json:"comment"`
	CompletionDate     int64   `json:"completion_date"`
	CreatedBy          string  `json:"created_by"`
	CreationDate       int64   `json:"creation_date"`
	DlLimit            int64   `json:"dl_limit"`
	DlSpeed            int64   `json:"dl_speed"`
	DownloadPath       string  `json:"download_path"`
	IsPrivate          bool    `json:"is_private"`
	LastSeen           int64   `json:"last_seen"`
	Name               string  `json:"name"`
	NbConnections      int64   `json:"nb_connections"`
	NbConnectionsLimit int64   `json:"nb_connections_limit"`
	Peers              int64   `json:"peers"`
	PeersTotal         int64   `json:"peers_total"`
	PieceSize          int64   `json:"piece_size"`
	PiecesHave         int64   `json:"pieces_have"`
	PiecesNum          int64   `json:"pieces_num"`
	Reannounce         int64   `json:"reannounce"`
	SavePath           string  `json:"save_path"`
	SeedingTime        int64   `json:"seeding_time"`
	Seeds              int64   `json:"seeds"`
	SeedsTotal         int64   `json:"seeds_total"`
	ShareRatio         float64 `json:"share_ratio"`
	TimeElapsed        int64   `json:"time_elapsed"`
	TotalDownloaded    int64   `json:"total_downloaded"`
	TotalSize          int64   `json:"total_size"`
	TotalUploaded      int64   `json:"total_uploaded"`
	TotalWasted        int64   `json:"total_wasted"`
	UpLimit            int64   `json:"up_limit"`
	UpSpeed            int64   `json:"up_speed"`
}

// torrentFile /api/v2/torrents/files
type torrentFile struct {
	Availability float64 `json:"availability"`
	Index        int64   `json:"index"`
	IsSeed       bool    `json:"is_seed"`
	Name         string  `json:"name"`
	PieceRange   []int64 `json:"piece_range"`
	Priority     int64   `json:"priority"`
	Progress     float64 `json:"progress"`
	Size         int64   `json:"size"`
}

// torrentTracker /api/v2/torrents/trackers
type torrentTracker struct {
	URL           string `json:"url"`
	Status        int64  `json:"status"`
	Tier          int64  `json:"tier"`
	NumPeers      int64  `json:"num_peers"`
	NumSeeds      int64  `json:"num_seeds"`
	NumLeechers   int64  `json:"num_leeches"`
	NumDownloaded int64  `json:"num_downloaded"`
	Message       string `json:"msg"`
}

// torrentPeer /api/v2/torrents/peers
type torrentPeer struct {
	IP          string  `json:"ip"`
	Port        int64   `json:"port"`
	Client      string  `json:"client"`
	Connection  string  `json:"connection"`
	Country     string  `json:"country"`
	CountryCode string  `json:"country_code"`
	Flags       string  `json:"flags"`
	FlagsDesc   string  `json:"flags_desc"`
	Progress    float64 `json:"progress"`
	DlSpeed     int64   `json:"dl_speed"`
	UpSpeed     int64   `json:"up_speed"`
	Downloaded  int64   `json:"downloaded"`
	Uploaded    int64   `json:"uploaded"`
	Relevance   float64 `json:"relevance"`
}

// ---- 列表 / 详情 ----

// GetTorrents 获取种子列表（带短 TTL 缓存，避免每次轮询都打满 qBittorrent）
func (c *Client) GetTorrents(ctx context.Context) ([]*models.Torrent, error) {
	return c.getTorrents(ctx, false)
}

// GetTorrentsFresh 强制拉取最新列表
func (c *Client) GetTorrentsFresh(ctx context.Context) ([]*models.Torrent, error) {
	return c.getTorrents(ctx, true)
}

// listCacheTTL 列表缓存有效期。与 Transmission 侧保持同一量级，
// 让 WebSocket 轮询与 REST 兜底共享一份结果。
const listCacheTTL = 6 * time.Second

func (c *Client) getTorrents(ctx context.Context, force bool) ([]*models.Torrent, error) {
	c.listMu.Lock()
	if !force && c.listCache != nil && time.Since(c.listCached) < listCacheTTL {
		out := c.listCache
		c.listMu.Unlock()
		return out, nil
	}
	c.listMu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var raw []torrentInfo
	if err := c.get(ctx, "torrents/info", nil, &raw); err != nil {
		return nil, err
	}
	out := c.mapTorrents(raw)
	c.listMu.Lock()
	c.listCache = out
	c.listCached = time.Now()
	c.listMu.Unlock()
	return out, nil
}

// GetTorrentDetail 获取单个种子详情（文件 / Peers / Tracker / 块位图）
func (c *Client) GetTorrentDetail(ctx context.Context, id int64) (*models.Torrent, error) {
	hash := c.HashFor(id)
	if hash == "" {
		return nil, fmt.Errorf("种子 %d 不存在", id)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	var raw []torrentInfo
	params := url.Values{"hashes": {hash}}
	if err := c.get(ctx, "torrents/info", params, &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("种子 %d 不存在", id)
	}
	t := c.mapTorrent(raw[0])

	var props torrentProperties
	if err := c.get(ctx, "torrents/properties", url.Values{"hash": {hash}}, &props); err == nil {
		applyProperties(t, props)
	} else {
		slog.Debug("qBittorrent 读取种子属性失败", "err", err)
	}

	var files []torrentFile
	if err := c.get(ctx, "torrents/files", url.Values{"hash": {hash}}, &files); err == nil {
		applyFiles(t, files)
	}

	var trackers []torrentTracker
	if err := c.get(ctx, "torrents/trackers", url.Values{"hash": {hash}}, &trackers); err == nil {
		applyTrackers(t, trackers)
	}

	var peerResp struct {
		Peers map[string]torrentPeer `json:"peers"`
	}
	if err := c.get(ctx, "torrents/peers", url.Values{"hash": {hash}}, &peerResp); err == nil {
		applyPeers(t, peerResp.Peers)
	}

	// 块位图：pieceStates 返回每块状态（0 未下载 / 1 下载中 / 2 已下载），
	// 编码成与 Transmission pieces 一致的 base64 位图供前端渲染。
	var states []int
	if err := c.get(ctx, "torrents/pieceStates", url.Values{"hash": {hash}}, &states); err == nil {
		t.Pieces = encodePieces(states)
		t.PieceCount = int64(len(states))
		if props.PieceSize > 0 {
			t.PieceSize = props.PieceSize
		}
	}
	return t, nil
}

// ---- 站点（Tracker 维度） ----

// GetTorrentSites 每个种子关联的 Tracker 站点。
// qBittorrent 的 /torrents/info 不返回 tracker 列表，必须逐种子查 /torrents/trackers；
// 并发拉取并缓存，避免站点分组每次都打几百个请求。
func (c *Client) GetTorrentSites(ctx context.Context) (map[int64][]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	hashes, err := c.allHashes(ctx)
	if err != nil {
		return nil, err
	}
	trackers, err := c.trackerMap(ctx, hashes)
	if err != nil {
		return nil, err
	}
	out := make(map[int64][]string, len(trackers))
	for hash, urls := range trackers {
		sites := siteNames(urls)
		if len(sites) > 0 {
			out[c.IDFor(hash)] = sites
		}
	}
	return out, nil
}

// allHashes 当前全部种子的 hash
func (c *Client) allHashes(ctx context.Context) ([]string, error) {
	var raw []torrentInfo
	if err := c.get(ctx, "torrents/info", nil, &raw); err != nil {
		return nil, err
	}
	hashes := make([]string, 0, len(raw))
	for _, t := range raw {
		if t.Hash != "" {
			hashes = append(hashes, t.Hash)
		}
	}
	c.syncIDs(hashes)
	return hashes, nil
}

// trackerMap 并发拉取多个种子的 tracker 列表（带 60s 缓存）
func (c *Client) trackerMap(ctx context.Context, hashes []string) (map[string][]string, error) {
	c.trackerMu.RLock()
	if len(c.trackers) > 0 && time.Since(c.trackerTime) < 60*time.Second {
		cached := c.trackers
		c.trackerMu.RUnlock()
		return cached, nil
	}
	c.trackerMu.RUnlock()

	const concurrency = 8
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	// 缓存已存在的不重复请求：站点维度会被反复调用
	c.trackerMu.RLock()
	pending := make([]string, 0, len(hashes))
	for _, h := range hashes {
		if _, ok := c.trackers[h]; !ok {
			pending = append(pending, h)
		}
	}
	c.trackerMu.RUnlock()

	for _, h := range pending {
		wg.Add(1)
		sem <- struct{}{}
		go func(hash string) {
			defer wg.Done()
			defer func() { <-sem }()
			var list []torrentTracker
			err := c.get(ctx, "torrents/trackers", url.Values{"hash": {hash}}, &list)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				return
			}
			urls := make([]string, 0, len(list))
			for _, tr := range list {
				if tr.URL != "" {
					urls = append(urls, tr.URL)
				}
			}
			c.trackerMu.Lock()
			c.trackers[hash] = urls
			c.trackerMu.Unlock()
		}(h)
	}
	wg.Wait()

	c.trackerMu.Lock()
	// 按当前清单剪枝：已被删除的种子的站点记录必须一并清掉，
	// 否则界面上的站点分组会一直挂着早就删掉的种子。
	// （hashes 由调用方传入完整清单，见 GetTorrentSites / ReplaceTracker）
	current := make(map[string]struct{}, len(hashes))
	for _, h := range hashes {
		current[h] = struct{}{}
	}
	for k := range c.trackers {
		if _, ok := current[k]; !ok {
			delete(c.trackers, k)
		}
	}
	c.trackerTime = time.Now()
	out := make(map[string][]string, len(c.trackers))
	for k, v := range c.trackers {
		out[k] = v
	}
	c.trackerMu.Unlock()
	return out, nil
}

// ---- 添加 ----

// AddTorrentByFile 以 .torrent 文件内容添加
func (c *Client) AddTorrentByFile(ctx context.Context, data []byte, downloadDir string, paused bool, labels []string, priority *int64, filesWanted, filesUnwanted []int64) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	fields := map[string]string{
		"paused":  boolStr(paused),
		"stopped": boolStr(paused), // Web API 2.11+ 新增 stopped，与 paused 同时下发兼容新旧版本
	}
	if downloadDir != "" {
		fields["savepath"] = downloadDir
		fields["autoTMM"] = "false"
	}
	if len(labels) > 0 {
		// 分类在面板里以标签形式呈现，这里只下发真正的 tag
		fields["tags"] = strings.Join(labels, ",")
	}
	resp, err := c.postMultipart(ctx, "torrents/add", fields, map[string][]byte{"torrents": data})
	if err != nil {
		return 0, err
	}
	// Web API 2.15+（qBittorrent 5.2）返回 JSON 带 added_torrent_ids；
	// 旧版本只回 "Ok."，此时用文件内容自行算出 infohash 定位刚添加的种子
	if hash := addedHash(resp); hash != "" {
		c.syncIDs([]string{hash})
		return c.IDFor(hash), nil
	}
	if hash := infoHashFromTorrent(data); hash != "" {
		c.syncIDs([]string{hash})
		return c.IDFor(hash), nil
	}
	return 0, fmt.Errorf("添加成功但未返回种子标识")
}

// AddTorrentByURL 以 http(s) 链接 / 磁力链接添加
func (c *Client) AddTorrentByURL(ctx context.Context, link, downloadDir string, paused bool, labels []string, priority *int64) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	form := url.Values{
		"urls":    {link},
		"paused":  {boolStr(paused)},
		"stopped": {boolStr(paused)},
	}
	if downloadDir != "" {
		form.Set("savepath", downloadDir)
		form.Set("autoTMM", "false")
	}
	if len(labels) > 0 {
		form.Set("tags", strings.Join(labels, ","))
	}
	resp, err := c.raw(ctx, "POST", c.base+"/api/v2/torrents/add", formReader(form),
		"application/x-www-form-urlencoded", true)
	if err != nil {
		return 0, err
	}
	if hash := addedHash(resp); hash != "" {
		c.syncIDs([]string{hash})
		return c.IDFor(hash), nil
	}
	if hash := hashFromMagnet(link); hash != "" {
		c.syncIDs([]string{hash})
		return c.IDFor(hash), nil
	}
	return 0, fmt.Errorf("添加成功但未返回种子标识（旧版本 qBittorrent 不支持回传 hash，请刷新列表）")
}

// torrentAddReport Web API 2.15（qBittorrent 5.2）的 torrents/add JSON 响应：
// 成功 / 等待元数据 / 失败的计数与 added_torrent_ids（hash 列表）一同返回；
// 4.x 只回 "Ok." 文本，走不到这条解析。
type torrentAddReport struct {
	AddedIDs   []string `json:"added_torrent_ids"`
	SuccessCnt int      `json:"success_count"`
	PendingCnt int      `json:"pending_count"`
	FailureCnt int      `json:"failure_count"`
}

// addedHash 解析 5.2+ 的添加响应，返回首个 hash
func addedHash(resp []byte) string {
	r, _ := parseAddReport(resp)
	if r != nil && len(r.AddedIDs) > 0 {
		return strings.ToLower(strings.TrimSpace(r.AddedIDs[0]))
	}
	return ""
}

// parseAddReport 解析添加结果；非 JSON（旧版本 "Ok."）返回 (nil, nil)
func parseAddReport(resp []byte) (*torrentAddReport, error) {
	if len(resp) == 0 || !looksJSON(resp) {
		return nil, nil
	}
	var out torrentAddReport
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, nil
	}
	if out.FailureCnt > 0 && len(out.AddedIDs) == 0 {
		return &out, fmt.Errorf("qBittorrent 报告添加失败（success=%d pending=%d failure=%d）",
			out.SuccessCnt, out.PendingCnt, out.FailureCnt)
	}
	return &out, nil
}

// hashFromMagnet 从磁力链接取 btih（v1/v2）
func hashFromMagnet(link string) string {
	m := regexp.MustCompile(`(?i)xt=urn:btih:([0-9a-f]{40}|[0-9a-f]{64})`).FindStringSubmatch(link)
	if len(m) < 2 {
		return ""
	}
	return strings.ToLower(m[1])
}

// ---- 基本操作 ----
// 端点版本差异统一走 compat.go 的回退链：新版本端点在前，旧版本在后，
// 未来端点再改名时只需把新名字插入链头。

// StartTorrents 开始种子。Web API 2.11+（qBittorrent 5.0）用 torrents/start，
// 4.x 没有该端点（404）时回退到经典的 torrents/resume。
func (c *Client) StartTorrents(ctx context.Context, ids []int64) error {
	hashes, err := c.requireHashes(ids)
	if err != nil {
		return err
	}
	return c.postFallback(ctx, url.Values{"hashes": {hashes}},
		"torrents/start", "torrents/resume")
}

// StartTorrentsNow 强制开始（忽略队列限制）
func (c *Client) StartTorrentsNow(ctx context.Context, ids []int64) error {
	hashes, err := c.requireHashes(ids)
	if err != nil {
		return err
	}
	return c.post(ctx, "torrents/setForceStart", url.Values{"hashes": {hashes}, "value": {"true"}})
}

// StopTorrents 暂停种子。5.0 改名为 torrents/stop（pause 标记废弃），
// 4.x 只有 torrents/pause：404 时回退，同一套面板同时管理新旧版本。
func (c *Client) StopTorrents(ctx context.Context, ids []int64) error {
	hashes, err := c.requireHashes(ids)
	if err != nil {
		return err
	}
	return c.postFallback(ctx, url.Values{"hashes": {hashes}},
		"torrents/stop", "torrents/pause")
}

// VerifyTorrents 重新校验本地数据
func (c *Client) VerifyTorrents(ctx context.Context, ids []int64) error {
	hashes, err := c.requireHashes(ids)
	if err != nil {
		return err
	}
	return c.post(ctx, "torrents/recheck", url.Values{"hashes": {hashes}})
}

// ReannounceTorrents 重新宣告 Tracker
func (c *Client) ReannounceTorrents(ctx context.Context, ids []int64) error {
	hashes, err := c.requireHashes(ids)
	if err != nil {
		return err
	}
	return c.post(ctx, "torrents/reannounce", url.Values{"hashes": {hashes}})
}

// RemoveTorrents 删除种子（deleteData 同时删除本地文件）
func (c *Client) RemoveTorrents(ctx context.Context, ids []int64, deleteData bool) error {
	hashes, err := c.requireHashes(ids)
	if err != nil {
		return err
	}
	c.invalidate()
	return c.post(ctx, "torrents/delete", url.Values{
		"hashes":      {hashes},
		"deleteFiles": {boolStr(deleteData)},
	})
}

// invalidate 写操作后作废列表与 tracker 缓存，保证下次拉取是最新状态
// invalidate 缓存失效：列表缓存整体作废，站点缓存标记过期。
// 站点缓存不作整体清空——下次取站点时 trackerMap 会按最新清单剪枝并补齐，
// 既清掉已删除种子的残留，又不必为一次属性修改重拉全部 Tracker
func (c *Client) invalidate() {
	c.listMu.Lock()
	c.listCache = nil
	c.listMu.Unlock()
	c.trackerMu.Lock()
	c.trackerTime = time.Time{}
	c.trackerMu.Unlock()
}

// ---- 属性修改 ----

// SetTorrent 修改种子属性（限速单位 KB/s → qBittorrent bytes/s）
func (c *Client) SetTorrent(ctx context.Context, ids []int64, patch driver.TorrentPatch) error {
	if len(ids) == 0 {
		return fmt.Errorf("缺少种子 ID")
	}
	hashes, err := c.requireHashes(ids)
	if err != nil {
		return err
	}
	if patch.Labels != nil {
		// 标签整体覆盖：5.1+ 有 setTags 一步到位，旧版本退化为先清后加
		tags := c.stripCategories(ids[0], patch.Labels)
		if err := c.setTags(ctx, hashes, strings.Join(tags, ",")); err != nil {
			return err
		}
	}
	if patch.DownloadLimited != nil || patch.DownloadLimit != nil {
		limit := resolveLimit(patch.DownloadLimit, patch.DownloadLimited)
		if err := c.post(ctx, "torrents/setDownloadLimit", url.Values{
			"hashes": {hashes}, "limit": {strconv.FormatInt(limit, 10)}}); err != nil {
			return err
		}
	}
	if patch.UploadLimited != nil || patch.UploadLimit != nil {
		limit := resolveLimit(patch.UploadLimit, patch.UploadLimited)
		if err := c.post(ctx, "torrents/setUploadLimit", url.Values{
			"hashes": {hashes}, "limit": {strconv.FormatInt(limit, 10)}}); err != nil {
			return err
		}
	}
	// 分享率 / 做种时长：qBittorrent 用 -1 表示「无限制」，-2 表示「跟随全局」。
	// 面板的 seedRatioMode：0 跟随全局 / 1 单种覆盖 / 2 不限。
	//
	// 注意 case 1 与 case 2 不能都落到 -1：面板的「不限」是 -1、但「单种覆盖」
	// 必须用用户填的具体数值（下面 SeedRatioLimit 分支会覆盖它），若这里先把
	// case 1 也写成 -1，一旦前端没带 limit 就会静默变成「不限」——用户以为设了
	// 目标，实际永不停止做种。
	if patch.SeedRatioLimit != nil || patch.SeedRatioMode != nil {
		ratio := -2.0
		seeding := int64(-2)
		if patch.SeedRatioMode != nil && *patch.SeedRatioMode == 2 {
			ratio = -1
			seeding = -1
		}
		if patch.SeedRatioLimit != nil && *patch.SeedRatioLimit > 0 {
			ratio = *patch.SeedRatioLimit
			// 单种覆盖：时长仍跟随全局（QB 没有单种时长独立入口时保持 -2）
			if patch.SeedRatioMode != nil && *patch.SeedRatioMode == 2 {
				seeding = -1
			}
		}
		if err := c.post(ctx, "torrents/setShareLimits", url.Values{
			"hashes":           {hashes},
			"ratioLimit":       {strconv.FormatFloat(ratio, 'f', 2, 64)},
			"seedingTimeLimit": {strconv.FormatInt(seeding, 10)},
		}); err != nil {
			return err
		}
	}
	// 空闲做种上限：qBittorrent 的 setShareLimits 只认整体设置，没有只改一个
	// 字段的接口，所以必须把当前值一并带回去。
	//
	// 早前这里硬编码 ratioLimit=-1 / seedingTimeLimit=-1，会把手动设置分享率
	// 的那一步刚写进去的值直接覆盖成「不限」——用户在同一个面板里同时设了
	// 分享率和做种空闲上限，分享率就静默失效了。改为：先读回当前值，只替换
	// inactiveSeedingTimeLimit。
	if patch.SeedIdleLimitMin != nil || patch.SeedIdleMode != nil {
		idle := int64(-2)
		if patch.SeedIdleLimitMin != nil && *patch.SeedIdleLimitMin > 0 {
			idle = *patch.SeedIdleLimitMin
		} else if patch.SeedIdleMode != nil && *patch.SeedIdleMode == 2 {
			idle = -1
		}
		curRatio, curSeeding, ok := c.currentShareLimits(ctx, hashes)
		if !ok {
			// 读不回当前值时不能拿 -1 顶上：那等于把用户的分享率改成「不限」。
			// 宁可明确失败，也不静默破坏用户已有的做种目标。
			return fmt.Errorf("qBittorrent 读取分享率设置失败，无法在保留现有分享率的前提下设置空闲做种上限")
		}
		if err := c.post(ctx, "torrents/setShareLimits", url.Values{
			"hashes":                   {hashes},
			"ratioLimit":               {strconv.FormatFloat(curRatio, 'f', 2, 64)},
			"seedingTimeLimit":         {strconv.FormatInt(curSeeding, 10)},
			"inactiveSeedingTimeLimit": {strconv.FormatInt(idle, 10)},
		}); err != nil {
			// 旧版 qBittorrent（< 4.3）没有 inactiveSeedingTimeLimit 参数，
			// 这里明确报错而不是静默跳过：用户以为设上了、实际没生效，
			// 种子的做种时长会无限增长。
			return fmt.Errorf("qBittorrent 设置空闲做种上限失败（需 4.3+）: %w", err)
		}
	}
	if patch.QueuePosition != nil {
		// qBittorrent 没有「直接设置队列位置」的 API，只能相对移动，
		// 这里退化为置顶以保持与 Transmission 语义最接近的行为
		if err := c.post(ctx, "torrents/topPrio", url.Values{"hashes": {hashes}}); err != nil {
			return err
		}
	}
	if len(patch.FilesWanted) > 0 || len(patch.FilesUnwanted) > 0 {
		hash := c.HashFor(ids[0])
		if hash != "" {
			if len(patch.FilesWanted) > 0 {
				if err := c.setFilePriority(ctx, hash, patch.FilesWanted, 1); err != nil {
					return err
				}
			}
			if len(patch.FilesUnwanted) > 0 {
				if err := c.setFilePriority(ctx, hash, patch.FilesUnwanted, 0); err != nil {
					return err
				}
			}
		}
	}
	if len(patch.PriorityHigh) > 0 {
		hash := c.HashFor(ids[0])
		if hash != "" {
			if err := c.setFilePriority(ctx, hash, patch.PriorityHigh, 7); err != nil {
				return err
			}
		}
	}
	if len(patch.PriorityNormal) > 0 {
		hash := c.HashFor(ids[0])
		if hash != "" {
			if err := c.setFilePriority(ctx, hash, patch.PriorityNormal, 1); err != nil {
				return err
			}
		}
	}
	if len(patch.PriorityLow) > 0 {
		hash := c.HashFor(ids[0])
		if hash != "" {
			if err := c.setFilePriority(ctx, hash, patch.PriorityLow, 2); err != nil {
				return err
			}
		}
	}
	if len(patch.TrackerList) > 0 {
		// 整体覆盖 tracker 列表：先删旧的，再按 tier 顺序追加
		hash := c.HashFor(ids[0])
		if hash != "" {
			if err := c.replaceTrackerList(ctx, hash, patch.TrackerList); err != nil {
				return err
			}
		}
	}
	// 以下字段 qBittorrent 无对应能力：单种连接数、带宽优先级、跟随全局限速
	// 忽略而非报错，避免面板每次改属性都弹失败
	if patch.BandwidthPriority != nil || patch.PeerLimit != nil || patch.HonorsSessionLimits != nil {
		slog.Debug("qBittorrent 忽略不支持的种子字段",
			"bandwidthPriority", patch.BandwidthPriority != nil,
			"peerLimit", patch.PeerLimit != nil)
	}
	c.invalidate()
	return nil
}

// resolveLimit 计算要下发的限速（bytes/s）：关闭限速或未给值时传 0
func resolveLimit(limit *int64, enabled *bool) int64 {
	if enabled != nil && !*enabled {
		return 0
	}
	if limit == nil {
		return 0
	}
	if *limit <= 0 {
		return 0
	}
	return *limit * 1024
}

// setTags 整体覆盖标签（优先 5.1+ 的 setTags，旧版本回退 addTags/removeTags）
func (c *Client) setTags(ctx context.Context, hashes, tags string) error {
	// postKnown 带 404 记忆：旧版本上后续写标签不再重复浪费一次 404 往返
	if err := c.postKnown(ctx, "torrents/setTags", url.Values{"hashes": {hashes}, "tags": {tags}}); err == nil {
		return nil
	} else if !errors.Is(err, errEndpointMissing) {
		return err
	}
	// 旧版本没有 setTags：先算出差集，再增删
	want := splitTags(tags)
	for _, h := range strings.Split(hashes, "|") {
		if h == "" {
			continue
		}
		var list []torrentInfo
		if err := c.get(ctx, "torrents/info", url.Values{"hashes": {h}}, &list); err != nil || len(list) == 0 {
			continue
		}
		have := splitTags(list[0].Tags)
		var add, remove []string
		for _, t := range want {
			if !contains(have, t) {
				add = append(add, t)
			}
		}
		for _, t := range have {
			if !contains(want, t) {
				remove = append(remove, t)
			}
		}
		if len(remove) > 0 {
			_ = c.post(ctx, "torrents/removeTags", url.Values{"hashes": {h}, "tags": {strings.Join(remove, ",")}})
		}
		if len(add) > 0 {
			_ = c.post(ctx, "torrents/addTags", url.Values{"hashes": {h}, "tags": {strings.Join(add, ",")}})
		}
	}
	return nil
}

// stripCategories 从待写入的标签里剔除「其实是分类」的项
func (c *Client) stripCategories(id int64, labels []string) []string {
	hash := c.HashFor(id)
	cat := ""
	if hash != "" {
		cat = c.CategoryOf(hash)
	}
	if cat == "" {
		return labels
	}
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		if l == cat {
			continue
		}
		out = append(out, l)
	}
	return out
}

// setFilePriority 设置文件优先级（0 不下载 / 1 普通 / 2 低 / 7 高）
func (c *Client) setFilePriority(ctx context.Context, hash string, ids []int64, priority int64) error {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return c.post(ctx, "torrents/filePrio", url.Values{
		"hash":     {hash},
		"id":       {strings.Join(parts, "|")},
		"priority": {strconv.FormatInt(priority, 10)},
	})
}

// replaceTrackerList 用给定列表整体替换种子 Tracker
func (c *Client) replaceTrackerList(ctx context.Context, hash string, urls []string) error {
	var list []torrentTracker
	if err := c.get(ctx, "torrents/trackers", url.Values{"hash": {hash}}, &list); err == nil {
		old := make([]string, 0, len(list))
		for _, tr := range list {
			if tr.URL != "" {
				old = append(old, tr.URL)
			}
		}
		if len(old) > 0 {
			_ = c.post(ctx, "torrents/removeTrackers", url.Values{"hash": {hash}, "urls": {strings.Join(old, "|")}})
		}
	}
	if len(urls) == 0 {
		return nil
	}
	return c.post(ctx, "torrents/addTrackers", url.Values{"hash": {hash}, "urls": {strings.Join(urls, "\n")}})
}

// SetTorrentFlags 顺序下载 / 分类（面板的「分组」在 qBittorrent 里对应分类，单值）
func (c *Client) SetTorrentFlags(ctx context.Context, ids []int64, flags driver.TorrentFlagPatch) error {
	if flags.Empty() {
		return nil
	}
	hashes, err := c.requireHashes(ids)
	if err != nil {
		return err
	}
	if flags.SequentialDownload != nil {
		if err := c.setSequentialDownload(ctx, hashes, *flags.SequentialDownload); err != nil {
			return err
		}
	}
	if flags.Groups != nil {
		category := ""
		if len(flags.Groups) > 0 {
			category = flags.Groups[0]
		}
		if category != "" {
			if err := c.post(ctx, "torrents/createCategory", url.Values{"category": {category}}); err != nil {
				slog.Debug("qBittorrent 创建分类失败（可能已存在）", "err", err)
			}
		}
		if err := c.post(ctx, "torrents/setCategory", url.Values{"hashes": {hashes}, "category": {category}}); err != nil {
			return err
		}
	}
	c.invalidate()
	return nil
}

// setSequentialDownload 把顺序下载设置为目标值。
// Web API 只有 toggle 接口（无参数版 set），直接调用等于「按当前状态取反」：
// 设 true 会把本已开启的种子关掉。先读当前状态、只对不一致的种子切换。
func (c *Client) setSequentialDownload(ctx context.Context, hashes string, want bool) error {
	var list []struct {
		Hash       string `json:"hash"`
		Sequential bool   `json:"seq_dl"`
	}
	if err := c.get(ctx, "torrents/info", url.Values{"hashes": {hashes}}, &list); err != nil {
		return err
	}
	diff := make([]string, 0, len(list))
	for _, t := range list {
		if t.Sequential != want {
			diff = append(diff, t.Hash)
		}
	}
	if len(diff) == 0 {
		return nil
	}
	return c.post(ctx, "torrents/toggleSequentialDownload", url.Values{"hashes": {strings.Join(diff, "|")}})
}

// SetTorrentLocation 迁移存储位置（qBittorrent 总是实际搬移文件）
func (c *Client) SetTorrentLocation(ctx context.Context, id int64, location string, move bool) error {
	hash := c.HashFor(id)
	if hash == "" {
		return fmt.Errorf("种子 %d 不存在", id)
	}
	if !move {
		// qBittorrent 没有「仅改指向」的能力，明确告知而不是静默搬移
		return fmt.Errorf("qBittorrent 不支持仅修改指向，请勾选「同时移动文件」")
	}
	c.invalidate()
	return c.post(ctx, "torrents/setLocation", url.Values{"hashes": {hash}, "location": {location}})
}

// QueueMove 队列排序（需 qBittorrent 开启队列管理）
func (c *Client) QueueMove(ctx context.Context, ids []int64, direction string) error {
	hashes, err := c.requireHashes(ids)
	if err != nil {
		return err
	}
	endpoint := ""
	switch direction {
	case "top":
		endpoint = "torrents/topPrio"
	case "bottom":
		endpoint = "torrents/bottomPrio"
	case "up":
		endpoint = "torrents/increasePrio"
	case "down":
		endpoint = "torrents/decreasePrio"
	default:
		return fmt.Errorf("无效的队列方向: %s", direction)
	}
	if err := c.post(ctx, endpoint, url.Values{"hashes": {hashes}}); err != nil {
		return fmt.Errorf("队列调整失败（qBittorrent 需开启「队列管理」）: %w", err)
	}
	return nil
}

// currentShareLimits 读回种子的当前分享率 / 做种时长限制。
// qBittorrent 的 setShareLimits 是整体覆盖，改一个字段必须把其余字段原值带回，
// 否则会把用户已有的设置抹掉。ok=false 表示没读到，调用方不能拿默认值顶上。
func (c *Client) currentShareLimits(ctx context.Context, hashes string) (ratio float64, seeding int64, ok bool) {
	var raw []torrentInfo
	if err := c.get(ctx, "torrents/info", url.Values{"hashes": {hashes}}, &raw); err != nil || len(raw) == 0 {
		return 0, 0, false
	}
	return raw[0].RatioLimit, raw[0].MaxSeedingTime, true
}

// RenameFile 重命名种子内文件 / 目录。
//
// 与 Transmission 的差异：Transmission 的 TorrentRenamePath 支持把 path 传成
// 种子自己的名字来重命名顶层目录，qBittorrent 的 renameFile / renameFolder 只认
// 种子内的相对路径，传种子名必然 404。界面上的「重命名」传的正是种子名，所以
// 这里要把顶层重命名翻译成：枚举该种子的顶层条目，逐个改名。
func (c *Client) RenameFile(ctx context.Context, id int64, path, name string) error {
	hash := c.HashFor(id)
	if hash == "" {
		return fmt.Errorf("种子 %d 不存在", id)
	}
	if name == "" {
		return fmt.Errorf("缺少新的名称")
	}
	// 顶层重命名：path 为空或等于种子名（Transmission 语义）时，改为逐个顶层条目改名
	if path == "" || c.torrentName(ctx, hash) == path {
		return c.renameTopLevel(ctx, hash, path, name)
	}
	err := c.post(ctx, "torrents/renameFile", url.Values{
		"hash": {hash}, "oldPath": {path}, "newPath": {name}})
	if err != nil {
		if err2 := c.post(ctx, "torrents/renameFolder", url.Values{
			"hash": {hash}, "oldPath": {path}, "newPath": {name}}); err2 == nil {
			c.invalidate()
			return nil
		}
		return err
	}
	c.invalidate()
	return nil
}

// torrentName 读取种子的显示名称（顶层重命名需要它来判断 path 是不是种子名）
func (c *Client) torrentName(ctx context.Context, hash string) string {
	var raw []torrentInfo
	if err := c.get(ctx, "torrents/info", url.Values{"hashes": {hash}}, &raw); err != nil || len(raw) == 0 {
		return ""
	}
	return raw[0].Name
}

// renameTopLevel 重命名种子的顶层条目。
//
// 单文件种子：文件名就是顶层，直接改文件名。
// 多文件种子：所有文件共享同一个顶层目录，改目录名即可（QB 会连带移动文件）。
// 混合 / 无目录前缀的种子：退化为逐个顶层前缀改名。
func (c *Client) renameTopLevel(ctx context.Context, hash, oldName, newName string) error {
	var files []torrentFile
	if err := c.get(ctx, "torrents/files", url.Values{"hash": {hash}}, &files); err != nil {
		return fmt.Errorf("读取种子文件列表失败: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("种子内没有文件，无法重命名")
	}
	plan, err := planTopLevelRename(files, oldName, newName)
	if err != nil {
		return err
	}
	var firstErr error
	for _, step := range plan {
		endpoint := "torrents/renameFile"
		if step.Folder {
			endpoint = "torrents/renameFolder"
		}
		if err := c.post(ctx, endpoint, url.Values{
			"hash": {hash}, "oldPath": {step.Old}, "newPath": {step.New}}); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return fmt.Errorf("重命名失败: %w", firstErr)
	}
	c.invalidate()
	return nil
}

// renameStep 一次改名动作：Old → New，Folder 决定走 renameFolder 还是 renameFile
type renameStep struct {
	Old    string
	New    string
	Folder bool
}

// planTopLevelRename 把「重命名种子顶层」翻译成具体的改名步骤。
//
// 与 Transmission 的差异：Transmission 的 TorrentRenamePath 接受种子自己的名字
// 作为 path 来重命名顶层，qBittorrent 只认种子内的相对路径。界面「重命名」传进来
// 的正是种子名，所以必须在这里翻译，否则请求必然 404。
//
// 规则：
//   - 多文件且只有一个顶层目录（名字等于 oldName）→ 只改这一层目录
//   - 单文件种子 → 改那一个文件名
//   - 多个顶层条目 → 只改名字等于 oldName 的那一个，其余不动（避免把无关条目也改名）
func planTopLevelRename(files []torrentFile, oldName, newName string) ([]renameStep, error) {
	prefixes := make([]string, 0, 4)
	seen := make(map[string]bool, 4)
	hasBareTopLevel := false
	for _, f := range files {
		if f.Name == "" {
			continue
		}
		top := f.Name
		if i := strings.IndexByte(f.Name, '/'); i >= 0 {
			top = f.Name[:i]
		} else {
			hasBareTopLevel = true
		}
		if top == "" || seen[top] {
			continue
		}
		seen[top] = true
		prefixes = append(prefixes, top)
	}
	if len(prefixes) == 0 {
		return nil, fmt.Errorf("未能解析种子顶层名称")
	}

	// 多文件且只有一个顶层目录、名字正好是旧名：只改这一层目录名
	if !hasBareTopLevel && len(prefixes) == 1 && prefixes[0] == oldName {
		return []renameStep{{Old: prefixes[0], New: newName, Folder: true}}, nil
	}

	steps := make([]renameStep, 0, len(prefixes))
	if len(prefixes) > 1 {
		// 多个顶层条目：只改名字等于 oldName 的那一个
		for _, top := range prefixes {
			if top == oldName {
				steps = append(steps, renameStep{Old: top, New: newName, Folder: !hasBareTopLevel})
			}
		}
		if len(steps) == 0 {
			return nil, fmt.Errorf("种子内未找到「%s」（可能已被重命名或属于另一形态）", oldName)
		}
		return steps, nil
	}

	// 单个顶层条目：就是整个种子的名字
	return []renameStep{{Old: prefixes[0], New: newName, Folder: !hasBareTopLevel}}, nil
}

// ReplaceTracker 批量替换 / 追加 Tracker
func (c *Client) ReplaceTracker(ctx context.Context, from, to string, appendMode bool) (int64, []string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	hashes, err := c.allHashes(ctx)
	if err != nil {
		return 0, nil, err
	}
	trackers, err := c.trackerMap(ctx, hashes)
	if err != nil {
		return 0, nil, err
	}
	re, reErr := regexp.Compile(from)
	var affected int64
	var names []string
	for _, hash := range hashes {
		urls := trackers[hash]
		if len(urls) == 0 {
			continue
		}
		matched := false
		var next []string
		seen := map[string]bool{}
		for _, u := range urls {
			hit := false
			if reErr != nil {
				hit = strings.Contains(u, from)
			} else {
				hit = re.MatchString(u)
			}
			if hit {
				matched = true
				if !appendMode {
					if seen[to] {
						continue
					}
					seen[to] = true
					next = append(next, to)
					continue
				}
			}
			if seen[u] {
				continue
			}
			seen[u] = true
			next = append(next, u)
		}
		if appendMode && matched && !seen[to] {
			next = append(next, to)
		}
		if !matched {
			continue
		}
		if err := c.replaceTrackerList(ctx, hash, next); err != nil {
			return affected, names, err
		}
		affected++
		var list []torrentInfo
		if err := c.get(ctx, "torrents/info", url.Values{"hashes": {hash}}, &list); err == nil && len(list) > 0 {
			names = append(names, list[0].Name)
		}
	}
	c.trackerMu.Lock()
	c.trackerTime = time.Time{}
	c.trackerMu.Unlock()
	c.invalidate()
	return affected, names, nil
}

// ---- 工具 ----

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func splitTags(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// infoHashFromTorrent 从 .torrent 内容计算 v1 infohash。
// 旧版 qBittorrent 的 /torrents/add 不回传 hash，靠它定位刚添加的种子。
func infoHashFromTorrent(data []byte) string {
	// info 字典在 .torrent 中一定以 "4:infod" 开头（键名 + 字典起始符），
	// 加上 'd' 可以避免命中文件列表里恰好出现的同名字符串
	idx := strings.Index(string(data), "4:infod")
	if idx < 0 {
		return ""
	}
	raw := data[idx+len("4:info"):]
	end := bencodeDictEnd(raw)
	if end < 0 {
		return ""
	}
	sum := sha1.Sum(raw[:end])
	return fmt.Sprintf("%x", sum)
}

// bencodeDictEnd 找到与起始 'd' 配对的 'e'（只处理嵌套字典与列表）
func bencodeDictEnd(raw []byte) int {
	if len(raw) == 0 || raw[0] != 'd' {
		return -1
	}
	depth := 0
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case 'd', 'l':
			depth++
		case 'e':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}
