import { HardDrive } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { KindBadge } from '@/components/TorrentList/ServerCell'
import { formatBytes } from '@/utils/format'
import type { DiskSpaces } from '@/hooks/useDiskSpaces'

const RING_R = 22
const RING_C = 2 * Math.PI * RING_R

// 磁盘余量卡片。单台部署是原有环形图；聚合视图逐台列出（类型徽标 + 服务器名 +
// 可用空间，各查各的盘）。总量未知（qBittorrent Web API 不提供）时不画占比环、
// 也不显示「/ 总量」——否则会出现「/ 0 B」和一个永远空心的环。
export function DiskSpaceCard({ disk, ringSize = 58 }: { disk: DiskSpaces; ringSize?: number }) {
  const { t } = useTranslation()

  if (disk.aggregate) {
    return (
      <div className="shrink-0 glass-subcard rounded-tile px-3 py-2.5">
        <div className="text-caption1 text-gray-400 flex items-center gap-1 mb-1">
          <HardDrive className="w-3 h-3" />
          {t('statusBar.freeSpace')}
        </div>
        <div className="max-h-36 overflow-y-auto space-y-0.5 pr-1">
          {disk.rows.map((r) => (
            <div key={r.key} className="flex items-center gap-2" title={r.name}>
              <KindBadge kind={r.kind} />
              <span className="truncate flex-1 text-caption1 text-gray-600 dark:text-gray-300">{r.name}</span>
              <span className="tm-mono text-caption1 text-gray-700 dark:text-gray-200 shrink-0">
                {r.ok ? formatBytes(r.freeSpace) : '--'}
                {r.ok && r.totalSize > 0 && (
                  <span className="text-gray-400"> / {formatBytes(r.totalSize)}</span>
                )}
              </span>
            </div>
          ))}
        </div>
      </div>
    )
  }

  const fs = disk.single
  const total = fs && fs.totalSize > 0 ? fs.totalSize : 0
  // 可用空间占比（totalSize 可能为 0，需防除零，否则 SVG 属性为 NaN）
  const ratio = fs && total > 0 ? Math.min(1, Math.max(0, fs.freeSpace / total)) : 0

  return (
    <div className="shrink-0 glass-subcard rounded-tile px-3 py-2.5 flex items-center gap-3">
      {total > 0 ? (
        <svg width={ringSize} height={ringSize} viewBox="0 0 58 58" className="-rotate-90 shrink-0">
          <circle cx="29" cy="29" r={RING_R} stroke="rgba(120,130,160,0.16)" strokeWidth="6.5" fill="none" />
          <circle
            cx="29" cy="29" r={RING_R}
            strokeWidth="6.5" fill="none" strokeLinecap="round"
            strokeDasharray={`${RING_C * ratio} ${RING_C}`}
            style={{ stroke: 'var(--color-primary)', transition: 'stroke-dasharray 0.6s var(--ease-standard)' }}
          />
        </svg>
      ) : (
        // 总量未知：占比无从谈起，用一个中性圆盘代替环，避免「永远空心」
        <div
          className="rounded-full grid place-items-center shrink-0"
          style={{ width: ringSize, height: ringSize, background: 'rgba(120,130,160,0.12)' }}
        >
          <HardDrive className="w-6 h-6 text-gray-400" />
        </div>
      )}
      <div className="min-w-0">
        <div className="text-caption1 text-gray-400 flex items-center gap-1">
          <HardDrive className="w-3 h-3" />
          {t('statusBar.freeSpace')}
        </div>
        <div className="tm-mono truncate whitespace-nowrap">
          <span className="text-subhead font-semibold text-gray-700 dark:text-gray-200">
            {fs ? formatBytes(fs.freeSpace) : '--'}
          </span>
          {total > 0 && <span className="text-caption1 text-gray-400"> / {formatBytes(total)}</span>}
        </div>
      </div>
    </div>
  )
}
