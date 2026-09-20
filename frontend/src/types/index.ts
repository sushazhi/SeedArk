// 种子文件信息
export interface FileInfo {
  bytesCompleted: number
  length: number
  name: string
}

// 种子文件统计
export interface FileStat {
  bytesCompleted: number
  wanted: boolean
  priority: number
}

// Tracker 基础信息
export interface Tracker {
  announce: string
  id: number
  scrape: string
  sitename: string
  tier: number
}

// Tracker 状态
export interface TrackerStat {
  id: number
  host: string
  announce: string
  announceState: number
  tier: number
  isBackup: boolean
  lastAnnounceResult: string
  lastAnnounceSucceeded: boolean
  lastAnnounceTimedOut: boolean
  lastAnnounceTime: number
  lastAnnouncePeerCount: number
  nextAnnounceTime: number
  scrapeState: number
  lastScrapeResult: string
  lastScrapeSucceeded: boolean
  lastScrapeTime: number
  nextScrapeTime: number
  seederCount: number
  leecherCount: number
  downloadCount: number
}

// Peer 信息
export interface Peer {
  address: string
  clientName: string
  flagStr: string
  progress: number
  rateToClient: number
  rateToPeer: number
  isDownloadingFrom: boolean
  isUploadingTo: boolean
  isEncrypted: boolean
  isIncoming: boolean
  isUTP: boolean
  port: number
}

// 种子数据
export interface Torrent {
  id: number
  name: string
  hashString: string
  creator: string
  totalSize: number
  sizeWhenDone: number
  percentDone: number
  status: number
  rateDownload: number
  rateUpload: number
  eta: number
  uploadedEver: number
  downloadedEver: number
  uploadRatio: number
  secondsSeeding: number
  error: number
  errorString: string
  labels: string[]
  queuePosition: number
  peersConnected: number
  peersSendingToUs: number
  peersGettingFromUs: number
  downloadDir: string
  addedDate: number
  doneDate: number
  activityDate: number
  isFinished: boolean
  isStalled: boolean
  isPrivate: boolean
  magnetLink: string
  fileCount: number
  haveValid: number
  haveUnchecked: number
  leftUntilDone: number
  comment: string
  peerLimit: number
  seedIdleLimit: number
  seedIdleMode: number
  seedRatioLimit: number
  seedRatioMode: number
  bandwidthPriority: number
  sequentialDownload: boolean
  // 带宽组（Transmission 4.x）
  groups?: string[]
  honorsSessionLimits: boolean
  downloadLimited: boolean
  downloadLimit: number
  uploadLimited: boolean
  uploadLimit: number
  files?: FileInfo[]
  fileStats?: FileStat[]
  trackers?: Tracker[]
  trackerStats?: TrackerStat[]
  peers?: Peer[]
  // 块位图（详情接口返回，base64 编码）
  pieces?: string
  pieceCount?: number
  pieceSize?: number
}

// 下载器类型
export type DownloaderKind = 'transmission' | 'qbittorrent'

// 下载器能力自述：界面据此隐藏当前下载器不支持的入口
export interface DownloaderCaps {
  bandwidthGroups: boolean
  blocklist: boolean
  freeSpace: boolean
  portTest: boolean
  sequentialDownload: boolean
  queueMove: boolean
  renameFile: boolean
  systemCommand: boolean
  altSpeedSchedule: boolean
  trackerReplace: boolean
  pieceBitmap: boolean
  incompleteDir: boolean
  scriptHooks: boolean
  globalSeedRatio: boolean
}

// 会话信息
export interface Session {
  version: string
  type?: DownloaderKind
  caps?: DownloaderCaps
  rpcVersion: number
  downloadDir: string
  speedLimitDown: number
  speedLimitDownOn: boolean
  speedLimitUp: number
  speedLimitUpOn: boolean
  altSpeedDown: number
  altSpeedUp: number
  altSpeedEnabled: boolean
  peerLimitGlobal: number
  peerPort: number
  peerPortRandomOnStart: boolean
  pexEnabled: boolean
  dhtEnabled: boolean
  lpdEnabled: boolean
  utpEnabled: boolean
  encryption: string
  seedRatioLimit: number
  startAdded: boolean
  incompleteDir: string
  downloadQueueSize: number
  downloadQueueEnabled: boolean
  seedQueueSize: number
  seedQueueEnabled: boolean
  queueStalledEnabled: boolean
  queueStalledMinutes: number
  blocklistEnabled: boolean
  blocklistUrl: string
  blocklistSize: number
  portForwardingEnabled: boolean
  incompleteDirEnabled: boolean
  cacheSizeMB: number
  altSpeedTimeEnabled: boolean
  altSpeedTimeBegin: number
  altSpeedTimeEnd: number
  altSpeedTimeDay: number
  scriptTorrentAddedEnabled: boolean
  scriptTorrentAddedFilename: string
  scriptTorrentDoneEnabled: boolean
  scriptTorrentDoneFilename: string
  scriptTorrentDoneSeedingEnabled: boolean
  scriptTorrentDoneSeedingFilename: string
  defaultTrackers: string[]
  renamePartialFiles: boolean
  trashOriginalTorrentFiles: boolean
  idleSeedingLimitEnabled: boolean
  idleSeedingLimit: number
}

// 连接状态
export interface SessionStatus {
  connected: boolean
  version?: string
  type?: DownloaderKind
  caps?: DownloaderCaps
  error?: string
  // 聚合视图：同时管理多台下载器（tr + qb 混合）
  aggregate?: boolean
  aggregateErrors?: string[]
}

// 会话统计
export interface SessionStats {
  activeTorrentCount: number
  downloadSpeed: number
  pausedTorrentCount: number
  torrentCount: number
  uploadSpeed: number
  cumulative: SessionStatsDetails
  current: SessionStatsDetails
}

export interface SessionStatsDetails {
  downloadedBytes: number
  filesAdded: number
  secondsActive: number
  sessionCount: number
  uploadedBytes: number
}

// 统一 API 响应
export interface ApiResponse<T = unknown> {
  code: number
  message: string
  data: T
}

// WebSocket 消息：全量快照（full/update）与增量推送（diff）两种
export interface WsFullMessage {
  type: 'full' | 'update' | 'ping'
  data?: Torrent[]
  timestamp: number
}

// 增量推送：仅包含变化的种子（added/updated 为完整种子对象，removed 为 id 列表）
export interface WsDiffMessage {
  type: 'diff'
  added: Torrent[]
  updated: Torrent[]
  removed: number[]
  timestamp: number
}

export type WsMessage = WsFullMessage | WsDiffMessage

// 带宽组（Transmission 4.x）
export interface BandwidthGroup {
  name: string
  downKB: number
  upKB: number
  downEnabled: boolean
  upEnabled: boolean
  honorsSessionLimits: boolean
}

// 后端建种任务状态
export interface CreateTorrentJobStatus {
  status: 'running' | 'done' | 'error'
  processed: number
  total: number
  name: string
  error: string
  autoAdded: boolean
}

// 后端建种请求参数
export interface CreateTorrentServerOptions {
  path: string
  announce?: string
  announceList?: string[]
  comment?: string
  private?: boolean
  pieceLength?: number
  webSeeds?: string[]
  autoAdd?: boolean
  downloadDir?: string
  paused?: boolean
  labels?: string[]
}

// 路径映射（远端 Transmission 路径 → 宿主本地路径）
export interface PathMapping {
  from: string
  to: string
}

// 表格列配置
export interface ColumnConfig {
  key: string
  label: string
  visible: boolean
  width?: number
}

// 过滤选项
export interface FilterOptions {
  status: string[]
  labels: string[]
  sites: string[]
  downloadDirs: string[]
  error: string[]
  search: string
  sortBy: string
  sortOrder: 'asc' | 'desc'
}

// 多服务器
export interface ServerInfo {
  index?: number
  name: string
  // 下载器类型：transmission（默认）| qbittorrent
  type: DownloaderKind
  url: string
  user: string
  pass?: string
  hasPass?: boolean
  enabled: boolean
}

// 自动文件管理规则
export interface AutoMoveRule {
  id: string
  name: string
  enabled: boolean
  sites: string[]
  labels: string[]
  nameMatch: string
  targetDir: string
}

// 做种策略达标后的动作：暂停、删除（保留文件）、删除并连带删文件
export type SeedPolicyAction = 'pause' | 'delete' | 'deleteData'

// 做种策略规则：按站点 / 标签 / 名称圈定范围，达标条件全部满足后执行动作
export interface SeedPolicyRule {
  id: string
  name: string
  enabled: boolean
  sites: string[]
  labels: string[]
  nameMatch: string
  minRatio: number
  minSeedDays: number
  minUploadGB: number
  action: SeedPolicyAction
}

// 做种策略全局安全保护，对所有规则生效
export interface SeedPolicyGuard {
  // enforce 关闭时引擎只写预览记录，不会真的暂停 / 删除种子
  enforce: boolean
  minSeedHours: number
  excludeSites: string[]
  excludeLabels: string[]
}

// 达标依据的结构化片段，界面按语言渲染成文案
export interface SeedPolicyReasonPart {
  kind: 'ratio' | 'days' | 'upload'
  actual: number
  target: number
}

// 做种策略执行记录
export interface SeedPolicyLog {
  time: number
  rule: string
  torrent: string
  site: string
  action: string
  reason?: SeedPolicyReasonPart[]
  dryRun: boolean
}

// 做种策略单轮执行统计
export interface SeedPolicyResult {
  matched: number
  paused: number
  deleted: number
  previewed: number
  failed: number
}

// 分组限速规则：命中站点 / 标签 / 名称的一组种子共享总速度上限，
// 下载与上传可同时设置，0 = 该方向不限制
export interface SpeedPolicyRule {
  id: string
  name: string
  enabled: boolean
  downLimit: number // KB/s，组内下载总上限；0 = 不限
  upLimit: number // KB/s，组内上传总上限；0 = 不限
  sites: string[]
  labels: string[]
  nameMatch: string
}

// 组内总限速引擎开关
export interface SpeedPolicyGuard {
  enforce: boolean
}

// 组内总限速单轮执行统计
export interface SpeedPolicyResult {
  enabled: boolean
  matched: number
  applied: number
  released: number
  failed: number
}
