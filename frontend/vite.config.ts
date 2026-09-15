import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

// vite.config.ts 以 ESM 求值，没有 __dirname；用 import.meta.url 还原
const __dirname = path.dirname(fileURLToPath(import.meta.url))

// flag-icons 的 1x1 变体（.fis）本项目用不到，但它的 CSS 为每个国家都引了一份，
// 会让构建产物凭空多出 271 个无用 SVG（约 1.9MB），文件数也会逼近一些批量删除保护阈值。
// 在 CSS 处理前剥掉这些规则，只保留 4x3（界面里旗帜按 20×15 展示）。
const flagIcons4x3Only = {
  name: 'flag-icons-4x3-only',
  enforce: 'pre' as const,
  transform(code: string, id: string) {
    if (!id.includes('flag-icons') || !id.endsWith('.css')) return null
    return code.replace(/\.fi-[a-z-]+\.fis\{background-image:url\([^)]*\)\}/g, '')
  },
}

// sw.js 的缓存版本注入。
//
// 背景：sw.js 原先写死 `tm-cache-v2`，所有版本共用一个缓存名，activate 里的
// 「清理非当前缓存」因此永远清不掉任何东西，旧哈希资源永久残留并持续被缓存优先
// 命中——移动端「刷新即白屏」的直接成因之一。
// 这里用构建产物的文件名集合派生出版本串，写进 sw.js 的 __SW_CACHE_VERSION__。
// 用产物指纹而非时间戳/随机数：内容没变时指纹不变，缓存得以复用；
// 内容一变版本即变，activate 立刻清空上一版全部条目。
//
// 必须走 closeBundle + 直接读文件：public/ 下的文件由 Vite 原样拷贝，
// 不经过 generateBundle 的 asset 流程，插件拿不到它的内容。
const swVersion = {
  name: 'sw-cache-version',
  enforce: 'post' as const,
  closeBundle() {
    const outDir = path.resolve(__dirname, 'dist')
    const swPath = path.join(outDir, 'sw.js')
    if (!fs.existsSync(swPath)) return

    // 指纹来源：assets 目录下所有产物文件名（带内容哈希）+ index.html 内容
    const assetsDir = path.join(outDir, 'assets')
    const names = fs.existsSync(assetsDir) ? fs.readdirSync(assetsDir).sort() : []
    let hash = 0
    const feed = (s: string) => {
      for (let i = 0; i < s.length; i++) hash = (Math.imul(31, hash) + s.charCodeAt(i)) | 0
    }
    names.forEach(feed)
    const indexPath = path.join(outDir, 'index.html')
    if (fs.existsSync(indexPath)) feed(fs.readFileSync(indexPath, 'utf8'))

    const version = (hash >>> 0).toString(36)
    const src = fs.readFileSync(swPath, 'utf8')
    if (!src.includes('__SW_CACHE_VERSION__')) {
      // 占位符缺失说明 sw.js 与构建脚本脱节，直接失败比发出一个所有版本
      // 共用缓存名的 SW 要安全得多（那正是白屏事故的根源）
      this.error('sw.js 缺少 __SW_CACHE_VERSION__ 占位符，缓存版本无法注入')
      return
    }
    fs.writeFileSync(swPath, src.replaceAll('__SW_CACHE_VERSION__', version))
    console.log(`  \u2713 sw.js \u7f13\u5b58\u7248\u672c\u5df2\u6ce8\u5165: ${version}`)
  },
}

export default defineConfig({
  // 相对路径构建：资源可被部署在任意子路径（如 fnOS 网关 /app/transmission/）下
  base: './',
  plugins: [flagIcons4x3Only, react(), tailwindcss(), swVersion],
  resolve: {
    alias: {
      '@': '/src',
    },
  },
  server: {
    host: true,
    port: 5173,
    strictPort: true,
    // 开启 HMR（React Fast Refresh + CSS 热替换）：保存源码后浏览器自动局部更新，无需手动刷新。
    // 特殊环境（如阻断 WebSocket 的内嵌 WebView）可用 `DEV_NO_HMR=1 pnpm dev` 关闭。
    hmr: process.env.DEV_NO_HMR === '1' ? false : { overlay: true },
    // 网络盘 / 虚拟机共享目录 / WSL 挂载目录下原生监听可能收不到事件，
    // 此时用 `DEV_WATCH_POLL=1 pnpm dev` 切换为轮询监听（默认不覆盖 Vite 自身的 watch 配置）
    ...(process.env.DEV_WATCH_POLL === '1'
      ? { watch: { usePolling: true, interval: 300 } }
      : {}),
    proxy: {
      '/api': { target: 'http://localhost:8200', changeOrigin: true },
      '/mcp': { target: 'http://localhost:8200', changeOrigin: true },
      '/ws': { target: 'ws://localhost:8200', ws: true },
    },
  },
  build: {
    outDir: 'dist',
    chunkSizeWarningLimit: 1500,
    // flag-icons 的国家/地区旗帜是 271 个独立 SVG（其中 200 个小于默认 4KB 阈值）：
    // 一旦被内联进 CSS，样式表会膨胀到几百 KB，且未用到的旗帜也被塞进首屏。
    // 这里对 SVG 关闭内联，CSS 保持 ~27KB，浏览器只按需请求当前页实际出现的旗帜。
    assetsInlineLimit: (filePath: string) => (filePath.endsWith('.svg') ? false : undefined),
    rollupOptions: {
      output: {
        // Vite 8（rolldown）仅支持函数形式的 manualChunks
        manualChunks(id: string) {
          if (/node_modules[\\/](react|react-dom|axios|zustand)([\\/]|$)/.test(id)) {
            return 'vendor'
          }
          return undefined
        },
      },
    },
  },
})
