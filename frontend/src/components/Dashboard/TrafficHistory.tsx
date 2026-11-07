import { useEffect, useMemo, useRef, useState } from 'react'
import type { KeyboardEvent as ReactKeyboardEvent, PointerEvent as ReactPointerEvent } from 'react'
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
  // 悬停 / 点按查看单个时间点的数值。null = 没有在看哪一个桶
  const [hover, setHover] = useState<number | null>(null)

  // 一个点画不出线；全 0 说明这段时间还没采到东西，给空态而不是一条贴底的直线
  const hasData = points.length >= 2 && (totals.down > 0 || totals.up > 0)

  const peakPoint = useMemo(() => {
    let best = points[0]
    for (const p of points) {
      if (Math.max(p.down, p.up) > Math.max(best.down, best.up)) best = p
    }
    return best
  }, [points])

  // 描边与圆点的尺寸基准。viewBox 只有 280 宽，容器是 w-full：桌面弹窗里整张图放大 3 倍多，
  // 留在坐标系里的 strokeWidth=2 会被画成 7px 粗线——所以描边一律用 non-scaling-stroke
  // 钉在 CSS px 上；圆点半径没法这么钉，按实测宽度换算成固定 4 CSS px。
  const boxRef = useRef<HTMLDivElement>(null)
  const [unitPerPx, setUnitPerPx] = useState(1)
  useEffect(() => {
    const el = boxRef.current
    if (!el) return
    const sync = () => {
      const w = el.clientWidth
      if (w > 0) setUnitPerPx(W / w)
    }
    sync()
    const ro = new ResizeObserver(sync)
    ro.observe(el)
    return () => ro.disconnect()
  }, [hasData])

  // x 轴刻度取 4 个：手机上再多就叠字，且桶是等距的，按下标取点即等距
  const ticks = useMemo(() => {
    const n = 4
    return Array.from({ length: n }, (_, i) => {
      const idx = Math.round((i / (n - 1)) * (points.length - 1))
      return { idx, t: points[idx]?.t ?? data.start }
    })
  }, [points, data.start])

  // 指针纵向位置 → 最近的桶下标。桶在区间内等距，所以按容器宽度按比例换算即可，
  // 不做折线命中测试：时间序列图要回答的是「这个时刻多少流量」，不是「离哪条线近」。
  const indexAt = (e: ReactPointerEvent<SVGSVGElement>) => {
    const rect = e.currentTarget.getBoundingClientRect()
    if (rect.width <= 0) return null
    const ratio = (e.clientX - rect.left) / rect.width
    return Math.min(points.length - 1, Math.max(0, Math.round(ratio * (points.length - 1))))
  }

  // 键盘也能逐点看：图是可聚焦的，←/→ 移动，Home/End 跳到两端，Esc 收起
  const onKeyDown = (e: ReactKeyboardEvent<SVGSVGElement>) => {
    if (e.key === 'Escape') {
      setHover(null)
      return
    }
    if (e.key === 'Home' || e.key === 'End') {
      e.preventDefault()
      setHover(e.key === 'Home' ? 0 : points.length - 1)
      return
    }
    if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return
    e.preventDefault()
    const step = e.key === 'ArrowRight' ? 1 : -1
    setHover((cur) => {
      const from = cur ?? (step > 0 ? -1 : points.length)
      return Math.min(points.length - 1, Math.max(0, from + step))
    })
  }

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
  const x = (i: number) => (points.length > 1 ? (i / (points.length - 1)) * W : 0)
  const y = (v: number) => H - PAD - (v / yMax) * (H - PAD * 2)
  // 两条折线只在数据变化时重算：悬停是 60Hz 级的 setState，每次都拼 288 个坐标点没必要
  const paths = useMemo(() => {
    const build = (key: 'down' | 'up') =>
      points.map((p, i) => `${i === 0 ? 'M' : 'L'}${x(i).toFixed(1)},${y(p[key]).toFixed(1)}`).join(' ')
    return { down: build('down'), up: build('up') }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- x/y 只由 points 与 yMax 决定
  }, [points, yMax])

  // 光标位置与提示框。viewBox 等比缩放到容器宽度，所以百分比在两边都成立。
  // 切换区间后点数组会变短，下标按当前长度夹一下，免得读到空位
  const cursorIdx = hover !== null && points.length > 0 ? Math.min(hover, points.length - 1) : null
  const cursor = cursorIdx !== null ? points[cursorIdx] : null
  const cursorX = cursorIdx !== null ? (x(cursorIdx) / W) * 100 : 0
  const cursorY = cursor ? (y(Math.max(cursor.down, cursor.up)) / H) * 100 : 0
  // 圆点半径：4 CSS px 换算回坐标系单位（见上面的 unitPerPx）
  const dotR = 4 * unitPerPx
  // 靠边的桶把提示框往回收，避免溢出图外；靠顶的点把框放到下方
  const tooltipX = cursorX < 14 ? '0%' : cursorX > 86 ? '-100%' : '-50%'
  const tooltipY = cursorY > 30 ? 'calc(-100% - 8px)' : '8px'

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
          {/* 图外面包一层 relative：提示框是 HTML（SVG 文本会随容器整体缩放，
              桌面端会被放大到 20px 以上），要贴着光标定位在图内 */}
          <div className="relative" ref={boxRef}>
            <svg
              viewBox={`0 0 ${W} ${H}`}
              // touch-pan-y：竖向滑动仍然滚动弹窗，只有横向拖动才落到图上取点
              className="block h-auto w-full touch-pan-y focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-primary"
              role="img"
              aria-label={t('traffic.title')}
              tabIndex={0}
              onPointerMove={(e) => {
                const i = indexAt(e)
                if (i !== null) setHover(i)
              }}
              onPointerDown={(e) => {
                const i = indexAt(e)
                if (i !== null) setHover(i)
              }}
              // 手指离开就收起；鼠标靠 pointerleave，不必等抬起
              onPointerUp={(e) => {
                if (e.pointerType !== 'mouse') setHover(null)
              }}
              onPointerCancel={() => setHover(null)}
              onPointerLeave={() => setHover(null)}
              onKeyDown={onKeyDown}
            >
              {/* 网格线对齐上下两条刻度：顶=峰值、中=峰值一半，底是基线 */}
              {[PAD, H / 2, H - PAD].map((gy) => (
                <line key={gy} x1="0" y1={gy} x2={W} y2={gy} stroke="rgba(128,128,128,0.15)" strokeWidth="1" vectorEffect="non-scaling-stroke" />
              ))}
              <path d={paths.down} fill="none" strokeWidth="1.5" strokeLinejoin="round" vectorEffect="non-scaling-stroke" className="stroke-green-600 dark:stroke-green-400" />
              <path d={paths.up} fill="none" strokeWidth="1.5" strokeLinejoin="round" vectorEffect="non-scaling-stroke" className="stroke-blue-600 dark:stroke-blue-400" />
              {/* 取中的那一桶：竖虚线 + 两条线上的点，先看清「看的是哪一列」 */}
              {cursor && cursorIdx !== null && (
                <g>
                  <line
                    x1={x(cursorIdx)}
                    y1={PAD}
                    x2={x(cursorIdx)}
                    y2={H - PAD}
                    stroke="rgba(128,128,128,0.55)"
                    strokeWidth="1"
                    strokeDasharray="2 2"
                    vectorEffect="non-scaling-stroke"
                  />
                  <circle cx={x(cursorIdx)} cy={y(cursor.down)} r={dotR} strokeWidth="1" vectorEffect="non-scaling-stroke" className="fill-green-600 stroke-white dark:fill-green-400 dark:stroke-gray-900" />
                  <circle cx={x(cursorIdx)} cy={y(cursor.up)} r={dotR} strokeWidth="1" vectorEffect="non-scaling-stroke" className="fill-blue-600 stroke-white dark:fill-blue-400 dark:stroke-gray-900" />
                </g>
              )}
            </svg>
            {cursor && (
              <div
                role="status"
                aria-live="polite"
                className="pointer-events-none absolute z-10 rounded-lg border border-gray-200/70 bg-white/95 px-2 py-1 text-caption1 shadow-lg backdrop-blur dark:border-white/10 dark:bg-gray-900/95"
                style={{ left: `${cursorX}%`, top: `${cursorY}%`, transform: `translate(${tooltipX}, ${tooltipY})` }}
              >
                <div className="whitespace-nowrap tabular-nums text-gray-500 dark:text-gray-400">
                  {formatDate(cursor.t, EXACT_FMT[range])}
                </div>
                <div className="mt-0.5 flex gap-2 whitespace-nowrap tabular-nums">
                  <span className="text-green-600 dark:text-green-400">
                    ↓ {formatBytes(cursor.down)}
                  </span>
                  <span className="text-blue-600 dark:text-blue-400">
                    ↑ {formatBytes(cursor.up)}
                  </span>
                </div>
              </div>
            )}
          </div>
          {/* 屏读用户看不到悬停，用方向键也能逐点走 */}
          <span className="sr-only">{t('traffic.keyboardHint')}</span>
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
