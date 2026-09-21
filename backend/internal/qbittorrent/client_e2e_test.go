package qbittorrent

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sushazhi/seedark/backend/internal/driver"
	"github.com/sushazhi/seedark/backend/internal/models"
	"github.com/sushazhi/seedark/backend/internal/qbmock"
)

// startMock 启动 qbmock 并返回其 URL（随测试结束自动关闭）
func startMock(t *testing.T, opts qbmock.Options) string {
	t.Helper()
	ts := httptest.NewServer(qbmock.New(opts))
	t.Cleanup(ts.Close)
	return ts.URL
}

// findByHash 在列表中按 hash 查找种子
func findByHash(t *testing.T, list []*models.Torrent, hash string) *models.Torrent {
	t.Helper()
	for _, tr := range list {
		if tr.HashString == hash {
			return tr
		}
	}
	t.Fatalf("列表中找不到 hash=%s 的种子（共 %d 个）", hash, len(list))
	return nil
}

// findById 在列表中按面板 ID 查找种子
func findById(t *testing.T, list []*models.Torrent, id int64) *models.Torrent {
	t.Helper()
	for _, tr := range list {
		if tr.ID == id {
			return tr
		}
	}
	t.Fatalf("列表中找不到 id=%d 的种子（共 %d 个）", id, len(list))
	return nil
}

// refresh 拉取最新列表（同时建立 ID→hash 映射）
func refresh(t *testing.T, c *Client) []*models.Torrent {
	t.Helper()
	list, err := c.GetTorrentsFresh(context.Background())
	if err != nil {
		t.Fatalf("GetTorrentsFresh: %v", err)
	}
	return list
}

// ---- 5.x（qBittorrent 5.2.3 / WebAPI v2.15.1）链路 ----

// cookie 登录 + 版本握手
func TestE2ECookieLoginAndPing(t *testing.T) {
	url := startMock(t, qbmock.Options{User: "admin", Pass: "secret", Seed: 3})
	c, err := New(url, "admin", "secret")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	ver, err := c.Ping(ctx)
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if ver != "5.2.3" { // Ping 会去掉 v 前缀
		t.Errorf("Ping 版本 = %q, 期望 5.2.3", ver)
	}
	wv, err := c.WebAPIVersion(ctx)
	if err != nil {
		t.Fatalf("WebAPIVersion: %v", err)
	}
	if wv != "2.15.1" {
		t.Errorf("WebAPIVersion = %q, 期望 2.15.1", wv)
	}
	if c.Kind() != driver.KindQBittorrent {
		t.Errorf("Kind = %v", c.Kind())
	}

	list := refresh(t, c)
	if len(list) != 3 {
		t.Fatalf("种子数 = %d, 期望 3", len(list))
	}
	for _, tr := range list {
		// mock 按 sampleNames 布置真实感种子名，这里只要求非空
		if tr.Name == "" {
			t.Error("种子名为空")
		}
		if tr.ID <= 0 {
			t.Errorf("本地 ID 未分配: %d", tr.ID)
		}
		// mock 按 stateCycle 轮转覆盖全部状态，验证映射结果落在合法枚举内
		if tr.Status < 0 || tr.Status > trStatusSeeding {
			t.Errorf("初始状态 = %d, 超出 0..%d 的状态枚举", tr.Status, trStatusSeeding)
		}
	}
}

// API Key 直连（Bearer，无需登录 cookie）
func TestE2EAPIKey(t *testing.T) {
	// 显式配置 API Key（否则 mock 进入免认证模式，测不到 Bearer 链路）
	url := startMock(t, qbmock.Options{Seed: 2, APIKey: "qbt_" + strings.Repeat("b", 28)})
	c, err := New(url, "", "qbt_"+strings.Repeat("b", 28))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	if _, err := c.Ping(ctx); err != nil {
		t.Fatalf("API Key Ping: %v", err)
	}
	list := refresh(t, c)
	if len(list) != 2 {
		t.Fatalf("种子数 = %d, 期望 2", len(list))
	}
}

// 磁力添加：5.2+ 的 torrents/add 带 Accept 头返回 added_torrent_ids JSON
func TestE2EAddTorrentByURL(t *testing.T) {
	url := startMock(t, qbmock.Options{Seed: 1})
	c, err := New(url, "", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	hash := strings.Repeat("ab", 20) // 40 位 hex
	magnet := "magnet:?xt=urn:btih:" + hash + "&dn=e2e-url-torrent"
	id, err := c.AddTorrentByURL(ctx, magnet, "/data/e2e", false, nil, nil)
	if err != nil {
		t.Fatalf("AddTorrentByURL: %v", err)
	}
	if id <= 0 {
		t.Fatalf("添加返回非法 ID: %d", id)
	}

	list := refresh(t, c)
	tr := findByHash(t, list, hash)
	if tr.Name != "e2e-url-torrent" {
		t.Errorf("新种子名 = %q, 期望 e2e-url-torrent（取自 dn 参数）", tr.Name)
	}
	if tr.DownloadDir != "/data/e2e" {
		t.Errorf("保存路径 = %q, 期望 /data/e2e", tr.DownloadDir)
	}
	if tr.ID != id {
		t.Errorf("列表 ID = %d, 添加返回 %d", tr.ID, id)
	}
}

// .torrent 文件添加：驱动带 Accept 头拿 added_torrent_ids
func TestE2EAddTorrentByFile(t *testing.T) {
	url := startMock(t, qbmock.Options{Seed: 1})
	c, err := New(url, "", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	data := []byte("e2e fake torrent payload v1")
	sum := sha1.Sum(data) // qbmock 以内容 sha1 模拟 infohash
	wantHash := hex.EncodeToString(sum[:])

	id, err := c.AddTorrentByFile(ctx, data, "", false, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("AddTorrentByFile: %v", err)
	}
	if id <= 0 {
		t.Fatalf("添加返回非法 ID: %d", id)
	}

	list := refresh(t, c)
	tr := findByHash(t, list, wantHash)
	if tr.ID != id {
		t.Errorf("列表 ID = %d, 添加返回 %d", tr.ID, id)
	}
}

// start/stop 使用 5.0+ 的 torrents/start 与 torrents/stop 端点
func TestE2EStartStopTorrents(t *testing.T) {
	url := startMock(t, qbmock.Options{Seed: 2})
	c, err := New(url, "", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	id := refresh(t, c)[0].ID

	if err := c.StopTorrents(ctx, []int64{id}); err != nil {
		t.Fatalf("StopTorrents: %v", err)
	}
	if tr := findById(t, refresh(t, c), id); tr.Status != trStatusStopped {
		t.Errorf("stop 后状态 = %d, 期望 %d (stopped)", tr.Status, trStatusStopped)
	}

	if err := c.StartTorrents(ctx, []int64{id}); err != nil {
		t.Fatalf("StartTorrents: %v", err)
	}
	if tr := findById(t, refresh(t, c), id); tr.Status != trStatusDownload {
		t.Errorf("start 后状态 = %d, 期望 %d (downloading)", tr.Status, trStatusDownload)
	}
}

// 单种限速：KB/s → bytes/s（×1024），读取时再除回来
func TestE2ESetTorrentLimits(t *testing.T) {
	url := startMock(t, qbmock.Options{Seed: 1})
	c, _ := New(url, "", "")
	ctx := context.Background()

	id := refresh(t, c)[0].ID

	limited, dl, ul := true, int64(1000), int64(2000)
	if err := c.SetTorrent(ctx, []int64{id}, driver.TorrentPatch{
		DownloadLimited: &limited, DownloadLimit: &dl,
		UploadLimited: &limited, UploadLimit: &ul,
	}); err != nil {
		t.Fatalf("SetTorrent: %v", err)
	}

	detail, err := c.GetTorrentDetail(ctx, id)
	if err != nil {
		t.Fatalf("GetTorrentDetail: %v", err)
	}
	if detail.DownloadLimit != 1000 {
		t.Errorf("下载限速 = %d KB/s, 期望 1000", detail.DownloadLimit)
	}
	if detail.UploadLimit != 2000 {
		t.Errorf("上传限速 = %d KB/s, 期望 2000", detail.UploadLimit)
	}
	if !detail.DownloadLimited || !detail.UploadLimited {
		t.Errorf("限速开关未生效: dl=%v ul=%v", detail.DownloadLimited, detail.UploadLimited)
	}
}

// 标签（5.1+ setTags）、顺序下载开关、分类映射（Groups→category）
func TestE2ESetTorrentFlagsAndLabels(t *testing.T) {
	url := startMock(t, qbmock.Options{Seed: 1})
	c, _ := New(url, "", "")
	ctx := context.Background()

	id := refresh(t, c)[0].ID

	if err := c.SetTorrent(ctx, []int64{id}, driver.TorrentPatch{
		Labels: []string{"e2e-tag", "another"},
	}); err != nil {
		t.Fatalf("SetTorrent Labels: %v", err)
	}
	seq := true
	if err := c.SetTorrentFlags(ctx, []int64{id}, driver.TorrentFlagPatch{
		SequentialDownload: &seq,
		Groups:             []string{"movies"},
	}); err != nil {
		t.Fatalf("SetTorrentFlags: %v", err)
	}

	tr := findById(t, refresh(t, c), id)
	for _, want := range []string{"e2e-tag", "another", "movies"} {
		found := false
		for _, l := range tr.Labels {
			if l == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Labels = %v, 缺少 %q", tr.Labels, want)
		}
	}
	if !tr.SequentialDownload {
		t.Errorf("顺序下载开关未生效")
	}
}

// 会话中途失效：401/403 重试必须重发完整请求体。
// raw 若复用已消费的 io.Reader，重登后的第二次请求会变成空体——
// 表现为「认证恢复了但参数全丢」，且不报错（服务端只当参数缺失）。
func TestE2ERetryKeepsRequestBody(t *testing.T) {
	url := startMock(t, qbmock.Options{User: "admin", Pass: "secret", Seed: 1})
	c, err := New(url, "admin", "secret")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	id := refresh(t, c)[0].ID

	// 以正确凭据再登录一次：服务端换发 SID，驱动器手里的旧 cookie 随即失效，
	// 下一次写请求必然先吃 403、走重登重试路径
	resp, err := http.Post(url+"/api/v2/auth/login", "application/x-www-form-urlencoded",
		strings.NewReader("username=admin&password=secret"))
	if err != nil {
		t.Fatalf("重新登录: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	limited, dl := true, int64(1000)
	if err := c.SetTorrent(ctx, []int64{id}, driver.TorrentPatch{
		DownloadLimited: &limited, DownloadLimit: &dl,
	}); err != nil {
		t.Fatalf("重登后 SetTorrent: %v", err)
	}
	detail, err := c.GetTorrentDetail(ctx, id)
	if err != nil {
		t.Fatalf("GetTorrentDetail: %v", err)
	}
	if detail.DownloadLimit != 1000 || !detail.DownloadLimited {
		t.Errorf("重登重试丢掉了请求体：下载限速 = %d (limited=%v)", detail.DownloadLimit, detail.DownloadLimited)
	}
}

// 顺序下载必须按目标值收敛，而不是「按当前状态取反」。
// Web API 只有 toggle 接口：无视参数的实现会把「设为开启」变成「关掉已开启的」。
func TestE2ESequentialDownloadHonorsValue(t *testing.T) {
	url := startMock(t, qbmock.Options{Seed: 8})
	c, _ := New(url, "", "")
	ctx := context.Background()
	list := refresh(t, c)

	on, off := true, false
	// mock 初始值按 i%7==3 播种：挑一颗开着、一颗关着的，两个方向都验
	var wasOn, wasOff = list[3].ID, list[0].ID
	if !list[3].SequentialDownload {
		t.Fatalf("测试前提不成立：list[3] 应为已开启顺序下载")
	}

	// 已开启的再置 true：必须保持 true（旧实现会切成 false）
	if err := c.SetTorrentFlags(ctx, []int64{wasOn}, driver.TorrentFlagPatch{SequentialDownload: &on}); err != nil {
		t.Fatalf("置 true: %v", err)
	}
	if tr := findById(t, refresh(t, c), wasOn); !tr.SequentialDownload {
		t.Error("对已开启的种子再置 true 后被切成 false（toggle 语义未收敛）")
	}
	// 未开启的置 true
	if err := c.SetTorrentFlags(ctx, []int64{wasOff}, driver.TorrentFlagPatch{SequentialDownload: &on}); err != nil {
		t.Fatalf("置 true: %v", err)
	}
	if tr := findById(t, refresh(t, c), wasOff); !tr.SequentialDownload {
		t.Error("置 true 未生效")
	}
	// 置 false 必须真正关闭
	if err := c.SetTorrentFlags(ctx, []int64{wasOff}, driver.TorrentFlagPatch{SequentialDownload: &off}); err != nil {
		t.Fatalf("置 false: %v", err)
	}
	if tr := findById(t, refresh(t, c), wasOff); tr.SequentialDownload {
		t.Error("置 false 未生效")
	}
}

// 删除种子后，其站点记录必须从缓存中剪掉：站点分组不应残留已删除的种子
func TestE2ETorrentSitesPruneDeleted(t *testing.T) {
	url := startMock(t, qbmock.Options{Seed: 4})
	c, _ := New(url, "", "")
	ctx := context.Background()
	list := refresh(t, c)

	before, err := c.GetTorrentSites(ctx)
	if err != nil {
		t.Fatalf("GetTorrentSites: %v", err)
	}
	victim := list[1]
	if _, ok := before[victim.ID]; !ok {
		t.Fatalf("测试前提不成立：种子 %d 没有站点记录", victim.ID)
	}

	if err := c.RemoveTorrents(ctx, []int64{victim.ID}, false); err != nil {
		t.Fatalf("RemoveTorrents: %v", err)
	}
	after, err := c.GetTorrentSites(ctx)
	if err != nil {
		t.Fatalf("删除后 GetTorrentSites: %v", err)
	}
	if _, ok := after[victim.ID]; ok {
		t.Error("已删除种子的站点记录仍残留在站点映射中")
	}
}

// 队列操作与删除
func TestE2EQueueAndRemove(t *testing.T) {
	url := startMock(t, qbmock.Options{Seed: 3})
	c, _ := New(url, "", "")
	ctx := context.Background()

	list := refresh(t, c)
	id, victim := list[0].ID, list[1].ID

	if err := c.QueueMove(ctx, []int64{id}, "top"); err != nil {
		t.Fatalf("QueueMove top: %v", err)
	}
	if err := c.QueueMove(ctx, []int64{id}, "down"); err != nil {
		t.Fatalf("QueueMove down: %v", err)
	}
	if err := c.RemoveTorrents(ctx, []int64{victim}, false); err != nil {
		t.Fatalf("RemoveTorrents: %v", err)
	}
	list = refresh(t, c)
	if len(list) != 2 {
		t.Errorf("删除后种子数 = %d, 期望 2", len(list))
	}
}

// 会话读取/写入与统计
func TestE2ESessionAndStats(t *testing.T) {
	url := startMock(t, qbmock.Options{Seed: 2})
	c, _ := New(url, "", "")
	ctx := context.Background()

	sess, err := c.GetSession(ctx)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.DownloadDir != "/downloads" {
		t.Errorf("下载目录 = %q", sess.DownloadDir)
	}

	down, downOn := int64(5000), true
	if err := c.SetSession(ctx, driver.SessionPatch{
		SpeedLimitDown: &down, SpeedLimitDownOn: &downOn,
	}); err != nil {
		t.Fatalf("SetSession: %v", err)
	}
	sess, err = c.GetSession(ctx)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.SpeedLimitDown != 5000 {
		t.Errorf("全局下载限速 = %d KB/s, 期望 5000", sess.SpeedLimitDown)
	}
	if !sess.SpeedLimitDownOn {
		t.Errorf("全局下载限速开关未生效")
	}

	stats, err := c.GetSessionStats(ctx)
	if err != nil {
		t.Fatalf("GetSessionStats: %v", err)
	}
	if stats.TorrentCount != 2 {
		t.Errorf("TorrentCount = %d, 期望 2", stats.TorrentCount)
	}
	if stats.Cumulative.DownloadedBytes != 200*1024*1024*1024 {
		t.Errorf("累计下载 = %d, 期望 200GiB", stats.Cumulative.DownloadedBytes)
	}
}

// 空间查询与端口状态（connection_status=connected 近似）
func TestE2EFreeSpaceAndPort(t *testing.T) {
	url := startMock(t, qbmock.Options{Seed: 1})
	c, _ := New(url, "", "")
	ctx := context.Background()

	free, total, err := c.GetFreeSpace(ctx, "")
	if err != nil {
		t.Fatalf("GetFreeSpace: %v", err)
	}
	if free != 500*1024*1024*1024 {
		t.Errorf("剩余空间 = %d", free)
	}
	if total != 0 {
		t.Errorf("Web API 不提供总容量, 期望 0, 得到 %d", total)
	}

	ok, err := c.TestPort(ctx)
	if err != nil {
		t.Fatalf("TestPort: %v", err)
	}
	if !ok {
		t.Errorf("connection_status=connected 时 TestPort 应为 true")
	}
}

// 无能力接口返回 ErrUnsupported（上层转 501）
func TestE2EUnsupported(t *testing.T) {
	url := startMock(t, qbmock.Options{Seed: 1})
	c, _ := New(url, "", "")
	ctx := context.Background()

	if _, err := c.UpdateBlocklist(ctx); err != driver.ErrUnsupported {
		t.Errorf("UpdateBlocklist = %v, 期望 ErrUnsupported", err)
	}
	if _, err := c.GetSessionGroups(ctx); err != driver.ErrUnsupported {
		t.Errorf("GetSessionGroups = %v, 期望 ErrUnsupported", err)
	}
	if err := c.SetSessionGroup(ctx, "g", nil); err != driver.ErrUnsupported {
		t.Errorf("SetSessionGroup = %v, 期望 ErrUnsupported", err)
	}
}

// ---- 4.x 兼容回退（qbmock -compat 4.x）----

func TestE2ECompat4xFallbacks(t *testing.T) {
	url := startMock(t, qbmock.Options{Compat: "4.x", Seed: 2})
	c, _ := New(url, "", "")
	ctx := context.Background()

	ver, err := c.Ping(ctx)
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if ver != "4.6.7" {
		t.Errorf("版本 = %q, 期望 4.6.7", ver)
	}

	id := refresh(t, c)[0].ID

	// stop → torrents/stop 404 → 回退 torrents/pause
	if err := c.StopTorrents(ctx, []int64{id}); err != nil {
		t.Fatalf("StopTorrents(4.x 回退): %v", err)
	}
	if tr := findById(t, refresh(t, c), id); tr.Status != trStatusStopped {
		t.Errorf("stop 后状态 = %d, 期望 stopped", tr.Status)
	}

	// start → torrents/start 404 → 回退 torrents/resume
	if err := c.StartTorrents(ctx, []int64{id}); err != nil {
		t.Fatalf("StartTorrents(4.x 回退): %v", err)
	}
	if tr := findById(t, refresh(t, c), id); tr.Status != trStatusDownload {
		t.Errorf("start 后状态 = %d, 期望 downloading", tr.Status)
	}

	// 标签 → torrents/setTags 404 → 回退 addTags/removeTags
	if err := c.SetTorrent(ctx, []int64{id}, driver.TorrentPatch{
		Labels: []string{"legacy", "tags"},
	}); err != nil {
		t.Fatalf("SetTorrent Labels(4.x 回退): %v", err)
	}
	tr := findById(t, refresh(t, c), id)
	for _, want := range []string{"legacy", "tags"} {
		found := false
		for _, l := range tr.Labels {
			if l == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("4.x 回退后 Labels = %v, 缺少 %q", tr.Labels, want)
		}
	}

	// 磁力添加：4.x 回 "Ok." 纯文本 → 从磁力 btih 回退定位新种子
	hash := strings.Repeat("cd", 20)
	magnet := "magnet:?xt=urn:btih:" + hash + "&dn=e2e-4x"
	added, err := c.AddTorrentByURL(ctx, magnet, "", false, nil, nil)
	if err != nil {
		t.Fatalf("AddTorrentByURL(4.x 回退): %v", err)
	}
	if added <= 0 {
		t.Fatalf("4.x 磁力添加返回非法 ID: %d", added)
	}
	if got := findByHash(t, refresh(t, c), hash); got.ID != added {
		t.Errorf("4.x 添加后列表 ID = %d, 返回 %d", got.ID, added)
	}
}
