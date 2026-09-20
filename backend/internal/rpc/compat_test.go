package rpc

import (
	"context"
	"testing"
)

// TestRPCVersionAtLeastCache 版本门卫的缓存语义：
// rpc-version 已缓存时直接比较，不发任何网络请求（零值 tr 客户端即可验证，
// 若误触网络路径会因 nil 库客户端 panic 而失败）。
func TestRPCVersionAtLeastCache(t *testing.T) {
	c := &Client{}
	ctx := context.Background()

	// 未缓存：走 GetSession 惰性查询——用不可达地址构造真实客户端，
	// 验证「报错而不是把通信故障当成版本过旧」的语义
	real, err := New("http://127.0.0.1:1/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := real.RPCVersionAtLeast(ctx, 16); err == nil {
		t.Fatal("无法通信时应返回错误")
	}

	// 缓存后：纯内存比较
	c.cacheRPCVersion(17)
	for _, tc := range []struct {
		v    int64
		want bool
	}{
		{14, true},
		{17, true},
		{18, false},
	} {
		got, err := c.RPCVersionAtLeast(ctx, tc.v)
		if err != nil {
			t.Fatalf("RPCVersionAtLeast(%d): %v", tc.v, err)
		}
		if got != tc.want {
			t.Errorf("rpc-version=17 时 AtLeast(%d) = %v, 期望 %v", tc.v, got, tc.want)
		}
	}
}

// TestCacheRPCVersionIgnored 无效版本号不覆盖缓存
func TestCacheRPCVersionIgnored(t *testing.T) {
	c := &Client{}
	c.cacheRPCVersion(16)
	c.cacheRPCVersion(0) // 服务器异常未返回版本，不应清掉已知值
	if c.rpcVersion != 16 {
		t.Errorf("缓存被无效值覆盖: %d", c.rpcVersion)
	}
}
