import type { AxiosAdapter, AxiosResponse, InternalAxiosRequestConfig } from 'axios'
import type { ApiResponse } from '@/types'
import {
  addTorrent,
  autoMoveAll,
  autoMoveRemove,
  autoMoveSave,
  demoSettings,
  findTorrent,
  freeSpace,
  getSession,
  listTorrents,
  moveTorrent,
  pauseAll,
  peersGeo,
  queueMove,
  removeTorrents,
  renameTorrent,
  seedPolicyAll,
  seedPolicyClearLogs,
  seedPolicyRemove,
  seedPolicyReset,
  seedPolicyRun,
  seedPolicySave,
  seedPolicySaveGuard,
  serverList,
  serverRemove,
  serverSave,
  serverSession,
  serverSwitch,
  serverFreeSpace,
  sessionStats,
  speedPolicyAll,
  speedPolicyRemove,
  speedPolicyRun,
  speedPolicySave,
  speedPolicySaveGuard,
  startAll,
  startTorrent,
  stopTorrent,
  torrentDetail,
  torrentSites,
  updateDemoSettings,
  updateSession,
  updateTorrent,
  verifyTorrent,
  APP_VERSION,
} from './state'

// 演示模式 axios adapter：全部 /api 请求在浏览器内应答，无需真实后端。
// 未覆盖的接口返回 code!=0，由 request() 统一弹错

const wait = (ms: number) => new Promise<void>((r) => setTimeout(r, ms))

function respond(config: InternalAxiosRequestConfig, data?: unknown): AxiosResponse<ApiResponse> {
  return { data: { code: 0, message: '', data: data ?? null }, status: 200, statusText: 'OK', headers: {}, config }
}

function fail(config: InternalAxiosRequestConfig, message: string): AxiosResponse<ApiResponse> {
  return { data: { code: 1, message, data: null }, status: 200, statusText: 'OK', headers: {}, config }
}

function formDataToObject(data: FormData): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  data.forEach((value, key) => { out[key] = value })
  return out
}

function parseLabels(v: unknown): string[] | undefined {
  if (Array.isArray(v)) return v.map(String)
  if (typeof v === 'string' && v.trim()) return v.split(',').map((s) => s.trim()).filter(Boolean)
  return undefined
}

function num(v: unknown): number | undefined {
  const n = Number(v)
  return Number.isFinite(n) ? n : undefined
}

export const demoAdapter: AxiosAdapter = async (config) => {
  await wait(50 + Math.random() * 100)
  const method = (config.method ?? 'get').toLowerCase()
  const url = (config.url ?? '').split('?')[0].replace(/\/+$/, '') || '/'
  let body: Record<string, unknown> = {}
  if (config.data instanceof FormData) {
    body = formDataToObject(config.data)
  } else if (typeof config.data === 'string') {
    try { body = JSON.parse(config.data) as Record<string, unknown> } catch { body = {} }
  } else if (config.data && typeof config.data === 'object') {
    body = config.data as Record<string, unknown>
  }
  const params = (config.params ?? {}) as Record<string, unknown>
  const ids = (v: unknown): number[] => (Array.isArray(v) ? v.map(Number) : []).filter((n) => Number.isFinite(n))

  // —— 种子 ——
  if (url === '/torrents' && method === 'get') return respond(config, listTorrents())
  if (url === '/torrents/sites' && method === 'get') return respond(config, torrentSites())
  if (url === '/torrents/add' && method === 'post') {
    const file = body.file as { name?: string } | undefined
    return respond(config, {
      id: addTorrent({
        url: body.url as string | undefined,
        path: body.path as string | undefined,
        name: file?.name,
        downloadDir: body.downloadDir as string | undefined,
        paused: body.paused === true || body.paused === 'true',
        verify: body.verify === true || body.verify === 'true',
        labels: parseLabels(body.labels),
        bandwidthPriority: num(body.bandwidthPriority),
      }),
    })
  }
  if (url === '/torrents/add-batch' && method === 'post') {
    const urls = Array.isArray(body.urls) ? (body.urls as string[]) : []
    const newIds = urls.map((u) => addTorrent({
      url: u,
      downloadDir: body.downloadDir as string | undefined,
      paused: body.paused === true,
      labels: parseLabels(body.labels),
      bandwidthPriority: num(body.bandwidthPriority),
    }))
    return respond(config, { ids: newIds })
  }
  if (url === '/torrents/start' && method === 'post') { ids(body.ids).forEach((id) => { const t = findTorrent(id); if (t) startTorrent(t, false) }); return respond(config) }
  if (url === '/torrents/start-now' && method === 'post') { ids(body.ids).forEach((id) => { const t = findTorrent(id); if (t) startTorrent(t, true) }); return respond(config) }
  if (url === '/torrents/stop' && method === 'post') { ids(body.ids).forEach((id) => { const t = findTorrent(id); if (t) stopTorrent(t) }); return respond(config) }
  if (url === '/torrents/start-all' && method === 'post') { startAll(true); return respond(config) }
  if (url === '/torrents/pause-all' && method === 'post') { pauseAll(); return respond(config) }
  if (url === '/torrents/reannounce-all' && method === 'post') return respond(config)
  if (url === '/torrents/move' && method === 'post') { ids(body.ids).forEach((id) => { const t = findTorrent(id); if (t) moveTorrent(t, String(body.location ?? '')) }); return respond(config) }
  if (url === '/torrents/update' && method === 'post') { ids(body.ids).forEach((id) => { const t = findTorrent(id); if (t) updateTorrent(t, body) }); return respond(config) }
  if (url === '/torrents' && method === 'delete') { removeTorrents(ids((body as { ids?: number[] }).ids)); return respond(config) }

  const m = /^\/torrents\/(\d+)(?:\/(start|start-now|stop|verify|reannounce|move|rename|queue))?$/.exec(url)
  if (m) {
    const t = findTorrent(Number(m[1]))
    if (!t) return fail(config, '种子不存在（演示数据已被删除）')
    const sub = m[2]
    if (!sub && method === 'get') return respond(config, torrentDetail(t.id))
    if (!sub && method === 'put') { updateTorrent(t, body); return respond(config) }
    if (method === 'delete' && !sub) { removeTorrents([t.id]); return respond(config) }
    if (method === 'post') {
      switch (sub) {
        case 'start': startTorrent(t, false); break
        case 'start-now': startTorrent(t, true); break
        case 'stop': stopTorrent(t); break
        case 'verify': verifyTorrent(t); break
        case 'reannounce': break
        case 'move': moveTorrent(t, String(body.location ?? '')); break
        case 'rename': renameTorrent(t, String(body.path ?? ''), String(body.name ?? '')); break
        case 'queue': queueMove(t, String(body.direction ?? '')); break
      }
      return respond(config)
    }
  }

  // —— 系统 / 会话 ——
  if (url === '/peers/geo' && method === 'post') return respond(config, peersGeo(Array.isArray(body.ips) ? (body.ips as string[]) : []))
  if (url === '/paths/semantic' && method === 'post') return respond(config, { available: false, map: {} })
  if (url.startsWith('/system/') && method === 'post') return respond(config)
  if (url === '/session' && method === 'get') return respond(config, getSession())
  if (url === '/session' && method === 'put') { updateSession(body); return respond(config) }
  if (url === '/session/status' && method === 'get') return respond(config, { connected: true, version: APP_VERSION })
  if (url === '/session/stats' && method === 'get') return respond(config, sessionStats())
  if (url === '/session/port-test' && method === 'get') return respond(config, { open: true })
  if (url === '/session/blocklist/update' && method === 'post') return respond(config, { entries: 0 })
  if (url === '/session/free-space' && method === 'get') return respond(config, freeSpace(String(params.path ?? '')))

  // —— 应用设置 ——
  if (url === '/settings' && method === 'get') return respond(config, { ...demoSettings })
  if (url === '/settings' && method === 'put') { updateDemoSettings(body); return respond(config, { updated: true }) }

  // —— 多服务器 ——
  if (url === '/servers' && method === 'get') return respond(config, serverList())
  if (url === '/servers' && method === 'post') { serverSave((body.servers ?? []) as never[]); return respond(config) }
  if (url === '/servers/switch' && method === 'post') return respond(config, serverSwitch(num(body.index) ?? 0))
  // 指定服务器的会话：桌面端设置面板按服务器标签各读各的，缺这条会让
  // 「下载器设置」进去就报错（adapter 兜底返回「未提供该接口」）
  const srvSession = /^\/servers\/(\d+)\/session$/.exec(url)
  if (srvSession) {
    const idx = Number(srvSession[1])
    const res = serverSession(idx)
    if (!res) return fail(config, '服务器不存在')
    if (method === 'get') return respond(config, res)
    // 写：演示模式只把改动记进内存（不区分具体键，行为上等价于"已保存"）
    return respond(config, { updated: true })
  }
  const serverDel = /^\/servers\/(\d+)$/.exec(url)
  if (serverDel && method === 'delete') { serverRemove(Number(serverDel[1])); return respond(config) }
  // 指定服务器的磁盘余量：聚合视图下侧栏逐台显示
  const srvFree = /^\/servers\/(\d+)\/free-space$/.exec(url)
  if (srvFree && method === 'get') {
    const res = serverFreeSpace(Number(srvFree[1]))
    if (!res) return fail(config, '服务器不存在')
    return respond(config, res)
  }

  // —— 做种策略 ——
  if (url === '/seedpolicy' && method === 'get') return respond(config, seedPolicyAll())
  if (url === '/seedpolicy' && method === 'post') return respond(config, seedPolicySave(body as never))
  if (url === '/seedpolicy/guard' && method === 'post') return respond(config, seedPolicySaveGuard(body as never))
  if (url === '/seedpolicy/run' && method === 'post') return respond(config, seedPolicyRun())
  if (url === '/seedpolicy/reset' && method === 'post') return respond(config, seedPolicyReset())
  if (url === '/seedpolicy/clear-logs' && method === 'post') return respond(config, seedPolicyClearLogs())
  const seedDel = /^\/seedpolicy\/(.+)$/.exec(url)
  if (seedDel && method === 'delete') { seedPolicyRemove(seedDel[1]); return respond(config) }

  // —— 分组限速 ——
  if (url === '/speedpolicy' && method === 'get') return respond(config, speedPolicyAll())
  if (url === '/speedpolicy' && method === 'post') return respond(config, speedPolicySave(body as never))
  if (url === '/speedpolicy/guard' && method === 'post') return respond(config, speedPolicySaveGuard(body as never))
  if (url === '/speedpolicy/run' && method === 'post') return respond(config, speedPolicyRun())
  const speedDel = /^\/speedpolicy\/(.+)$/.exec(url)
  if (speedDel && method === 'delete') { speedPolicyRemove(speedDel[1]); return respond(config) }

  // —— 自动文件管理 ——
  if (url === '/automove' && method === 'get') return respond(config, autoMoveAll())
  if (url === '/automove' && method === 'post') return respond(config, autoMoveSave(body as never))
  if (url === '/automove/run' && method === 'post') return respond(config)
  const autoDel = /^\/automove\/(.+)$/.exec(url)
  if (autoDel && method === 'delete') { autoMoveRemove(autoDel[1]); return respond(config) }

  // —— 检查更新 ——
  if (url === '/update/check' && method === 'get') {
    return respond(config, {
      currentVersion: APP_VERSION, latestVersion: APP_VERSION, hasUpdate: false, changelog: '',
      publishedAt: '', releaseUrl: '', fpkUrl: '', fpkSize: 0, arch: 'demo', downloadReady: false,
    })
  }
  if (url === '/update/status' && method === 'get') {
    return respond(config, { updating: false, failed: false, progress: 0, message: '', latestVersion: APP_VERSION, fpkFilename: '' })
  }
  if (url === '/update/install' && method === 'post') return fail(config, '演示模式不支持在线更新')

  return fail(config, `演示模式未提供该接口: ${method.toUpperCase()} ${url}`)
}
