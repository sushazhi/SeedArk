package qbittorrent

// 版本兼容层：把「上游 Web API 版本差异」的处理集中到这一个文件，
// 业务方法只声明端点候选链或版本门卫，不感知具体版本号。
//
// 未来 qBittorrent 6.x / WebAPI 2.16+ 适配时按三种模式扩展，无需改动业务代码：
//
//  1. 端点改名（如 5.0 把 pause/resume 改成 stop/start）：
//     把新端点追加到 postFallback 候选链头部，旧端点留在链尾。
//     旧版本对未知端点回 404，链自动回退并记忆（本进程内不再浪费往返）：
//
//	c.postFallback(ctx, form, "torrents/新名", "torrents/旧名")
//
//  2. 新版本独有的端点或参数：先 webAPIAtLeast 门卫再走新路径，
//     旧版本走保守路径，互不干扰：
//
//	if ok, err := c.webAPIAtLeast(ctx, 2, 16); err == nil && ok {
//	    return c.post(ctx, "torrents/新能力", form)
//	}
//	return c.postFallback(ctx, form, "旧端点A", "旧端点B")
//
//  3. 响应格式变更（如 5.2 的 torrents/add 返回 added_torrent_ids JSON）：
//     按内容自适应解析，参照 torrent.go 的 parseAddReport——
//     新格式解析得出走新路径，非 JSON / 解析不出回退旧逻辑。

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// endpointGoneTTL 端点缺失记忆的有效期。
// 服务器可能在进程存活期间升级，记忆过期后重新探测以免永久退化；
// Ping 成功也会主动清空记忆（视为「对端状态已刷新」）。
const endpointGoneTTL = 30 * time.Minute

// postFallback 依次尝试同义端点候选链（新版本在前，旧版本在后）：
//   - 命中即返回 nil；
//   - HTTP 404（errEndpointMissing）→ 记忆该端点缺失，尝试下一个；
//   - 其他错误（网络 / 409 / 415 等）原样返回，不回退——只有「端点不存在」
//     才是版本信号，业务错误静默降级会掩盖真实问题；
//   - 候选链里被记忆的端点直接跳过（30 分钟过期重试）；若全部被记忆，
//     仍会强制尝试最后一个，保证操作有自愈路径。
func (c *Client) postFallback(ctx context.Context, form url.Values, endpoints ...string) error {
	if len(endpoints) == 0 {
		return errors.New("端点候选链为空")
	}
	// 统计被记忆缺失的候选；全部缺失时强制尝试最后一个（自愈路径）
	missing := 0
	for _, ep := range endpoints {
		if c.knownMissing(ep) {
			missing++
		}
	}
	var lastErr error
	for i, ep := range endpoints {
		if c.knownMissing(ep) && !(missing == len(endpoints) && i == len(endpoints)-1) {
			continue
		}
		err := c.post(ctx, ep, form)
		if err == nil {
			return nil
		}
		if errors.Is(err, errEndpointMissing) {
			c.noteMissing(ep)
			lastErr = err
			continue
		}
		return err
	}
	return lastErr
}

// postKnown 单端点请求 + 缺失记忆：404 时记忆并返回 errEndpointMissing，
// 供调用方走替代动作（如 setTags → addTags/removeTags 差集回退）。
func (c *Client) postKnown(ctx context.Context, endpoint string, form url.Values) error {
	if c.knownMissing(endpoint) {
		return fmt.Errorf("%s：%w", endpoint, errEndpointMissing)
	}
	err := c.post(ctx, endpoint, form)
	if errors.Is(err, errEndpointMissing) {
		c.noteMissing(endpoint)
	}
	return err
}

// knownMissing 端点是否被记忆为缺失（未过期）
func (c *Client) knownMissing(endpoint string) bool {
	c.goneMu.RLock()
	defer c.goneMu.RUnlock()
	at, ok := c.endpointGone[endpoint]
	return ok && time.Since(at) < endpointGoneTTL
}

// noteMissing 记忆端点缺失
func (c *Client) noteMissing(endpoint string) {
	c.goneMu.Lock()
	defer c.goneMu.Unlock()
	if c.endpointGone == nil {
		c.endpointGone = map[string]time.Time{}
	}
	c.endpointGone[endpoint] = time.Now()
}

// clearMissing 清空端点缺失记忆（Ping 成功时调用；单测亦可直接使用）
func (c *Client) clearMissing() {
	c.goneMu.Lock()
	defer c.goneMu.Unlock()
	c.endpointGone = map[string]time.Time{}
}

// webAPIAtLeast 判断远端 Web API 版本是否 >= major.minor。
// 版本在服务器运行期间不变，首次探测后缓存；探测失败返回错误，
// 调用方应走保守路径（回退链），不要把失败当成「版本过旧」。
func (c *Client) webAPIAtLeast(ctx context.Context, major, minor int) (bool, error) {
	c.compatMu.Lock()
	maj, min, known := c.webAPIMajor, c.webAPIMinor, c.webAPIKnown
	c.compatMu.Unlock()
	if !known {
		var raw string
		if err := c.get(ctx, "app/webapiVersion", nil, &raw); err != nil {
			return false, err
		}
		var err error
		maj, min, err = parseWebAPIVersion(raw)
		if err != nil {
			return false, err
		}
		c.compatMu.Lock()
		c.webAPIMajor, c.webAPIMinor, c.webAPIKnown = maj, min, true
		c.compatMu.Unlock()
	}
	return maj > major || (maj == major && min >= minor), nil
}

// parseWebAPIVersion 解析 "2.15.1" / "v2.15.1" → (2, 15)；补丁位不参与比较
func parseWebAPIVersion(s string) (major, minor int, err error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("WebAPI 版本格式无效: %q", s)
	}
	major, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("WebAPI 主版本无效: %q", s)
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("WebAPI 次版本无效: %q", s)
	}
	return major, minor, nil
}
