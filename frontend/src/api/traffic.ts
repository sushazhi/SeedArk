import { client, request } from './client'
import type { TrafficHistory, TrafficRange } from '@/types/traffic'

// 历史流量接口。区间由后端决定桶宽（见 types/traffic.ts），前端只挑区间。
// 服务不可用时后端返回 503，由 request() 抛出、调用方给内联重试入口。
export const trafficApi = {
  history: (range: TrafficRange) => request<TrafficHistory>(client.get('/traffic/history', { params: { range } })),
}
