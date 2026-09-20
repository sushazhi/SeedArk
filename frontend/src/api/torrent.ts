import { client, request } from './client'
import type { AutoMoveRule, BandwidthGroup, CreateTorrentJobStatus, CreateTorrentServerOptions, PathMapping, SeedPolicyGuard, SeedPolicyLog, SeedPolicyResult, SeedPolicyRule, ServerInfo, ServerSessionResponse, Session, SessionStats, SessionStatus, SpeedPolicyGuard, SpeedPolicyResult, SpeedPolicyRule, Torrent } from '@/types'

// 种子相关接口
export const torrentApi = {
  // 获取种子列表
  list: () => request<Torrent[]>(client.get('/torrents')),
  // 获取每个种子的 Tracker 站点（站点过滤）
  sites: () => request<Record<number, string[]>>(client.get('/torrents/sites')),
  // 获取种子详情（含文件/Peers/Trackers）
  detail: (id: number) => request<Torrent>(client.get(`/torrents/${id}`)),
  // 添加种子（文件；filesWanted/filesUnwanted 为文件索引，不传=全部下载）
  addFile: (file: File, downloadDir?: string, paused = false, labels?: string[], priority?: number, verify = false, filesWanted?: number[], filesUnwanted?: number[]) => {
    const form = new FormData()
    form.append('file', file)
    if (downloadDir) form.append('downloadDir', downloadDir)
    if (paused) form.append('paused', 'true')
    if (verify) form.append('verify', 'true')
    if (labels?.length) form.append('labels', labels.join(','))
    if (priority != null) form.append('bandwidthPriority', String(priority))
    if (filesWanted?.length) form.append('filesWanted', JSON.stringify(filesWanted))
    if (filesUnwanted?.length) form.append('filesUnwanted', JSON.stringify(filesUnwanted))
    return request<{ id: number }>(client.post('/torrents/add', form))
  },
  // 添加种子（URL/磁力链接）
  addUrl: (url: string, downloadDir?: string, paused = false, labels?: string[], priority?: number, verify = false) =>
    request<{ id: number }>(client.post('/torrents/add', { url, downloadDir, paused, verify, labels, bandwidthPriority: priority })),
  // 添加种子（NAS 路径，飞牛文件选择器选中）
  addByPath: (path: string, downloadDir?: string, paused = false, labels?: string[], priority?: number, verify = false) =>
    request<{ id: number }>(client.post('/torrents/add', { path, downloadDir, paused, verify, labels, bandwidthPriority: priority })),
  // 批量添加多个URL/磁力链接。后端逐条尝试：失败的链接连同原因放在 failed 里返回，
  // 已成功的 id 照常返回，前端无需重试（重试会造成重复添加）
  addUrls: (urls: string[], downloadDir?: string, paused = false, labels?: string[], priority?: number, verify = false) =>
    request<{ ids: number[]; failed?: { url: string; error: string }[] }>(
      client.post('/torrents/add-batch', { urls, downloadDir, paused, verify, labels, bandwidthPriority: priority }),
    ),
  // 启动/强制启动/暂停/校验/重新宣告
  start: (id: number) => request(client.post(`/torrents/${id}/start`)),
  startNow: (id: number) => request(client.post(`/torrents/${id}/start-now`)),
  stop: (id: number) => request(client.post(`/torrents/${id}/stop`)),
  verify: (id: number) => request(client.post(`/torrents/${id}/verify`)),
  reannounce: (id: number) => request(client.post(`/torrents/${id}/reannounce`)),
  // 修改属性
  update: (id: number, body: Record<string, unknown>) => request(client.put(`/torrents/${id}`, body)),
  // 移动下载位置
  move: (id: number, location: string, move = true) =>
    request(client.post(`/torrents/${id}/move`, { location, move })),
  // 重命名文件/目录
  rename: (id: number, path: string, name: string) =>
    request(client.post(`/torrents/${id}/rename`, { path, name })),
  // 队列移动
  queue: (id: number, direction: 'top' | 'up' | 'down' | 'bottom') =>
    request(client.post(`/torrents/${id}/queue`, { direction })),
  // 删除
  remove: (id: number, deleteData = false) =>
    request(client.delete(`/torrents/${id}`, { params: { deleteData } })),
  // 批量操作
  startMany: (ids: number[]) => request(client.post('/torrents/start', { ids })),
  startNowMany: (ids: number[]) => request(client.post('/torrents/start-now', { ids })),
  stopMany: (ids: number[]) => request(client.post('/torrents/stop', { ids })),
  // 批量启动/暂停所有种子
  startAll: () => request(client.post('/torrents/start-all')),
  pauseAll: () => request(client.post('/torrents/pause-all')),
  reannounceAll: () => request(client.post('/torrents/reannounce-all')),
  moveMany: (ids: number[], location: string, move = true) =>
    request(client.post('/torrents/move', { ids, location, move })),
  updateMany: (ids: number[], body: Record<string, unknown>) =>
    request(client.post('/torrents/update', { ids, ...body })),
  removeMany: (ids: number[], deleteData = false) =>
    request(client.delete('/torrents', { data: { ids, deleteData } })),
  // Peer 地理位置查询（批量）
  peersGeo: (ips: string[]) =>
    request<Record<string, { country: string; city: string }>>(client.post('/peers/geo', { ips })),
  // 语义路径批量转换（fnOS：内部路径 → 宿主展示路径；不可用时 available=false，前端回退原始路径）
  semanticPaths: (paths: string[], language: 'zh' | 'en') =>
    request<{ available: boolean; map: Record<string, string> }>(client.post('/paths/semantic', { paths, language })),
  // 远端→本地路径映射配置（打开目录/复制路径时的展示转换）
  pathMap: () => request<{ mappings: PathMapping[] }>(client.get('/paths/map')),

  // 后端建种：服务器路径 + 多线程哈希（提交任务 → 轮询进度 → 下载/自动添加）
  createStart: (opts: CreateTorrentServerOptions) =>
    request<{ jobId: string }>(client.post('/torrent-create', opts)),
  createStatus: (jobId: string) =>
    request<CreateTorrentJobStatus>(client.get(`/torrent-create/${jobId}`)),
  createFile: async (jobId: string): Promise<Uint8Array> => {
    const resp = await client.get(`/torrent-create/${jobId}/file`, { responseType: 'arraybuffer' })
    return new Uint8Array(resp.data as ArrayBuffer)
  },
}

// 服务设置（连接配置 + MCP 服务开关）。
// 轮询间隔由后端 WS 推流与前端的 REST 兜底轮询共用，故在启动时读一次
export interface ServiceSettings {
  url: string
  user: string
  pollInterval?: string
  mcpEnabled?: boolean
  mcpAllowDelete?: boolean
  mcpAllowDangerous?: boolean
  mcpToken?: string
  mcpPort?: string
}

export const settingsApi = {
  get: () => request<ServiceSettings>(client.get('/settings')),
  update: (body: Record<string, unknown>) => request(client.put('/settings', body)),
}

// 系统命令
export const systemApi = {
  command: (action: 'shutdown' | 'reboot') => request(client.post(`/system/${action}`)),
}

// 会话相关接口
export const sessionApi = {
  get: () => request<Session>(client.get('/session')),
  update: (body: Record<string, unknown>) => request(client.put('/session', body)),
  // 指定服务器的会话读写：设置面板在服务器标签之间切换时，各自读各自的那台，
  // 避免「改了一台却把值写进另一台」
  getAt: (index: number) => request<ServerSessionResponse>(client.get(`/servers/${index}/session`)),
  updateAt: (index: number, body: Record<string, unknown>) =>
    request(client.put(`/servers/${index}/session`, body)),
  status: () => request<SessionStatus>(client.get('/session/status')),
  stats: () => request<SessionStats>(client.get('/session/stats')),
  portTest: () => request<{ open: boolean }>(client.get('/session/port-test')),
  blocklistUpdate: () => request<{ entries: number }>(client.post('/session/blocklist/update')),
  freeSpace: (path: string) =>
    request<{ path: string; freeSpace: number; totalSize: number }>(client.get('/session/free-space', { params: { path } })),
  // 带宽组（Transmission 4.x）：列出 / 创建或更新
  groups: () => request<BandwidthGroup[]>(client.get('/session/groups')),
  saveGroup: (g: Partial<BandwidthGroup> & { name: string }) =>
    request<{ saved: boolean }>(client.put('/session/groups', g)),
}

// 多服务器管理。save / remove / switch 的响应可能带 warning（状态写入失败、
// 切换到备用服务器失败等），调用方应展示给用户，避免以为操作完全成功
export const serverApi = {
  list: () => request<{ servers: ServerInfo[]; activeServer: number }>(client.get('/servers')),
  save: (servers: ServerInfo[]) => request<{ updated: boolean; warning?: string }>(client.post('/servers', { servers })),
  remove: (index: number) => request<{ deleted: boolean; warning?: string }>(client.delete(`/servers/${index}`)),
  switch: (index: number) => request<{ index: number; version: string; warning?: string }>(client.post('/servers/switch', { index })),
}

// 自动文件管理
export const autoMoveApi = {
  list: () => request<{ rules: AutoMoveRule[] }>(client.get('/automove')),
  save: (rule: AutoMoveRule) => request<{ id: string }>(client.post('/automove', rule)),
  remove: (id: string) => request(client.delete(`/automove/${id}`)),
  run: () => request(client.post('/automove/run')),
}

// 做种策略：按站点的分享率 / 做种时长目标与达标动作
export const seedPolicyApi = {
  list: () => request<{ rules: SeedPolicyRule[]; guard: SeedPolicyGuard; logs: SeedPolicyLog[] }>(client.get('/seedpolicy')),
  save: (rule: SeedPolicyRule) => request<{ id: string }>(client.post('/seedpolicy', rule)),
  remove: (id: string) => request(client.delete(`/seedpolicy/${id}`)),
  saveGuard: (guard: SeedPolicyGuard) => request<{ saved: boolean }>(client.post('/seedpolicy/guard', guard)),
  run: () => request<{ result: SeedPolicyResult; at: number }>(client.post('/seedpolicy/run')),
  reset: () => request<{ cleared: number }>(client.post('/seedpolicy/reset')),
  clearLogs: () => request<{ cleared: boolean }>(client.post('/seedpolicy/clear-logs')),
}

// 组内总限速：按站点 / 标签 / 名称分组的共享带宽上限
export const speedPolicyApi = {
  list: () => request<{ rules: SpeedPolicyRule[]; guard: SpeedPolicyGuard }>(client.get('/speedpolicy')),
  save: (rule: SpeedPolicyRule) => request<{ id: string }>(client.post('/speedpolicy', rule)),
  remove: (id: string) => request(client.delete(`/speedpolicy/${id}`)),
  saveGuard: (guard: SpeedPolicyGuard) => request<{ saved: boolean }>(client.post('/speedpolicy/guard', guard)),
  run: () => request<{ result: SpeedPolicyResult; at: number }>(client.post('/speedpolicy/run')),
}

// 检查更新（飞牛部署：后端查 GitHub Releases，gh-proxy 回退下载 fpk 更新包）
export interface UpdateCheckResult {
  currentVersion: string
  latestVersion: string
  hasUpdate: boolean
  changelog: string
  publishedAt: string
  releaseUrl: string
  fpkUrl: string
  fpkSize: number
  arch: string
  downloadReady: boolean
}

export interface UpdateStatus {
  updating: boolean
  failed: boolean
  progress: number
  message: string
  latestVersion: string
  fpkFilename: string
  downloadUrl?: string
}

export const updateApi = {
  check: () => request<UpdateCheckResult>(client.get('/update/check')),
  install: () => request<{ message: string }>(client.post('/update/install')),
  status: () => request<UpdateStatus>(client.get('/update/status')),
}
