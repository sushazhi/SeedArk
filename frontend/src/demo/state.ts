import type {
  AutoMoveRule,
  FileInfo,
  FileStat,
  Peer,
  SeedPolicyGuard,
  SeedPolicyLog,
  SeedPolicyResult,
  SeedPolicyRule,
  Session,
  SessionStats,
  SessionStatsDetails,
  SettingsSection,
  SpeedPolicyGuard,
  SpeedPolicyResult,
  SpeedPolicyRule,
  Torrent,
  Tracker,
  TrackerStat,
} from '@/types'

// 演示模式内存后端：模拟一套 Transmission 数据（口径对齐后端 REST 响应），
// 数据模型与 backend/cmd/trmock 的种子生成保持一致的风格

const KB = 1024
const MB = 1024 * KB
const GB = 1024 * MB
const TB = 1024 * GB
const HOUR = 3600
const DAY = 86400

export const APP_VERSION = '1.0.0'

// 可变伪随机：每个种子由固定种子数生成，刷新后数据形态一致
function mulberry32(seed: number): () => number {
  let a = seed >>> 0
  return () => {
    a = (a + 0x6d2b79f5) | 0
    let t = Math.imul(a ^ (a >>> 15), 1 | a)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

function fakeHash(seed: number): string {
  const rnd = mulberry32(seed * 2654435761 + 7)
  let s = ''
  for (let i = 0; i < 40; i++) s += Math.floor(rnd() * 16).toString(16)
  return s
}

const SITES = [
  { name: 'example-tracker.org', announce: 'https://tracker.example-tracker.org/announce' },
  { name: 'open-mirror.net', announce: 'udp://open-mirror.net:6969/announce' },
  { name: 'private-hd.club', announce: 'https://private-hd.club/tracker.php' },
  { name: 'public.pushep.com', announce: 'udp://public.pushep.com:1337/announce' },
]

export interface MockTorrent extends Torrent {
  baseDown: number
  baseUp: number
  holdTicks: number
  prevStatus: number
}

interface Spec {
  name: string
  size: number // GB
  status: number
  progress: number
  labels?: string[]
  dir: string
  site: number // SITES 下标，-1 = 无 tracker（站点过滤归入「其他」）
  files: number
  addedDays: number
  ratio?: number
  priv?: boolean
  error?: string
  stalled?: boolean
  downLimitKB?: number
  baseDown?: number
  baseUp?: number
}

// 覆盖全部状态（0 暂停/1 校验/2 等待校验/3 等待下载/4 下载/5 等待做种/6 做种）、
// 含无标签、无站点、错误、限速与暂停样本
const SPECS: Spec[] = [
  { name: 'ubuntu-24.04-desktop-amd64.iso', size: 5.8, status: 4, progress: 0.62, labels: ['linux', 'iso'], dir: '/downloads', site: 0, files: 1, addedDays: 2, baseDown: 8.4 * MB, baseUp: 640 * KB },
  { name: 'Big.Buck.Bunny.2008.1080p.BluRay.x264', size: 8.4, status: 6, progress: 1, labels: ['video', 'hd'], dir: '/downloads/media', site: 2, files: 6, addedDays: 45, ratio: 2.31, priv: true, baseUp: 1.8 * MB },
  { name: 'The.Wire.S01E01.720p.WEB-DL', size: 2.1, status: 4, progress: 0.34, labels: ['video'], dir: '/volume1/video', site: 1, files: 1, addedDays: 1, baseDown: 3.2 * MB, baseUp: 256 * KB },
  { name: 'debian-12.5.0-amd64-DVD-1.iso', size: 4.7, status: 6, progress: 1, labels: ['iso'], dir: '/downloads', site: 0, files: 1, addedDays: 30, ratio: 3.05, baseUp: 900 * KB },
  { name: 'archlinux-2024.06.01-x86_64.iso', size: 1.2, status: 0, progress: 1, labels: ['linux'], dir: '/downloads', site: 1, files: 1, addedDays: 60, ratio: 4.7 },
  { name: 'Nature.Documentary.Oceans.2160p.HDR', size: 42, status: 4, progress: 0.12, labels: ['video', '4k'], dir: '/volume1/video', site: 2, files: 108, addedDays: 3, priv: true, stalled: true, downLimitKB: 2048, baseDown: 1.5 * MB, baseUp: 512 * KB },
  { name: 'fedora-workstation-live-40.iso', size: 2.3, status: 3, progress: 0, labels: ['linux', 'iso'], dir: '/downloads', site: 0, files: 1, addedDays: 0 },
  { name: 'Indie.Game.Soundtrack.FLAC', size: 0.64, status: 6, progress: 1, dir: '/downloads/media', site: 3, files: 24, addedDays: 90, ratio: 6.2, baseUp: 420 * KB },
  { name: 'LibreOffice.Help.Docs.All.Langs', size: 1.9, status: 2, progress: 0.4, labels: ['docs'], dir: '/downloads', site: 1, files: 12, addedDays: 5 },
  { name: 'Vintage.Film.Collection.Remastered', size: 18, status: 1, progress: 0.73, labels: ['video'], dir: '/volume1/video', site: 2, files: 14, addedDays: 12, priv: true },
  { name: 'Machine.Learning.Course.Notes.PDF', size: 0.32, status: 6, progress: 1, labels: ['docs'], dir: '/downloads', site: 3, files: 3, addedDays: 120, ratio: 1.8, baseUp: 128 * KB },
  { name: 'KDE.Plasma.Wallpapers.Pack', size: 0.96, status: 0, progress: 0.55, labels: ['wallpapers'], dir: '/downloads', site: 1, files: 42, addedDays: 8, error: 'could not connect to tracker' },
  { name: 'Blender.Foundation.Sintel.4K', size: 12.5, status: 5, progress: 1, labels: ['video', '4k'], dir: '/volume1/video', site: 2, files: 4, addedDays: 20, ratio: 1.02, priv: true, baseUp: 2.2 * MB },
  { name: 'Big.Buck.Bunny.Sample.Clip', size: 0.21, status: 0, progress: 0, dir: '/downloads', site: -1, files: 1, addedDays: 15 },
]

function trackerOf(siteIdx: number, error?: string): { trackers: Tracker[]; stats: TrackerStat[] } {
  if (siteIdx < 0) return { trackers: [], stats: [] }
  const s = SITES[siteIdx]
  const now = Math.floor(Date.now() / 1000)
  return {
    trackers: [{ id: 0, announce: s.announce, scrape: s.announce.replace('announce', 'scrape'), sitename: s.name, tier: 0 }],
    stats: [{
      id: 0, host: s.name, announce: s.announce, announceState: 1, tier: 0, isBackup: false,
      lastAnnounceResult: error ? 'Could not connect to tracker' : 'Success',
      lastAnnounceSucceeded: !error, lastAnnounceTimedOut: !!error,
      lastAnnounceTime: now - 300, lastAnnouncePeerCount: 8, nextAnnounceTime: now + 300,
      scrapeState: 1, lastScrapeResult: '', lastScrapeSucceeded: true,
      lastScrapeTime: now - 600, nextScrapeTime: now + 600,
      seederCount: 12, leecherCount: 5, downloadCount: 1200,
    }],
  }
}

function extFor(name: string): string {
  const dot = name.lastIndexOf('.')
  if (dot > 0) return name.slice(dot)
  if (/2160p|1080p|720p|web-?dl|film|movie|documentary|bunny|wire|sintel/i.test(name)) return '.mkv'
  if (/docs|help|notes|book/i.test(name)) return '.pdf'
  if (/wallpaper/i.test(name)) return '.jpg'
  if (/soundtrack|audio/i.test(name)) return '.flac'
  return '.dat'
}

function buildFiles(id: number, name: string, size: number, percentDone: number, count: number): { files: FileInfo[]; fileStats: FileStat[] } {
  const done = (len: number) => Math.round(len * percentDone)
  if (count <= 1) {
    return {
      files: [{ name, length: size, bytesCompleted: done(size) }],
      fileStats: [{ bytesCompleted: done(size), wanted: true, priority: 0 }],
    }
  }
  const rnd = mulberry32(id * 31 + 7)
  const ext = extFor(name)
  const files: FileInfo[] = []
  const fileStats: FileStat[] = []
  let remain = size
  for (let i = 0; i < count; i++) {
    const len = i === count - 1
      ? Math.max(MB, remain)
      : Math.max(MB, Math.round((size / count) * (0.5 + rnd())))
    remain -= len
    files.push({ name: `${name}/file-${String(i + 1).padStart(2, '0')}${ext}`, length: len, bytesCompleted: done(len) })
    fileStats.push({ bytesCompleted: done(len), wanted: true, priority: 0 })
  }
  return { files, fileStats }
}

function genPeers(seed: number, n: number): Peer[] {
  const rnd = mulberry32(seed * 17 + 3)
  const clients = ['qBittorrent 4.6.2', 'Transmission 4.0.6', 'Deluge 2.1.1', 'libtorrent 2.0.9', 'aria2 1.37.0']
  const peers: Peer[] = []
  for (let i = 0; i < n; i++) {
    peers.push({
      address: `${1 + Math.floor(rnd() * 223)}.${Math.floor(rnd() * 256)}.${Math.floor(rnd() * 256)}.${2 + Math.floor(rnd() * 250)}`,
      clientName: clients[Math.floor(rnd() * clients.length)],
      flagStr: 'DTU',
      progress: rnd(),
      rateToClient: Math.floor(rnd() * 2 * MB),
      rateToPeer: Math.floor(rnd() * 512 * KB),
      isDownloadingFrom: rnd() > 0.5,
      isUploadingTo: rnd() > 0.3,
      isEncrypted: true,
      isIncoming: rnd() > 0.5,
      isUTP: false,
      port: 1024 + Math.floor(rnd() * 60000),
    })
  }
  return peers
}

function buildTorrent(id: number, spec: Spec, now: number): MockTorrent {
  const rnd = mulberry32(id * 97 + 13)
  const size = Math.round(spec.size * GB)
  const progress = spec.progress
  const have = Math.round(size * progress)
  const ratio = spec.ratio ?? (progress >= 1 ? 1.2 + rnd() : progress * 0.3)
  const uploaded = Math.round(size * ratio)
  const { trackers, stats } = trackerOf(spec.site, spec.error)
  const baseDown = spec.baseDown ?? 4 * MB
  const baseUp = spec.baseUp ?? 512 * KB
  const rateDown = spec.status === 4
    ? Math.round(spec.stalled ? (rnd() < 0.5 ? 0 : baseDown * 0.3) : baseDown * (0.75 + rnd() * 0.5))
    : 0
  const rateUp = spec.status === 6
    ? Math.round(baseUp * (0.7 + rnd() * 0.6))
    : spec.status === 4 ? Math.round(baseUp * 0.4) : 0
  const sending = spec.status === 4 ? 3 + Math.floor(rnd() * 12) : 0
  const getting = spec.status === 6 ? 2 + Math.floor(rnd() * 12) : spec.status === 4 ? 1 + Math.floor(rnd() * 6) : 0
  return {
    id,
    name: spec.name,
    hashString: fakeHash(id),
    creator: 'Transmission 4.0.6',
    totalSize: size,
    sizeWhenDone: size,
    percentDone: progress,
    status: spec.status,
    rateDownload: rateDown,
    rateUpload: rateUp,
    eta: spec.status === 4 && rateDown > 0 ? Math.round((size - have) / rateDown) : -1,
    uploadedEver: uploaded,
    downloadedEver: have,
    uploadRatio: ratio,
    secondsSeeding: progress >= 1 ? Math.round(spec.addedDays * DAY * (0.4 + rnd() * 0.4)) : 0,
    error: spec.error ? 2 : 0,
    errorString: spec.error ?? '',
    labels: spec.labels ? [...spec.labels] : [],
    queuePosition: id - 1,
    peersConnected: sending + getting,
    peersSendingToUs: sending,
    peersGettingFromUs: getting,
    downloadDir: spec.dir,
    addedDate: now - spec.addedDays * DAY - Math.floor(rnd() * 6 * HOUR),
    doneDate: progress >= 1 ? now - Math.floor(spec.addedDays * DAY * 0.5) : 0,
    activityDate: rateDown + rateUp > 0 ? now - 3 : now - 2 * HOUR,
    isFinished: progress >= 1,
    isStalled: !!spec.stalled,
    isPrivate: !!spec.priv,
    magnetLink: `magnet:?xt=urn:btih:${fakeHash(id)}&dn=${encodeURIComponent(spec.name)}`,
    fileCount: spec.files,
    haveValid: have,
    haveUnchecked: 0,
    leftUntilDone: size - have,
    comment: '',
    peerLimit: 60,
    seedIdleLimit: 30,
    seedIdleMode: 0,
    seedRatioLimit: 2,
    seedRatioMode: 0,
    bandwidthPriority: 0,
    sequentialDownload: false,
    honorsSessionLimits: true,
    downloadLimited: spec.downLimitKB != null,
    downloadLimit: spec.downLimitKB ?? 0,
    uploadLimited: false,
    uploadLimit: 0,
    pieceSize: 4 * MB,
    pieceCount: Math.max(1, Math.ceil(size / (4 * MB))),
    trackers,
    trackerStats: stats,
    ...buildFiles(id, spec.name, size, progress, spec.files),
    peers: genPeers(id, sending + getting > 0 ? 3 + Math.floor(rnd() * 3) : 0),
    baseDown,
    baseUp,
    holdTicks: 0,
    prevStatus: spec.status,
  }
}

// —— 全局状态 ——

let list: MockTorrent[] = []
let nextId = 1
let ticker: number | null = null

const bootNow = Math.floor(Date.now() / 1000)

function seed(): void {
  list = SPECS.map((spec, i) => buildTorrent(i + 1, spec, bootNow))
  nextId = list.length + 1
}

export function ensureDemo(): void {
  if (list.length === 0) seed()
  if (ticker == null) ticker = window.setInterval(tick, 1000)
}

const session: Session = {
  version: '4.0.6',
  rpcVersion: 17,
  downloadDir: '/downloads',
  speedLimitDown: 0,
  speedLimitDownOn: false,
  speedLimitUp: 0,
  speedLimitUpOn: false,
  altSpeedDown: 512,
  altSpeedUp: 256,
  altSpeedEnabled: false,
  peerLimitGlobal: 240,
  peerPort: 51413,
  peerPortRandomOnStart: false,
  pexEnabled: true,
  dhtEnabled: true,
  lpdEnabled: false,
  utpEnabled: true,
  encryption: 'preferred',
  seedRatioLimit: 2,
  startAdded: true,
  incompleteDir: '/downloads/incomplete',
  downloadQueueSize: 5,
  downloadQueueEnabled: true,
  seedQueueSize: 0,
  seedQueueEnabled: false,
  queueStalledEnabled: true,
  queueStalledMinutes: 30,
  blocklistEnabled: false,
  blocklistUrl: '',
  blocklistSize: 0,
  portForwardingEnabled: false,
  incompleteDirEnabled: false,
  cacheSizeMB: 16,
  altSpeedTimeEnabled: false,
  altSpeedTimeBegin: 480,
  altSpeedTimeEnd: 1020,
  altSpeedTimeDay: 127,
  scriptTorrentAddedEnabled: false,
  scriptTorrentAddedFilename: '',
  scriptTorrentDoneEnabled: false,
  scriptTorrentDoneFilename: '',
  scriptTorrentDoneSeedingEnabled: false,
  scriptTorrentDoneSeedingFilename: '',
  defaultTrackers: [],
  renamePartialFiles: false,
  trashOriginalTorrentFiles: false,
  idleSeedingLimitEnabled: false,
  idleSeedingLimit: 30,
}

let cumDownloaded = 8.6 * TB
let cumUploaded = 19.2 * TB
let curDownloaded = 3.1 * GB
let curUploaded = 5.4 * GB
let curSeconds = 36 * HOUR
let cumSeconds = 260 * DAY

function statsDetails(down: number, up: number, files: number, seconds: number): SessionStatsDetails {
  return { downloadedBytes: down, filesAdded: files, secondsActive: seconds, sessionCount: 1, uploadedBytes: up }
}

export function sessionStats(): SessionStats {
  ensureDemo()
  const down = list.reduce((s, t) => s + t.rateDownload, 0)
  const up = list.reduce((s, t) => s + t.rateUpload, 0)
  return {
    activeTorrentCount: list.filter((t) => t.status >= 1 && t.status <= 5).length,
    downloadSpeed: down,
    pausedTorrentCount: list.filter((t) => t.status === 0).length,
    torrentCount: list.length,
    uploadSpeed: up,
    cumulative: statsDetails(cumDownloaded, cumUploaded, list.length, cumSeconds),
    current: statsDetails(curDownloaded, curUploaded, list.length, curSeconds),
  }
}

function setStatus(t: MockTorrent, s: number): void {
  t.status = s
  t.rateDownload = 0
  t.rateUpload = 0
  t.eta = -1
  if (s === 6) {
    t.percentDone = 1
    t.haveValid = t.totalSize
    t.leftUntilDone = 0
    t.doneDate = Math.floor(Date.now() / 1000)
    t.isFinished = true
  }
  if (s === 4) {
    t.rateDownload = Math.round(t.baseDown * (0.8 + Math.random() * 0.4))
    t.rateUpload = Math.round(t.baseUp * 0.3)
    t.eta = Math.round(t.leftUntilDone / Math.max(1, t.rateDownload))
  }
}

function tick(): void {
  const now = Math.floor(Date.now() / 1000)
  for (const t of list) {
    switch (t.status) {
      case 1: // 校验完成回到校验前状态
        if (--t.holdTicks <= 0) setStatus(t, t.prevStatus === 1 || t.prevStatus === 2 ? 0 : t.prevStatus)
        break
      case 2:
        if (--t.holdTicks <= 0) setStatus(t, 1)
        break
      case 3:
        if (--t.holdTicks <= 0) setStatus(t, 4)
        break
      case 4: {
        const r = t.isStalled
          ? (Math.random() < 0.6 ? 0 : Math.round(t.baseDown * 0.25))
          : Math.round(t.baseDown * (0.75 + Math.random() * 0.5))
        t.rateDownload = r
        t.rateUpload = Math.round(t.baseUp * (0.2 + Math.random() * 0.5))
        if (r > 0) {
          t.percentDone = Math.min(1, t.percentDone + r / t.totalSize)
          const have = Math.round(t.totalSize * t.percentDone)
          t.downloadedEver = Math.max(t.downloadedEver, have)
          t.haveValid = have
          t.leftUntilDone = t.totalSize - have
          t.eta = Math.round(t.leftUntilDone / r)
        }
        t.activityDate = now
        break
      }
      case 5:
        if (--t.holdTicks <= 0) setStatus(t, 6)
        break
      case 6:
        t.rateDownload = 0
        t.rateUpload = Math.round(t.baseUp * (0.7 + Math.random() * 0.6))
        t.uploadedEver += t.rateUpload
        t.uploadRatio = t.uploadedEver / t.totalSize
        t.secondsSeeding += 1
        t.peersSendingToUs = 0
        t.peersGettingFromUs = 1 + Math.floor(Math.random() * 12)
        t.peersConnected = t.peersGettingFromUs
        t.activityDate = now
        break
      default:
        break
    }
  }
  const down = list.reduce((s, t) => s + t.rateDownload, 0)
  const up = list.reduce((s, t) => s + t.rateUpload, 0)
  cumDownloaded += down
  cumUploaded += up
  curDownloaded += down
  curUploaded += up
  curSeconds += 1
  cumSeconds += 1
}

// —— 读取 ——

const clone = <T,>(v: T): T => JSON.parse(JSON.stringify(v)) as T

// 列表形态：不含文件/Peer/块位图（详情接口才有），保留 tracker 供列排序
export function listTorrents(): Torrent[] {
  ensureDemo()
  return list.map(({ files: _f, peers: _p, ...rest }) => clone(rest))
}

function piecesBitmap(t: MockTorrent): string {
  const count = t.pieceCount ?? 1
  const done = Math.floor(count * t.percentDone)
  const bytes = new Uint8Array(Math.ceil(count / 8))
  for (let i = 0; i < done; i++) bytes[i >> 3] |= 1 << (i & 7)
  let s = ''
  for (let i = 0; i < bytes.length; i += 0x8000) {
    s += String.fromCharCode(...bytes.subarray(i, i + 0x8000))
  }
  return btoa(s)
}

export function torrentDetail(id: number): Torrent | null {
  ensureDemo()
  const t = list.find((x) => x.id === id)
  if (!t) return null
  return clone({ ...t, pieces: piecesBitmap(t) })
}

export function torrentSites(): Record<number, string[]> {
  ensureDemo()
  const out: Record<number, string[]> = {}
  for (const t of list) {
    const seen = new Set<string>()
    const arr: string[] = []
    for (const tr of t.trackers ?? []) {
      const name = tr.sitename || tr.announce
      if (!name || seen.has(name)) continue
      seen.add(name)
      arr.push(name)
    }
    if (arr.length > 0) out[t.id] = arr
  }
  return out
}

export function getSession(): Session {
  ensureDemo()
  return clone(session)
}

export function updateSession(patch: Record<string, unknown>): void {
  for (const k of Object.keys(patch)) {
    if (k in session) (session as unknown as Record<string, unknown>)[k] = patch[k]
  }
}

export function freeSpace(path: string): { path: string; freeSpace: number; totalSize: number } {
  return { path, freeSpace: 512 * GB, totalSize: 2 * TB }
}

// 指定服务器的磁盘余量（GET /servers/:index/free-space）。聚合视图下侧栏逐台显示：
// TR 有总量（RPC v17 起可用），qB 的总量按 Web API 的真实能力回 0（未知，不显示 / 总量）
export function serverFreeSpace(index: number): {
  index: number
  name: string
  path: string
  freeSpace: number
  totalSize: number
} | null {
  ensureDemo()
  const srv = servers[index]
  if (!srv) return null
  const path = session.downloadDir
  if ((srv.type ?? 'transmission') === 'qbittorrent') {
    return { index, name: srv.name, path, freeSpace: 320 * GB, totalSize: 0 }
  }
  return { index, name: srv.name, path, freeSpace: 512 * GB, totalSize: 2 * TB }
}

// —— 种子操作 ——

export function findTorrent(id: number): MockTorrent | undefined {
  ensureDemo()
  return list.find((t) => t.id === id)
}

export function startTorrent(t: MockTorrent, immediately: boolean): void {
  if (t.status === 4 || t.status === 6) return
  t.prevStatus = t.status
  const target = t.percentDone >= 1 ? 6 : 4
  if (immediately) {
    setStatus(t, target)
  } else {
    setStatus(t, target === 6 ? 5 : 3)
    t.holdTicks = 1
  }
}

export function stopTorrent(t: MockTorrent): void {
  t.prevStatus = t.status
  setStatus(t, 0)
}

export function verifyTorrent(t: MockTorrent): void {
  if (t.status === 1 || t.status === 2) return
  t.prevStatus = t.status
  t.status = 1
  t.holdTicks = 2 + Math.floor(Math.random() * 3)
  t.rateDownload = 0
  t.rateUpload = 0
  t.eta = -1
}

const MUTABLE_KEYS = [
  'labels', 'downloadLimited', 'downloadLimit', 'uploadLimited', 'uploadLimit',
  'seedRatioLimit', 'seedRatioMode', 'seedIdleLimit', 'seedIdleMode', 'peerLimit',
  'bandwidthPriority', 'sequentialDownload', 'honorsSessionLimits', 'queuePosition',
]

export function updateTorrent(t: MockTorrent, body: Record<string, unknown>): void {
  for (const k of MUTABLE_KEYS) {
    if (k in body) (t as unknown as Record<string, unknown>)[k] = body[k]
  }
}

export function moveTorrent(t: MockTorrent, location: string): void {
  if (location) t.downloadDir = location
}

export function renameTorrent(t: MockTorrent, path: string, name: string): void {
  if (!path && name) t.name = name
}

export function queueMove(t: MockTorrent, direction: string): void {
  const sorted = [...list].sort((a, b) => a.queuePosition - b.queuePosition)
  const at = sorted.indexOf(t)
  if (at < 0) return
  const to = direction === 'top' ? 0 : direction === 'up' ? Math.max(0, at - 1) : direction === 'down' ? Math.min(sorted.length - 1, at + 1) : sorted.length - 1
  sorted.splice(at, 1)
  sorted.splice(to, 0, t)
  sorted.forEach((x, i) => { x.queuePosition = i })
}

export function removeTorrents(ids: number[]): void {
  const set = new Set(ids)
  list = list.filter((t) => !set.has(t.id))
}

export function startAll(all: boolean): void {
  for (const t of list) {
    if (all || t.status === 0 || t.status === 3 || t.status === 5) startTorrent(t, false)
  }
}

export function pauseAll(): void {
  for (const t of list) if (t.status !== 0) stopTorrent(t)
}

export interface AddInput {
  url?: string
  path?: string
  name?: string
  downloadDir?: string
  paused?: boolean
  verify?: boolean
  labels?: string[]
  bandwidthPriority?: number
}

function nameFromInput(input: AddInput): string {
  if (input.name) return input.name
  if (input.url) {
    if (input.url.startsWith('magnet:')) {
      const dn = /[?&]dn=([^&]*)/.exec(input.url)
      if (dn) return decodeURIComponent(dn[1]) || 'magnet-download'
      return 'magnet-download'
    }
    const last = input.url.split('/').filter(Boolean).pop() ?? 'download'
    return decodeURIComponent(last).replace(/\.torrent$/i, '') || 'download'
  }
  if (input.path) {
    const segs = input.path.split(/[\\/]/).filter(Boolean)
    return (segs[segs.length - 1] ?? 'download').replace(/\.torrent$/i, '')
  }
  return 'download'
}

export function addTorrent(input: AddInput): number {
  ensureDemo()
  const rnd = mulberry32(nextId * 7 + Date.now() % 100000)
  const id = nextId++
  const now = Math.floor(Date.now() / 1000)
  const size = Math.round((0.5 + rnd() * 7.5) * GB)
  const spec: Spec = {
    name: nameFromInput(input),
    size: size / GB,
    status: 0,
    progress: 0,
    dir: input.downloadDir || session.downloadDir,
    site: -1,
    files: 1 + Math.floor(rnd() * 4),
    addedDays: 0,
  }
  const t = buildTorrent(id, spec, now)
  t.labels = input.labels ? [...input.labels] : []
  t.bandwidthPriority = input.bandwidthPriority ?? 0
  t.baseDown = 2 * MB + rnd() * 4 * MB
  t.baseUp = 256 * KB + rnd() * 768 * KB
  t.queuePosition = list.length
  list.push(t)
  if (!input.paused) startTorrent(t, false)
  if (input.verify) verifyTorrent(t)
  return id
}

// —— 应用设置（/settings） ——

export interface DemoSettings {
  url: string
  user: string
  pollInterval: string
  mcpEnabled: boolean
  mcpAllowDelete: boolean
  mcpToken: string
  mcpPort: string
}

export const demoSettings: DemoSettings = {
  url: 'http://192.168.1.10:9091/transmission/rpc',
  user: 'demo',
  pollInterval: '2s',
  mcpEnabled: true,
  mcpAllowDelete: false,
  mcpToken: 'seedark-demo-token',
  mcpPort: '',
}

export function updateDemoSettings(patch: Record<string, unknown>): void {
  for (const k of ['url', 'user', 'pollInterval', 'mcpEnabled', 'mcpAllowDelete', 'mcpToken'] as const) {
    if (patch[k] !== undefined) (demoSettings as unknown as Record<string, unknown>)[k] = patch[k]
  }
}

// —— 多服务器 ——

// qBittorrent 偏好与自述分节：演示模式下的精简版。
// 键名与结构刻意与真实驱动（backend/internal/qbittorrent/qbprefs.go）保持一致，
// 这样界面那条「按 schema 通用渲染」的代码路径在演示里也是真的被走到的；
// 只取每节有代表性的几项，不必把上百个偏好全搬过来。
// 收录进来的这几项，label / labelEn / unit / 枚举选项也必须照抄驱动自述：
// 在线演示是公开预览页，同一项文案不能和真实面板各说一套。
const qbPreferences: Record<string, unknown> = {
  confirm_torrent_deletion: false,
  confirm_torrent_recheck: false,
  recheck_completed_torrents: true,
  performance_warning: true,
  file_log_enabled: false,
  save_path: '/downloads',
  temp_path_enabled: true,
  temp_path: '/downloads/incomplete',
  preallocate_all: false,
  queueing_enabled: true,
  max_active_downloads: 5,
  max_active_uploads: 3,
  max_active_torrents: 8,
  dont_count_slow_torrents: true,
  listen_port: 6881,
  upnp: true,
  dl_limit: 0,
  up_limit: 0,
  alt_dl_limit: 1024,
  alt_up_limit: 512,
  scheduler_enabled: false,
  schedule_from: '08:00',
  schedule_to: '23:00',
  dht: true,
  pex: true,
  lsd: true,
  encryption: 0,
  max_connec: 500,
  max_connec_per_torrent: 100,
  max_uploads: 4,
  max_uploads_per_torrent: 4,
  web_ui_port: 8080,
  web_ui_username: 'admin',
  web_ui_password: '',
  web_ui_csrf_protection_enabled: true,
  web_ui_clickjacking_protection_enabled: true,
  web_ui_secure_cookie_enabled: false,
  web_ui_max_auth_fail_count: 5,
  web_ui_ban_duration: 3600,
  web_ui_session_timeout: 3600,
  web_ui_host_header_validation_enabled: true,
  web_ui_domain_list: '*',
  web_ui_address: '*',
  use_https: false,
  web_ui_https_cert_path: '',
  web_ui_https_key_path: '',
  announce_to_all_trackers: false,
  announce_to_all_tiers: false,
  add_trackers_enabled: false,
  add_trackers: '',
  async_io_threads: 4,
  checking_memory_use: 32,
  current_interface_address: '',
  current_network_interface: '',
  disk_cache: -1,
  disk_cache_ttl: 60,
  disk_io_read_mode: 0,
  disk_io_type: 0,
  disk_queue_size: 1024,
  embedded_tracker_port: 9000,
  enable_embedded_tracker: false,
  enable_multi_connections_from_same_ip: false,
  send_buffer_watermark: 512,
  send_buffer_low_watermark: 128,
  send_buffer_watermark_factor: 150,
  socket_backlog_size: 511,
  torrent_stop_condition: 0,
  torrent_content_layout: 'Original',
  rss_processing_enabled: true,
  rss_auto_downloading_enabled: false,
  rss_max_articles_per_feed: 50,
  rss_refresh_interval: 30,
}

const qbSchema: SettingsSection[] = [
  {
    key: 'behavior', label: '行为', labelEn: 'Behavior', icon: 'behavior',
    fields: [
      { key: 'confirm_torrent_deletion', type: 'bool', label: '删除种子时提示确认', labelEn: 'Confirm when deleting torrents' },
      { key: 'confirm_torrent_recheck', type: 'bool', label: '重新校验种子时提示确认', labelEn: 'Confirm torrent recheck' },
      { key: 'recheck_completed_torrents', type: 'bool', label: '下载完成后自动重新校验', labelEn: 'Recheck torrents on completion' },
      { key: 'performance_warning', type: 'bool', label: '记录性能告警', labelEn: 'Log performance warnings' },
      { key: 'file_log_enabled', type: 'bool', label: '启用日志文件', labelEn: 'Enable log file' },
    ],
  },
  {
    key: 'downloads', label: '下载', labelEn: 'Downloads', icon: 'download',
    fields: [
      { key: 'save_path', type: 'path', label: '默认保存路径', labelEn: 'Default Save Path' },
      { key: 'temp_path_enabled', type: 'bool', label: '保存未完成的种子到', labelEn: 'Keep incomplete torrents in' },
      { key: 'temp_path', type: 'path', label: '未完成文件目录', labelEn: 'Incomplete files path' },
      { key: 'preallocate_all', type: 'bool', label: '为所有文件预分配磁盘空间', labelEn: 'Pre-allocate disk space for all files' },
      { key: 'queueing_enabled', type: 'bool', label: '种子排队', labelEn: 'Torrent Queueing' },
      { key: 'max_active_downloads', type: 'int', label: '最大同时下载数', labelEn: 'Maximum active downloads', min: 1 },
      { key: 'max_active_uploads', type: 'int', label: '最大同时做种数', labelEn: 'Maximum active uploads', min: 1 },
      { key: 'max_active_torrents', type: 'int', label: '最大同时活动种子数', labelEn: 'Maximum active torrents', min: 1 },
      { key: 'dont_count_slow_torrents', type: 'bool', label: '慢速种子不计入限制内', labelEn: 'Do not count slow torrents in these limits' },
    ],
  },
  {
    key: 'connection', label: '连接', labelEn: 'Connection', icon: 'connect',
    fields: [
      { key: 'listen_port', type: 'int', label: '传入连接使用的端口', labelEn: 'Port used for incoming connections', min: 1, max: 65535 },
      { key: 'upnp', type: 'bool', label: '使用路由器的 UPnP / NAT-PMP 端口转发', labelEn: 'Use UPnP / NAT-PMP port forwarding from my router' },
      { key: 'max_connec', type: 'int', label: '全局最大连接数', labelEn: 'Global maximum number of connections', min: -1 },
      { key: 'max_connec_per_torrent', type: 'int', label: '单种最大连接数', labelEn: 'Maximum number of connections per torrent', min: -1 },
      { key: 'max_uploads', type: 'int', label: '全局最大上传窗口数', labelEn: 'Global maximum number of upload slots', min: -1 },
      { key: 'max_uploads_per_torrent', type: 'int', label: '单种最大上传窗口数', labelEn: 'Maximum number of upload slots per torrent', min: -1 },
    ],
  },
  {
    key: 'speed', label: '速度', labelEn: 'Speed', icon: 'speed',
    fields: [
      { key: 'dl_limit', type: 'int', label: '全局下载限速', labelEn: 'Global download rate limit', unit: 'KiB/s', min: 0 },
      { key: 'up_limit', type: 'int', label: '全局上传限速', labelEn: 'Global upload rate limit', unit: 'KiB/s', min: 0 },
      { key: 'alt_dl_limit', type: 'int', label: '备用下载限速', labelEn: 'Alternative download rate limit', unit: 'KiB/s', min: 0 },
      { key: 'alt_up_limit', type: 'int', label: '备用上传限速', labelEn: 'Alternative upload rate limit', unit: 'KiB/s', min: 0 },
      { key: 'scheduler_enabled', type: 'bool', label: '按计划启用备用限速', labelEn: 'Schedule the use of alternative rate limits' },
      { key: 'schedule_from', type: 'time', label: '计划开始时刻', labelEn: 'Schedule from' },
      { key: 'schedule_to', type: 'time', label: '计划结束时刻', labelEn: 'Schedule to' },
    ],
  },
  {
    key: 'bittorrent', label: 'BitTorrent', labelEn: 'BitTorrent', icon: 'bittorrent',
    fields: [
      { key: 'dht', type: 'bool', label: '启用 DHT (去中心化网络) 以找到更多用户', labelEn: 'Enable DHT (decentralized network) to find more peers' },
      { key: 'pex', type: 'bool', label: '启用用户交换 (PeX) 以找到更多用户', labelEn: 'Enable Peer Exchange (PeX) to find more peers' },
      { key: 'lsd', type: 'bool', label: '启用本地用户发现以找到更多用户', labelEn: 'Enable Local Peer Discovery to find more peers' },
      {
        key: 'encryption', type: 'select', label: '加密模式', labelEn: 'Encryption mode', valueType: 'int',
        options: [
          { value: '0', label: '允许加密', labelEn: 'Allow encryption' },
          { value: '1', label: '强制加密', labelEn: 'Require encryption' },
          { value: '2', label: '禁用加密', labelEn: 'Disable encryption' },
        ],
      },
      { key: 'announce_to_all_trackers', type: 'bool', label: '始终向同级的所有 Tracker 汇报', labelEn: 'Always announce to all trackers in a tier' },
      { key: 'announce_to_all_tiers', type: 'bool', label: '始终向所有层级汇报', labelEn: 'Always announce to all tiers' },
      { key: 'add_trackers_enabled', type: 'bool', label: '自动为下载添加以下 Tracker', labelEn: 'Automatically append these trackers to new downloads' },
      { key: 'add_trackers', type: 'text', label: 'Tracker 列表（每行一个）', labelEn: 'Tracker list (one per line)' },
      { key: 'torrent_content_layout', type: 'select', label: '种子内容布局', labelEn: 'Torrent content layout',
        options: [
          { value: 'Original', label: '原始（种子内结构）', labelEn: 'Original' },
          { value: 'Subfolder', label: '创建子文件夹', labelEn: 'Create subfolder' },
          { value: 'NoSubfolder', label: '不创建子文件夹', labelEn: 'Don\'t create subfolder' },
        ] },
    ],
  },
  {
    key: 'rss', label: 'RSS', labelEn: 'RSS', icon: 'rss',
    fields: [
      { key: 'rss_processing_enabled', type: 'bool', label: '启用 RSS 抓取', labelEn: 'Enable fetching RSS feeds' },
      { key: 'rss_auto_downloading_enabled', type: 'bool', label: '启用 RSS 种子自动下载', labelEn: 'Enable auto downloading of RSS torrents' },
      { key: 'rss_max_articles_per_feed', type: 'int', label: '每个订阅最多保留文章数', labelEn: 'Maximum number of articles per feed', min: 0 },
      { key: 'rss_refresh_interval', type: 'int', label: '订阅刷新间隔', labelEn: 'Feeds refresh interval', unit: 'min', min: 1 },
    ],
  },
  {
    key: 'webui', label: 'WebUI', labelEn: 'WebUI', icon: 'webui',
    fields: [
      { key: 'web_ui_port', type: 'int', label: '监听端口', labelEn: 'Port', min: 1, max: 65535, danger: true },
      { key: 'web_ui_username', type: 'string', label: '用户名', labelEn: 'Username', danger: true },
      { key: 'web_ui_password', type: 'string', label: '密码', labelEn: 'Password', secret: true, writeOnly: true, danger: true },
      { key: 'web_ui_csrf_protection_enabled', type: 'bool', label: '启用跨站请求伪造（CSRF）保护', labelEn: 'Enable Cross-Site Request Forgery (CSRF) protection' },
      { key: 'web_ui_clickjacking_protection_enabled', type: 'bool', label: '启用点击劫持保护', labelEn: 'Enable clickjacking protection' },
      { key: 'web_ui_secure_cookie_enabled', type: 'bool', label: '启用 cookie 安全标志（需要 HTTPS 或本地连接）', labelEn: 'Enable cookie Secure flag (requires HTTPS or localhost connection)' },
      { key: 'web_ui_max_auth_fail_count', type: 'int', label: '连续失败多少次后封禁客户端', labelEn: 'Ban client after consecutive failures', min: 0 },
      { key: 'web_ui_ban_duration', type: 'int', label: '封禁时长', labelEn: 'Ban for', unit: 'sec', min: 0 },
      { key: 'web_ui_session_timeout', type: 'int', label: '会话超时', labelEn: 'Session timeout', unit: 'sec', min: 0 },
      { key: 'use_https', type: 'bool', label: '启用 HTTPS', labelEn: 'Use HTTPS instead of HTTP', danger: true },
      { key: 'web_ui_https_cert_path', type: 'string', label: '证书', labelEn: 'Certificate', danger: true },
      { key: 'web_ui_https_key_path', type: 'string', label: '私钥', labelEn: 'Key', danger: true },
    ],
  },
  {
    key: 'advanced', label: '高级', labelEn: 'Advanced', icon: 'advanced',
    fields: [
      { key: 'async_io_threads', type: 'int', label: '异步 IO 线程数', labelEn: 'Asynchronous I/O threads', min: 1, max: 1024 },
      { key: 'checking_memory_use', type: 'int', label: '校验时内存占用上限', labelEn: 'Outstanding memory when checking torrents', unit: 'MiB', min: 1 },
      { key: 'disk_cache', type: 'int', label: '磁盘缓存', labelEn: 'Disk cache', unit: 'MiB', min: -1 },
      { key: 'disk_cache_ttl', type: 'int', label: '磁盘缓存过期间隔', labelEn: 'Disk cache expiry interval', unit: 'sec', min: 1 },
      { key: 'disk_queue_size', type: 'int', label: '磁盘队列大小', labelEn: 'Disk queue size', unit: 'KiB', min: 1 },
      { key: 'enable_embedded_tracker', type: 'bool', label: '启用内置 Tracker', labelEn: 'Enable embedded tracker' },
      { key: 'embedded_tracker_port', type: 'int', label: '内置 Tracker 端口', labelEn: 'Embedded tracker port', min: 1, max: 65535 },
      { key: 'enable_multi_connections_from_same_ip', type: 'bool', label: '允许来自同一 IP 地址的多条连接', labelEn: 'Allow multiple connections from the same IP address' },
      { key: 'send_buffer_watermark', type: 'int', label: '发送缓冲区上限', labelEn: 'Send buffer watermark', unit: 'KiB', min: 1 },
      { key: 'send_buffer_low_watermark', type: 'int', label: '发送缓冲区下限', labelEn: 'Send buffer low watermark', unit: 'KiB', min: 1 },
      { key: 'socket_backlog_size', type: 'int', label: 'Socket 积压队列大小', labelEn: 'Socket backlog size', min: 1 },
      { key: 'current_network_interface', type: 'string', label: '网络接口', labelEn: 'Network interface', readOnly: true },
      { key: 'current_interface_address', type: 'string', label: '可选：绑定的 IP 地址', labelEn: 'Optional IP address to bind to', readOnly: true },
    ],
  },
]

interface DemoServer {
  index?: number
  name: string
  type?: 'transmission' | 'qbittorrent'
  url: string
  user: string
  pass?: string
  hasPass?: boolean
  enabled: boolean
}

// 两台：TR 与 qB 各一。设置面板的「下载器设置」按服务器类型渲染不同面板
// （TR 内置表单 / qB 驱动自述），只有一类就演示不出这个分流，因此默认带上 qB。
let servers: DemoServer[] = [
  { name: '演示 Transmission', type: 'transmission', url: 'http://192.168.1.10:9091/transmission/rpc', user: 'demo', hasPass: true, enabled: true },
  { name: '演示 qBittorrent', type: 'qbittorrent', url: 'http://192.168.1.11:8080', user: 'admin', hasPass: true, enabled: true },
]
let activeServer = 0

export function serverList(): { servers: DemoServer[]; activeServer: number } {
  return { servers: clone(servers), activeServer }
}

// 指定服务器的会话（GET /servers/:index/session）。
// 桌面端设置面板靠它按服务器标签各读各的；演示模式下按服务器类型给一份对应的会话，
// qB 的还带上驱动自述分节，否则「下载器设置」进去只有一片空白。
export function serverSession(index: number): {
  index: number
  name: string
  active: boolean
  session: Session
} | null {
  ensureDemo()
  const srv = servers[index]
  if (!srv) return null
  const base = clone(session)
  const kind = srv.type ?? 'transmission'
  if (kind === 'qbittorrent') {
    base.type = 'qbittorrent'
    base.version = '5.2.3'
    base.prefs = clone(qbPreferences)
    base.schema = clone(qbSchema)
  }
  return { index, name: srv.name, active: index === activeServer, session: base }
}

export function serverActiveIndex(): number {
  return activeServer
}

export function serverSave(arr: DemoServer[]): void {
  servers = clone(arr)
  if (activeServer >= servers.length) activeServer = 0
}

export function serverRemove(index: number): void {
  servers.splice(index, 1)
  if (activeServer >= servers.length) activeServer = Math.max(0, servers.length - 1)
}

export function serverSwitch(index: number): { index: number; version: string } {
  activeServer = index
  return { index, version: session.version }
}

// —— 做种策略 ——

let seedRules: SeedPolicyRule[] = [
  { id: 'sp-1', name: 'HD 保种达标暂停', enabled: true, sites: ['private-hd.club'], labels: [], nameMatch: '', minRatio: 2, minSeedDays: 5, minUploadGB: 0, action: 'pause' },
  { id: 'sp-2', name: '公开源达标清理', enabled: false, sites: ['example-tracker.org', 'open-mirror.net'], labels: [], nameMatch: '', minRatio: 1, minSeedDays: 3, minUploadGB: 0, action: 'delete' },
]
const seedGuard: SeedPolicyGuard = { enforce: false, minSeedHours: 48, excludeSites: [], excludeLabels: [] }
let seedLogs: SeedPolicyLog[] = [
  {
    time: Date.now() - 2 * HOUR * 1000,
    rule: 'HD 保种达标暂停',
    torrent: 'Big.Buck.Bunny.2008.1080p.BluRay.x264',
    site: 'private-hd.club',
    action: 'pause',
    reason: [{ kind: 'ratio', actual: 2.31, target: 2 }],
    dryRun: true,
  },
]

export function seedPolicyAll(): { rules: SeedPolicyRule[]; guard: SeedPolicyGuard; logs: SeedPolicyLog[] } {
  return { rules: clone(seedRules), guard: clone(seedGuard), logs: clone(seedLogs) }
}

export function seedPolicySave(rule: SeedPolicyRule): { id: string } {
  const r = clone(rule)
  if (!r.id) r.id = 'sp-' + Date.now()
  const at = seedRules.findIndex((x) => x.id === r.id)
  if (at >= 0) seedRules[at] = r
  else seedRules.push(r)
  return { id: r.id }
}

export function seedPolicyRemove(id: string): void {
  seedRules = seedRules.filter((r) => r.id !== id)
}

export function seedPolicySaveGuard(guard: SeedPolicyGuard): { saved: boolean } {
  Object.assign(seedGuard, guard)
  return { saved: true }
}

function ruleMatches(r: SeedPolicyRule, t: MockTorrent, sites: Record<number, string[]>): boolean {
  const ts = sites[t.id] ?? []
  if (r.sites.length > 0 && !r.sites.some((s) => ts.includes(s))) return false
  if (r.labels.length > 0 && !r.labels.some((l) => t.labels.includes(l))) return false
  if (r.nameMatch && !t.name.toLowerCase().includes(r.nameMatch.toLowerCase())) return false
  if (r.minRatio > 0 && t.uploadRatio < r.minRatio) return false
  if (r.minSeedDays > 0 && t.secondsSeeding < r.minSeedDays * DAY) return false
  if (r.minUploadGB > 0 && t.uploadedEver < r.minUploadGB * GB) return false
  return true
}

export function seedPolicyRun(): { result: SeedPolicyResult; at: number } {
  ensureDemo()
  const sites = torrentSites()
  const active = seedRules.filter((r) => r.enabled)
  const matched: MockTorrent[] = []
  for (const t of list) {
    if (active.some((r) => ruleMatches(r, t, sites))) matched.push(t)
  }
  const enforced = seedGuard.enforce && matched.length > 0
  const logs: SeedPolicyLog[] = []
  for (const m of matched.slice(0, 5)) {
    const rule = active.find((r) => ruleMatches(r, m, sites))!
    const reason = []
    if (rule.minRatio > 0) reason.push({ kind: 'ratio' as const, actual: Math.round(m.uploadRatio * 100) / 100, target: rule.minRatio })
    if (rule.minSeedDays > 0) reason.push({ kind: 'days' as const, actual: Math.round(m.secondsSeeding / DAY), target: rule.minSeedDays })
    if (rule.minUploadGB > 0) reason.push({ kind: 'upload' as const, actual: Math.round(m.uploadedEver / GB), target: rule.minUploadGB })
    logs.push({ time: Date.now(), rule: rule.name, torrent: m.name, site: (sites[m.id] ?? [])[0] ?? '', action: rule.action, reason, dryRun: !enforced })
  }
  seedLogs = [...logs, ...seedLogs].slice(0, 50)
  return {
    result: { matched: matched.length, paused: enforced ? matched.length : 0, deleted: 0, previewed: enforced ? 0 : matched.length, failed: 0 },
    at: Date.now(),
  }
}

export function seedPolicyReset(): { cleared: number } {
  return { cleared: 0 }
}

export function seedPolicyClearLogs(): { cleared: boolean } {
  seedLogs = []
  return { cleared: true }
}

// —— 分组限速 ——

let speedRules: SpeedPolicyRule[] = [
  { id: 'sp-1', name: 'PT 组限速', enabled: true, downLimit: 20480, upLimit: 5120, sites: ['private-hd.club'], labels: [], nameMatch: '' },
]
const speedGuard: SpeedPolicyGuard = { enforce: false }

export function speedPolicyAll(): { rules: SpeedPolicyRule[]; guard: SpeedPolicyGuard } {
  return { rules: clone(speedRules), guard: clone(speedGuard) }
}

export function speedPolicySave(rule: SpeedPolicyRule): { id: string } {
  const r = clone(rule)
  if (!r.id) r.id = 'spd-' + Date.now()
  const at = speedRules.findIndex((x) => x.id === r.id)
  if (at >= 0) speedRules[at] = r
  else speedRules.push(r)
  return { id: r.id }
}

export function speedPolicyRemove(id: string): void {
  speedRules = speedRules.filter((r) => r.id !== id)
}

export function speedPolicySaveGuard(guard: SpeedPolicyGuard): { saved: boolean } {
  speedGuard.enforce = guard.enforce
  return { saved: true }
}

export function speedPolicyRun(): { result: SpeedPolicyResult; at: number } {
  ensureDemo()
  const sites = torrentSites()
  const matched = list.filter((t) =>
    speedRules.some((r) => r.enabled && (r.sites.length === 0 || r.sites.some((s) => (sites[t.id] ?? []).includes(s))) && (r.labels.length === 0 || r.labels.some((l) => t.labels.includes(l)))),
  ).length
  return { result: { enabled: speedGuard.enforce, matched, applied: speedGuard.enforce ? matched : 0, released: 0, failed: 0 }, at: Date.now() }
}

// —— 自动文件管理 ——

let autoRules: AutoMoveRule[] = [
  { id: 'am-1', name: '视频归档', enabled: true, sites: [], labels: ['video'], nameMatch: '', targetDir: '/volume1/video' },
]

export function autoMoveAll(): { rules: AutoMoveRule[] } {
  return { rules: clone(autoRules) }
}

export function autoMoveSave(rule: AutoMoveRule): { id: string } {
  const r = clone(rule)
  if (!r.id) r.id = 'am-' + Date.now()
  const at = autoRules.findIndex((x) => x.id === r.id)
  if (at >= 0) autoRules[at] = r
  else autoRules.push(r)
  return { id: r.id }
}

export function autoMoveRemove(id: string): void {
  autoRules = autoRules.filter((r) => r.id !== id)
}

// —— 其他 ——

// 与真实后端一致：MaxMind GeoLite2 返回的是 ISO 3166-1 两位代码（如 CN），
// 此前 demo 返回国家全名，界面无法据此渲染国旗
const GEO_POOL = ['US', 'JP', 'DE', 'SG', 'FR', 'NL', 'CN', 'GB', 'CA', 'RU']

export function peersGeo(ips: string[]): Record<string, { country: string; city: string }> {
  const out: Record<string, { country: string; city: string }> = {}
  for (const ip of ips) {
    let h = 0
    for (let i = 0; i < ip.length; i++) h = (h * 31 + ip.charCodeAt(i)) | 0
    out[ip] = { country: GEO_POOL[Math.abs(h) % GEO_POOL.length], city: '' }
  }
  return out
}
