import { useMemo } from 'react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { useAppStore } from '@/stores/appStore'
import { formatBytes, formatRatio } from '@/utils/format'
import { SpeedHistory } from '@/components/StatusBar/SpeedHistory'
import { TrafficHistory } from '@/components/Dashboard/TrafficHistory'
import { statusMeta } from '@/utils/status'
import { isActiveTorrent } from '@/hooks/useFilter'
import { cn } from '@/lib/utils'

// 统计页签（保种台 → 统计）：全库概览 + 状态分布 + 历史流量 + 速度历史 + 标签分布。
//
// 与「概览」的分工：概览读后端台账（保种资产、站点预算、释放计划，带采样覆盖说明），
// 这一页只读本地种子列表的累计计数器——除流量历史外不发任何请求，打开即渲染。
//
// 站点分布刻意不在这里重复：概览的站点表信息更全（今日上传、预算、分享率），
// 且只有那一套站点识别口径，两张表各自算一遍只会让用户对不上数。
export function StatsTab() {
  const { t } = useTranslation()
  const torrents = useAppStore((s) => s.torrents)

  const stats = useMemo(() => {
    let downloaded = 0
    let uploaded = 0
    let active = 0
    const byStatus: Record<number, number> = {}
    const byLabel: Record<string, { count: number; size: number }> = {}

    for (const t2 of torrents) {
      downloaded += t2.downloadedEver || 0
      uploaded += t2.uploadedEver || 0
      if (isActiveTorrent(t2)) active++
      byStatus[t2.status] = (byStatus[t2.status] ?? 0) + 1
      for (const l of t2.labels ?? []) {
        if (!byLabel[l]) byLabel[l] = { count: 0, size: 0 }
        byLabel[l].count++
        byLabel[l].size += t2.totalSize || 0
      }
    }
    return { downloaded, uploaded, active, byStatus, byLabel }
  }, [torrents])

  const total = torrents.length
  const ratio = stats.downloaded > 0 ? stats.uploaded / stats.downloaded : 0

  return (
    <div className="space-y-3">
      {/* 统计卡片：与概览的 StatCard 同一套网格与字号，切页签时视觉不跳 */}
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5">
        <Card label={t('dashboard.torrentCount')} value={String(total)} />
        <Card
          label={t('dashboard.totalDownloaded')}
          value={formatBytes(stats.downloaded)}
          tone="text-green-600 dark:text-green-400"
        />
        <Card
          label={t('dashboard.totalUploaded')}
          value={formatBytes(stats.uploaded)}
          tone="text-blue-600 dark:text-blue-400"
        />
        <Card label={t('dashboard.globalRatio')} value={formatRatio(ratio)} tone="text-orange-500" />
        <Card label={t('dashboard.active')} value={String(stats.active)} />
      </div>

      {/* 历史流量：上下行折线，三档区间。放在「速度历史」之上——
          速度历史只有最近两分钟（前端内存采样），这一张才是真历史 */}
      <TrafficHistory />

      {/* 状态分布 + 速度历史 */}
      <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        <Panel>
          <div className="mb-2 text-body font-medium">{t('dashboard.statusDist')}</div>
          <StatusDonut byStatus={stats.byStatus} />
        </Panel>
        <Panel>
          <div className="mb-2 text-body font-medium">{t('dashboard.speedHistory')}</div>
          <SpeedHistory />
        </Panel>
      </div>

      {/* 标签分布 */}
      <BarList title={t('dashboard.byLabel')} data={stats.byLabel} />

      <p className="text-caption1 text-gray-500 dark:text-gray-400">{t('pt.statsNote')}</p>
    </div>
  )
}

// 弹窗根容器是 glass-panel-strong，子卡片只能用 glass-subcard（见 PTDialog 的 Panel 注释），
// 不能再叠一层 backdrop-filter。
function Panel({ children }: { children: ReactNode }) {
  return <div className="glass-subcard rounded-tile p-3">{children}</div>
}

function Card({ label, value, tone }: { label: string; value: string; tone?: string }) {
  return (
    <div className="glass-subcard rounded-tile px-3 py-2">
      <p className="truncate text-caption1 text-gray-500 dark:text-gray-400">{label}</p>
      <p className={cn('mt-0.5 truncate text-title2 font-semibold tabular-nums', tone)}>{value}</p>
    </div>
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
      <svg viewBox="0 0 100 100" className="h-28 w-28 shrink-0">
        <circle cx="50" cy="50" r={R} fill="none" stroke="rgba(128,128,128,0.15)" strokeWidth="12" />
        {entries.map((e) => {
          const frac = e.count / total
          const len = frac * CIRC
          const el = (
            <circle
              key={e.status}
              cx="50"
              cy="50"
              r={R}
              fill="none"
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
        <text x="50" y="48" textAnchor="middle" className="text-caption2 fill-gray-400">
          {total}
        </text>
        <text x="50" y="60" textAnchor="middle" className="text-caption2 fill-gray-500">
          {t('dashboard.total')}
        </text>
      </svg>
      <div className="min-w-0 flex-1 space-y-1">
        {entries.slice(0, 6).map((e) => (
          <div key={e.status} className="flex items-center gap-2 text-footnote">
            <span
              className="h-2.5 w-2.5 shrink-0 rounded-sm"
              style={{ background: statusMeta(e.status).color }}
            />
            <span className="flex-1 truncate">{t(`status.${e.status}`)}</span>
            <span className="text-gray-500">{e.count}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

// 横向条形统计（标签分布）
function BarList({ title, data }: { title: string; data: Record<string, { count: number; size: number }> }) {
  const { t } = useTranslation()
  const entries = Object.entries(data)
    .sort((a, b) => b[1].size - a[1].size)
    .slice(0, 6)
  if (entries.length === 0) {
    return (
      <Panel>
        <div className="mb-2 text-body font-medium">{title}</div>
        <div className="py-6 text-center text-footnote text-gray-400">{t('common.empty')}</div>
      </Panel>
    )
  }
  const max = Math.max(...entries.map(([, v]) => v.size), 1)
  return (
    <Panel>
      <div className="mb-2 text-body font-medium">{title}</div>
      <div className="space-y-1.5">
        {entries.map(([k, v]) => (
          <div key={k}>
            <div className="mb-0.5 flex items-center justify-between text-footnote">
              <span className="max-w-[60%] truncate">{k}</span>
              <span className="text-gray-500">
                {v.count} · {formatBytes(v.size)}
              </span>
            </div>
            <div className="tm-progress-track">
              <div className="tm-progress-fill" style={{ width: `${(v.size / max) * 100}%` }} />
            </div>
          </div>
        ))}
      </div>
    </Panel>
  )
}
