import { useLayoutEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useVirtualizer } from '@tanstack/react-virtual'
import { useAppStore } from '@/stores/appStore'
import { formatBytes, formatDuration, formatRatio, formatSpeed } from '@/utils/format'
import { tagColor } from '@/utils/tagColor'
import { statusColor, statusPulses } from '@/utils/status'
import type { EditMode, EditTarget, MenuCtx } from '@/components/TorrentMenu'
import { buildTorrentMenu, EditModals, TorrentMenuDropdown } from '@/components/TorrentMenu'
import { useTorrentActions } from '@/hooks/useTorrentActions'
import { useTorrentMenuHandler } from '@/hooks/useTorrentMenuHandler'
import { useResponsive } from '@/hooks/useResponsive'
import { usePlatform } from '@/platform'
import { RemoveTorrentDialog, ReplaceTrackerDialog } from '@/components/ToolsDialogs'
import { Checkbox } from '@/components/ui/checkbox'
import { ProgressBar } from '@/components/TorrentList/ProgressBar'
import { EmptyList } from '@/components/TorrentList/EmptyList'
import { ServerInline } from '@/components/TorrentList/ServerCell'
import { cn, cssVars } from '@/lib/utils'
import { MoreVertical } from 'lucide-react'
import type { Torrent } from '@/types'

// 指标分隔点：只在同一行内出现，避免换行后行首挂一个孤点
function Sep({ className }: { className?: string }) {
  return <span aria-hidden className={cn('text-gray-300 dark:text-gray-600 shrink-0', className)}>·</span>
}

// 图一风格卡片列表（桌面/移动共用）
export function GridView({ torrents, onOpenDetail, isMobile, onOpenBatchClean }: {
  torrents: Torrent[]
  onOpenDetail: (t: Torrent) => void
  isMobile?: boolean
  onOpenBatchClean?: () => void
}) {
  const { t } = useTranslation()
  const { isCoarse } = useResponsive()
  const actions = useTorrentActions()
  const { can } = usePlatform()
  const selectedIds = useAppStore((s) => s.selectedIds)
  const showCheckboxes = useAppStore((s) => s.showCheckboxes)
  const toggleSelect = useAppStore((s) => s.toggleSelect)
  const setSelection = useAppStore((s) => s.setSelection)
  const clearSelection = useAppStore((s) => s.clearSelection)
  const selectAnchorId = useAppStore((s) => s.selectAnchorId)
  const setSelectAnchor = useAppStore((s) => s.setSelectAnchor)
  const scrollTargetIds = useAppStore((s) => s.scrollTargetIds)
  const consumeScrollTarget = useAppStore((s) => s.consumeScrollTarget)
  const [editTarget, setEditTarget] = useState<EditTarget | null>(null)
  const [removeIds, setRemoveIds] = useState<number[] | null>(null)
  const [trackerOpen, setTrackerOpen] = useState(false)
  const listRef = useRef<HTMLDivElement>(null)
  const lastClickRef = useRef<{ id: number; t: number }>({ id: -1, t: 0 })

  // 虚拟滚动：卡片与表格行一样是等高列表项（差异只在元信息行是否折行），
  // 千级种子时全量挂载会拖慢首屏与每次刷新，故与桌面表格同一套虚拟化。
  // 估算高度取实测常见值，渲染出来的行再由 measureElement 逐个实测修正
  const virtualizer = useVirtualizer({
    count: torrents.length,
    getScrollElement: () => listRef.current,
    estimateSize: () => 88,
    overscan: 8,
  })

  // 从分组切回「全部」后滚回之前选中的种子；在绘制前定位，避免先闪一下旧位置。
  // 按 scrollTargetIds 的顺序（最后点选的锚点在前）定位第一个仍在新列表里的种子。
  // 虚拟化后目标行通常不在 DOM 里，只能按索引跳；未实测行的估算高度会累计出偏差，
  // 故目标行渲染出来后再按它的实际位置校正（最多三轮，收敛即停）
  useLayoutEffect(() => {
    if (scrollTargetIds.length === 0) return
    consumeScrollTarget()
    const target = scrollTargetIds.find((id) => torrents.some((t) => t.id === id))
    if (target === undefined) return
    const idx = torrents.findIndex((t) => t.id === target)
    virtualizer.scrollToIndex(idx, { align: 'center' })
    let raf = 0
    let tries = 0
    const align = () => {
      const host = listRef.current
      const el = host?.querySelector<HTMLElement>(`[data-torrent-id="${target}"]`)
      if (!host || !el || tries >= 3) return
      const delta =
        el.getBoundingClientRect().top - host.getBoundingClientRect().top - (host.clientHeight - el.offsetHeight) / 2
      if (Math.abs(delta) < 2) return
      tries += 1
      host.scrollTop += delta
      raf = requestAnimationFrame(align)
    }
    raf = requestAnimationFrame(align)
    return () => { if (raf) cancelAnimationFrame(raf) }
  }, [scrollTargetIds, torrents, consumeScrollTarget, virtualizer])

  // 所有 Hook 必须写在下面的空列表早返回之前。
  // useTorrentMenuHandler 是自定义 Hook（内部调用 useTranslation / useTorrentActions /
  // useRevealPath / useCallback），此前它排在 `torrents.length === 0` 的早返回之后：
  // 首屏列表为空时走 EmptyList 分支（Hook 少几个），XHR 返回种子后重渲染就会多出 Hook，
  // React 抛 #310（Rendered more hooks than during the previous render）并卸载整棵树 ——
  // 表现为移动端「列表一闪之后整页空白」。移动端固定走卡片视图，桌面走表格，
  // 所以这个崩溃只在移动端可见。
  const handleMenuClick = useTorrentMenuHandler({
    onOpenDetail,
    onOpenBatchClean,
    onEdit: setEditTarget,
    onRemove: (id) => setRemoveIds([id]),
    onReplaceTrackers: () => setTrackerOpen(true),
    canRevealPath: can('fs.revealPath'),
  })

  if (torrents.length === 0) {
    return <EmptyList />
  }

  const handleSelect = (torrent: Torrent, e: React.MouseEvent) => {
    // Shift 连选：从锚点到当前项整段选中（触屏无 Shift 键，不会走到这里）
    if (e.shiftKey && !e.ctrlKey && !e.metaKey) {
      const anchorId = selectAnchorId ?? torrents[0]?.id
      if (anchorId != null) {
        const a = torrents.findIndex((x) => x.id === anchorId)
        const b = torrents.findIndex((x) => x.id === torrent.id)
        const from = a < 0 ? 0 : Math.min(a, b)
        const to = a < 0 ? b : Math.max(a, b)
        setSelection(torrents.slice(from, to + 1).map((x) => x.id))
      }
      return
    }
    if (e.ctrlKey || e.metaKey) {
      toggleSelect(torrent.id)
      setSelectAnchor(torrent.id)
      return
    }
    // 触屏没有右键也没有悬停：点按即打开详情，选择交给常显的 checkbox 与 ⋮
    if (isMobile || isCoarse) {
      onOpenDetail(torrent)
      return
    }
    // 再点一次已选中的唯一项 = 取消选中；300ms 内同行两击是双击前半程，不取消
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

  // 菜单内容按需构建：每屏十几张卡片，若每次渲染都为每颗种子生成二十来个菜单项
  // （含图标元素），千级列表下每轮刷新就会造出上万对象。工厂函数只在菜单真正
  // 打开时调用一次（Radix 弹层与手写右键菜单都是打开时才渲染内容）
  const menuCtx: MenuCtx = {
    actions,
    t: (k) => t(k),
    onOpenDetail,
    canRevealPath: can('fs.revealPath'),
    onEdit: (mode: EditMode, tt: Torrent) => setEditTarget({ torrent: tt, mode }),
    onOpenBatchClean,
  }

  return (
    <div
      ref={listRef}
      className="tm-scroll h-full"
      style={{ paddingLeft: 'calc(var(--safe-left) + 0.75rem)', paddingRight: 'calc(var(--safe-right) + 0.75rem)' }}
    >
      <div className="relative max-w-5xl mx-auto" style={{ height: virtualizer.getTotalSize() }}>
        {virtualizer.getVirtualItems().map((vi) => {
          const torrent = torrents[vi.index]
          // 虚拟项在数据收缩后可能短暂越界（列表刚被过滤/删除），跳过，
          // 下一帧 virtualizer 会自行修正
          if (!torrent) return null
          const selected = selectedIds.includes(torrent.id)
          const pct = torrent.percentDone
          const dot = { color: statusColor(torrent), pulse: statusPulses(torrent) }
          const labels = torrent.labels ?? []
          const eta = torrent.eta > 0 ? `${t('card.remaining')} ${formatEtaShort(torrent.eta)}` : ''
          // 已下载大小 = 种子文件里实际持有的字节（含未校验部分）；只下载了一部分就只显示这部分
          const downloadedSize = formatBytes(Math.min(torrent.haveValid + torrent.haveUnchecked, torrent.totalSize))
          // 已完成（含做种中/暂停）：显示分享率；未完成：显示「已下载 / 总大小」
          const isDone = torrent.percentDone >= 1
          const seedingFor =
            isDone && torrent.secondsSeeding > 0 ? `${t('card.seeding')} ${formatDuration(torrent.secondsSeeding)}` : ''
          return (
            <div
              key={torrent.id}
              data-index={vi.index}
              ref={virtualizer.measureElement}
              data-torrent-id={torrent.id}
              // 卡片之间的间距落在行内边距上：绝对定位的行没有 flex gap 可用，
              // 且内边距会被 measureElement 计入行高
              className="absolute top-0 left-0 w-full pb-1.5"
              style={{ transform: `translateY(${vi.start}px)` }}
            >
              <TorrentMenuDropdown items={() => buildTorrentMenu(menuCtx, torrent)} onClick={handleMenuClick(torrent)} trigger="contextMenu" align="start">
                <div
                  onClick={(e) => handleSelect(torrent, e)}
                  onDoubleClick={() => onOpenDetail(torrent)}
                  data-selected={selected || undefined}
                  className={cn(
                    'glass-card tm-card flex items-start gap-3.5 px-3 py-2 cursor-default select-none group',
                    selected && 'z-[1]',
                  )}
                >
                  <div className="flex-1 min-w-0">
                    {/* 名称 + 标签 + 选择 */}
                    <div className="flex items-center gap-2">
                      <span className="font-medium text-body min-w-0 truncate text-gray-800 dark:text-gray-100" title={torrent.name}>
                        {torrent.error > 0 && <span className="text-red-500 mr-1">!</span>}
                        {torrent.name}
                      </span>
                      {labels.length > 0 && (
                        <span className="hidden md:flex items-center gap-1.5 shrink-0 ml-auto">
                          {labels.slice(0, 3).map((l) => {
                            const color = tagColor(l)
                            return (
                              <span
                                key={l}
                                className="tm-chip px-2 py-0.5 rounded-full border text-caption1 font-medium"
                                style={cssVars({ '--chip': color })}
                              >
                                {l}
                              </span>
                            )
                          })}
                          {labels.length > 3 && (
                            <span className="px-2 py-0.5 rounded-full text-caption1 bg-gray-100 dark:bg-gray-800 text-gray-400">
                              +{labels.length - 3}
                            </span>
                          )}
                        </span>
                      )}
                      {showCheckboxes && (
                        <span
                          className="shrink-0 tm-reveal-hover"
                          onClick={(e) => e.stopPropagation()}
                          onPointerDown={(e) => e.stopPropagation()}
                        >
                          {/* 触屏：圆形选择圈，与卡片的圆角/⋮ 圆按钮同一设计语言（桌面表格仍用方角） */}
                          <Checkbox
                            checked={selected}
                            onCheckedChange={() => { toggleSelect(torrent.id); setSelectAnchor(torrent.id) }}
                            className={(isMobile || isCoarse) ? 'rounded-full' : undefined}
                          />
                        </span>
                      )}
                      {/* ⋮ 菜单（触屏设备显示；鼠标设备用右键菜单） */}
                      <span
                        className="tm-touch-menu shrink-0"
                        onClick={(e) => e.stopPropagation()}
                        onPointerDown={(e) => e.stopPropagation()}
                      >
                        <TorrentMenuDropdown items={() => buildTorrentMenu(menuCtx, torrent)} onClick={handleMenuClick(torrent)} trigger="click">
                          <span className="tm-hug w-8 h-8 rounded-full flex items-center justify-center text-gray-400 active:text-primary active:bg-primary/10">
                            <MoreVertical className="w-4 h-4" aria-hidden />
                          </span>
                        </TorrentMenuDropdown>
                      </span>
                    </div>

                    {/* 大小 + 进度条（scaleX 驱动）+ 状态点；移动端标签靠右挤同一行。
                        进度条给保底宽度，标签再长也先截断标签，避免条被挤成一条缝 */}
                    <div className="flex items-center gap-2.5 mt-1.5">
                      <span className="tm-mono shrink-0 text-footnote text-gray-500 dark:text-gray-400">{downloadedSize}</span>
                      <ProgressBar value={pct} error={torrent.error > 0} className="flex-1 min-w-20" />
                      <span
                        className={cn('w-2 h-2 rounded-full shrink-0', dot.pulse && 'animate-pulse')}
                        style={{ backgroundColor: dot.color }}
                      />
                      {labels.length > 0 && (
                        <span className="md:hidden flex items-center justify-end gap-1.5 min-w-0 max-w-[40%] shrink">
                          {labels.slice(0, 2).map((l) => {
                            const color = tagColor(l)
                            return (
                              <span key={l} className="tm-chip border max-w-[4.5rem] truncate px-2 py-0.5 rounded-full text-caption1 font-medium" style={cssVars({ '--chip': color })}>
                                {l}
                              </span>
                            )
                          })}
                          {labels.length > 2 && <span className="text-caption1 text-gray-400 shrink-0">+{labels.length - 2}</span>}
                        </span>
                      )}
                    </div>

                    {/* 元信息：下行 / 上行 · 分享率 · 做种/剩余。
                        宽屏保持一整行紧凑排列（相邻卡片高度一致）；窄屏允许折行，
                        而不是把塞不下的部分截断——此前 flex-nowrap + overflow-hidden
                        在窄屏会把「做种时长」直接吃掉，信息静默丢失。
                        速度与分享率各自 shrink-0 保住，只有尾部的做种/剩余可折到第二行 */}
                    <div className="flex flex-wrap items-center gap-x-1.5 gap-y-0.5 md:gap-x-2.5 md:flex-nowrap text-caption1 md:text-footnote text-gray-500 dark:text-gray-400 mt-1 min-w-0">
                      <span className="shrink-0 inline-flex items-center gap-x-1.5 md:gap-x-2.5">
                        <span className="tm-mono text-green-600 dark:text-green-400">↓{formatSpeed(torrent.rateDownload)}</span>
                        <span className="tm-mono text-blue-600 dark:text-blue-400">↑{formatSpeed(torrent.rateUpload)}</span>
                      </span>
                      <span className="inline-flex items-center gap-x-1.5 md:gap-x-2.5 min-w-0 flex-wrap">
                        <Sep />
                        <span className="tm-mono shrink-0">{t('columns.ratio')} {formatRatio(torrent.uploadRatio)}</span>
                        {/* 归属下载器（仅聚合视图有值）：TR/QB 徽标 + 服务器名 */}
                        {(torrent.kind || torrent.serverName) && (
                          <>
                            <Sep />
                            <ServerInline torrent={torrent} />
                          </>
                        )}
                        {seedingFor && (
                          <>
                            <Sep className="hidden md:inline" />
                            <span className="text-gray-400 dark:text-gray-500 truncate min-w-0">{seedingFor}</span>
                          </>
                        )}
                        {eta && (
                          <>
                            <Sep className="hidden md:inline" />
                            <span className="truncate min-w-0">{eta}</span>
                          </>
                        )}
                      </span>
                    </div>
                  </div>
                </div>
              </TorrentMenuDropdown>
            </div>
          )
        })}
      </div>
      <EditModals target={editTarget} onClose={() => setEditTarget(null)} />
      <RemoveTorrentDialog open={!!removeIds} ids={removeIds ?? []} onClose={() => setRemoveIds(null)} />
      <ReplaceTrackerDialog open={trackerOpen} onClose={() => setTrackerOpen(false)} />
    </div>
  )
}

function formatEtaShort(sec: number): string {
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const s = Math.floor(sec % 60)
  if (h > 0) return `${h}h${m}m`
  if (m > 0) return `${m}m${s}s`
  return `${s}s`
}
