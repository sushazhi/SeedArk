import { useEffect, useState } from 'react'
import { getAuthToken } from '@/api/authToken'
import { fetchWsTicket } from '@/api/wsTicket'
import { useAppStore } from '@/stores/appStore'
import { torrentApi } from '@/api/torrent'
import { APP_BASE } from '@/platform/appBase'
import { DEMO_MODE, DemoSocket } from '@/demo'
import type { WsMessage } from '@/types'

export type WsStatus = 'connecting' | 'connected' | 'disconnected'

// REST 兜底拉取间隔：优先跟随「设置 → 轮询间隔」（与后端 WS 推流节奏同源），
// 未知或非法值时退回默认值。下限避免把间隔配成 0 造成请求洪水。
const DEFAULT_FALLBACK_POLL_MS = 5000
const MIN_FALLBACK_POLL_MS = 1000
// 初次连接阶段的连续失败上限，达到后放弃重连，仅用 REST 轮询（内嵌 WebView 等环境 ws 不可用）
const MAX_WS_RETRIES = 5
// 已成功连接过说明环境支持 ws，此时放宽上限：后端开发态热重启（air）会频繁断开 ws，
// 不应因重启期间的重试计数而永久退化到轮询
const MAX_WS_RETRIES_AFTER_SUCCESS = 30

// parseIntervalMs 解析 Go duration 风格的轮询间隔（如 "2s" / "500ms" / "1m"）。
// 解析失败返回 0，由调用方回退到默认值。
function parseIntervalMs(raw: string): number {
  const m = /^(\d+(?:\.\d+)?)(ms|s|m|h)$/.exec((raw ?? '').trim())
  if (!m) return 0
  const n = Number(m[1])
  if (!Number.isFinite(n) || n <= 0) return 0
  switch (m[2]) {
    case 'ms':
      return n
    case 's':
      return n * 1000
    case 'm':
      return n * 60_000
    default:
      return n * 3_600_000
  }
}

// fallbackPollMs 当前应使用的兜底轮询间隔
function fallbackPollMs(): number {
  const parsed = parseIntervalMs(useAppStore.getState().pollInterval)
  return parsed > 0 ? Math.max(MIN_FALLBACK_POLL_MS, parsed) : DEFAULT_FALLBACK_POLL_MS
}

// WebSocket 连接管理（自动重连，指数退避）
// 兜底策略：某些环境（如内嵌 WebView）会阻断 WebSocket，此时退化用 REST API 轮询，保证数据可显示
export function useWebSocket() {
  const [status, setStatus] = useState<WsStatus>('connecting')

  useEffect(() => {
    let ws: WebSocket | null = null
    let retryTimer: number | null = null
    let pollTimer: number | null = null
    let retries = 0
    let closed = false
    let everConnected = false
    // 首次成功连接前用较小上限（快速判定环境是否支持 ws），之后放宽以便后端重启后自动恢复
    const retryLimit = () => (everConnected ? MAX_WS_RETRIES_AFTER_SUCCESS : MAX_WS_RETRIES)

    const store = useAppStore.getState()

    // REST 兜底拉取全量种子
    const fetchFallback = async () => {
      try {
        const list = await torrentApi.list()
        useAppStore.getState().setTorrents(list)
      } catch {
        // 忽略：等待下一次兜底或 ws 恢复
      }
    }

    const stopPolling = () => {
      if (pollTimer) {
        window.clearInterval(pollTimer)
        pollTimer = null
      }
    }
    const startPolling = () => {
      stopPolling()
      void fetchFallback()
      pollTimer = window.setInterval(() => void fetchFallback(), fallbackPollMs())
    }

    // connectSeq 递增序号：connect 改为异步（先换票）后，
    // 换票期间若有新的 connect 发起或组件卸载，旧的这次必须放弃
    let connectSeq = 0

    const connect = async () => {
      const seq = ++connectSeq
      // 取消已排队的重试，避免与本次调用并发创建多个连接
      if (retryTimer) {
        window.clearTimeout(retryTimer)
        retryTimer = null
      }
      if (retries >= retryLimit()) {
        // ws 在此环境不可用，停止重连，仅用 REST 轮询
        setStatus('disconnected')
        store.setWsStatus('disconnected')
        startPolling()
        return
      }
      const proto = location.protocol === 'https:' ? 'wss' : 'ws'
      // 握手无法携带自定义请求头。首选一次性票据（已鉴权的普通请求换取，
      // 用后即焚）；换票失败时回退到 ?token=，保证旧后端仍可用
      let handshake = ''
      const token = getAuthToken()
      if (token && !DEMO_MODE) {
        const ticket = await fetchWsTicket()
        if (closed || seq !== connectSeq) return
        handshake = ticket
          ? `?ticket=${encodeURIComponent(ticket)}`
          : `?token=${encodeURIComponent(token)}`
      }
      let socket: WebSocket
      if (DEMO_MODE) {
        socket = new DemoSocket() as unknown as WebSocket
      } else {
        try {
          // APP_BASE：网关部署时的基础路径（见 platform/appBase），直连部署为空串
          socket = new WebSocket(`${proto}://${location.host}${APP_BASE}/ws${handshake}`)
        } catch {
          startPolling()
          return
        }
      }
      ws = socket
      setStatus('connecting')
      store.setWsStatus('connecting')
      socket.onopen = () => {
        setStatus('connected')
        store.setWsStatus('connected')
        retries = 0
        everConnected = true
        stopPolling() // ws 可用，停止 REST 轮询
        void fetchFallback() // 拉一次最新数据，覆盖连接期间的变更
      }
      socket.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data) as WsMessage
          // data 必须是数组：异常负载缺 data 时直接写入会把 torrents 置为
          // undefined，下游 .length 访问导致整站白屏
          if ((msg.type === 'full' || msg.type === 'update') && Array.isArray(msg.data)) {
            useAppStore.getState().setTorrents(msg.data)
          } else if (msg.type === 'diff') {
            // 增量推送：仅合并变化的种子；断线重连后服务端会补发全量快照，无需在此兜底
            useAppStore.getState().applyTorrentDiff({
              added: Array.isArray(msg.added) ? msg.added : [],
              updated: Array.isArray(msg.updated) ? msg.updated : [],
              removed: Array.isArray(msg.removed) ? msg.removed : [],
            })
          }
        } catch {
          // 忽略无效消息
        }
      }
      socket.onclose = () => {
        if (closed) return
        setStatus('disconnected')
        store.setWsStatus('disconnected')
        startPolling() // ws 断开，REST 轮询兜底
        retries = Math.min(retries + 1, retryLimit())
        retryTimer = window.setTimeout(() => void connect(), Math.min(1000 * retries, 30000))
      }
      // 不在 onerror 中手动 close（避免在连接未打开时抛 "WebSocket closed without opened"），
      // 让浏览器自动触发 onclose 完成清理与重试
      socket.onerror = () => {}
    }

    // 页面隐藏时暂停轮询/重连，避免后台空转；恢复可见后自动续接
    const handleVisibilityChange = () => {
      if (document.hidden) {
        stopPolling()
        return
      }
      if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) return
      if (retries >= retryLimit()) startPolling()
      else void connect()
    }

    // 轮询间隔变化（设置页保存）后重建兜底定时器，无需刷新页面
    const unsubscribeInterval = useAppStore.subscribe((state, prev) => {
      if (state.pollInterval !== prev.pollInterval && pollTimer) startPolling()
    })

    // 挂载时立即用 REST 拉取一次，避免依赖 ws 才出数据
    void fetchFallback()
    void connect()
    document.addEventListener('visibilitychange', handleVisibilityChange)
    return () => {
      closed = true
      unsubscribeInterval()
      if (retryTimer) {
        window.clearTimeout(retryTimer)
        retryTimer = null
      }
      stopPolling()
      document.removeEventListener('visibilitychange', handleVisibilityChange)
      if (ws) {
        // 卸载前摘掉回调，避免卸载后仍触发重连/轮询
        ws.onopen = null
        ws.onmessage = null
        ws.onclose = null
        ws.onerror = null
        try {
          ws.close()
        } catch {
          // 连接从未打开时 close 可能抛异常，忽略
        }
        ws = null
      }
    }
  }, [])

  return status
}
