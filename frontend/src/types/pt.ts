// PT 中心对外类型：与 docs/pt-api-contract.md v1 一一对应。
// 后端所有估算字段都带 estimated/releaseAt=0 语义，前端只做展示，不自行推算。

export type PTStatus = 'seeding' | 'downloading' | 'paused' | 'error' | 'other'

/** 今日增量可信度：exact 可比；partial 部分种子缺基线；unknown 无前一日采样；rollback 读数回退 */
export type PTDeltaQuality = 'exact' | 'partial' | 'unknown' | 'rollback'

export type PTReleaseReason = 'min_days' | 'hr' | 'ratio' | 'unknown'

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
  efficiency: number
  score: number
  lastUpload: number
  status: PTStatus
  labels: string[]
  hrRisk: boolean
  hrDeadline: number
  releaseAt: number
  releaseReason: PTReleaseReason
  estimated: boolean
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

/** 采样窗口覆盖情况：解释 uploaded30d 的「≈」是怎么来的 */
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
  duplicateBytes: number
  sites: PTSiteStat[]
  upcoming: PTUpcomingDay[]
  generatedAt: number
  coverage: PTSampleCoverage
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
  zeroUpload: number
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

export interface PTContentGroup {
  key: string
  name: string
  size: number
  totalBytes: number
  duplicateBytes: number
  sameDownloader: boolean
  copies: PTContentCopy[]
}

export interface PTMatrix {
  groups: PTContentGroup[]
  summary: { groups: number; duplicateBytes: number }
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