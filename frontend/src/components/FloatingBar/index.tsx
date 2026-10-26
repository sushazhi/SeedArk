import { useEffect, useRef, useState, type RefObject } from 'react'
import { Eraser, FolderOpen, Pause, Play, Plus, Settings } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useAppStore } from '@/stores/appStore'
import { useHideOnScroll } from '@/hooks/useHideOnScroll'
import { useRevealPath } from '@/hooks/useRevealPath'
import { useTorrentActions } from '@/hooks/useTorrentActions'
import { usePlatform } from '@/platform'
import { cn } from '@/lib/utils'

interface Props {
  isMobile?: boolean
  onOpenAdd: () => void
  onOpenClean: () => void
  onOpenSettings?: () => void
  /** 主内容列：其中任何滚动发生都暂时收起胶囊 */
  scrollHost?: RefObject<HTMLElement | null>
}

/**
 * 底部悬浮胶囊（液态玻璃 · float 材质档）
 *
 * 只放首页拿不到的操作：添加任务、全部启停、清理已完成。
 * 刷新 / 统计已在分类标题行常驻，重复一遍等于让玻璃多压住一块列表。
 *
 * 水平居中悬浮在列表之上，且刻意不占停靠边界：它只有 56px 宽，而任何预留
 * 净空都按整条列表的宽度生效，为一颗圆钮让出全宽空带不值。让位靠
 * useHideOnScroll——滑动期间整块玻璃下滑出屏，静止时才浮在列表上。
 */
export function FloatingBar({ isMobile, onOpenAdd, onOpenClean, onOpenSettings, scrollHost }: Props) {
  const { t } = useTranslation()
  const torrents = useAppStore((s) => s.torrents)
  const downloadDir = useAppStore((s) => s.session?.downloadDir)
  const actions = useTorrentActions()
  const { can } = usePlatform()
  const revealPath = useRevealPath()
  const [expanded, setExpanded] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)
  const hidden = useHideOnScroll(scrollHost)

  // 有活动任务（排队/下载/做种中）时显示暂停，否则显示开始
  const hasActive = torrents.some((x) => x.status >= 3 && x.status <= 6)
  const hasCompleted = torrents.some((x) => x.percentDone >= 1)

  // 展开态：点击胶囊外部或按 Esc 收起
  useEffect(() => {
    if (!expanded) return
    const onPointerDown = (e: PointerEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setExpanded(false)
    }
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setExpanded(false)
    }
    document.addEventListener('pointerdown', onPointerDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('pointerdown', onPointerDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [expanded])

  // 滑走时一并收起，回来才是干净的一颗 +
  useEffect(() => {
    if (hidden) setExpanded(false)
  }, [hidden])

  const openDir = async () => {
    if (!downloadDir) return
    await revealPath(downloadDir)
  }

  const size = isMobile ? 'h-11' : 'h-10'
  const items = [
    // 「添加」一律用 Plus：与胶囊收起态的圆钮、顶栏的「添加任务」保持同一语义。
    // 曾用 Sprout，与保种台入口撞成同一个图标（移动端两处同时可见，看起来像重复）。
    { key: 'add', label: t('topbar.addTask'), icon: Plus, run: onOpenAdd },
    {
      key: 'toggleAll',
      label: hasActive ? t('action.pauseAll') : t('action.startAll'),
      icon: hasActive ? Pause : Play,
      run: () => void (hasActive ? actions.pauseAll() : actions.startAll()),
    },
    { key: 'openDir', label: t('action.openDownloadDir'), icon: FolderOpen, run: () => void openDir(), disabled: !downloadDir },
    { key: 'clean', label: t('batchClean.title'), icon: Eraser, run: onOpenClean, disabled: !hasCompleted },
    ...(onOpenSettings ? [{ key: 'settings', label: t('common.settings'), icon: Settings, run: onOpenSettings }] : []),
  ]
  // 「打开下载目录」需要宿主文件管理器能力，不支持时隐藏（与种子右键菜单保持一致）
  const visibleItems = can('fs.revealPath') ? items : items.filter((i) => i.key !== 'openDir')

  return (
    <div
      ref={rootRef}
      role="toolbar"
      aria-expanded={expanded}
      aria-label={t('floatingBar.label')}
      inert={hidden || undefined}
      className={cn(
        // 停靠边界取自实测：状态栏高度随字号设置变化，写死像素会让胶囊压上去
        'fixed z-40 left-1/2 bottom-[calc(var(--pad-bottom)-var(--shell-gap)+8px)]',
        // 玻璃胶囊：float 档材质 + 完全圆角，内层按钮同为圆角构成同心关系
        'tm-dock glass-panel-strong flex items-center rounded-full p-1.5',
        // 居中偏移与隐藏位移各自只写 --tw-translate-x / -y，
        // 共用同一条 translate 简写声明，因此可以合并在这一层
        '-translate-x-1/2',
        'transition-[transform,translate,opacity] duration-[350ms] [transition-timing-function:var(--ease-spring)]',
        // fixed 元素不受 shell 的 overflow 裁切，位移量必须覆盖"自身高 + 到视口底的偏移"
        hidden && 'translate-y-[calc(100%+var(--pad-bottom)+8px)] opacity-0',
      )}
    >
      {/* 展开手柄：+ 旋转 45° 读作 ×，同一控件承担开与合 */}
      <button
        onClick={() => setExpanded((v) => !v)}
        aria-label={expanded ? t('floatingBar.collapse') : t('floatingBar.expand')}
        className={cn(
          size, 'w-11 shrink-0 rounded-full tm-press flex items-center justify-center text-white',
          'bg-[linear-gradient(135deg,var(--brand-grad-from),var(--brand-grad-to))]',
          'shadow-[0_4px_16px_color-mix(in_srgb,var(--color-primary)_42%,transparent)]',
        )}
      >
        <Plus
          className={cn('w-5 h-5 transition-transform duration-[350ms] [transition-timing-function:var(--ease-spring)]', expanded && 'rotate-45')}
          strokeWidth={2.2}
        />
      </button>

      {visibleItems.map((item, i) => {
        const Icon = item.icon
        const collapsed = !expanded
        return (
          <button
            key={item.key}
            title={item.label}
            aria-label={item.label}
            tabIndex={collapsed ? -1 : 0}
            disabled={item.disabled}
            onClick={item.run}
            style={{
              // 展开时由内向外依次长出，收起时反向——避免整排"啪"地跳变
              transitionDelay: collapsed ? `${(visibleItems.length - i) * 22}ms` : `${60 + i * 40}ms`,
            }}
            className={cn(
              size,
              'overflow-hidden rounded-full tm-press flex items-center justify-center shrink-0',
              'transition-[width,opacity,transform,scale] duration-[350ms] [transition-timing-function:var(--ease-spring)]',
              collapsed
                ? 'w-0 opacity-0 scale-75 pointer-events-none'
                : 'w-11 opacity-100 scale-100 ml-1',
              item.disabled
                ? 'text-gray-300 dark:text-gray-600 cursor-not-allowed'
                : 'text-gray-600 dark:text-gray-300 hover:text-primary hover:bg-primary/10',
            )}
          >
            <Icon className="w-5 h-5 shrink-0" strokeWidth={1.75} />
          </button>
        )
      })}
    </div>
  )
}
