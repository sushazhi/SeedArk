// 保种台的演示数据。
//
// 演示模式跑在浏览器里、没有后端，因此这里自己造一套自洽的数据，
// 让七个页签都有内容可看（调研文档 §4.4 列的必补项：不同站点/不同效率的种子、
// 档案库条目、一个同内容分组、发种资料包）。数据是**手工编排**的而非从种子列表
// 推导：演示需要保证每个页签都有代表性内容（无效做种、保种契约、超预算各来一个），
// 推导出来的集合未必恰好覆盖这些情形。
//
// 口径与真实后端保持一致：所有释放时间/效率都标明是估算，不提供任何「自动删除」操作。

import type {
  PTCalendar,
  PTContract,
  PTContractRequest,
  PTLedger,
  PTMatrix,
  PTOverview,
  PTPublishPreview,
  PTPublishRequest,
  PTSiteProfile,
  PTSitesResponse,
  PTTorrentRef,
  PTVaultDetail,
  PTVaultEntry,
  PTVaultList,
} from '@/types/pt'

const HOUR = 3600
const DAY = 86400
const GB = 1 << 30

/** 演示时钟：以页面打开时刻为「今天」，避免写死日期后看起来永远过期 */
const NOW = Math.floor(Date.now() / 1000)
const dayKey = (offsetDays: number) => new Date((NOW + offsetDays * DAY) * 1000).toISOString().slice(0, 10)

// —— 站点规则（与真实实现同构：一处配置，释放日历与做种策略都取它）——
let profiles: PTSiteProfile[] = [
  { id: 'sp-1', name: '影音之家', match: 'private-hd.club', minSeedDays: 7, minRatio: 1.2, hrSeedHours: 72, budgetGb: 200, enabled: true },
  { id: 'sp-2', name: '开源镜像站', match: 'open-mirror.net', minSeedDays: 3, minRatio: 1, hrSeedHours: 0, budgetGb: 80, enabled: true },
  { id: 'sp-3', name: '综合站', match: 'public.pushep.com', minSeedDays: 0, minRatio: 0, hrSeedHours: 0, budgetGb: 0, enabled: true },
]

// —— 保种契约（①）——
let contracts: Record<string, PTContract> = {
  'aa11bb22cc33dd44ee55ff667788990011223344': { hash: 'aa11bb22cc33dd44ee55ff667788990011223344', until: NOW + 3 * DAY, note: '站庆活动要求', createdAt: NOW - 27 * DAY },
  'ff00ee11dd22cc33bb44aa556677889900112233': { hash: 'ff00ee11dd22cc33bb44aa556677889900112233', until: NOW - 2 * DAY, note: '', createdAt: NOW - 40 * DAY },
}

function ref(r: Partial<PTTorrentRef> & Pick<PTTorrentRef, 'id' | 'name' | 'hash' | 'site' | 'size'>): PTTorrentRef {
  return {
    serverName: 'qBittorrent',
    kind: 'qbittorrent',
    seedDays: 30,
    ratio: 1.5,
    uploaded30d: 0,
    efficiency: 0,
    score: 50,
    lastUpload: NOW - DAY,
    status: 'seeding',
    labels: [],
    hrRisk: false,
    hrDeadline: 0,
    releaseAt: 0,
    releaseReason: 'unknown',
    estimated: true,
    idleDays: 0,
    uploadedToday: 0,
    todayQuality: 'exact',
    invalidSeed: false,
    hasContract: false,
    contractUntil: 0,
    contractDaysLeft: 0,
    contractBreach: false,
    ...r,
  }
}

// 六颗种子覆盖：高/低效率、僵尸、心跳正常、无效做种、契约将到期/已过期、超预算站点
const rows: PTTorrentRef[] = [
  ref({
    id: 1, name: 'Dune.Part.Two.2024.2160p.WEB-DL', hash: 'aa11bb22cc33dd44ee55ff667788990011223344',
    site: '影音之家', size: 62 * GB, seedDays: 12.4, ratio: 2.31, uploaded30d: 480 * GB,
    efficiency: 24.6, score: 88, status: 'seeding', labels: ['video', 'hd'],
    idleDays: 0, uploadedToday: 3.8 * GB, todayQuality: 'exact',
    releaseAt: NOW + 21 * DAY, releaseReason: 'min_days',
    hasContract: true, contractUntil: NOW + 3 * DAY, contractDaysLeft: 3,
  }),
  ref({
    id: 2, name: 'The.Wire.S01.1080p.BluRay', hash: 'bb22cc33dd44ee55ff6677889900112233445566',
    site: '影音之家', size: 38 * GB, seedDays: 96, ratio: 3.42, uploaded30d: 120 * GB,
    efficiency: 3.2, score: 62, status: 'seeding', labels: ['video'], hrRisk: true,
    hrDeadline: NOW + 9 * HOUR, idleDays: 1, uploadedToday: 120 * 1024 * 1024, todayQuality: 'exact',
    releaseAt: NOW + 9 * HOUR, releaseReason: 'hr',
  }),
  ref({
    id: 3, name: 'LibreOffice.Help.Docs.All.Langs', hash: 'ff00ee11dd22cc33bb44aa556677889900112233',
    site: '开源镜像站', size: 1.9 * GB, seedDays: 140, ratio: 1.02, uploaded30d: 2 * GB,
    efficiency: 0.4, score: 30, status: 'seeding', labels: ['docs'],
    idleDays: 63, uploadedToday: 0, todayQuality: 'unknown',
    releaseAt: NOW - 20 * DAY, releaseReason: 'ratio',
    hasContract: true, contractUntil: NOW - 2 * DAY, contractDaysLeft: -2, contractBreach: true,
  }),
  ref({
    id: 4, name: 'Vintage.Film.Collection.Remastered', hash: 'cc33dd44ee55ff66778899001122334455667788',
    site: '影音之家', size: 148 * GB, seedDays: 210, ratio: 0.42, uploaded30d: 0,
    efficiency: 0, score: 8, status: 'paused', labels: ['video'],
    idleDays: 88, uploadedToday: 0, todayQuality: 'rollback',
    invalidSeed: true, invalidReason: 'Torrent not registered with this tracker',
  }),
  ref({
    id: 5, name: 'ubuntu-24.04.1-desktop-amd64.iso', hash: 'dd44ee55ff6677889900112233445566778899aa',
    site: '开源镜像站', size: 5.8 * GB, seedDays: 5.2, ratio: 5.1, uploaded30d: 210 * GB,
    efficiency: 36.2, score: 96, status: 'seeding', labels: ['linux', 'iso'],
    idleDays: 0, uploadedToday: 7.2 * GB, todayQuality: 'exact',
    releaseAt: NOW - 2 * DAY, releaseReason: 'min_days',
  }),
  ref({
    id: 6, name: 'KDE.Plasma.Wallpapers.Pack', hash: 'ee55ff6677889900112233445566778899aabb',
    site: '综合站', size: 0.96 * GB, seedDays: 8, ratio: 0.9, uploaded30d: 1.2 * GB,
    efficiency: 1.2, score: 34, status: 'seeding', labels: ['wallpapers'],
    idleDays: 4, uploadedToday: 8 * 1024 * 1024, todayQuality: 'partial',
    serverName: 'Transmission', kind: 'transmission',
  }),
]

function siteSizes() {
  const m = new Map<string, { count: number; size: number }>()
  for (const r of rows) {
    const cur = m.get(r.site) ?? { count: 0, size: 0 }
    cur.count++
    cur.size += r.size
    m.set(r.site, cur)
  }
  return Array.from(m, ([site, v]) => ({ site, ...v }))
}

function siteStatOf(site: string) {
  const list = rows.filter((r) => r.site === site)
  const size = list.reduce((a, r) => a + r.size, 0)
  const up30 = list.reduce((a, r) => a + r.uploaded30d, 0)
  const profile = profiles.find((p) => p.name === site)
  const budgetBytes = profile && profile.budgetGb > 0 ? profile.budgetGb * GB : 0
  // 影音之家故意超预算，用来演示⑦的标记与告警
  const over = site === '影音之家'
  return {
    site,
    count: list.length,
    size,
    uploaded30d: up30,
    ratio: size > 0 ? Math.round((up30 / size) * 100) / 100 : 0,
    budgetBytes: over ? 100 * GB : budgetBytes,
    overBudget: over,
    uploadedToday: list.reduce((a, r) => a + r.uploadedToday, 0),
    todayQuality: (list.some((r) => r.todayQuality !== 'exact') ? 'partial' : 'exact') as PTTorrentRef['todayQuality'],
  }
}

export function ptOverview(): PTOverview {
  const totalSize = rows.reduce((a, r) => a + r.size, 0)
  const uploaded30d = rows.reduce((a, r) => a + r.uploaded30d, 0)
  const eff = rows.filter((r) => r.size > 0)
  return {
    torrentCount: rows.length,
    seedCount: rows.filter((r) => r.status === 'seeding').length,
    totalSize,
    uploaded30d,
    avgEfficiency: Math.round((eff.reduce((a, r) => a + r.efficiency, 0) / eff.length) * 10) / 10,
    hrRiskCount: rows.filter((r) => r.hrRisk).length,
    readyCount: 2,
    readyBytes: 7.7 * GB,
    vaultCount: 3,
    vaultBytes: 12.4 * GB,
    groupCount: 2,
    duplicateBytes: 55.5 * GB,
    sites: ['影音之家', '开源镜像站', '综合站'].map(siteStatOf),
    upcoming: [
      { date: dayKey(1), count: 1, bytes: 62 * GB },
      { date: dayKey(21), count: 1, bytes: 38 * GB },
    ],
    generatedAt: NOW,
    coverage: { days: 34, from: dayKey(-34), to: dayKey(0), lastSample: NOW - 60, full: true },
    idleCount: rows.filter((r) => r.idleDays >= 7).length,
    idleBytes: rows.filter((r) => r.idleDays >= 7).reduce((a, r) => a + r.size, 0),
    invalidCount: rows.filter((r) => r.invalidSeed).length,
    invalidBytes: rows.filter((r) => r.invalidSeed).reduce((a, r) => a + r.size, 0),
    contractCount: rows.filter((r) => r.hasContract).length,
    contractBreach: rows.filter((r) => r.contractBreach).length,
    contractDueCount: rows.filter((r) => r.hasContract && !r.contractBreach && r.contractDaysLeft <= 7).length,
    contractDueBytes: rows.filter((r) => r.hasContract && !r.contractBreach && r.contractDaysLeft <= 7).reduce((a, r) => a + r.size, 0),
    overBudgetCount: 1,
    overBudgetBytes: 148 * GB,
    uploadedToday: rows.reduce((a, r) => a + r.uploadedToday, 0),
    todayQuality: 'partial',
    todayNote: `相对 ${dayKey(-1)}：部分种子当日新加或未被采到，合计偏小`,
    warnings: [
      '1 颗种子的站点已判为未注册（无效做种），占 148 GB',
      '2 颗种子连续 7 天以上没有上传增长，占 150 GB',
      '1 颗种子的保种契约已过期未解除',
      '1 个站点超出保种预算，合计超出 148 GB',
    ],
  }
}

export function ptLedger(): PTLedger {
  const sorted = [...rows].sort((a, b) => b.score - a.score)
  const eff = rows.filter((r) => r.size > 0)
  return {
    rows: sorted,
    totals: {
      count: rows.length,
      size: rows.reduce((a, r) => a + r.size, 0),
      uploaded30d: rows.reduce((a, r) => a + r.uploaded30d, 0),
      avgEfficiency: Math.round((eff.reduce((a, r) => a + r.efficiency, 0) / eff.length) * 10) / 10,
      zeroUpload: rows.filter((r) => r.uploaded30d <= 0).length,
      idleCount: rows.filter((r) => r.idleDays >= 7).length,
      invalidCount: rows.filter((r) => r.invalidSeed).length,
      contractBreach: rows.filter((r) => r.contractBreach).length,
    },
    sites: siteSizes(),
  }
}

export function ptCalendar(month?: string): PTCalendar {
  const m = month ?? new Date(NOW * 1000).toISOString().slice(0, 7)
  const ready = rows.filter((r) => r.releaseAt > 0 && r.releaseAt <= NOW)
  const soon = rows.filter((r) => r.releaseAt > NOW)
  const dayOf = (r: PTTorrentRef) => new Date(r.releaseAt * 1000).toISOString().slice(0, 10)
  return {
    month: m,
    days: [
      ...ready.map((r) => ({ date: dayOf(r), count: 0, bytes: 0, ready: true, hrRisk: 0, contractDue: 0, torrents: [] as never[] })),
      ...soon.map((r) => ({ date: dayOf(r), count: 1, bytes: r.size, ready: false, hrRisk: r.hrRisk ? 1 : 0, contractDue: 0, torrents: [{ id: r.id, name: r.name, site: r.site, size: r.size, seedDays: r.seedDays, hrRisk: r.hrRisk, releaseAt: r.releaseAt, reason: r.releaseReason }] })),
    ].reduce<PTCalendar['days']>((acc, d) => {
      const hit = acc.find((x) => x.date === d.date)
      if (hit) { hit.count += d.count; hit.bytes += d.bytes; hit.hrRisk += d.hrRisk; hit.torrents.push(...d.torrents) }
      else acc.push(d)
      return acc
    }, []),
    unknown: { count: 0, bytes: 0 },
    summary: {
      todayBytes: ready.reduce((a, r) => a + r.size, 0),
      weekBytes: 0,
      monthBytes: rows.reduce((a, r) => a + r.size, 0),
      hrCount: rows.filter((r) => r.hrRisk).length,
      contractDue: rows.filter((r) => r.hasContract && !r.contractBreach).length,
      contractBreach: rows.filter((r) => r.contractBreach).length,
    },
  }
}

const vaultEntries: PTVaultEntry[] = [
  { id: 'v1', hash: '1122334455667788990011223344556677889900', name: 'Interstellar.2014.2160p.REMUX', site: '影音之家', size: 92 * GB, deletedAt: NOW - 6 * DAY, seedDays: 180, ratio: 4.2, uploadedEver: 386 * GB, labels: ['video', '4k'], serverName: 'qBittorrent', kind: 'qbittorrent', savePath: '/volume1/video', hasTorrent: false, canRevive: true },
  { id: 'v2', hash: '2233445566778899001122334455667788990011', name: 'debian-12.5.0-amd64-DVD-1.iso', site: '开源镜像站', size: 4.7 * GB, deletedAt: NOW - 15 * DAY, seedDays: 60, ratio: 3.05, uploadedEver: 14 * GB, labels: ['iso'], serverName: 'qBittorrent', kind: 'qbittorrent', savePath: '/downloads', hasTorrent: false, canRevive: true },
  { id: 'v3', hash: '3344556677889900112233445566778899001122', name: 'Blender.Foundation.Sintel.4K', site: '综合站', size: 12.5 * GB, deletedAt: NOW - 40 * DAY, seedDays: 210, ratio: 1.02, uploadedEver: 12.8 * GB, labels: ['video'], serverName: 'Transmission', kind: 'transmission', savePath: '/volume1/video', hasTorrent: false, canRevive: true },
]

export function ptVault(q?: string): PTVaultList {
  const query = (q ?? '').trim().toLowerCase()
  const entries = query
    ? vaultEntries.filter((e) => `${e.name} ${e.hash} ${e.site}`.toLowerCase().includes(query))
    : vaultEntries
  return { entries, total: entries.length, bytes: entries.reduce((a, e) => a + e.size, 0) }
}

export function ptVaultDetail(id: string): PTVaultDetail | null {
  const entry = vaultEntries.find((e) => e.id === id)
  if (!entry) return null
  return {
    entry,
    files: [
      { name: `${entry.name}.mkv`, size: entry.size - 20 * 1024 * 1024 },
      { name: 'sample.mkv', size: 8 * 1024 * 1024 },
      { name: 'poster.jpg', size: 12 * 1024 * 1024 },
    ],
  }
}

export function ptMatrix(): PTMatrix {
  return {
    groups: [
      {
        key: 'dune part two 2024 2160p web dl',
        name: 'Dune.Part.Two.2024.2160p.WEB-DL',
        size: 62 * GB,
        totalBytes: 130 * GB,
        duplicateBytes: 68 * GB,
        sameDownloader: false,
        copies: [
          { id: 1, site: '影音之家', serverName: 'qBittorrent', kind: 'qbittorrent', status: 'seeding', size: 62 * GB, uploaded: 480 * GB, labels: ['video'] },
          { id: 9, site: '综合站', serverName: 'Transmission', kind: 'transmission', status: 'seeding', size: 68 * GB, uploaded: 96 * GB, labels: ['video'] },
        ],
      },
      {
        key: 'ubuntu 24 04 1 desktop amd64 iso',
        name: 'ubuntu-24.04.1-desktop-amd64.iso',
        size: 5.8 * GB,
        totalBytes: 11.6 * GB,
        duplicateBytes: 5.8 * GB,
        sameDownloader: true,
        copies: [
          { id: 5, site: '开源镜像站', serverName: 'qBittorrent', kind: 'qbittorrent', status: 'seeding', size: 5.8 * GB, uploaded: 210 * GB, labels: ['linux'] },
          { id: 10, site: '开源镜像站', serverName: 'qBittorrent', kind: 'qbittorrent', status: 'paused', size: 5.8 * GB, uploaded: 0, labels: ['linux'] },
        ],
      },
    ],
    summary: { groups: 2, duplicateBytes: 73.8 * GB },
  }
}

export function ptSites(): PTSitesResponse {
  return {
    profiles: [...profiles],
    detected: [
      { site: '影音之家', count: 3 },
      { site: '开源镜像站', count: 2 },
      { site: '综合站', count: 1 },
    ],
  }
}

export function ptSaveSite(p: PTSiteProfile): PTSiteProfile {
  const saved = { ...p, id: p.id || `sp-${Date.now()}` }
  const i = profiles.findIndex((x) => x.id === saved.id)
  if (i >= 0) profiles[i] = saved
  else profiles = [...profiles, saved]
  return saved
}

export function ptDeleteSite(id: string): boolean {
  const before = profiles.length
  profiles = profiles.filter((p) => p.id !== id)
  return profiles.length < before
}

export function ptSetContract(req: PTContractRequest): PTContract {
  const c: PTContract = {
    hash: req.hash.toLowerCase(),
    until: req.days && req.days > 0 ? NOW + req.days * DAY : (req.until ?? NOW + 30 * DAY),
    note: req.note ?? '',
    createdAt: NOW,
  }
  contracts = { ...contracts, [c.hash]: c }
  const row = rows.find((r) => r.hash === c.hash)
  if (row) {
    row.hasContract = true
    row.contractUntil = c.until
    row.contractDaysLeft = Math.floor((c.until - NOW) / DAY)
    row.contractBreach = row.contractDaysLeft < 0
  }
  return c
}

export function ptDeleteContract(hash: string): boolean {
  const key = hash.toLowerCase()
  const existed = key in contracts
  const { [key]: _drop, ...rest } = contracts
  contracts = rest
  const row = rows.find((r) => r.hash === key)
  if (row) {
    row.hasContract = false
    row.contractUntil = 0
    row.contractDaysLeft = 0
    row.contractBreach = false
  }
  return existed
}

// —— 发种 ——
export function ptPublishPreview(req: PTPublishRequest): PTPublishPreview {
  const warnings: string[] = []
  if (!req.name?.trim()) warnings.push('缺少种子名称')
  if (!req.mediaInfo?.trim()) warnings.push('未粘贴 MediaInfo，发布前请补齐')
  if (!req.screenshots?.length) warnings.push('未填写截图链接，发布前请补齐')
  const lines = [
    ...(req.screenshots ?? []).map((s) => `[img]${s}[/img]`),
    '[quote]',
    req.title ? `[b]译名[/b] ${req.title}` : '',
    req.name ? `[b]片名[/b] ${req.name}` : '',
    req.resolution ? `[b]分辨率[/b] ${req.resolution}` : '',
    req.video ? `[b]视频编码[/b] ${req.video}` : '',
    req.audio ? `[b]音频[/b] ${req.audio}` : '',
    req.subtitle ? `[b]字幕[/b] ${req.subtitle}` : '',
    req.sizeBytes ? `[b]大小[/b] ${(req.sizeBytes / GB).toFixed(2)} GB` : '',
    '[/quote]',
    req.mediaInfo ? `[hide]MediaInfo\n${req.mediaInfo}\n[/hide]` : '',
  ]
  return { bbcode: lines.filter(Boolean).join('\n'), warnings }
}

/** 演示资料包：真的能打开的最小 ZIP（stored，不压缩）。 */
function crc32(bytes: Uint8Array): number {
  let c = ~0
  for (let i = 0; i < bytes.length; i++) {
    c ^= bytes[i]
    for (let k = 0; k < 8; k++) c = (c >>> 1) ^ (0xedb88320 & -(c & 1))
  }
  return ~c >>> 0
}

function makeZip(files: { name: string; text: string }[]): Blob {
  const enc = new TextEncoder()
  const chunks: Uint8Array[] = []
  const central: Uint8Array[] = []
  let offset = 0
  const u16 = (v: number) => [v & 0xff, (v >> 8) & 0xff]
  const u32 = (v: number) => [v & 0xff, (v >> 8) & 0xff, (v >> 16) & 0xff, (v >>> 24) & 0xff]

  for (const f of files) {
    const nameBytes = enc.encode(f.name)
    const data = enc.encode(f.text)
    const crc = crc32(data)
    const local = new Uint8Array([
      0x50, 0x4b, 0x03, 0x04, ...u16(20), ...u16(0), ...u16(0), ...u16(0), ...u16(0),
      ...u32(crc), ...u32(data.length), ...u32(data.length), ...u16(nameBytes.length), ...u16(0),
    ])
    chunks.push(local, nameBytes, data)
    central.push(new Uint8Array([
      0x50, 0x4b, 0x01, 0x02, ...u16(20), ...u16(20), ...u16(0), ...u16(0), ...u16(0), ...u16(0),
      ...u32(crc), ...u32(data.length), ...u32(data.length), ...u16(nameBytes.length),
      ...u16(0), ...u16(0), ...u16(0), ...u16(0), ...u32(0), ...u32(offset),
    ]), nameBytes)
    offset += local.length + nameBytes.length + data.length
  }
  const centralSize = central.reduce((a, c) => a + c.length, 0)
  const end = new Uint8Array([
    0x50, 0x4b, 0x05, 0x06, ...u16(0), ...u16(0), ...u16(files.length), ...u16(files.length),
    ...u32(centralSize), ...u32(offset), ...u16(0),
  ])
  // Uint8Array 的 buffer 类型在 TS 里是 ArrayBufferLike，与 BlobPart 不兼容，这里显式转一次
  return new Blob([...chunks, ...central, end] as unknown as BlobPart[], { type: 'application/zip' })
}

export function ptPublishKit(req: PTPublishRequest, torrentName?: string): Blob {
  const pv = ptPublishPreview(req)
  const files = [
    { name: 'bbcode.txt', text: pv.bbcode },
    { name: 'mediainfo.txt', text: req.mediaInfo || '(未提供 MediaInfo)' },
    { name: 'checklist.txt', text: `发布前检查清单\n\n[x] 标题/种子名称已填写\n${pv.warnings.map((w) => `[ ] ${w}`).join('\n')}\n\n注意：资料包只生成发布素材，SeedArk 不会自动上传到站点。\n` },
  ]
  if (torrentName) files.push({ name: torrentName, text: '(演示模式：未附带真实种子文件)' })
  return makeZip(files)
}

/** 站点分布是只读视图，这里没有额外需要模拟的写操作 */
export function ptSample(): { sampled: boolean; count: number } {
  return { sampled: true, count: rows.length }
}
