package rpc

import (
	"testing"
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

// TestGroupIDs 批量操作的 ID 必须按后端分组，且组内顺序与输入一致
func TestGroupIDs(t *testing.T) {
	m, err := NewManager(Credentials{URL: "http://127.0.0.1:9091/transmission/rpc"})
	if err != nil {
		t.Fatalf("创建 Manager 失败: %v", err)
	}
	// 未设置聚合成员时，全部落到同一组（活动后端）
	groups := m.GroupIDs([]int64{1, 2, EncodeID(3, 9)})
	if len(groups) != 1 {
		t.Fatalf("未聚合时应只有一组，得到 %d 组", len(groups))
	}
	if len(groups[0].IDs) != 3 {
		t.Errorf("组内应有 3 个 ID，得到 %d", len(groups[0].IDs))
	}
	// 未知服务器的 ID 必须还原成本地 ID 而不是把编码值发给后端
	if groups[0].IDs[2] != 9 {
		t.Errorf("未知服务器的 ID 应还原为本地 ID 9，得到 %d", groups[0].IDs[2])
	}
	if m.GroupIDs(nil) != nil {
		t.Error("空 ID 列表应返回 nil（Transmission 语义：作用于全部种子）")
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
