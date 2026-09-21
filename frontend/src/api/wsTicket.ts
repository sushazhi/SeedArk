import axios from 'axios'
import { APP_BASE } from '@/platform/appBase'
import { getAuthToken } from './authToken'

// 独立实例：不复用 client 的拦截器——换票失败要静默回退到旧路径，
// 不能让每次重连都弹一条错误提示
const bare = axios.create({ baseURL: APP_BASE + '/api', timeout: 5000 })

// fetchWsTicket 用已鉴权的普通请求换一张一次性 WebSocket 握手票据。
// 握手查询串会进入浏览器历史、网关/代理访问日志与 Referer，
// 长期令牌不应直接出现在 URL 里；票据用后即焚、60 秒过期。
// 任何失败都返回空串，由调用方回退到 ?token=（旧后端 / 网关拦截等场景）。
export async function fetchWsTicket(): Promise<string> {
  try {
    const token = getAuthToken()
    const resp = await bare.post<{ data?: { ticket?: string } }>(
      '/ws-ticket',
      null,
      token ? { headers: { 'X-Auth-Token': token } } : undefined,
    )
    return resp.data?.data?.ticket ?? ''
  } catch {
    return ''
  }
}
