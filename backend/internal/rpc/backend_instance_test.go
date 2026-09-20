package rpc

import (
	"testing"

	"github.com/trpanel/backend/internal/qbittorrent"
)

// TestActiveBackendInstanceMismatch 记录一个真实缺陷：
//
// m.client（活动后端）与 m.members[idx].backend（聚合成员）是各自独立构造的
// 两个实例。qBittorrent 驱动的面板 ID 是与实例绑定的内存映射
// （qbittorrent.Client.idToHash，见 HashFor），两个实例各有一份。
//
// 后果：聚合列表里「活动服务器」的种子 ID 不编码（保持原始值），详情按
// DecodeID 得到 idx=-1 后由 BackendFor 路由到 m.client，而列表是 members 实例
// 拉出来的——用 A 实例分配的 ID 去 B 实例的映射表里查，查不到就报「种子不存在」。
//
// 非活动服务器不受影响：它们的 ID 编码后由 BackendFor 精确路由到自己的实例。
//
// 本测试用同一份 hash 在两个实例上验证映射表确实各存一份。
func TestActiveBackendInstanceMismatch(t *testing.T) {
	const (
		url  = "http://127.0.0.1:8080"
		hash = "0123456789abcdef0123456789abcdef01234567"
	)
	// 模拟 m.client：活动后端实例
	active, err := qbittorrent.New(url, "", "")
	if err != nil {
		t.Fatalf("创建活动后端失败: %v", err)
	}
	// 模拟 m.members[0].backend：聚合成员实例（另一次构造）
	member, err := qbittorrent.New(url, "", "")
	if err != nil {
		t.Fatalf("创建成员后端失败: %v", err)
	}
	if active == member {
		t.Fatal("前提不成立：两次构造应得到不同实例")
	}

	id := member.IDFor(hash) // 列表由成员实例产出，ID 记在成员实例的映射表里
	if id == 0 {
		t.Fatal("hash 应能分配到非零 ID")
	}
	// 活动实例从未见过这个 hash
	if got := active.HashFor(id); got != "" {
		t.Fatalf("预期活动实例查不到该 ID（两实例映射表独立），却得到 %q", got)
	}
	// 成员实例自己能查到——这正是当前聚合详情走错实例时会失败的证据
	if got := member.HashFor(id); got != hash {
		t.Fatalf("成员实例应能由 ID 反查回 hash，得到 %q", got)
	}
}
