package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sushazhi/seedark/backend/internal/middleware"
)

// newWSTicketEngine 只挂 WS 握手链路所需的最小路由：/api/ws-ticket（同 api 组鉴权）+ /ws
func newWSTicketEngine(token string) (*gin.Engine, *Handler) {
	gin.SetMode(gin.TestMode)
	h := &Handler{apiToken: token, wsTickets: newWSTicketStore()}
	r := gin.New()
	if token != "" {
		r.POST("/api/ws-ticket", middleware.Auth(token), h.issueWsTicket)
	}
	r.GET("/ws", h.wsGuard(), func(c *gin.Context) { c.String(http.StatusOK, "upgraded") })
	return r, h
}

// 启用令牌时：无票据无令牌一律 401；令牌本身仍可用（旧客户端兼容）；
// 票据用后即焚，重放必须失败
func TestWSTicketHandshake(t *testing.T) {
	r, _ := newWSTicketEngine("secret-token")

	cases := []struct {
		name string
		path string
		want int
	}{
		{"无凭据", "/ws", http.StatusUnauthorized},
		{"错误令牌", "/ws?token=wrong", http.StatusUnauthorized},
		{"长期令牌（兼容路径）", "/ws?token=secret-token", http.StatusOK},
		{"伪造票据", "/ws?ticket=deadbeef", http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, c.path, nil))
			if w.Code != c.want {
				t.Fatalf("状态码 = %d, 期望 %d（body %s）", w.Code, c.want, w.Body.String())
			}
		})
	}

	// 换票后用票据握手成功，且同一张票据不能再次使用
	ticket := issueTicketVia(t, r)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ws?ticket="+ticket, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("持有效票据的握手状态码 = %d, 期望 200（body %s）", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ws?ticket="+ticket, nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("票据重放状态码 = %d, 期望 401（必须用后即焚）", w.Code)
	}
}

// 未启用令牌：握手不需要任何凭据（保持「依赖宿主网关认证」的部署模型）
func TestWSTicketNoTokenAllowsDirect(t *testing.T) {
	r, _ := newWSTicketEngine("")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ws", nil))
	if w.Code != http.StatusOK {
		t.Errorf("未启用令牌时状态码 = %d, 期望 200", w.Code)
	}
}

// 换票接口受普通 API 的令牌鉴权保护：未授权者拿不到票据
func TestWSTicketEndpointRequiresAuth(t *testing.T) {
	r, _ := newWSTicketEngine("secret-token")
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/ws-ticket", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("未带令牌换票状态码 = %d, 期望 401", w.Code)
	}
}

// 过期票据不可用
func TestWSTicketExpiry(t *testing.T) {
	s := newWSTicketStore()
	ticket := s.issue()
	if ticket == "" {
		t.Fatal("签发票据失败")
	}
	// 直接把过期时间拨到过去，避免测试里真的等 60 秒
	s.mu.Lock()
	s.tickets[ticket] = time.Now().Add(-time.Second)
	s.mu.Unlock()
	if s.consume(ticket) {
		t.Error("过期票据不应通过校验")
	}
}

// issueTicketVia 走 POST /api/ws-ticket 拿一张票据
func issueTicketVia(t *testing.T, r *gin.Engine) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/ws-ticket", nil)
	req.Header.Set("X-Auth-Token", "secret-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("换票状态码 = %d（body %s）", w.Code, w.Body.String())
	}
	var out struct {
		Data struct {
			Ticket string `json:"ticket"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析换票响应失败: %v（body %s）", err, w.Body.String())
	}
	if out.Data.Ticket == "" {
		t.Fatal("响应未携带票据")
	}
	return out.Data.Ticket
}
