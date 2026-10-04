import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAppStore } from '@/stores/appStore'
import { formatSpeed } from '@/utils/format'

const MAX_POINTS = 60
const W = 280
const H = 90

// 速度历史折线图（SVG 无依赖实现）。
// 传了 torrentId 就只画那一颗：详情页此前直接 <SpeedHistory />，于是在单颗种子的
// 「速度历史」里画的是全库总速度——曲线与页面上那两颗数字（该种子的上下行）对不上
export function SpeedHistory({ torrentId }: { torrentId?: number }) {
  const { t } = useTranslation()
  const [points, setPoints] = useState<{ down: number; up: number }[]>([])

  useEffect(() => {
    const push = () => {
      const { torrents } = useAppStore.getState()
      let down = 0
      let up = 0
      if (torrentId == null) {
        torrents.forEach((x) => {
          down += x.rateDownload
          up += x.rateUpload
        })
      } else {
        // 从 store 现取：详情对象可能是打开那一刻的快照，速率得跟着列表刷新走
        const one = torrents.find((x) => x.id === torrentId)
        down = one?.rateDownload ?? 0
        up = one?.rateUpload ?? 0
      }
      setPoints((prev) => [...prev.slice(-(MAX_POINTS - 1)), { down, up }])
    }
    // 换了种子就重新开始：留着上一颗的曲线会被当成这一颗的历史
    setPoints([])
    push()
    const iv = window.setInterval(push, 2000)
    return () => window.clearInterval(iv)
  }, [torrentId])

  if (points.length < 2) {
    return <div className="text-footnote text-gray-400 py-6 text-center">{t('common.loading')}</div>
  }

  const max = Math.max(1, ...points.map((p) => Math.max(p.down, p.up)))
  const toPath = (key: 'down' | 'up') =>
    points
      .map((p, i) => {
        const x = (i / (points.length - 1)) * W
        const y = H - (p[key] / max) * (H - 8) - 4
        return `${i === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`
      })
      .join(' ')

  const last = points[points.length - 1]

  return (
    <div>
      <svg viewBox={`0 0 ${W} ${H}`} className="w-full h-auto" preserveAspectRatio="none">
        {/* 网格线 */}
        {[0.25, 0.5, 0.75].map((r) => (
          <line key={r} x1="0" y1={H * r} x2={W} y2={H * r} stroke="rgba(128,128,128,0.15)" strokeWidth="1" />
        ))}
        <path d={toPath('down')} fill="none" strokeWidth="2" strokeLinejoin="round" className="stroke-green-600 dark:stroke-green-400" />
        <path d={toPath('up')} fill="none" strokeWidth="2" strokeLinejoin="round" className="stroke-blue-600 dark:stroke-blue-400" />
      </svg>
      <div className="flex justify-between text-footnote mt-1">
        <span className="text-green-600 dark:text-green-400">
          ↓ {formatSpeed(last.down)} / {t('columns.download')}
        </span>
        <span className="text-blue-600 dark:text-blue-400">
          ↑ {formatSpeed(last.up)} / {t('columns.upload')}
        </span>
      </div>
    </div>
  )
}
