import React, { useEffect, useMemo, useRef, useState } from 'react'
import {
  Activity,
  AlertCircle,
  ArrowDown,
  ArrowUp,
  Check,
  CheckCircle,
  ChevronDown,
  CloudDownload,
  FolderOpen,
  Gauge,
  Globe,
  Layers,
  PauseCircle,
  RotateCcw,
  Search,
  Server,
  Settings,
  ShieldAlert,
  Tags,
  Upload,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { createPortal } from 'react-dom'
import { useAppStore } from '@/stores/appStore'
import { matchesStatus } from '@/hooks/useFilter'
import { useNavRail } from '@/hooks/useNavRail'
import { useDiskSpaces } from '@/hooks/useDiskSpaces'
import { useFloatingMenuPosition, useDismissOnOutside } from '@/hooks/useFloatingMenuPosition'
import { useSemanticPath } from '@/hooks/useSemanticPath'
import { usePlatform } from '@/platform'
import { FloatingContextMenu } from '@/components/TorrentMenu'
import type { MenuItem } from '@/components/TorrentMenu'
import { formatBytes, formatRatio, formatSpeed } from '@/utils/format'
import { translateError } from '@/utils/errorText'
import { tagColor } from '@/utils/tagColor'
import { cn, cssVars } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { KindBadge } from '@/components/TorrentList/ServerCell'
import { DiskSpaceCard } from '@/components/Sidebar/DiskSpaceCard'
import type { DownloaderKind, SidebarMenuVisible } from '@/types'

// 主状态过滤：全部 / 活跃 / 正在下载 / 正在做种 / 已完成 / 暂停 / 校验 / 错误
// 图标统一由中性色承载、激活时染品牌色；仅 error 在存在异常时保留红色语义
// 导出供移动端滑动切分类复用：滑动顺序与列表必须同源，否则会出现滑得到、点不到的分类
export const STATUS_ITEMS: { key: string; icon: React.ElementType; label: string }[] = [
  { key: 'all', icon: Layers, label: 'nav.all' },
  { key: 'active', icon: Activity, label: 'nav.active' },
  { key: 'downloading', icon: CloudDownload, label: 'nav.downloading' },
  { key: 'seeding', icon: Upload, label: 'nav.seeding' },
  { key: 'completed', icon: CheckCircle, label: 'nav.completed' },
  { key: 'paused', icon: PauseCircle, label: 'nav.paused' },
  { key: 'verifying', icon: RotateCcw, label: 'nav.verifying' },
  { key: 'error', icon: ShieldAlert, label: 'nav.error' },
]

// 错误分组沿用渗入玻璃的选中语言，只把色相从品牌色换成系统红
const ERROR_TINT = { '--nav-tint': 'var(--color-red-500)' } as React.CSSProperties

export const DesktopSidebar: React.FC = () => {
  const { t } = useTranslation()
  const sem = useSemanticPath()
  const filters = useAppStore((s) => s.filters)
  const setFilters = useAppStore((s) => s.setFilters)
  const torrents = useAppStore((s) => s.torrents)
  const torrentSites = useAppStore((s) => s.torrentSites)
  const showStats = useAppStore((s) => s.showStats)
  const setShowStats = useAppStore((s) => s.setShowStats)
  const groupShowSize = useAppStore((s) => s.groupShowSize)
  const setGroupShowSize = useAppStore((s) => s.setGroupShowSize)
  const sidebarMenuVisible = useAppStore((s) => s.sidebarMenuVisible)
  const setSidebarMenuVisible = useAppStore((s) => s.setSidebarMenuVisible)
  const statusFilterVisible = useAppStore((s) => s.statusFilterVisible)
  const setStatusFilterVisible = useAppStore((s) => s.setStatusFilterVisible)
  const enableDoubleClickSelect = useAppStore((s) => s.enableDoubleClickSelect)
  const setEnableDoubleClickSelect = useAppStore((s) => s.setEnableDoubleClickSelect)
  const selectedIds = useAppStore((s) => s.selectedIds)
  const setSelection = useAppStore((s) => s.setSelection)
  const clearSelection = useAppStore((s) => s.clearSelection)
  const sidebarWidth = useAppStore((s) => s.sidebarWidth)
  const setSidebarWidth = useAppStore((s) => s.setSidebarWidth)
  const sidebarCollapsed = useAppStore((s) => s.sidebarCollapsed)
  const setSidebarCollapsed = useAppStore((s) => s.setSidebarCollapsed)
  const [ctxMenu, setCtxMenu] = useState<{ x: number; y: number } | null>(null)
  const [submenuOpen, setSubmenuOpen] = useState(false)
  // 分组项右键：站点 / 标签 → 快速新建组内总限速规则
  const [groupCtx, setGroupCtx] = useState<{ x: number; y: number; kind: 'site' | 'label'; value: string } | null>(null)
  // 分组限速依赖 honorsSessionLimits：qBittorrent 的单种限速是绝对值、没有
  // 「遵循全局限速」开关，引擎无法区分自己写入的值和用户手动限速，会误覆盖，
  // 故该能力缺失时整个入口隐藏（后端 Tick 同样会跳过）
  const groupLimitSupported = useAppStore((s) => s.session?.caps?.honorsSessionLimits !== false)
  const groupLimit = (kind: 'site' | 'label', value: string) => {
    setGroupCtx(null)
    useAppStore.getState().openSpeedPolicy(kind === 'site' ? { sites: [value] } : { labels: [value] })
  }
  // 子菜单延迟关闭：光标短暂离开一级菜单（含 4px 间隙）时不闪退
  const submenuTimer = useRef<number | null>(null)
  const closeSubmenuSoon = () => {
    if (submenuTimer.current) window.clearTimeout(submenuTimer.current)
    submenuTimer.current = window.setTimeout(() => setSubmenuOpen(false), 150)
  }
  const cancelSubmenuClose = () => {
    if (submenuTimer.current) {
      window.clearTimeout(submenuTimer.current)
      submenuTimer.current = null
    }
  }
  // 卸载时清理待执行的关闭定时器，避免对已卸载组件 setState
  useEffect(() => () => {
    if (submenuTimer.current) window.clearTimeout(submenuTimer.current)
  }, [])

  // 右键菜单只能点外部关闭的话，键盘用户就被困在里面了：至少让 Esc 生效
  useEffect(() => {
    if (!ctxMenu) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      setCtxMenu(null)
      setSubmenuOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [ctxMenu])

  // 下载目录可用空间（DiskRing / 聚合逐台，60s 刷新）
  const disk = useDiskSpaces()

  // 拖拽调整宽度
  const dragRef = useRef<{ startX: number; startWidth: number } | null>(null)
  const onResizeStart = (e: React.MouseEvent) => {
    e.preventDefault()
    dragRef.current = { startX: e.clientX, startWidth: sidebarWidth }
    const onMove = (ev: MouseEvent) => {
      if (!dragRef.current) return
      const w = Math.min(400, Math.max(200, dragRef.current.startWidth + ev.clientX - dragRef.current.startX))
      setSidebarWidth(w)
    }
    const onUp = () => {
      dragRef.current = null
      document.removeEventListener('mousemove', onMove)
      document.removeEventListener('mouseup', onUp)
    }
    document.addEventListener('mousemove', onMove)
    document.addEventListener('mouseup', onUp)
  }

  const activeStatus = filters.status[0] || 'all'
  // 滑动玻璃胶囊：替代逐项换底色
  const visibleStatusCount = STATUS_ITEMS.filter(
    (item) => item.key === 'all' || statusFilterVisible[item.key] !== false,
  ).length
  const rail = useNavRail(activeStatus, `${visibleStatusCount}:${groupShowSize}`)

  // 实时速度汇总
  const totals = useMemo(() => {
    let down = 0
    let up = 0
    let downloadedEver = 0
    let uploadedEver = 0
    for (const tr of torrents) {
      down += tr.rateDownload
      up += tr.rateUpload
      downloadedEver += tr.downloadedEver
      uploadedEver += tr.uploadedEver
    }
    return { down, up, downloadedEver, uploadedEver }
  }, [torrents])

  const statusCounts = useMemo(() => {
    const counts: Record<string, number> = { all: torrents.length }
    const sizes: Record<string, number> = { all: 0 }
    for (const tr of torrents) {
      sizes.all += tr.totalSize || 0
      for (const item of STATUS_ITEMS) {
        if (item.key !== 'all' && matchesStatus(tr, item.key)) {
          counts[item.key] = (counts[item.key] ?? 0) + 1
          sizes[item.key] = (sizes[item.key] ?? 0) + (tr.totalSize || 0)
        }
      }
    }
    return { counts, sizes }
  }, [torrents])

  // 标签/目录/错误：条目为 [名称, 数量, 体积]
  const labelCounts = useMemo(() => {
    const m = new Map<string, number>()
    const s = new Map<string, number>()
    for (const tr of torrents) {
      for (const l of tr.labels ?? []) {
        m.set(l, (m.get(l) ?? 0) + 1)
        s.set(l, (s.get(l) ?? 0) + (tr.totalSize || 0))
      }
    }
    return Array.from(m.entries()).sort((a, b) => b[1] - a[1]).map(([k, c]) => [k, c, s.get(k) ?? 0] as const)
  }, [torrents])

  // 「全部」与「无标签」两行的计数（无标签走 __none__ 哨兵值过滤）
  const labelExtra = useMemo(() => {
    let none = 0
    let noneSize = 0
    let totalSize = 0
    for (const tr of torrents) {
      totalSize += tr.totalSize || 0
      if (!tr.labels || tr.labels.length === 0) {
        none++
        noneSize += tr.totalSize || 0
      }
    }
    return { none, noneSize, totalSize }
  }, [torrents])
  const noLabelActive = filters.labels.includes('__none__')

  const dirCounts = useMemo(() => {
    const m = new Map<string, number>()
    const s = new Map<string, number>()
    for (const tr of torrents) {
      if (tr.downloadDir) {
        m.set(tr.downloadDir, (m.get(tr.downloadDir) ?? 0) + 1)
        s.set(tr.downloadDir, (s.get(tr.downloadDir) ?? 0) + (tr.totalSize || 0))
      }
    }
    return Array.from(m.entries()).sort((a, b) => b[1] - a[1]).map(([k, c]) => [k, c, s.get(k) ?? 0] as const)
  }, [torrents])

  // 站点计数 + 「其他」（无站点归属的种子）
  const siteStats = useMemo(() => {
    const counts = new Map<string, number>()
    const sizes = new Map<string, number>()
    let other = 0
    let otherSize = 0
    for (const tr of torrents) {
      const ids = torrentSites[tr.id] ?? []
      if (ids.length === 0) { other++; otherSize += tr.totalSize || 0; continue }
      for (const id of ids) {
        counts.set(id, (counts.get(id) ?? 0) + 1)
        sizes.set(id, (sizes.get(id) ?? 0) + (tr.totalSize || 0))
      }
    }
    return { counts, sizes, other, otherSize }
  }, [torrents, torrentSites])

  // 错误分布（按错误信息分组）
  const errorCounts = useMemo(() => {
    const m = new Map<string, number>()
    const s = new Map<string, number>()
    for (const tr of torrents) {
      if (tr.errorString) {
        m.set(tr.errorString, (m.get(tr.errorString) ?? 0) + 1)
        s.set(tr.errorString, (s.get(tr.errorString) ?? 0) + (tr.totalSize || 0))
      }
    }
    return Array.from(m.entries()).sort((a, b) => b[1] - a[1]).map(([k, c]) => [k, c, s.get(k) ?? 0] as const)
  }, [torrents])

  // 下载器分布：多服务器聚合时每颗种子带 serverName，按它分组。
  // 至少出现 2 台才显示这一组——只有一台时它是个恒等比筛选，点了等于没点，
  // 白占侧栏空间（与「聚合才显示」的产品预期一致）。
  // 按名字而非索引聚合：索引会随服务器增删重排，而筛选条件要持久化。
  const serverCounts = useMemo(() => {
    const m = new Map<string, { count: number; size: number; kind?: DownloaderKind }>()
    for (const tr of torrents) {
      const name = tr.serverName
      if (!name) continue
      const cur = m.get(name)
      if (cur) {
        cur.count++
        cur.size += tr.totalSize || 0
      } else {
        m.set(name, { count: 1, size: tr.totalSize || 0, kind: tr.kind })
      }
    }
    if (m.size < 2) return []
    return Array.from(m.entries())
      .sort((a, b) => b[1].count - a[1].count)
      .map(([name, v]) => [name, v.count, v.size, v.kind] as const)
  }, [torrents])

  // 双击分组项：全选该分组种子（开关开启时；已完整选中该分组则清空）
  const dblSelect = (ids: number[]) => {
    if (!enableDoubleClickSelect || ids.length === 0) return
    // 必须「选区与分组完全相等」才算已全选，
    // 否则仅选中组内 1 个种子时双击会被误判为已全选而清空选区
    const group = new Set(ids)
    const allInGroup =
      selectedIds.length === ids.length && selectedIds.every((id) => group.has(id))
    if (allInGroup) clearSelection()
    else setSelection(ids)
  }

  const onSidebarContextMenu = (e: React.MouseEvent) => {
    const target = e.target as HTMLElement
    if (!target.closest('button, input, .tm-nav-item, a')) {
      e.preventDefault()
      cancelSubmenuClose()
      setSubmenuOpen(false)
      setCtxMenu({ x: e.clientX, y: e.clientY })
    }
  }

  const setStatusFilter = (key: string) => setFilters({ status: key === 'all' ? ['all'] : [key] })

  return (
    <aside
      className="tm-side flex flex-col"
      style={{ width: sidebarWidth }}
      onContextMenu={onSidebarContextMenu}
    >
      {/* 拖拽手柄 */}
      <div
        className="absolute right-0 top-0 bottom-0 w-1 cursor-col-resize hover:bg-primary/40 transition-colors z-10 rounded-full"
        onMouseDown={onResizeStart}
      />

      <div className="tm-dock glass-panel rounded-dock flex-1 min-h-0 flex flex-col overflow-hidden">
        <div className="tm-scroll tm-scroll--side flex-1 min-h-0 overflow-x-hidden px-3 flex flex-col gap-4">
          {/* LiveStats：大字号实时速度 */}
          <div className="shrink-0 grid grid-cols-2 gap-2">
            <div className="glass-subcard rounded-tile px-3 py-2.5 text-center">
              <div className="flex items-center justify-center gap-1 text-caption1 text-gray-500 dark:text-gray-400 mb-0.5">
                <ArrowDown className="w-3.5 h-3.5 text-green-500" />
                {t('sidebar.download')}
              </div>
              <div className="tm-mono text-title2 font-semibold leading-tight text-green-500">{formatSpeed(totals.down)}</div>
            </div>
            <div className="glass-subcard rounded-tile px-3 py-2.5 text-center">
              <div className="flex items-center justify-center gap-1 text-caption1 text-gray-500 dark:text-gray-400 mb-0.5">
                <ArrowUp className="w-3.5 h-3.5 text-blue-500" />
                {t('sidebar.upload')}
              </div>
              <div className="tm-mono text-title2 font-semibold leading-tight text-blue-500">{formatSpeed(totals.up)}</div>
            </div>
            {showStats && (
              <div className="col-span-2 glass-subcard rounded-tile px-3 py-2 flex items-center justify-between text-caption1">
                <span className="text-gray-400">{t('sidebar.down')}</span>
                <span className="tm-mono text-green-600 dark:text-green-400">{formatBytes(totals.downloadedEver)}</span>
                <span className="text-gray-400">{t('sidebar.up')}</span>
                <span className="tm-mono text-blue-600 dark:text-blue-400">{formatBytes(totals.uploadedEver)}</span>
                <span className="text-gray-400">{t('sidebar.ratio')}</span>
                <span className="tm-mono text-orange-500">{formatRatio(totals.downloadedEver > 0 ? totals.uploadedEver / totals.downloadedEver : 0)}</span>
              </div>
            )}
          </div>

          {/* DiskRing：单台环形图 / 聚合逐台列出 */}
          <DiskSpaceCard disk={disk} />

          {/* 过滤器 */}
          {sidebarMenuVisible.status && (
            <div className="shrink-0">
              <SectionHeader icon={<Layers className="w-3.5 h-3.5" />} title={t('nav.filter')} />
              <div ref={rail.boxRef} className="relative flex flex-col gap-0.5 mt-1">
                <span ref={rail.railRef} className="tm-nav-rail" aria-hidden />
                {STATUS_ITEMS.filter((item) => item.key === 'all' || statusFilterVisible[item.key] !== false).map((item) => {
                  const Icon = item.icon
                  const isActive = activeStatus === String(item.key)
                  const count = statusCounts.counts[item.key] ?? 0
                  const size = statusCounts.sizes[item.key] ?? 0
                  return (
                    <button
                      key={item.key}
                      onClick={() => setStatusFilter(item.key)}
                      onDoubleClick={() =>
                        dblSelect(torrents.filter((t) => matchesStatus(t, item.key)).map((t) => t.id))
                      }
                      data-nav-active={isActive ? 'true' : undefined}
                      className={cn(
                        'relative w-full flex items-center gap-2.5 px-2.5 py-2 rounded-tile text-body transition-colors tm-nav-item',
                        isActive
                          ? 'text-primary font-medium'
                          : 'text-gray-600 dark:text-gray-300 hover:bg-white/60 dark:hover:bg-white/8 hover:text-gray-800 dark:hover:text-gray-100',
                      )}
                    >
                      <Icon
                        className={cn(
                          'w-4 h-4 shrink-0',
                          isActive
                            ? 'text-primary'
                            : item.key === 'error' && count > 0
                              ? 'text-red-500'
                              : 'text-gray-500 dark:text-gray-400',
                        )}
                      />
                      <span className="flex-1 text-left truncate">{t(item.label)}</span>
                      {groupShowSize && size > 0 && (
                        <span className="text-caption1 tm-mono text-gray-400 tm-glass-label">{formatBytes(size)}</span>
                      )}
                      <span
                        className={cn(
                          'min-w-5 h-5 px-1.5 rounded-full text-caption2 tm-mono flex items-center justify-center',
                          isActive
                            ? 'bg-primary/15 text-primary'
                            : 'bg-white/70 dark:bg-white/10 text-gray-500 dark:text-gray-400',
                          item.key === 'error' && count > 0 && !isActive && 'bg-red-500 text-white',
                        )}
                      >
                        {count}
                      </span>
                    </button>
                  )
                })}
              </div>
            </div>
          )}

          {/* 数据目录 */}
          {sidebarMenuVisible.dirs && dirCounts.length > 0 && (
            <div className="shrink-0">
              <SectionHeader
                icon={<FolderOpen className="w-3.5 h-3.5" />}
                title={t('nav.dirs')}
                count={dirCounts.length}
                collapsed={sidebarCollapsed.dirs}
                onToggle={() => setSidebarCollapsed({ dirs: !sidebarCollapsed.dirs })}
              />
              {!sidebarCollapsed.dirs && (
                <div className="max-h-36 overflow-y-auto space-y-0.5 mt-1 pr-1">
                  {dirCounts.map(([dir, count, size]) => {
                    const isActive = filters.downloadDirs.includes(dir)
                    return (
                      <button
                        key={dir}
                        onClick={() =>
                          setFilters({
                            downloadDirs: isActive
                              ? filters.downloadDirs.filter((d) => d !== dir)
                              : [...filters.downloadDirs, dir],
                          })
                        }
                        onDoubleClick={() =>
                          dblSelect(torrents.filter((t) => t.downloadDir === dir).map((t) => t.id))
                        }
                        className={cn(
                          'w-full flex items-center gap-2 px-2.5 py-1.5 rounded-lg text-body transition-colors tm-nav-item',
                          isActive
                            ? 'tm-nav-active text-primary font-medium'
                            : 'text-gray-600 dark:text-gray-300 hover:bg-white/60 dark:hover:bg-white/8',
                        )}
                        title={dir}
                      >
                        <FolderOpen className="w-3.5 h-3.5 shrink-0 opacity-70" />
                        <span className="truncate flex-1 text-left">{sem(dir)}</span>
                        {groupShowSize && size > 0 && (
                          <span className="text-caption2 tm-mono text-gray-400">{formatBytes(size)}</span>
                        )}
                        <span className="text-caption2 tm-mono text-gray-400">{count}</span>
                      </button>
                    )
                  })}
                </div>
              )}
            </div>
          )}

          {/* 标签（彩色徽标） */}
          {sidebarMenuVisible.labels && labelCounts.length > 0 && (
            <div className="shrink-0">
              <SectionHeader
                icon={<Tags className="w-3.5 h-3.5" />}
                title={t('nav.labels')}
                count={labelCounts.length}
                collapsed={sidebarCollapsed.labels}
                onToggle={() => setSidebarCollapsed({ labels: !sidebarCollapsed.labels })}
              />
              {!sidebarCollapsed.labels && (
                <div className="flex flex-wrap gap-1.5 mt-1.5">
                  <button
                    onClick={() => setFilters({ labels: [] })}
                    onDoubleClick={() => dblSelect(torrents.map((t) => t.id))}
                    className={cn(
                      'tm-chip inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full border text-body font-medium transition-all tm-nav-item',
                      filters.labels.length === 0 ? 'scale-105' : 'opacity-90 hover:opacity-100',
                    )}
                    data-active={filters.labels.length === 0 || undefined}
                    style={cssVars({ '--chip': '#9ca3af' })}
                  >
                    {filters.labels.length === 0 && <Check className="w-3 h-3 shrink-0" strokeWidth={2.6} />}
                    {t('nav.all')}
                    {groupShowSize && labelExtra.totalSize > 0 && (
                      <span className="tm-mono text-caption2 opacity-70">{formatBytes(labelExtra.totalSize)}</span>
                    )}
                    <span className="tm-mono text-caption2 opacity-70">{torrents.length}</span>
                  </button>
                  {labelExtra.none > 0 && (
                    <button
                      onClick={() =>
                        setFilters({
                          labels: noLabelActive
                            ? filters.labels.filter((l) => l !== '__none__')
                            : [...filters.labels, '__none__'],
                        })
                      }
                      onDoubleClick={() =>
                        dblSelect(torrents.filter((t) => !t.labels || t.labels.length === 0).map((t) => t.id))
                      }
                      className={cn(
                        'tm-chip inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full border text-body font-medium transition-all tm-nav-item',
                        noLabelActive ? 'scale-105' : 'opacity-90 hover:opacity-100',
                      )}
                      data-active={noLabelActive || undefined}
                      style={cssVars({ '--chip': '#9ca3af' })}
                    >
                      {noLabelActive && <Check className="w-3 h-3 shrink-0" strokeWidth={2.6} />}
                      {t('nav.noLabel')}
                      {groupShowSize && labelExtra.noneSize > 0 && (
                        <span className="tm-mono text-caption2 opacity-70">{formatBytes(labelExtra.noneSize)}</span>
                      )}
                      <span className="tm-mono text-caption2 opacity-70">{labelExtra.none}</span>
                    </button>
                  )}
                  {labelCounts.map(([label, count, size]) => {
                    const isActive = filters.labels.includes(label)
                    const color = tagColor(label)
                    return (
                      <button
                        key={label}
                        onClick={() =>
                          setFilters({ labels: isActive ? filters.labels.filter((l) => l !== label) : [...filters.labels, label] })
                        }
                        onDoubleClick={() =>
                          dblSelect(torrents.filter((t) => t.labels?.includes(label)).map((t) => t.id))
                        }
                        onContextMenu={(e) => {
                          e.preventDefault()
                          e.stopPropagation()
                          setGroupCtx({ x: e.clientX, y: e.clientY, kind: 'label', value: label })
                        }}
                        className={cn(
                          'tm-chip inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full border text-body font-medium transition-all tm-nav-item',
                          isActive ? 'scale-105' : 'opacity-90 hover:opacity-100',
                        )}
                        data-active={isActive || undefined}
                        style={cssVars({ '--chip': color })}
                      >
                        {isActive && <Check className="w-3 h-3 shrink-0" strokeWidth={2.6} />}
                        <span className="truncate">{label}</span>
                        {/* 体积与计数降为「次要信息」：此前两者与标签名同为 opacity-70
                            且字号只差一档，一个 chip 里三组数字读起来没有主次。
                            这里把元信息统一收进一个低对比度分组，并用分隔点断开 */}
                        {(groupShowSize && size > 0) || count > 0 ? (
                          <span className="flex items-center gap-1 shrink-0 text-caption2 opacity-60">
                            {groupShowSize && size > 0 && <span className="tm-mono">{formatBytes(size)}</span>}
                            {groupShowSize && size > 0 && count > 0 && <span aria-hidden>·</span>}
                            {count > 0 && <span className="tm-mono">{count}</span>}
                          </span>
                        ) : null}
                      </button>
                    )
                  })}
                </div>
              )}
            </div>
          )}

          {/* 错误分布 */}
          {sidebarMenuVisible.error && errorCounts.length > 0 && (
            <div className="shrink-0">
              <SectionHeader
                icon={<ShieldAlert className="w-3.5 h-3.5" />}
                title={t('nav.errors')}
                count={errorCounts.length}
                collapsed={sidebarCollapsed.error}
                onToggle={() => setSidebarCollapsed({ error: !sidebarCollapsed.error })}
              />
              {!sidebarCollapsed.error && (
                <div className="max-h-32 overflow-y-auto space-y-0.5 mt-1 pr-1">
                  {errorCounts.map(([err, count, size]) => {
                    const isActive = filters.error.includes(err)
                    return (
                      <button
                        key={err}
                        onClick={() =>
                          setFilters({
                            error: isActive ? filters.error.filter((x) => x !== err) : [...filters.error, err],
                          })
                        }
                        onDoubleClick={() =>
                          dblSelect(torrents.filter((t) => t.errorString === err).map((t) => t.id))
                        }
                        className={cn(
                          'w-full flex items-center gap-2 px-2.5 py-1.5 rounded-lg text-body transition-colors tm-nav-item',
                          isActive
                            ? 'tm-nav-active text-red-600 dark:text-red-400 font-medium'
                            : 'text-gray-600 dark:text-gray-300 hover:bg-white/60 dark:hover:bg-white/8',
                        )}
                        style={ERROR_TINT}
                        title={translateError(err, t)}
                      >
                        <ShieldAlert className="w-3.5 h-3.5 shrink-0 opacity-70 text-red-500" />
                        <span className="truncate flex-1 text-left">{translateError(err, t)}</span>
                        {groupShowSize && size > 0 && (
                          <span className="text-caption2 tm-mono text-gray-400">{formatBytes(size)}</span>
                        )}
                        <span className="text-caption2 tm-mono text-gray-400">{count}</span>
                      </button>
                    )
                  })}
                </div>
              )}
            </div>
          )}

          {/* 下载器分布（多服务器聚合）：回答「这些种子分别在哪个下载器上」 */}
          {sidebarMenuVisible.servers && serverCounts.length > 0 && (
            <div className="shrink-0">
              <SectionHeader
                icon={<Server className="w-3.5 h-3.5" />}
                title={t('nav.servers')}
                count={serverCounts.length}
                collapsed={sidebarCollapsed.servers}
                onToggle={() => setSidebarCollapsed({ servers: !sidebarCollapsed.servers })}
              />
              {!sidebarCollapsed.servers && (
                <div className="max-h-36 overflow-y-auto space-y-0.5 mt-1 pr-1">
                  <button
                    onClick={() => setFilters({ servers: [] })}
                    onDoubleClick={() => dblSelect(torrents.map((t) => t.id))}
                    className={cn(
                      'w-full flex items-center gap-2 px-2.5 py-1.5 rounded-lg text-body transition-colors tm-nav-item',
                      filters.servers.length === 0
                        ? 'tm-nav-active text-primary font-medium'
                        : 'text-gray-600 dark:text-gray-300 hover:bg-white/60 dark:hover:bg-white/8',
                    )}
                  >
                    <Layers className="w-3.5 h-3.5 shrink-0 opacity-70" />
                    <span className="truncate flex-1 text-left">{t('nav.all')}</span>
                    <span className="text-caption2 tm-mono text-gray-400">{torrents.length}</span>
                  </button>
                  {serverCounts.map(([name, count, size, kind]) => {
                    const isActive = filters.servers.includes(name)
                    return (
                      <button
                        key={name}
                        onClick={() =>
                          setFilters({
                            servers: isActive
                              ? filters.servers.filter((s) => s !== name)
                              : [...filters.servers, name],
                          })
                        }
                        onDoubleClick={() =>
                          dblSelect(torrents.filter((t) => t.serverName === name).map((t) => t.id))
                        }
                        className={cn(
                          'w-full flex items-center gap-2 px-2.5 py-1.5 rounded-lg text-body transition-colors tm-nav-item',
                          isActive
                            ? 'tm-nav-active text-primary font-medium'
                            : 'text-gray-600 dark:text-gray-300 hover:bg-white/60 dark:hover:bg-white/8',
                        )}
                        title={name}
                      >
                        {/* TR / QB 徽标：同名下载器并列时靠它一眼分辨类型 */}
                        <KindBadge kind={kind} />
                        <span className="truncate flex-1 text-left">{name}</span>
                        {groupShowSize && size > 0 && (
                          <span className="text-caption2 tm-mono text-gray-400">{formatBytes(size)}</span>
                        )}
                        <span className="text-caption2 tm-mono text-gray-400">{count}</span>
                      </button>
                    )
                  })}
                </div>
              )}
            </div>
          )}

          {/* 站点 */}
          {sidebarMenuVisible.sites && (
            // shrink-0 与其他分组同口径：作为普通块参与侧栏整体滚动。
            // 不能用 flex-1 min-h-0——空间不足时容器被压缩，而列表仍固定 max-h，
            // 溢出部分会被停靠面板 overflow-hidden 裁掉，表现为“滚不动、看不到站点”
            <div className="shrink-0">
              <SiteNav
                sites={torrentSites}
                siteStats={siteStats}
                currentSiteIds={filters.sites}
                onSelect={(siteId) => setFilters({ sites: siteId ? [siteId] : [] })}
                onDblSelect={(siteId) =>
                  dblSelect(
                    // '' = 全站（"全部站点"行），'__other__' = 无站点归属
                    siteId === ''
                      ? torrents.map((t) => t.id)
                      : torrents
                        .filter((t) => {
                          const ids = torrentSites[t.id] ?? []
                          return siteId === '__other__' ? ids.length === 0 : ids.includes(siteId)
                        })
                        .map((t) => t.id),
                  )
                }
                groupShowSize={groupShowSize}
                collapsed={sidebarCollapsed.sites}
                onToggle={() => setSidebarCollapsed({ sites: !sidebarCollapsed.sites })}
                onGroupMenu={(name, x, y) => setGroupCtx({ x, y, kind: 'site', value: name })}
              />
            </div>
          )}
        </div>

      </div>

      {/* 右键菜单 */}
      {ctxMenu && (
        <SidebarCtxMenu
          pos={ctxMenu}
          onClose={() => { setCtxMenu(null); setSubmenuOpen(false) }}
          submenuOpen={submenuOpen}
          onSubmenuEnter={() => { cancelSubmenuClose(); setSubmenuOpen(true) }}
          onSubmenuLeave={closeSubmenuSoon}
          statusFilterVisible={statusFilterVisible}
          setStatusFilterVisible={setStatusFilterVisible}
          sidebarMenuVisible={sidebarMenuVisible}
          setSidebarMenuVisible={setSidebarMenuVisible}
          enableDoubleClickSelect={enableDoubleClickSelect}
          setEnableDoubleClickSelect={setEnableDoubleClickSelect}
          groupShowSize={groupShowSize}
          setGroupShowSize={setGroupShowSize}
          showStats={showStats}
          setShowStats={setShowStats}
        />
      )}

      {/* 站点 / 标签分组项右键：快速新建组内总限速规则 */}
      {groupCtx && groupLimitSupported && (
        <GroupCtxMenu pos={groupCtx} onClose={() => setGroupCtx(null)} onGroupLimit={groupLimit} />
      )}
    </aside>
  )
}

// ========== 右键菜单勾选项 ==========
function CtxCheck({ label, checked, onClick }: { label: string; checked: boolean; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      role="menuitemcheckbox"
      aria-checked={checked}
      className="w-full flex items-center gap-2 px-3 py-1.5 rounded-md text-footnote text-left hover:bg-white/60 dark:hover:bg-white/10"
    >
      <span className="w-3.5 shrink-0 flex justify-center text-primary">{checked && <Check className="w-3.5 h-3.5" strokeWidth={2.4} />}</span>
      {label}
    </button>
  )
}

// ========== 分组标题 ==========
function SectionHeader({
  icon,
  title,
  count,
  collapsed,
  onToggle,
}: {
  icon: React.ReactNode
  title: string
  count?: number
  collapsed?: boolean
  onToggle?: () => void
}) {
  const inner = (
    <>
      {onToggle && (
        <ChevronDown className={cn('w-3 h-3 text-gray-400 transition-transform duration-200', collapsed && '-rotate-90')} />
      )}
      <span className="text-primary">{icon}</span>
      <span className="text-body font-semibold uppercase tracking-wider">{title}</span>
      {typeof count === 'number' && <span className="text-caption2 tm-mono text-gray-400 ml-auto">{count}</span>}
    </>
  )
  const cls = 'flex items-center gap-1.5 px-1 select-none text-gray-500 dark:text-gray-400'

  // 可折叠的标题是一个真按钮：div + onClick 拿不到键盘焦点，也读不出展开状态
  if (!onToggle) return <div className={cls}>{inner}</div>
  return (
    <button
      type="button"
      onClick={onToggle}
      aria-expanded={!collapsed}
      className={cn(
        cls,
        'w-full rounded-md hover:text-gray-700 dark:hover:text-gray-200 transition-colors',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/60',
      )}
    >
      {inner}
    </button>
  )
}

// ========== 站点分组导航 ==========
interface SiteNavProps {
  sites: Record<number, string[]>
  siteStats: { counts: Map<string, number>; sizes: Map<string, number>; other: number; otherSize: number }
  currentSiteIds: string[]
  onSelect: (siteId: string) => void
  onDblSelect: (siteId: string) => void
  groupShowSize: boolean
  collapsed: boolean
  onToggle: () => void
  // 站点行右键 → 新建组内总限速（name 为站点名，x/y 为菜单落点）
  onGroupMenu?: (name: string, x: number, y: number) => void
}

const SiteNav: React.FC<SiteNavProps> = ({ sites, siteStats, currentSiteIds, onSelect, onDblSelect, groupShowSize, collapsed, onToggle, onGroupMenu }) => {
  const { t } = useTranslation()
  const [search, setSearch] = useState('')
  // 分组限速入口：当前下载器不支持「遵循全局限速」时隐藏（见 Sidebar 顶部的同名判断）
  const groupLimitSupported = useAppStore((s) => s.session?.caps?.honorsSessionLimits !== false)

  // sites 的语义是「种子 ID → 该种子的站点名数组」，
  // 这里需按站点名聚合去重，否则同一站点会按种子数重复成多行，
  // 且拿种子 ID 去查以站点名为 key 的 siteStats 会恒为 0、筛选也永不命中
  const siteEntries = useMemo(() => {
    const names = new Set<string>()
    for (const list of Object.values(sites)) {
      for (const n of list) if (n) names.add(n)
    }
    return Array.from(names).sort().map((name) => ({ id: name, name }))
  }, [sites])

  // 全站合计（供"全部站点"行展示，避免误用"其他"分组的计数）
  const { totalSiteCount, totalSiteSize } = useMemo(() => {
    let count = siteStats.other
    let size = siteStats.otherSize
    for (const c of siteStats.counts.values()) count += c
    for (const s of siteStats.sizes.values()) size += s
    return { totalSiteCount: count, totalSiteSize: size }
  }, [siteStats])

  const filtered = siteEntries.filter((s) =>
    s.name.toLowerCase().includes(search.toLowerCase()),
  )

  return (
    <>
      <SectionHeader
        icon={<Server className="w-3.5 h-3.5" />}
        title={t('site.nav')}
        count={siteEntries.length}
        collapsed={collapsed}
        onToggle={onToggle}
      />

      {!collapsed && (
        <>
          <div className="relative mt-1 mb-1.5">
            <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-gray-400" />
            <Input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              aria-label={t('site.searchAria')}
              placeholder={t('common.search')}
              className="h-8 pl-8 text-footnote bg-white/50 dark:bg-white/5 border-white/60 dark:border-white/10 rounded-lg focus-visible:ring-primary/50"
            />
          </div>

          <div className="space-y-0.5 max-h-44 overflow-y-auto pr-1">
            {/* 全部站点：点击清空站点筛选，双击全选所有种子；计数为全站合计（不是"其他"分组的） */}
            <button
              onClick={() => onSelect('')}
              onDoubleClick={() => onDblSelect('')}
              className={cn(
                'w-full flex items-center gap-2 px-2.5 py-1.5 rounded-lg text-body transition-colors tm-nav-item',
                currentSiteIds.length === 0
                  ? 'tm-nav-active text-primary font-medium'
                  : 'text-gray-600 dark:text-gray-300 hover:bg-white/60 dark:hover:bg-white/8',
              )}
            >
              <Globe className="w-3.5 h-3.5 shrink-0" />
              <span className="truncate flex-1 text-left">{t('site.all')}</span>
              {groupShowSize && totalSiteSize > 0 && (
                <span className="text-caption2 tm-mono text-gray-400">{formatBytes(totalSiteSize)}</span>
              )}
              <span className="text-caption2 tm-mono text-gray-400">{totalSiteCount}</span>
            </button>

            {filtered.map((site) => {
              const isActive = currentSiteIds.includes(String(site.id))
              const count = siteStats.counts.get(site.id) ?? 0
              const size = siteStats.sizes.get(site.id) ?? 0
              return (
                <button
                  key={site.id}
                  onClick={() => onSelect(String(site.id))}
                  onDoubleClick={() => onDblSelect(String(site.id))}
                  onContextMenu={(e) => {
                    if (!onGroupMenu) return
                    e.preventDefault()
                    e.stopPropagation()
                    onGroupMenu(String(site.id), e.clientX, e.clientY)
                  }}
                  className={cn(
                    'w-full flex items-center gap-2 px-2.5 py-1.5 rounded-lg text-body transition-colors tm-nav-item',
                    isActive
                      ? 'tm-nav-active text-primary font-medium'
                      : 'text-gray-600 dark:text-gray-300 hover:bg-white/60 dark:hover:bg-white/8',
                  )}
                >
                  <span
                    className="w-2 h-2 rounded-full shrink-0"
                    style={{ backgroundColor: isActive ? 'var(--color-primary)' : 'var(--color-gray-300)' }}
                  />
                  <span className="truncate flex-1 text-left">{site.name}</span>
                  {groupShowSize && size > 0 && (
                    <span className="text-caption2 tm-mono text-gray-400">{formatBytes(size)}</span>
                  )}
                  <span className="text-caption2 tm-mono text-gray-400">{count}</span>
                </button>
              )
            })}

            {/* 其他（无站点归属） */}
            {siteStats.other > 0 && (
              <button
                onClick={() => onSelect('__other__')}
                onDoubleClick={() => onDblSelect('__other__')}
                className={cn(
                  'w-full flex items-center gap-2 px-2.5 py-1.5 rounded-lg text-body transition-colors tm-nav-item',
                  currentSiteIds.includes('__other__')
                    ? 'tm-nav-active text-primary font-medium'
                    : 'text-gray-600 dark:text-gray-300 hover:bg-white/60 dark:hover:bg-white/8',
                )}
              >
                <span className="w-2 h-2 rounded-full shrink-0 bg-gray-300" />
                <span className="truncate flex-1 text-left">{t('site.other')}</span>
                {groupShowSize && siteStats.otherSize > 0 && (
                  <span className="text-caption2 tm-mono text-gray-400">{formatBytes(siteStats.otherSize)}</span>
                )}
                <span className="text-caption2 tm-mono text-gray-400">{siteStats.other}</span>
              </button>
            )}

            {filtered.length === 0 && search && (
              <div className="text-footnote text-gray-400 text-center py-4">{t('common.noResults')}</div>
            )}
          </div>
        </>
      )}
    </>
  )
}

// ========== 移动端抽屉 ==========
interface MobileDrawerProps {
  visible: boolean
  onClose: () => void
  onOpenSettings: () => void
}

export const MobileDrawer: React.FC<MobileDrawerProps> = ({ visible, onClose, onOpenSettings }) => {
  const { t } = useTranslation()
  const { can } = usePlatform()
  const sem = useSemanticPath()
  const filters = useAppStore((s) => s.filters)
  const setFilters = useAppStore((s) => s.setFilters)
  const torrentSites = useAppStore((s) => s.torrentSites)
  const torrents = useAppStore((s) => s.torrents)
  const sidebarCollapsed = useAppStore((s) => s.sidebarCollapsed)
  const setSidebarCollapsed = useAppStore((s) => s.setSidebarCollapsed)
  const statusFilterVisible = useAppStore((s) => s.statusFilterVisible)
  // 与桌面端一致：遵守右键菜单里的分组显隐设置（该设置是持久化的）
  const sidebarMenuVisible = useAppStore((s) => s.sidebarMenuVisible)
  const selectedIds = useAppStore((s) => s.selectedIds)
  const setSelection = useAppStore((s) => s.setSelection)
  const clearSelection = useAppStore((s) => s.clearSelection)
  // 分组限速入口：当前下载器不支持「遵循全局限速」时隐藏（与桌面端 Sidebar 一致）
  const groupLimitSupported = useAppStore((s) => s.session?.caps?.honorsSessionLimits !== false)

  // 下载目录可用空间（DiskRing / 聚合逐台）：抽屉未打开时不轮询，避免后台空转
  const disk = useDiskSpaces(visible)

  const dirCounts = useMemo(() => {
    const m = new Map<string, number>()
    for (const tr of torrents) if (tr.downloadDir) m.set(tr.downloadDir, (m.get(tr.downloadDir) ?? 0) + 1)
    return Array.from(m.entries()).sort((a, b) => b[1] - a[1])
  }, [torrents])

  const totals = useMemo(() => {
    let down = 0
    let up = 0
    for (const tr of torrents) {
      down += tr.rateDownload
      up += tr.rateUpload
    }
    return { down, up }
  }, [torrents])

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

  const labelCounts = useMemo(() => {
    const m = new Map<string, number>()
    for (const tr of torrents) for (const l of tr.labels ?? []) m.set(l, (m.get(l) ?? 0) + 1)
    return Array.from(m.entries()).sort((a, b) => b[1] - a[1])
  }, [torrents])

  // 「全部」与「无标签」徽标用（无标签走 __none__ 哨兵值过滤）
  const noLabelCount = useMemo(
    () => torrents.reduce((n, tr) => n + (!tr.labels || tr.labels.length === 0 ? 1 : 0), 0),
    [torrents],
  )

  // 错误分布（按错误信息分组，次数降序）
  const errorCounts = useMemo(() => {
    const m = new Map<string, number>()
    for (const tr of torrents) if (tr.errorString) m.set(tr.errorString, (m.get(tr.errorString) ?? 0) + 1)
    return Array.from(m.entries()).sort((a, b) => b[1] - a[1])
  }, [torrents])

  // 下载器分布（多服务器聚合）：至少 2 台才显示，与桌面端同口径
  const serverCounts = useMemo(() => {
    const m = new Map<string, { count: number; kind?: DownloaderKind }>()
    for (const tr of torrents) {
      const name = tr.serverName
      if (!name) continue
      const cur = m.get(name)
      if (cur) cur.count++
      else m.set(name, { count: 1, kind: tr.kind })
    }
    if (m.size < 2) return []
    return Array.from(m.entries())
      .sort((a, b) => b[1].count - a[1].count)
      .map(([name, v]) => [name, v.count, v.kind] as const)
  }, [torrents])

  const siteStats = useMemo(() => {
    const counts = new Map<string, number>()
    let other = 0
    for (const tr of torrents) {
      const ids = torrentSites[tr.id] ?? []
      if (ids.length === 0) { other++; continue }
      for (const id of ids) counts.set(id, (counts.get(id) ?? 0) + 1)
    }
    return { counts, other }
  }, [torrents, torrentSites])

  // 站点名聚合去重（torrentSites 的 key 是种子 ID，不能当站点 ID 用）
  const mobileSiteNames = useMemo(() => {
    const names = new Set<string>()
    for (const list of Object.values(torrentSites)) {
      for (const n of list) if (n) names.add(n)
    }
    return Array.from(names).sort()
  }, [torrentSites])

  const mobileSiteTotal = useMemo(
    () => siteStats.other + Array.from(siteStats.counts.values()).reduce((a, b) => a + b, 0),
    [siteStats],
  )

  const activeStatus = filters.status[0] || 'all'

  // 触屏没有双击 / 右键：长按分组行补齐「全选该组」与「为此分组添加限速规则」，
  // 手势参数与 TorrentMenu.ContextMenuAnchor 相同（500ms、10px 抖动容差），
  // Android 原生 contextmenu 与自实现长按去重，松手后的补发 click 吞掉（否则抽屉会先关闭）
  const [groupMenu, setGroupMenu] = useState<{
    pos: { x: number; y: number }
    ids: number[]
    scope: { kind: 'site' | 'label'; value: string } | null
  } | null>(null)
  const lp = useRef<{ timer: number | null; start: { x: number; y: number } | null; firedAt: number }>({
    timer: null,
    start: null,
    firedAt: 0,
  })
  useEffect(
    () => () => {
      if (lp.current.timer) window.clearTimeout(lp.current.timer)
    },
    [],
  )
  const lpCancel = () => {
    if (lp.current.timer) {
      window.clearTimeout(lp.current.timer)
      lp.current.timer = null
    }
    lp.current.start = null
  }
  const longPressProps = (onLongPress: (x: number, y: number) => void) => ({
    onContextMenu: (e: React.MouseEvent) => {
      e.preventDefault()
      lpCancel()
      if (Date.now() - lp.current.firedAt < 800) return
      onLongPress(e.clientX, e.clientY)
    },
    onPointerDown: (e: React.PointerEvent) => {
      if (e.pointerType === 'mouse' || e.button !== 0) return
      lpCancel()
      lp.current.start = { x: e.clientX, y: e.clientY }
      const { clientX, clientY } = e
      lp.current.timer = window.setTimeout(() => {
        lp.current.timer = null
        lp.current.start = null
        lp.current.firedAt = Date.now()
        onLongPress(clientX, clientY)
      }, 500)
    },
    onPointerMove: (e: React.PointerEvent) => {
      const from = lp.current.start
      if (!lp.current.timer || !from) return
      if (Math.abs(e.clientX - from.x) > 10 || Math.abs(e.clientY - from.y) > 10) lpCancel()
    },
    onPointerUp: lpCancel,
    onPointerCancel: lpCancel,
    onClickCapture: (e: React.MouseEvent) => {
      if (Date.now() - lp.current.firedAt < 800) {
        e.preventDefault()
        e.stopPropagation()
      }
    },
  })
  const openGroupMenu = (
    pos: { x: number; y: number },
    ids: number[],
    scope: { kind: 'site' | 'label'; value: string } | null,
  ) => {
    if (ids.length === 0) return
    setGroupMenu({ pos, ids, scope })
  }
  const pickGroupMenu = (key: string) => {
    const menu = groupMenu
    setGroupMenu(null)
    if (!menu) return
    if (key === 'select') {
      // 与桌面双击全选同语义：选区与分组完全相等才算已全选，此时再按一次是清空
      const group = new Set(menu.ids)
      const allIn =
        selectedIds.length === menu.ids.length && selectedIds.every((id) => group.has(id))
      if (allIn) clearSelection()
      else setSelection(menu.ids)
      onClose()
    } else if (key === 'limit' && menu.scope) {
      useAppStore
        .getState()
        .openSpeedPolicy(
          menu.scope.kind === 'site' ? { sites: [menu.scope.value] } : { labels: [menu.scope.value] },
        )
      onClose()
    }
  }

  return (
    <Sheet open={visible} onOpenChange={onClose}>
      <SheetContent className="h-[78dvh] sm:max-w-sm flex flex-col" onDismiss={onClose}>
        <SheetHeader className="border-b border-white/60 dark:border-white/10 pb-2">
          <SheetTitle className="flex items-center gap-2">
            <span className="w-7 h-7 rounded-full bg-gradient-to-br from-[var(--brand-grad-from)] to-[var(--brand-grad-to)] flex items-center justify-center">
              <span className="text-white font-bold text-footnote">SA</span>
            </span>
            <span className="text-primary">SeedArk</span>
            {can('app.update') && <span className="text-gray-400 dark:text-gray-500 font-medium text-footnote"> for fnOS</span>}
          </SheetTitle>
        </SheetHeader>

        <div className="flex-1 overflow-y-auto space-y-4 py-3">
          {/* LiveStats */}
          <div className="grid grid-cols-2 gap-2">
            <div className="glass-subcard rounded-tile px-3 py-2.5 text-center">
              <div className="flex items-center justify-center gap-1 text-caption1 text-gray-500 dark:text-gray-400 mb-0.5">
                <ArrowDown className="w-3.5 h-3.5 text-green-500" />
                {t('sidebar.download')}
              </div>
              <div className="tm-mono text-title2 font-semibold leading-tight text-green-500">{formatSpeed(totals.down)}</div>
            </div>
            <div className="glass-subcard rounded-tile px-3 py-2.5 text-center">
              <div className="flex items-center justify-center gap-1 text-caption1 text-gray-500 dark:text-gray-400 mb-0.5">
                <ArrowUp className="w-3.5 h-3.5 text-blue-500" />
                {t('sidebar.upload')}
              </div>
              <div className="tm-mono text-title2 font-semibold leading-tight text-blue-500">{formatSpeed(totals.up)}</div>
            </div>
          </div>

          {/* DiskRing：单台环形图 / 聚合逐台列出 */}
          <DiskSpaceCard disk={disk} ringSize={52} />

          {/* 状态筛选 */}
          {sidebarMenuVisible.status && (
          <div>
            <SectionHeader icon={<Layers className="w-3.5 h-3.5" />} title={t('nav.filter')} />
            <div className="space-y-0.5 mt-1">
              {STATUS_ITEMS.filter((item) => item.key === 'all' || statusFilterVisible[item.key] !== false).map((item) => {
                const Icon = item.icon
                const isActive = activeStatus === String(item.key)
                const count = statusCounts[item.key] ?? 0
                return (
                  <button
                    key={item.key}
                    {...longPressProps((x, y) =>
                      openGroupMenu(
                        { x, y },
                        item.key === 'all'
                          ? torrents.map((tr) => tr.id)
                          : torrents.filter((tr) => matchesStatus(tr, item.key)).map((tr) => tr.id),
                        null,
                      ),
                    )}
                    onClick={() => {
                      setFilters({ status: item.key === 'all' ? ['all'] : [item.key] })
                      onClose()
                    }}
                    className={cn(
                      'w-full flex items-center gap-2.5 px-2.5 py-2 rounded-xl text-body transition-all tm-nav-item',
                      isActive
                        ? 'tm-nav-active text-primary font-medium'
                        : 'text-gray-600 dark:text-gray-300 hover:bg-white/60 dark:hover:bg-white/8',
                    )}
                  >
                    <Icon
                      className={cn(
                        'w-4 h-4 shrink-0',
                        isActive ? 'text-primary' : item.key === 'error' && count > 0 ? 'text-red-500' : 'text-gray-500 dark:text-gray-400',
                      )}
                    />
                    <span className="flex-1 text-left truncate">{t(item.label)}</span>
                    <span
                      className={cn(
                        'min-w-5 h-5 px-1.5 rounded-full text-caption2 tm-mono flex items-center justify-center',
                        isActive
                          ? 'bg-primary/15 text-primary'
                          : 'bg-white/70 dark:bg-white/10 text-gray-500 dark:text-gray-400',
                        item.key === 'error' && count > 0 && !isActive && 'bg-red-500 text-white',
                      )}
                    >
                      {count}
                    </span>
                  </button>
                )
              })}
            </div>
          </div>
          )}

          {/* 下载目录 */}
          {sidebarMenuVisible.dirs && dirCounts.length > 0 && (
            <div>
              <SectionHeader
                icon={<FolderOpen className="w-3.5 h-3.5" />}
                title={t('nav.dirs')}
                count={dirCounts.length}
                collapsed={sidebarCollapsed.dirs}
                onToggle={() => setSidebarCollapsed({ dirs: !sidebarCollapsed.dirs })}
              />
              {!sidebarCollapsed.dirs && (
                <div className="space-y-0.5 mt-1">
                  {dirCounts.map(([dir, count]) => {
                    const isActive = filters.downloadDirs.includes(dir)
                    return (
                      <button
                        key={dir}
                        {...longPressProps((x, y) =>
                          openGroupMenu(
                            { x, y },
                            torrents.filter((tr) => tr.downloadDir === dir).map((tr) => tr.id),
                            null,
                          ),
                        )}
                        onClick={() => {
                          setFilters({
                            downloadDirs: isActive
                              ? filters.downloadDirs.filter((d) => d !== dir)
                              : [...filters.downloadDirs, dir],
                          })
                          onClose()
                        }}
                        className={cn(
                          'w-full flex items-center gap-2 px-2.5 py-1.5 rounded-lg text-body transition-colors tm-nav-item',
                          isActive ? 'tm-nav-active text-primary font-medium' : 'text-gray-600 dark:text-gray-300 hover:bg-white/60 dark:hover:bg-white/8',
                        )}
                      >
                        <FolderOpen className="w-3.5 h-3.5 shrink-0 opacity-70" />
                        <span className="truncate flex-1 text-left">{sem(dir)}</span>
                        <span className="text-caption2 tm-mono text-gray-400">{count}</span>
                      </button>
                    )
                  })}
                </div>
              )}
            </div>
          )}

          {/* 错误分布（与桌面端对齐，否则移动端无法错误筛选） */}
          {sidebarMenuVisible.error && errorCounts.length > 0 && (
            <div>
              <SectionHeader
                icon={<AlertCircle className="w-3.5 h-3.5" />}
                title={t('nav.errors')}
                count={errorCounts.length}
                collapsed={sidebarCollapsed.error}
                onToggle={() => setSidebarCollapsed({ error: !sidebarCollapsed.error })}
              />
              {!sidebarCollapsed.error && (
                <div className="space-y-0.5 mt-1">
                  {errorCounts.map(([msg, count]) => {
                    const isActive = filters.error.includes(msg)
                    return (
                      <button
                        key={msg}
                        {...longPressProps((x, y) =>
                          openGroupMenu(
                            { x, y },
                            torrents.filter((tr) => tr.errorString === msg).map((tr) => tr.id),
                            null,
                          ),
                        )}
                        onClick={() => {
                          setFilters({
                            error: isActive ? filters.error.filter((e) => e !== msg) : [...filters.error, msg],
                          })
                          onClose()
                        }}
                        className={cn(
                          'w-full flex items-center gap-2 px-2.5 py-1.5 rounded-lg text-body transition-colors tm-nav-item',
                          isActive ? 'tm-nav-active text-red-600 dark:text-red-400 font-medium' : 'text-gray-600 dark:text-gray-300 hover:bg-white/60 dark:hover:bg-white/8',
                        )}
                        style={ERROR_TINT}
                        title={translateError(msg, t)}
                      >
                        <AlertCircle className="w-3.5 h-3.5 shrink-0 opacity-70" />
                        <span className="truncate flex-1 text-left">{translateError(msg, t)}</span>
                        <span className="text-caption2 tm-mono text-gray-400">{count}</span>
                      </button>
                    )
                  })}
                </div>
              )}
            </div>
          )}

          {/* 下载器分布（多服务器聚合） */}
          {sidebarMenuVisible.servers && serverCounts.length > 0 && (
            <div>
              <SectionHeader
                icon={<Server className="w-3.5 h-3.5" />}
                title={t('nav.servers')}
                count={serverCounts.length}
                collapsed={sidebarCollapsed.servers}
                onToggle={() => setSidebarCollapsed({ servers: !sidebarCollapsed.servers })}
              />
              {!sidebarCollapsed.servers && (
                <div className="space-y-0.5 mt-1">
                  <button
                    {...longPressProps((x, y) => openGroupMenu({ x, y }, torrents.map((tr) => tr.id), null))}
                    onClick={() => { setFilters({ servers: [] }); onClose() }}
                    className={cn(
                      'w-full flex items-center gap-2 px-2.5 py-1.5 rounded-lg text-body transition-colors tm-nav-item',
                      filters.servers.length === 0
                        ? 'tm-nav-active text-primary font-medium'
                        : 'text-gray-600 dark:text-gray-300 hover:bg-white/60 dark:hover:bg-white/8',
                    )}
                  >
                    <Layers className="w-3.5 h-3.5 shrink-0 opacity-70" />
                    <span className="truncate flex-1 text-left">{t('nav.all')}</span>
                    <span className="text-caption2 tm-mono text-gray-400">{torrents.length}</span>
                  </button>
                  {serverCounts.map(([name, count, kind]) => {
                    const isActive = filters.servers.includes(name)
                    return (
                      <button
                        key={name}
                        {...longPressProps((x, y) =>
                          openGroupMenu({ x, y }, torrents.filter((tr) => tr.serverName === name).map((tr) => tr.id), null),
                        )}
                        onClick={() => {
                          setFilters({
                            servers: isActive ? filters.servers.filter((s) => s !== name) : [...filters.servers, name],
                          })
                          onClose()
                        }}
                        className={cn(
                          'w-full flex items-center gap-2 px-2.5 py-1.5 rounded-lg text-body transition-colors tm-nav-item',
                          isActive
                            ? 'tm-nav-active text-primary font-medium'
                            : 'text-gray-600 dark:text-gray-300 hover:bg-white/60 dark:hover:bg-white/8',
                        )}
                        title={name}
                      >
                        <KindBadge kind={kind} />
                        <span className="truncate flex-1 text-left">{name}</span>
                        <span className="text-caption2 tm-mono text-gray-400">{count}</span>
                      </button>
                    )
                  })}
                </div>
              )}
            </div>
          )}

          {/* 标签 */}
          {sidebarMenuVisible.labels && labelCounts.length > 0 && (
            <div>
              <SectionHeader
                icon={<Tags className="w-3.5 h-3.5" />}
                title={t('nav.labels')}
                count={labelCounts.length}
                collapsed={sidebarCollapsed.labels}
                onToggle={() => setSidebarCollapsed({ labels: !sidebarCollapsed.labels })}
              />
              {!sidebarCollapsed.labels && (
                <div className="flex flex-wrap gap-1.5 mt-1.5">
                  <button
                    {...longPressProps((x, y) => openGroupMenu({ x, y }, torrents.map((tr) => tr.id), null))}
                    onClick={() => { setFilters({ labels: [] }); onClose() }}
                    className={cn(
                      'tm-chip inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full border text-body font-medium transition-all tm-nav-item',
                      filters.labels.length === 0 ? 'scale-105' : 'opacity-90 hover:opacity-100',
                    )}
                    data-active={filters.labels.length === 0 || undefined}
                    style={cssVars({ '--chip': '#9ca3af' })}
                  >
                    {filters.labels.length === 0 && <Check className="w-3 h-3 shrink-0" strokeWidth={2.6} />}
                    {t('nav.all')}
                    <span className="tm-mono text-caption2 opacity-70">{torrents.length}</span>
                  </button>
                  {noLabelCount > 0 && (
                    <button
                      {...longPressProps((x, y) =>
                        openGroupMenu(
                          { x, y },
                          torrents.filter((tr) => !tr.labels || tr.labels.length === 0).map((tr) => tr.id),
                          null,
                        ),
                      )}
                      onClick={() => {
                        const isActive = filters.labels.includes('__none__')
                        setFilters({
                          labels: isActive
                            ? filters.labels.filter((l) => l !== '__none__')
                            : [...filters.labels, '__none__'],
                        })
                        onClose()
                      }}
                      className={cn(
                        'tm-chip inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full border text-body font-medium transition-all tm-nav-item',
                        filters.labels.includes('__none__') ? 'scale-105' : 'opacity-90 hover:opacity-100',
                      )}
                      data-active={filters.labels.includes('__none__') || undefined}
                      style={cssVars({ '--chip': '#9ca3af' })}
                    >
                      {filters.labels.includes('__none__') && <Check className="w-3 h-3 shrink-0" strokeWidth={2.6} />}
                      {t('nav.noLabel')}
                      <span className="tm-mono text-caption2 opacity-70">{noLabelCount}</span>
                    </button>
                  )}
                  {labelCounts.map(([label, count]) => {
                    const isActive = filters.labels.includes(label)
                    const color = tagColor(label)
                    return (
                      <button
                        key={label}
                        {...longPressProps((x, y) =>
                          openGroupMenu(
                            { x, y },
                            torrents.filter((tr) => tr.labels?.includes(label)).map((tr) => tr.id),
                            { kind: 'label', value: label },
                          ),
                        )}
                        onClick={() => {
                          setFilters({ labels: isActive ? filters.labels.filter((l) => l !== label) : [...filters.labels, label] })
                          onClose()
                        }}
                        className={cn(
                          'tm-chip inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full border text-body font-medium transition-all tm-nav-item',
                          isActive ? 'scale-105' : 'opacity-90 hover:opacity-100',
                        )}
                        data-active={isActive || undefined}
                        style={cssVars({ '--chip': color })}
                      >
                        {isActive && <Check className="w-3 h-3 shrink-0" strokeWidth={2.6} />}
                        {label}
                        <span className="tm-mono text-caption2 opacity-70">{count}</span>
                      </button>
                    )
                  })}
                </div>
              )}
            </div>
          )}

          {/* 站点 */}
          {sidebarMenuVisible.sites && mobileSiteNames.length > 0 && (
            <div>
              <SectionHeader
                icon={<Server className="w-3.5 h-3.5" />}
                title={t('site.nav')}
                collapsed={sidebarCollapsed.sites}
                onToggle={() => setSidebarCollapsed({ sites: !sidebarCollapsed.sites })}
              />
              {!sidebarCollapsed.sites && (
                <div className="space-y-0.5 mt-1">
                <button
                  {...longPressProps((x, y) => openGroupMenu({ x, y }, torrents.map((tr) => tr.id), null))}
                  onClick={() => { setFilters({ sites: [] }); onClose() }}
                  className={cn(
                    'w-full flex items-center gap-2 px-2.5 py-1.5 rounded-lg text-body transition-colors tm-nav-item',
                    filters.sites.length === 0 ? 'tm-nav-active text-primary font-medium' : 'text-gray-600 dark:text-gray-300 hover:bg-white/60 dark:hover:bg-white/8',
                  )}
                >
                  <Globe className="w-3.5 h-3.5" />
                  <span className="truncate flex-1 text-left">{t('site.all')}</span>
                  <span className="text-caption2 tm-mono text-gray-400">{mobileSiteTotal}</span>
                </button>
                {/* 站点名聚合去重：torrentSites 的 key 是种子 ID，
                    直接拿它当站点 ID 会导致计数恒为 0 且筛选永不命中 */}
                {mobileSiteNames.map((name) => {
                  const isActive = filters.sites.includes(name)
                  const count = siteStats.counts.get(name) ?? 0
                  return (
                    <button
                      key={name}
                      {...longPressProps((x, y) =>
                        openGroupMenu(
                          { x, y },
                          torrents.filter((tr) => (torrentSites[tr.id] ?? []).includes(name)).map((tr) => tr.id),
                          { kind: 'site', value: name },
                        ),
                      )}
                      onClick={() => { setFilters({ sites: isActive ? [] : [name] }); onClose() }}
                      className={cn(
                        'w-full flex items-center gap-2 px-2.5 py-1.5 rounded-lg text-body transition-colors tm-nav-item',
                        isActive ? 'tm-nav-active text-primary font-medium' : 'text-gray-600 dark:text-gray-300 hover:bg-white/60 dark:hover:bg-white/8',
                      )}
                    >
                      <span className="w-2 h-2 rounded-full shrink-0" style={{ backgroundColor: isActive ? 'var(--color-primary)' : 'var(--color-gray-300)' }} />
                      <span className="truncate flex-1 text-left">{name}</span>
                      <span className="text-caption2 tm-mono text-gray-400">{count}</span>
                    </button>
                  )
                })}
                {siteStats.other > 0 && (
                  <button
                    {...longPressProps((x, y) =>
                      openGroupMenu(
                        { x, y },
                        torrents.filter((tr) => (torrentSites[tr.id] ?? []).length === 0).map((tr) => tr.id),
                        null,
                      ),
                    )}
                    onClick={() => { setFilters({ sites: ['__other__'] }); onClose() }}
                    className={cn(
                      'w-full flex items-center gap-2 px-2.5 py-1.5 rounded-lg text-body transition-colors tm-nav-item',
                      filters.sites.includes('__other__') ? 'tm-nav-active text-primary font-medium' : 'text-gray-600 dark:text-gray-300 hover:bg-white/60 dark:hover:bg-white/8',
                    )}
                  >
                    <span className="w-2 h-2 rounded-full shrink-0 bg-gray-300" />
                    <span className="truncate flex-1 text-left">{t('site.other')}</span>
                    <span className="text-caption2 tm-mono text-gray-400">{siteStats.other}</span>
                  </button>
                )}
                </div>
              )}
            </div>
          )}

        </div>

        {/* 长按分组菜单：portal 到 body，避免 Sheet 动画的 transform 影响固定定位 */}
        {groupMenu && createPortal(
          <FloatingContextMenu
            pos={groupMenu.pos}
            items={[
              {
                key: 'select',
                icon: <Check className="w-3.5 h-3.5 text-primary shrink-0" strokeWidth={2.5} />,
                label: t('sidebar.selectGroupAll', { n: groupMenu.ids.length }),
              },
              ...(groupMenu.scope && groupLimitSupported
                ? ([
                    { type: 'divider' },
                    {
                      key: 'limit',
                      icon: <Gauge className="w-3.5 h-3.5 text-primary shrink-0" />,
                      label: t('sidebar.groupLimit'),
                    },
                  ] as MenuItem[])
                : []),
            ]}
            onPick={pickGroupMenu}
            onClose={() => setGroupMenu(null)}
          />,
          document.body,
        )}

        {/* 底部：设置入口（抽屉覆盖顶栏时保持可及） */}
        <div className="border-t border-white/60 dark:border-white/10 pt-3 flex items-center">
          <Button variant="outline" size="sm" className="w-full justify-center gap-2 bg-white/50 dark:bg-white/5 border-white/60 dark:border-white/10" onClick={() => { onClose(); onOpenSettings() }}>
            <Settings className="w-4 h-4" />
            {t('common.settings')}
          </Button>
        </div>
      </SheetContent>
    </Sheet>
  )
}

// ========== 侧栏右键菜单（独立组件） ==========
// 抽出来的原因：菜单尺寸随内容变化（标签/站点数量不定），原先用
// Math.min(x, innerWidth - 180) 这类写死尺寸的写法，长菜单仍会溢出屏幕。
// 统一交给 useFloatingMenuPosition 按实测尺寸翻转 + 双向夹取。
interface SidebarCtxMenuProps {
  pos: { x: number; y: number }
  onClose: () => void
  submenuOpen: boolean
  onSubmenuEnter: () => void
  onSubmenuLeave: () => void
  statusFilterVisible: Record<string, boolean>
  setStatusFilterVisible: (key: string, visible: boolean) => void
  sidebarMenuVisible: SidebarMenuVisible
  setSidebarMenuVisible: (patch: Partial<SidebarMenuVisible>) => void
  enableDoubleClickSelect: boolean
  setEnableDoubleClickSelect: (v: boolean) => void
  groupShowSize: boolean
  setGroupShowSize: (v: boolean) => void
  showStats: boolean
  setShowStats: (v: boolean) => void
}

function SidebarCtxMenu(props: SidebarCtxMenuProps) {
  const { t } = useTranslation()
  const { ref, style } = useFloatingMenuPosition<HTMLDivElement>(props.pos)
  useDismissOnOutside(ref, props.onClose)

  return createPortal(
    <div
      ref={ref}
      className="fixed z-50 min-w-44 max-h-[80dvh] overflow-y-auto overscroll-contain rounded-tile glass-panel-strong p-1 animate-tm-pop-in"
      role="menu"
      aria-orientation="vertical"
      style={style}
      onPointerDown={(e) => e.stopPropagation()}
      onContextMenu={(e) => e.preventDefault()}
    >
      <CtxCheck
        label={t('sidebar.status')}
        checked={props.sidebarMenuVisible.status}
        onClick={() => { props.setSidebarMenuVisible({ status: !props.sidebarMenuVisible.status }); props.onClose() }}
      />

      {/* 状态过滤器子菜单 */}
      <div
        className="relative"
        onMouseEnter={props.onSubmenuEnter}
        onMouseLeave={props.onSubmenuLeave}
      >
        <div className="w-full flex items-center gap-2 px-3 py-1.5 rounded-md text-footnote text-left cursor-pointer hover:bg-white/60 dark:hover:bg-white/10">
          <span className="w-3.5 shrink-0" />
          <span className="flex-1">{t('sidebar.statusFilter')}</span>
          <ChevronDown className="w-3 h-3 -rotate-90 text-gray-400" />
        </div>
        {props.submenuOpen && (
          <div
            onMouseEnter={props.onSubmenuEnter}
            onMouseLeave={props.onSubmenuLeave}
            className="absolute top-0 left-full ml-1 z-10 min-w-36 rounded-tile glass-panel-solid p-1"
          >
            {STATUS_ITEMS.map((item) => {
              // 'all' 是常驻项，不参与显隐控制：
              // 若允许取消，会写入永不生效的 statusFilterVisible.all=false 脏状态
              const isAll = item.key === 'all'
              const visible = isAll || props.statusFilterVisible[item.key] !== false
              return (
                <button
                  key={item.key}
                  disabled={isAll}
                  onClick={() => props.setStatusFilterVisible(item.key, !visible)}
                  className={cn(
                    'w-full flex items-center gap-2 px-3 py-1.5 rounded-md text-footnote text-left text-gray-700 dark:text-gray-200 hover:bg-white/60 dark:hover:bg-white/10',
                    isAll && 'cursor-default opacity-60 hover:bg-transparent dark:hover:bg-transparent',
                  )}
                >
                  <span className="w-3.5 shrink-0 flex justify-center text-primary">{visible && <Check className="w-3.5 h-3.5" strokeWidth={2.4} />}</span>
                  {t(item.label)}
                </button>
              )
            })}
          </div>
        )}
      </div>

      <div className="my-1 h-px bg-gray-200/60 dark:bg-white/10" />
      <CtxCheck
        label={t('sidebar.labels')}
        checked={props.sidebarMenuVisible.labels}
        onClick={() => { props.setSidebarMenuVisible({ labels: !props.sidebarMenuVisible.labels }); props.onClose() }}
      />
      <CtxCheck
        label={t('sidebar.dirs')}
        checked={props.sidebarMenuVisible.dirs}
        onClick={() => { props.setSidebarMenuVisible({ dirs: !props.sidebarMenuVisible.dirs }); props.onClose() }}
      />
      <CtxCheck
        label={t('sidebar.sites')}
        checked={props.sidebarMenuVisible.sites}
        onClick={() => { props.setSidebarMenuVisible({ sites: !props.sidebarMenuVisible.sites }); props.onClose() }}
      />
      <CtxCheck
        label={t('sidebar.error')}
        checked={props.sidebarMenuVisible.error}
        onClick={() => { props.setSidebarMenuVisible({ error: !props.sidebarMenuVisible.error }); props.onClose() }}
      />
      {/* 下载器分布：只有聚合视图才有内容，非聚合时开关留着但分组不渲染
          （与其它分组一致，免得用户以为菜单缺了条目） */}
      <CtxCheck
        label={t('nav.servers')}
        checked={props.sidebarMenuVisible.servers}
        onClick={() => { props.setSidebarMenuVisible({ servers: !props.sidebarMenuVisible.servers }); props.onClose() }}
      />

      <div className="my-1 h-px bg-gray-200/60 dark:bg-white/10" />
      <CtxCheck
        label={t('sidebar.enableDoubleClickSelect')}
        checked={props.enableDoubleClickSelect}
        onClick={() => { props.setEnableDoubleClickSelect(!props.enableDoubleClickSelect); props.onClose() }}
      />
      <CtxCheck
        label={t('sidebar.groupShowSize')}
        checked={props.groupShowSize}
        onClick={() => { props.setGroupShowSize(!props.groupShowSize); props.onClose() }}
      />

      <div className="my-1 h-px bg-gray-200/60 dark:bg-white/10" />
      <CtxCheck
        label={t('sidebar.showStats')}
        checked={props.showStats}
        onClick={() => { props.setShowStats(!props.showStats); props.onClose() }}
      />
    </div>,
    document.body,
  )
}

// ========== 分组项右键菜单（站点 / 标签） ==========
function GroupCtxMenu({ pos, onClose, onGroupLimit }: {
  pos: { x: number; y: number; kind: 'site' | 'label'; value: string }
  onClose: () => void
  onGroupLimit: (kind: 'site' | 'label', value: string) => void
}) {
  const { t } = useTranslation()
  const { ref, style } = useFloatingMenuPosition<HTMLDivElement>(pos)
  useDismissOnOutside(ref, onClose)

  return createPortal(
    <div
      ref={ref}
      className="fixed z-50 min-w-56 rounded-tile glass-panel-strong p-1 animate-tm-pop-in"
      role="menu"
      style={style}
      onPointerDown={(e) => e.stopPropagation()}
      onContextMenu={(e) => e.preventDefault()}
    >
      <div className="px-3 py-1.5 text-footnote text-gray-400 truncate max-w-[224px]" title={pos.value}>
        {pos.kind === 'site' ? t('site.nav') : t('nav.labels')}: {pos.value}
      </div>
      <button
        onClick={() => onGroupLimit(pos.kind, pos.value)}
        className="w-full flex items-center gap-2 px-3 py-1.5 rounded-md text-footnote text-left text-gray-700 dark:text-gray-200 hover:bg-white/60 dark:hover:bg-white/10"
      >
        <Gauge className="w-3.5 h-3.5 text-primary shrink-0" />
        {t('sidebar.groupLimit')}
      </button>
    </div>,
    document.body,
  )
}
