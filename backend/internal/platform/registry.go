package platform

import (
	"os"
	"sort"
	"strings"
	"sync"
)

// Factory 平台构造函数
type Factory func(cfg Config) Platform

var (
	regMu     sync.RWMutex
	factories = map[string]Factory{}
)

// Register 注册一个宿主平台实现（通常在子包的 init 中调用）。
// 重复注册同一标识时后注册者生效。
func Register(id string, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	factories[id] = f
}

// Registered 已注册的平台标识（排序后返回，便于日志输出与排错）
func Registered() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(factories))
	for id := range factories {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Resolve 按标识解析平台；未注册时回退到通用实现，保证服务始终可启动
func Resolve(id string, cfg Config) Platform {
	regMu.RLock()
	f, ok := factories[id]
	regMu.RUnlock()
	if ok {
		return f(cfg)
	}
	return NewGeneric(cfg)
}

// Detect 推断当前宿主平台，优先级：
//  1. 显式指定（服务配置的 platform 字段，或 PLATFORM / SA_PLATFORM 环境变量）
//  2. 宿主注入的运行时环境变量（fnOS 应用提供 TRIM_APPDEST）
//  3. 配置了网关前缀（由宿主网关挂载到子路径）
//  4. 以上都不满足 → 通用部署
func Detect(id string, cfg Config) Platform {
	if id == "" {
		id = os.Getenv("PLATFORM")
	}
	if id != "" {
		return Resolve(strings.ToLower(strings.TrimSpace(id)), cfg)
	}
	if os.Getenv("TRIM_APPDEST") != "" {
		return Resolve(IDFnOS, cfg)
	}
	if cfg.GatewayPrefix != "" {
		return Resolve(IDFnOS, cfg)
	}
	return NewGeneric(cfg)
}
