package rpc

import (
	"testing"

	"github.com/sushazhi/seedark/backend/internal/models"
)

// TestEncodeDecodeID 聚合 ID 编解码必须可逆，且不能与「未编码的活动服务器 ID」混淆。
// tr 与 qb 各有自己的 ID 空间，两台服务器上的 3 号种子在合并列表里必须可区分，
// 否则批量暂停会把另一台的同号种子一起停掉。
func TestEncodeDecodeID(t *testing.T) {
	// 活动服务器（idx=-1）的 ID 不编码，既有链接/状态文件不受影响
	idx, local := DecodeID(12345)
	if idx != -1 || local != 12345 {
		t.Errorf("未编码 ID 应解析为活动服务器，得到 idx=%d local=%d", idx, local)
	}
	for i := 0; i < 8; i++ {
		encoded := EncodeID(i, 3)
		gotIdx, gotLocal := DecodeID(encoded)
		if gotIdx != i || gotLocal != 3 {
			t.Errorf("EncodeID(%d,3) 解码回 idx=%d local=%d", i, gotIdx, gotLocal)
		}
		// 不同服务器的同号种子必须不同
		if i > 0 && encoded == EncodeID(i-1, 3) {
			t.Errorf("服务器 %d 与 %d 的 3 号种子 ID 冲突", i, i-1)
		}
	}
	// 本地 ID 上限内不得溢出到服务器编号位
	if _, local := DecodeID(EncodeID(0, maxLocalID-1)); local != maxLocalID-1 {
		t.Errorf("本地 ID 上限处编码错误：%d", local)
	}
}

// TestGroupIDs 批量操作的 ID 必须按后端分组，且组内顺序与输入一致；
// 指向不可用服务器的编码 ID 必须整体报错，绝不能回落到活动服务器执行
func TestGroupIDs(t *testing.T) {
	m, err := NewManager(Credentials{URL: "http://127.0.0.1:9091/transmission/rpc"})
	if err != nil {
		t.Fatalf("创建 Manager 失败: %v", err)
	}
	// 未编码的 ID 属于活动后端，全部落到同一组
	groups, err := m.GroupIDs([]int64{1, 2, 3})
	if err != nil {
		t.Fatalf("未编码 ID 不应报错: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("未编码 ID 应只有一组，得到 %d 组", len(groups))
	}
	if len(groups[0].IDs) != 3 {
		t.Errorf("组内应有 3 个 ID，得到 %d", len(groups[0].IDs))
	}
	// 未知服务器的编码 ID 必须报错：不同服务器的本地 ID 空间独立，回落即误伤
	if _, err := m.GroupIDs([]int64{EncodeID(3, 9)}); err == nil {
		t.Error("指向不存在服务器的 ID 必须报错，不能回落到活动服务器")
	}
	if g, err := m.GroupIDs(nil); g != nil || err != nil {
		t.Error("空 ID 列表应返回 nil（Transmission 语义：作用于全部种子）")
	}
}

// TestTargetLabel 未命名服务器的展示名必须回落到「服务器 N」（N 从 1 起）
func TestTargetLabel(t *testing.T) {
	if got := (Target{Index: 0}).Label(); got != "服务器 1" {
		t.Errorf("0 号未命名应显示「服务器 1」，得到 %q", got)
	}
	if got := (Target{Index: 2, Name: "家里的QB"}).Label(); got != "家里的QB" {
		t.Errorf("有名字时应原样返回，得到 %q", got)
	}
}

// TestSameTargetIgnoresName 改显示名不得触发后端重建（qBittorrent 重建要重新登录）
func TestSameTargetIgnoresName(t *testing.T) {
	base := Target{Index: 1, Kind: "qbittorrent", URL: "http://127.0.0.1:8080", User: "admin", Pass: "x", Enabled: true}
	renamed := base
	renamed.Name = "另外一个名字"
	if !sameTarget(base, renamed) {
		t.Error("仅显示名不同必须复用同一后端实例")
	}
	if base == renamed {
		t.Error("Name 应参与结构体比较（测试前提不成立）")
	}
	// 地址变了必须重建
	moved := base
	moved.URL = "http://127.0.0.1:8081"
	if sameTarget(base, moved) {
		t.Error("地址变化必须重建后端")
	}
}

// TestActiveIndexMatchesCredentials 活动服务器索引按凭据比对得出，
// 不依赖 state 里的 ActiveServer（两者可能不同步，按状态猜会把归属标到另一台）
func TestActiveIndexMatchesCredentials(t *testing.T) {
	m, err := NewManager(Credentials{Type: "qbittorrent", URL: "http://127.0.0.1:8080"})
	if err != nil {
		t.Fatalf("创建 Manager 失败: %v", err)
	}
	// members 为空时无从判断，必须返回 -1 而不是硬猜 0
	if got := m.activeIndex(); got != -1 {
		t.Errorf("没有聚合成员时应返回 -1，得到 %d", got)
	}
}

// TestTagTorrentsOwnership 归属字段必须写上，且活动服务器的 ID 不编码。
// 这是「列表里分不清哪个是 TR、哪个是 QB」的修复点：光看 ID 无法区分，
// 必须显式带上服务器索引 / 名称 / 下载器类型。
func TestTagTorrentsOwnership(t *testing.T) {
	m, err := NewManager(Credentials{Type: "qbittorrent", URL: "http://127.0.0.1:8080"})
	if err != nil {
		t.Fatalf("创建 Manager 失败: %v", err)
	}
	// 直接塞一个成员，模拟聚合目标（backend 为 nil，仅测打标逻辑）
	m.mu.Lock()
	m.members[1] = &member{target: Target{Index: 1, Kind: "qbittorrent", Name: "QB机", URL: "http://127.0.0.1:8080"}}
	m.mu.Unlock()

	// 活动服务器：ID 保持原值，但归属照写
	active := []*models.Torrent{{ID: 7}, {ID: 8}}
	m.tagTorrents(active, 1, true)
	for _, x := range active {
		if x.ServerIndex == nil || *x.ServerIndex != 1 || x.ServerName != "QB机" || x.Kind != "qbittorrent" {
			t.Errorf("活动服务器归属错误: %+v", x)
		}
	}
	if active[0].ID != 7 {
		t.Errorf("活动服务器的 ID 不应编码，得到 %d", active[0].ID)
	}

	// 非活动服务器：ID 编码，归属照写
	other := []*models.Torrent{{ID: 7}}
	m.tagTorrents(other, 0, false)
	if other[0].ID != EncodeID(0, 7) {
		t.Errorf("非活动服务器的 ID 应编码，得到 %d", other[0].ID)
	}
	// 未命名成员回落到「服务器 N」
	if other[0].ServerName != "服务器 1" {
		t.Errorf("未命名成员应回落「服务器 1」，得到 %q", other[0].ServerName)
	}
	// 0 号服务器的归属必须是个「有值的 0」，不能被当成没有归属
	if other[0].ServerIndex == nil || *other[0].ServerIndex != 0 {
		t.Errorf("0 号服务器的 ServerIndex 必须显式为 0，得到 %v", other[0].ServerIndex)
	}
}

// TestTagTorrentsIndexNotAliased 归属指针必须各自独立。
// 实现里对循环/参数变量取地址，一旦所有种子共享同一个指针，
// 后面改一台的归属会把前面所有种子一起改掉。
func TestTagTorrentsIndexNotAliased(t *testing.T) {
	m, err := NewManager(Credentials{URL: "http://127.0.0.1:9091/transmission/rpc"})
	if err != nil {
		t.Fatalf("创建 Manager 失败: %v", err)
	}
	list := []*models.Torrent{{ID: 1}, {ID: 2}, {ID: 3}}
	m.tagTorrents(list, 2, false)
	for i, x := range list {
		if x.ServerIndex == nil || *x.ServerIndex != 2 {
			t.Fatalf("第 %d 颗归属错误: %v", i, x.ServerIndex)
		}
		if x.ServerIndex == list[0].ServerIndex && i > 0 {
			t.Fatalf("第 %d 颗与第 0 颗共享了同一个指针，改一台会串改全部", i)
		}
	}
	// 改一颗不得影响其它
	*list[0].ServerIndex = 9
	if *list[1].ServerIndex != 2 {
		t.Error("修改一颗的归属串改了另一颗：指针被共享")
	}
}

func TestNewManagerKind(t *testing.T) {
	tr, err := NewManager(Credentials{URL: "http://127.0.0.1:9091/transmission/rpc"})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if tr.Kind().String() != "transmission" {
		t.Errorf("缺省类型应为 transmission，得到 %s", tr.Kind())
	}
	qb, err := NewManager(Credentials{Type: "qbittorrent", URL: "http://127.0.0.1:8080"})
	if err != nil {
		t.Fatalf("创建 qBittorrent Manager 失败: %v", err)
	}
	if qb.Kind().String() != "qbittorrent" {
		t.Errorf("类型应为 qbittorrent，得到 %s", qb.Kind())
	}
	if qb.Capabilities().BandwidthGroups {
		t.Error("qBittorrent 不应声明支持带宽组")
	}
	// qBittorrent 地址必须规范化（去掉 /api/v2 与结尾斜杠）
	if qb.Credentials().URL != "http://127.0.0.1:8080" {
		t.Errorf("地址未规范化：%s", qb.Credentials().URL)
	}
}
