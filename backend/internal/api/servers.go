package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/sushazhi/seedark/backend/internal/config"
	"github.com/sushazhi/seedark/backend/internal/driver"
	"github.com/sushazhi/seedark/backend/internal/models"
	"github.com/sushazhi/seedark/backend/internal/rpc"
	"github.com/sushazhi/seedark/backend/internal/state"
)

// listServers 获取服务器列表（脱敏密码）
func (h *Handler) listServers(c *gin.Context) {
	st := h.state.Get()
	out := make([]map[string]any, 0, len(st.Servers))
	for i, s := range st.Servers {
		out = append(out, map[string]any{
			"index":     i,
			"name":      s.Name,
			"type":      driver.NormalizeKind(s.Type).String(),
			"url":       s.URL,
			"user":      s.User,
			"hasPass":   s.Pass != "",
			"enabled":   s.Enabled,
			"diskTotal": s.DiskTotal,
		})
	}
	respond(c, gin.H{"servers": out, "activeServer": st.ActiveServer})
}

// serverInput 保存服务器列表的入参。Pass 用指针区分两种语义：
//   - nil（字段缺省）：未提交密码，沿用原值（列表接口不回传密码）
//   - 非 nil（含空串）：显式设置密码，空串表示清空
//
// 若无此区分，界面仅编辑地址或名称时（前端全量回传、密码字段留空）就会静默清空凭据。
// DiskTotal 同样用指针：列表接口虽会回传它，但缓存了旧版前端的客户端回传的列表里
// 没有这个键，按零值落盘会把用户手填的总容量悄悄抹掉。
type serverInput struct {
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	URL       string  `json:"url"`
	User      string  `json:"user"`
	Pass      *string `json:"pass"`
	Enabled   bool    `json:"enabled"`
	DiskTotal *int64  `json:"diskTotal"`
}

// saveServers 全量保存服务器列表
func (h *Handler) saveServers(c *gin.Context) {
	var body struct {
		Servers []serverInput `json:"servers"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	for i := range body.Servers {
		// 空地址行是尚未填完的草稿，必须收下：界面逐键保存整份列表，
		// 拒收会让「添加服务器」必然失败，本地留下后端不存在的幽灵行（删除它再报 400）
		// 切换服务器时会写入 .env.local（dotenv 按行解析），含换行即注入新的配置键
		for _, f := range []struct{ name, value string }{
			{"地址", body.Servers[i].URL},
			{"用户名", body.Servers[i].User},
		} {
			if err := config.ValidateEnvValue(f.name, f.value); err != nil {
				respondError(c, http.StatusBadRequest, fmt.Sprintf("第 %d 个服务器：%s", i+1, err.Error()))
				return
			}
		}
		if body.Servers[i].Pass != nil {
			if err := config.ValidateEnvValue("密码", *body.Servers[i].Pass); err != nil {
				respondError(c, http.StatusBadRequest, fmt.Sprintf("第 %d 个服务器：%s", i+1, err.Error()))
				return
			}
		}
		if t := body.Servers[i].DiskTotal; t != nil && *t < 0 {
			respondError(c, http.StatusBadRequest, fmt.Sprintf("第 %d 个服务器：磁盘总容量不能为负", i+1))
			return
		}
	}
	err := h.state.Update(func(st *state.State) {
		// 密码沿用采用两级匹配，兼顾两种编辑方式：
		//  1) 地址+名称相同 —— 覆盖「仅调整顺序」的场景（纯按索引回填会在重排后串号）
		//  2) 索引相同 —— 覆盖「改了地址或名称」的场景（此时按地址匹配必然落空）
		byKey := make(map[string]string, len(st.Servers))
		diskTotalByKey := make(map[string]int64, len(st.Servers))
		for _, s := range st.Servers {
			byKey[s.URL+"\x00"+s.Name] = s.Pass
			diskTotalByKey[s.URL+"\x00"+s.Name] = s.DiskTotal
		}
		next := make([]state.Server, 0, len(body.Servers))
		for i := range body.Servers {
			in := &body.Servers[i]
			s := state.Server{Name: in.Name, Type: driver.NormalizeKind(in.Type).String(), URL: in.URL, User: in.User, Enabled: in.Enabled}
			switch {
			case in.Pass != nil:
				s.Pass = *in.Pass
			default:
				if pass, ok := byKey[in.URL+"\x00"+in.Name]; ok {
					s.Pass = pass
				} else if i < len(st.Servers) {
					s.Pass = st.Servers[i].Pass
				}
			}
			switch {
			case in.DiskTotal != nil:
				s.DiskTotal = *in.DiskTotal
			default:
				if total, ok := diskTotalByKey[in.URL+"\x00"+in.Name]; ok {
					s.DiskTotal = total
				} else if i < len(st.Servers) {
					s.DiskTotal = st.Servers[i].DiskTotal
				}
			}
			next = append(next, s)
		}
		st.Servers = next
		switch {
		case len(st.Servers) == 0:
			// 列表已空：0 不是有效索引，置 -1 表示无活动服务器
			st.ActiveServer = -1
		case st.ActiveServer < 0 || st.ActiveServer >= len(st.Servers):
			st.ActiveServer = 0
		}
	})
	if err != nil {
		respondError(c, http.StatusInternalServerError, err.Error())
		return
	}
	h.SyncAggregateTargets()
	respond(c, gin.H{"updated": true})
}

// deleteServer 删除指定服务器。
// 若删掉的正是当前连接的服务器，必须显式热切换 RPC 客户端：连接不会被状态修改
// 自动带走，否则界面显示的是新的活动服务器、数据却仍来自已被删除的那台。
func (h *Handler) deleteServer(c *gin.Context) {
	idx, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "无效的服务器索引")
		return
	}
	var (
		deleted   bool
		wasActive bool
		next      rpc.Credentials
		hasNext   bool
	)
	err = h.state.Update(func(st *state.State) {
		if idx < 0 || idx >= len(st.Servers) {
			return
		}
		wasActive = st.ActiveServer == idx
		st.Servers = append(st.Servers[:idx], st.Servers[idx+1:]...)
		switch {
		case len(st.Servers) == 0:
			// 列表已空：0 不是有效索引，用 -1 表示当前没有活动服务器
			st.ActiveServer = -1
		case st.ActiveServer > idx:
			st.ActiveServer--
		case st.ActiveServer == idx:
			st.ActiveServer = 0
		}
		if st.ActiveServer >= 0 && st.ActiveServer < len(st.Servers) {
			if srv := st.Servers[st.ActiveServer]; srv.URL != "" {
				next = credentialsOf(srv)
				hasNext = true
			}
		}
		deleted = true
	})
	if !deleted {
		respondError(c, http.StatusBadRequest, "服务器不存在")
		return
	}
	var warnings []string
	if err != nil {
		warnings = append(warnings, "删除已生效但写入状态文件失败: "+err.Error())
	}
	if wasActive {
		switch {
		case !hasNext:
			warnings = append(warnings, "已删除当前服务器且没有可用的备用服务器，连接保持不变")
		case h.rpc.Reconfigure(next) != nil:
			warnings = append(warnings, "切换到备用服务器失败，连接仍指向已删除的服务器")
		default:
			if _, perr := h.rpc.Ping(c.Request.Context()); perr != nil {
				warnings = append(warnings, "已切换到备用服务器，但连接测试失败: "+perr.Error())
			}
			if werr := config.SaveLocalSettings(h.dataDir,
				h.currentLocalSettings(next, h.hub.getPollInterval().String())); werr != nil {
				warnings = append(warnings, "连接配置未能写入 .env.local，重启后仍会使用原地址")
			}
			h.hub.Bump()
		}
	}
	// 聚合成员必须按删除后的列表重建：members 以服务器索引为键，删掉一台会让后面
	// 所有条目的索引整体前移，不刷新就仍会去拉已被删除那台的种子（归属名还是它的），
	// 且对这些种子 ID 的写操作会路由到那台已删除的下载器上。
	// 放在 Reconfigure 之后：Reconfigure 会清空 members，先同步等于白做。
	h.SyncAggregateTargets()
	out := gin.H{"deleted": true}
	if len(warnings) > 0 {
		out["warning"] = strings.Join(warnings, "；")
	}
	respond(c, out)
}

// switchServer 切换到指定服务器（测试连通后热更新并持久化）
func (h *Handler) switchServer(c *gin.Context) {
	var body struct {
		Index int `json:"index"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	st := h.state.Get()
	if body.Index < 0 || body.Index >= len(st.Servers) {
		respondError(c, http.StatusBadRequest, "服务器不存在")
		return
	}
	srv := st.Servers[body.Index]
	if srv.URL == "" {
		respondError(c, http.StatusBadRequest, "该服务器未填写地址")
		return
	}
	if !srv.Enabled {
		respondError(c, http.StatusBadRequest, "该服务器未启用")
		return
	}
	// 记录当前配置以便失败回滚
	old := h.rpc.Credentials()

	// 热更新客户端（按服务器类型切换到对应驱动）
	cred := credentialsOf(srv)
	if err := h.rpc.Reconfigure(cred); err != nil {
		respondError(c, http.StatusBadGateway, "切换失败: "+err.Error())
		return
	}
	// 测试连通性
	version, err := h.rpc.Ping(c.Request.Context())
	if err != nil {
		_ = h.rpc.Reconfigure(old)
		// Reconfigure 会把聚合成员整批作废，回滚后必须按当前配置重建：
		// 否则后续列表只剩活动一台的种子，直到下次触发热更新才恢复
		h.SyncAggregateTargets()
		respondErrorCode(c, http.StatusBadGateway, models.ErrCodeServerUnreachable, "无法连接该服务器，已回滚: "+err.Error())
		return
	}
	// 持久化：内存已切换成功，写盘失败不回滚连接，但必须显式告知用户重启后会回滚
	var warnings []string
	if err := h.state.Update(func(st2 *state.State) { st2.ActiveServer = body.Index }); err != nil {
		slog.Warn("持久化活动服务器失败", "index", body.Index, "err", err)
		warnings = append(warnings, "活动服务器未能写入状态文件，重启后会回到原服务器")
	}
	// 必须携带全部受管键：.env.local 是整文件重写，漏传的键会被写成空值
	// （曾漏传 MCP_TOKEN，导致切换服务器后 MCP 变成无鉴权端点）
	if err := config.SaveLocalSettings(h.dataDir,
		h.currentLocalSettings(cred, h.hub.getPollInterval().String())); err != nil {
		slog.Warn("持久化连接配置失败", "err", err)
		warnings = append(warnings, "连接配置未能写入 .env.local，重启后仍会使用原地址")
	}
	// 服务器列表/类型可能已变，重新计算聚合成员
	h.SyncAggregateTargets()
	// 切换后立即触发一次拉取
	h.hub.Bump()
	out := gin.H{"index": body.Index, "version": version}
	if len(warnings) > 0 {
		out["warning"] = strings.Join(warnings, "；")
	}
	respond(c, out)
}
