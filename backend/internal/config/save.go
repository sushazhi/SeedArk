package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// localConfigName 界面保存的连接配置文件名（优先级高于 .env 与 config.yaml）
const localConfigName = ".env.local"

// ValidateEnvValue 校验会写入 .env.local 的字段值。
// dotenv 按行解析，而 .env.local 的优先级高于 config.yaml 与进程环境，
// 值里带换行就会被解析成一个新键 —— 例如密码填 "x\nAPI_TOKEN=y" 可直接注入鉴权令牌、
// SERVER_HOST（改成全网卡监听）或 TORRENT_PATH_ROOTS（放开文件读取白名单）。
func ValidateEnvValue(name, value string) error {
	if strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("%s 不能包含换行或空字符", name)
	}
	return nil
}

// LocalSettings 界面保存的全部受管配置。
// .env.local 采用整文件重写：新增受管键必须纳入此处，否则会在下一次保存时被抹掉
type LocalSettings struct {
	Type            string // 下载器类型：transmission | qbittorrent；空 = 不写入
	TransmissionURL string
	User            string
	Pass            string
	PollInterval    string // 空 = 不写入，沿用 config.yaml / 环境变量
	MCPEnabled      bool
	MCPAllowDelete  bool
	// MCPAllowDangerous 允许通过 MCP 执行移动 / 重命名 / 立即执行做种策略等其它高危操作
	MCPAllowDangerous bool
	MCPToken          string // 空 = 显式关闭令牌鉴权（写入空值行，避免被 config.yaml 复活）
	MCPPort           string // 空 = 显式关闭直连端口（写入空值行，避免被 config.yaml 复活）；修改需重启生效
}

// SaveLocalSettings 将界面配置保存到数据目录（默认 ~/.seedark）。
// 连接与 MCP 设置共用此文件，任何入口保存都必须携带全部受管键的当前生效值
func SaveLocalSettings(dataDir string, s LocalSettings) error {
	values := []struct {
		key, value string
		always     bool // always：空值也要写入显式覆盖行，防止低优先级来源的旧值在重启后复活
	}{
		{"SA_TYPE", s.Type, true},
		{"SA_URL", s.TransmissionURL, true},
		{"SA_USER", s.User, true},
		{"SA_PASS", s.Pass, true},
		{"POLL_INTERVAL", s.PollInterval, false},
		{"MCP_TOKEN", s.MCPToken, true},
		{"MCP_PORT", s.MCPPort, true},
		{"MCP_ENABLED", strconv.FormatBool(s.MCPEnabled), true},
		{"MCP_ALLOW_DELETE", strconv.FormatBool(s.MCPAllowDelete), true},
		{"MCP_ALLOW_DANGEROUS", strconv.FormatBool(s.MCPAllowDangerous), true},
	}
	var content strings.Builder
	for _, f := range values {
		if err := ValidateEnvValue(f.key, f.value); err != nil {
			return err
		}
		if f.value == "" && !f.always {
			continue
		}
		content.WriteString(f.key)
		content.WriteString("=")
		content.WriteString(f.value)
		content.WriteString("\n")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("创建数据目录失败: %w", err)
	}
	// 原子写入 + 0600：文件含下载器密码，且进程中断不能留下半截文件
	// （半截文件会让下次启动的配置解析失败，界面也读不回原值）
	path := filepath.Join(dataDir, localConfigName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content.String()), 0o600); err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("保存配置失败: %w", err)
	}
	return nil
}
