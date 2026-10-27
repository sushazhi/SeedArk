// 历史流量（GET /api/traffic/history）的响应类型。
// 契约由后端冻结：points 在所选区间内连续（没有流量的桶也在，值为 0/0），
// 所以 x 轴是等距的、可以按下标直接画；down/up 是「桶内字节数」而不是速率。

/** 可选区间，区间决定桶宽：24h→300s、7d/30d→3600s */
export type TrafficRange = '24h' | '7d' | '30d'

export interface TrafficPoint {
  /** 桶起点，unix 秒 */
  t: number
  /** 桶内下载字节数（不是速率） */
  down: number
  /** 桶内上传字节数（不是速率） */
  up: number
}

export interface TrafficTotals {
  down: number
  up: number
}

/** 区间内最大的单个桶（字节） */
export type TrafficPeak = TrafficTotals

export interface TrafficCoverage {
  /** 本机开始记录的时刻：区间早于它的部分没有数据，只能标出来而不是假装是 0 */
  since: number
  /** 已记录的采样数 */
  samples: number
}

export interface TrafficHistory {
  range: TrafficRange
  bucketSeconds: number
  /** 区间起点（= points[0].t） */
  start: number
  /** 区间终点（= start + 桶数 × 桶宽） */
  end: number
  points: TrafficPoint[]
  totals: TrafficTotals
  peak: TrafficPeak
  coverage: TrafficCoverage
}
