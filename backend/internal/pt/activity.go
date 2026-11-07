package pt

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// A 档剩下两项：活跃热力图（纯本地，ActivityDate 已有）与「有文件没做种」。

// ActivityHeat 7×24 活跃热力图：按「上次活动时间」落在哪个星期几/几点聚合成体积与颗数。
//
// 它回答的是「我的做种在上传活跃的时间和别人下载的时间对不对得上」——
// 如果全部集中在深夜，说明这些种子的 swarm 活跃期已过，可以优先放掉。
// 只用本地 ActivityDate，不访问任何站点。
type ActivityHeat struct {
	// Cells[day][hour]：day 0=周一 … 6=周日，hour 0-23
	Cells [][]HeatCell `json:"cells"`
	// PeakHour 最活跃的小时（0-23），-1 表示没有数据
	PeakHour int `json:"peakHour"`
	// DeadHours 完全没有活动的小时（升序）
	DeadHours []int  `json:"deadHours"`
	Note      string `json:"note"`
}

// HeatCell 一格：颗数与体积
type HeatCell struct {
	Count int   `json:"count"`
	Bytes int64 `json:"bytes"`
}

// Activity 生成活跃热力图（近 14 天内有活动的种子参与统计）
func (s *Service) Activity(ctx context.Context) (*ActivityHeat, error) {
	torrents, _, now, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	cells := make([][]HeatCell, 7)
	for d := range cells {
		cells[d] = make([]HeatCell, 24)
	}
	hours := make([]int64, 24)
	cutoff := now.AddDate(0, 0, -14).Unix()
	total := 0
	for _, t := range torrents {
		if t == nil || t.ActivityDate <= 0 || t.ActivityDate < cutoff {
			continue
		}
		at := time.Unix(t.ActivityDate, 0).In(now.Location())
		day := (int(at.Weekday()) + 6) % 7 // Go 的周日=0，这里改成周一=0
		cells[day][at.Hour()].Count++
		cells[day][at.Hour()].Bytes += t.TotalSize
		hours[at.Hour()] += t.TotalSize
		total++
	}
	out := &ActivityHeat{Cells: cells, PeakHour: -1, DeadHours: []int{}}
	if total == 0 {
		out.Note = "近 14 天没有活动记录，热力图暂不可用"
		return out, nil
	}
	var peak int64
	for h, v := range hours {
		if v > peak {
			peak, out.PeakHour = v, h
		}
		if v == 0 {
			out.DeadHours = append(out.DeadHours, h)
		}
	}
	out.Note = "按上次活动时间聚合（近 14 天）；只统计本地读数，不代表站点侧流量"
	return out, nil
}

// LoneItem「有文件但没在做种」的一条。
// 这是 NAS 场景最实在的浪费：内容占了盘，却没在任何地方产生上传。
// ptool 的 findalone 就是这个思路；我们只读本地目录 + 客户端种子清单，不做任何删除。
type LoneItem struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	Kind       string `json:"kind"` // dir / file
	ModifiedAt int64  `json:"modifiedAt"`
	// Guess 为什么判定它「没做种」：没有对应种子的路径
	Guess string `json:"guess"`
}

// LoneResult GET /pt/lone
type LoneResult struct {
	Roots   []string   `json:"roots"`
	Items   []LoneItem `json:"items"`
	Scanned int        `json:"scanned"`
	Note    string     `json:"note"`
}

// 扫描上限：目录可能巨大，只做「列出顶层目录、与做种路径比对」这么浅的一层。
// 深递归在 NAS 上动辄几十万文件，会把面板拖死，而收益不成比例。
const loneMaxItems = 200

// Lone 找出下载目录里「有内容但不在做种」的条目（只读，不删）。
//
// 判定很保守：只看做种任务保存路径的**顶层子项**，与所有做种的 SavePath 比较；
// 命中就跳过。任何一条都只作为「可能可以清理」的提示。
func (s *Service) Lone(ctx context.Context) (*LoneResult, error) {
	torrents, _, _, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	// 做种任务占用的路径：既收下载目录本身，也收「目录/种子名」这一层。
	// 前者是客户端把内容直接放进目录的情形，后者是常见的「每个种子一个子目录」。
	seeding := map[string]bool{}
	roots := map[string]bool{}
	for _, t := range torrents {
		if t == nil || t.DownloadDir == "" {
			continue
		}
		dir := filepath.Clean(t.DownloadDir)
		seeding[hashOfPath(dir)] = true
		roots[dir] = true
		if t.Name != "" {
			seeding[hashOfPath(filepath.Join(dir, t.Name))] = true
		}
	}
	out := &LoneResult{Roots: []string{}, Items: []LoneItem{}}
	for r := range roots {
		out.Roots = append(out.Roots, r)
	}
	sort.Strings(out.Roots)
	// 只扫「真正的保存根」：某根若是另一个根的子孙，它只是那个根里的一个子目录，
	// 扫它等于把同一片内容扫两遍（mock 里每颗种子的 DownloadDir 都不同，不去重会
	// 列出 12 个根、40 条候选，其中大半是同一棵树的父子关系）。
	out.Roots = pruneNestedRoots(out.Roots)
	for _, root := range out.Roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue // 无权限/不存在：跳过这个根，不影响其它
		}
		for _, e := range entries {
			if len(out.Items) >= loneMaxItems {
				break
			}
			full := filepath.Join(root, e.Name())
			if seeding[hashOfPath(full)] {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			size := info.Size()
			kind := "file"
			if e.IsDir() {
				kind = "dir"
				if sz, err := dirSize(full, 4); err == nil {
					size = sz
				}
			}
			out.Items = append(out.Items, LoneItem{
				Path:       full,
				Size:       size,
				Kind:       kind,
				ModifiedAt: info.ModTime().Unix(),
				Guess:      "该路径不在任何做种任务的保存路径下",
			})
			out.Scanned++
		}
	}
	sort.SliceStable(out.Items, func(i, j int) bool { return out.Items[i].Size > out.Items[j].Size })
	out.Note = "只比对做种任务的保存路径与下载目录顶层子项；仅提示，不会删除任何内容"
	if len(out.Roots) == 0 {
		out.Note = "没有可用的下载目录（种子未上报保存路径）"
	}
	return out, nil
}

// dirSize 递归求目录大小，depth 限制层数（4 层足够覆盖常见的
// 「种子名/Season/集数」结构，再深就超出一个条目该有的成本）。
func dirSize(root string, depth int) (int64, error) {
	if depth < 0 {
		return 0, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			if sz, err := dirSize(filepath.Join(root, e.Name()), depth-1); err == nil {
				total += sz
			}
			continue
		}
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	return total, nil
}

// hashOfPath 给「路径 → 种子」比对用的小工具：去掉多余分隔符与大小写差异
// （NAS 上常是大小写不敏感文件系统，直接字符串比较会漏判）。
func hashOfPath(p string) string {
	return strings.ToLower(filepath.Clean(p))
}

// pruneNestedRoots 去掉被其它根包含的路径，只留最外层。
// 输入已排序，所以「父」一定排在「子」前面，一遍线性扫描即可。
func pruneNestedRoots(sorted []string) []string {
	out := make([]string, 0, len(sorted))
	for _, p := range sorted {
		nested := false
		for _, keep := range out {
			if strings.HasPrefix(hashOfPath(p), hashOfPath(keep)+string(filepath.Separator)) {
				nested = true
				break
			}
		}
		if !nested {
			out = append(out, p)
		}
	}
	return out
}
