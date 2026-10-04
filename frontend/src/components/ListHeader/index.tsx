import { useMemo, useState } from 'react'
import { ArrowDownUp, ArrowUpDown, BarChart3, FilePlus2, LayoutGrid, LayoutList, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { torrentApi } from '@/api/torrent'
import { useAppStore } from '@/stores/appStore'
import { useResponsive } from '@/hooks/useResponsive'
import { matchesStatus } from '@/hooks/useFilter'
import { cn } from '@/lib/utils'
import { toast } from '@/lib/toast'
import { STATUS_ITEMS } from '@/components/Sidebar'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import type { Torrent } from '@/types'

const STATUS_LABEL: Record<string, string> = {
  all: 'nav.all',
  active: 'nav.active',
  downloading: 'nav.downloading',
  seeding: 'nav.seeding',
  'waiting-seed': 'nav.waitingSeed',
  completed: 'nav.completed',
  paused: 'nav.paused',
  verifying: 'nav.verifying',
  error: 'nav.error',
}

// 移动端标题栏用的状态分组计数（与侧边栏同源：STATUS_ITEMS + matchesStatus）
export function statusCountsFor(torrents: Torrent[], matchesStatus: (t: Torrent, s: string) => boolean) {
  const counts: Record<string, number> = { all: torrents.length }
  for (const tr of torrents) {
    for (const item of STATUS_ITEMS) {
      if (item.key !== 'all' && matchesStatus(tr, item.key)) {
        counts[item.key] = (counts[item.key] ?? 0) + 1
      }
    }
  }
  return counts
}

const SORT_OPTIONS = [
  { key: 'name', label: 'filter.sort.name' },
  { key: 'size', label: 'filter.sort.size' },
  { key: 'added', label: 'filter.sort.date' },
  { key: 'doneDate', label: 'filter.sort.doneDate' },
  { key: 'ratio', label: 'filter.sort.ratio' },
  { key: 'secondsSeeding', label: 'filter.sort.secondsSeeding' },
  { key: 'download', label: 'filter.sort.dlSpeed' },
  { key: 'upload', label: 'filter.sort.upSpeed' },
  { key: 'progress', label: 'filter.sort.progress' },
  { key: 'uploaded', label: 'filter.sort.uploaded' },
  { key: 'downloaded', label: 'filter.sort.downloaded' },
] as const

// 排序按钮的提示语按 col.key 查文案：列头点击传的是 col.key（download / eta / tracker…），
// 而 filter.sort.* 的键名与它们并不一一对应（dlSpeed / upSpeed / date），直接拼 key 会显示原文。
// 先查排序菜单自己的映射（与下拉里看到的词一致），查不到再退回 columns.<key>（覆盖全部列）。
const SORT_LABEL: Record<string, string> = Object.fromEntries(SORT_OPTIONS.map((o) => [o.key, o.label]))

// 排序 / 视图切换 / 刷新 控制组（桌面端并入 TopBar，移动端留在 ListHeader）
export function ListControls({ compact, isMobile, onOpenDashboard, onOpenCreate }: { compact?: boolean; isMobile?: boolean; onOpenDashboard?: () => void; onOpenCreate?: () => void }) {
  const { t } = useTranslation()
  const { isCoarse } = useResponsive()
  const sortField = useAppStore((s) => s.sortField)
  const sortOrder = useAppStore((s) => s.sortOrder)
  const setSortField = useAppStore((s) => s.setSortField)
  const setSortOrder = useAppStore((s) => s.setSortOrder)
  const viewMode = useAppStore((s) => s.viewMode)
  const setViewMode = useAppStore((s) => s.setViewMode)
  const setTorrents = useAppStore((s) => s.setTorrents)
  const [sortOpen, setSortOpen] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const sortHint = `${t('filter.sortBy')}: ${t(SORT_LABEL[sortField] ?? `columns.${sortField}`)}`

  const changeSort = (field: string) => {
    const nextAsc = sortField === field ? (sortOrder === 'asc' ? 'desc' : 'asc') : 'asc'
    setSortField(field)
    setSortOrder(nextAsc)
  }

  // 触屏一律 44pt 命中区（按输入能力判定，平板走的是顶栏紧凑档）；桌面按紧凑/宽松两档
  const big = isMobile || isCoarse
  const iconBtn = big ? 'h-11 w-11 rounded-full' : compact ? 'h-9 w-9 rounded-full' : 'h-8 w-8 rounded-lg'
  const iconSize = big ? 'w-5 h-5' : 'w-4 h-4'

  const refresh = async () => {
    setRefreshing(true)
    try {
      const list = await torrentApi.list()
      setTorrents(list)
      toast.success(t('toast.refreshed'))
    } catch {
      // 拦截器已提示
    } finally {
      setRefreshing(false)
    }
  }

  return (
    <div className={cn('flex items-center gap-1.5 shrink-0', compact && 'gap-0.5')}>
      {/* 排序 */}
      <Popover open={sortOpen} onOpenChange={setSortOpen}>
        <PopoverTrigger asChild>
          <button
            className={cn(
              iconBtn,
              'flex items-center justify-center transition-colors',
              sortField !== 'name'
                ? 'text-primary bg-primary/10'
                : 'text-gray-500 dark:text-gray-400 hover:bg-white/60 dark:hover:bg-white/10',
            )}
            title={sortHint}
            aria-label={sortHint}
          >
            {sortField === 'name' ? (
              <ArrowUpDown className={iconSize} />
            ) : (
              <ArrowDownUp className={cn(iconSize, sortOrder === 'asc' ? 'rotate-0' : 'rotate-180')} />
            )}
          </button>
        </PopoverTrigger>
        <PopoverContent className="glass-panel-strong p-1 w-40" align="end">
          <div className="space-y-0.5">
            {SORT_OPTIONS.map((opt) => {
              const isActive = sortField === opt.key
              return (
                <button
                  key={opt.key}
                  onClick={() => { changeSort(opt.key); setSortOpen(false) }}
                  className={cn(
                    'w-full flex items-center justify-between px-2.5 rounded-lg text-footnote transition-colors',
                    big ? 'min-h-11' : 'py-1.5',
                    isActive
                      ? 'bg-primary/15 text-primary font-medium'
                      : 'text-gray-700 dark:text-gray-300 hover:bg-white/60 dark:hover:bg-white/10',
                  )}
                >
                  <span>{t(opt.label)}</span>
                  {isActive && <ArrowDownUp className={cn('w-3 h-3', sortOrder === 'asc' ? 'rotate-0' : 'rotate-180')} />}
                </button>
              )
            })}
          </div>
        </PopoverContent>
      </Popover>

      {/* 视图切换（移动端隐藏） */}
      {!isMobile && (
        <button
          onClick={() => setViewMode(viewMode === 'grid' ? 'table' : 'grid')}
          className={cn(
            iconBtn,
            'flex items-center justify-center text-gray-500 dark:text-gray-400 hover:text-primary hover:bg-white/60 dark:hover:bg-white/10 transition-colors',
          )}
          title={viewMode === 'grid' ? t('dashboard.viewTable') : t('dashboard.viewGrid')}
          aria-label={viewMode === 'grid' ? t('dashboard.viewTable') : t('dashboard.viewGrid')}
        >
          {viewMode === 'grid' ? <LayoutList className={iconSize} /> : <LayoutGrid className={iconSize} />}
        </button>
      )}

      {/* 刷新 */}
      <button
        onClick={() => void refresh()}
        className={cn(
          iconBtn,
          'flex items-center justify-center text-gray-500 dark:text-gray-400 hover:text-primary hover:bg-white/60 dark:hover:bg-white/10 transition-colors',
        )}
        title={t('common.refresh')}
        aria-label={t('common.refresh')}
      >
        <RefreshCw className={cn(iconSize, refreshing && 'animate-spin')} />
      </button>

      {/* 创建种子：移动端与统计仪表并排（桌面端入口在顶栏） */}
      {isMobile && onOpenCreate && (
        <button
          onClick={onOpenCreate}
          className={cn(
            iconBtn,
            'flex items-center justify-center text-gray-500 dark:text-gray-400 hover:text-primary hover:bg-white/60 dark:hover:bg-white/10 transition-colors',
          )}
          title={t('createTorrent.title')}
          aria-label={t('createTorrent.title')}
        >
          <FilePlus2 className={iconSize} />
        </button>
      )}

      {/* 统计仪表盘 */}
      {onOpenDashboard && (
        <button
          onClick={onOpenDashboard}
          className={cn(
            iconBtn,
            'flex items-center justify-center text-gray-500 dark:text-gray-400 hover:text-primary hover:bg-white/60 dark:hover:bg-white/10 transition-colors',
          )}
          title={t('dashboard.title')}
          aria-label={t('dashboard.title')}
        >
          <BarChart3 className={iconSize} />
        </button>
      )}
    </div>
  )
}

// 主区标题栏：分类标题（点击下拉切换状态分组）+ 计数 + 排序/视图/刷新（仅移动端渲染）
export function ListHeader({ count, isMobile, onOpenDashboard, onOpenCreate }: { count: number; isMobile?: boolean; onOpenDashboard?: () => void; onOpenCreate?: () => void }) {
  const { t } = useTranslation()
  const filters = useAppStore((s) => s.filters)
  const setFilters = useAppStore((s) => s.setFilters)
  const torrents = useAppStore((s) => s.torrents)
  const statusFilterVisible = useAppStore((s) => s.statusFilterVisible)
  const activeStatus = filters.status[0] || 'all'
  const titleKey = STATUS_LABEL[activeStatus] ?? 'nav.all'
  const [statusOpen, setStatusOpen] = useState(false)

  // 各状态分组计数：与侧边栏/移动抽屉同源（STATUS_ITEMS + matchesStatus）
  const statusCounts = useMemo(() => statusCountsFor(torrents, matchesStatus), [torrents])
  const visibleItems = STATUS_ITEMS.filter(
    (item) => item.key === 'all' || statusFilterVisible[item.key] !== false,
  )

  const pickStatus = (key: string) => {
    setFilters({ status: key === 'all' ? ['all'] : [key] })
    setStatusOpen(false)
  }

  return (
    <div className="tm-dock glass-panel rounded-dock h-11 px-4 flex items-center gap-3">
      {/* 标题 + 计数：点击展开状态分组下拉，快速切换分类 */}
      <Popover open={statusOpen} onOpenChange={setStatusOpen}>
        <PopoverTrigger asChild>
          <button
            type="button"
            aria-label={t('nav.filter')}
            className="flex items-center min-w-0 min-h-11 -my-1 -ml-1 pl-1 pr-1.5 rounded-lg transition-colors hover:bg-white/60 dark:hover:bg-white/10 active:bg-white/80 dark:active:bg-white/15"
          >
            {/* 文字基线对齐由内层负责：按钮自身 44pt 高，直接 baseline 会把文字锚到顶部而偏上 */}
            <span className="flex items-baseline gap-1.5 min-w-0">
              <span className="text-subhead font-semibold text-gray-800 dark:text-gray-100 whitespace-nowrap shrink-0">{t(titleKey)}</span>
              <span className="text-footnote text-gray-400 tm-mono truncate min-w-0">{`(${count})`}</span>
            </span>
          </button>
        </PopoverTrigger>
        <PopoverContent className="glass-panel-strong p-1 w-44" align="start">
          <div className="space-y-0.5">
            {visibleItems.map((item) => {
              const Icon = item.icon
              const isActive = activeStatus === item.key
              const groupCount = statusCounts[item.key] ?? 0
              return (
                <button
                  key={item.key}
                  onClick={() => pickStatus(item.key)}
                  className={cn(
                    'w-full flex items-center gap-2.5 px-2.5 rounded-lg text-footnote transition-colors min-h-11',
                    isActive
                      ? 'bg-primary/15 text-primary font-medium'
                      : 'text-gray-700 dark:text-gray-300 hover:bg-white/60 dark:hover:bg-white/10',
                  )}
                >
                  <Icon
                    className={cn(
                      'w-4 h-4 shrink-0',
                      isActive
                        ? 'text-primary'
                        : item.key === 'error' && groupCount > 0
                          ? 'text-red-500'
                          : 'text-gray-500 dark:text-gray-400',
                    )}
                  />
                  <span className="flex-1 text-left truncate">{t(item.label)}</span>
                  <span
                    className={cn(
                      'min-w-5 h-5 px-1.5 rounded-full text-caption2 tm-mono flex items-center justify-center',
                      isActive
                        ? 'bg-primary/15 text-primary'
                        : 'bg-white/70 dark:bg-white/10 text-gray-500 dark:text-gray-400',
                      item.key === 'error' && groupCount > 0 && !isActive && 'bg-red-500 text-white',
                    )}
                  >
                    {groupCount}
                  </span>
                </button>
              )
            })}
          </div>
        </PopoverContent>
      </Popover>

      <div className="ml-auto flex items-center gap-1.5 shrink-0">
        <ListControls isMobile onOpenDashboard={onOpenDashboard} onOpenCreate={onOpenCreate} />
      </div>
    </div>
  )
}
