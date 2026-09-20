package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sushazhi/seedark/backend/internal/models"
)

// RawCall 发送任意 Transmission RPC 方法（绕过库未实现的字段/方法）
func (c *Client) RawCall(ctx context.Context, method string, args map[string]any) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rq := map[string]any{"method": method}
	if len(args) > 0 {
		rq["arguments"] = args
	}
	payload, err := json.Marshal(rq)
	if err != nil {
		return nil, err
	}
	do := func(sid string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if sid != "" {
			req.Header.Set("X-Transmission-Session-Id", sid)
		}
		if c.user != "" {
			req.SetBasicAuth(c.user, c.pass)
		}
		// 复用 Client 自建的 HTTP 客户端：带整体超时与 TLS 版本下限，不使用全局 DefaultClient
		return c.httpClient.Do(req)
	}
	c.sessionMu.Lock()
	sid := c.sessionID
	c.sessionMu.Unlock()
	resp, err := do(sid)
	if err != nil {
		return nil, err
	}
	// Transmission 会在 409 时返回新的会话 ID，重试一次
	if resp.StatusCode == http.StatusConflict {
		c.sessionMu.Lock()
		c.sessionID = resp.Header.Get("X-Transmission-Session-Id")
		sid = c.sessionID
		c.sessionMu.Unlock()
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		resp, err = do(sid)
		if err != nil {
			return nil, err
		}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Transmission RPC 失败 (HTTP %d): %s", resp.StatusCode, truncateStr(string(body), 200))
	}
	var out struct {
		Result    string         `json:"result"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("解析 RPC 响应失败: %w", err)
	}
	if out.Result != "success" {
		return nil, fmt.Errorf("Transmission RPC 错误: %s", out.Result)
	}
	return out.Arguments, nil
}

// SetTorrentRawFields 通过原始 RPC 设置库未实现的字段（如 sequentialDownload）
func (c *Client) SetTorrentRawFields(ctx context.Context, ids []int64, fields map[string]any) error {
	args := map[string]any{}
	if len(ids) == 0 {
		args["ids"] = []int64{}
	} else {
		idsAny := make([]any, len(ids))
		for i, id := range ids {
			idsAny[i] = id
		}
		args["ids"] = idsAny
	}
	for k, v := range fields {
		args[k] = v
	}
	_, err := c.RawCall(ctx, "torrent-set", args)
	return err
}

// GetTorrentRawFields 读取库未实现的种子字段（sequentialDownload / groups）
func (c *Client) GetTorrentRawFields(ctx context.Context, ids []int64) (map[int64]map[string]any, error) {
	args := map[string]any{"fields": []string{"id", "sequentialDownload", "groups"}}
	if len(ids) > 0 {
		idsAny := make([]any, len(ids))
		for i, id := range ids {
			idsAny[i] = id
		}
		args["ids"] = idsAny
	}
	out, err := c.RawCall(ctx, "torrent-get", args)
	if err != nil {
		return nil, err
	}
	torrents, _ := out["torrents"].([]any)
	result := make(map[int64]map[string]any, len(torrents))
	for _, item := range torrents {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		idf, ok := m["id"].(float64)
		if !ok {
			continue
		}
		result[int64(idf)] = m
	}
	return result, nil
}

// GetTorrentPieces 读取块位图（pieces / pieceCount / pieceSize）
func (c *Client) GetTorrentPieces(ctx context.Context, ids []int64) (map[int64]map[string]any, error) {
	args := map[string]any{"fields": []string{"id", "pieces", "pieceCount", "pieceSize"}}
	idsAny := make([]any, len(ids))
	for i, id := range ids {
		idsAny[i] = id
	}
	args["ids"] = idsAny
	out, err := c.RawCall(ctx, "torrent-get", args)
	if err != nil {
		return nil, err
	}
	torrents, _ := out["torrents"].([]any)
	result := make(map[int64]map[string]any, len(torrents))
	for _, item := range torrents {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		idf, ok := m["id"].(float64)
		if !ok {
			continue
		}
		result[int64(idf)] = m
	}
	return result, nil
}

// SystemCommand 系统命令
// 注意：Transmission RPC 本身没有关机/重启能力，两者都只能停止会话（session-close），
// 由前端按语义提示用户；未知动作必须报错而不是静默执行。
func (c *Client) SystemCommand(ctx context.Context, action string) error {
	switch action {
	case "shutdown":
		_, err := c.RawCall(ctx, "session-close", map[string]any{})
		return err
	case "reboot":
		_, err := c.RawCall(ctx, "session-close", map[string]any{})
		return err
	default:
		return fmt.Errorf("不支持的系统命令: %s", action)
	}
}

// GetSessionGroups 获取带宽组列表（Transmission 4.x group-get）
func (c *Client) GetSessionGroups(ctx context.Context) ([]models.BandwidthGroup, error) {
	out, err := c.RawCall(ctx, "group-get", map[string]any{
		"fields": []string{"name", "speed-limit-down", "speed-limit-up",
			"speed-limit-down-enabled", "speed-limit-up-enabled", "honor-session-limits"},
	})
	if err != nil {
		return nil, err
	}
	arr, _ := out["groups"].([]any)
	groups := make([]models.BandwidthGroup, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		g := models.BandwidthGroup{}
		if v, ok := m["name"].(string); ok {
			g.Name = v
		}
		if v, ok := m["speed-limit-down"].(float64); ok {
			g.DownKB = int64(v)
		}
		if v, ok := m["speed-limit-up"].(float64); ok {
			g.UpKB = int64(v)
		}
		if v, ok := m["speed-limit-down-enabled"].(bool); ok {
			g.DownEnabled = v
		}
		if v, ok := m["speed-limit-up-enabled"].(bool); ok {
			g.UpEnabled = v
		}
		if v, ok := m["honor-session-limits"].(bool); ok {
			g.HonorsSessionLimits = v
		}
		if g.Name == "" {
			continue
		}
		groups = append(groups, g)
	}
	return groups, nil
}

// SetSessionGroup 创建或更新带宽组（Transmission 4.x group-set）。
// fields 仅允许 speed-limit-down / speed-limit-up / speed-limit-down-enabled /
// speed-limit-up-enabled / honor-session-limits，其余键拒绝。
func (c *Client) SetSessionGroup(ctx context.Context, name string, fields map[string]any) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("带宽组名不能为空")
	}
	allowed := map[string]bool{
		"speed-limit-down":         true,
		"speed-limit-up":           true,
		"speed-limit-down-enabled": true,
		"speed-limit-up-enabled":   true,
		"honor-session-limits":     true,
	}
	args := map[string]any{"name": strings.TrimSpace(name)}
	for k, v := range fields {
		if allowed[k] {
			args[k] = v
		}
	}
	_, err := c.RawCall(ctx, "group-set", args)
	return err
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}
