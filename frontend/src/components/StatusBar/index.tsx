import React, { useEffect, useMemo, useRef, useState } from 'react'
import { HardDrive } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { sessionApi } from '@/api/torrent'
import { STATUS_ITEMS } from '@/components/Sidebar'
import { matchesStatus } from '@/hooks/useFilter'
import { useAppStore } from '@/stores/appStore'
import { usePlatform } from '@/platform'
import { cn } from '@/lib/utils'
import { formatBytes } from '@/utils/format'
import { Badge } from '@/components/ui/badge'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import type { SessionStats } from '@/types'

interface Props {
  isMobile?: boolean
}

// 状态码 → 图标/颜色/文案统一取自 utils/status，这里不再维护副本

// 图一状态栏：已连接 · PPC 免密认证 · 本次会话 ↓/↑ 流量
export const StatusBar: React.FC<Props> = ({ isMobile }) => {
  const { t } = useTranslation()
  const { can } = usePlatform()
  const torrents = useAppStore((s) => s.torrents)
  const wsStatus = useAppStore((s) => s.wsStatus)
  const session = useAppStore((s) => s.session)
  const [showStats, setShowStats] = useState(false)
  const prevCountRef = useRef(torrents.length)
  const firstLoadRef = useRef(true)
  const [newCount, setNewCount] = useState(0)
  // 下载目录可用空间（60s 刷新）
  const [freeSpace, setFreeSpace] = useState<{ freeSpace: number; totalSize: number } | null>(null)
  // 会话统计（本次会话流量）
  const [stats, setStats] = useState<SessionStats | null>(null)

  useEffect(() => {
    if (!session?.downloadDir) return
    let cancelled = false
    const load = () => {
      sessionApi
        .freeSpace(session.downloadDir)
        .then((d) => { if (!cancelled) setFreeSpace(d) })
        .catch(() => {})
    }
    load()
    const id = setInterval(load, 60000)
    return () => { cancelled = true; clearInterval(id) }
  }, [session?.downloadDir])

  useEffect(() => {
    let cancelled = false
    const load = () => {
      sessionApi
        .stats()
        .then((s) => { if (!cancelled) setStats(s) })
        .catch(() => {})
    }
    load()
    const id = setInterval(load, 30000)
    return () => { cancelled = true; clearInterval(id) }
  }, [])

  // 检测新种子（跳过首次加载，避免每次刷新/重连都闪现 "+N"）
  useEffect(() => {
    // 首次挂载或首次拿到种子列表时只记录基线，不提示
    if (firstLoadRef.current) {
      if (torrents.length > 0) firstLoadRef.current = false
      prevCountRef.current = torrents.length
      return
    }
    const diff = torrents.length - prevCountRef.current
    if (diff > 0) {
      setNewCount(diff)
      const timer = setTimeout(() => setNewCount(0), 2000)
      prevCountRef.current = torrents.length
      return () => clearTimeout(timer)
    }
    prevCountRef.current = torrents.length
  }, [torrents.length])

  // 与侧栏过滤器同口径（STATUS_ITEMS × matchesStatus）：
  // 同名分类只允许一套数字，否则右下角和侧栏永远对不上
  const statusCounts = useMemo(() => {
    const counts: Record<string, number> = { all: torrents.length }
    for (const tr of torrents) {
      for (const item of STATUS_ITEMS) {
        if (item.key !== 'all' && matchesStatus(tr, item.key)) {
          counts[item.key] = (counts[item.key] ?? 0) + 1
        }
      }
    }
    return counts
  }, [torrents])

  const activeCount = statusCounts.active ?? 0
  const pausedCount = statusCounts.paused ?? 0
  const verifyingCount = statusCounts.verifying ?? 0
  // 窄屏摘要用：异常任务数是最需要被看见的一项，即使底栏再挤也要保留
  const errorCount = statusCounts.error ?? 0

  const statusText = wsStatus === 'connected'
    ? t('common.connected')
    : wsStatus === 'connecting'
      ? t('common.connecting')
      : t('common.disconnected')

  return (
    <div className="tm-dock glass-panel rounded-dock h-9 flex items-center gap-2 px-4 text-footnote text-gray-500 dark:text-gray-400 tm-glass-label">
      <div className="flex items-center gap-2 flex-wrap min-w-0">
        {/* 连接状态 */}
        <span
          role="status"
          aria-live="polite"
          className={cn('flex items-center gap-1.5 shrink-0', wsStatus === 'connected' ? 'text-green-600 dark:text-green-400' : 'text-gray-400')}
        >
          <span className={cn('w-1.5 h-1.5 rounded-full', wsStatus === 'connected' ? 'bg-green-500' : 'bg-gray-400 animate-pulse')} />
          {statusText}
        </span>

        {/* 免密认证由宿主统一承担；通用部署下没有这回事，不显示 */}
        {can('auth.passwordless') && (
          <>
            <span className="text-gray-300 dark:text-gray-600 hidden sm:inline">·</span>
            <span className="hidden sm:inline shrink-0">{t('common.passwordless')}</span>
          </>
        )}

        {/* 本次会话流量 */}
        {stats && (
          <span className="flex items-center gap-2 shrink-0 tm-mono">
            <span className="text-gray-400 hidden lg:inline">{t('status.sessionTraffic')}</span>
            <span className="text-green-600 dark:text-green-400">↓{formatBytes(stats.current.downloadedBytes)}</span>
            <span className="text-blue-600 dark:text-blue-400">↑{formatBytes(stats.current.uploadedBytes)}</span>
          </span>
        )}

        {/* 新增提醒 */}
        {newCount > 0 && (
          <Badge variant="outline" className="bg-primary/10 text-primary border-primary/30 text-footnote px-1.5 py-0 shrink-0">
            +{newCount}
          </Badge>
        )}
      </div>

      {/* 实时统计：窄屏放不下时不再整块隐藏，而是把同样的明细收进「详情」里。
          此前这几项全是 hidden md:flex，手机端底栏只剩连接状态与流量，
          侧栏又是抽屉——移动端用户看不到任何任务汇总计数 */}
      <div className="ml-auto flex items-center gap-3 shrink-0">
        <span className="hidden md:flex items-center gap-1 shrink-0">
          <span className="text-gray-400">{t('common.torrentCount')}</span>
          <span className="tm-mono">{torrents.length}</span>
        </span>
        <span className="hidden md:flex items-center gap-1 shrink-0 text-primary">
          {t('status.activeShort', { count: activeCount })}
        </span>
        {pausedCount > 0 && (
          <span className="hidden md:flex items-center gap-1 shrink-0 text-gray-400">
            {t('status.pausedShort', { count: pausedCount })}
          </span>
        )}
        {verifyingCount > 0 && (
          <span className="hidden md:flex items-center gap-1 shrink-0 text-orange-500">
            {t('status.verifyingShort', { count: verifyingCount })}
          </span>
        )}

        {/* 硬盘剩余空间：窄屏降级为纯图标，数字收进 title 与详情弹层 */}
        {freeSpace && (
          <span
            className="flex items-center gap-1 shrink-0"
            title={`${t('statusBar.freeSpace')}: ${formatBytes(freeSpace.freeSpace)} / ${formatBytes(freeSpace.totalSize)}`}
          >
            <HardDrive className="w-3 h-3 text-gray-400" />
            <span className="tm-mono hidden md:inline">{formatBytes(freeSpace.freeSpace)}</span>
          </span>
        )}

        {/* 窄屏汇总摘要：让「详情」在移动端也承担入口作用，而不是只显示两个字 */}
        <span className="flex md:hidden items-center gap-1.5 shrink-0 tm-mono">
          <span className="text-gray-400">{t('common.torrentCount')}</span>
          <span>{torrents.length}</span>
          {activeCount > 0 && <span className="text-primary">·{activeCount}</span>}
          {errorCount > 0 && <span className="text-red-500">!{errorCount}</span>}
        </span>

        {/* 统计详情弹窗 */}
        <Popover open={showStats} onOpenChange={setShowStats}>
          <PopoverTrigger asChild>
            <button className="tm-hug px-2 text-footnote hover:text-primary transition-colors underline decoration-dashed underline-offset-2 shrink-0">
              {t('status.details')}
            </button>
          </PopoverTrigger>
          <PopoverContent className="glass-panel-strong p-3 w-56" align="end">
            <div className="space-y-2 text-footnote">
              {/* 会话流量与实时计数在窄屏从底栏移入此处，保证信息不丢失 */}
              <div className="md:hidden space-y-1 pb-2 border-b border-white/60 dark:border-white/10">
                <div className="flex justify-between">
                  <span className="text-gray-500">{t('common.torrentCount')}</span>
                  <span className="tm-mono">{torrents.length}</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-gray-500">{t('nav.active')}</span>
                  <span className="tm-mono text-primary">{activeCount}</span>
                </div>
                {freeSpace && (
                  <div className="flex justify-between">
                    <span className="text-gray-500">{t('statusBar.freeSpace')}</span>
                    <span className="tm-mono">
                      {formatBytes(freeSpace.freeSpace)} / {formatBytes(freeSpace.totalSize)}
                    </span>
                  </div>
                )}
              </div>
              {stats && (
                <>
                  <div className="flex justify-between">
                    <span className="text-gray-500">{t('session.downloaded')}</span>
                    <span className="tm-mono text-green-600 dark:text-green-400">{formatBytes(stats.cumulative.downloadedBytes)}</span>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-gray-500">{t('session.uploaded')}</span>
                    <span className="tm-mono text-blue-600 dark:text-blue-400">{formatBytes(stats.cumulative.uploadedBytes)}</span>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-gray-500">{t('session.sessionCount')}</span>
                    <span className="tm-mono">{stats.cumulative.sessionCount}</span>
                  </div>
                  {session && (
                    <>
                      <div className="border-t border-white/60 dark:border-white/10 pt-2 mt-1 flex justify-between">
                        <span className="text-gray-500">{t('status.downloadSpeed')}</span>
                        <span className="tm-mono">{session.speedLimitDown} KB/s</span>
                      </div>
                      <div className="flex justify-between">
                        <span className="text-gray-500">{t('status.uploadSpeed')}</span>
                        <span className="tm-mono">{session.speedLimitUp} KB/s</span>
                      </div>
                    </>
                  )}
                </>
              )}
              <div className="border-t border-white/60 dark:border-white/10 pt-2 mt-2">
                <div className="text-gray-500 mb-1">{t('common.status')}</div>
                {STATUS_ITEMS.map((item) => {
                  const Icon = item.icon
                  const count = statusCounts[item.key] ?? 0
                  return (
                    <div key={item.key} className="flex items-center justify-between py-0.5">
                      <div className="flex items-center gap-1.5">
                        <Icon
                          className={cn(
                            'w-3 h-3',
                            item.key === 'error' && count > 0 ? 'text-red-500' : 'text-gray-400',
                          )}
                        />
                        <span className="text-gray-600 dark:text-gray-300">{t(item.label)}</span>
                      </div>
                      <span className="tm-mono text-gray-500">{count}</span>
                    </div>
                  )
                })}
              </div>
            </div>
          </PopoverContent>
        </Popover>
      </div>
    </div>
  )
}
