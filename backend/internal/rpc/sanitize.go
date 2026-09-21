package rpc

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

// userinfoRe 匹配带凭据的 URL userinfo（形如 http://user:pass@host）。
var userinfoRe = regexp.MustCompile(`(?i)(https?://)[^\s/"?#]*@`)

// minSecretLen 过短的敏感值全局替换会误伤正常文本（如 1~2 字符密码），不做登记
const minSecretLen = 3

// 运行时登记的敏感值（密码等）。底层错误文本未必以 URL 形式带出密码
// （如拼进了自定义错误、请求体摘要等），只有按值精确替换才能兜底。
var (
	secretMu     sync.RWMutex
	knownSecrets []string // 按长度降序，避免长密码被短密码先替换掉一部分
)

// RegisterSecret 登记需要在错误文本中抹除的敏感值（密码）。
// 由后端构造入口调用；空值、过短值忽略，重复值不重复登记。
func RegisterSecret(values ...string) {
	secretMu.Lock()
	defer secretMu.Unlock()
	for _, v := range values {
		if len(v) < minSecretLen || v == "***" {
			continue
		}
		dup := false
		for _, s := range knownSecrets {
			if s == v {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		knownSecrets = append(knownSecrets, v)
	}
	sort.Slice(knownSecrets, func(i, j int) bool { return len(knownSecrets[i]) > len(knownSecrets[j]) })
}

// SanitizeClientMsg 收敛回传给客户端的错误文本。
// RPC 客户端用 URL userinfo 承载认证，底层 *url.Error 会把完整 URL 打进错误字符串，
// 而 REST/MCP 各接口习惯把 err.Error() 原样回传——不抹掉就等于把下载器密码发给调用方。
func SanitizeClientMsg(msg string) string {
	// 先按登记值替换：密码可能以非 URL 形式出现在错误文本里；
	// 再按 URL userinfo 模式兜底（覆盖单次探测等未登记场景）。
	secretMu.RLock()
	for _, s := range knownSecrets {
		msg = strings.ReplaceAll(msg, s, "***")
	}
	secretMu.RUnlock()
	msg = userinfoRe.ReplaceAllString(msg, "$1***@")
	if r := []rune(msg); len(r) > 512 {
		msg = string(r[:512]) + "…"
	}
	return msg
}
