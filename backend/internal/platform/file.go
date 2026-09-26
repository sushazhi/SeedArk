package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// fileAccess 「按路径添加种子」「后端建种」的读取策略实现
type fileAccess struct {
	enabled  bool
	prefixes []string
}

// NewFileAccess 构造文件访问策略。
// enabled=false 时一律拒绝；prefixes 非空时仅允许这些前缀下的文件（含前缀自身）。
func NewFileAccess(enabled bool, prefixes []string) FileAccess {
	return &fileAccess{enabled: enabled, prefixes: prefixes}
}

func (f *fileAccess) Enabled() bool { return f.enabled }

// AllowRead 校验并解析「按路径添加种子」的目标，返回解析过符号链接后的真实路径。
// 调用方必须打开返回的路径而不是原始入参：否则校验的是一个路径、读取的是另一个路径。
func (f *fileAccess) AllowRead(path string) (string, error) {
	resolved, fi, err := f.resolve(path)
	if err != nil {
		return "", err
	}
	// 只允许普通文件：设备文件（/dev/...）与 fifo 都不应成为读取目标
	if !fi.Mode().IsRegular() {
		return "", errors.New("种子路径必须是普通文件")
	}
	return resolved, nil
}

// AllowReadDir 校验并解析「后端建种」的目录目标，返回解析过符号链接后的真实路径。
// 仅允许普通目录；目录内的具体文件由调用方逐个再过 AllowRead，防止软链逃逸白名单。
func (f *fileAccess) AllowReadDir(path string) (string, error) {
	resolved, fi, err := f.resolve(path)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", errors.New("路径必须是目录")
	}
	return resolved, nil
}

// ErrFileTooLarge 目标文件超出调用方给定的大小上限
var ErrFileTooLarge = errors.New("文件超过大小上限")

// MaxTorrentFileSize 「按路径添加种子」的 .torrent 体积上限（10MB）。
// 与 REST 上传分支共用同一个值：同一类文件走哪个入口不该有不同上限
const MaxTorrentFileSize = 10 << 20

// ReadFileCapped 走白名单校验后读取文件，并限制体积。
// 「按路径添加种子」的 REST 与 MCP 两个入口共用，避免白名单与体积两份口径各自演化；
// 上限的必要性：路径由宿主文件选择器给出，选错一个大文件会整份读进内存
// （HTTP 请求体上限管不到从磁盘读的文件）。
func ReadFileCapped(fa FileAccess, path string, maxSize int64) ([]byte, error) {
	target, err := fa.AllowRead(path)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if fi.Size() > maxSize {
		return nil, ErrFileTooLarge
	}
	return os.ReadFile(target)
}

// resolve 公共校验：启用开关、非法字符、路径穿越、符号链接解析、白名单比对
func (f *fileAccess) resolve(path string) (string, os.FileInfo, error) {
	if !f.enabled {
		return "", nil, errors.New("当前部署未启用「按路径读取文件」，请改用文件上传或链接")
	}
	if strings.TrimSpace(path) == "" {
		return "", nil, errors.New("路径为空")
	}
	if strings.ContainsRune(path, 0) {
		return "", nil, errors.New("路径含非法字符")
	}
	clean := filepath.Clean(path)
	// 路径穿越：Clean 之后仍以 .. 开头（如 ../../etc/passwd）
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", nil, errors.New("路径不允许越权访问上级目录")
	}
	// Clean 不解析符号链接：允许目录下放一枚指向 /etc 的软链即可绕过下面的前缀比对，
	// 因此必须先拿到真实路径再判定，并且后续只读这个真实路径
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		// 解析不了就按最小权限拒绝。注意 Windows 目录联接（junction）会出现 stat 成功而
		// 解析失败的情形；Linux（fnOS / Docker 部署）下软链可正常解析，走后面的真实路径比对。
		return "", nil, fmt.Errorf("路径的符号链接无法解析: %w", err)
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		return "", nil, fmt.Errorf("路径不可用: %w", err)
	}
	if len(f.prefixes) == 0 {
		return resolved, fi, nil
	}
	for _, prefix := range f.prefixes {
		// 前缀自身也可能是软链接（宿主常见 /vol -> /mnt/vol），两侧都按真实路径比对
		p, err := filepath.EvalSymlinks(filepath.Clean(prefix))
		if err != nil {
			continue
		}
		if resolved == p || strings.HasPrefix(resolved, p+string(filepath.Separator)) {
			return resolved, fi, nil
		}
	}
	return "", nil, fmt.Errorf("路径不在允许范围内（仅允许：%s）", strings.Join(f.prefixes, "、"))
}
