package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Server 一台下载器服务端的连接配置。
// Type 为下载器类型（transmission / qbittorrent），空值按 Transmission 处理，
// 这样既有部署的状态文件无需迁移即可继续工作。
// Pass 在 qBittorrent 下既可以是密码，也可以是 5.2+ 的 API Key（qbt_ 前缀）。
type Server struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	URL     string `json:"url"`
	User    string `json:"user"`
	Pass    string `json:"pass"`
	Enabled bool   `json:"enabled"`
}

// AutoMoveRule 自动文件管理规则
type AutoMoveRule struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Enabled   bool     `json:"enabled"`
	Sites     []string `json:"sites"`     // Tracker 站点（主机名）
	Labels    []string `json:"labels"`    // 标签
	NameMatch string   `json:"nameMatch"` // 名称包含
	TargetDir string   `json:"targetDir"`
}

// 做种策略动作
const (
	PolicyActionPause      = "pause"
	PolicyActionDelete     = "delete"
	PolicyActionDeleteData = "deleteData"
)

// SeedPolicyRule 做种策略：按站点/标签/名称圈定范围，达到分享率等目标后执行动作。
// MinRatio 等达标条件填 0 表示不参与判断，但至少需配置一个，否则规则无意义。
type SeedPolicyRule struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Enabled     bool     `json:"enabled"`
	Sites       []string `json:"sites"`     // Tracker 站点（主机名）
	Labels      []string `json:"labels"`    // 标签
	NameMatch   string   `json:"nameMatch"` // 名称包含
	MinRatio    float64  `json:"minRatio"`
	MinSeedDays float64  `json:"minSeedDays"`
	MinUploadGB float64  `json:"minUploadGB"`
	Action      string   `json:"action"` // pause / delete / deleteData
}

// SeedPolicyGuard 做种策略全局安全保护，对所有规则生效
type SeedPolicyGuard struct {
	// Enforce 需显式开启，策略才会真正动作；关闭时只写预览记录。
	// 取这个方向是为了让「从未配置」落在安全的一侧。
	Enforce       bool     `json:"enforce"`
	MinSeedHours  float64  `json:"minSeedHours"`  // 全局最低做种时长（小时），0 表示不设下限
	ExcludeSites  []string `json:"excludeSites"`  // 排除的站点
	ExcludeLabels []string `json:"excludeLabels"` // 排除的标签
}

// SeedPolicyReasonPart 一条达标依据的结构化片段，由界面按语言渲染成文案
type SeedPolicyReasonPart struct {
	Kind   string  `json:"kind"`   // ratio / days / upload
	Actual float64 `json:"actual"` // 实际值（分享率 / 做种天数 / 上传 GB）
	Target float64 `json:"target"` // 规则设定的目标值
}

// SeedPolicyLog 策略执行记录，供界面回看引擎对哪些种子做了什么
type SeedPolicyLog struct {
	Time    int64                  `json:"time"`
	Rule    string                 `json:"rule"`
	Torrent string                 `json:"torrent"`
	Site    string                 `json:"site"`
	Action  string                 `json:"action"`
	Reason  []SeedPolicyReasonPart `json:"reason"`
	DryRun  bool                   `json:"dryRun"`
}

// 组内总限速方向
const (
	SpeedDirectionDown = "down" // 下载
	SpeedDirectionUp   = "up"   // 上传
)

// SpeedPolicyRule 分组限速规则：命中站点/标签/名称的一组种子共享总速度上限。
// 下载与上传可同时设置；填 0 表示该方向不限制（引擎不接管该方向）。
// 引擎每轮把「上限减去组内手动限速种子实际占用」后的余量均分给
// 组内运行中且未单独限速的种子，达到整组总和封顶的效果。
type SpeedPolicyRule struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Enabled   bool     `json:"enabled"`
	DownLimit int64    `json:"downLimit"` // KB/s，组内下载总上限；0 = 不限
	UpLimit   int64    `json:"upLimit"`   // KB/s，组内上传总上限；0 = 不限
	Sites     []string `json:"sites"`     // Tracker 站点（与做种策略同口径）
	Labels    []string `json:"labels"`    // 标签
	NameMatch string   `json:"nameMatch"` // 名称包含
}

// SpeedPolicyGuard 组内总限速引擎开关
type SpeedPolicyGuard struct {
	Enforce bool `json:"enforce"` // 关闭时引擎不触碰任何种子
}

// SpeedApplied 引擎对某个种子的接管记录。
// Honors 保存接管前该种子「跟随全局限速」的原值：下发单种限速前必须关掉它
// （honors 为 true 时 Transmission 会忽略本种子的限速字段），释放时再按原值还原，
// 否则会把原本跟随全局限速的种子改成完全不限速。
type SpeedApplied struct {
	Down   int64 `json:"down"`   // KB/s，0 表示该方向未接管
	Up     int64 `json:"up"`     // KB/s
	Honors bool  `json:"honors"` // 接管前 honorsSessionLimits 的值
}

// State 持久化状态
type State struct {
	Servers          []Server          `json:"servers"`
	ActiveServer     int               `json:"activeServer"`
	AutoMoveRules    []AutoMoveRule    `json:"autoMoveRules"`
	SeedPolicyRules  []SeedPolicyRule  `json:"seedPolicyRules"`
	SeedPolicyGuard  SeedPolicyGuard   `json:"seedPolicyGuard"`
	SeedPolicyLogs   []SeedPolicyLog   `json:"seedPolicyLogs"`
	SpeedPolicyRules []SpeedPolicyRule `json:"speedPolicyRules"`
	SpeedPolicyGuard SpeedPolicyGuard  `json:"speedPolicyGuard"`
	// SpeedPolicyApplied 引擎最近一轮下发给各种子（方向 down/up）的限速 KB/s，
	// 以及接管前是否跟随全局限速。持久化保存：服务重启后仍能区分
	// 「引擎设定」与「用户手动设定」的限速，只在确认是引擎写入时才释放。
	SpeedPolicyApplied map[int64]SpeedApplied `json:"speedPolicyApplied"`
	ProcessedMoves     map[string]string      `json:"processedMoves"`  // hashString -> time
	ProcessedPolicy    map[string]string      `json:"processedPolicy"` // ruleID+"\x00"+hashString -> time
}

// Applied 返回该种子的接管记录（未接管过返回 ok=false）
func (st *State) Applied(id int64) (SpeedApplied, bool) {
	if st.SpeedPolicyApplied == nil {
		return SpeedApplied{}, false
	}
	a, ok := st.SpeedPolicyApplied[id]
	return a, ok
}

// AppliedCap 返回引擎在某方向下发给该种子的限速（0 表示该方向未接管）
func (st *State) AppliedCap(id int64, dir string) int64 {
	a, ok := st.Applied(id)
	if !ok {
		return 0
	}
	if dir == SpeedDirectionUp {
		return a.Up
	}
	return a.Down
}

// SetAppliedCap 记录某方向的下发值。种子首次被接管时，honors 写入接管前的原值；
// 已有记录时保留原值不动（后面每轮看到的都是引擎自己关掉的 false）。
func (st *State) SetAppliedCap(id int64, dir string, cap int64, honors bool) {
	if st.SpeedPolicyApplied == nil {
		st.SpeedPolicyApplied = map[int64]SpeedApplied{}
	}
	a, ok := st.SpeedPolicyApplied[id]
	if !ok {
		a = SpeedApplied{Honors: honors}
	}
	if dir == SpeedDirectionUp {
		a.Up = cap
	} else {
		a.Down = cap
	}
	st.SpeedPolicyApplied[id] = a
}

// ClearAppliedDir 清除某方向的接管记录；两个方向都清空时删除整条记录
func (st *State) ClearAppliedDir(id int64, dir string) {
	a, ok := st.Applied(id)
	if !ok {
		return
	}
	if dir == SpeedDirectionUp {
		a.Up = 0
	} else {
		a.Down = 0
	}
	if a.Down <= 0 && a.Up <= 0 {
		delete(st.SpeedPolicyApplied, id)
		return
	}
	st.SpeedPolicyApplied[id] = a
}

// Store JSON 文件存储（线程安全）
type Store struct {
	mu   sync.RWMutex
	path string
	data *State
}

// Load 加载状态文件，不存在时创建默认空状态
func Load(path string) (*Store, error) {
	s := &Store{path: path}
	if data, err := os.ReadFile(path); err == nil {
		var st State
		if err := json.Unmarshal(data, &st); err != nil {
			return nil, err
		}
		st.normalize()
		s.data = &st
		return s, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	s.data = &State{
		Servers:          []Server{},
		AutoMoveRules:    []AutoMoveRule{},
		SeedPolicyRules:  []SeedPolicyRule{},
		SpeedPolicyRules: []SpeedPolicyRule{},
		ProcessedMoves:   map[string]string{},
		ProcessedPolicy:  map[string]string{},
	}
	_ = s.save()
	return s, nil
}

// normalize 补齐反序列化后可能缺失的映射表，并把下线动作降级到安全一侧
func (st *State) normalize() {
	if st.ProcessedMoves == nil {
		st.ProcessedMoves = map[string]string{}
	}
	if st.ProcessedPolicy == nil {
		st.ProcessedPolicy = map[string]string{}
	}
	if st.SpeedPolicyRules == nil {
		st.SpeedPolicyRules = []SpeedPolicyRule{}
	}
	if st.SpeedPolicyApplied == nil {
		st.SpeedPolicyApplied = map[int64]SpeedApplied{}
	}
	// 归档（move）已下线：旧规则改成暂停，避免被引擎静默跳过
	for i := range st.SeedPolicyRules {
		if st.SeedPolicyRules[i].Action == "move" {
			st.SeedPolicyRules[i].Action = PolicyActionPause
		}
	}
}

// Get 返回当前状态的深拷贝（调用方可自由修改，不影响内部数据）
func (s *Store) Get() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return *s.data.clone()
}

// clone 通过 JSON 往返复制状态（内部持有读锁时调用）
func (st *State) clone() *State {
	data, err := json.Marshal(st)
	if err != nil {
		return st
	}
	var cp State
	if err := json.Unmarshal(data, &cp); err != nil {
		return st
	}
	cp.normalize()
	return &cp
}

// Update 在锁内修改状态并持久化
func (s *Store) Update(fn func(*State)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s.data)
	return s.save()
}

func (s *Store) save() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(s.path); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	// 原子写入：先写临时文件再改名，避免进程中断导致损坏
	// 状态文件含服务器明文密码，权限收紧为仅属主可读写
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// DefaultStatePath 返回默认状态文件路径
func DefaultStatePath(dir string) string {
	if dir != "" {
		return filepath.Join(dir, "sa-state.json")
	}
	return "sa-state.json"
}

// PolicyKey 构造做种策略已处理标记键
func PolicyKey(ruleID, hashString string) string {
	return ruleID + "\x00" + hashString
}

// NowUnix 当前 Unix 时间戳
func NowUnix() int64 {
	return time.Now().Unix()
}
