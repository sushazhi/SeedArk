package qbittorrent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sushazhi/seedark/backend/internal/qbmock"
)

// newCountingServer 起一个按端点计数并可控响应的 mock：
// handlers 返回的 (status, body) 为 nil 时回 200 空体。
func newCountingServer(t *testing.T, handlers map[string]func() (int, string), counter *map[string]*atomic.Int64) *httptest.Server {
	t.Helper()
	counts := map[string]*atomic.Int64{}
	for ep := range handlers {
		counts[ep] = &atomic.Int64{}
	}
	*counter = counts
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ep := strings.TrimPrefix(r.URL.Path, "/api/v2/")
		// 与真实 qB 一致：POST 需要 Origin/Referer 与 Host 一致
		if r.Method == http.MethodPost {
			if h := r.Header.Get("Origin"); h != "" && !strings.Contains(h, r.Host) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		h, ok := handlers[ep]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("not found"))
			return
		}
		if cnt, ok := counts[ep]; ok {
			cnt.Add(1)
		}
		if h == nil {
			return
		}
		status, body := h()
		if status != 0 {
			w.WriteHeader(status)
		}
		if body != "" {
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

// TestPostFallbackChain 验证回退链：首端点 404 → 次端点命中；
// 后续调用经缺失记忆直接跳过 404 端点，不再浪费往返。
func TestPostFallbackChain(t *testing.T) {
	var counts map[string]*atomic.Int64
	ts := newCountingServer(t, map[string]func() (int, string){
		"app/version":  func() (int, string) { return 0, "v5.2.3" },
		"new/action":   func() (int, string) { return http.StatusNotFound, "banned" },
		"old/action":   nil,
		"other/action": func() (int, string) { return 0, "" },
	}, &counts)
	c, err := New(ts.URL, "", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	// 第一次：走完整回退链
	if err := c.postFallback(ctx, nil, "new/action", "old/action"); err != nil {
		t.Fatalf("首次 postFallback: %v", err)
	}
	if got := counts["new/action"].Load(); got != 1 {
		t.Errorf("new/action 请求数 = %d, 期望 1", got)
	}
	if got := counts["old/action"].Load(); got != 1 {
		t.Errorf("old/action 请求数 = %d, 期望 1", got)
	}

	// 第二次：new/action 被记忆缺失，直接命中 old/action
	if err := c.postFallback(ctx, nil, "new/action", "old/action"); err != nil {
		t.Fatalf("二次 postFallback: %v", err)
	}
	if got := counts["new/action"].Load(); got != 1 {
		t.Errorf("缺失记忆未生效，new/action 请求数 = %d, 期望仍为 1", got)
	}
	if got := counts["old/action"].Load(); got != 2 {
		t.Errorf("old/action 请求数 = %d, 期望 2", got)
	}

	// Ping 成功后重置记忆，恢复探测
	if _, err := c.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if err := c.postFallback(ctx, nil, "new/action", "old/action"); err != nil {
		t.Fatalf("重置后 postFallback: %v", err)
	}
	if got := counts["new/action"].Load(); got != 2 {
		t.Errorf("Ping 后应重新探测 new/action, 请求数 = %d, 期望 2", got)
	}
}

// TestPostFallbackAllMissing 全部候选缺失时仍强制尝试最后一个（自愈路径）
func TestPostFallbackAllMissing(t *testing.T) {
	var counts map[string]*atomic.Int64
	ts := newCountingServer(t, map[string]func() (int, string){
		"gone/a": func() (int, string) { return http.StatusNotFound, "" },
		"gone/b": func() (int, string) { return http.StatusNotFound, "" },
	}, &counts)
	c, _ := New(ts.URL, "", "")
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		err := c.postFallback(ctx, nil, "gone/a", "gone/b")
		if err == nil {
			t.Fatalf("第 %d 次应返回 404 错误", i+1)
		}
		if !errors.Is(err, errEndpointMissing) {
			t.Fatalf("错误应包装 errEndpointMissing: %v", err)
		}
	}
	// 记忆生效：3 次调用中 a 只发 1 次，b 因「全部缺失强制尾试」每次都发
	if got := counts["gone/a"].Load(); got != 1 {
		t.Errorf("gone/a 请求数 = %d, 期望 1（记忆后跳过）", got)
	}
	if got := counts["gone/b"].Load(); got != 3 {
		t.Errorf("gone/b 请求数 = %d, 期望 3（全缺失时强制尾试自愈）", got)
	}
}

// TestPostFallbackNonVersionError 业务错误（409 等）不回退，原样上抛
func TestPostFallbackNonVersionError(t *testing.T) {
	var counts map[string]*atomic.Int64
	ts := newCountingServer(t, map[string]func() (int, string){
		"new/action": func() (int, string) { return http.StatusConflict, "conflict" },
		"old/action": nil,
	}, &counts)
	c, _ := New(ts.URL, "", "")

	err := c.postFallback(context.Background(), nil, "new/action", "old/action")
	if err == nil {
		t.Fatal("409 应上抛错误")
	}
	if !errors.Is(err, errAlreadyExists) {
		t.Fatalf("错误应包装 errAlreadyExists: %v", err)
	}
	if got := counts["old/action"].Load(); got != 0 {
		t.Errorf("业务错误不应回退, old/action 请求数 = %d, 期望 0", got)
	}
}

// TestWebAPIAtLeast 版本判定：5.x mock 判 2.15 成立、2.16 不成立；
// 4.x mock 判 2.15 不成立、2.11 成立；缓存生效只探测一次。
func TestWebAPIAtLeast(t *testing.T) {
	for _, tc := range []struct {
		compat   string
		maj, min int
		want     bool
	}{
		{"5.x", 2, 15, true},
		{"5.x", 2, 16, false},
		{"4.x", 2, 15, false},
		{"4.x", 2, 11, true},
	} {
		ts := httptest.NewServer(qbmock.New(qbmock.Options{Compat: tc.compat}))
		c, err := New(ts.URL, "", "")
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		got, err := c.webAPIAtLeast(context.Background(), tc.maj, tc.min)
		if err != nil {
			t.Fatalf("compat=%s webAPIAtLeast(%d,%d): %v", tc.compat, tc.maj, tc.min, err)
		}
		if got != tc.want {
			t.Errorf("compat=%s webAPIAtLeast(%d,%d) = %v, 期望 %v", tc.compat, tc.maj, tc.min, got, tc.want)
		}
	}
}

// TestParseWebAPIVersion 版本字符串解析
func TestParseWebAPIVersion(t *testing.T) {
	for _, tc := range []struct {
		in       string
		maj, min int
		ok       bool
	}{
		{"2.15.1", 2, 15, true},
		{"v2.11.2", 2, 11, true},
		{"2.16", 2, 16, true},
		{" 2.15.1 ", 2, 15, true},
		{"", 0, 0, false},
		{"abc", 0, 0, false},
		{"2", 0, 0, false},
	} {
		maj, min, err := parseWebAPIVersion(tc.in)
		if tc.ok && (err != nil || maj != tc.maj || min != tc.min) {
			t.Errorf("parseWebAPIVersion(%q) = (%d,%d,%v), 期望 (%d,%d,nil)", tc.in, maj, min, err, tc.maj, tc.min)
		}
		if !tc.ok && err == nil {
			t.Errorf("parseWebAPIVersion(%q) 应报错", tc.in)
		}
	}
}

// TestKnownMissingLifecycle 缺失记忆的记录 / 查询 / 清空
func TestKnownMissingLifecycle(t *testing.T) {
	c := &Client{}
	if c.knownMissing("torrents/x") {
		t.Fatal("初始不应记忆缺失")
	}
	c.noteMissing("torrents/x")
	if !c.knownMissing("torrents/x") {
		t.Fatal("noteMissing 后应命中")
	}
	c.clearMissing()
	if c.knownMissing("torrents/x") {
		t.Fatal("clearMissing 后不应再命中")
	}
	// 零值 Client 直接 noteMissing（惰性初始化 map）不 panic
	c2 := &Client{}
	c2.noteMissing("torrents/y")
	if !c2.knownMissing("torrents/y") {
		t.Fatal("零值 Client 惰性初始化失败")
	}
}
