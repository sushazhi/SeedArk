import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { useAppStore } from '@/stores/appStore'
import { formatBytes, formatRatio } from '@/utils/format'
import { SpeedHistory } from '@/components/StatusBar/SpeedHistory'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

import { statusMeta } from '@/utils/status'
import { isActiveTorrent } from '@/hooks/useFilter'

// 统计仪表盘：全局概览 + 状态分布 + 速度历史 + 标签/站点流量
export function Dashboard({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t } = useTranslation()
  const torrents = useAppStore((s) => s.torrents)

  const stats = useMemo(() => {
    let downloaded = 0
    let uploaded = 0
    let active = 0
    let downloading = 0
    let seeding = 0
    let paused = 0
    let error = 0
    const byStatus: Record<number, number> = {}
    const byLabel: Record<string, { count: number; size: number }> = {}
    const bySite: Record<string, { count: number; size: number }> = {}
    const sites = useAppStore.getState().torrentSites

    for (const t2 of torrents) {
      downloaded += t2.downloadedEver || 0
      uploaded += t2.uploadedEver || 0
      if (isActiveTorrent(t2)) active++
      if (t2.status === 3 || t2.status === 4) downloading++
      if (t2.status === 5 || t2.status === 6) seeding++
      if (t2.status === 0) paused++
      if (t2.error > 0) error++
      byStatus[t2.status] = (byStatus[t2.status] ?? 0) + 1
      for (const l of t2.labels ?? []) {
        if (!byLabel[l]) byLabel[l] = { count: 0, size: 0 }
        byLabel[l].count++
        byLabel[l].size += t2.totalSize || 0
      }
      for (const s of sites[t2.id] ?? []) {
        if (!bySite[s]) bySite[s] = { count: 0, size: 0 }
        bySite[s].count++
        bySite[s].size += t2.totalSize || 0
      }
    }
    return { downloaded, uploaded, active, downloading, seeding, paused, error, byStatus, byLabel, bySite }
  }, [torrents])

  const total = torrents.length
  const ratio = stats.downloaded > 0 ? stats.uploaded / stats.downloaded : 0
  const card = 'flex-1 min-w-[120px] rounded-xl glass-section p-3 text-center'

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="sm:max-w-3xl h-[90dvh] sm:h-auto sm:max-h-[85dvh] flex flex-col p-0">
        <DialogHeader className="px-4 pt-4 pb-2 border-b border-gray-200/40 dark:border-gray-700/30">
          <DialogTitle>{t('dashboard.title')}</DialogTitle>
        </DialogHeader>
        <div className="overflow-y-auto px-4 py-3 space-y-4">
        {/* 统计卡片 */}
        <div className="flex flex-wrap gap-2">
          <div className={card}>
            <div className="text-footnote text-gray-400 mb-1">{t('dashboard.torrentCount')}</div>
            <div className="text-title2 font-semibold">{total}</div>
          </div>
          <div className={card}>
            <div className="text-footnote text-gray-400 mb-1">{t('dashboard.totalDownloaded')}</div>
            <div className="text-title2 font-semibold text-green-600 dark:text-green-400">{formatBytes(stats.downloaded)}</div>
          </div>
          <div className={card}>
            <div className="text-footnote text-gray-400 mb-1">{t('dashboard.totalUploaded')}</div>
            <div className="text-title2 font-semibold text-blue-600 dark:text-blue-400">{formatBytes(stats.uploaded)}</div>
          </div>
          <div className={card}>
            <div className="text-footnote text-gray-400 mb-1">{t('dashboard.globalRatio')}</div>
            <div className="text-title2 font-semibold text-orange-500">{formatRatio(ratio)}</div>
          </div>
          <div className={card}>
            <div className="text-footnote text-gray-400 mb-1">{t('dashboard.active')}</div>
            <div className="text-title2 font-semibold">{stats.active}</div>
          </div>
        </div>

        {/* 状态分布 + 速度历史 */}
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div className="rounded-xl glass-section p-3">
            <div className="text-body font-medium mb-2">{t('dashboard.statusDist')}</div>
            <StatusDonut byStatus={stats.byStatus} />
          </div>
          <div className="rounded-xl glass-section p-3">
            <div className="text-body font-medium mb-2">{t('dashboard.speedHistory')}</div>
            <SpeedHistory />
          </div>
        </div>

        {/* 标签 / 站点流量 */}
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <BarList title={t('dashboard.byLabel')} data={stats.byLabel} />
          <BarList title={t('dashboard.bySite')} data={stats.bySite} />
        </div>
      </div>
      </DialogContent>
    </Dialog>
  )
}

// 状态分布环形图（SVG）
function StatusDonut({ byStatus }: { byStatus: Record<number, number> }) {
  const { t } = useTranslation()
  const entries = Object.entries(byStatus)
    .map(([s, n]) => ({ status: Number(s), count: n }))
    .sort((a, b) => b.count - a.count)
  const total = entries.reduce((sum, e) => sum + e.count, 0)
  if (total === 0) {
    return <div className="py-8 text-center text-footnote text-gray-400">{t('common.empty')}</div>
  }
  const R = 40
  const CIRC = 2 * Math.PI * R
  let offset = 0

  return (
    <div className="flex items-center gap-4">
      <svg viewBox="0 0 100 100" className="w-28 h-28 shrink-0">
        <circle cx="50" cy="50" r={R} fill="none" stroke="rgba(128,128,128,0.15)" strokeWidth="12" />
        {entries.map((e) => {
          const frac = e.count / total
          const len = frac * CIRC
          const el = (
            <circle
              key={e.status}
              cx="50" cy="50" r={R} fill="none"
              strokeWidth="12"
              style={{ stroke: statusMeta(e.status).color }}
              strokeDasharray={`${len} ${CIRC - len}`}
              strokeDashoffset={-offset}
              strokeLinecap="butt"
              transform="rotate(-90 50 50)"
            />
          )
          offset += len
          return el
        })}
        <text x="50" y="48" textAnchor="middle" className="text-caption2 fill-gray-400">{total}</text>
        <text x="50" y="60" textAnchor="middle" className="text-caption2 fill-gray-500">{t('dashboard.total')}</text>
      </svg>
      <div className="flex-1 space-y-1 min-w-0">
        {entries.slice(0, 6).map((e) => (
          <div key={e.status} className="flex items-center gap-2 text-footnote">
            <span className="w-2.5 h-2.5 rounded-sm shrink-0" style={{ background: statusMeta(e.status).color }} />
            <span className="flex-1 truncate">{t(`status.${e.status}`)}</span>
            <span className="text-gray-500">{e.count}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

// 横向条形统计（标签/站点分布）
function BarList({ title, data }: {
  title: string
  data: Record<string, { count: number; size: number }>
}) {
  const { t } = useTranslation()
  const entries = Object.entries(data).sort((a, b) => b[1].size - a[1].size).slice(0, 6)
  if (entries.length === 0) {
    return (
      <div className="rounded-xl glass-section p-3">
        <div className="text-body font-medium mb-2">{title}</div>
        <div className="py-6 text-center text-footnote text-gray-400">{t('common.empty')}</div>
      </div>
    )
  }
  const max = Math.max(...entries.map(([, v]) => v.size), 1)
  return (
    <div className="rounded-xl glass-section p-3">
      <div className="text-body font-medium mb-2">{title}</div>
      <div className="space-y-1.5">
        {entries.map(([k, v]) => (
          <div key={k}>
            <div className="flex items-center justify-between text-footnote mb-0.5">
              <span className="truncate max-w-[60%]">{k}</span>
              <span className="text-gray-500">{v.count} · {formatBytes(v.size)}</span>
            </div>
            <div className="tm-progress-track">
              <div className="tm-progress-fill" style={{ width: `${(v.size / max) * 100}%` }} />
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
