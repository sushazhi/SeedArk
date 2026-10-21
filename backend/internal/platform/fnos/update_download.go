package fnos

// 自更新下载器：Range 分片并发 + 多加速镜像轮转，Range 不可用时回退单流顺序下载。
//
// 为什么不是「串行试镜像、谁快用谁」：
// 实测（2026-10-21，16MB fpk）单个 GitHub 代理节点的**单条连接**被限流到约
// 0.3–0.5 MB/s，但同节点开多条连接、或多个节点并发时整体能到 2–7 MB/s。
// 也就是说瓶颈是单连接而不是节点总带宽，正确做法是把文件切片、多条连接并行。
// 候选镜像都支持 Range（206 + Content-Range）且对同一资源给出相同的强 ETag，
// 所以切片之间可以用 ETag 做一致性基线，最终仍以整文件 SHA-256 兜底。

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// downloadChunkSize 分片大小：1MB。
	//
	// 为什么不是 4MB：真机实测 16MB 包切 4 片时只有 4 个 worker 在跑（片数 = 并发数），
	// 而且每片绑死一个镜像，墙钟由最慢的那片决定（实测最慢片 64s，整体比单流 27s 还慢）。
	// 1MB 切 16 片后 worker 才真正打满，慢片/慢镜像也只拖走 1/16 的进度。
	downloadChunkSize = 1 << 20
	// downloadChunkSizeMax 自适应上界：文件很大时把分片放大到 4MB，控制分片数
	downloadChunkSizeMax = 4 << 20
	// downloadWorkers 并发连接数（4–6）。镜像侧对单 IP 有连接数限制，
	// 6 条实测能拿到打满、又不至于被节点判为刷量。
	downloadWorkers = 6
	// downloadHedgeFactor 分片墙钟放宽系数：一片的正常成本 ≈ 单片字节 / 全局估计速度，
	// 超过「该成本 × 系数」仍未完成就取消并改派到别的镜像。
	// 首次改派放宽到 8（给瞬时抖动留面子），二次收紧到 4（已经派过一次还慢，就是真的慢）。
	downloadHedgeFirst = 8.0
	downloadHedgeNext  = 4.0
	// downloadHedgeFloor 墙钟下限：不管算出来多小，至少等这么久再换源，
	// 避免小分片在高延迟链路上被反复打断、每次都白下。
	downloadHedgeFloor = 4 * time.Second
	// downloadSlowRatio 镜像 EWMA 速度低于全局速度的 1/4 即降级
	downloadSlowRatio = 0.25
	// downloadSpeedAlpha EWMA 平滑系数（越小越平滑）
	downloadSpeedAlpha = 0.4
	// downloadSpeedMinSamples 速度降级的冷启动门槛：每个镜像至少成功下过这么多片
	// 才参与「慢于中位数」判断，否则首个慢响应就能把可用镜像全判死
	downloadSpeedMinSamples = 2
	// downloadFallbackAt 增量自检：已完成这么多比例后还继续看；
	downloadFallbackAt = 0.25
	// downloadFallbackFactor 增量自检：平均单连接速度低于基线 × 该系数即判并行无收益
	// （略高于 1 是给「基线本身是历史实测值」留的误差）
	downloadFallbackFactor = 1.1
	// downloadBaselineSpeed 单连接实测基线：镜像对单连接限流在 0.3–0.5 MB/s，
	// 取偏保守的 0.25 MB/s 作为「单流也至少有这么快」的下限
	downloadBaselineSpeed = 256 << 10
	// downloadFallbackMinElapsed 增量自检的最短观察期：本地/局域网/缓存命中的链路
	// 一块分片几十毫秒就完成，此时「单流预估」本身只有几百毫秒，任何调度抖动都会
	// 被算成「超过 1.2 倍」。低于这个时长一律不判，避免把快链路误判成慢链路。
	downloadFallbackMinElapsed = 3 * time.Second
	// downloadChunkRetries 单个分片的最大尝试次数：每失败一次换一个镜像
	downloadChunkRetries = 4
	// mirrorFailLimit 同一镜像在本次下载中连续失败多少次后降级跳过
	mirrorFailLimit = 3
	// downloadMaxFiles 分片数上限，防止异常大小把 goroutine 数量放大
	downloadMaxChunks = 64
	// downloadQuietWindow 进度回调节流间隔
	downloadQuietWindow = 200 * time.Millisecond
	// downloadStallTimeout 单条连接的最长无进展时间（读超时）
	downloadStallTimeout = 45 * time.Second
)

// UpdateMirrors 自更新加速源（顺序即优先级）。
//
// 选型依据（2026-10-21 16MB fpk 实测，单连接）：
//   - gh-proxy.com        多轮 0.95~6.4 MB/s，最稳，首选
//   - v4.gh-proxy.org     与 gh-proxy.com 同一后端（x-github-request-id / ETag /
//     x-cache-hits 完全一致）——只能算「同后端换节点」，不是灾备
//   - ghfast.top          独立运营，抖动极大（1.8 MB/s ~ 100s 超时）
//   - ghproxy.imciel.com  独立运营，0.5~0.9 MB/s
//
// 已淘汰：gh.dpik.top（实测仅 279 KB/s，且 API 侧 403）。
// 这份列表是唯一事实来源：不再从安装包里读外部清单，改这里即可。
var UpdateMirrors = []string{
	"https://gh-proxy.com/",
	"https://v4.gh-proxy.org/",
	"https://ghfast.top/",
	"https://ghproxy.imciel.com/",
}

// legacyMirror 已淘汰的旧代理：出现时一律剔除，避免回退到它
const legacyMirror = "https://gh.dpik.top/"

// testMirrorOverride 仅测试使用：非 nil 时 mirrorOrder 直接返回它（不读磁盘）
var testMirrorOverride []string

// mirrorOrder 返回本次下载使用的镜像顺序。
// 只认内置的 UpdateMirrors——加速源清单随代码走，不读任何外部文件，
// 也就不存在「清单过期 / 清单与代码不一致」这类问题。
func mirrorOrder() []string {
	if testMirrorOverride != nil {
		return append([]string(nil), testMirrorOverride...)
	}
	out := make([]string, 0, len(UpdateMirrors))
	for _, m := range UpdateMirrors {
		if strings.HasPrefix(m, legacyMirror) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// contentRange 解析 RFC 7233 的 Content-Range: bytes <start>-<end>/<total>。
// total 为 "*"（服务器不知道总长）时 total 返回 -1。
func parseContentRange(v string) (start, end, total int64, ok bool) {
	v = strings.TrimSpace(v)
	if len(v) < 6 || !strings.EqualFold(v[:5], "bytes") {
		return 0, 0, 0, false
	}
	v = strings.TrimSpace(v[5:])
	rangePart, totalPart, found := strings.Cut(v, "/")
	if !found {
		return 0, 0, 0, false
	}
	s, e, found := strings.Cut(strings.TrimSpace(rangePart), "-")
	if !found {
		return 0, 0, 0, false
	}
	start, err := parseInt64(s)
	if err != nil || start < 0 {
		return 0, 0, 0, false
	}
	end, err = parseInt64(e)
	if err != nil || end < start {
		return 0, 0, 0, false
	}
	totalPart = strings.TrimSpace(totalPart)
	if totalPart == "*" {
		return start, end, -1, true
	}
	total, err = parseInt64(totalPart)
	if err != nil || total <= end {
		return 0, 0, 0, false
	}
	return start, end, total, true
}

func parseInt64(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty")
	}
	var n int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int64(c-'0')
		if n < 0 {
			return 0, errors.New("overflow")
		}
	}
	return n, nil
}

// chunkRange 一个分片在文件中的 [start, end] 闭区间
type chunkRange struct {
	index      int
	start, end int64
}

func (c chunkRange) size() int64 { return c.end - c.start + 1 }

// planChunks 把 size 字节按 chunk 大小切成连续分片（最后一片可为短片）。
// size<=0 或 chunk<=0 返回 nil。
func planChunks(size, chunk int64) []chunkRange {
	if size <= 0 || chunk <= 0 {
		return nil
	}
	n := (size + chunk - 1) / chunk
	out := make([]chunkRange, 0, n)
	for i := int64(0); i < n; i++ {
		start := i * chunk
		end := start + chunk - 1
		if end >= size {
			end = size - 1
		}
		out = append(out, chunkRange{index: int(i), start: start, end: end})
	}
	return out
}

// chooseChunkSize 根据文件大小选分片大小：默认 4MB；分片数超过上限时
// 向上放大（不超过 8MB），避免小文件过度切分或大文件分片数失控。
func chooseChunkSize(size int64) int64 {
	chunk := int64(downloadChunkSize)
	if size <= 0 {
		return chunk
	}
	for (size+chunk-1)/chunk > downloadMaxChunks && chunk < downloadChunkSizeMax {
		chunk *= 2
	}
	return chunk
}

// updateClient 下载用的 http.Client：不复用默认 Transport 的超时，
// 只设单次 TLS 握手与空闲连接参数，读超时由各分片自己的 context 控制。
func updateClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
			MaxIdleConns:        downloadWorkers * 2,
			MaxIdleConnsPerHost: downloadWorkers,
			IdleConnTimeout:     60 * time.Second,
			ForceAttemptHTTP2:   true,
		},
	}
}

// sourceSet 一次下载的候选来源（镜像前缀 + 直连兜底）
type sourceSet struct {
	prefixes []string // 镜像前缀，按优先级
	rawURL   string   // GitHub 原始地址（直连）
}

// urlFor 返回第 i 个候选地址：前 len(prefixes) 个是镜像，最后一个是直连。
// 直连排在最后：实测直连常被墙，放最前只会让用户先白等一轮超时。
func (s sourceSet) urlFor(i int) string {
	n := len(s.prefixes)
	if n == 0 {
		return s.rawURL
	}
	if i < 0 {
		i = 0
	}
	i %= n + 1
	if i == n {
		return s.rawURL
	}
	return s.prefixes[i] + s.rawURL
}

func (s sourceSet) count() int { return len(s.prefixes) + 1 }

// label 来源的展示名（进度信息里用主机名，不泄露完整 URL）
func (s sourceSet) label(i int) string {
	u := s.urlFor(i)
	if u == s.rawURL {
		return "github.com"
	}
	return strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://"), "/")
}

// probeResult Range 探测结果
type probeResult struct {
	size    int64
	etag    string
	source  int  // 探测成功的候选序号
	hasETag bool //
	// rangeOK 服务器是否真的支持 Range（返回 206 + 可解析的 Content-Range）
	rangeOK bool
}

// probeRemote 用 Range: bytes=0-0 探测文件总大小与 ETag（作为一致性基线）。
// 依次尝试各候选来源；全部失败返回 error。
func probeRemote(ctx context.Context, client *http.Client, src sourceSet, userAgent string) (*probeResult, error) {
	var lastErr error
	for i := 0; i < src.count(); i++ {
		size, etag, rangeOK, err := probeOne(ctx, client, src.urlFor(i), userAgent)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", src.label(i), err)
			continue
		}
		return &probeResult{size: size, etag: etag, source: i, hasETag: strings.TrimSpace(etag) != "", rangeOK: rangeOK}, nil
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的下载来源")
	}
	return nil, lastErr
}

// probeOne 对单个地址做 Range 探测。
// 206 + Content-Range 表示支持分片；200 表示服务器忽略 Range（不支持），
// 此时仍可拿到大小（Content-Length），由调用方回退单流下载。
func probeOne(ctx context.Context, client *http.Client, url, userAgent string) (size int64, etag string, rangeOK bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, "", false, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", false, fmt.Errorf("网络错误: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
	}()
	etag = resp.Header.Get("ETag")
	switch resp.StatusCode {
	case http.StatusPartialContent:
		_, _, total, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if !ok || total <= 0 {
			// 声称 206 却给不出合法 Content-Range：视为不可分片
			return 0, etag, false, fmt.Errorf("Content-Range 非法: %q", resp.Header.Get("Content-Range"))
		}
		if total > fpkMaxSize {
			return 0, etag, false, fmt.Errorf("文件过大 (%.1fMB > %.1fMB)", float64(total)/(1<<20), float64(fpkMaxSize)/(1<<20))
		}
		return total, etag, true, nil
	case http.StatusOK:
		if resp.ContentLength > fpkMaxSize {
			return 0, etag, false, fmt.Errorf("文件过大 (%.1fMB > %.1fMB)", float64(resp.ContentLength)/(1<<20), float64(fpkMaxSize)/(1<<20))
		}
		return resp.ContentLength, etag, false, nil
	case http.StatusRequestedRangeNotSatisfiable:
		// 416：多半是资源不可 Range；不视为可用来源
		return 0, etag, false, fmt.Errorf("服务器返回 HTTP %d", resp.StatusCode)
	default:
		return 0, etag, false, fmt.Errorf("服务器返回 HTTP %d", resp.StatusCode)
	}
}

// etagKind 判断 ETag 是否为弱校验器（W/ 前缀）。弱 ETag 不保证字节级一致，
// 因此不能作为分片一致性基线。
func isWeakETag(etag string) bool {
	etag = strings.TrimSpace(etag)
	return len(etag) >= 2 && (etag[0] == 'W' || etag[0] == 'w') && etag[1] == '/'
}

// etagMatches 分片响应的 ETag 是否与探测基线一致。
// 基线为空（服务器没给 ETag）或分片响应没给 ETag 时无法判定不一致，
// 交由全文件 SHA-256 兜底；给了就必须逐字节相等。
func etagMatches(base, got string) bool {
	base = strings.TrimSpace(base)
	got = strings.TrimSpace(got)
	if base == "" || got == "" {
		return true
	}
	return base == got
}

// fetchRange 取一个分片，校验状态码 / Content-Range 偏移 / ETag，返回数据。
// 任何不一致都返回错误，由调用方换镜像重试。
func fetchRange(ctx context.Context, client *http.Client, url string, cr chunkRange, baseETag, userAgent string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", cr.start, cr.end))
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("网络错误: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return nil, "", fmt.Errorf("服务器返回 HTTP %d（分片需要 206）", resp.StatusCode)
	}
	start, end, _, ok := parseContentRange(resp.Header.Get("Content-Range"))
	if !ok {
		return nil, "", fmt.Errorf("Content-Range 非法: %q", resp.Header.Get("Content-Range"))
	}
	// 偏移校验：服务器若返回别的区间，按 offset 写回就会把文件写错位
	if start != cr.start || end != cr.end {
		return nil, "", fmt.Errorf("Content-Range 偏移不符: 期望 %d-%d, 实际 %d-%d", cr.start, cr.end, start, end)
	}
	gotETag := resp.Header.Get("ETag")
	if !etagMatches(baseETag, gotETag) {
		return nil, gotETag, fmt.Errorf("ETag 与基线不一致（%q != %q），上游文件已变化", gotETag, baseETag)
	}
	buf := make([]byte, cr.size())
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		return nil, gotETag, fmt.Errorf("分片读取中断: %w", err)
	}
	// 多读一个字节：响应体比声明的长说明这条链路本身有问题
	if extra, _ := resp.Body.Read(make([]byte, 1)); extra > 0 {
		return nil, gotETag, fmt.Errorf("分片长度超过声明值")
	}
	return buf, gotETag, nil
}

// chunkOutcome 分片下载结果（测试与进度展示用）
type chunkOutcome struct {
	index   int
	source  int
	attempt int
	elapsed time.Duration
}

// downloadProgress 进度回调：written/total 为已写字节与总字节，chunk 为刚完成的分片序号。
type downloadProgress func(written, total int64, doneChunks, totalChunks int, label string)

// parallelDownload 并行分片下载到单个 .part 文件（WriteAt 按偏移写，不产生分片临时文件）。
// 返回分片结果（含每个分片实际用的来源，测试用）。
func parallelDownload(ctx context.Context, client *http.Client, dst *os.File, src sourceSet, probe *probeResult, onProgress downloadProgress) ([]chunkOutcome, error) {
	chunks := planChunks(probe.size, chooseChunkSize(probe.size))
	if len(chunks) == 0 {
		return nil, fmt.Errorf("文件大小异常: %d 字节", probe.size)
	}
	// 预分配：Truncate 让文件长度等于总大小，后续 WriteAt 直接落到正确偏移
	if err := dst.Truncate(probe.size); err != nil {
		return nil, fmt.Errorf("预分配文件失败: %w", err)
	}

	baseETag := ""
	if probe.hasETag && !isWeakETag(probe.etag) {
		baseETag = strings.TrimSpace(probe.etag)
	}

	n := src.count()
	// 起始来源轮转：第 i 片从第 (probe.source+i) 个候选开始，把并发摊到不同镜像上
	nextSource := func(chunkIdx, attempt int) int {
		return (probe.source + chunkIdx + attempt) % n
	}

	var (
		wg         sync.WaitGroup
		liveConns  atomic.Int32
		writeMu    sync.Mutex
		written    atomic.Int64
		chunksDone atomic.Int32
		failed     atomic.Bool
		firstErr   error
		errOnce    sync.Once
	)
	// 镜像健康度：两个维度
	//   1) 连续失败 streak：报错/超时够多就降级
	//   2) EWMA 速度 speed：明显慢于全局中位数就降级（慢镜像不报错，但会吃掉整轮墙钟）
	// 冷启动保护：速度样本不足 downloadSpeedMinSamples 时绝不按速度降级，
	// 否则第一个慢响应就会把可用镜像全判死。
	var healthMu sync.Mutex
	streak := make([]int, n)
	disabled := make([]bool, n)
	speed := make([]float64, n) // bytes/s EWMA
	samples := make([]int, n)
	markFail := func(i int) {
		healthMu.Lock()
		streak[i]++
		if streak[i] >= mirrorFailLimit {
			disabled[i] = true
		}
		healthMu.Unlock()
	}
	markOK := func(i int, bytes int64, elapsed time.Duration) {
		healthMu.Lock()
		streak[i] = 0
		if elapsed > 0 && bytes > 0 {
			v := float64(bytes) / elapsed.Seconds()
			if samples[i] == 0 {
				speed[i] = v
			} else {
				speed[i] = (1-downloadSpeedAlpha)*speed[i] + downloadSpeedAlpha*v
			}
			samples[i]++
			// 速度降级：样本够 + 慢于中位数 1/4
			if median := medianSpeed(speed, samples, downloadSpeedMinSamples); median > 0 {
				for j := 0; j < n; j++ {
					if samples[j] >= downloadSpeedMinSamples && speed[j] < median*downloadSlowRatio {
						disabled[j] = true
					}
				}
			}
		}
		healthMu.Unlock()
	}
	globalSpeed := func() float64 {
		healthMu.Lock()
		defer healthMu.Unlock()
		return medianSpeed(speed, samples, downloadSpeedMinSamples)
	}
	isDisabled := func(i int) bool {
		healthMu.Lock()
		defer healthMu.Unlock()
		return disabled[i]
	}
	enableable := func(i int) bool { return !isDisabled(i) }

	pickSource := func(chunkIdx, attempt int) int {
		for k := 0; k < n; k++ {
			cand := nextSource(chunkIdx, attempt+k)
			if enableable(cand) {
				return cand
			}
		}
		// 全部降级：仍然重试轮转，避免因为健康度判断把可用的源挡在外面
		return nextSource(chunkIdx, attempt)
	}

	// chunkDeadline 单片墙钟上限：正常成本 = 片字节 / 全局估计速度；没有速度样本时
	// 用「单流基准 250KB/s」兜底。改派第一次放宽、之后收紧。
	chunkDeadline := func(cr chunkRange, attempt int) time.Duration {
		sp := globalSpeed()
		if sp <= 0 {
			sp = 256 << 10
		}
		cost := time.Duration(float64(cr.size()) / sp * float64(time.Second))
		factor := downloadHedgeFirst
		if attempt > 0 {
			factor = downloadHedgeNext
		}
		d := time.Duration(float64(cost) * factor)
		if d < downloadHedgeFloor {
			d = downloadHedgeFloor
		}
		// 上限：单片再慢也不能超过整体超时的三成
		if max := downloadStallTimeout * 3; d > max {
			d = max
		}
		return d
	}

	// 增量自检：并行是否真的比单流快？
	//
	// 拿「已下字节 / 实测速度」推算总时长是错的：那是按**已并发速度**线性外推
	// （速度里已含并发收益），3 秒看到 300KB 就会推算「整包要 20 秒」而真实约 15 秒，
	// 于是正常下载被判死。而且小文件在 3 秒内本来就该下完，观察期本身就签到了失败。
	//
	// 正确口径是比「效率」：
	//   并行速度 / 当前发起过的连接数  = 平均每条连接的速度
	// 如果这个值已经低于「单连接实测基线」（downloadBaselineSpeed），说明并发
	// 完全没有带来收益，单流（一条连接）不会更差 —— 此时才回退。
	// 观察期取「单流基线耗时 x 0.25」与 4s 的较大者：小文件（本地/缓存链路）在
	// 这段时间内早已下完，根本轮不到判断。
	startedAt := time.Now()
	arrangeAbort := func() bool {
		writtenNow := written.Load()
		// 进度不足 25%（整数比较，避开浮点）
		if writtenNow*4 < probe.size {
			return false
		}
		elapsed := time.Since(startedAt)
		minWatch := downloadFallbackMinElapsed
		if base := time.Duration(float64(probe.size) / float64(downloadBaselineSpeed) * float64(time.Second)); base/4 > minWatch {
			minWatch = base / 4
		}
		if elapsed < minWatch {
			return false
		}
		sp := globalSpeed()
		if sp <= 0 {
			return false
		}
		conns := liveConns.Load()
		if conns < 2 {
			return false // 只有一条连接在跑，谈不上「并发没收益」
		}
		perConn := sp / float64(conns)
		return perConn < float64(downloadBaselineSpeed)*downloadFallbackFactor
	}

	jobs := make(chan chunkRange)
	outcomes := make([]chunkOutcome, len(chunks))
	var lastLabel atomic.Value
	lastLabel.Store(src.label(probe.source))

	var lastProgressAt atomic.Int64
	emitProgress := func(force bool) {
		if onProgress == nil {
			return
		}
		now := time.Now().UnixNano()
		if !force && now-lastProgressAt.Load() < int64(downloadQuietWindow) {
			return
		}
		lastProgressAt.Store(now)
		label, _ := lastLabel.Load().(string)
		onProgress(written.Load(), probe.size, int(chunksDone.Load()), len(chunks), label)
	}

	workers := downloadWorkers
	if workers > len(chunks) {
		workers = len(chunks)
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for cr := range jobs {
				if ctx.Err() != nil || failed.Load() {
					return
				}
				var lastErr error
				done := false
				for attempt := 0; attempt < downloadChunkRetries && attempt < n*downloadChunkRetries; attempt++ {
					if ctx.Err() != nil || failed.Load() {
						return
					}
					if attempt >= n {
						select {
						case <-ctx.Done():
							return
						case <-time.After(time.Duration(attempt-n+1) * 200 * time.Millisecond):
						}
					}
					si := pickSource(cr.index, attempt)
					url := src.urlFor(si)
					lastLabel.Store(src.label(si))
					started := time.Now()

					// 每片独立 ctx：墙钟到点就 cancel，让这条连接立刻被放弃
					// （否则慢镜像的 goroutine 会一直占着连接池，连带堵住其它分片）
					chunkCtx, chunkCancel := context.WithTimeout(ctx, chunkDeadline(cr, attempt))
					liveConns.Add(1)
					data, _, err := fetchRange(chunkCtx, client, url, cr, baseETag, updateUserAgent)
					liveConns.Add(-1)
					chunkCancel()
					if err == nil {
						writeMu.Lock()
						_, werr := dst.WriteAt(data, cr.start)
						writeMu.Unlock()
						if werr != nil {
							failed.Store(true)
							errOnce.Do(func() { firstErr = fmt.Errorf("写入失败: %w", werr) })
							return
						}
						markOK(si, cr.size(), time.Since(started))
						outcomes[cr.index] = chunkOutcome{index: cr.index, source: si, attempt: attempt, elapsed: time.Since(started)}
						written.Add(cr.size())
						chunksDone.Add(1)
						emitProgress(false)
						done = true
						break
					}
					lastErr = err
					// 墙钟超时（而不是网络报错）：只算「慢」，不算「坏」——换源继续，
					// 但不给它记失败streak，避免把「只是慢」的镜像整轮判死
					if isDeadline(err) {
						slog.Debug("更新包分片超过墙钟上限，改派其它镜像",
							"chunk", cr.index, "source", src.label(si), "err", err)
					} else {
						markFail(si)
					}
					slog.Debug("更新包分片下载失败，换源重试",
						"chunk", cr.index, "range", cr.start, "source", src.label(si), "err", err)
				}
				if !done {
					failed.Store(true)
					errOnce.Do(func() {
						firstErr = fmt.Errorf("分片 %d/%d (%d-%d) 重试 %d 次仍失败: %v",
							cr.index+1, len(chunks), cr.start, cr.end, downloadChunkRetries, lastErr)
					})
					return
				}
			}
		}()
	}

	// 监视线程：增量自检 + 定期把「并行不划算」判出来（放在独立 goroutine，
	// 避免 worker 全被慢分片占住时没人发现）
	// 监视线程必须能被主流程停掉：它监听的是内部 mctx，而不是外层 ctx
	mctx, mstop := context.WithCancel(ctx)
	defer mstop()
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-mctx.Done():
				return
			case <-tick.C:
				if !failed.Load() && arrangeAbort() {
					failed.Store(true)
					errOnce.Do(func() {
						firstErr = errSlowParallel
					})
					return
				}
			}
		}
	}()

	for _, cr := range chunks {
		select {
		case jobs <- cr:
		case <-ctx.Done():
			failed.Store(true)
			errOnce.Do(func() { firstErr = ctx.Err() })
			close(jobs)
			wg.Wait()
			mstop()
			<-monitorDone
			return outcomes, firstErr
		}
	}
	close(jobs)
	wg.Wait()
	mstop()
	<-monitorDone
	emitProgress(true)
	if failed.Load() {
		if firstErr == nil {
			firstErr = errors.New("分片下载中断")
		}
		return outcomes, firstErr
	}
	if int(chunksDone.Load()) != len(chunks) || written.Load() != probe.size {
		return outcomes, fmt.Errorf("分片下载未完成（已写 %d/%d 字节）", written.Load(), probe.size)
	}
	return outcomes, nil
}

// errSlowParallel 增量自检判定「并行分片比单流还慢」，调用方据此回退单流。
// 与「下载失败」区分开：失败不改走单流（单流只会更慢），而这里单流确实可能更快。
var errSlowParallel = errors.New("并行分片下载不划算（慢于单流预估）")

// isDeadline 判断是否「墙钟超时/被 cancel」，用于区分「慢」与「坏」
func isDeadline(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "context canceled") ||
		strings.Contains(msg, "Client.Timeout")
}

// medianSpeed 有足够样本的镜像速度中位数；样本不足返回 0（表示「还判断不了」）
func medianSpeed(speed []float64, samples []int, minSamples int) float64 {
	vals := make([]float64, 0, len(speed))
	for i := range speed {
		if samples[i] >= minSamples {
			vals = append(vals, speed[i])
		}
	}
	if len(vals) == 0 {
		return 0
	}
	sort.Float64s(vals)
	return vals[len(vals)/2]
}

// sequentialDownload Range 不可用时的回退路径：单流顺序下载，镜像按顺序尝试、直连兜底。
// 这是改造前的原逻辑，保留它保证「服务器不支持 Range 时功能不退化」。
func sequentialDownload(ctx context.Context, client *http.Client, dst *os.File, src sourceSet, probe *probeResult, onProgress downloadProgress) error {
	var lastErr error
	// 优先用探测成功的那个来源，再按顺序试其余
	order := make([]int, 0, src.count())
	if probe != nil {
		order = append(order, probe.source)
	}
	for i := 0; i < src.count(); i++ {
		if probe != nil && i == probe.source {
			continue
		}
		order = append(order, i)
	}
	for _, si := range order {
		url := src.urlFor(si)
		err := streamToFile(ctx, client, url, dst, src.label(si), onProgress)
		if err == nil {
			return nil
		}
		lastErr = fmt.Errorf("%s: %w", src.label(si), err)
		slog.Warn("更新包顺序下载失败", "source", src.label(si), "err", err)
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的下载来源")
	}
	return lastErr
}

// streamToFile 单流下载写入 dst（从头覆盖，失败时调用方负责清理 .part）
func streamToFile(ctx context.Context, client *http.Client, url string, dst *os.File, label string, onProgress downloadProgress) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", updateUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("网络错误: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("服务器返回 HTTP %d", resp.StatusCode)
	}
	total := resp.ContentLength
	if total <= 0 {
		total = 0
	}
	if total > fpkMaxSize {
		return fmt.Errorf("文件过大 (%.1fMB > %.1fMB)", float64(total)/(1<<20), float64(fpkMaxSize)/(1<<20))
	}
	if err := dst.Truncate(0); err != nil {
		return err
	}
	if _, err := dst.Seek(0, io.SeekStart); err != nil {
		return err
	}
	buf := make([]byte, 256<<10)
	var downloaded int64
	lastAt := time.Time{}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return fmt.Errorf("写入失败: %w", werr)
			}
			downloaded += int64(n)
			if downloaded > fpkMaxSize {
				return fmt.Errorf("下载内容超过大小限制 (%d MB)", fpkMaxSize>>20)
			}
			if onProgress != nil && (total > 0 && time.Since(lastAt) > downloadQuietWindow) {
				lastAt = time.Now()
				onProgress(downloaded, total, 0, 0, label)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("下载中断: %w", rerr)
		}
	}
	if downloaded == 0 {
		return errors.New("下载文件为空")
	}
	if total > 0 && downloaded != total {
		return fmt.Errorf("下载长度不符: 期望 %d 字节, 实际 %d 字节", total, downloaded)
	}
	if onProgress != nil {
		onProgress(downloaded, downloaded, 0, 0, label)
	}
	return nil
}

// sha256File 全文件 SHA-256（分片拼接后的最终校验，不可省略）
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fetchFPK 下载更新包到 dest 并做完整校验（魔数 / 大小 / SHA-256），
// 先写 dest+".part"，全部校验通过后 fsync + rename。
// direct 表示本次唯一可用来源是 GitHub 直连（仅影响「上游无摘要」时的取舍）。
//
// 任何失败路径都会删除 .part 并把错误抛给调用方，调用方随即释放 updating 标志，
// 因此用户点「一键更新」重试即可，不会被残留文件或卡住的标志挡住。
func fetchFPK(ctx context.Context, rawURL, dest string, direct bool, info *releaseInfo, onProgress downloadProgress) error {
	prefixes := mirrorOrder()
	if direct {
		prefixes = nil
	}
	src := sourceSet{prefixes: prefixes, rawURL: rawURL}
	client := updateClient()

	tmp := dest + ".part"
	// 先清掉上次失败留下的残件，再以 O_EXCL 创建：即便有人预置了同名软链，
	// Remove 只删链接本身，O_EXCL 也不会跟随链接写到目标文件上
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	cleanup := func() {
		_ = f.Close()
		_ = os.Remove(tmp)
	}

	probe, perr := probeRemote(ctx, client, src, updateUserAgent)
	if perr != nil {
		cleanup()
		return fmt.Errorf("探测下载来源失败: %w", perr)
	}
	if probe.size > 0 && info != nil && info.FPKSize > 0 && probe.size != info.FPKSize {
		cleanup()
		return fmt.Errorf("远端文件大小 %d 与发布信息 %d 不一致（加速源可能缓存了旧文件）", probe.size, info.FPKSize)
	}

	if probe.rangeOK && probe.size > 0 {
		if _, perr := parallelDownload(ctx, client, f, src, probe, onProgress); perr != nil {
			// 并行被判「不如单流」是唯一允许回退的情形：单流确实可能更快。
			// 真正的下载失败（分片重试耗尽）不回退——单流只会更慢，还会多等一轮。
			if errors.Is(perr, errSlowParallel) && ctx.Err() == nil {
				slog.Info("并行分片未提速，回退单流下载", "err", perr)
				if rerr := f.Truncate(0); rerr != nil {
					cleanup()
					return rerr
				}
				if rerr := sequentialDownload(ctx, client, f, src, probe, onProgress); rerr != nil {
					cleanup()
					return rerr
				}
			} else {
				cleanup()
				return perr
			}
		}
	} else {
		// 服务器不支持 Range：回退单流顺序下载（镜像优先、直连兜底）
		slog.Info("加速源不支持 Range 分片，回退为单流下载")
		if probe.size <= 0 {
			probe = nil
		}
		if err := sequentialDownload(ctx, client, f, src, probe, onProgress); err != nil {
			cleanup()
			return err
		}
	}

	st, err := f.Stat()
	if err != nil {
		cleanup()
		return err
	}
	size := st.Size()
	if size == 0 {
		cleanup()
		return errors.New("下载文件为空")
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("刷盘失败: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("关闭文件失败: %w", err)
	}

	// fpk 实体是 tar.gz / zip 包，校验魔数防止把错误页存成更新包
	head := make([]byte, 2)
	if hf, err := os.Open(tmp); err == nil {
		_, _ = hf.Read(head)
		hf.Close()
	}
	gzipMagic := head[0] == 0x1f && head[1] == 0x8b
	zipMagic := head[0] == 'P' && head[1] == 'K'
	if !gzipMagic && !zipMagic {
		_ = os.Remove(tmp)
		return fmt.Errorf("文件校验失败: 内容异常 (%q)", head)
	}
	if info != nil && info.FPKSize > 0 && size != info.FPKSize {
		_ = os.Remove(tmp)
		return fmt.Errorf("文件大小不匹配: 期望 %d 字节, 实际 %d 字节", info.FPKSize, size)
	}

	// 完整性：分片来自不同第三方代理，大小与魔数都可伪造；分片拼接后必须整文件
	// 重算 SHA-256，与 GitHub 侧摘要（随 api.github.com 元数据直连取得）比对。
	got, err := sha256File(tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("计算摘要失败: %w", err)
	}
	digest := ""
	md5sum := ""
	if info != nil {
		digest = info.FPKDigest
		md5sum = info.FPKMD5
	}
	if digest == "" && md5sum != "" {
		// 上游只给了 md5：它足够发现「代理返回了别的文件 / 缓存串包」，但抗不住构造。
		// 所以经代理链路仍要求至少有一种摘要，同时在日志里标明用的是弱摘要。
		if sum, err := md5File(tmp); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("计算摘要失败: %w", err)
		} else if !strings.EqualFold(sum, md5sum) {
			_ = os.Remove(tmp)
			return errors.New("文件校验失败: MD5 与发布版本不一致（下载链路可能被替换）")
		}
		slog.Warn("发布资源仅提供 MD5 摘要，已按 MD5 校验", "fpk", fpkNameOf(info))
		return os.Rename(tmp, dest)
	}
	if digest == "" {
		// 无摘要可校验时，经代理链路一律拒绝——否则「校验」形同虚设。
		// GitHub 直连下载时文件与元数据同源，才降级为仅告警。
		if !direct {
			_ = os.Remove(tmp)
			return errors.New("发布资源未提供 SHA-256 摘要，无法校验经代理下载的更新包")
		}
		slog.Warn("发布资源未提供 SHA-256 摘要，本次仅校验了大小与魔数", "fpk", fpkNameOf(info), "sha256", got)
	} else if !strings.EqualFold(got, digest) {
		_ = os.Remove(tmp)
		return errors.New("文件校验失败: SHA-256 与发布版本不一致（下载链路可能被替换）")
	}
	return os.Rename(tmp, dest)
}

// md5File 全文件 MD5（仅在上游未提供 SHA-256 时作为弱摘要兜底）
func md5File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fpkNameOf 摘要日志用的包名（info 允许为 nil）
func fpkNameOf(info *releaseInfo) string {
	if info == nil {
		return ""
	}
	return info.FPKName
}

// sortedMirrorList 排序后的镜像快照（测试/日志用，避免外部修改切片）
func sortedMirrorList() []string {
	out := append([]string(nil), mirrorOrder()...)
	sort.Strings(out)
	return out
}
