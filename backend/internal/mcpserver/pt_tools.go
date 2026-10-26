package mcpserver

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sushazhi/seedark/backend/internal/pt"
)

// 保种台（PT Center）只读工具。
//
// 设计边界：全部基于「下载器全量种子 + 本地采样账本」，不访问任何站点、不需要
// 站点账号，也不修改任何种子——AI 客户端问「我的种子值多少、什么时候能删、
// 删了还能不能找回」时，这里都能回答，而释放时间与效率一律是估算值，
// 必须在返回值里带上 estimated / coverage 这类口径字段，不能让模型把估算当事实。

// ---- pt_seed_ledger ----

type ptLedgerIn struct {
	// Only 过滤欠佳资产。注意：描述里不要写 "key=value" 形式——
	// go-sdk 会把以 WORD= 开头的 jsonschema 标签当作指令，直接 panic。
	Only  string `json:"only,omitempty" jsonschema:"过滤范围：可选值 idle 表示连续无上传、invalid 表示站点已删种、contract 表示已设保种契约；留空返回全部"`
	Sort  string `json:"sort,omitempty" jsonschema:"排序字段：保种分（默认）、体积、连续无上传天数、上传效率"`
	Limit int    `json:"limit,omitempty" jsonschema:"返回的最大行数，默认 50，上限 500"`
}

type ptRow struct {
	Hash       string  `json:"hash"`
	Name       string  `json:"name"`
	Site       string  `json:"site"`
	ServerName string  `json:"serverName"`
	Size       int64   `json:"sizeBytes"`
	Status     string  `json:"status"`
	Ratio      float64 `json:"ratio"`
	SeedDays   float64 `json:"seedDays"`
	Uploaded30 int64   `json:"uploaded30dBytes"`
	Estimated  bool    `json:"uploaded30dEstimated"`
	Efficiency float64 `json:"efficiencyMbPerGbDay"`
	Score      int     `json:"keepScore"`
	IdleDays   int     `json:"idleDays"`
	HRRisk     bool    `json:"hrRisk"`
	// 今日增量与可比性：没有前一日基线时给 0 会像「今天没上传」，必须靠 quality 区分
	UploadedToday int64  `json:"uploadedTodayBytes"`
	TodayQuality  string `json:"todayQuality"`
	// 站点已删种：占着盘但永远不会有上传
	InvalidSeed   bool   `json:"invalidSeed"`
	InvalidReason string `json:"invalidReason,omitempty"`
	// 保种契约
	HasContract      bool     `json:"hasContract"`
	ContractUntil    int64    `json:"contractUntil,omitempty"`
	ContractDaysLeft int      `json:"contractDaysLeft,omitempty"`
	ContractBreach   bool     `json:"contractBreach"`
	Labels           []string `json:"labels,omitempty"`
}

func (s *Server) ptSeedLedger(ctx context.Context, _ *mcp.CallToolRequest, in ptLedgerIn) (*mcp.CallToolResult, any, error) {
	l, err := s.ptSvc.Ledger(ctx)
	if err != nil {
		return nil, nil, errors.New(err.Error())
	}
	rows := l.Rows
	switch in.Only {
	case "idle":
		rows = filterRows(rows, func(r pt.TorrentRef) bool { return r.IdleDays >= 7 })
	case "invalid":
		rows = filterRows(rows, func(r pt.TorrentRef) bool { return r.InvalidSeed })
	case "contract":
		rows = filterRows(rows, func(r pt.TorrentRef) bool { return r.HasContract })
	}
	switch in.Sort {
	case "size":
		sortRows(rows, func(a, b pt.TorrentRef) bool { return a.Size > b.Size })
	case "idle":
		sortRows(rows, func(a, b pt.TorrentRef) bool { return a.IdleDays > b.IdleDays })
	case "efficiency":
		sortRows(rows, func(a, b pt.TorrentRef) bool { return a.Efficiency > b.Efficiency })
	default:
		// Ledger 已按保种分排好
	}

	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	matched := len(rows)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	out := make([]ptRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, ptRow{
			Hash: r.Hash, Name: r.Name, Site: r.Site, ServerName: r.ServerName,
			Size: r.Size, Status: string(r.Status), Ratio: r.Ratio, SeedDays: r.SeedDays,
			Uploaded30: r.Uploaded30, Estimated: r.Estimated, Efficiency: r.Efficiency,
			Score: r.Score, IdleDays: r.IdleDays, HRRisk: r.HRRisk,
			UploadedToday: r.UploadedToday, TodayQuality: string(r.TodayQuality),
			InvalidSeed: r.InvalidSeed, InvalidReason: r.InvalidReason,
			HasContract: r.HasContract, ContractUntil: r.ContractUntil,
			ContractDaysLeft: r.ContractDaysLeft, ContractBreach: r.ContractBreach,
			Labels: r.Labels,
		})
	}
	cov := s.ptSvc.Coverage()
	ov, ovErr := s.ptSvc.Overview(ctx)
	today := map[string]any{
		"note": "今日增量严格对比前一自然日；quality 不是 exact 时该数字不可当作完整增量",
	}
	if ovErr == nil && ov != nil {
		today["uploadedBytes"] = ov.UploadedToday
		today["quality"] = string(ov.TodayQuality)
		today["detail"] = ov.TodayNote
	} else {
		today["note"] = "今日增量不可用：" + ovErr.Error()
	}
	return nil, map[string]any{
		"totals": l.Totals,
		"sites":  l.Sites,
		"today":  today,
		"coverage": map[string]any{
			"days":       cov.Days,
			"from":       cov.From,
			"full":       cov.Full,
			"lastSample": cov.LastSample,
			"note":       "采样不足 29 天时近 30 天上传为折算估算值",
		},
		"matched":  matched,
		"returned": len(out),
		"rows":     out,
	}, nil
}

func filterRows(in []pt.TorrentRef, keep func(pt.TorrentRef) bool) []pt.TorrentRef {
	out := make([]pt.TorrentRef, 0, len(in))
	for _, r := range in {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out
}

func sortRows(in []pt.TorrentRef, less func(a, b pt.TorrentRef) bool) {
	// 行数可达数千，用标准库排序即可（稳定性对展示无关紧要）
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && less(in[j], in[j-1]); j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}

// ---- pt_release_calendar ----

type ptCalendarIn struct {
	Month string `json:"month,omitempty" jsonschema:"月份 YYYY-MM，留空为当月"`
}

func (s *Server) ptReleaseCalendar(ctx context.Context, _ *mcp.CallToolRequest, in ptCalendarIn) (*mcp.CallToolResult, any, error) {
	cal, err := s.ptSvc.Calendar(ctx, in.Month)
	if err != nil {
		return nil, nil, errors.New(err.Error())
	}
	days := make([]map[string]any, 0, len(cal.Days))
	for _, d := range cal.Days {
		names := make([]string, 0, len(d.Torrents))
		for i, t := range d.Torrents {
			if i >= 20 {
				break
			}
			names = append(names, t.Name)
		}
		days = append(days, map[string]any{
			"date": d.Date, "count": d.Count, "bytes": d.Bytes,
			"ready": d.Ready, "hrRisk": d.HRRisk, "contractDue": d.ContractDue,
			"sampleTorrents": names,
		})
	}
	return nil, map[string]any{
		"month":   cal.Month,
		"summary": cal.Summary,
		"unknown": cal.Unknown,
		"days":    days,
		"note":    "可释放时间由站点规则推算，仅为预计值；本工具不会删除任何种子",
	}, nil
}

// ---- pt_vault_search ----

type ptVaultIn struct {
	Query string `json:"query,omitempty" jsonschema:"按名称 / infohash / 站点搜索，留空返回全部"`
	Limit int    `json:"limit,omitempty" jsonschema:"返回的最大条目数，默认 50，上限 200"`
}

func (s *Server) ptVaultSearch(_ context.Context, _ *mcp.CallToolRequest, in ptVaultIn) (*mcp.CallToolResult, any, error) {
	v := s.ptSvc.VaultList(in.Query)
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	entries := v.Entries
	if len(entries) > limit {
		entries = entries[:limit]
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"id": e.ID, "hash": e.Hash, "name": e.Name, "site": e.Site,
			"sizeBytes": e.Size, "deletedAt": e.DeletedAt, "seedDays": e.SeedDays,
			"ratio": e.Ratio, "uploadedEverBytes": e.UploadedEver,
			"labels": e.Labels, "savePath": e.SavePath,
			"hasTorrent": e.HasTorrent, "canRevive": e.CanRevive,
		})
	}
	return nil, map[string]any{
		"total": v.Total, "bytes": v.Bytes, "returned": len(out), "entries": out,
		"note": "档案只保留元数据与磁力；hasTorrent=false 时只能靠磁力重新添加（会重新下载）",
	}, nil
}

// ---- pt_content_matrix ----

type ptMatrixIn struct {
	Limit int `json:"limit,omitempty" jsonschema:"返回的最大分组数，默认 20，上限 100"`
}

func (s *Server) ptContentMatrix(ctx context.Context, _ *mcp.CallToolRequest, in ptMatrixIn) (*mcp.CallToolResult, any, error) {
	m, err := s.ptSvc.Matrix(ctx)
	if err != nil {
		return nil, nil, errors.New(err.Error())
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	groups := m.Groups
	if len(groups) > limit {
		groups = groups[:limit]
	}
	out := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		copies := make([]map[string]any, 0, len(g.Copies))
		for _, c := range g.Copies {
			copies = append(copies, map[string]any{
				"id": c.ID, "site": c.Site, "serverName": c.ServerName,
				"kind": c.Kind, "status": string(c.Status), "sizeBytes": c.Size,
				"uploadedBytes": c.Uploaded,
			})
		}
		out = append(out, map[string]any{
			"key": g.Key, "name": g.Name, "sizeBytes": g.Size,
			"totalBytes": g.TotalBytes, "duplicateBytes": g.DuplicateBytes,
			"sameDownloader": g.SameDownloader, "copies": copies,
		})
	}
	return nil, map[string]any{
		"summary": m.Summary, "returned": len(out), "groups": out,
		"note": "分组按名称近似匹配；同名不同版本（1080p 与 2160p）不会被合并，需人工确认",
	}, nil
}
