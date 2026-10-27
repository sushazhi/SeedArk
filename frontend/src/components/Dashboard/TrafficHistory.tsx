import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertTriangle, Loader2, RefreshCw } from 'lucide-react'
import { trafficApi } from '@/api/traffic'
import type { TrafficHistory, TrafficRange } from '@/types/traffic'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { formatBytes, formatDate } from '@/utils/format'

// 历史流量折线图（SVG 无依赖实现，坐标系与 StatusBar/SpeedHistory 同一套）。
// viewBox + w-full h-auto：手机上按容器宽度整体缩放，不需要额外断点。
const W = 280
const H = 90
// 上下各留 4：峰值贴到边框上会分不清「到顶了」还是「被裁了」
const PAD = 4

const RANGES: TrafficRange[] = ['24h', '7d', '30d']

// 24 小时看时刻、7/30 天看日期：桶宽本来就不同，标签画成一样反而读不出来
const AXIS_FMT: Record<TrafficRange, string> = { '24h': 'HH:mm', '7d': 'M-D', '30d': 'M-D' }
// 峰值桶与覆盖起点要精确到时刻：只写「8-3」看不出是白天还是半夜
const EXACT_FMT: Record<TrafficRange, string> = { '24h': 'HH:mm', '7d': 'MM-DD HH:mm', '30d': 'MM-DD HH:mm' }

interface LoadState {
  loading: boolean
  error: boolean
  data: TrafficHistory | null
}

// 进入区间即拉取，失败给内联重试而不是抛错炸掉统计弹窗（与保种台各页签一致）
function useHistory(range: TrafficRange) {
  const [state, setState] = useState<LoadState>({ loading: true, error: false, data: null })
  const [nonce, setNonce] = useState(0)
  useEffect(() => {
    let alive = true
    setState((s) => ({ ...s, loading: true, error: false }))
    trafficApi
      .history(range)
      .then((d) => {
        if (alive) setState({ loading: false, error: false, data: d })
      })
      .catch(() => {
        if (alive) setState({ loading: false, error: true, data: null })
      })
    return () => {
      alive = false
    }
  }, [range, nonce])
  return { ...state, reload: () => setNonce((n) => n + 1) }
}

export function TrafficHistory() {
  const { t } = useTranslation()
  const [range, setRange] = useState<TrafficRange>('24h')
  const { loading, error, data, reload } = useHistory(range)

  return (
    <div className="rounded-xl glass-section p-3">
      {/* 区间切换与标题同排：它是这张图唯一的前置条件，放到别处等于让用户去找 */}
      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
        <div className="text-body font-medium">{t('traffic.title')}</div>
        <div className="flex rounded-lg bg-gray-100 p-0.5 dark:bg-gray-800/80">
          {RANGES.map((r) => (
            <button
              key={r}
              onClick={() => setRange(r)}
              aria-pressed={range === r}
              className={cn(
                // h-9：手机上 36px 的命中高度够用，且三个一起也不会撑破窄屏
                'tm-press h-9 rounded-md px-2.5 text-footnote font-medium transition-colors',
                range === r
                  ? 'bg-white text-primary shadow-sm dark:bg-gray-700'
                  : 'text-gray-500 hover:text-gray-700 dark:text-gray-400',
              )}
            >
              {t(`traffic.range.${r}`)}
            </button>
          ))}
        </div>
      </div>

      {loading ? (
        <div className="flex items-center justify-center gap-2 py-10 text-footnote text-gray-500 dark:text-gray-400">
          <Loader2 className="h-4 w-4 animate-spin" />
          {t('traffic.loading')}
        </div>
      ) : error || !data ? (
        <div className="flex flex-col items-center gap-3 py-8 text-center">
          <AlertTriangle className="h-6 w-6" style={{ color: 'var(--color-status-error)' }} />
          <p className="text-footnote text-gray-500 dark:text-gray-400">{t('traffic.loadFailed')}</p>
          <Button variant="outline" size="sm" onClick={reload} className="gap-1.5">
            <RefreshCw className="h-3.5 w-3.5" />
            {t('traffic.retry')}
          </Button>
        </div>
      ) : (
        <Chart data={data} range={range} />
      )}
    </div>
  )
}

function Chart({ data, range }: { data: TrafficHistory; range: TrafficRange }) {
  const { t } = useTranslation()
  const points = data.points
  const { totals, peak, coverage } = data

  // 一个点画不出线；全 0 说明这段时间还没采到东西，给空态而不是一条贴底的直线
  const hasData = points.length >= 2 && (totals.down > 0 || totals.up > 0)

  const peakPoint = useMemo(() => {
    let best = points[0]
    for (const p of points) {
      if (Math.max(p.down, p.up) > Math.max(best.down, best.up)) best = p
    }
    return best
  }, [points])

  // x 轴刻度取 4 个：手机上再多就叠字，且桶是等距的，按下标取点即等距
  const ticks = useMemo(() => {
    const n = 4
    return Array.from({ length: n }, (_, i) => {
      const idx = Math.round((i / (n - 1)) * (points.length - 1))
      return { idx, t: points[idx]?.t ?? data.start }
    })
  }, [points, data.start])

  // 覆盖不全时把「从什么时候才有记录」摆在图下，而不是让人把前面的空桶当成没流量
  const coverageNote = coverage.since > data.start && (
    <p className="mt-1 text-caption1 text-gray-400 dark:text-gray-500">
      {t('traffic.coverage', { time: formatDate(coverage.since, EXACT_FMT[range]) })}
    </p>
  )

  if (!hasData) {
    return (
      <div>
        <p className="py-8 text-center text-footnote text-gray-400 dark:text-gray-500">{t('traffic.empty')}</p>
        {/* 空态更要说明起点：否则「没采到数据」与「这段时间真没流量」分不开 */}
        {coverageNote}
      </div>
    )
  }

  const yMax = Math.max(1, peak.down, peak.up)
  const x = (i: number) => (i / (points.length - 1)) * W
  const y = (v: number) => H - PAD - (v / yMax) * (H - PAD * 2)
  const toPath = (key: 'down' | 'up') =>
    points.map((p, i) => `${i === 0 ? 'M' : 'L'}${x(i).toFixed(1)},${y(p[key]).toFixed(1)}`).join(' ')

  return (
    <div>
      <div className="flex gap-1.5">
        {/* y 轴刻度用 HTML 而不是 SVG <text>：SVG 会随容器整体缩放，
            桌面端弹窗里文字会被放大到 20px 以上 */}
        <div className="flex shrink-0 flex-col justify-between text-caption2 leading-none tabular-nums text-gray-400 dark:text-gray-500">
          <span className="-translate-y-1/2">{formatBytes(yMax)}</span>
          <span>{formatBytes(yMax / 2)}</span>
          <span className="translate-y-1/2">{formatBytes(0)}</span>
        </div>
        <div className="min-w-0 flex-1">
          <svg viewBox={`0 0 ${W} ${H}`} className="h-auto w-full" role="img" aria-label={t('traffic.title')}>
            {/* 网格线对齐上下两条刻度：顶=峰值、中=峰值一半，底是基线 */}
            {[PAD, H / 2, H - PAD].map((gy) => (
              <line key={gy} x1="0" y1={gy} x2={W} y2={gy} stroke="rgba(128,128,128,0.15)" strokeWidth="1" />
            ))}
            <path d={toPath('down')} fill="none" strokeWidth="2" strokeLinejoin="round" className="stroke-green-600 dark:stroke-green-400" />
            <path d={toPath('up')} fill="none" strokeWidth="2" strokeLinejoin="round" className="stroke-blue-600 dark:stroke-blue-400" />
          </svg>
          {/* x 轴刻度与图同宽同一列：不靠左内边距猜 y 轴标签的宽度 */}
          <div className="mt-0.5 flex justify-between text-caption2 tabular-nums text-gray-400 dark:text-gray-500">
            {ticks.map((tk) => (
              <span key={tk.idx}>{formatDate(tk.t, AXIS_FMT[range])}</span>
            ))}
          </div>
        </div>
      </div>

      <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-footnote">
        <span className="text-green-600 dark:text-green-400">
          ↓ {t('traffic.down')} {formatBytes(totals.down)}
        </span>
        <span className="text-blue-600 dark:text-blue-400">
          ↑ {t('traffic.up')} {formatBytes(totals.up)}
        </span>
        <span className="text-gray-500 dark:text-gray-400">
          {t('traffic.peak')} {formatBytes(Math.max(peak.down, peak.up))} · {formatDate(peakPoint.t, EXACT_FMT[range])}
        </span>
      </div>
      {coverageNote}
    </div>
  )
}
