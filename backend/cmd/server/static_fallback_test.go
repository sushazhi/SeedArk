// 静态资源回退行为测试：锁定「移动端刷新白屏」的根因。
//
// 回归背景：serveStatic 的 NoRoute 曾对任何未命中路径返回 200 + index.html。
// 发布新版本后，持有旧 HTML 的客户端会请求已删除的 /assets/index-OLDHASH.js，
// 拿到 200 + HTML，浏览器按 ES module 解析必然失败 → #root 空白；
// 且 SW 会把这份 HTML 当作该 JS 的合法响应写进缓存，导致白屏不可自愈。
//
// 这里直接驱动真实的 serveStatic 路由，断言：
//  1. 缺失的 /assets/*.js 返回 404，且响应体不是 HTML
//  2. 缺失的带扩展名静态文件同样 404
//  3. 前端路由（无扩展名）仍然回退 index.html
//  4. 存在的资源正常返回
package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func newStaticRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	serveStatic(r, "")
	return r
}

func doGet(r *gin.Engine, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// 缺失的构建产物必须是 404，绝不能回退成 HTML
func TestMissingAssetReturns404(t *testing.T) {
	r := newStaticRouter(t)

	cases := []string{
		"/assets/index-OLDHASH.js",  // 旧版本脚本：白屏事故的直接触发路径
		"/assets/index-OLDHASH.css", // 旧版本样式
		"/assets/vendor-OLDHASH.js", // 旧版本 vendor
		"/favicon-oldhash.png",      // 带扩展名的其它静态文件
		"/sw.js.bak",                // 任意带扩展名路径
	}
	for _, p := range cases {
		w := doGet(r, p)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: 期望 404，实际 %d", p, w.Code)
		}
		// 关键断言：响应体不能是 HTML，否则会被当成脚本/样式解析失败并污染 SW 缓存
		body := w.Body.String()
		if strings.Contains(strings.ToLower(body), "<!doctype html") ||
			strings.Contains(strings.ToLower(body), "<html") {
			t.Errorf("%s: 响应体是 HTML，会毒化脚本请求（前 80 字节: %q）", p, truncate(body, 80))
		}
	}
}

// 前端路由仍应回退到 index.html（SPA 深链不能 404）
func TestSpaRouteFallsBackToIndex(t *testing.T) {
	r := newStaticRouter(t)

	for _, p := range []string{"/", "/index.html", "/torrents", "/torrents/123", "/settings"} {
		w := doGet(r, p)
		if w.Code != http.StatusOK {
			t.Errorf("%s: 期望 200（SPA 回退），实际 %d", p, w.Code)
			continue
		}
		if !strings.Contains(strings.ToLower(w.Body.String()), "<!doctype html") {
			t.Errorf("%s: SPA 回退应返回 HTML 文档", p)
		}
	}
}

// API 未命中返回 JSON 404，不能返回 HTML
func TestMissingApiReturnsJSON404(t *testing.T) {
	r := newStaticRouter(t)

	w := doGet(r, "/api/does-not-exist")
	if w.Code != http.StatusNotFound {
		t.Errorf("期望 404，实际 %d", w.Code)
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "<html") {
		t.Error("API 404 不应返回 HTML")
	}
}

// 真实存在的资源必须正常返回（回退逻辑不能误伤）
func TestExistingAssetServed(t *testing.T) {
	r := newStaticRouter(t)

	w := doGet(r, "/manifest.webmanifest")
	if w.Code != http.StatusOK {
		t.Errorf("manifest.webmanifest 期望 200，实际 %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "manifest+json") {
		t.Errorf("manifest 的 Content-Type 应为 application/manifest+json，实际 %q", ct)
	}
}

// isStaticAssetPath 的判据本身
func TestIsStaticAssetPath(t *testing.T) {
	yes := []string{"assets/index-abc.js", "assets/x.css", "favicon.ico", "sw.js", "icons/icon-512.png"}
	no := []string{"", "index.html", "torrents", "torrents/123", "settings/advanced"}
	for _, p := range yes {
		if !isStaticAssetPath(p) {
			t.Errorf("%q 应判定为静态资源", p)
		}
	}
	for _, p := range no {
		// index.html 由调用方单独处理，这里只要求路由类路径不被误判
		if p == "index.html" {
			continue
		}
		if isStaticAssetPath(p) {
			t.Errorf("%q 不应判定为静态资源（会误伤 SPA 深链）", p)
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
