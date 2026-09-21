// 服务端 API_TOKEN 鉴权所需的访问令牌。
// 后端只认 X-Auth-Token / Authorization 请求头；WebSocket 握手无法携带自定义头，
// 改用一次性票据（见 api/wsTicket.ts），令牌本身不再出现在任何 URL 里。
// 令牌由运维写在服务端配置里，浏览器侧只能由用户输入一次并留在本地。
//
// ⚠️ 红线：浏览器侧发送令牌只能走 X-Auth-Token 这个自定义头，绝不能改用标准的 Authorization 头。
// 飞牛 fnOS 1.2.0604 起，统一网关对 /app/** 一律要求宿主登录态票据，票据缺失或无效时直接短路
// 返回纯文本 "invalid token"（HTTP 200），请求根本到不了本服务：面板能打开、但所有接口都拿不到
// 数据（列表空白 + 一句笼统的「请求失败」）。网关会从请求头里取票据，而 `Authorization: Bearer`
// 正是它的票据头之一，所以浏览器侧绝不能占用该头（本服务只认自定义头）。
// 实机复核（fnOS 1.2.0604 网关部署）：不带任何鉴权头访问 /app/<app>/ 同样得到 200 + "invalid token"，
// 可见触发条件是「没有宿主票据」而非某个特定头——结论只到「别用 Authorization」这一层，
// 不要把注释写成对网关校验顺序的断言。
// 同一坑位已在 fnos-logmanager 踩过一次（见其 app/ui/src/services/api.ts 的 SESSION_TOKEN_HEADER 注释）。
const STORAGE_KEY = 'tm.authToken'

// 任一请求返回 401 时广播，由令牌输入对话框统一接管，避免每个并发请求各弹一条错误
export const UNAUTHORIZED_EVENT = 'tm:unauthorized'

export function getAuthToken(): string {
  try {
    return window.localStorage.getItem(STORAGE_KEY) ?? ''
  } catch {
    // 无痕模式等场景下 localStorage 不可用
    return ''
  }
}

export function setAuthToken(token: string): void {
  try {
    if (token) {
      window.localStorage.setItem(STORAGE_KEY, token)
    } else {
      window.localStorage.removeItem(STORAGE_KEY)
    }
  } catch {
    // 存不下就只影响本次会话，不阻断界面
  }
}
