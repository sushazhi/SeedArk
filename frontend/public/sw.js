/* Service Worker
 *
 * 策略（发布即时生效优先于离线可用）：
 *   - 页面导航：网络优先，失败回退缓存 —— 保证刷新总能拿到最新 HTML
 *   - 哈希资源（/assets/*，文件名含构建哈希）：网络优先，失败回退缓存
 *   - 其他静态文件（图标、清单等稳定 URL）：缓存优先，后台刷新
 *
 * 为什么不给哈希资源用缓存优先（旧实现）：
 *   Vite 产物带内容哈希，新版一定引用新文件名，缓存里的旧条目永远不可能再被
 *   正确命中——它只会成为"陈旧毒药"。一旦某次请求把错误内容（如服务端 SPA
 *   回退返回的 200 HTML）写进该 key，此后每次刷新都会把 HTML 当作 JS 返回，
 *   页面永久白屏，且用户无法通过刷新自愈。网络优先可以彻底避免这一状态。
 */

// CACHE_VERSION 由构建期注入（见 vite.config.ts 的 sw-version 插件）：
// 每个版本的缓存名都不同，activate 才会清掉上一版的条目。
// 若占位符未被替换（说明 sw.js 没走构建流程），缓存名会退化成一个固定串——
// 这正是本次要修的问题，因此构建期会直接报错终止，而不是静默发布。
const CACHE_VERSION = '__SW_CACHE_VERSION__'
const CACHE = 'tm-cache-' + CACHE_VERSION

// 一切路径相对 SW 自身解析：部署到宿主网关子路径（/app/transmission/）后，
// 写死 '/' 前缀会缓存失败（install 直接 reject）并把 API 响应当静态资源缓存
const BASE_URL = new URL('./', self.location)
const BASE = BASE_URL.href
// 用于与 url.pathname 比较的路径前缀（以 '/' 结尾，如 /app/transmission/）。
// 注意不能用 BASE 本身去比 pathname：BASE 是绝对 URL，pathname 是路径，
// startsWith 恒为 false——旧实现因此从未真正排除过 API/WS 请求。
const BASE_PATH = BASE_URL.pathname
const PRECACHE = [BASE, BASE + 'manifest.webmanifest', BASE + 'icons/icon-512.png']

// 请求与响应的内容类型必须相容才允许写缓存。
// 这是白屏事故的第二道防线：即便服务端因配置问题把 HTML 返给 .js 请求，
// 也不会被污染进缓存（响应仍然透传给浏览器，报错可见而非静默固化）。
function contentTypeMatches(request, response) {
  const ct = (response.headers.get('content-type') || '').toLowerCase()
  if (!ct) return true // 无类型信息时不过度拦截，交给 status/type 判断

  // 导航请求要 HTML
  if (request.mode === 'navigate') return ct.includes('text/html')
  // 明确接受 HTML 的请求（如 fetch 后 innerHTML）同样按 HTML 校验
  const accept = (request.headers.get('accept') || '').toLowerCase()
  if (accept.includes('text/html') && !accept.includes('application/json')) {
    return ct.includes('text/html')
  }

  const url = new URL(request.url)
  const ext = url.pathname.slice(url.pathname.lastIndexOf('.') + 1).toLowerCase()
  switch (ext) {
    case 'js':
    case 'mjs':
      return ct.includes('javascript') || ct.includes('ecmascript')
    case 'css':
      return ct.includes('text/css')
    case 'json':
    case 'webmanifest':
      return ct.includes('json')
    case 'png':
    case 'jpg':
    case 'jpeg':
    case 'gif':
    case 'webp':
    case 'svg':
    case 'ico':
      return ct.startsWith('image/')
    case 'woff':
    case 'woff2':
    case 'ttf':
    case 'otf':
      return ct.includes('font') || ct.includes('octet-stream')
    default:
      return true
  }
}

// 只有「成功、同源、且内容类型与请求相符」的响应才可写入缓存
function isCacheable(request, response) {
  if (!response || response.status !== 200) return false
  if (response.type !== 'basic' && response.type !== 'cors') return false
  return contentTypeMatches(request, response)
}

function putInCache(request, response) {
  const clone = response.clone()
  caches.open(CACHE).then((cache) => cache.put(request, clone)).catch(() => {})
}

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches
      .open(CACHE)
      .then((cache) => cache.addAll(PRECACHE))
      .then(() => self.skipWaiting()),
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      // 只保留当前版本的缓存：带版本号后，旧版本条目在此被彻底清除，
      // 不再像旧实现那样所有版本共用一个 cache 名、旧资源永久残留
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  )
})

// 网络优先：成功则刷新缓存，失败（离线/超时）回退缓存
function networkFirst(request, fallback) {
  return fetch(request)
    .then((resp) => {
      if (isCacheable(request, resp)) putInCache(request, resp)
      return resp
    })
    .catch(() =>
      caches.match(request).then((cached) => {
        if (cached) return cached
        return typeof fallback === 'function' ? fallback() : Promise.reject(new Error('offline'))
      }),
    )
}

self.addEventListener('fetch', (event) => {
  const { request } = event
  if (request.method !== 'GET') return
  const url = new URL(request.url)
  // 仅处理同源请求
  if (url.origin !== self.location.origin) return
  // API/WS 不缓存：下载状态一旦缓存下来，离线时会拿回一屏过期数据。
  // 必须与 url.pathname 比较（BASE_PATH 以 '/' 结尾），用绝对 URL 比较恒为 false
  if (url.pathname.startsWith(BASE_PATH + 'api') || url.pathname.startsWith(BASE_PATH + 'ws')) return

  // 页面导航：网络优先，离线回退到缓存页面（再兜底到预缓存的应用外壳）
  if (request.mode === 'navigate') {
    event.respondWith(
      networkFirst(request, () => caches.match(BASE).then((r) => r || Response.error())),
    )
    return
  }

  // 构建哈希资源：网络优先。
  // 这些文件名唯一，缓存命中只意味着「同一个已发布文件」，网络优先不会牺牲
  // 多少离线能力（仍保留回退），却能从根上杜绝旧哈希被长期复用。
  if (url.pathname.startsWith(BASE_PATH + 'assets/')) {
    event.respondWith(networkFirst(request))
    return
  }

  // 其他稳定 URL 的静态文件（图标、清单等）：缓存优先 + 后台刷新
  event.respondWith(
    caches.match(request).then((cached) => {
      const network = fetch(request)
        .then((resp) => {
          if (isCacheable(request, resp)) putInCache(request, resp)
          return resp
        })
        .catch(() => cached)
      return cached || network
    }),
  )
})

// 供 PwaUpdatePrompt 触发立即接管
self.addEventListener('message', (event) => {
  if (event.data === 'SKIP_WAITING') self.skipWaiting()
})
