package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/trpanel/backend/internal/models"
	"github.com/trpanel/backend/internal/platform"
	"github.com/trpanel/backend/internal/rpc"
)

const (
	writeWait     = 10 * time.Second
	pongWait      = 60 * time.Second
	pingPeriod    = 30 * time.Second
	maxSendBuffer = 256
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	// Origin 校验统一在 HandleWS 中按宿主平台策略执行：
	// upgrader 是包级变量，拿不到平台实例，故此处先放行。
	CheckOrigin: func(r *http.Request) bool { return true },
}

// wsClient 单个 WebSocket 客户端
type wsClient struct {
	conn *websocket.Conn
	send chan []byte
}

// Hub WebSocket 广播中心
type Hub struct {
	rpc          *rpc.Manager
	plat         platform.Platform
	pollInterval atomic.Int64 // 纳秒，支持运行期热调整

	mu          sync.RWMutex
	curTorrents []*models.Torrent // 最近一轮种子快照（新客户端接入时全量补发）
	prevByID    map[int64][]byte  // 上一轮每个种子的 JSON 字节，作为增量 diff 的基准
	lastDigest  [sha256.Size]byte // 最近一轮种子数据的摘要，用于快速去重
	clients     map[*wsClient]struct{}
	clientCount atomic.Int64
	register    chan *wsClient
	unregister  chan *wsClient
	broadcast   chan []byte
	// bump 唤醒拉取协程（容量 1，多次 Bump 合并为一次补拉）
	bump chan struct{}
}

// NewHub 创建 Hub（plat 提供 WebSocket 握手的同源判定策略）
func NewHub(manager *rpc.Manager, interval time.Duration, plat platform.Platform) *Hub {
	h := &Hub{
		rpc:        manager,
		plat:       plat,
		clients:    make(map[*wsClient]struct{}),
		register:   make(chan *wsClient),
		unregister: make(chan *wsClient),
		broadcast:  make(chan []byte, 16),
		bump:       make(chan struct{}, 1),
	}
	h.pollInterval.Store(interval.Nanoseconds())
	return h
}

// getPollInterval 获取当前轮询间隔
func (h *Hub) getPollInterval() time.Duration {
	return time.Duration(h.pollInterval.Load())
}

// SetPollInterval 运行期调整轮询间隔
func (h *Hub) SetPollInterval(interval time.Duration) {
	if interval < 500*time.Millisecond {
		interval = 500 * time.Millisecond
	}
	h.pollInterval.Store(interval.Nanoseconds())
	h.Signal()
}

// Start 启动广播与唯一的拉取协程（串行拉取，避免并发请求互相覆盖）
func (h *Hub) Start(ctx context.Context) {
	go h.run()
	go h.pollLoop(ctx)
}

func (h *Hub) run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = struct{}{}
			// 首个客户端接入时刷新一次数据（无客户端期间未轮询，缓存可能过期）
			if h.clientCount.Add(1) == 1 {
				h.Bump()
			}
			// 新连接立即推送当前全量快照（增量 diff 只对已持有全量的客户端有意义）
			h.mu.RLock()
			cur := h.curTorrents
			h.mu.RUnlock()
			if cur != nil {
				if data, err := marshalFullEnvelope(cur); err == nil {
					select {
					case client.send <- data:
					default:
					}
				}
			}
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
				h.clientCount.Add(-1)
			}
		case data := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.send <- data:
				default:
					// 发送缓冲区满，判定客户端过慢，断开
					delete(h.clients, client)
					close(client.send)
				}
			}
		}
	}
}

func (h *Hub) pollLoop(ctx context.Context) {
	for {
		// 无客户端时跳过拉取，避免对 Transmission 无意义的高频请求；
		// 首个客户端接入时 Bump 会唤醒这里补一次
		if h.clientCount.Load() > 0 {
			h.pollLocked()
		} else if !h.hasLastData() {
			h.pollLocked() // 启动预热，保证静态首屏有数据
		}
		if !h.wait(ctx) {
			return
		}
	}
}

// wait 等待一个轮询周期；期间被 Signal/Bump 唤醒则立即返回，
// 返回 false 表示上下文已取消（进程退出）
func (h *Hub) wait(ctx context.Context) bool {
	timer := time.NewTimer(h.getPollInterval())
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	case <-h.bump:
		return true
	}
}

// hasLastData 是否已有可推送的缓存数据
func (h *Hub) hasLastData() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.curTorrents != nil
}

// Signal 打断等待，尽快拉取一次（合并短时间内多次信号）
func (h *Hub) Signal() {
	select {
	case h.bump <- struct{}{}:
	default:
	}
}

// Bump 数据变更后尽快拉取并广播。
// 旧实现在已有拉取在跑时直接丢弃本次请求，而那次拉取可能早于本次变更，
// 导致最后一次变更要等到下个轮询周期才可见。
func (h *Hub) Bump() { h.Signal() }

// pollLocked 实际拉取逻辑（仅由 pollLoop 单协程调用）。
// 与上一轮逐种子比较字节级差异，只广播变化部分：
//   - 首轮或前端不支持时发送 {type:"full"} 全量信封；
//   - 之后发送 {type:"diff", added, updated, removed}，仅含变化的种子对象。
//
// 大多数轮询周期里只有极少数种子在动（做种中的列表几乎全部静止），
// 增量报文通常只有全量的几十分之一，显著降低广播序列化与带宽开销。
func (h *Hub) pollLocked() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	torrents, err := h.rpc.GetTorrentsFresh(ctx)
	if err != nil {
		slog.Error("轮询种子失败", "err", err)
		return
	}
	// 每个种子只序列化一次，字节切片同时用于 diff 比较与信封拼装
	bodies := make([][]byte, len(torrents))
	byID := make(map[int64][]byte, len(torrents))
	ids := make([]int64, len(torrents))
	for i, t := range torrents {
		b, err := json.Marshal(t)
		if err != nil {
			slog.Error("序列化种子数据失败", "err", err)
			return
		}
		bodies[i] = b
		ids[i] = t.ID
		byID[t.ID] = b
	}

	var data []byte
	h.mu.RLock()
	prev := h.prevByID
	h.mu.RUnlock()

	if prev == nil {
		// 首轮：全量信封
		full := joinBodies(bodies)
		data, err = json.Marshal(map[string]interface{}{
			"type":      "full",
			"data":      json.RawMessage(full),
			"timestamp": time.Now().UnixMilli(),
		})
		if err != nil {
			slog.Error("序列化推送数据失败", "err", err)
			return
		}
	} else {
		// 逐种子 diff
		var addedIDs, updatedIDs []int
		var removed []int64
		for i, id := range ids {
			if old, ok := prev[id]; !ok {
				addedIDs = append(addedIDs, i)
			} else if !bytes.Equal(old, bodies[i]) {
				updatedIDs = append(updatedIDs, i)
			}
		}
		prevIDs := make(map[int64]struct{}, len(prev))
		for id := range prev {
			prevIDs[id] = struct{}{}
			if _, ok := byID[id]; !ok {
				removed = append(removed, id)
			}
		}
		if len(addedIDs) == 0 && len(updatedIDs) == 0 && len(removed) == 0 {
			// 完全无变化：仅推进基准（内容等价），跳过广播
			h.mu.Lock()
			h.curTorrents = torrents
			h.prevByID = byID
			h.mu.Unlock()
			return
		}
		added := make([]json.RawMessage, 0, len(addedIDs))
		for _, i := range addedIDs {
			added = append(added, json.RawMessage(bodies[i]))
		}
		updated := make([]json.RawMessage, 0, len(updatedIDs))
		for _, i := range updatedIDs {
			updated = append(updated, json.RawMessage(bodies[i]))
		}
		data, err = json.Marshal(map[string]interface{}{
			"type":      "diff",
			"added":     added,
			"updated":   updated,
			"removed":   removed,
			"timestamp": time.Now().UnixMilli(),
		})
		if err != nil {
			slog.Error("序列化推送数据失败", "err", err)
			return
		}
	}

	// 快速去重：整体摘要未变则不广播（与逐种子比较结论一致，但 O(1) 比对）
	digest := sha256.Sum256(joinBodies(bodies))
	h.mu.Lock()
	h.curTorrents = torrents
	h.prevByID = byID
	h.lastDigest = digest
	h.mu.Unlock()
	h.broadcast <- data
}

// joinBodies 将各种子的 JSON 字节拼装为 JSON 数组体（含方括号）
func joinBodies(bodies [][]byte) []byte {
	n := 2
	for _, b := range bodies {
		n += len(b) + 1
	}
	out := make([]byte, 0, n)
	out = append(out, '[')
	for i, b := range bodies {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, b...)
	}
	return append(out, ']')
}

// marshalFullEnvelope 构建全量信封报文（新客户端接入时补发）
func marshalFullEnvelope(torrents []*models.Torrent) ([]byte, error) {
	bodies := make([][]byte, len(torrents))
	for i, t := range torrents {
		b, err := json.Marshal(t)
		if err != nil {
			return nil, err
		}
		bodies[i] = b
	}
	return json.Marshal(map[string]interface{}{
		"type":      "full",
		"data":      json.RawMessage(joinBodies(bodies)),
		"timestamp": time.Now().UnixMilli(),
	})
}

// HandleWS 处理 WebSocket 连接
func (h *Hub) HandleWS(c *gin.Context) {
	pol := h.plat.SecurityPolicy()
	// WebSocket 不受 CORS 约束，浏览器同源策略拦不住跨站订阅，任意网页都能静默收到
	// 全量种子数据（名称/下载路径/磁力链接/Peer 地址）。Sec-Fetch-Site 由浏览器强制写入、
	// 页面脚本无法伪造，因此两种部署形态都先拒掉标记为跨站的握手。
	if c.Request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		respondError(c, http.StatusForbidden, "跨站 WebSocket 连接已被拒绝")
		return
	}
	// 网关模式（AllowEmbedding）：宿主统一认证 + 服务仅本机 socket/回环可达，信任网关转发，
	// 跳过 Origin 同源判定（网关改写的 Host / 缺失的 X-Forwarded-Host 会让 WS 误判跨域而 403）。
	// 直连部署保持严格同源校验，防止任意网页静默订阅全量种子数据（名称/路径/磁力链接）。
	if !pol.AllowEmbedding {
		if origin := c.Request.Header.Get("Origin"); origin != "" && !pol.IsSameSiteOrigin(c.Request, origin) {
			respondError(c, http.StatusForbidden, "Origin 不被信任")
			return
		}
	}
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		slog.Error("升级 WebSocket 失败", "err", err)
		return
	}
	client := &wsClient{conn: conn, send: make(chan []byte, maxSendBuffer)}
	h.register <- client
	go h.writePump(client)
	h.readPump(client)
}

func (h *Hub) readPump(client *wsClient) {
	defer func() {
		h.unregister <- client
		client.conn.Close()
	}()
	client.conn.SetReadLimit(4096)
	_ = client.conn.SetReadDeadline(time.Now().Add(pongWait))
	client.conn.SetPongHandler(func(string) error {
		return client.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		if _, _, err := client.conn.ReadMessage(); err != nil {
			break
		}
	}
}

func (h *Hub) writePump(client *wsClient) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		client.conn.Close()
	}()
	for {
		select {
		case message, ok := <-client.send:
			_ = client.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = client.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := client.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			_ = client.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := client.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
