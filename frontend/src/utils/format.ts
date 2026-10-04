import dayjs from 'dayjs'

// 小数位随量级收缩，始终留 3~4 位有效数字：335.18 MB → 335 MB，9.40 GB 照旧两位小数。
// 表格数值列是固定宽度，[1013.47 KB/s] 这种长串在小列里必被截断，而尾数小数位
// 对"扫一眼比大小"没有贡献。
function precisionFor(val: number): number {
  if (val >= 100) return 0
  if (val >= 10) return 1
  return 2
}

// 格式化字节数为可读单位
export function formatBytes(bytes: number): string {
  if (!bytes || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  let val = bytes / Math.pow(1024, i)
  // 档位按未取整的值判定，四舍五入可能把显示值顶到 1024（1023.6 KB →「1024 KB」），
  // 再进一档，保证显示值的单位是"装得下它的最小单位"
  if (i > 0 && i < units.length - 1 && Math.round(val) >= 1024) {
    i += 1
    val = bytes / Math.pow(1024, i)
  }
  return `${i === 0 ? val.toFixed(0) : val.toFixed(precisionFor(val))} ${units[i]}`
}

// 格式化速度
export function formatSpeed(bytes: number): string {
  return `${formatBytes(bytes)}/s`
}

// 格式化剩余时间（d/h/m/s 通用单位）
export function formatEta(sec: number): string {
  if (!sec || !isFinite(sec) || sec < 0) return '\u221e'
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const s = Math.floor(sec % 60)
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m ${s}s`
  return `${s}s`
}

// 格式化分享率（负数表示无限）
export function formatRatio(r: number): string {
  if (!isFinite(r) || r < 0) return '\u221e'
  return r >= 100 ? r.toFixed(0) : r.toFixed(2)
}

// 格式化累计时长（做种时间等，0 或无数据显示 -）
export function formatDuration(sec: number): string {
  if (!sec || sec <= 0 || !isFinite(sec)) return '-'
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const m = Math.floor((sec % 3600) / 60)
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m`
  return `${Math.floor(sec)}s`
}

// 格式化日期时间戳（秒）
export function formatDate(ts: number, fmt = 'YYYY-MM-DD HH:mm'): string {
  if (!ts) return '-'
  return dayjs(ts * 1000).format(fmt)
}

// 格式化百分比 0~1 → xx.x%
export function formatPercent(p: number, decimal = 1): string {
  return `${(p * 100).toFixed(decimal)}%`
}
