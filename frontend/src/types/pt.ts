// PT 中心对外类型：与 docs/pt-api-contract.md 一一对应（含 v3 增补）。
// 后端所有估算字段都带 windowDays/ageSource/swarm 这类口径字段，前端只做展示，不自行推算。

export type PTStatus = 'seeding' | 'downloading' | 'paused' | 'error' | 'other'

/** 今日增量可信度：exact 可比；partial 部分种子缺基线；unknown 无前一日采样；rollback 读数回退 */
export type PTDeltaQuality = 'exact' | 'partial' | 'unknown' | 'rollback'

export type PTReleaseReason = 'min_days' | 'hr' | 'ratio' | 'unknown'

/** 做种群体健康档：稀缺 → 建议继续保；未知 → 保守处理（不提示释放） */
export type PTSwarm = 'scarce' | 'normal' | 'abundant' | 'unknown'

/** 种子年龄来源：created = 站点发布时间（准）；client = 本地添加时间（会低估周龄） */
export type PTAgeSource = 'created' | 'client'

export interface PTTorrentRef {
  id: number
  name: string
  hash: string
  site: string
  serverName: string
  kind: string
  size: number
  seedDays: number
  ratio: number
  uploaded30d: number
  /**
   * uploaded30d 实际统计的天数（≤30）。上传量只报实测增量、不折算，
   * 所以靠它说明这个数覆盖了几天；0 = 采样不足一天，没有数据（显示「—」）。
   */
  windowDays: number
  // —— v3：稀缺度 / 释放安全 / 时魔 ——
  /** 主 tracker 汇报的做种人数；0 = 未知（私有站常不给 scrape） */
  seeders: number
  leechers: number
  /** 稀缺度 0-100，做种人数越少越高；未知按 100 */
  scarcity: number
  swarm: PTSwarm
  /** 义务已满足 且 swarm 不稀缺；scarce/unknown 一律 false */
  safeToRelease: boolean
  /** 人话解释，如「义务已满足 · 做种 12 人」 */
  releaseNote: string
  /** 种子年龄与来源：时魔周龄取自它，created 才准 */
  ageDays: number
  ageSource: PTAgeSource
  /** 按站点口径算出的单颗时魔与「时魔·GB」；0 = 没得算（见 bonusMethodKey） */
  bonusPerHour: number
  bonusPerGb: number
  /**
   * 口径说明（后端给人话，直出即可）：各站内核不同，同一个「时魔·GB」在不同站
   * 是不同东西（NPHP 估的单颗边际 / 抄录的站点时魔摊到体积），必须在列上标出来。
   */
  bonusMethod?: string
  /** 口径机读键：none / nexusphp / site / stale（site = 抄录档；stale = 只有每种固定加成） */
  bonusMethodKey?: PTBonusMethodKey
  savePath?: string
  efficiency: number
  score: number
  lastUpload: number
  status: PTStatus
  labels: string[]
  hrRisk: boolean
  hrDeadline: number
  releaseAt: number
  releaseReason: PTReleaseReason
  /** 距最近一次上传增长的连续天数；0 表示无采样数据、不做判断 */
  idleDays: number
  /** 今日相对前一自然日的上传增量；todayQuality 说明是否可比 */
  uploadedToday: number
  todayQuality: PTDeltaQuality
  /** 站点已删种（Tracker 宣告未注册）；占盘且不会再有上传 */
  invalidSeed: boolean
  invalidReason?: string
  /** 保种契约：承诺保种至的时间与剩余天数 */
  hasContract: boolean
  contractUntil: number
  contractDaysLeft: number
  contractBreach: boolean
}

/** 采样窗口覆盖情况：days 就是上传量统计了几天（0 = 不足一天、无数据） */
export interface PTSampleCoverage {
  days: number
  from: string
  to: string
  lastSample: number
  full: boolean
}

export interface PTContract {
  hash: string
  until: number
  note?: string
  createdAt: number
}

export interface PTContractRequest {
  hash: string
  days?: number
  until?: number
  note?: string
}

export interface PTSiteStat {
  site: string
  count: number
  size: number
  uploaded30d: number
  ratio: number
  /** 保种预算（字节）；0 表示该站未设预算 */
  budgetBytes: number
  overBudget: boolean
  /** 今日上传增量（严格对前一自然日）与可信度 */
  uploadedToday: number
  todayQuality: PTDeltaQuality
  /** v3.1 时魔列：配了口径的站显示实际每小时；未配口径的站字段缺省（显示「—」） */
  bonusSet?: boolean
  bonusPerHour?: number
  bonusCapPerHour?: number
  bonusEngine?: string
}

export interface PTSiteSize {
  site: string
  count: number
  size: number
}

export interface PTUpcomingDay {
  date: string
  count: number
  bytes: number
}

export interface PTOverview {
  torrentCount: number
  seedCount: number
  totalSize: number
  uploaded30d: number
  avgEfficiency: number
  hrRiskCount: number
  readyCount: number
  readyBytes: number
  vaultCount: number
  vaultBytes: number
  groupCount: number
  /** 同内容里真正可省的体积（只算能证明是两份独立数据的组） */
  reclaimableBytes: number
  /** 辅种组数：多站共用一份数据、不重复占盘 */
  sharedGroups: number
  sites: PTSiteStat[]
  upcoming: PTUpcomingDay[]
  generatedAt: number
  coverage: PTSampleCoverage
  /** v3 磁盘余量：释放最真实的触发条件是「盘要满了」 */
  disk: PTDiskStat
  /** v3 稀缺汇总（unknown 单列：不知道 ≠ 不稀缺） */
  scarcity: PTScarcityStat
  /** v3 时魔上限；configured=false 表示没填公式参数 */
  bonusCap: PTBonusCap
  /**
   * uploaded30d 这一列实际统计的天数（各行最大值，≤30）；0 = 没有任何一行有可用窗口。
   * 与 coverage.days 不是一回事：全库采样可能已跑 30 天，而种子全是刚加的。
   */
  windowDays: number
  idleCount: number
  idleBytes: number
  invalidCount: number
  invalidBytes: number
  contractCount: number
  contractBreach: number
  contractDueCount: number
  contractDueBytes: number
  overBudgetCount: number
  overBudgetBytes: number
  /** 概览级告警文案（②③⑦ 汇总） */
  warnings: string[]
  /** 今日上传增量：严格对比前一自然日；quality 不是 exact 时不可当作完整增量 */
  uploadedToday: number
  todayQuality: PTDeltaQuality
  todayNote: string
}

export interface PTLedgerTotals {
  count: number
  size: number
  uploaded30d: number
  avgEfficiency: number
  /** 窗口内实测零上传的种子数；没有采样数据的种子不计入 */
  zeroUpload: number
  /** uploaded30d 这一列实际统计的天数（各行最大值），表头照它写「近 N 天」；0 = 无数据 */
  windowDays: number
  idleCount: number
  invalidCount: number
  contractBreach: number
}

export interface PTLedger {
  rows: PTTorrentRef[]
  totals: PTLedgerTotals
  sites: PTSiteSize[]
}

export interface PTCalendarTorrent {
  id: number
  name: string
  site: string
  size: number
  seedDays: number
  hrRisk: boolean
  releaseAt: number
  reason: PTReleaseReason
}

export interface PTCalendarDay {
  date: string
  count: number
  bytes: number
  ready: boolean
  hrRisk: number
  torrents: PTCalendarTorrent[]
  /** 当日到期的保种契约数 */
  contractDue: number
}

export interface PTCalendarSummary {
  todayBytes: number
  weekBytes: number
  monthBytes: number
  hrCount: number
  contractDue: number
  contractBreach: number
}

export interface PTCalendar {
  month: string
  days: PTCalendarDay[]
  unknown: { count: number; bytes: number }
  summary: PTCalendarSummary
}

export interface PTVaultEntry {
  id: string
  hash: string
  name: string
  site: string
  size: number
  deletedAt: number
  seedDays: number
  ratio: number
  uploadedEver: number
  labels: string[]
  serverName: string
  kind: string
  savePath: string
  hasTorrent: boolean
  canRevive: boolean
}

export interface PTVaultFile {
  name: string
  size: number
}

export interface PTVaultList {
  entries: PTVaultEntry[]
  total: number
  bytes: number
}

export interface PTVaultDetail {
  entry: PTVaultEntry
  files: PTVaultFile[]
}

export interface PTContentCopy {
  id: number
  site: string
  serverName: string
  kind: string
  status: PTStatus
  size: number
  uploaded: number
  labels: string[]
}

/** 同内容判定：shared = 辅种共用一份数据（删了不省盘）；duplicate = 真有多份数据；unknown = 跨机/缺路径，不下结论 */
export type PTMatrixVerdict = 'shared' | 'duplicate' | 'unknown'

export interface PTContentGroup {
  key: string
  name: string
  size: number
  totalBytes: number
  /** 真正能省的体积；只有 verdict=duplicate 才非 0 */
  reclaimableBytes: number
  verdict: PTMatrixVerdict
  /** 这一组压着哪几个站的义务（风险视角） */
  sites: string[]
  sameDownloader: boolean
  /** 跨下载器：跨机路径体系不同，未做共用/重复比较 */
  multiServer: boolean
  copies: PTContentCopy[]
}

export interface PTMatrix {
  groups: PTContentGroup[]
  summary: { groups: number; reclaimableBytes: number; sharedGroups: number }
}

export interface PTSiteProfile {
  id: string
  name: string
  match: string
  minSeedDays: number
  minRatio: number
  hrSeedHours: number
  /** 保种体积预算（GB）；0 = 不设 */
  budgetGb: number
  enabled: boolean
  // —— v3 规则卡：站点侧的保号/H&R/时魔参数，全部由用户录入 ——
  /** H&R 豁免分享率（判定式是 上传量 ÷ 种子体积） */
  hrAmnestyRatio?: number
  /** H&R 观察窗（小时）：超窗未达标记一次未达标 */
  hrInspectHours?: number
  /** 累计未达标几次封号（只用于提示） */
  hrBanCount?: number
  deleteUnpackedDays?: number
  deletePackedDays?: number
  deleteNoTransferDays?: number
  // 时魔公式常数（NexusPHP mybonus.php）；全为 0 = 未配置，界面不估算
  bonusT0Weeks?: number
  bonusN0?: number
  bonusB0?: number
  bonusL?: number
  bonusPerSeeding?: number
  bonusMaxSeeding?: number
  /**
   * 官种匹配正则（对种子名匹配，大小写不敏感）。空 = 不区分官种，所有做种都计入数量项。
   * 有的站把数量项限成官种（Springsunday：「0.4 × 做种数，最多计 50 个不小于 3GB 的官种」），
   * 不录这一项本地时魔会偏高。
   */
  bonusOfficialPattern?: string
  /** 计入数量项的最小体积（GB）；0 = 不限。Springsunday 填 3 */
  bonusOfficialMinGb?: number
  /**
   * 时魔运算引擎。回答的是「这个站的时魔从哪来」，不是「公式长什么样」：
   * - none：不算，显示「—」（Gazelle 这类本来就没有时魔的站选它）
   * - nexusphp：按 NPHP 公式估单颗边际 A 值（只用于站内排序，校不到站方总时魔）
   * - manual：抄一个站点魔力页上的「你当前每小时能获取」，按本站做种体积均摊
   * 留空按 nexusphp 处理（v3 老配置语义不变）。
   */
  bonusEngine?: 'none' | 'nexusphp' | 'manual'
  /**
   * 公式曲线形态（只在 nexusphp 档有效）。同一条「A → B」的换算，各站魔改的差异有两层：
   * 常数不同是换参数，曲线不同是换函数——后者靠调 B0/L 永远凑不出来。
   * - atan（缺省）：B = B0×(2/π)×atan(A/L)，有上限 B0，原版 NexusPHP
   * - log：B = B0×ln(1+A/L)，无上限（Springsunday/春天 这类魔改站）
   */
  bonusCurve?: 'atan' | 'log'
  /**
   * 单颗 A 值的形态（只在 nexusphp 档有效）。曲线管「A 怎么变成 B」，形态管
   * 「A 怎么由体积/周龄/做种人数算出来」——站点两层都可能魔改。
   * - classic（缺省）：A_t = (1−10^(−周龄/T0)) × 体积GB × (1+√2×10^(−(做种人数−1)/(N0−1)))
   * - log：A_t = 体积GB × (0.25+0.32×ln(1+周龄)) × (0.12+2.88×10^(−⌈(做种人数−1)/5⌉/(N0−1)))
   */
  bonusAModel?: 'classic' | 'log'
  /** 手工抄录的站点侧时魔（点/小时）；manual 档必填 */
  bonusManualPerHour?: number
  /** 上面那个数的抄录时刻（unix 秒），界面标「N 天前抄的」 */
  bonusManualAt?: number
  /** 捐赠者「N 倍时魔」；0/缺省 = 非捐赠者。站点页面会打印「Donors always get N times of bonus」 */
  bonusDonorTimes?: number
  /** 从 mybonus.php 页面抄的 `(A = xx.x)`，用于与本地复刻的 ΣA 对账 */
  bonusASumObserved?: number
  /** 站点页加成百分比之和（等级 / 二级验证 / 持续在线等，加法叠加；如 府尹 6% + 2FA 10% → 16） */
  bonusExtraPct?: number
  /** 规则来源 URL 与最后核对时间（界面据此标「已核对 / 待核对 / 超 90 天」） */
  ruleSource?: string
  ruleCheckedAt?: number
  levels?: PTSiteLevel[]
}

export interface PTDetectedSite {
  site: string
  count: number
}

export interface PTSitesResponse {
  profiles: PTSiteProfile[]
  detected: PTDetectedSite[]
}

export interface PTPublishRequest {
  title: string
  name: string
  site: string
  category: string
  sizeBytes: number
  mediaInfo: string
  screenshots: string[]
  subtitle: string
  video: string
  audio: string
  resolution: string
  year: number
  country: string
  genre: string
  description: string
  lang?: string
}

export interface PTPublishPreview {
  bbcode: string
  warnings: string[]
}

export interface PTVaultReviveResult {
  id: number
  via: 'torrent' | 'magnet'
}

// ---------- v3 增补 ----------

export interface PTDiskStat {
  path: string
  freeBytes: number
  totalBytes: number
  usedPercent: number
  lowSpace: boolean
  targetFreeGb: number
}

export interface PTScarcityStat {
  scarceCount: number
  scarceBytes: number
  unknownCount: number
}

/**
 * 时魔口径机读键。同名一个「时魔·GB」列在不同站含义不同，界面靠它决定标签与配色：
 * - none：没得算（没录引擎 / 该站没有时魔）
 * - nexusphp：NPHP 公式估的单颗边际值（不能与站方对账）
 * - site：抄录的站点时魔 ÷ 本站在做种体积（唯一能与站方对账的口径）
 * - stale：公式常数不全，只算得出「每种固定加成」那一项
 */
export type PTBonusMethodKey = 'none' | 'nexusphp' | 'site' | 'stale'

/** 站点的时魔口径引擎：none 不显示 / nexusphp 公式估 / manual 用户抄站点时魔 */
export type PTSiteBonusEngine = 'none' | 'nexusphp' | 'manual'

export interface PTBonusCap {
  perHour: number
  configured: boolean
  /** 取得该上限的站点展示名（configured=false 时为空） */
  bestSite?: string
  /** 该站用的引擎：none / nexusphp / manual */
  engine?: string
  /** 所有站里配了时魔口径的站数（配合总站数说明覆盖率） */
  configCount: number
  /**
   * 各配置站**实际每小时**的合计（nexusphp=本地复刻 B(ΣA,颗数)，manual=抄录值）。
   * 头条应该用它——上限（perHour）是「打满」的数，跟站点页显示的当前值对不上，
   * 用户会以为面板算错了。
   */
  totalPerHour: number
  /** 每站明细（估 / 上限 / ΣA 对账 / 是否核对过） */
  sites?: PTBonusSiteCard[]
}

export interface PTBonusSiteCard {
  site: string
  engine: string
  /** 实际每小时 */
  perHour: number
  /** nexusphp 理论上限 */
  capPerHour?: number
  /** 本地复刻 ΣA（与站点的 (A = xx.x) 对账） */
  localSumA?: number
  /** 本地在做种颗数 */
  count?: number
  /** 站点页抄的 (A = xx.x)；0 = 未抄 */
  observedA?: number
  /** 规则 90 天内核对过 */
  verified: boolean
}

export interface PTEvictionTrigger {
  freeBytes: number
  targetFreeGb: number
  needBytes: number
  low: boolean
}

export interface PTEvictionCandidate {
  hash: string
  name: string
  site: string
  size: number
  seeders: number
  scarcity: number
  bonusPerGb: number
  /**
   * 该站在候选集里的相对位置（0=本站最该先放，1=本站最后才放）。
   * 排序按它而不是 bonusPerGb 的绝对值：各站货币不可通约，绝对值只在站内有意义。
   */
  bonusEff?: number
  efficiency: number
  safeToRelease: boolean
  reason: string
  /** 删到这一行为止能腾出多少，界面直接显示「删到第 N 行可腾出 X」 */
  cumulativeBytes: number
}

export interface PTEviction {
  trigger: PTEvictionTrigger
  candidates: PTEvictionCandidate[]
  totalBytes: number
  note: string
}

export interface PTHeatCell {
  count: number
  bytes: number
}

export interface PTActivity {
  /** cells[day][hour]，day 0=周一 */
  cells: PTHeatCell[][]
  peakHour: number
  deadHours: number[]
  note: string
}

export interface PTLoneItem {
  path: string
  size: number
  kind: 'dir' | 'file'
  modifiedAt: number
  guess: string
}

export interface PTLone {
  roots: string[]
  items: PTLoneItem[]
  scanned: number
  note: string
}

export interface PTSiteLevel {
  name: string
  weeks: number
  downloadedGb: number
  ratio: number
  bonus?: number
  seedBonus?: number
  uploads?: number
  kept?: boolean
  /** 非空表示「任一满足即可」的双路径（PTer 式） */
  alternatives?: PTSiteLevel[]
}

export interface PTSnapshot {
  site: string
  at: number
  uploaded?: number
  downloaded?: number
  ratio?: number
  bonus?: number
  seedBonus?: number
  levelName?: string
  seedingCount?: number
  seedingSize?: number
  hnrUnsatisfied?: number
  hnrPreWarning?: number
  lastAccessAt?: number
  note?: string
}

export interface PTSnapshotDiff {
  site: string
  daysSince: number
  /** 本地累计 ÷ 站点累计：告诉用户「本地口径与站点差多少」 */
  localVsSiteRatio: number
  /**
   * 时魔三通道（互相独立、可交叉验证）：
   * - measured：Δ魔力/Δ小时，纯实测（对魔改公式站也成立）
   * - formula：nexusphp 档本地复刻的 B（依赖公式参数 + 做种集合）
   * - sumA：本地复刻的 ΣA，与页面 `(A = xx.x)` 比对——比 B 灵敏（B 被 atan 饱和压缩）
   * formula 与 sumA/observed 相差很远 → 站点改过公式或数据缺失，只能信 measured / 手工抄录
   */
  bonusPerHourMeasured?: number
  /** 差分跨度（小时） */
  bonusMeasuredHours?: number
  /** 差分是否可信（跨度 ≥1h 且魔力未下降）；false 时显示「不可比」而不是 0 */
  bonusMeasuredOk?: boolean
  /** nexusphp 档本地复刻的 B（点/小时）；0 = 该站不可复刻 */
  bonusPerHourFormula?: number
  /** 本地复刻的 ΣA（对账锚点） */
  bonusSumA?: number
  /** 用户从页面抄的 `(A = xx.x)`；0 = 未抄 */
  bonusASumObserved?: number
}

export interface PTSnapshots {
  items: PTSnapshot[]
  diff: PTSnapshotDiff[]
}

export interface PTExam {
  site: string
  name: string
  deadline?: number
  minRatio?: number
  minUploadGb?: number
  minSeedingSizeGb?: number
  minSeedDays?: number
}

export interface PTExamProgress {
  localUploadGb: number
  localSeedingSizeGb: number
  localRatio: number
  daysLeft: number
  met: string[]
  missing: string[]
}

export interface PTExamItem extends PTExam {
  progress: PTExamProgress
  /** 恒定 true：本地累计永远是站点统计的下界 */
  lowerBound: boolean
  note: string
}

export interface PTExams {
  items: PTExamItem[]
}

export interface PTSettings {
  scarceSeeders: number
  releaseTargetFreeGb: number
}
