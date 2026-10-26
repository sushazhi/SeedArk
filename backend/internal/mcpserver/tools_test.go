package mcpserver

import (
	"context"
	"maps"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sushazhi/seedark/backend/internal/driver"
	"github.com/sushazhi/seedark/backend/internal/pt"
	"github.com/sushazhi/seedark/backend/internal/rpc"
)

// backendSig 用与运行时相同的工厂构造一次后端，取它声明的类型与能力。
// 直接取真实驱动而不是在测试里抄一份能力表，能力声明变了测试才会跟着变
func backendSig(t *testing.T, kind driver.Kind, url string) driverSig {
	t.Helper()
	b, err := rpc.NewProbe(rpc.Credentials{Type: kind, URL: url})
	if err != nil {
		t.Fatalf("构造 %s 后端失败: %v", kind, err)
	}
	return driverSig{kind: b.Kind(), caps: b.Capabilities()}
}

// listTools 用内存传输连一次 MCP 客户端，取回工具名 → 描述
func listTools(t *testing.T, srv *mcp.Server) map[string]string {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("服务端连接失败: %v", err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("客户端连接失败: %v", err)
	}
	defer cs.Close()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("拉取工具清单失败: %v", err)
	}
	out := make(map[string]string, len(res.Tools))
	for _, tool := range res.Tools {
		out[tool.Name] = tool.Description
	}
	return out
}

// toolsFor 按驱动签名注册一份工具清单（handlers 不会被调用，Server 允许为 nil）
func toolsFor(t *testing.T, sig driverSig) map[string]string {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "SeedArk", Version: "test"}, nil)
	registerTools(srv, nil, sig)
	return listTools(t, srv)
}

// TestToolsFollowCapabilities 工具清单必须随当前下载器能力自适应：
// 拿不到的能力不注册，AI 客户端才不会调用到注定返回「不支持」的工具
func TestToolsFollowCapabilities(t *testing.T) {
	tr := toolsFor(t, backendSig(t, driver.KindTransmission, "http://127.0.0.1:19091/transmission/rpc"))
	qb := toolsFor(t, backendSig(t, driver.KindQBittorrent, "http://127.0.0.1:18080"))

	// 两种下载器都该有的核心工具（防止清单整体塌成空集让下面的差异断言失去意义）
	for _, name := range []string{
		"list_torrents", "get_torrent", "get_stats", "add_torrent", "remove_torrents",
		"get_free_space", "get_session_config", "set_session_config", "set_torrent_limits",
		"set_torrent_labels", "move_torrents", "queue_move", "rename_file", "execute_seed_policy",
	} {
		_, inTR := tr[name]
		_, inQB := qb[name]
		if !inTR || !inQB {
			t.Errorf("核心工具 %s 在两种下载器下都应注册（tr=%v qb=%v）", name, inTR, inQB)
		}
	}

	// 仅 Transmission 具备的能力：透传 / 黑名单 / 端口测试
	trOnly := map[string]bool{}
	for name := range tr {
		if _, ok := qb[name]; !ok {
			trOnly[name] = true
		}
	}
	want := map[string]bool{"transmission_api_request": true, "update_blocklist": true, "test_port": true}
	if !maps.Equal(trOnly, want) {
		t.Errorf("Transmission 独有工具应为 %v，实际 %v", want, trOnly)
	}
	for name := range qb {
		if _, ok := tr[name]; !ok {
			t.Errorf("qBittorrent 出现了 Transmission 没有的工具 %s", name)
		}
	}

	// 行为差异要写在描述里：qBittorrent 追加说明，Transmission 保持通用描述
	if !strings.Contains(qb["get_free_space"], "qBittorrent") {
		t.Error("qBittorrent 下 get_free_space 的描述应说明「只提供全局剩余空间」")
	}
	if !strings.Contains(qb["set_session_config"], "Transmission 专属项") {
		t.Error("qBittorrent 下 set_session_config 的描述应说明被忽略的设置项")
	}
	if strings.Contains(tr["get_free_space"], "qBittorrent") || strings.Contains(tr["set_session_config"], "qBittorrent") {
		t.Error("Transmission 下的描述不应出现 qBittorrent 说明")
	}
}

// TestServerReusesInstancePerDriver 同一驱动复用实例（stateless 下每个请求都要取实例，
// 每次重建等于重算一遍全量工具定义的 JSON Schema）；切换下载器后按新能力重建
func TestServerReusesInstancePerDriver(t *testing.T) {
	m, err := rpc.NewManager(rpc.Credentials{Type: driver.KindTransmission, URL: "http://127.0.0.1:19091/transmission/rpc"})
	if err != nil {
		t.Fatalf("创建 Manager 失败: %v", err)
	}
	s := New(m, nil, nil, nil, &atomic.Bool{}, &atomic.Bool{}, nil, nil)
	first := s.server()
	if s.server() != first {
		t.Error("驱动未变时应复用同一实例")
	}
	if _, ok := listTools(t, first)["transmission_api_request"]; !ok {
		t.Error("Transmission 下应暴露 transmission_api_request")
	}

	if err := m.Reconfigure(rpc.Credentials{Type: driver.KindQBittorrent, URL: "http://127.0.0.1:18080"}); err != nil {
		t.Fatalf("切换下载器失败: %v", err)
	}
	second := s.server()
	if second == first {
		t.Fatal("切换下载器后应重建工具服务实例")
	}
	if _, ok := listTools(t, second)["transmission_api_request"]; ok {
		t.Error("切换为 qBittorrent 后不应再暴露 transmission_api_request")
	}
}

// ptToolNames 保种台的只读工具
var ptToolNames = []string{"pt_seed_ledger", "pt_release_calendar", "pt_vault_search", "pt_content_matrix"}

// toolsForServer 用给定的 Server 注册一份工具清单（处理器不会被调用）
func toolsForServer(t *testing.T, s *Server, sig driverSig) map[string]string {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "SeedArk", Version: "test"}, nil)
	registerTools(srv, s, sig)
	return listTools(t, srv)
}

// TestPTToolsGatedOnService 保种台工具只在 pt 服务可用时才注册。
// ptSvc 为 nil 表示数据目录不可写：此时注册出去的工具只会返回错误，
// 不该出现在清单里让 AI 客户端白调一次。
func TestPTToolsGatedOnService(t *testing.T) {
	m, err := rpc.NewManager(rpc.Credentials{Type: driver.KindTransmission, URL: "http://127.0.0.1:19091/transmission/rpc"})
	if err != nil {
		t.Fatalf("创建 Manager 失败: %v", err)
	}
	sig := backendSig(t, driver.KindTransmission, "http://127.0.0.1:19091/transmission/rpc")

	off := toolsForServer(t, New(m, nil, nil, nil, &atomic.Bool{}, &atomic.Bool{}, nil, nil), sig)
	for _, name := range ptToolNames {
		if _, ok := off[name]; ok {
			t.Errorf("ptSvc 为 nil 时不应注册 %s", name)
		}
	}

	// 只验证注册条件，处理器不会被调用，因此 state.Store 传 nil 即可
	svc, err := pt.New(m, nil, t.TempDir())
	if err != nil {
		t.Fatalf("创建 pt 服务失败: %v", err)
	}
	on := toolsForServer(t, New(m, nil, nil, nil, &atomic.Bool{}, &atomic.Bool{}, nil, svc), sig)
	for _, name := range ptToolNames {
		if _, ok := on[name]; !ok {
			t.Errorf("ptSvc 可用时应注册 %s", name)
		}
	}
}
