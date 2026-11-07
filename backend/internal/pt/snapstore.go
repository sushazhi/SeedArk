package pt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// 站点快照与考核目标的持久化。
//
// 为什么需要手工录入：站点侧的 uploaded/downloaded/魔力/等级/H&R 计数只存在于
// 站点账号页里，而本面板按设计**不登录站点**。所以这两块数据一律由用户抄一次，
// 面板负责存快照、算差值、标口径。参考 tracker-tracker 的做法：
// 「我们只记录站点返回的东西，不做跨站比较」——这里同样只记录，不推断。
//
// 保留策略：每站最多 maxSnapshotsPerSite 条、超过 maxSnapshotDays 天的丢弃，
// 否则长期使用后这个文件会无界增长。

const (
	maxSnapshotsPerSite = 400
	maxSnapshotDays     = 180
)

// SiteSnapshot 一次站点侧快照（手工登记）
type SiteSnapshot struct {
	Site           string  `json:"site"`
	At             int64   `json:"at"`
	Uploaded       int64   `json:"uploaded,omitempty"`
	Downloaded     int64   `json:"downloaded,omitempty"`
	Ratio          float64 `json:"ratio,omitempty"`
	Bonus          float64 `json:"bonus,omitempty"`     // 魔力/猫粮/UCoin
	SeedBonus      float64 `json:"seedBonus,omitempty"` // 做种积分
	LevelName      string  `json:"levelName,omitempty"`
	SeedingCount   int     `json:"seedingCount,omitempty"`
	SeedingSize    int64   `json:"seedingSize,omitempty"`
	HNRUnsatisfied int     `json:"hnrUnsatisfied,omitempty"`
	HNRPreWarning  int     `json:"hnrPreWarning,omitempty"`
	LastAccessAt   int64   `json:"lastAccessAt,omitempty"`
	Note           string  `json:"note,omitempty"`
}

// SnapshotDiff 本地口径与站点口径的比值。它不改任何数字，只用来告诉用户
// 「本地累计流量与站点统计差多少」——促销会放大、站点只计有效 peer。
//
// 时魔三件套（BonusPerHourMeasured / BonusPerHourFormula / BonusSumA）回答
// 「时魔如何精准获取」的三条独立通道，互相之间可以交叉验证：
//   - Measured：Δ魔力/Δ小时，纯实测、与公式无关（对魔改站也成立）；
//   - Formula：nexusphp 档本地复刻的 B（依赖公式参数与做种集合）；
//   - SumA：本地复刻的 ΣA，与页面 `(A = xx.x)` 比对——比 B 更灵敏（两条曲线都会把 A 的差压小）。
//
// 两者相差很远 → 说明站点改过公式或本地数据缺失，此时只能信 Measured / 手工抄录。
type SnapshotDiff struct {
	Site             string  `json:"site"`
	DaysSince        float64 `json:"daysSince"`
	LocalVsSiteRatio float64 `json:"localVsSiteRatio"`

	// BonusPerHourMeasured 快照差分实测时魔：最近两条带魔力读数的快照 (b₂−b₁)/Δh；0 = 算不出
	BonusPerHourMeasured float64 `json:"bonusPerHourMeasured,omitempty"`
	// BonusMeasuredHours 差分跨度（小时）；跨度 <1h 噪声太大、魔力下降=被花掉，都判不可比
	BonusMeasuredHours float64 `json:"bonusMeasuredHours,omitempty"`
	// BonusMeasuredOk 差分是否可信；false 时界面必须显示「不可比 + 原因」而不是 0
	BonusMeasuredOk bool `json:"bonusMeasuredOk,omitempty"`
	// BonusPerHourFormula nexusphp 档本地复刻的 B（×捐赠倍数）；0 = 该站不可复刻
	BonusPerHourFormula float64 `json:"bonusPerHourFormula,omitempty"`
	// BonusSumA 本地复刻的 ΣA（做种中集合）；对账锚点
	BonusSumA float64 `json:"bonusSumA,omitempty"`
	// BonusASumObserved 用户从页面抄的 `(A = xx.x)`；0 = 未抄，无从对账
	BonusASumObserved float64 `json:"bonusASumObserved,omitempty"`
}

// SnapshotsResponse GET /pt/snapshots
type SnapshotsResponse struct {
	Items []SiteSnapshot `json:"items"`
	Diff  []SnapshotDiff `json:"diff"`
}

// Exam 站点考核目标（用户自定义；代码里不内置任何站点的考核阈值）
type Exam struct {
	Site             string  `json:"site"`
	Name             string  `json:"name"`
	Deadline         int64   `json:"deadline,omitempty"`
	MinRatio         float64 `json:"minRatio,omitempty"`
	MinUploadGB      float64 `json:"minUploadGb,omitempty"`
	MinSeedingSizeGB float64 `json:"minSeedingSizeGb,omitempty"`
	MinSeedDays      float64 `json:"minSeedDays,omitempty"`
}

// ExamProgress 考核进度。全部本地量都是**下界**：本地累计上传 ≠ 站点统计，
// 促销会放大、站点只计有效 peer，所以界面必须标「至少这么多」而不是「就是这么多」。
type ExamProgress struct {
	LocalUploadGB      float64  `json:"localUploadGb"`
	LocalSeedingSizeGB float64  `json:"localSeedingSizeGb"`
	LocalRatio         float64  `json:"localRatio"`
	DaysLeft           float64  `json:"daysLeft"`
	Met                []string `json:"met"`
	Missing            []string `json:"missing"`
}

// ExamItem 一条考核 + 进度
type ExamItem struct {
	Exam
	Progress   ExamProgress `json:"progress"`
	LowerBound bool         `json:"lowerBound"`
	Note       string       `json:"note"`
}

// ExamResponse GET /pt/exams
type ExamResponse struct {
	Items []ExamItem `json:"items"`
}

type snapshotFile struct {
	Items []SiteSnapshot `json:"items"`
	Exams []Exam         `json:"exams"`
}

// snapStore 快照与考核的落盘。与采样/契约/档案各用一个文件，互不影响。
type snapStore struct {
	mu   sync.Mutex
	dir  string
	data snapshotFile
}

func newSnapStore(dir string) *snapStore {
	s := &snapStore{dir: dir, data: snapshotFile{Items: []SiteSnapshot{}, Exams: []Exam{}}}
	raw, err := os.ReadFile(filepath.Join(dir, "site-snapshots.json"))
	if err != nil {
		return s
	}
	var f snapshotFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return s
	}
	if f.Items == nil {
		f.Items = []SiteSnapshot{}
	}
	if f.Exams == nil {
		f.Exams = []Exam{}
	}
	s.data = f
	return s
}

func (s *snapStore) save() {
	raw, err := json.Marshal(s.data)
	if err != nil {
		return
	}
	path := filepath.Join(s.dir, "site-snapshots.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// add 登记一条快照，并按保留策略裁剪
func (s *snapStore) add(snap SiteSnapshot) SiteSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if snap.At <= 0 {
		snap.At = time.Now().Unix()
	}
	s.data.Items = append(s.data.Items, snap)
	cutoff := time.Now().AddDate(0, 0, -maxSnapshotDays).Unix()
	kept := make([]SiteSnapshot, 0, len(s.data.Items))
	for _, it := range s.data.Items {
		if it.At >= cutoff {
			kept = append(kept, it)
		}
	}
	// 每站最多保留 N 条：按站点分组后再截断，避免一个站点把别的站点挤掉
	bySite := map[string][]SiteSnapshot{}
	for _, it := range kept {
		bySite[it.Site] = append(bySite[it.Site], it)
	}
	out := make([]SiteSnapshot, 0, len(kept))
	for _, list := range bySite {
		sort.SliceStable(list, func(i, j int) bool { return list[i].At < list[j].At })
		if len(list) > maxSnapshotsPerSite {
			list = list[len(list)-maxSnapshotsPerSite:]
		}
		out = append(out, list...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At > out[j].At })
	s.data.Items = out
	s.save()
	return snap
}

func (s *snapStore) all() []SiteSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SiteSnapshot, len(s.data.Items))
	copy(out, s.data.Items)
	return out
}

// latest 每站最新一条
func (s *snapStore) latest() map[string]SiteSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]SiteSnapshot{}
	for _, it := range s.data.Items {
		if cur, ok := out[it.Site]; !ok || it.At > cur.At {
			out[it.Site] = it
		}
	}
	return out
}

func (s *snapStore) deleteSite(site string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := make([]SiteSnapshot, 0, len(s.data.Items))
	hit := false
	for _, it := range s.data.Items {
		if it.Site == site {
			hit = true
			continue
		}
		kept = append(kept, it)
	}
	if !hit {
		return false
	}
	s.data.Items = kept
	s.save()
	return true
}

func (s *snapStore) exams() []Exam {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Exam, len(s.data.Exams))
	copy(out, s.data.Exams)
	return out
}

func (s *snapStore) putExam(e Exam) Exam {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Exams {
		if s.data.Exams[i].Site == e.Site {
			s.data.Exams[i] = e
			s.save()
			return e
		}
	}
	s.data.Exams = append(s.data.Exams, e)
	s.save()
	return e
}

func (s *snapStore) deleteExam(site string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := make([]Exam, 0, len(s.data.Exams))
	hit := false
	for _, e := range s.data.Exams {
		if e.Site == site {
			hit = true
			continue
		}
		kept = append(kept, e)
	}
	if !hit {
		return false
	}
	s.data.Exams = kept
	s.save()
	return true
}
