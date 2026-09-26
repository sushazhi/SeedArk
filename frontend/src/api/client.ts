import axios from 'axios'
import type { ApiResponse } from '@/types'
import i18n from '@/i18n'
import { APP_BASE } from '@/platform/appBase'
import { getMessage } from '@/utils/messageHolder'
import { translateApiError } from '@/utils/errors'
import { DEMO_MODE, demoAdapter } from '@/demo'
import { getAuthToken, UNAUTHORIZED_EVENT } from './authToken'

export const client = axios.create({
  baseURL: APP_BASE + '/api',
  timeout: 30000,
})

// 演示模式：请求在浏览器内被 mock adapter 应答，无需真实后端
if (DEMO_MODE) client.defaults.adapter = demoAdapter

// 请求拦截：服务端启用 API_TOKEN 时，鉴权令牌只走请求头
client.interceptors.request.use((config) => {
  const token = getAuthToken()
  if (token) config.headers['X-Auth-Token'] = token
  return config
})

// 响应拦截：解包统一响应结构
client.interceptors.response.use(
  (resp) => resp,
  (error) => {
    // 401 交给令牌输入框统一处理：首屏并发请求若各弹一条提示会刷屏
    if (error.response?.status === 401) {
      window.dispatchEvent(new Event(UNAUTHORIZED_EVENT))
      return Promise.reject(error)
    }
    const raw =
      error.response?.data?.message ||
      (error.code === 'ECONNABORTED' ? i18n.t('errors.requestTimeout') : i18n.t('errors.networkError'))
    getMessage()?.error(translateApiError(raw, error.response?.data?.errorCode))
    return Promise.reject(error)
  },
)

// 通用请求助手：解包 ApiResponse
export async function request<T>(promise: Promise<{ data: ApiResponse<T> }>): Promise<T> {
  const resp = await promise
  // 宿主网关在请求未携带有效的宿主登录态时会短路返回 200 + 纯文本（如飞牛的 "invalid token"），
  // axios 不报错、data 也不是对象；此处显式提示，避免只表现为「列表空白 + 一句笼统的请求失败」而无从排查。
  const payload: unknown = resp.data
  if (typeof payload !== 'object' || payload === null) {
    const raw = typeof payload === 'string' ? payload.trim() : ''
    const msg = /invalid\s+token/i.test(raw)
      ? i18n.t('errors.gatewayTokenInvalid')
      : i18n.t('errors.unexpectedResponse')
    getMessage()?.error(msg)
    throw new Error(msg)
  }
  const body = payload as ApiResponse<T>
  if (body.code !== 0) {
    const msg = translateApiError(body.message || i18n.t('errors.requestFailed'), body.errorCode)
    getMessage()?.error(msg)
    throw new Error(msg)
  }
  return body.data
}
