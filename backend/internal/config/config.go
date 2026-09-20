package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config 服务配置
type Config struct {
	TransmissionType  string        // 下载器类型：transmission（默认）| qbittorrent
	TransmissionURL   string        // 下载器 RPC 端点（qBittorrent 填 WebUI 根地址）
	User              string        // RPC 用户名
	Password          string        // RPC 密码
	Host              string        // 本服务监听地址（默认仅回环，避免局域网裸奔）
	Port              string        // 本服务监听端口
	APIToken          string        // 接口访问令牌；为空表示不启用鉴权
	Platform          string        // 宿主平台：generic（默认）| fnos，留空时自动推断
	GatewayPrefix     string        // 宿主网关挂载的 URL 前缀（如 /app/transmission）
	TorrentPathRoots  []string      // 允许按路径读取 .torrent 的根目录（NAS 文件选择器）
	PathMappings      []string      // 远端→本地路径映射（"远端路径=本地路径"），用于打开目录/复制路径的展示转换
	PollInterval      time.Duration // WebSocket 轮询间隔
	LogLevel          string        // 日志级别 debug/info/warn/error
	DataDir           string        // 数据目录（状态文件存放位置）
	MCPEnabled        bool          // 启用 MCP 服务（/mcp 端点，供 AI 客户端接入）
	MCPAllowDelete    bool          // 允许通过 MCP 删除种子（高危操作，默认关闭）
	MCPAllowDangerous bool          // 允许通过 MCP 执行其它高危操作（移动/重命名/执行做种策略，默认关闭）
	MCPToken          string        // MCP 接入令牌；空表示不启用（仅建议回环 / 内网使用）
	MCPPort           string        // MCP 专用直连端口；空表示不开启（socket 部署下供 AI 客户端绕过网关直连）
}

// Load 加载配置，优先级：环境变量 > .env.local > .env > config.yaml > 默认值
func Load() (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("$HOME/.seedark")

	// 默认值
	v.SetDefault("sa_type", "transmission")
	v.SetDefault("sa_url", "http://localhost:9091/transmission/rpc")
	v.SetDefault("sa_user", "")
	v.SetDefault("sa_pass", "")
	v.SetDefault("server_host", "127.0.0.1")
	v.SetDefault("server_port", 8200)
	v.SetDefault("api_token", "")
	// platform 留空时由 platform.Detect 按 GATEWAY_PREFIX / 宿主环境变量推断
	v.SetDefault("platform", "")
	v.SetDefault("gateway_prefix", "")
	// NAS 文件选择器返回的路径仅允许落在这些根目录下（逗号分隔，可用 TORRENT_PATH_ROOTS 覆盖）
	v.SetDefault("torrent_path_roots", []string{"/vol", "/mnt", "/media", "/volume1"})
	v.SetDefault("path_mappings", []string{})
	v.SetDefault("poll_interval", "2s")
	v.SetDefault("log_level", "info")
	v.SetDefault("data_dir", defaultDataDir())
	v.SetDefault("mcp_enabled", false)
	v.SetDefault("mcp_allow_delete", false)
	v.SetDefault("mcp_allow_dangerous", false)
	v.SetDefault("mcp_token", "")
	v.SetDefault("mcp_port", "")

	// 配置文件（可选）
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("读取配置文件失败: %w", err)
		}
	}

	// 数据目录（SA_DATA_DIR > config.yaml > 默认家目录），用于定位 .env/.env.local
	dataDir := expandPath(v.GetString("data_dir"))
	if envDir := os.Getenv("SA_DATA_DIR"); envDir != "" {
		dataDir = expandPath(envDir)
	}

	// .env / .env.local 文件（可选，后者优先级更高，界面保存的连接配置写入数据目录；
	// 兼容旧版本：当前工作目录的同名文件也读取，后加载的覆盖先加载的）
	envCandidates := []string{".env", ".env.local",
		filepath.Join(dataDir, ".env"), filepath.Join(dataDir, ".env.local")}
	for _, envName := range envCandidates {
		if _, err := os.Stat(envName); err == nil {
			envFile := viper.New()
			envFile.SetConfigFile(envName)
			envFile.SetConfigType("env")
			if err := envFile.ReadInConfig(); err == nil {
				for _, k := range envFile.AllKeys() {
					v.Set(k, envFile.Get(k))
				}
			}
		}
	}

	// 环境变量覆盖（最高优先级）
	envKeys := map[string]string{
		"sa_type":             "SA_TYPE",
		"sa_url":              "SA_URL",
		"sa_user":             "SA_USER",
		"sa_pass":             "SA_PASS",
		"server_host":         "SERVER_HOST",
		"server_port":         "SERVER_PORT",
		"api_token":           "API_TOKEN",
		"platform":            "SA_PLATFORM",
		"gateway_prefix":      "GATEWAY_PREFIX",
		"poll_interval":       "POLL_INTERVAL",
		"log_level":           "LOG_LEVEL",
		"data_dir":            "SA_DATA_DIR",
		"mcp_enabled":         "MCP_ENABLED",
		"mcp_allow_delete":    "MCP_ALLOW_DELETE",
		"mcp_allow_dangerous": "MCP_ALLOW_DANGEROUS",
		"mcp_token":           "MCP_TOKEN",
		"mcp_port":            "MCP_PORT",
	}
	for key, env := range envKeys {
		if val, ok := os.LookupEnv(env); ok {
			v.Set(key, val)
		}
	}

	dur, err := time.ParseDuration(v.GetString("poll_interval"))
	if err != nil {
		return nil, fmt.Errorf("poll_interval 格式无效: %w", err)
	}

	roots := expandList(v.GetStringSlice("torrent_path_roots"))
	if raw, ok := os.LookupEnv("TORRENT_PATH_ROOTS"); ok {
		roots = expandList(strings.Split(raw, ","))
	}

	// 路径映射：config.yaml 列表或 PATH_MAPPINGS 环境变量（逗号分隔，每项 远端=本地）
	pathMappings := expandList(v.GetStringSlice("path_mappings"))
	if raw, ok := os.LookupEnv("PATH_MAPPINGS"); ok {
		pathMappings = expandList(strings.Split(raw, ","))
	}

	return &Config{
		TransmissionType:  strings.TrimSpace(v.GetString("sa_type")),
		TransmissionURL:   v.GetString("sa_url"),
		User:              v.GetString("sa_user"),
		Password:          v.GetString("sa_pass"),
		Host:              v.GetString("server_host"),
		Port:              fmt.Sprintf("%d", v.GetInt("server_port")),
		APIToken:          v.GetString("api_token"),
		Platform:          strings.TrimSpace(v.GetString("platform")),
		GatewayPrefix:     strings.TrimSpace(v.GetString("gateway_prefix")),
		TorrentPathRoots:  roots,
		PathMappings:      pathMappings,
		PollInterval:      dur,
		LogLevel:          v.GetString("log_level"),
		DataDir:           dataDir,
		MCPEnabled:        v.GetBool("mcp_enabled"),
		MCPAllowDelete:    v.GetBool("mcp_allow_delete"),
		MCPAllowDangerous: v.GetBool("mcp_allow_dangerous"),
		MCPToken:          strings.TrimSpace(v.GetString("mcp_token")),
		MCPPort:           strings.TrimSpace(v.GetString("mcp_port")),
	}, nil
}

// expandList 清理并展开路径列表中的 ~ 与环境变量，丢弃空项
func expandList(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if p := strings.TrimSpace(item); p != "" {
			out = append(out, expandPath(p))
		}
	}
	return out
}

// defaultDataDir 默认数据目录为 ~/.seedark（与 config.yaml 搜索路径一致），
// 避免运行时生成的状态/配置文件落在代码或部署目录
func defaultDataDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".seedark")
	}
	return "."
}

// expandPath 展开路径中的 ~ 与 $HOME 等环境变量
func expandPath(p string) string {
	if p == "" {
		return p
	}
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return os.ExpandEnv(p)
}
