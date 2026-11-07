package pt

import (
	"sort"
)

// 同内容分组的判定：这一组副本「是不是指向同一份数据」，决定它能不能省盘。
//
// 为什么要单拎出来：只看名称与体积时，同内容页给出的「冗余体积」在 PT 场景里
// 绝大多数是假的——辅种（同一条数据挂多个站的种子）删掉一个种子一字节都不释放。
// 判定依据是 contentPath（qB 的 content_path / TR 的 downloadDir+name）：
//
//	所有副本路径相同              → shared（辅种共用一份数据）：可省 0
//	同一台下载器内出现多条不同路径 → duplicate：可省 = 该机「除最大那份外」的体积
//	其余（跨机路径不一致 / 路径缺失）→ unknown：不下结论，也不给可省数字
//
// 逻辑写成纯函数，方便单测钉住每个分支。

// matrixOf 从台账行构建同内容分组。只做「名称归一化 + 路径判定」，不碰下载器。
func matrixOf(refs []TorrentRef) *Matrix {
	groups := map[string]*ContentGroup{}
	pathByKey := map[string]map[int64]string{}
	for _, r := range refs {
		key := normalizeName(r.Name)
		if key == "" {
			continue
		}
		g, ok := groups[key]
		if !ok {
			g = &ContentGroup{Key: key, Name: r.Name, Copies: []ContentCopy{}, Sites: []string{}}
			groups[key] = g
			pathByKey[key] = map[int64]string{}
		}
		if r.Size > g.Size {
			g.Size = r.Size
			g.Name = r.Name
		}
		g.Copies = append(g.Copies, ContentCopy{
			ID: r.ID, Site: r.Site, ServerName: r.ServerName, Kind: r.Kind,
			Status: r.Status, Size: r.Size, Uploaded: r.Uploaded30, Labels: r.Labels,
		})
		pathByKey[key][r.ID] = r.ContentPath
	}

	m := &Matrix{Groups: []ContentGroup{}}
	for key, g := range groups {
		if len(g.Copies) < 2 {
			continue
		}
		finishGroup(g, pathByKey[key])
		m.Groups = append(m.Groups, *g)
	}
	// 排序：真能省的排前面（按可省体积降序），其余按体积降序——让「值得看的」靠前
	sort.SliceStable(m.Groups, func(i, j int) bool {
		a, b := m.Groups[i], m.Groups[j]
		if a.ReclaimableBytes != b.ReclaimableBytes {
			return a.ReclaimableBytes > b.ReclaimableBytes
		}
		return a.TotalBytes > b.TotalBytes
	})
	if len(m.Groups) > 50 {
		m.Groups = m.Groups[:50]
	}
	for _, g := range m.Groups {
		m.Summary.ReclaimableBytes += g.ReclaimableBytes
		if g.Verdict == VerdictShared {
			m.Summary.SharedGroups++
		}
	}
	m.Summary.Groups = len(m.Groups)
	return m
}

// finishGroup 补全一组的汇总字段与「省不省盘」结论。
func finishGroup(g *ContentGroup, pathByID map[int64]string) {
	sites := map[string]bool{}
	serverCopies := map[string]int{}                 // 下载器 → 副本数
	serverPathBytes := map[string]map[string]int64{} // 下载器 → 路径 → 体积
	allPaths := map[string]bool{}
	missingPath := false

	for _, c := range g.Copies {
		g.TotalBytes += c.Size
		if c.Site != "" {
			sites[c.Site] = true
		}
		server := c.ServerName + "|" + c.Kind
		serverCopies[server]++
		p := pathByID[c.ID]
		if p == "" {
			missingPath = true
			continue
		}
		allPaths[p] = true
		if serverPathBytes[server] == nil {
			serverPathBytes[server] = map[string]int64{}
		}
		serverPathBytes[server][p] += c.Size
	}
	for s := range sites {
		g.Sites = append(g.Sites, s)
	}
	sort.Strings(g.Sites)
	g.MultiServer = len(serverCopies) > 1
	for _, n := range serverCopies {
		if n > 1 {
			g.SameDownloader = true
			break
		}
	}

	// 同一台下载器内出现多条不同内容路径 → 唯一能证明「存在两份独立数据」的信号
	var reclaimable int64
	sawMultiPath := false
	for _, paths := range serverPathBytes {
		if len(paths) < 2 {
			continue
		}
		sawMultiPath = true
		var total, maxBytes int64
		for _, b := range paths {
			total += b
			if b > maxBytes {
				maxBytes = b
			}
		}
		if total > maxBytes {
			reclaimable += total - maxBytes
		}
	}

	switch {
	case sawMultiPath:
		// 有确凿的重复证据，即使另有副本路径缺失也照报——但只报能证明的那部分
		g.Verdict = VerdictDuplicate
		g.ReclaimableBytes = reclaimable
	case !missingPath && len(allPaths) == 1:
		// 所有副本指向同一路径（跨客户端也成立）→ 辅种共用一份数据
		g.Verdict = VerdictShared
	default:
		// 跨机路径不一致，或路径缺失：既不能证明共用也不能证明重复
		g.Verdict = VerdictUnknown
	}
	sort.SliceStable(g.Copies, func(i, j int) bool { return g.Copies[i].Size > g.Copies[j].Size })
}
