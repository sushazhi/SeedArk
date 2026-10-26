package pt

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 采样保留天数：略大于「近 30 天」窗口，留出跨月比对余量
const sampleKeepDays = 45

// sampleDay 某一天里各 hash 的 uploadedEver 高水位。
// 只记高水位而不是逐次采样，文件不会随轮询频率膨胀。
type sampleDay struct {
	Up map[string]int64 `json:"up"`
}

type sampleFile struct {
	Days   map[string]*sampleDay `json:"days"`
	LastAt int64                 `json:"lastAt"`
}

// contractFile 保种契约（①）：hash -> 承诺。独立文件，与采样/档案互不影响。
type contractFile struct {
	Items     map[string]Contract `json:"items"`
	UpdatedAt int64               `json:"updatedAt"`
}

// vaultRecord 档案的内部完整记录（含磁力与留档路径，不直接对外序列化）
type vaultRecord struct {
	VaultEntry
	Magnet      string      `json:"magnet"`
	TorrentPath string      `json:"torrentPath"`
	Files       []VaultFile `json:"files"`
}

type vaultFile struct {
	Records   []vaultRecord `json:"records"`
	UpdatedAt int64         `json:"updatedAt"`
}

// store PT 中心的本地持久化：采样与档案索引两个 JSON 文件 + vault/ 下的 .torrent。
// 全部字段原子写、权限 0600（磁力里可能带 passkey）。
type store struct {
	mu     sync.Mutex
	dir    string
	sample *sampleFile
	vault  *vaultFile
	// contracts 保种契约（①）
	contracts *contractFile

	lastSampleSave int64
}

func newStore(dataDir string) (*store, error) {
	dir := filepath.Join(dataDir, "pt")
	if err := os.MkdirAll(filepath.Join(dir, "vault"), 0o700); err != nil {
		return nil, fmt.Errorf("创建 PT 数据目录失败: %w", err)
	}
	s := &store{
		dir:       dir,
		sample:    &sampleFile{Days: map[string]*sampleDay{}},
		vault:     &vaultFile{Records: []vaultRecord{}},
		contracts: &contractFile{Items: map[string]Contract{}},
	}
	s.loadSample()
	s.loadVault()
	s.loadContracts()
	return s, nil
}

func (s *store) loadContracts() {
	data, err := os.ReadFile(filepath.Join(s.dir, "contracts.json"))
	if err != nil {
		return
	}
	var f contractFile
	if err := json.Unmarshal(data, &f); err != nil {
		slog.Warn("PT 保种契约文件解析失败，已从空契约继续", "err", err)
		return
	}
	if f.Items == nil {
		f.Items = map[string]Contract{}
	}
	s.contracts = &f
}

func (s *store) loadSample() {
	data, err := os.ReadFile(filepath.Join(s.dir, "samples.json"))
	if err != nil {
		return
	}
	var f sampleFile
	if err := json.Unmarshal(data, &f); err != nil {
		slog.Warn("PT 采样文件解析失败，已从空样本继续", "err", err)
		return
	}
	if f.Days == nil {
		f.Days = map[string]*sampleDay{}
	}
	s.sample = &f
}

func (s *store) loadVault() {
	data, err := os.ReadFile(filepath.Join(s.dir, "vault.json"))
	if err != nil {
		return
	}
	var f vaultFile
	if err := json.Unmarshal(data, &f); err != nil {
		slog.Warn("PT 档案索引解析失败，已从空档案继续", "err", err)
		return
	}
	if f.Records == nil {
		f.Records = []vaultRecord{}
	}
	s.vault = &f
}

// writeJSON 原子写：临时文件 + rename，权限 0600
func (s *store) writeJSON(name string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func dayKey(t time.Time) string { return t.Format("2006-01-02") }

// recordSample 把当前快照写入今天的采样（高水位），并按保留窗口清理。
// 返回本轮实际记录的种子数。
func (s *store) recordSample(now time.Time, ups map[string]int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := dayKey(now)
	day, ok := s.sample.Days[key]
	if !ok {
		day = &sampleDay{Up: map[string]int64{}}
		s.sample.Days[key] = day
	}
	n := 0
	for hash, up := range ups {
		if hash == "" {
			continue
		}
		if prev, ok := day.Up[hash]; !ok || up > prev {
			day.Up[hash] = up
			n++
		}
	}
	s.sample.LastAt = now.Unix()
	// 清理超出保留窗口的日期
	cutoff := now.AddDate(0, 0, -sampleKeepDays).Format("2006-01-02")
	for k := range s.sample.Days {
		if k < cutoff {
			delete(s.sample.Days, k)
		}
	}
	// 落盘节流：样本文件随种子数线性增长，每分钟全量重写没必要
	if now.Unix()-s.lastSampleSave >= 240 {
		if err := s.writeJSON("samples.json", s.sample); err != nil {
			slog.Warn("PT 采样落盘失败", "err", err)
		} else {
			s.lastSampleSave = now.Unix()
		}
	}
	return n
}

// baseline30 返回该 hash 在 since 之后最早一天的上传高水位。
// ok=false 表示还没有覆盖到窗口内的采样，调用方需退化为折算估算。
func (s *store) baseline30(hash string, now time.Time) (value int64, at time.Time, ok bool) {
	if hash == "" {
		return 0, time.Time{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := dayKey(now.AddDate(0, 0, -30))
	best := ""
	for k := range s.sample.Days {
		if k < cutoff || k > dayKey(now) {
			continue
		}
		if best == "" || k < best {
			best = k
		}
	}
	if best == "" {
		return 0, time.Time{}, false
	}
	v, exists := s.sample.Days[best].Up[hash]
	if !exists {
		return 0, time.Time{}, false
	}
	t, err := time.ParseInLocation("2006-01-02", best, now.Location())
	if err != nil {
		return 0, time.Time{}, false
	}
	return v, t, true
}

// flush 强制落盘（服务退出或手动采样时调用）
func (s *store) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writeJSON("samples.json", s.sample); err != nil {
		slog.Warn("PT 采样落盘失败", "err", err)
	} else {
		s.lastSampleSave = time.Now().Unix()
	}
}

// ---------- 保种契约（①） ----------

// contractAll 全部契约的副本（调用方只读，避免持锁期间做展示计算）
func (s *store) contractAll() map[string]Contract {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Contract, len(s.contracts.Items))
	for k, v := range s.contracts.Items {
		out[k] = v
	}
	return out
}

// contractGet 单条契约
func (s *store) contractGet(hash string) (Contract, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.contracts.Items[hash]
	return c, ok
}

// contractPut 写入/覆盖一条契约并落盘
func (s *store) contractPut(c Contract) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.contracts.Items == nil {
		s.contracts.Items = map[string]Contract{}
	}
	s.contracts.Items[c.Hash] = c
	s.contracts.UpdatedAt = time.Now().Unix()
	if err := s.writeJSON("contracts.json", s.contracts); err != nil {
		return err
	}
	return nil
}

// contractDelete 删除一条契约并落盘
func (s *store) contractDelete(hash string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.contracts.Items[hash]; !ok {
		return false, nil
	}
	delete(s.contracts.Items, hash)
	s.contracts.UpdatedAt = time.Now().Unix()
	if err := s.writeJSON("contracts.json", s.contracts); err != nil {
		return true, err
	}
	return true, nil
}

// ---------- 采样覆盖与活跃度（②） ----------

// coverage 采样窗口的覆盖情况。Days 不足 29 时，uploaded30d 必然是折算值，
// 界面要据此说明「≈」的可信度，而不是让用户对着一堆 ≈ 猜。
func (s *store) coverage(now time.Time) SampleCoverage {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := dayKey(now.AddDate(0, 0, -30))
	today := dayKey(now)
	days := 0
	from := ""
	for k := range s.sample.Days {
		if k < cutoff || k > today {
			continue
		}
		days++
		if from == "" || k < from {
			from = k
		}
	}
	return SampleCoverage{
		Days:       days,
		From:       from,
		To:         today,
		LastSample: s.sample.LastAt,
		Full:       days >= 29,
	}
}

// lastIncreaseDays 返回窗口内每个 hash「最近一次上传增长」发生在哪天，以及
// 该 hash 是否在采样里出现过（seen）。
//
// 只记高水位，因此增长 = 当天的值严格大于上一次记录的值；某天有值但没有变大
// 不算增长。seen 用来区分两种「没有增长」：确实一直没上传（seen=true），
// 与压根没有采样数据（seen=false，此时不该判定为僵尸种子）。
func (s *store) lastIncreaseDays(now time.Time) (last map[string]string, seen map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := dayKey(now.AddDate(0, 0, -30))
	today := dayKey(now)
	keys := make([]string, 0, len(s.sample.Days))
	for k := range s.sample.Days {
		if k < cutoff || k > today {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	last = map[string]string{}
	seen = map[string]bool{}
	prev := map[string]int64{}
	for _, k := range keys {
		day := s.sample.Days[k]
		if day == nil {
			continue
		}
		for hash, up := range day.Up {
			if !seen[hash] {
				seen[hash] = true
				prev[hash] = up
				continue
			}
			if up > prev[hash] {
				last[hash] = k
				prev[hash] = up
			}
		}
	}
	return last, seen
}

// ---------- 今日增量 ----------

// TodayDelta 今日相对「严格前一自然日」的上传增量。
//
// 口径纪律（对标 MoviePilot 的 SiteStatistic）：没有前一日基线时**不计算增量**，
// 也不把它当成 0——没有基线只是「不可比」，不是「没上传」；昨值为 0 时算出来的
// 是累计值而非增量，同样不采信；读数回退（计数器重置、换下载器）时不补算、
// 不推断，更不当成负增量。
//
// 一点不同：SiteStatistic 要给 PB 级读数标 ≈，是因为它读的是站点网页上三位小数
// 的显示值，最小步长约 1.02T。SeedArk 读的是下载器上报的字节级累计计数器，
// 不存在那个舍入问题，所以这里需要标注的只有「有没有可比基线」。
type TodayDelta struct {
	Known    map[string]int64 // hash -> 增量（仅含可信项）
	Unknown  map[string]bool  // hash -> 今日有读数但缺可比基线
	Rollback map[string]bool  // hash -> 读数回退，不计入
	Total    int64            // Known 的合计
	HasPrev  bool             // 是否存在前一自然日的采样
	PrevDay  string
	Today    string
}

func (s *store) todayDelta(now time.Time) TodayDelta {
	s.mu.Lock()
	defer s.mu.Unlock()
	today := dayKey(now)
	prev := dayKey(now.AddDate(0, 0, -1))
	d := TodayDelta{
		Known:    map[string]int64{},
		Unknown:  map[string]bool{},
		Rollback: map[string]bool{},
		Today:    today,
		PrevDay:  prev,
	}
	todayDay := s.sample.Days[today]
	prevDay := s.sample.Days[prev]
	d.HasPrev = prevDay != nil
	if todayDay == nil {
		return d
	}
	for hash, cur := range todayDay.Up {
		if prevDay == nil {
			d.Unknown[hash] = true
			continue
		}
		base, ok := prevDay.Up[hash]
		if !ok {
			// 前一日没有这颗种子：新加种或当时未被采到，无法判断增量
			d.Unknown[hash] = true
			continue
		}
		if cur < base {
			// 计数器重置或换了下载器：不补算、不当成负增量
			d.Rollback[hash] = true
			continue
		}
		delta := cur - base
		d.Known[hash] = delta
		d.Total += delta
	}
	return d
}

// ---------- 档案 ----------

func (s *store) vaultPut(rec vaultRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.vault.Records {
		if s.vault.Records[i].ID == rec.ID {
			// 重复留档（同一颗种子被多次删除）：元数据以最新一次为准，但文件清单与
			// 留档路径只在新记录确实带值时覆盖，避免第二次留档拉不到文件列表，
			// 把首次留下的清单清空（旧实现整条替换，注释与行为不符）
			if len(rec.Files) == 0 && len(s.vault.Records[i].Files) > 0 {
				rec.Files = s.vault.Records[i].Files
			}
			if rec.TorrentPath == "" && s.vault.Records[i].TorrentPath != "" {
				rec.TorrentPath = s.vault.Records[i].TorrentPath
			}
			if rec.Magnet == "" && s.vault.Records[i].Magnet != "" {
				rec.Magnet = s.vault.Records[i].Magnet
			}
			s.vault.Records[i] = rec
			s.vault.UpdatedAt = time.Now().Unix()
			return s.writeJSON("vault.json", s.vault)
		}
	}
	s.vault.Records = append(s.vault.Records, rec)
	s.vault.UpdatedAt = time.Now().Unix()
	return s.writeJSON("vault.json", s.vault)
}

func (s *store) vaultAll() []vaultRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]vaultRecord, len(s.vault.Records))
	copy(out, s.vault.Records)
	return out
}

func (s *store) vaultFind(id string) (vaultRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.vault.Records {
		if r.ID == id {
			return r, true
		}
	}
	return vaultRecord{}, false
}

func (s *store) vaultRemove(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.vault.Records {
		if s.vault.Records[i].ID == id {
			path := s.vault.Records[i].TorrentPath
			s.vault.Records = append(s.vault.Records[:i], s.vault.Records[i+1:]...)
			s.vault.UpdatedAt = time.Now().Unix()
			if err := s.writeJSON("vault.json", s.vault); err != nil {
				slog.Warn("PT 档案索引落盘失败", "err", err)
			}
			if path != "" {
				_ = os.Remove(path)
			}
			return true
		}
	}
	return false
}

// safeHash 把 infohash 收敛成安全文件名，避免路径穿越
func safeHash(hash string) string {
	h := strings.ToLower(strings.TrimSpace(hash))
	var b strings.Builder
	for _, r := range h {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return b.String()
}

// vaultTorrentPath 留档 .torrent 的预期路径。
// 只接受规范化的 40/64 位 hex infohash：早先只做「保留 hex 字符」过滤，非法输入会被
// 塌缩成单字符文件名（"../evil" 与 "e" 同名），不同种子可能互相覆盖；这里要求过滤
// 前后完全一致，既杜绝塌缩，也顺带保证不会拼出 vault 目录外的路径。
func (s *store) vaultTorrentPath(hash string) string {
	h := safeHash(hash)
	if len(h) != 40 && len(h) != 64 {
		return ""
	}
	if h != strings.ToLower(strings.TrimSpace(hash)) {
		return ""
	}
	return filepath.Join(s.dir, "vault", h+".torrent")
}

func (s *store) vaultStats() (count int, bytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.vault.Records {
		count++
		bytes += r.Size
	}
	return count, bytes
}

// sortRecordsNewest 档案按删除时间倒序，供列表接口使用
func sortRecordsNewest(recs []vaultRecord) {
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].DeletedAt > recs[j].DeletedAt })
}
