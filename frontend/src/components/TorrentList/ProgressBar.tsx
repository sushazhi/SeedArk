import { cn } from '@/lib/utils'
import { formatPercent } from '@/utils/format'

interface Props {
  /** 进度 0~1 */
  value: number
  error?: boolean
  className?: string
}

/**
 * 内嵌百分比的进度条：填充区白字、未填充区灰字。
 *
 * 填充走 transform: scaleX()（GPU 合成，不触发布局），而不是 width —— 列表里
 * 每张卡片都在 WebSocket 推流下持续变化，width 过渡每帧都会引发 layout + paint。
 * 白色文字层用外层 scaleX(pct) + 内层 scaleX(1/pct) 抵消，保证数字始终完整居中。
 */
export function ProgressBar({ value, error, className }: Props) {
  const clamped = Math.min(1, Math.max(0, value))
  // pct 为 0 时无法求倒数，且填充层本就不可见，直接跳过文字层的缩放抵消
  const hasFill = clamped > 0
  const label = formatPercent(clamped, 0)
  return (
    <div
      className={cn('tm-progress-track tm-progress-track--labeled relative', className)}
      role="progressbar"
      aria-valuenow={Math.round(clamped * 100)}
      aria-valuemin={0}
      aria-valuemax={100}
    >
      <div
        className={cn('tm-progress-fill', error && 'tm-progress-fill--error')}
        style={{ transform: `scaleX(${clamped})` }}
      />
      <span className="absolute inset-0 flex items-center justify-center tm-mono text-caption2 font-medium text-gray-500 dark:text-gray-300">
        {label}
      </span>
      {hasFill && (
        <span
          className="absolute inset-y-0 left-0 overflow-hidden origin-left"
          style={{ width: '100%', transform: `scaleX(${clamped})` }}
          aria-hidden
        >
          <span
            className="absolute inset-y-0 left-0 flex items-center justify-center tm-mono text-caption2 font-medium text-white origin-left"
            // 与父层 scaleX(pct) 相乘 ≈ 1，把文字还原成正常宽度
            style={{ width: '100%', transform: `scaleX(${1 / clamped})` }}
          >
            {label}
          </span>
        </span>
      )}
    </div>
  )
}
