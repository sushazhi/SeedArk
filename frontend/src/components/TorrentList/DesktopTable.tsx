import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { AlertCircle, ArrowDownWideNarrow, ArrowUpNarrowWide, RotateCcw } from 'lucide-react'
import { useVirtualizer } from '@tanstack/react-virtual'
import { useTranslation } from 'react-i18next'
import { torrentApi } from '@/api/torrent'
import { useTorrentActions } from '@/hooks/useTorrentActions'
import { useTorrentMenuHandler } from '@/hooks/useTorrentMenuHandler'
import { useSemanticPath } from '@/hooks/useSemanticPath'
import { usePlatform } from '@/platform'
import { useAppStore } from '@/stores/appStore'
import type { ColumnConfig, Torrent } from '@/types'
import { formatBytes, formatDate, formatDuration, formatEta, formatRatio, formatSpeed } from '@/utils/format'
import { translateError } from '@/utils/errorText'
import { toast } from '@/lib/toast'
import { cn, cssVars } from '@/lib/utils'
import { tagColor } from '@/utils/tagColor'
import { StatusTag } from '@/components/status/StatusTag'
import { ServerCell } from '@/components/TorrentList/ServerCell'
import { ProgressBar } from '@/components/TorrentList/ProgressBar'
import { EmptyList } from '@/components/TorrentList/EmptyList'
import { buildTorrentMenu, EditModals, FloatingContextMenu, TorrentMenuDropdown } from '@/components/TorrentMenu'
import { RemoveTorrentDialog, ReplaceTrackerDialog } from '@/components/ToolsDialogs'
import type { EditMode, EditTarget, MenuItem } from '@/components/TorrentMenu'
import { Badge } from '@/components/ui/badge'
import { Checkbox } from '@/components/ui/checkbox'

const mainTracker = (t: Torrent) => t.trackerStats?.find((x) => !x.isBackup)?.host ?? t.trackerStats?.[0]?.host ?? ''

// 数值列：表头与单元格右对齐 + 等宽数字，便于纵向对比
const NUMERIC_COLS = new Set([
  'size', 'download', 'upload', 'ratio', 'secondsSeeding', 'eta', 'peers',
  'uploaded', 'downloaded', 'added', 'doneDate', 'queuePosition', 'fileCount', 'limits',
])

// 单元格渲染
function Cell({ torrent, col }: { torrent: Torrent; col: ColumnConfig }) {
  const { t } = useTranslation()
  const sem = useSemanticPath()
  switch (col.key) {
    case 'name':
      return (
        <div className="flex items-center gap-2 min-w-0 flex-1">
          {torrent.error > 0 && <AlertCircle className="w-3.5 h-3.5 text-red-500 shrink-0" />}
          <span className="truncate font-medium text-gray-800 dark:text-gray-100" title={translateError(torrent.errorString, t) || torrent.name}>
            {torrent.name}
          </span>
        </div>
      )
    case 'size':
      return <span className="tm-mono">{formatBytes(torrent.totalSize)}</span>
    case 'progress':
      // 百分比内嵌到进度条里（填充区白字 / 未填充区灰字）
      return <ProgressBar value={torrent.percentDone} error={torrent.error > 0} />
    case 'status':
      return <StatusTag torrent={torrent} />
    case 'download':
      return <span className="tm-mono text-green-600 dark:text-green-400">{formatSpeed(torrent.rateDownload)}</span>
    case 'upload':
      return <span className="tm-mono text-blue-600 dark:text-blue-400">{formatSpeed(torrent.rateUpload)}</span>
    case 'ratio':
      return <span className="tm-mono">{formatRatio(torrent.uploadRatio)}</span>
    case 'secondsSeeding':
      return <span className="tm-mono text-footnote">{formatDuration(torrent.secondsSeeding)}</span>
    case 'eta':
      return <span className="tm-mono">{formatEta(torrent.eta)}</span>
    case 'peers':
      return (
        <span className="tm-mono text-footnote">
          <span className="text-blue-600 dark:text-blue-400">{torrent.peersSendingToUs}</span>
          <span className="text-gray-400"> / </span>
          <span className="text-green-600 dark:text-green-400">{torrent.peersGettingFromUs}</span>
        </span>
      )
    case 'uploaded':
      return <span className="tm-mono">{formatBytes(torrent.uploadedEver)}</span>
    case 'downloaded':
      return <span className="tm-mono">{formatBytes(torrent.downloadedEver)}</span>
    case 'added':
      return <span className="tm-mono text-footnote">{formatDate(torrent.addedDate)}</span>
    case 'doneDate':
      return <span className="tm-mono text-footnote">{formatDate(torrent.doneDate)}</span>
    case 'tracker':
      return (
        <span className="text-footnote text-gray-600 dark:text-gray-300 truncate" title={torrent.trackerStats?.[0]?.announce}>
          {mainTracker(torrent) || '-'}
        </span>
      )
    case 'label':
      return (
        <span className="flex gap-1">
          {torrent.labels?.length ? (
            torrent.labels.map((l) => (
              <Badge
                key={l}
                variant="outline"
                className="tm-chip text-caption2 px-1.5 py-0 leading-none"
                style={cssVars({ '--chip': tagColor(l) })}
              >
                {l}
              </Badge>
            ))
          ) : (
            <span className="text-gray-300 dark:text-gray-600">-</span>
          )}
        </span>
      )
    case 'queuePosition':
      return <span>{torrent.queuePosition + 1}</span>
    case 'priority':
      return (
        <span
          className={cn(
            'text-footnote font-medium',
            (torrent.bandwidthPriority ?? 0) > 0
              ? 'text-red-500'
              : (torrent.bandwidthPriority ?? 0) < 0
                ? 'text-blue-500'
                : 'text-gray-400 dark:text-gray-500',
          )}
        >
          {(torrent.bandwidthPriority ?? 0) > 0
            ? t('action.priorityHigh')
            : (torrent.bandwidthPriority ?? 0) < 0
              ? t('action.priorityLow')
              : t('action.priorityNormal')}
        </span>
      )
    case 'limits':
      return (
        <span className="text-footnote text-gray-600 dark:text-gray-300 whitespace-nowrap">
          <span className={torrent.downloadLimited ? '' : 'text-gray-300 dark:text-gray-600'}>
            ↓ {torrent.downloadLimited ? formatBytes(torrent.downloadLimit * 1024) + '/s' : '∞'}
          </span>
          <span className="mx-1 text-gray-300 dark:text-gray-600">/</span>
          <span className={torrent.uploadLimited ? '' : 'text-gray-300 dark:text-gray-600'}>
            ↑ {torrent.uploadLimited ? formatBytes(torrent.uploadLimit * 1024) + '/s' : '∞'}
          </span>
        </span>
      )
    case 'fileCount':
      return <span>{torrent.fileCount}</span>
    case 'downloadDir':
      return <span className="text-footnote text-gray-500 truncate" title={torrent.downloadDir}>{sem(torrent.downloadDir) || '-'}</span>
    case 'hashString':
      return <span className="text-footnote tm-mono text-gray-500 truncate" title={torrent.hashString}>{torrent.hashString}</span>
    case 'server':
      return <ServerCell torrent={torrent} />
    case 'error':
      return (
        <span className="text-red-500 truncate" title={translateError(torrent.errorString, t) || undefined}>
          {translateError(torrent.errorString, t) || '-'}
        </span>
      )
    default:
      return null
  }
}

// 桌面虚拟滚动表格
export function DesktopTable({ torrents, onOpenDetail, onOpenBatchClean }: {
  torrents: Torrent[]
  onOpenDetail: (t: Torrent) => void
  onOpenBatchClean?: () => void
}) {
  const { t } = useTranslation()
  const actions = useTorrentActions()
  const { can } = usePlatform()
  const columns = useAppStore((s) => s.columns)
  const showCheckboxes = useAppStore((s) => s.showCheckboxes)
  const selectedIds = useAppStore((s) => s.selectedIds)
  const toggleSelect = useAppStore((s) => s.toggleSelect)
  const setSelection = useAppStore((s) => s.setSelection)
  const clearSelection = useAppStore((s) => s.clearSelection)
  const selectAnchorId = useAppStore((s) => s.selectAnchorId)
  const setSelectAnchor = useAppStore((s) => s.setSelectAnchor)
  const setColumnWidth = useAppStore((s) => s.setColumnWidth)
  const setColumns = useAppStore((s) => s.setColumns)
  const toggleColumn = useAppStore((s) => s.toggleColumn)
  const resetColumns = useAppStore((s) => s.resetColumns)
  const singleLine = useAppStore((s) => s.singleLine)
  const sortField = useAppStore((s) => s.sortField)
  const sortOrder = useAppStore((s) => s.sortOrder)
  const setSortField = useAppStore((s) => s.setSortField)
  const setSortOrder = useAppStore((s) => s.setSortOrder)
  const [editTarget, setEditTarget] = useState<EditTarget | null>(null)
  const [removeIds, setRemoveIds] = useState<number[] | null>(null)
  const [trackerOpen, setTrackerOpen] = useState(false)
  // 表头右键列选择菜单位置（null = 关闭）
  const [colMenuPos, setColMenuPos] = useState<{ x: number; y: number } | null>(null)
  const parentRef = useRef<HTMLDivElement>(null)
  const headerRef = useRef<HTMLDivElement>(null)
  const dragIdRef = useRef<number | null>(null)
  // 拖拽排序的视觉反馈：只有按队列位置排序时拖拽才真正生效，因此
  // canReorder 为假时直接禁用 draggable，而不是等用户拖完再弹警告
  const [dragOverId, setDragOverId] = useState<number | null>(null)
  const [draggingId, setDraggingId] = useState<number | null>(null)
  const lastClickRef = useRef<{ id: number; t: number }>({ id: -1, t: 0 })
  const rowHeight = singleLine ? 40 : 56
  const canReorder = sortField === 'queuePosition'

  // 表头横向滚动与内容区同步（表头独立于虚拟滚动容器，需手动镜像 scrollLeft）
  const syncHeaderScroll = (e: React.UIEvent<HTMLDivElement>) => {
    if (headerRef.current) headerRef.current.scrollLeft = e.currentTarget.scrollLeft
  }

  // 正在拖拽调宽的列（含实时预览宽度）：拖拽期间分隔线保持高亮，
  // 且只走本地状态——写 store 等于每帧序列化整份列配置落一次 localStorage
  const [resizing, setResizing] = useState<{ key: string; width: number } | null>(null)

  // 表头拖拽调整列宽
  const startColResize = (e: React.MouseEvent, col: ColumnConfig) => {
    e.preventDefault()
    e.stopPropagation()
    const startX = e.clientX
    const startWidth = col.width ?? 100
    const onMove = (ev: MouseEvent) =>
      setResizing({
        key: col.key,
        width: Math.min(900, Math.max(60, Math.round(startWidth + ev.clientX - startX))),
      })
    const onUp = () => {
      document.removeEventListener('mousemove', onMove)
      document.removeEventListener('mouseup', onUp)
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
      // 只有松手这一次才真正提交
      setResizing((cur) => {
        if (cur) setColumnWidth(cur.key, cur.width)
        return null
      })
    }
    document.addEventListener('mousemove', onMove)
    document.addEventListener('mouseup', onUp)
    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'
    setResizing({ key: col.key, width: startWidth })
  }

  // 表头列拖拽移动（指针事件实现，不用 HTML5 DnD：避免浏览器半透明拖影、draggable 与调宽手柄冲突）
  const colDragRef = useRef<{ key: string; startX: number; startY: number; active: boolean; overKey: string | null } | null>(null)
  const [dragCol, setDragCol] = useState<string | null>(null)
  const [overCol, setOverCol] = useState<string | null>(null)
  const [dragPos, setDragPos] = useState<{ x: number; y: number } | null>(null)
  const suppressSortRef = useRef(false)

  const onColDragMove = (e: MouseEvent) => {
    const d = colDragRef.current
    if (!d) return
    if (!d.active) {
      if (Math.abs(e.clientX - d.startX) < 5 && Math.abs(e.clientY - d.startY) < 5) return
      d.active = true
      setDragCol(d.key)
      document.body.style.cursor = 'grabbing'
      document.body.style.userSelect = 'none'
    }
    setDragPos({ x: e.clientX, y: e.clientY })
    const el = document.elementFromPoint(e.clientX, e.clientY)
    const key = (el as HTMLElement | null)?.closest?.('[data-col-cell]')?.getAttribute('data-col-key') ?? null
    if (d.overKey !== key) {
      d.overKey = key
      setOverCol(key)
    }
  }

  const onColDragUp = () => {
    const d = colDragRef.current
    colDragRef.current = null
    document.removeEventListener('mousemove', onColDragMove)
    document.removeEventListener('mouseup', onColDragUp)
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
    setDragPos(null)
    setDragCol(null)
    setOverCol(null)
    if (!d?.active) return
    // 这是一次拖动而非点击排序，吃掉随后派发的 click
    suppressSortRef.current = true
    setTimeout(() => { suppressSortRef.current = false }, 300)
    const fromKey = d.key
    const toKey = d.overKey
    if (!toKey || toKey === fromKey) return
    const next = [...columns]
    const from = next.findIndex((c) => c.key === fromKey)
    const to = next.findIndex((c) => c.key === toKey)
    if (from < 0 || to < 0) return
    const [moved] = next.splice(from, 1)
    next.splice(to, 0, moved)
    setColumns(next)
  }

  const startColDrag = (e: React.MouseEvent, col: ColumnConfig) => {
    // 从宽度调节手柄开始的拖拽是调宽，不进入列移动
    if ((e.target as HTMLElement).closest('[data-col-resize]')) return
    e.preventDefault()
    colDragRef.current = { key: col.key, startX: e.clientX, startY: e.clientY, active: false, overKey: null }
    document.addEventListener('mousemove', onColDragMove)
    document.addEventListener('mouseup', onColDragUp)
  }

  // 排序列头点击
  const handleSort = (field: string) => {
    if (suppressSortRef.current) return
    if (sortField === field) {
      setSortOrder(sortOrder === 'asc' ? 'desc' : 'asc')
    } else {
      setSortField(field)
      setSortOrder('asc')
    }
  }

  // 初始化排序：从 localStorage 恢复
  useEffect(() => {
    const stored = localStorage.getItem('tm_sort')
    if (stored) {
      try {
        const s = JSON.parse(stored) as { field: string; order: 'asc' | 'desc' }
        setSortField(s.field)
        setSortOrder(s.order)
      } catch {}
    }
  }, [])
  // 持久化排序状态
  useEffect(() => {
    localStorage.setItem('tm_sort', JSON.stringify({ field: sortField, order: sortOrder }))
  }, [sortField, sortOrder])

  // 排序已统一在 useFilter（多级排序）中完成，这里直接使用过滤+排序后的列表
  const sortedTorrents = torrents

  // 行点击：普通=单选（再点一次已选中的唯一项则取消），Ctrl/Cmd=切换，Shift=从锚点连选
  const handleSelect = (torrent: Torrent, e: React.MouseEvent) => {
    if (e.shiftKey && !e.ctrlKey && !e.metaKey) {
      const anchorId = selectAnchorId ?? sortedTorrents[0]?.id
      if (anchorId != null) {
        const a = sortedTorrents.findIndex((x) => x.id === anchorId)
        const b = sortedTorrents.findIndex((x) => x.id === torrent.id)
        const from = a < 0 ? 0 : Math.min(a, b)
        const to = a < 0 ? b : Math.max(a, b)
        setSelection(sortedTorrents.slice(from, to + 1).map((x) => x.id))
      }
      return
    }
    if (e.ctrlKey || e.metaKey) {
      toggleSelect(torrent.id)
      setSelectAnchor(torrent.id)
      return
    }
    // 300ms 内同行两击是双击的前半程，不算「再点取消」，否则双击开详情时选中态会闪掉
    const now = Date.now()
    const repeatClick = lastClickRef.current.id === torrent.id && now - lastClickRef.current.t <= 300
    lastClickRef.current = { id: torrent.id, t: now }
    if (repeatClick) return
    if (selectedIds.length === 1 && selectedIds[0] === torrent.id) {
      clearSelection()
      return
    }
    setSelection([torrent.id])
    setSelectAnchor(torrent.id)
  }

  // 拖拽排序：按队列位置差执行 QueueMove。
  // 只有按「队列位置」排序时屏幕顺序才与队列顺序一致；其它排序下移动队列不会改变
  // 行的显示位置，故此时行不可拖（draggable=false，见 canReorder），这里只做兜底
  const handleDrop = async (dst: Torrent) => {
    const srcId = dragIdRef.current
    dragIdRef.current = null
    if (srcId == null || srcId === dst.id) return
    if (sortField !== 'queuePosition') return
    const src = sortedTorrents.find((x) => x.id === srcId)
    if (!src) return
    const steps = src.queuePosition - dst.queuePosition
    if (steps === 0) return
    const direction = steps > 0 ? 'up' : 'down'
    const count = Math.abs(steps)
    try {
      for (let k = 0; k < count; k++) {
        await torrentApi.queue(srcId, direction as 'up' | 'down')
      }
      toast.success(t('toast.queueMoved'))
    } catch {
      // 拦截器已提示
    }
  }

  const visibleColumns = useMemo(() => columns.filter((c) => c.visible), [columns])

  // 表头右键菜单：全部列勾选显隐 + 重置布局
  const columnMenuItems = useMemo<MenuItem[]>(
    () => [
      ...columns.map((c) => ({ key: c.key, label: t(c.label), checked: c.visible })),
      { type: 'divider' as const },
      { key: 'reset-columns', label: t('columns.reset'), icon: <RotateCcw className="w-4 h-4 text-primary" /> },
    ],
    [columns, t],
  )
  const handleColumnMenuPick = (key: string) => {
    if (key === 'reset-columns') resetColumns()
    else toggleColumn(key)
  }

  const virtualizer = useVirtualizer({
    count: sortedTorrents.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => rowHeight,
    overscan: 12,
  })

  // 行高随「单行模式」变化：estimateSize 换了实现，但 virtualizer 已经缓存了旧尺寸，
  // 不重测会出现行高错位 / 滚动位置跳动
  const singleLineRef = useRef(singleLine)
  useEffect(() => {
    if (singleLineRef.current === singleLine) return
    singleLineRef.current = singleLine
    virtualizer.measure()
  }, [singleLine, virtualizer])

  const scrollTargetIds = useAppStore((s) => s.scrollTargetIds)
  const consumeScrollTarget = useAppStore((s) => s.consumeScrollTarget)
  // 从分组切回「全部」后滚回之前选中的种子；在绘制前定位，避免先闪一下旧位置。
  // 按 scrollTargetIds 的顺序（最后点选的锚点在前）定位第一个仍在新列表里的种子
  useLayoutEffect(() => {
    if (scrollTargetIds.length === 0) return
    consumeScrollTarget()
    const indexOf = new Map(sortedTorrents.map((t, i) => [t.id, i]))
    for (const id of scrollTargetIds) {
      const idx = indexOf.get(id)
      if (idx !== undefined) {
        virtualizer.scrollToIndex(idx, { align: 'center' })
        break
      }
    }
  }, [scrollTargetIds, sortedTorrents, virtualizer, consumeScrollTarget])

  const handleMenuClick = useTorrentMenuHandler({
    onOpenDetail,
    onOpenBatchClean,
    onEdit: setEditTarget,
    onRemove: (id) => setRemoveIds([id]),
    onReplaceTrackers: () => setTrackerOpen(true),
    canRevealPath: can('fs.revealPath'),
  })

  const menuCtx = { actions, t: (k: string) => t(k), onOpenDetail, canRevealPath: can('fs.revealPath'), onEdit: (mode: EditMode, tt: Torrent) => setEditTarget({ torrent: tt, mode }), onOpenBatchClean }

  if (sortedTorrents.length === 0) {
    return <EmptyList />
  }

  // 所有列统一固定宽度（名称列默认 320），行尾占位吸收剩余空间
  // 拖拽中的宽度只在本地预览态里，这里优先读它，表头与单元格才会实时跟手
  const colWidth = (col: ColumnConfig) =>
    resizing?.key === col.key ? resizing.width : col.width ?? 100

  const headerStyle = (col: ColumnConfig) => ({
    width: colWidth(col),
    flex: `0 0 ${colWidth(col)}px`,
  })

  // 可排序列的排序图标。图标槽位常驻（未排序时渲染等宽占位），
  // 否则切换排序字段时表头文字会因为多/少一个箭头而左右跳动
  const sortIcon = (field: string) => {
    if (sortField !== field) return <span className="w-3 h-3 shrink-0" aria-hidden />
    return sortOrder === 'asc'
      ? <ArrowUpNarrowWide className="w-3 h-3 shrink-0 text-primary" />
      : <ArrowDownWideNarrow className="w-3 h-3 shrink-0 text-primary" />
  }

  return (
    <div className="flex flex-col h-full" style={{ paddingTop: 'var(--pad-top)' }}>
      {/* 表头 */}
      <div
        ref={headerRef}
        onContextMenu={(e) => {
          e.preventDefault()
          setColMenuPos({ x: e.clientX, y: e.clientY })
        }}
        className="tm-dock glass-panel flex items-center px-3 h-9 text-footnote font-semibold tracking-wide text-gray-500 dark:text-gray-400 shrink-0 overflow-hidden"
      >
        {showCheckboxes && <div className="w-8 shrink-0" />}
        {visibleColumns.map((col) => {
          const isSortable = true
          return (
            <div
              key={col.key}
              data-col-cell
              data-col-key={col.key}
              onMouseDown={(e) => startColDrag(e, col)}
              className={cn(
                // 表头对齐方式必须跟随该列的数据对齐：数字列若表头居中而单元格右对齐，
                // 两者没有共同基线，纵向扫视时标签与数值对不上。末尾留出与单元格
                // 等宽的排序图标位（w-3 + gap-0.5），否则加了箭头后文字会整体偏移
                'px-2 relative flex items-center gap-0.5 select-none transition-colors',
                NUMERIC_COLS.has(col.key) ? 'justify-end text-right' : 'justify-start text-left',
                overCol === col.key ? 'bg-primary/10' : 'hover:bg-black/[0.03] dark:hover:bg-white/[0.06]',
                dragCol === col.key && 'opacity-40',
              )}
              style={{ ...headerStyle(col), cursor: 'grab' }}
              // 队列位置列：拖拽排序只在这一列排序时可用，把原因写在表头上，
              // 免得用户拖不动时四处找开关
              title={col.key === 'queuePosition' && !canReorder ? t('toast.queueSortRequired') : undefined}
              onClick={() => handleSort(col.key)}
            >
              <span className="truncate">{t(col.label)}</span>
              {isSortable && sortIcon(col.key)}
              {/* 列宽手柄：常驻 2px 分隔线（hover/拖拽时加粗成品牌色），
                  热区 12px 且跨列边界 5px，方便从两侧抓住 */}
              <span
                data-col-resize
                className="group/resize absolute -right-[5px] top-0 bottom-0 z-10 w-3 flex items-center justify-center cursor-col-resize"
                onMouseDown={(e) => startColResize(e, col)}
                onClick={(e) => e.stopPropagation()}
              >
                <span
                  className={cn(
                    'rounded-full transition-all duration-150',
                    resizing?.key === col.key
                      ? 'w-[3px] h-[85%] bg-primary'
                      : 'w-[2px] h-[65%] bg-gray-400/90 dark:bg-gray-500 group-hover/resize:w-[3px] group-hover/resize:h-[85%] group-hover/resize:bg-primary',
                  )}
                />
              </span>
            </div>
          )
        })}
        <div className="flex-1 min-w-0" />
      </div>

      {/* 虚拟滚动区 */}
      <div ref={parentRef} onScroll={syncHeaderScroll} className="tm-scroll tm-scroll--notch flex-1">
        <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
          {virtualizer.getVirtualItems().map((vi) => {
            const torrent = sortedTorrents[vi.index]
            // 虚拟滚动位置可能在本帧数据收缩后短暂越界（列表刚被过滤/删除），
            // 此时跳过本项渲染，下一帧 virtualizer 会自行修正
            if (!torrent) return null
            const menuItems = buildTorrentMenu(menuCtx, torrent)
            return (
              <div
                key={torrent.id}
                className="absolute top-0 left-0 w-full"
                style={{ height: vi.size, transform: `translateY(${vi.start}px)` }}
              >
                <TorrentMenuDropdown items={menuItems} onClick={handleMenuClick(torrent)} trigger="contextMenu" align="start">
                  <div
                    title={torrent.error > 0 ? (translateError(torrent.errorString, t) || torrent.name) : torrent.name}
                    draggable={canReorder}
                    onDragStart={(e) => {
                      dragIdRef.current = torrent.id
                      setDraggingId(torrent.id)
                      e.dataTransfer.effectAllowed = 'move'
                    }}
                    onDragEnd={() => {
                      draggingId === torrent.id && setDraggingId(null)
                      setDragOverId(null)
                    }}
                    onDragOver={(e) => {
                      if (!canReorder || dragIdRef.current === torrent.id) return
                      e.preventDefault()
                      e.dataTransfer.dropEffect = 'move'
                      if (dragOverId !== torrent.id) setDragOverId(torrent.id)
                    }}
                    onDragLeave={() => {
                      setDragOverId((cur) => (cur === torrent.id ? null : cur))
                    }}
                    onDrop={(e) => {
                      e.preventDefault()
                      setDragOverId(null)
                      setDraggingId(null)
                      void handleDrop(torrent)
                    }}
                    onClick={(e) => handleSelect(torrent, e)}
                    className={cn(
                      'tm-nav-item flex items-center px-3 h-full border-b border-gray-100/60 dark:border-white/[0.04] text-body cursor-default select-none',
                      selectedIds.includes(torrent.id) && 'tm-row-selected',
                      'hover:bg-gray-100/50 dark:hover:bg-white/[0.04]',
                      // 拖拽中：源行淡出并标记，落点行上缘画出插入位（与表头列拖拽同一套语言）
                      draggingId === torrent.id && 'opacity-40',
                      dragOverId === torrent.id && 'shadow-[inset_0_2px_0_var(--color-primary)]',
                    )}
                    onDoubleClick={() => onOpenDetail(torrent)}
                  >
                    {showCheckboxes && (
                      <div className="shrink-0 w-8 h-8 flex items-center justify-center" onClick={(e) => e.stopPropagation()}>
                        <Checkbox checked={selectedIds.includes(torrent.id)} onCheckedChange={() => { toggleSelect(torrent.id); setSelectAnchor(torrent.id) }} />
                      </div>
                    )}
                    {visibleColumns.map((col) => (
                      <div key={col.key} className={cn('px-2 truncate', NUMERIC_COLS.has(col.key) && 'text-right')} style={headerStyle(col)}>
                        <Cell torrent={torrent} col={col} />
                      </div>
                    ))}
                    <div className="flex-1 min-w-0" />
                  </div>
                </TorrentMenuDropdown>
              </div>
            )
          })}
        </div>
      </div>

      {/* 横向滚动条：显式落在列表与底栏之间（原生条贴盒底边且被渐隐 mask 吞掉大半，难以察觉） */}
      <HorizontalScrollbar
        hostRef={parentRef}
        syncKey={`${resizing?.key ?? ''}:${resizing?.width ?? 0}:${columns
          .map((c) => (c.visible ? `${c.key}:${c.width ?? 100}` : ''))
          .join(',')}`}
      />

      {/* 列拖拽跟随指针的实心胶囊（portal 到 body，避免 fixed 被 backdrop-filter 劫持） */}
      {dragPos && dragCol && createPortal(
        <div
          className="fixed z-[200] pointer-events-none rounded-full glass-panel-solid px-3 py-1.5 text-footnote font-medium text-gray-700 dark:text-gray-200"
          style={{ left: dragPos.x + 14, top: dragPos.y + 14 }}
        >
          {t(columns.find((c) => c.key === dragCol)?.label ?? dragCol)}
        </div>,
        document.body,
      )}

      {/* 表头右键：列选择菜单（勾选列不关闭，便于连续操作；重置后关闭） */}
      {colMenuPos && createPortal(
        <FloatingContextMenu
          pos={colMenuPos}
          items={columnMenuItems}
          onPick={(key) => {
            if (key === 'reset-columns') setColMenuPos(null)
            handleColumnMenuPick(key)
          }}
          onClose={() => setColMenuPos(null)}
        />,
        document.body,
      )}

      <EditModals target={editTarget} onClose={() => setEditTarget(null)} />
      <RemoveTorrentDialog open={!!removeIds} ids={removeIds ?? []} onClose={() => setRemoveIds(null)} />
      <ReplaceTrackerDialog open={trackerOpen} onClose={() => setTrackerOpen(false)} />
    </div>
  )
}

// ========== 底部横向滚动条 ==========
// 表格列总宽超视口时，原生横向滚动条贴在滚动盒底边，且大半落在底部渐隐的
// 全透明 mask 区里，细到难以察觉。这里显式渲染一条放在列表与底栏之间：
// 支持拖拽拇指、点击/按住轨道跳转，拇指位置实时跟随横向滚动。
// 无横向溢出时不渲染，避免占位。
function HorizontalScrollbar({ hostRef, syncKey }: {
  hostRef: React.RefObject<HTMLDivElement | null>
  // 列宽拖拽等改变 scrollWidth 却不触发宿主 scroll/resize 的场景，由调用方捎带刷新
  syncKey?: string
}) {
  const [state, setState] = useState({ visible: false, ratio: 1, pos: 0 })
  const updateRef = useRef<() => void>(() => {})
  // 拖拽拇指：指针位移 × 溢出量/拇指行程 换算成 scrollLeft。
  // 拖拽中滚动会触发重渲染，状态必须放 ref，不能用渲染期局部对象。
  // 所有 hooks 必须在提前 return 之前调用，否则滚动条从隐藏变可见时 hook 数量
  // 增多会触发 React #310
  const dragRef = useRef<{ startX: number; startScroll: number; pxPerPx: number } | null>(null)

  useLayoutEffect(() => { updateRef.current() }, [syncKey])

  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    let raf = 0
    const update = () => {
      raf = 0
      const overflow = host.scrollWidth - host.clientWidth
      if (overflow <= 1 || host.clientWidth <= 0) {
        setState((cur) => (cur.visible ? { visible: false, ratio: 1, pos: 0 } : cur))
        return
      }
      // 拇指最窄 32px（宽度用百分比表达，左移量按剩余行程折算）
      const ratio = Math.max(32 / host.clientWidth, host.clientWidth / host.scrollWidth)
      setState({
        visible: true,
        ratio,
        pos: Math.min(1, Math.max(0, host.scrollLeft / overflow)),
      })
    }
    updateRef.current = update
    const schedule = () => {
      if (!raf) raf = requestAnimationFrame(update)
    }
    host.addEventListener('scroll', schedule, { passive: true })
    const ro = new ResizeObserver(schedule)
    ro.observe(host)
    // 绝对定位的行不改变内容 wrapper 的 border-box，仍观察它兜底内容尺寸变化
    if (host.firstElementChild) ro.observe(host.firstElementChild)
    update()
    return () => {
      host.removeEventListener('scroll', schedule)
      ro.disconnect()
      if (raf) cancelAnimationFrame(raf)
      updateRef.current = () => {}
    }
  }, [hostRef])

  if (!state.visible) return null

  // 拖拽拇指：指针位移 × 溢出量/拇指行程 换算成 scrollLeft
  const onThumbPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    e.preventDefault()
    e.stopPropagation()
    const host = hostRef.current
    const thumb = e.currentTarget
    if (!host) return
    const overflow = host.scrollWidth - host.clientWidth
    const maxTravel = Math.max(1, (thumb.parentElement?.clientWidth ?? 0) - thumb.offsetWidth)
    dragRef.current = { startX: e.clientX, startScroll: host.scrollLeft, pxPerPx: overflow / maxTravel }
    thumb.setPointerCapture(e.pointerId)
  }
  const onThumbPointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    const host = hostRef.current
    const d = dragRef.current
    if (!host || !d) return
    host.scrollLeft = d.startScroll + (e.clientX - d.startX) * d.pxPerPx
  }
  const onThumbPointerUp = (e: React.PointerEvent<HTMLDivElement>) => {
    dragRef.current = null
    e.currentTarget.releasePointerCapture(e.pointerId)
  }

  // 点击/按住轨道：拇指中心对齐落点并跟随指针
  const onTrackPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if ((e.target as HTMLElement).dataset.thumb) return
    e.preventDefault()
    const host = hostRef.current
    const track = e.currentTarget
    if (!host) return
    const rect = track.getBoundingClientRect()
    const overflow = host.scrollWidth - host.clientWidth
    const thumbW = Math.max(32, (host.clientWidth / host.scrollWidth) * rect.width)
    const maxTravel = Math.max(1, rect.width - thumbW)
    const jumpTo = (cx: number) => {
      host.scrollLeft = Math.min(overflow, Math.max(0, ((cx - rect.left - thumbW / 2) / maxTravel) * overflow))
    }
    jumpTo(e.clientX)
    track.setPointerCapture(e.pointerId)
    const onMove = (ev: PointerEvent) => jumpTo(ev.clientX)
    const onUp = () => {
      track.removeEventListener('pointermove', onMove)
      track.removeEventListener('pointerup', onUp)
      track.removeEventListener('pointercancel', onUp)
    }
    track.addEventListener('pointermove', onMove)
    track.addEventListener('pointerup', onUp)
    track.addEventListener('pointercancel', onUp)
  }

  return (
    // 占位收窄到 10px 并贴近状态栏：视觉条 4px（与全局竖向滚动条同款 45% 透明度），
    // 轨道平时近乎透明避免底部出现明显横带；热区保持整行 10px 高便于点按
    // --pad-bottom 含 --shell-gap 余量，玻璃上沿 = pad-bottom - gap；
    // 再减去容器内视觉条的居中偏移 3px，使条底正好贴住底栏玻璃上沿
    <div className="shrink-0 flex items-center px-3" style={{ height: 10, marginBottom: 'calc(var(--pad-bottom) - var(--shell-gap) - 3px)' }}>
      <div className="relative w-full h-full cursor-pointer touch-none group/hbar" onPointerDown={onTrackPointerDown}>
        <div className="absolute inset-x-0 top-1/2 -translate-y-1/2 h-1 rounded-full bg-gray-300/25 dark:bg-white/[0.05] group-hover/hbar:bg-gray-300/45 dark:group-hover/hbar:bg-white/[0.1] transition-colors" />
        <div
          data-thumb
          className="absolute top-1/2 -translate-y-1/2 h-1 rounded-full bg-gray-400/45 dark:bg-gray-500/45 hover:bg-primary/60 active:bg-primary/70 transition-colors cursor-grab active:cursor-grabbing touch-none"
          style={{ width: `${state.ratio * 100}%`, minWidth: 32, left: `${state.pos * (1 - state.ratio) * 100}%` }}
          onPointerDown={onThumbPointerDown}
          onPointerMove={onThumbPointerMove}
          onPointerUp={onThumbPointerUp}
          onPointerCancel={onThumbPointerUp}
        />
      </div>
    </div>
  )
}
