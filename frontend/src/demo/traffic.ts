// 历史流量的演示数据。
//
// 真实后端是「跑起来才开始采样」，演示页没有那个时间维度，所以这里按区间 synth
// 出一套自洽的点列：上下行都有昼夜节律（上行做种平稳、下行是零星的大包），
// 并且对同一个桶始终保持同一个值——每次切换/重绘曲线都跳一次就太假了。
//
// 口径与后端一致：点列在区间内连续、空桶补 0，down/up 是桶内字节数而不是速率。

import type { TrafficHistory, TrafficPoint, TrafficRange } from '@/types/traffic'

const MB = 1024 * 1024

/** 各档区间的桶宽与桶数，与后端 ranges 对齐 */
const SPEC: Record<TrafficRange, { bucket: number; count: number }> = {
  '24h': { bucket: 300, count: 24 * 60 / 5 },
  '7d': { bucket: 3600, count: 7 * 24 },
  '30d': { bucket: 3600, count: 30 * 24 },
}

// 桶下标 → [0,1) 的确定性伪随机：同一个桶永远得到同一个值，
// 切换区间或重绘不会让曲线抖动（真随机会让演示看起来像在刷新数据）
function rnd(seed: number): number {
  let x = seed | 0
  x = Math.imul(x ^ (x >>> 15), x | 1)
  x ^= x + Math.imul(x ^ (x >>> 7), x | 61)
  return ((x ^ (x >>> 14)) >>> 0) / 4294967296
}

// 一天里的做种活跃度：凌晨低、晚间高
function dailyWave(t: number): number {
  const hour = new Date(t * 1000).getHours() + new Date(t * 1000).getMinutes() / 60
  return 0.55 + 0.45 * Math.sin(((hour - 4) / 24) * Math.PI * 2)
}

export function trafficHistory(range?: string): TrafficHistory {
  const name: TrafficRange = range === '7d' || range === '30d' ? range : '24h'
  const { bucket, count } = SPEC[name]

  const now = Math.floor(Date.now() / 1000)
  const end = now - (now % bucket)
  const start = end - (count - 1) * bucket

  const points: TrafficPoint[] = []
  let totalDown = 0
  let totalUp = 0
  let peakDown = 0
  let peakUp = 0

  for (let i = 0; i < count; i++) {
    const t = start + i * bucket
    // 上行：做种常态，5 分钟桶按 3~14MB 打底，小时桶按同一速率放大 12 倍
    const scale = bucket / 300
    const up = Math.round((3 + rnd(t + 7) * 11) * MB * scale * dailyWave(t))
    // 下行：多数桶接近 0，偶尔来一个大包（演示「最近下过什么」的可读性）
    const burst = rnd(t + 31)
    const down = burst > 0.94 ? Math.round((80 + rnd(t + 13) * 900) * MB * scale) : 0

    points.push({ t, down, up })
    totalDown += down
    totalUp += up
    peakDown = Math.max(peakDown, down)
    peakUp = Math.max(peakUp, up)
  }

  return {
    range: name,
    bucketSeconds: bucket,
    start,
    end,
    points,
    totals: { down: totalDown, up: totalUp },
    peak: { down: peakDown, up: peakUp },
    // 演示数据是整段生成的，覆盖从区间起点开始（不显示「本机从…起才开始记录」）
    coverage: { since: start, samples: count },
  }
}
