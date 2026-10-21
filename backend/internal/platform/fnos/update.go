package fnos

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sushazhi/seedark/backend/internal/models"
)

// fnOS 应用更新：GitHub Release 检测最新版，下载 fpk 包走「多镜像 Range 分片并发」，
// 镜像不可用/不支持 Range 时回退单流顺序下载。加速源列表见 UpdateMirrors（update_download.go）。
const (
	updateRepo        = "sushazhi/fnos-transmission"
	updateAPIBase     = "https://api.github.com"
	updateCheckTTL    = 5 * time.Minute
	fpkMaxSize        = 100 << 20 // 100 MB
	downloadTimeout   = 3 * time.Minute
	defaultAppVersion = "1.0.0"
	sha256HexLen      = 64 // SHA-256 的十六进制表示长度
	// updateUserAgent 下载请求的 UA：部分镜像会过滤非常规 UA（空 UA 直接 403）
	updateUserAgent = "Mozilla/5.0 (compatible; SeedArk-updater)"
)

var (
	reFPKVersion  = regexp.MustCompile(`-([\d][\d.]*)-(?:amd64|arm64)\.fpk$`)
	reManifestVer = regexp.MustCompile(`(?i)^\s*version\s*=\s*(.+?)\s*$`)

	// changelog 归一化用：换行标签 / 其余 HTML 标签 / 连续空行
	reBrTag      = regexp.MustCompile(`(?i)<br\s*/?>`)
	reHTMLTag    = regexp.MustCompile(`(?s)</?[a-zA-Z][^>]*>`)
	reBlankLines = regexp.MustCompile(`\n{3,}`)
)

// normalizeChangelog 把 Release body 归一化为纯文本更新日志。
// fnOS manifest 的 changelog 用 <br> 分行，CI 原样作为 GitHub Release body 发布，
// 前端按纯文本渲染时 <br> 会原样显示且不换行；这里统一转为真实换行、
// 剥离其余 HTML 标签并解码实体，顺带去掉冗余的「更新日志」标题行。
func normalizeChangelog(body string) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	s := reBrTag.ReplaceAllString(body, "\n")
	s = reHTMLTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	if len(lines) > 1 {
		first := strings.TrimSpace(lines[0])
		if first == "更新日志" || strings.EqualFold(first, "changelog") || strings.EqualFold(first, "change log") {
			lines = lines[1:]
		}
	}
	s = reBlankLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")
	return strings.TrimSpace(s)
}

// appArch 当前部署架构（fnOS 仅提供 amd64 / arm64 更新包）
func appArch() string {
	switch a := strings.ToLower(runtime.GOARCH); a {
	case "arm64", "aarch64", "armv8l":
		return "arm64"
	default:
		return "amd64"
	}
}

// updateDirName 更新包在数据目录下的子目录名
const updateDirName = "update"

// fpkPath 已下载的更新包落盘位置。
// 放在数据目录（0600/0700）而非系统临时目录：后者所有本机用户可写，
// 固定文件名可被预置软链，服务写入时即以自身权限覆盖任意文件。
func (u *updateHandler) fpkPath() string {
	return filepath.Join(u.dir, updateDirName, "transmission-update.fpk")
}

// currentAppVersion 优先读 fnOS 运行时注入的版本信息，退回宿主机上的应用 manifest
func currentAppVersion() string {
	if v := os.Getenv("TRIM_APPVER"); v != "" {
		return v
	}
	paths := []string{}
	if dest := os.Getenv("TRIM_APPDEST"); dest != "" {
		paths = append(paths, filepath.Join(dest, "manifest"))
	}
	paths = append(paths, "/var/apps/transmission/manifest")
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if m := reManifestVer.FindStringSubmatch(line); m != nil {
				return m[1]
			}
		}
	}
	return defaultAppVersion
}

// compareAppVersion 语义化版本比较：latest 更新返回 1，相同 0，更旧 -1
func compareAppVersion(current, latest string) int {
	parse := func(v string) []int {
		parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
		out := make([]int, len(parts))
		for i, p := range parts {
			n, _ := strconv.Atoi(p)
			out[i] = n
		}
		return out
	}
	a, b := parse(current), parse(latest)
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			if y > x {
				return 1
			}
			return -1
		}
	}
	return 0
}

type githubRelease struct {
	TagName     string `json:"tag_name"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
	HTMLURL     string `json:"html_url"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
		// Digest 由 GitHub 侧计算（形如 "sha256:<hex>"），是走代理下载时唯一的完整性依据。
		// 注意 GitHub 早期资产只有 md5，需要兜底。
		Digest string `json:"digest"`
		MD5    string `json:"md5"`
	} `json:"assets"`
}

type releaseInfo struct {
	Version     string
	Changelog   string
	PublishedAt string
	ReleaseURL  string
	FPKURL      string
	FPKName     string
	FPKSize     int64
	FPKDigest   string // 期望的 SHA256（十六进制小写），空表示上游未提供
	FPKMD5      string // 上游只提供 md5 时的兜底摘要（十六进制小写）
}

// fetchLatestRelease 查询 GitHub 最新 Release 并按架构挑选 fpk 更新包
func fetchLatestRelease() (*releaseInfo, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", updateAPIBase, updateRepo)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "fnos-transmission-webui-updater")
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("无法连接 GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub 返回 HTTP %d（仓库可能尚未发布 Release）", resp.StatusCode)
	}
	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("解析版本信息失败: %w", err)
	}
	arch := appArch()
	archSuffix := "-" + arch + ".fpk"
	var fpkURL, fpkName, fpkDigest, fpkMD5 string
	var fpkSize int64
	for _, a := range rel.Assets {
		if strings.HasSuffix(a.Name, archSuffix) && strings.Contains(strings.ToLower(a.Name), "transmission") {
			fpkURL, fpkName, fpkSize, fpkDigest = a.BrowserDownloadURL, a.Name, a.Size, normalizeDigest(a.Digest)
			fpkMD5 = normalizeMD5(a.MD5)
			break
		}
	}
	if fpkURL == "" {
		for _, a := range rel.Assets {
			if strings.HasSuffix(a.Name, ".fpk") {
				fpkURL, fpkName, fpkSize, fpkDigest = a.BrowserDownloadURL, a.Name, a.Size, normalizeDigest(a.Digest)
				fpkMD5 = normalizeMD5(a.MD5)
				break
			}
		}
	}
	return &releaseInfo{
		Version:     strings.TrimPrefix(rel.TagName, "v"),
		Changelog:   normalizeChangelog(rel.Body),
		PublishedAt: rel.PublishedAt,
		ReleaseURL:  rel.HTMLURL,
		FPKURL:      fpkURL,
		FPKName:     fpkName,
		FPKSize:     fpkSize,
		FPKDigest:   fpkDigest,
		FPKMD5:      fpkMD5,
	}, nil
}

// normalizeDigest 归一化 GitHub 的资源摘要（"sha256:<hex>"）。
// 只认 sha256 且必须是 64 位十六进制；算法不符或格式异常按未提供处理，
// 免得把一条来历不明的摘要当成校验依据。
func normalizeDigest(raw string) string {
	raw = strings.TrimSpace(raw)
	algorithm, hexSum, ok := strings.Cut(raw, ":")
	if !ok || !strings.EqualFold(algorithm, "sha256") {
		return ""
	}
	hexSum = strings.ToLower(strings.TrimSpace(hexSum))
	if len(hexSum) != sha256HexLen {
		return ""
	}
	if _, err := hex.DecodeString(hexSum); err != nil {
		return ""
	}
	return hexSum
}

// normalizeMD5 归一化 GitHub 的 md5 字段（32 位十六进制）。仅在 sha256 缺失时兜底使用：
// 摘要强弱的取舍见 downloadFPK —— 弱摘要只能证明「没被换过」，不能证明「没被构造过」。
func normalizeMD5(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if len(raw) != 32 {
		return ""
	}
	if _, err := hex.DecodeString(raw); err != nil {
		return ""
	}
	return raw
}

// updateService 更新检测/下载状态（进程内单例，含检查结果缓存）
type updateService struct {
	mu       sync.Mutex
	updating bool
	failed   bool
	progress int
	message  string
	latest   string
	fpkName  string
	cached   gin.H
	cachedAt time.Time
}

func newUpdateService() *updateService { return &updateService{} }

// updateHandler fnOS 应用更新接口（仅在 fnOS 平台注册）
type updateHandler struct {
	svc *updateService
	// dir 服务数据目录（更新包落盘位置的根）
	dir string
}

func newUpdateHandler(dataDir string) *updateHandler {
	if strings.TrimSpace(dataDir) == "" {
		// 未配置数据目录时退回用户主目录（与默认数据目录同址），
		// 绝不回落系统临时目录（见 fpkPath 注释）
		dataDir = "."
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			dataDir = filepath.Join(home, ".seedark")
		}
	}
	return &updateHandler{svc: newUpdateService(), dir: dataDir}
}

// RegisterRoutes 挂载更新相关路由
func (u *updateHandler) RegisterRoutes(g *gin.RouterGroup) {
	g.GET("/update/check", u.check)
	g.POST("/update/install", u.install)
	g.GET("/update/status", u.status)
	g.GET("/update/download", u.download)
}

// check GET /api/update/check 检查是否有新版本（结果缓存 5 分钟）
func (u *updateHandler) check(c *gin.Context) {
	svc := u.svc
	svc.mu.Lock()
	if svc.cached != nil && time.Since(svc.cachedAt) < updateCheckTTL {
		result := svc.cached
		svc.mu.Unlock()
		respond(c, result)
		return
	}
	svc.mu.Unlock()

	info, err := fetchLatestRelease()
	if err != nil {
		slog.Warn("检查更新失败", "err", err)
		respondError(c, http.StatusInternalServerError, "检查更新失败: "+err.Error())
		return
	}
	cur := currentAppVersion()
	hasUpdate := compareAppVersion(cur, info.Version) > 0
	if hasUpdate && info.FPKURL == "" {
		// 新版本没有当前架构的更新包时不提示更新，避免用户点了却无法安装
		hasUpdate = false
	}
	result := gin.H{
		"currentVersion": cur,
		"latestVersion":  info.Version,
		"hasUpdate":      hasUpdate,
		"changelog":      info.Changelog,
		"publishedAt":    info.PublishedAt,
		"releaseUrl":     info.ReleaseURL,
		"fpkUrl":         info.FPKURL,
		"fpkSize":        info.FPKSize,
		"arch":           appArch(),
		"downloadReady":  u.fpkReady(info.FPKSize),
	}
	svc.mu.Lock()
	svc.cached, svc.cachedAt = result, time.Now()
	svc.mu.Unlock()
	respond(c, result)
}

// install POST /api/update/install 后台下载当前架构的 fpk 更新包
func (u *updateHandler) install(c *gin.Context) {
	svc := u.svc
	// 检查与置位必须在同一临界区：否则两个并发 install 都能在置位前通过检查，
	// 各自 performUpdate 并发写同一个 .part 文件，导致更新包交错损坏
	svc.mu.Lock()
	if svc.updating {
		svc.mu.Unlock()
		respondError(c, http.StatusConflict, "正在下载更新包，请稍候")
		return
	}
	svc.updating, svc.failed, svc.progress = true, false, 0
	svc.message = "正在检查新版本..."
	svc.mu.Unlock()

	// 网络请求/校验失败时必须释放占位，否则 updating 卡死为 true，后续更新永久 409
	release := func() {
		svc.mu.Lock()
		svc.updating = false
		svc.mu.Unlock()
	}

	info, err := fetchLatestRelease()
	if err != nil {
		release()
		respondError(c, http.StatusInternalServerError, "获取版本信息失败: "+err.Error())
		return
	}
	if info.FPKURL == "" {
		release()
		respondError(c, http.StatusBadRequest, "未找到当前架构的更新包")
		return
	}
	if m := reFPKVersion.FindStringSubmatch(info.FPKName); m != nil && m[1] != info.Version {
		release()
		respondError(c, http.StatusInternalServerError,
			fmt.Sprintf("版本信息不一致: API 返回 %s，更新包指向 %s", info.Version, m[1]))
		return
	}

	svc.mu.Lock()
	svc.message = "正在准备更新..."
	svc.latest, svc.fpkName = info.Version, info.FPKName
	svc.mu.Unlock()

	go u.performUpdate(info)
	respond(c, gin.H{"message": "开始下载更新包"})
}

// status GET /api/update/status 返回下载进度
func (u *updateHandler) status(c *gin.Context) {
	svc := u.svc
	svc.mu.Lock()
	result := gin.H{
		"updating":      svc.updating,
		"failed":        svc.failed,
		"progress":      svc.progress,
		"message":       svc.message,
		"latestVersion": svc.latest,
		"fpkFilename":   svc.fpkName,
	}
	svc.mu.Unlock()
	if !result["updating"].(bool) && result["progress"].(int) >= 100 && fileExists(u.fpkPath()) {
		result["downloadUrl"] = "/api/update/download"
	}
	respond(c, result)
}

// download GET /api/update/download 下发已下载的 fpk（供应用中心手动安装）
func (u *updateHandler) download(c *gin.Context) {
	path := u.fpkPath()
	if !fileExists(path) {
		respondError(c, http.StatusNotFound, "更新包不存在，请先点击一键更新")
		return
	}
	u.svc.mu.Lock()
	name := u.svc.fpkName
	u.svc.mu.Unlock()
	if name == "" {
		name = filepath.Base(path)
	}
	c.FileAttachment(path, name)
}

// downloadSource 更新包的一个下载来源
type downloadSource struct {
	url     string
	direct  bool // 直连 GitHub：元数据与文件同源，摘要缺失时才允许作为可信来源
	msg     string
	display string
}

// downloadSources 按尝试顺序给出下载来源：GitHub 直连在前，加速镜像按 UpdateMirrors 顺序兜底。
// 直连不经第三方中转，且文件与 api.github.com 的元数据同源；直连被墙时由
// directProbeTimeout 限时，超时即整体切到加速源，不会让用户白等整轮。
func downloadSources(fpURL string) []downloadSource {
	out := make([]downloadSource, 0, len(UpdateMirrors)+1)
	out = append(out, downloadSource{
		url:     fpURL,
		direct:  true,
		msg:     "正在从 GitHub 直连下载...",
		display: "github.com",
	})
	for i, m := range UpdateMirrors {
		msg := "直连未成功，切换加速源..."
		if i > 0 {
			msg = fmt.Sprintf("加速源切换中...（%d/%d）", i+1, len(UpdateMirrors))
		}
		out = append(out, downloadSource{url: m + fpURL, msg: msg, display: strings.TrimSuffix(strings.TrimPrefix(m, "https://"), "/")})
	}
	return out
}

// performUpdate 下载 fpk（多镜像 Range 分片并发，必要时回退单流），落盘并校验。
// 无论成功失败都会释放 updating 标志、清理 .part，保证用户可立刻重试。
func (u *updateHandler) performUpdate(info *releaseInfo) {
	svc := u.svc
	set := func(progress int, message string) {
		svc.mu.Lock()
		svc.progress, svc.message = progress, message
		svc.mu.Unlock()
	}
	fail := func(msg string) {
		svc.mu.Lock()
		svc.failed, svc.progress = true, 0
		svc.message = "更新失败: " + msg + failureNextStep(info)
		svc.mu.Unlock()
	}
	defer func() {
		svc.mu.Lock()
		svc.updating = false
		svc.mu.Unlock()
	}()

	dest := u.fpkPath()
	// 0700：更新包目录仅服务自身可进，杜绝同机用户预置软链/替换文件
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		fail(err.Error())
		return
	}
	sources := downloadSources(info.FPKURL)
	// 首个来源即直连 GitHub；分片会在探测成功的来源上轮转，直连卡住时由
	// directStallLimit 限时改派加速源。
	primary := sources[0]

	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()

	set(10, primary.msg)
	progress := func(written, total int64, doneChunks, totalChunks int, label string) {
		if label == "" {
			label = primary.display
		}
		if totalChunks > 0 {
			pct := 10
			if total > 0 {
				pct = 10 + int(written*50/total)
			}
			if pct > 60 {
				pct = 60
			}
			set(pct, fmt.Sprintf("正在从 %s 下载 · %d/%d 分片 %.1fMB/%.1fMB",
				label, doneChunks, totalChunks, float64(written)/(1<<20), float64(total)/(1<<20)))
			return
		}
		pct := 10
		if total > 0 {
			pct = 10 + int(written*50/total)
		}
		if pct > 60 {
			pct = 60
		}
		set(pct, fmt.Sprintf("正在从 %s 下载... %.1fMB/%.1fMB",
			label, float64(written)/(1<<20), float64(total)/(1<<20)))
	}

	set(15, "正在校验更新包...")
	if err := fetchFPK(ctx, info.FPKURL, dest, false, info, progress); err != nil {
		slog.Warn("更新包下载失败", "url", info.FPKURL, "err", err)
		// 兜底：直连与分片链路都失败时，再纯粹走一次直连顺序下载
		// （直连文件与元数据同源，上游无摘要时仍属可信来源）
		if ctx.Err() == nil {
			set(20, "加速源均失败，再试一次直连 GitHub...")
			if derr := fetchFPK(ctx, info.FPKURL, dest, true, info, progress); derr == nil {
				set(100, "下载完成")
				return
			} else {
				slog.Warn("更新包直连下载失败", "err", derr)
				err = fmt.Errorf("%v；直连也失败: %v", err, derr)
			}
		}
		fail(err.Error())
		return
	}
	set(100, "下载完成")
}

// failureNextStep 失败信息里的可操作下一步：给出发布页让用户手动下载 fpk 安装，
// 而不是只回一句「更新失败」把用户卡在界面上。
func failureNextStep(info *releaseInfo) string {
	if info == nil {
		return ""
	}
	url := info.ReleaseURL
	if url == "" {
		url = "https://github.com/" + updateRepo + "/releases/latest"
	}
	return fmt.Sprintf("。可前往发布页手动下载 fpk 后，在 应用中心 → 手动安装 上传：%s", url)
}

// downloadFPK 下载单个更新包（旧接口，保留给外部调用/测试）：
// 全部走 fetchFPK，镜像列表由 UpdateMirrors（或打包侧清单）决定。
func downloadFPK(rawURL, dest string, direct bool, info *releaseInfo, set func(int, string)) error {
	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()
	var lastPct int
	progress := func(written, total int64, doneChunks, totalChunks int, label string) {
		if total <= 0 {
			return
		}
		pct := 10 + int(written*50/total)
		if pct > 60 {
			pct = 60
		}
		if pct == lastPct {
			return
		}
		lastPct = pct
		if totalChunks > 0 {
			set(pct, fmt.Sprintf("正在从 %s 下载 · %d/%d 分片", label, doneChunks, totalChunks))
			return
		}
		set(pct, fmt.Sprintf("正在从 %s 下载... %.1fMB/%.1fMB", label, float64(written)/(1<<20), float64(total)/(1<<20)))
	}
	return fetchFPK(ctx, rawURL, dest, direct, info, progress)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// fpkReady 本地已下载的更新包是否与目标版本大小一致
func (u *updateHandler) fpkReady(expectedSize int64) bool {
	st, err := os.Stat(u.fpkPath())
	if err != nil || st.IsDir() {
		return false
	}
	return expectedSize <= 0 || st.Size() == expectedSize
}

// respond / respondError 复用统一响应格式（平台包不依赖 api 包，避免循环引用）
func respond(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, models.OK(data))
}

func respondError(c *gin.Context, status int, msg string) {
	c.JSON(status, models.Error(msg))
}
