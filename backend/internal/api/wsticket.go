package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sushazhi/seedark/backend/internal/middleware"
)

// wsTicketTTL 一次性握手票据的有效期：只覆盖「取票 → 建连」这段极短时间窗
const wsTicketTTL = 60 * time.Second

// wsTicketBytes 票据随机熵（32 字节 = 256 位，十六进制 64 字符）
const wsTicketBytes = 32

// wsTicketStore 一次性 WebSocket 握手票据。
//
// 浏览器无法为 WS 握手设置自定义请求头，令牌只能出现在查询串里；而查询串会进入
// 浏览器历史、网关/代理访问日志与 Referer——长期令牌一旦落进日志就等于泄露。
// 改为「先用已鉴权的普通请求换一张一次性短时票据，再用票据握手」：
// 票据用后即焚、过期自动作废，即使被记进日志也无法再次使用。
type wsTicketStore struct {
	mu      sync.Mutex
	tickets map[string]time.Time // ticket -> 过期时间
}

func newWSTicketStore() *wsTicketStore {
	return &wsTicketStore{tickets: map[string]time.Time{}}
}

// issue 签发一张新票据（随机源不可用时返回空串，由调用方报错）
func (s *wsTicketStore) issue() string {
	buf := make([]byte, wsTicketBytes)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	ticket := hex.EncodeToString(buf)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	// 顺手回收过期票据：签发频率低，表规模始终很小
	for k, exp := range s.tickets {
		if now.After(exp) {
			delete(s.tickets, k)
		}
	}
	s.tickets[ticket] = now.Add(wsTicketTTL)
	return ticket
}

// consume 校验并作废一张票据；无论是否有效都只判断一次（用后即焚）
func (s *wsTicketStore) consume(ticket string) bool {
	if ticket == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.tickets[ticket]
	if !ok {
		return false
	}
	delete(s.tickets, ticket)
	return time.Now().Before(exp)
}

// issueWsTicket POST /api/ws-ticket 用已鉴权的普通请求换一张一次性握手票据。
// 该路由在 /api 组内，受与其它接口相同的令牌鉴权保护。
func (h *Handler) issueWsTicket(c *gin.Context) {
	ticket := h.wsTickets.issue()
	if ticket == "" {
		respondError(c, http.StatusInternalServerError, "生成握手票据失败")
		return
	}
	respond(c, gin.H{"ticket": ticket, "expiresIn": int(wsTicketTTL / time.Second)})
}

// wsGuard WebSocket 握手鉴权中间件。
// 令牌未启用时直接放行（保持「依赖宿主网关认证」的既有部署模型）；
// 启用后优先接受一次性票据，并兼容仍带 ?token= 的旧客户端。
func (h *Handler) wsGuard() gin.HandlerFunc {
	if h.apiToken == "" {
		return func(c *gin.Context) { c.Next() }
	}
	fallback := middleware.AuthAllowQuery(h.apiToken)
	return func(c *gin.Context) {
		if h.wsTickets.consume(c.Query("ticket")) {
			c.Next()
			return
		}
		fallback(c)
	}
}
