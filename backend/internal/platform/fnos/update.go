package fnos

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
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

// fnOS 应用更新：GitHub Release 检测最新版 + gh-proxy 回退下载 fpk 包
const (
	updateRepo        = "sushazhi/fnos-transmission"
	updateAPIBase     = "https://api.github.com"
	updateProxyMain   = "https://gh-proxy.com/"
	updateProxyBackup = "https://gh-proxy.org/"
	updateCheckTTL    = 5 * time.Minute
	fpkMaxSize        = 100 << 20 // 100 MB
	downloadTimeout   = 3 * time.Minute
	defaultAppVersion = "1.0.0"
	sha256HexLen      = 64 // SHA-256 的十六进制表示长度
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
		// Digest 由 GitHub 侧计算（形如 "sha256:<hex>"），是走代理下载时唯一的完整性依据
		Digest string `json:"digest"`
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
	var fpkURL, fpkName, fpkDigest string
	var fpkSize int64
	for _, a := range rel.Assets {
		if strings.HasSuffix(a.Name, archSuffix) && strings.Contains(strings.ToLower(a.Name), "transmission") {
			fpkURL, fpkName, fpkSize, fpkDigest = a.BrowserDownloadURL, a.Name, a.Size, normalizeDigest(a.Digest)
			break
		}
	}
	if fpkURL == "" {
		for _, a := range rel.Assets {
			if strings.HasSuffix(a.Name, ".fpk") {
				fpkURL, fpkName, fpkSize, fpkDigest = a.BrowserDownloadURL, a.Name, a.Size, normalizeDigest(a.Digest)
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

// performUpdate 依次走主代理/备用代理/直连下载 fpk，落盘并校验
func (u *updateHandler) performUpdate(info *releaseInfo) {
	svc := u.svc
	set := func(progress int, message string) {
		svc.mu.Lock()
		svc.progress, svc.message = progress, message
		svc.mu.Unlock()
	}
	fail := func(msg string) {
		svc.mu.Lock()
		svc.failed, svc.progress, svc.message = true, 0, "更新失败: "+msg
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
	type source struct {
		url    string
		direct bool // 直连 GitHub：元数据与文件同源，摘要缺失时才允许作为可信来源
		msg    string
	}
	sources := []source{
		{updateProxyMain + info.FPKURL, false, "正在下载更新包..."},
		{updateProxyBackup + info.FPKURL, false, "主代理下载失败，切换备用代理..."},
		{info.FPKURL, true, "备用代理下载失败，尝试直连..."},
	}
	lastErr := ""
	for _, s := range sources {
		set(10, s.msg)
		if err := downloadFPK(s.url, dest, s.direct, info, set); err != nil {
			lastErr = err.Error()
			slog.Warn("更新包下载失败", "url", s.url, "err", err)
			continue
		}
		set(100, "下载完成")
		return
	}
	fail(lastErr)
}

// downloadFPK 单个 URL 的下载 + 校验（格式魔数 / 大小 / 摘要），失败时清理临时文件。
// direct 表示本轮是 GitHub 直连下载（仅影响「上游未提供摘要」时的取舍）。
func downloadFPK(url, dest string, direct bool, info *releaseInfo, set func(int, string)) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	client := &http.Client{
		Timeout:   downloadTimeout,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}},
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("网络错误: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("服务器返回 HTTP %d", resp.StatusCode)
	}

	// 先清掉上次失败留下的残件，再以 O_EXCL 创建：即便有人预置了同名软链，
	// Remove 只删链接本身，O_EXCL 也不会跟随链接写到目标文件上
	tmp := dest + ".part"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	fail := func(format string, args ...interface{}) error {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf(format, args...)
	}

	buf := make([]byte, 64*1024)
	hasher := sha256.New()
	var downloaded int64
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return fail("写入失败: %v", werr)
			}
			_, _ = hasher.Write(buf[:n])
			downloaded += int64(n)
			if downloaded > fpkMaxSize {
				return fail("下载内容超过大小限制 (%d MB)", fpkMaxSize>>20)
			}
			if resp.ContentLength > 0 {
				progress := 10 + int(downloaded*50/resp.ContentLength)
				set(progress, fmt.Sprintf("正在下载... %.1fMB/%.1fMB",
					float64(downloaded)/float64(1<<20), float64(resp.ContentLength)/float64(1<<20)))
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fail("下载中断: %v", rerr)
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if downloaded == 0 {
		_ = os.Remove(tmp)
		return fmt.Errorf("下载文件为空")
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
	if info.FPKSize > 0 && downloaded != info.FPKSize {
		_ = os.Remove(tmp)
		return fmt.Errorf("文件大小不匹配: 期望 %d 字节, 实际 %d 字节", info.FPKSize, downloaded)
	}
	// 完整性：下载链路经过第三方代理，大小与魔数都可伪造，只有 GitHub 侧算好的摘要
	// （随 api.github.com 元数据直连取得）能证明这个包没被替换过
	got := hex.EncodeToString(hasher.Sum(nil))
	if info.FPKDigest == "" {
		// 无摘要可校验时，代理链路一律拒绝——否则「校验」形同虚设。
		// GitHub 直连下载时文件与元数据同源，才降级为仅告警。
		if !direct {
			_ = os.Remove(tmp)
			return fmt.Errorf("发布资源未提供 SHA-256 摘要，无法校验经代理下载的更新包")
		}
		slog.Warn("发布资源未提供 SHA-256 摘要，本次仅校验了大小与魔数", "fpk", info.FPKName, "sha256", got)
	} else if !strings.EqualFold(got, info.FPKDigest) {
		_ = os.Remove(tmp)
		return fmt.Errorf("文件校验失败: SHA-256 与发布版本不一致（下载链路可能被替换）")
	}
	return os.Rename(tmp, dest)
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
