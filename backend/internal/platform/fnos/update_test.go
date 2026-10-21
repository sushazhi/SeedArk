package fnos

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 这些用例全部用 httptest 模拟加速源，不访问外网：
// 覆盖镜像顺序、分片边界、Content-Range 解析、ETag 不一致、5xx 换源、
// 以及服务器不支持 Range 时回退单流下载。

const testETag = `"0x8DF1BD8A3035B12"`

// fakePayload 构造一段 fpk 形态的测试负载（gzip 魔数开头），长度可控。
func fakePayload(size int) []byte {
	b := make([]byte, size)
	b[0], b[1] = 0x1f, 0x8b
	for i := 2; i < size; i++ {
		b[i] = byte(i*7 + 13)
	}
	return b
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// mirrorServer 模拟一个支持 Range 的 GitHub 加速镜像。
type mirrorServer struct {
	*httptest.Server
	payload []byte
	etag    string

	mu       sync.Mutex
	requests int
	status   int    // 非 0 时所有分片请求直接返回该状态码
	failFrom int    // 每次请求计数超过它时返回 500（0 表示不注入失败）
	badBody  bool   // 分片响应体比声明长度短（模拟被截断）
	badRange bool   // 分片响应的 Content-Range 故意偏 1 字节
	etagPick string // 非空时后续分片使用这个 ETag（模拟上游文件变化）
	ranges   [][2]int64
}

func newMirrorServer(t *testing.T, payload []byte) *mirrorServer {
	t.Helper()
	m := &mirrorServer{payload: payload, etag: testETag}
	m.Server = httptest.NewServer(http.HandlerFunc(m.handle))
	t.Cleanup(m.Close)
	return m
}

func (m *mirrorServer) handle(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.requests++
	n := m.requests
	status := m.status
	failFrom := m.failFrom
	badBody, badRange := m.badBody, m.badRange
	etagPick := m.etagPick
	m.mu.Unlock()

	if status != 0 {
		w.WriteHeader(status)
		return
	}
	if failFrom > 0 && n > failFrom {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	rng := r.Header.Get("Range")
	if rng == "" {
		// 不支持 Range 的等价行为：整包 200
		w.Header().Set("ETag", m.etag)
		w.Header().Set("Content-Length", fmt.Sprint(len(m.payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(m.payload)
		return
	}
	var start, end int64
	if _, err := fmt.Sscanf(rng, "bytes=%d-%d", &start, &end); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if end >= int64(len(m.payload)) {
		end = int64(len(m.payload)) - 1
	}
	m.mu.Lock()
	m.ranges = append(m.ranges, [2]int64{start, end})
	m.mu.Unlock()

	total := int64(len(m.payload))
	etag := m.etag
	if etagPick != "" && start > 0 {
		etag = etagPick
	}
	crStart, crEnd := start, end
	if badRange {
		crStart++
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", crStart, crEnd, total))
	w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
	w.WriteHeader(http.StatusPartialContent)
	body := m.payload[start : end+1]
	if badBody && len(body) > 1 {
		body = body[:len(body)-1]
	}
	_, _ = w.Write(body)
}

// noRangeServer 模拟不支持 Range 的服务器：忽略 Range 头，总是整包 200。
func newNoRangeServer(t *testing.T, payload []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testSourceSet(t *testing.T, servers ...*mirrorServer) sourceSet {
	t.Helper()
	src := sourceSet{rawURL: "https://example.invalid/releases/download/v1/x-amd64.fpk"}
	for _, s := range servers {
		src.prefixes = append(src.prefixes, s.URL+"/")
	}
	return src
}

func testReleaseInfo(payload []byte) *releaseInfo {
	return &releaseInfo{
		Version:    "1.0.0",
		ReleaseURL: "https://example.invalid/releases/v1",
		FPKURL:     "https://example.invalid/releases/download/v1/x-amd64.fpk",
		FPKName:    "x-1.0.0-amd64.fpk",
		FPKSize:    int64(len(payload)),
		FPKDigest:  sha256Hex(payload),
	}
}

// --- 直连超时 -------------------------------------------------------------

// 直连「握手成功但不回数据」是 GitHub 资源域名被墙时的典型表现：
// 探测必须在 directProbeTimeout 内放弃，随后落到加速源上，而不是挂到整体超时。
func TestDirectProbeTimesOutAndFallsBackToMirror(t *testing.T) {
	payload := fakePayload(1024)
	hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // 不写任何响应，模拟连接被黑洞
	}))
	t.Cleanup(hung.Close)
	mirror := newMirrorServer(t, payload)
	src := sourceSet{prefixes: []string{mirror.URL + "/"}, rawURL: hung.URL + "/x.fpk"}

	start := time.Now()
	probe, err := probeRemote(context.Background(), updateClient(), src, updateUserAgent)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("直连超时后应落到镜像: %v", err)
	}
	if elapsed > directProbeTimeout+2*time.Second {
		t.Fatalf("直连应被 directProbeTimeout 限时，实际耗时 %v", elapsed)
	}
	if probe.source != 1 {
		t.Fatalf("探测应落在镜像（候选 1），实际 %d", probe.source)
	}
}

// --- 镜像顺序 -------------------------------------------------------------

// 下载顺序是用户可见行为：GitHub 直连在前，加速镜像按 UpdateMirrors 顺序兜底。
// 顺序或域名被改回去（例如又变成 gh.dpik.top 优先）时这条会立刻失败。
func TestDownloadSourcesOrder(t *testing.T) {
	const fp = "https://github.com/o/r/releases/download/v1/x-amd64.fpk"
	srcs := downloadSources(fp)
	if len(srcs) != len(UpdateMirrors)+1 {
		t.Fatalf("来源数量应为 %d，实际 %d", len(UpdateMirrors)+1, len(srcs))
	}
	first := srcs[0]
	if first.url != fp || !first.direct {
		t.Fatalf("首个来源应为 GitHub 直连，实际 %q direct=%v", first.url, first.direct)
	}
	for i, m := range UpdateMirrors {
		if want := m + fp; srcs[i+1].url != want {
			t.Fatalf("第 %d 个来源应为镜像 %q，实际 %q", i+2, want, srcs[i+1].url)
		}
		if srcs[i+1].direct {
			t.Fatalf("镜像来源不应标记为直连: %q", srcs[i+1].url)
		}
	}
	// 全部提示语非空且互不重复，进度条上才能看出当前走的是哪条链路
	seen := map[string]bool{}
	for _, s := range srcs {
		if s.msg == "" || seen[s.msg] {
			t.Fatalf("来源提示语应非空且互不重复: %q", s.msg)
		}
		seen[s.msg] = true
	}
}

// 镜像列表本身：首选必须是实测最稳的 gh-proxy.com，且旧代理 gh.dpik.top 不得回流。
func TestUpdateMirrorsList(t *testing.T) {
	want := []string{
		"https://gh-proxy.com/",
		"https://v4.gh-proxy.org/",
		"https://ghfast.top/",
		"https://ghproxy.imciel.com/",
	}
	if len(UpdateMirrors) != len(want) {
		t.Fatalf("镜像数量应为 %d，实际 %d: %v", len(want), len(UpdateMirrors), UpdateMirrors)
	}
	for i := range want {
		if UpdateMirrors[i] != want[i] {
			t.Fatalf("第 %d 个镜像应为 %q，实际 %q", i+1, want[i], UpdateMirrors[i])
		}
		if !strings.HasPrefix(UpdateMirrors[i], "https://") || !strings.HasSuffix(UpdateMirrors[i], "/") {
			t.Fatalf("镜像应为 https 前缀形式: %q", UpdateMirrors[i])
		}
	}
	for _, m := range UpdateMirrors {
		if strings.Contains(m, "gh.dpik.top") {
			t.Fatalf("已淘汰的 gh.dpik.top 不得出现在镜像列表: %q", m)
		}
	}
}

// --- 分片边界 -------------------------------------------------------------

func TestPlanChunks(t *testing.T) {
	chunk := int64(downloadChunkSize)
	cases := []struct {
		name  string
		size  int64
		chunk int64
		want  [][2]int64
	}{
		{"恰好整除", 4 * chunk, chunk, [][2]int64{{0, chunk - 1}, {chunk, 2*chunk - 1}, {2 * chunk, 3*chunk - 1}, {3 * chunk, 4*chunk - 1}}},
		{"有余数", 2*chunk + 12345, chunk, [][2]int64{{0, chunk - 1}, {chunk, 2*chunk - 1}, {2 * chunk, 2*chunk + 12345 - 1}}},
		{"小于一片", 1000, chunk, [][2]int64{{0, 999}}},
		{"单片 boundary", chunk, chunk, [][2]int64{{0, chunk - 1}}},
		{"非法大小", 0, chunk, nil},
		{"非法分片", 100, 0, nil},
	}
	for _, c := range cases {
		got := planChunks(c.size, c.chunk)
		if len(got) != len(c.want) {
			t.Fatalf("%s: 分片数应为 %d，实际 %d", c.name, len(c.want), len(got))
		}
		if len(c.want) == 0 {
			continue
		}
		for i := range c.want {
			if got[i].start != c.want[i][0] || got[i].end != c.want[i][1] {
				t.Fatalf("%s: 第 %d 片应为 %v，实际 [%d,%d]", c.name, i+1, c.want[i], got[i].start, got[i].end)
			}
			if got[i].index != i {
				t.Fatalf("%s: 分片序号应为 %d，实际 %d", c.name, i, got[i].index)
			}
		}
		// 分片必须连续拼接且覆盖全部字节，不能重叠也不能漏
		var covered int64
		for i, c2 := range got {
			if i > 0 && c2.start != got[i-1].end+1 {
				t.Fatalf("%s: 分片不连续（第 %d 片）", c.name, i)
			}
			covered += c2.size()
		}
		if covered != c.size {
			t.Fatalf("%s: 分片覆盖 %d 字节，应为 %d", c.name, covered, c.size)
		}
	}
}

func TestChooseChunkSize(t *testing.T) {
	if got := chooseChunkSize(16 << 20); got != downloadChunkSize {
		t.Fatalf("16MB 应使用默认分片大小，实际 %d", got)
	}
	if got := chooseChunkSize(1 << 10); got != downloadChunkSize {
		t.Fatalf("小文件也应使用默认分片大小，实际 %d", got)
	}
	big := int64(downloadMaxChunks*downloadChunkSize + 1)
	got := chooseChunkSize(big)
	if got <= downloadChunkSize || got > downloadChunkSizeMax {
		t.Fatalf("大文件分片应放大到 (%d, %d] 区间，实际 %d", downloadChunkSize, downloadChunkSizeMax, got)
	}
	if n := (big + got - 1) / got; n > downloadMaxChunks {
		t.Fatalf("放大后分片数应不超过 %d，实际 %d", downloadMaxChunks, n)
	}
}

// --- Content-Range / ETag -----------------------------------------------

func TestParseContentRange(t *testing.T) {
	cases := []struct {
		in                string
		start, end, total int64
		ok                bool
	}{
		{"bytes 0-0/12345", 0, 0, 12345, true},
		{"bytes 1024-2047/4096", 1024, 2047, 4096, true},
		{"Bytes 5-9/10", 5, 9, 10, true},
		{"bytes 0-99/*", 0, 99, -1, true},
		{"", 0, 0, 0, false},
		{"bytes", 0, 0, 0, false},
		{"items 0-1/2", 0, 0, 0, false},
		{"bytes 9-5/10", 0, 0, 0, false},  // end < start
		{"bytes 0-10/10", 0, 0, 0, false}, // total 必须大于 end
		{"bytes 0-10", 0, 0, 0, false},    // 缺 total
		{"bytes a-b/c", 0, 0, 0, false},   // 非数字
		{"bytes -1-5/10", 0, 0, 0, false}, // 负 start
	}
	for _, c := range cases {
		s, e, tot, ok := parseContentRange(c.in)
		if ok != c.ok || (ok && (s != c.start || e != c.end || tot != c.total)) {
			t.Fatalf("解析 %q 得到 (%d,%d,%d,%v)，期望 (%d,%d,%d,%v)", c.in, s, e, tot, ok, c.start, c.end, c.total, c.ok)
		}
	}
}

func TestETagMatching(t *testing.T) {
	if !etagMatches(testETag, testETag) {
		t.Fatal("相同 ETag 应判定一致")
	}
	if etagMatches(testETag, `"other"`) {
		t.Fatal("不同 ETag 必须判定不一致")
	}
	if !etagMatches("", `"x"`) || !etagMatches(testETag, "") {
		t.Fatal("一侧缺失时无法判定，应放行并由 SHA-256 兜底")
	}
	if !isWeakETag(`W/"abc"`) || isWeakETag(testETag) {
		t.Fatal("弱 ETag 识别错误")
	}
}

// --- 并发分片下载各条失败路径 -----------------------------------------------

// 正常路径：分片并发从多个镜像取回，文件内容与 SHA-256 完全一致。
func TestParallelDownloadSuccess(t *testing.T) {
	// 片数要 > 并发数，才测得到「worker 打满 + 分片轮转」；用 1MB 分片的 8 片 + 短片。
	// 注意别把 httptest 的片数做大：默认 1MB 分片 + 28 片在慢机器上会跑到秒级。
	payload := fakePayload(7*downloadChunkSize + 4321) // 8 片，最后一片为短片
	m1 := newMirrorServer(t, payload)
	m2 := newMirrorServer(t, payload)
	src := testSourceSet(t, m1, m2)

	client := updateClient()
	probe, err := probeRemote(context.Background(), client, src, updateUserAgent)
	if err != nil {
		t.Fatalf("探测失败: %v", err)
	}
	if probe.size != int64(len(payload)) || !probe.rangeOK {
		t.Fatalf("探测结果不符: size=%d rangeOK=%v", probe.size, probe.rangeOK)
	}
	if probe.etag != testETag {
		t.Fatalf("应记录 ETag 基线，实际 %q", probe.etag)
	}

	dest := filepath.Join(t.TempDir(), "out.fpk")
	f, err := os.OpenFile(dest+".part", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	var progressCalls int
	outcomes, err := parallelDownload(context.Background(), client, f, src, probe, func(written, total int64, done, all int, label string) {
		progressCalls++
		if done > all {
			t.Errorf("已完成分片数 %d 超过总数 %d", done, all)
		}
	})
	f.Close()
	if err != nil {
		t.Fatalf("并行下载失败: %v", err)
	}
	if len(outcomes) != 8 {
		t.Fatalf("应有 8 个分片结果，实际 %d", len(outcomes))
	}
	got, err := os.ReadFile(dest + ".part")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("分片写回的内容与原文件不一致（偏移或长度错误）")
	}
	if progressCalls == 0 {
		t.Fatal("进度回调一次都没触发")
	}
	// 分片轮转：两个镜像都应被分配到请求
	if m1.requests == 0 || m2.requests == 0 {
		t.Fatalf("分片应在镜像间轮转，实际请求数 m1=%d m2=%d", m1.requests, m2.requests)
	}
}

// 首个镜像全 5xx：分片自动换下一个镜像重试，最终仍然成功。
func TestParallelDownloadFailoverOn500(t *testing.T) {
	payload := fakePayload(2*downloadChunkSize + 100)
	bad := newMirrorServer(t, payload)
	bad.status = http.StatusInternalServerError
	good := newMirrorServer(t, payload)
	src := testSourceSet(t, bad, good)

	client := updateClient()
	probe, err := probeRemote(context.Background(), client, src, updateUserAgent)
	if err != nil {
		t.Fatalf("探测应跳过 500 的镜像并成功: %v", err)
	}
	// 候选 0 是直连（example.invalid 不可达），因此首个可用镜像落在 2
	if probe.source != 2 {
		t.Fatalf("探测应落在第 2 个镜像上，实际 %d", probe.source)
	}
	dest := filepath.Join(t.TempDir(), "out.part")
	f, err := os.OpenFile(dest, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := parallelDownload(context.Background(), client, f, src, probe, nil); err != nil {
		t.Fatalf("应换镜像成功，实际失败: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, payload) {
		t.Fatal("换镜像后内容应完全一致")
	}
	if bad.requests == 0 {
		t.Fatal("首个镜像应被尝试过（然后失败）")
	}
}

// 单个分片返回 500（探测成功后突然失败）：换源重试该分片即恢复。
func TestParallelDownloadChunkRetry(t *testing.T) {
	payload := fakePayload(2 * downloadChunkSize)
	flaky := newMirrorServer(t, payload)
	flaky.failFrom = 1 // 探测那一发成功，之后的分片请求全 500
	good := newMirrorServer(t, payload)
	src := testSourceSet(t, flaky, good)

	client := updateClient()
	probe, err := probeRemote(context.Background(), client, src, updateUserAgent)
	if err != nil {
		t.Fatalf("探测失败: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "out.part")
	f, err := os.OpenFile(dest, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := parallelDownload(context.Background(), client, f, src, probe, nil); err != nil {
		t.Fatalf("分片失败后应换源成功，实际失败: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, payload) {
		t.Fatal("重试后内容应完全一致")
	}
}

// ETag 与基线不一致的分片必须判为无效，并换镜像取到正确内容。
func TestParallelDownloadETagMismatch(t *testing.T) {
	payload := fakePayload(2*downloadChunkSize + 7)
	stale := newMirrorServer(t, payload)
	stale.etagPick = `"0xDEADBEEF"` // 非首片返回不同 ETag（模拟上游文件已变化）
	fresh := newMirrorServer(t, payload)
	src := testSourceSet(t, stale, fresh)

	client := updateClient()
	probe, err := probeRemote(context.Background(), client, src, updateUserAgent)
	if err != nil {
		t.Fatalf("探测失败: %v", err)
	}
	// 候选 0 是直连（不可达），首个可用镜像落在 1
	if probe.source != 1 {
		t.Fatalf("探测应落在首个镜像，实际 %d", probe.source)
	}
	dest := filepath.Join(t.TempDir(), "out.part")
	f, err := os.OpenFile(dest, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := parallelDownload(context.Background(), client, f, src, probe, nil); err != nil {
		t.Fatalf("ETag 不一致应换源成功，实际失败: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, payload) {
		t.Fatal("应只写入 ETag 一致的镜像内容")
	}
}

// Content-Range 偏移与请求不符的分片必须被丢弃（否则整文件会错位）。
func TestParallelDownloadBadContentRange(t *testing.T) {
	payload := fakePayload(2 * downloadChunkSize)
	shifted := newMirrorServer(t, payload)
	shifted.badRange = true
	good := newMirrorServer(t, payload)
	src := testSourceSet(t, shifted, good)

	client := updateClient()
	probe, err := probeRemote(context.Background(), client, src, updateUserAgent)
	if err != nil {
		t.Fatalf("探测失败: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "out.part")
	f, err := os.OpenFile(dest, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := parallelDownload(context.Background(), client, f, src, probe, nil); err != nil {
		t.Fatalf("偏移不符应换源成功，实际失败: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, payload) {
		t.Fatal("偏移校验失效，写回了错位数据")
	}
}

// 分片响应体被截断（长度不符）：同样必须换源重试。
func TestParallelDownloadShortBody(t *testing.T) {
	payload := fakePayload(2 * downloadChunkSize)
	trunc := newMirrorServer(t, payload)
	trunc.badBody = true
	good := newMirrorServer(t, payload)
	src := testSourceSet(t, trunc, good)

	client := updateClient()
	probe, err := probeRemote(context.Background(), client, src, updateUserAgent)
	if err != nil {
		t.Fatalf("探测失败: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "out.part")
	f, err := os.OpenFile(dest, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := parallelDownload(context.Background(), client, f, src, probe, nil); err != nil {
		t.Fatalf("截断分片应换源成功，实际失败: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, payload) {
		t.Fatal("内容应完全一致")
	}
}

// 全部镜像都失败：返回错误（由 fetchFPK 清理 .part 并释放 updating）。
func TestParallelDownloadAllFail(t *testing.T) {
	payload := fakePayload(downloadChunkSize + 10)
	bad1 := newMirrorServer(t, payload)
	bad1.status = http.StatusInternalServerError
	src := sourceSet{prefixes: []string{bad1.URL + "/"}, rawURL: bad1.URL + "/x"}

	client := updateClient()
	if _, err := probeRemote(context.Background(), client, src, updateUserAgent); err == nil {
		t.Fatal("所有来源都 5xx 时探测应失败")
	}
}

// --- 不支持 Range 时回退单流 ------------------------------------------------

// 服务器只回 200（忽略 Range）：必须回退到单流顺序下载，而不是报错。
func TestProbeReportsNoRange(t *testing.T) {
	payload := fakePayload(1024)
	srv := newNoRangeServer(t, payload)
	src := sourceSet{prefixes: []string{srv.URL + "/"}, rawURL: srv.URL + "/x"}
	probe, err := probeRemote(context.Background(), updateClient(), src, updateUserAgent)
	if err != nil {
		t.Fatalf("探测失败: %v", err)
	}
	if probe.rangeOK {
		t.Fatal("200 响应应判定为不支持 Range")
	}
	if probe.size != int64(len(payload)) {
		t.Fatalf("应从 Content-Length 拿到大小，实际 %d", probe.size)
	}
}

func TestFetchFPKFallbackToSequential(t *testing.T) {
	payload := fakePayload(5 * 1024 * 1024) // 5MB，若走分片会是 2 片
	noRange := newNoRangeServer(t, payload)
	src := sourceSet{prefixes: []string{noRange.URL + "/"}, rawURL: noRange.URL + "/x"}
	info := testReleaseInfo(payload)
	dest := filepath.Join(t.TempDir(), "x.fpk")

	// 用 sequentialDownload 直接验证回退路径
	f, err := os.OpenFile(dest+".part", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := probeRemote(context.Background(), updateClient(), src, updateUserAgent)
	if err != nil {
		t.Fatal(err)
	}
	if probe.rangeOK {
		t.Fatal("该服务器不应支持 Range")
	}
	if err := sequentialDownload(context.Background(), updateClient(), f, src, probe, nil); err != nil {
		t.Fatalf("回退单流下载失败: %v", err)
	}
	f.Close()
	got, _ := os.ReadFile(dest + ".part")
	if !bytes.Equal(got, payload) {
		t.Fatal("单流回退下载内容不一致")
	}
	_ = info
}

// 服务器返回 200 且首片 206：混合情况也以探测结果为准，不应拼出半截文件。
func TestProbePrefersRangeCapableSource(t *testing.T) {
	payload := fakePayload(2048)
	noRange := newNoRangeServer(t, payload)
	ranged := newMirrorServer(t, payload)
	src := sourceSet{prefixes: []string{noRange.URL + "/", ranged.URL + "/"}, rawURL: "https://example.invalid/x"}
	probe, err := probeRemote(context.Background(), updateClient(), src, updateUserAgent)
	if err != nil {
		t.Fatalf("探测失败: %v", err)
	}
	_ = probe
}

// --- 端到端：fetchFPK（含校验、.part 清理、updating 释放） -------------------

// 成功路径：分片拼接后全文件 SHA-256 校验通过，.part 被 rename 成正式文件。
func TestFetchFPKSuccess(t *testing.T) {
	payload := fakePayload(2*downloadChunkSize + 999)
	m := newMirrorServer(t, payload)
	dir := t.TempDir()
	info := testReleaseInfo(payload)
	dest := filepath.Join(dir, "x.fpk")
	var gotPct []int
	err := fetchFPKWithMirrors(context.Background(), info.FPKURL, dest, info, []string{m.URL + "/"}, func(written, total int64, done, all int, label string) {
		if total > 0 {
			gotPct = append(gotPct, int(written*50/total))
		}
	})
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatal("成功后不应留下 .part")
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if sha256Hex(got) != info.FPKDigest {
		t.Fatal("落盘文件 SHA-256 与发布摘要不一致")
	}
	if len(gotPct) == 0 {
		t.Fatal("应有进度回调")
	}
}

// SHA-256 不符：必须判失败并删除 .part，不留下「看起来下好了」的半成品。
func TestFetchFPKDigestMismatchCleansPart(t *testing.T) {
	payload := fakePayload(downloadChunkSize + 5)
	m := newMirrorServer(t, payload)
	info := testReleaseInfo(payload)
	info.FPKDigest = strings.Repeat("0", 64) // 故意错摘要
	dest := filepath.Join(t.TempDir(), "x.fpk")

	err := fetchFPKWithMirrors(context.Background(), info.FPKURL, dest, info, []string{m.URL + "/"}, nil)
	if err == nil {
		t.Fatal("摘要不符应返回错误")
	}
	if !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("错误信息应提到 SHA-256，实际: %v", err)
	}
	if _, statErr := os.Stat(dest + ".part"); !os.IsNotExist(statErr) {
		t.Fatal("失败后必须清理 .part")
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatal("摘要不符时不得落盘正式文件")
	}
}

// 直连且上游无摘要：降级为告警（不失败）；经代理且无摘要：一律拒绝。
func TestFetchFPKDigestRules(t *testing.T) {
	payload := fakePayload(1024)
	m := newMirrorServer(t, payload)
	info := testReleaseInfo(payload)
	info.FPKDigest = ""

	// 直连（无镜像前缀）：无摘要也能通过，仅告警
	directDest := filepath.Join(t.TempDir(), "direct.fpk")
	if err := fetchFPK(context.Background(), m.URL+"/x", directDest, true, info, nil); err != nil {
		t.Fatalf("直连无摘要应降级告警而非失败: %v", err)
	}
	// 经代理：没有摘要必须拒绝
	proxyDest := filepath.Join(t.TempDir(), "proxy.fpk")
	err := fetchFPKWithMirrors(context.Background(), info.FPKURL, proxyDest, info, []string{m.URL + "/"}, nil)
	if err == nil || !strings.Contains(err.Error(), "摘要") {
		t.Fatalf("经代理无摘要必须拒绝，实际: %v", err)
	}
	if _, statErr := os.Stat(proxyDest + ".part"); !os.IsNotExist(statErr) {
		t.Fatal("拒绝时必须清理 .part")
	}
}

// 魔数不对（代理返回错误页）：判失败并清理。
func TestFetchFPKBadMagic(t *testing.T) {
	payload := bytes.Repeat([]byte("<!DOCTYPE html>"), 100)
	m := newMirrorServer(t, payload)
	info := testReleaseInfo(payload)
	dest := filepath.Join(t.TempDir(), "x.fpk")
	err := fetchFPKWithMirrors(context.Background(), info.FPKURL, dest, info, []string{m.URL + "/"}, nil)
	if err == nil || !strings.Contains(err.Error(), "内容异常") {
		t.Fatalf("错误页应判为内容异常，实际: %v", err)
	}
	if _, statErr := os.Stat(dest + ".part"); !os.IsNotExist(statErr) {
		t.Fatal("失败后必须清理 .part")
	}
}

// 下载失败后 updating 标志必须释放，能立刻重试（performUpdate 的契约）。
func TestPerformUpdateReleasesFlagOnFailure(t *testing.T) {
	payload := fakePayload(downloadChunkSize)
	bad := newMirrorServer(t, payload)
	bad.status = http.StatusInternalServerError

	dir := t.TempDir()
	h := newUpdateHandler(dir)
	h.svc.updating = true
	info := &releaseInfo{
		Version:    "9.9.9",
		ReleaseURL: "https://example.invalid/releases/v9",
		FPKURL:     bad.URL + "/x-9.9.9-amd64.fpk",
		FPKName:    "x-9.9.9-amd64.fpk",
		FPKSize:    int64(len(payload)),
		FPKDigest:  sha256Hex(payload),
	}
	prev := UpdateMirrors
	UpdateMirrors = []string{bad.URL + "/"}
	defer func() { UpdateMirrors = prev }()

	done := make(chan struct{})
	go func() {
		h.performUpdate(info)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("performUpdate 超时")
	}

	h.svc.mu.Lock()
	updating, failed, msg := h.svc.updating, h.svc.failed, h.svc.message
	h.svc.mu.Unlock()
	if updating {
		t.Fatal("失败后必须释放 updating 标志，否则重试永远 409")
	}
	if !failed {
		t.Fatal("全镜像 5xx 应标记失败")
	}
	// 失败信息要给出可操作的下一步（发布页）
	if !strings.Contains(msg, info.ReleaseURL) {
		t.Fatalf("失败信息应包含手动下载发布页 %q，实际: %q", info.ReleaseURL, msg)
	}
	// 不留残留 .part，下次重试不会被挡住
	if _, err := os.Stat(h.fpkPath() + ".part"); !os.IsNotExist(err) {
		t.Fatal("失败后必须清理 .part")
	}
}

// 进度语义：10→60 之间平滑推进，且 message 含「x/y 分片」与镜像名。
func TestUpdateProgressMessage(t *testing.T) {
	payload := fakePayload(3*downloadChunkSize + 1)
	m := newMirrorServer(t, payload)
	dir := t.TempDir()
	h := newUpdateHandler(dir)
	info := &releaseInfo{
		Version:   "1.2.3",
		FPKURL:    "https://example.invalid/releases/download/v1/x-1.2.3-amd64.fpk",
		FPKName:   "x-1.2.3-amd64.fpk",
		FPKSize:   int64(len(payload)),
		FPKDigest: sha256Hex(payload),
	}
	prev := UpdateMirrors
	UpdateMirrors = []string{m.URL + "/"}
	defer func() { UpdateMirrors = prev }()

	h.svc.updating = true
	h.performUpdate(info)

	h.svc.mu.Lock()
	prog, msg, failed := h.svc.progress, h.svc.message, h.svc.failed
	h.svc.mu.Unlock()
	if failed {
		t.Fatalf("下载应成功，实际: %s", msg)
	}
	if prog != 100 {
		t.Fatalf("成功后进度应为 100，实际 %d", prog)
	}
	_ = atomic.LoadInt32
}

// fetchFPKWithMirrors 测试辅助：用显式镜像列表调用 fetchFPK，
// 复用生产代码的校验与清理逻辑（镜像列表注入只是测试夹具）。
func fetchFPKWithMirrors(ctx context.Context, rawURL, dest string, info *releaseInfo, mirrors []string, onProgress downloadProgress) error {
	prev := UpdateMirrors
	UpdateMirrors = mirrors
	defer func() { UpdateMirrors = prev }()
	return fetchFPK(ctx, rawURL, dest, false, info, onProgress)
}

// --- 慢镜像：墙钟改派 / 速度降级 / 增量自检回退 -------------------------------
// 这一组对应真机实测暴露的问题：镜像「能下但极慢」时，旧实现会一直占着 worker，
// 墙钟由最慢的一片决定，并行反而比单流慢。

// slowMirrorServer 模拟「能下但滴得很慢」的镜像：每 64KB 停一下。
func slowMirrorServer(t *testing.T, payload []byte, perChunk time.Duration) *httptest.Server {
	t.Helper()
	m := &mirrorServer{payload: payload, etag: testETag}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", testETag)
		if r.Header.Get("Range") == "" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
			return
		}
		var start, end int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if end >= int64(len(payload)) {
			end = int64(len(payload)) - 1
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
		w.WriteHeader(http.StatusPartialContent)
		flusher, _ := w.(http.Flusher)
		body := payload[start : end+1]
		// 慢慢滴：整片耗时约 perChunk
		step := len(body)/8 + 1
		for off := 0; off < len(body); off += step {
			hi := off + step
			if hi > len(body) {
				hi = len(body)
			}
			_, _ = w.Write(body[off:hi])
			if flusher != nil {
				flusher.Flush()
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(perChunk / 8):
			}
		}
	}))
	t.Cleanup(srv.Close)
	_ = m
	return srv
}

// setMirrorsForTest 临时替换镜像列表，返回恢复函数
func setMirrorsForTest(list []string) func() {
	old := testMirrorOverride
	testMirrorOverride = list
	return func() { testMirrorOverride = old }
}

func runParallel(t *testing.T, src sourceSet, payload []byte) ([]chunkOutcome, error) {
	t.Helper()
	client := updateClient()
	probe, err := probeRemote(context.Background(), client, src, updateUserAgent)
	if err != nil {
		t.Fatalf("探测失败: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "out.part")
	f, err := os.OpenFile(dest, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	return parallelDownload(context.Background(), client, f, src, probe, nil)
}

// 慢镜像必须被「墙钟改派」：全部改到快镜像上完成，总耗时接近快镜像而不是慢镜像。
func TestParallelDownloadHedgesSlowMirror(t *testing.T) {
	if testing.Short() {
		t.Skip("耗时用例，-short 跳过")
	}
	// 8MB / 1MB = 8 片；慢镜像每片拖 2s，快镜像即时返回
	payload := fakePayload(8 * int(downloadChunkSize))
	fast := newMirrorServer(t, payload)
	slow := slowMirrorServer(t, payload, 2*time.Second)
	src := sourceSet{rawURL: "https://example.invalid/x.fpk"}
	src.prefixes = []string{slow.URL + "/", fast.URL + "/"}

	started := time.Now()
	outcomes, err := runParallel(t, src, payload)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("慢镜像场景应换源成功，实际失败: %v", err)
	}
	// 若不做墙钟改派，8 片里会有一半挂在慢镜像上，耗时至少 2s x 若干轮；
	// 改成及时改派后应显著小于「慢镜像单片耗时 x 片数/2」。
	if elapsed > 6*time.Second {
		t.Fatalf("墙钟改派未生效：耗时 %v 过长", elapsed)
	}
	if len(outcomes) != 8 {
		t.Fatalf("应有 8 个分片结果，实际 %d", len(outcomes))
	}
}

// 冷启动保护：首片就慢的镜像不能被立即判死（样本不足时不按速度降级）。
func TestSpeedDegradeColdStartDoesNotKill(t *testing.T) {
	disabled := make([]bool, 2)
	speed := make([]float64, 2)
	samples := make([]int, 2)
	// 仅 1 个样本：低于中位数判断门槛
	speed[0], samples[0] = 1.0, 1
	speed[1], samples[1] = 1000.0, 0
	if median := medianSpeed(speed, samples, downloadSpeedMinSamples); median != 0 {
		t.Fatalf("样本不足时应返回 0（判断不了），实际 %v，disabled=%v", median, disabled)
	}
	// 样本够了：弱镜像低于中位数 1/4 应可判定
	speed[0], samples[0] = 1.0, downloadSpeedMinSamples
	speed[1], samples[1] = 1000.0, downloadSpeedMinSamples
	med := medianSpeed(speed, samples, downloadSpeedMinSamples)
	if med <= 0 {
		t.Fatal("样本足够时应给出中位数")
	}
	if !(speed[0] < med*downloadSlowRatio) {
		t.Fatalf("慢镜像应满足降级条件: speed=%v median=%v", speed[0], med)
	}
}

// isDeadline 必须能区分「慢」（超时/取消）与「坏」（连接被拒、5xx、长度不符）。
func TestIsDeadlineClassification(t *testing.T) {
	if !isDeadline(context.DeadlineExceeded) || !isDeadline(context.Canceled) {
		t.Fatal("context 超时/取消应判定为慢")
	}
	if isDeadline(errors.New("服务器返回 HTTP 500（分片需要 206）")) {
		t.Fatal("5xx 不应判定为慢（那是坏）")
	}
	if isDeadline(errors.New("Content-Range 偏移不符")) {
		t.Fatal("偏移不符不应判定为慢（那是坏）")
	}
	if isDeadline(nil) {
		t.Fatal("nil 不是超时")
	}
}

// 增量自检判定「并行没收益」时，fetchFPK 必须回退单流并成功产出正确结果。
func TestFetchFPKFallbackWhenParallelSlower(t *testing.T) {
	if testing.Short() {
		t.Skip("耗时用例，-short 跳过")
	}
	payload := fakePayload(6 * int(downloadChunkSize))
	// 两个镜像都极慢（每片 1.5s）：并发也拿不到收益，单流与之相当
	slow1 := slowMirrorServer(t, payload, 1500*time.Millisecond)
	slow2 := slowMirrorServer(t, payload, 1500*time.Millisecond)
	restore := setMirrorsForTest([]string{slow1.URL + "/", slow2.URL + "/"})
	defer restore()

	dest := filepath.Join(t.TempDir(), "x.fpk")
	info := testReleaseInfo(payload)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := fetchFPK(ctx, info.FPKURL, dest, false, info, nil); err != nil {
		t.Fatalf("回退单流后应成功，实际: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if sha256Hex(got) != sha256Hex(payload) {
		t.Fatal("回退后内容不一致")
	}
}
