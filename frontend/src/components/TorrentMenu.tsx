import { useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import {
  ArrowDown,
  ArrowUp,
  Check,
  ChevronRight,
  Copy,
  Folder,
  FolderOpen,
  Link,
  Pause,
  Play,
  RefreshCw,
  Settings2,
  ShieldCheck,
  Star,
  Tags,
  Trash2,
  Zap,
  MoveDown,
  MoveUp,
  MoveUpLeft,
  Wifi,
  Pencil,
  Replace,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { sessionApi, torrentApi } from '@/api/torrent'
import { useTorrentActions } from '@/hooks/useTorrentActions'
import { usePlatform } from '@/platform'
import { useAppStore } from '@/stores/appStore'
import type { Torrent } from '@/types'
import { toast } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { TagInput } from '@/components/TagInput'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'

export type EditMode = 'path' | 'labels' | 'trackers' | 'rename' | 'other'

export interface EditTarget {
  torrent: Torrent
  mode: EditMode
}

export interface MenuCtx {
  actions: ReturnType<typeof useTorrentActions>
  t: (key: string) => string
  onOpenDetail: (t: Torrent) => void
  onEdit: (mode: EditMode, t: Torrent) => void
  // 宿主支持「在文件管理器中定位目录」时才显示该项
  canRevealPath?: boolean
  // 打开「批量清理已完成」对话框（全局操作，入口放在种子右键菜单底部）
  onOpenBatchClean?: () => void
}

export interface MenuItem {
  key?: string
  label?: string
  icon?: ReactNode
  disabled?: boolean
  checked?: boolean
  type?: 'divider'
  children?: MenuItem[]
}

// 图标配色，对齐 .ref-transmission-web 的 rowMenu 视觉
const primaryIcon = (el: ReactNode) => <span className="text-primary">{el}</span>
const errorIcon = (el: ReactNode) => <span className="text-red-500">{el}</span>
const infoIcon = (el: ReactNode) => <span className="text-blue-400">{el}</span>

// 构建种子操作菜单（桌面右键 / 移动长按共用），结构对齐 .ref-transmission-web
export function buildTorrentMenu(ctx: MenuCtx, torrent: Torrent): MenuItem[] {
  const { t } = ctx
  const isActive = torrent.status === 4 || torrent.status === 6
  return [
    { key: 'startNow', label: t('action.startNow'), icon: primaryIcon(<Zap className="w-4 h-4" />) },
    { key: 'start', label: t('action.start'), icon: primaryIcon(<Play className="w-4 h-4" />), disabled: isActive },
    { key: 'stop', label: t('action.stop'), icon: primaryIcon(<Pause className="w-4 h-4" />), disabled: !isActive },
    { key: 'verify', label: t('action.verify'), icon: primaryIcon(<ShieldCheck className="w-4 h-4" />) },
    { key: 'remove', label: t('action.remove'), icon: errorIcon(<Trash2 className="w-4 h-4" />) },
    { type: 'divider' },
    { key: 'reannounce', label: t('action.reannounce'), icon: primaryIcon(<RefreshCw className="w-4 h-4" />) },
    { key: 'path', label: t('action.changePath'), icon: primaryIcon(<FolderOpen className="w-4 h-4" />) },
    { key: 'rename', label: t('action.rename'), icon: primaryIcon(<Pencil className="w-4 h-4" />) },
    ...(ctx.canRevealPath
      ? [{ key: 'openDir', label: t('action.openDir'), icon: primaryIcon(<Folder className="w-4 h-4" />) }]
      : []),
    { type: 'divider' },
    { key: 'copyName', label: t('action.copyName'), icon: primaryIcon(<Copy className="w-4 h-4" />) },
    { key: 'copyPath', label: t('action.copyPath'), icon: primaryIcon(<Folder className="w-4 h-4" />) },
    { key: 'copyMagnet', label: t('action.copyMagnet'), icon: primaryIcon(<Link className="w-4 h-4" />) },
    { type: 'divider' },
    { key: 'labels', label: t('action.editLabels'), icon: primaryIcon(<Tags className="w-4 h-4" />) },
    {
      key: 'queue',
      label: t('action.queue'),
      icon: primaryIcon(<Star className="w-4 h-4" />),
      children: [
        { key: 'queue:top', label: t('action.queueTop'), icon: infoIcon(<MoveUpLeft className="w-4 h-4" />) },
        { key: 'queue:up', label: t('action.queueUp'), icon: infoIcon(<MoveUp className="w-4 h-4" />) },
        { key: 'queue:down', label: t('action.queueDown'), icon: infoIcon(<MoveDown className="w-4 h-4" />) },
        { key: 'queue:bottom', label: t('action.queueBottom'), icon: infoIcon(<MoveDown className="w-4 h-4 rotate-180" />) },
      ],
    },
    { type: 'divider' },
    { key: 'trackers', label: t('action.editTrackers'), icon: primaryIcon(<Wifi className="w-4 h-4" />) },
    { key: 'replaceTrackers', label: t('replaceTracker.title'), icon: primaryIcon(<Replace className="w-4 h-4" />) },
    { key: 'other', label: t('action.other'), icon: primaryIcon(<Settings2 className="w-4 h-4" />) },
    ...(ctx.onOpenBatchClean
      ? [
          { type: 'divider' as const },
          { key: 'deleteCompleted', label: t('action.deleteCompleted'), icon: errorIcon(<Trash2 className="w-4 h-4" />) },
        ]
      : []),
  ]
}

// ========== 渲染菜单项（递归支持子菜单） ==========
function renderItems(items: MenuItem[], onClick: (key: string) => void) {
  return items.map((item, idx) => {
    if (item.type === 'divider') return <DropdownMenuSeparator key={`div-${idx}`} className="my-0.5" />
    if (item.children) {
      return (
        <DropdownMenuSub key={item.key}>
          <DropdownMenuSubTrigger className="text-footnote gap-2">
            {item.icon}
            {item.label}
          </DropdownMenuSubTrigger>
          <DropdownMenuSubContent className="glass-panel-solid min-w-36">
            {renderItems(item.children, onClick)}
          </DropdownMenuSubContent>
        </DropdownMenuSub>
      )
    }
    return (
      <DropdownMenuItem
        key={item.key}
        disabled={item.disabled}
        className="text-footnote gap-2"
        onSelect={(e) => { e.preventDefault(); onClick(item.key as string) }}
      >
        {item.icon}
        {item.label}
        {item.checked && <Check className="w-3.5 h-3.5 text-primary ml-auto" strokeWidth={2.5} />}
      </DropdownMenuItem>
    )
  })
}

// 菜单行样式：主菜单、子菜单入口、子菜单项共用一份。
// 子菜单会 portal 到 body，写成三份时最容易漂移出「队列操作那几项比别的小」这类差异。
// 危险操作不整行标红（红字观感像换字体），红色只出现在图标上
const ctxRow = (opts?: { disabled?: boolean }) =>
  cn(
    'w-full flex items-center gap-2 px-2.5 py-1.5 text-footnote rounded-lg transition-colors text-left focus-visible:outline-none',
    'text-gray-700 dark:text-gray-200 hover:bg-primary/10 hover:text-primary focus-visible:bg-primary/10 focus-visible:text-primary',
    opts?.disabled && 'opacity-40 pointer-events-none',
  )
// 分隔线：菜单行本身已有内边距，再给 4px 外边距会把相邻两组推得过开
const ctxDivider = 'h-px my-0.5 bg-white/60 dark:bg-white/10'

// ========== 手写右键菜单（跟随鼠标位置，视口内自动翻转） ==========
export function FloatingContextMenu({ pos, items, onPick, onClose }: {
  pos: { x: number; y: number }
  items: MenuItem[]
  onPick: (key: string) => void
  onClose: () => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  const [style, setStyle] = useState<CSSProperties>({ left: pos.x, top: pos.y })

  // 初次渲染后按实际尺寸翻转，避免超出视口
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const rect = el.getBoundingClientRect()
    // iOS 键盘弹出时 innerHeight 不变、visualViewport 才反映可见区域
    const vw = window.visualViewport?.width ?? window.innerWidth
    const vh = window.visualViewport?.height ?? window.innerHeight
    const M = 8
    const flipX = pos.x + rect.width > vw - M ? pos.x - rect.width : pos.x
    const flipY = pos.y + rect.height > vh - M ? pos.y - rect.height : pos.y
    // 两侧都做夹取：手机上长按落点常贴着屏幕右缘，只翻转不夹取仍会溢出
    setStyle({
      left: Math.min(Math.max(M, flipX), Math.max(M, vw - rect.width - M)),
      top: Math.min(Math.max(M, flipY), Math.max(M, vh - rect.height - M)),
      // 入场缩放从长按落点长出：未翻转的方向取贴近落点的一侧为原点
      transformOrigin: `${flipX === pos.x ? 'left' : 'right'} ${flipY === pos.y ? 'top' : 'bottom'}`,
    })
  }, [pos])

  useEffect(() => {
    const close = () => onClose()
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    // 滚动/触屏滑动仅当发生在菜单外部时关闭，菜单内部滚动不受影响
    const onWheel = (e: WheelEvent | TouchEvent) => {
      if (!ref.current?.contains(e.target as Node)) onClose()
    }
    // pointerdown 同时覆盖鼠标与手指：mousedown 在触屏上迟到，会让菜单关不掉
    window.addEventListener('pointerdown', close)
    window.addEventListener('wheel', onWheel, { passive: true })
    window.addEventListener('touchmove', onWheel, { passive: true })
    window.addEventListener('keydown', onKey)
    window.addEventListener('blur', close)
    return () => {
      window.removeEventListener('pointerdown', close)
      window.removeEventListener('wheel', onWheel)
      window.removeEventListener('touchmove', onWheel)
      window.removeEventListener('keydown', onKey)
      window.removeEventListener('blur', close)
    }
  }, [onClose])

  return (
    <div
      ref={ref}
      onPointerDown={(e) => e.stopPropagation()}
      // pointer-events-auto：Radix 弹层（Sheet/Dialog）打开期间会给 body 设 pointer-events:none
      // 防滚动穿透，portal 到 body 的本菜单会继承到；不显式恢复的话整个菜单点不到、误触底下层
      // animate-tm-pop-in：此前瞬开瞬闭，与 Radix 弹层的 pop 语言不齐；缩放原点由上方 effect 按落点写入
      className="tm-ctx pointer-events-auto fixed z-[100] glass-panel-strong min-w-44 max-h-[80dvh] overflow-y-auto overscroll-contain rounded-xl p-1 animate-tm-pop-in"
      style={style}
    >
      {items.map((item, idx) => {
        if (item.type === 'divider') {
          return <div key={`div-${idx}`} className={ctxDivider} />
        }
        if (item.children) {
          return <CtxSubMenu key={item.key} item={item} onPick={onPick} />
        }
        return (
          <button
            key={item.key}
            disabled={item.disabled}
            onClick={() => onPick(item.key as string)}
            className={ctxRow({ disabled: item.disabled })}
          >
            {item.icon}
            {item.label}
            {item.checked && <Check className="w-3.5 h-3.5 text-primary ml-auto shrink-0" strokeWidth={2.5} />}
          </button>
        )
      })}
    </div>
  )
}

// 子菜单：hover 或点击展开（触屏可用点击）；Portal 到 body 避免被主菜单 overflow 裁剪
function CtxSubMenu({ item, onPick }: { item: MenuItem; onPick: (key: string) => void }) {
  const [open, setOpen] = useState(false)
  const [pos, setPos] = useState({ x: 0, y: 0 })
  const btnRef = useRef<HTMLButtonElement>(null)
  const timer = useRef<number | null>(null)
  const closeSoon = () => {
    if (timer.current) window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => setOpen(false), 150)
  }
  const cancelClose = () => {
    if (timer.current) {
      window.clearTimeout(timer.current)
      timer.current = null
    }
  }
  // 卸载时清理待执行的关闭定时器，避免对已卸载组件 setState
  useEffect(() => () => {
    if (timer.current) window.clearTimeout(timer.current)
  }, [])
  const openMenu = () => {
    cancelClose()
    const rect = btnRef.current?.getBoundingClientRect()
    if (rect) setPos({ x: rect.right, y: rect.top })
    setOpen(true)
  }
  return (
    <div
      className="relative"
      onMouseEnter={openMenu}
      onMouseLeave={closeSoon}
    >
      <button
        ref={btnRef}
        onClick={(e) => {
          e.stopPropagation()
          if (open) {
            cancelClose()
            setOpen(false)
          } else {
            openMenu()
          }
        }}
        className={ctxRow()}
      >
        {item.icon}
        {item.label}
        <ChevronRight className="w-3.5 h-3.5 ml-auto" />
      </button>
      {open && createPortal(
        // 定位放在 SubMenuPositioned（fixed + 计算后的 left/top），
        // 玻璃样式放在其内部普通 div：backdrop-filter 会为固定定位后代创建包含块，
        // 若外层带玻璃样式且不设坐标，子菜单会被定位到视口外
        <SubMenuPositioned pos={pos}>
          <div
            className="glass-panel-solid min-w-36 rounded-xl p-1"
            onMouseEnter={cancelClose}
            onMouseLeave={closeSoon}
          >
            {item.children?.map((child, idx) => {
              if (child.type === 'divider') {
                return <div key={`div-${idx}`} className={ctxDivider} />
              }
              return (
                <button
                  key={child.key}
                  disabled={child.disabled}
                  onClick={() => onPick(child.key as string)}
                  className={ctxRow({ disabled: child.disabled })}
                >
                  {child.icon}
                  {child.label}
                  {child.checked && <Check className="w-3.5 h-3.5 text-primary ml-auto shrink-0" strokeWidth={2.5} />}
                </button>
              )
            })}
          </div>
        </SubMenuPositioned>,
        document.body,
      )}
    </div>
  )
}

// 子菜单位置：渲染后按实际尺寸翻转，避免超出视口
function SubMenuPositioned({ pos, children }: { pos: { x: number; y: number }; children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null)
  const [style, setStyle] = useState<CSSProperties>({ left: pos.x, top: pos.y })
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const rect = el.getBoundingClientRect()
    const M = 8
    const x = pos.x + rect.width > window.innerWidth - M ? Math.max(M, pos.x - rect.width) : pos.x
    const y = pos.y + rect.height > window.innerHeight - M ? Math.max(M, window.innerHeight - rect.height - M) : pos.y
    setStyle({ left: x, top: y })
  }, [pos])
  return (
    <div
      ref={ref}
      onMouseDown={(e) => e.stopPropagation()}
      // tm-ctx：带上主菜单的标记类，子菜单才会吃到「触屏菜单行最小高度」等适配，
      // 否则它 portal 到 body 后行高只有 28px，比主菜单矮一截
      className="tm-ctx fixed z-[110] min-w-36"
      style={style}
    >
      {children}
    </div>
  )
}

// ========== 长按 / 右键锚点 ==========
// 长按阈值：短于系统的选择/菜单手势，长于误触
const LONG_PRESS_MS = 500
// 落点抖动容差：超过即认为用户是在滑动而不是长按
const LONG_PRESS_SLACK = 10

function ContextMenuAnchor({ items, onClick, children }: {
  items: MenuItem[]
  onClick: (key: string) => void
  children: ReactNode
}) {
  const [open, setOpen] = useState(false)
  const [pos, setPos] = useState({ x: 0, y: 0 })
  const timer = useRef<number | null>(null)
  const start = useRef<{ x: number; y: number } | null>(null)
  // 原生 contextmenu 与自实现长按在 Android 上会先后到达，用它去重
  const firedAt = useRef(0)

  const cancel = () => {
    if (timer.current) {
      window.clearTimeout(timer.current)
      timer.current = null
    }
    start.current = null
  }
  const show = (x: number, y: number) => {
    firedAt.current = Date.now()
    setPos({ x, y })
    setOpen(true)
  }
  useEffect(() => cancel, [])

  return (
    <>
      <div
        onContextMenu={(e) => {
          e.preventDefault()
          cancel()
          if (Date.now() - firedAt.current < 800) return
          show(e.clientX, e.clientY)
        }}
        onPointerDown={(e) => {
          // 鼠标交给 contextmenu 事件，触屏与手写笔才有长按语义
          if (e.pointerType === 'mouse' || e.button !== 0) return
          cancel()
          start.current = { x: e.clientX, y: e.clientY }
          const { clientX, clientY } = e
          timer.current = window.setTimeout(() => {
            timer.current = null
            start.current = null
            show(clientX, clientY)
          }, LONG_PRESS_MS)
        }}
        onPointerMove={(e) => {
          const from = start.current
          if (!timer.current || !from) return
          if (Math.abs(e.clientX - from.x) > LONG_PRESS_SLACK || Math.abs(e.clientY - from.y) > LONG_PRESS_SLACK) cancel()
        }}
        onPointerUp={cancel}
        onPointerCancel={cancel}
        onClickCapture={(e) => {
          // 长按松手后浏览器还会补一次 click：吞掉它，否则详情面板会盖在刚弹出的菜单上
          if (Date.now() - firedAt.current < 800) {
            e.preventDefault()
            e.stopPropagation()
          }
        }}
      >
        {children}
      </div>
      {open && createPortal(
        <FloatingContextMenu
          pos={pos}
          items={items}
          onPick={(key) => { setOpen(false); onClick(key) }}
          onClose={() => setOpen(false)}
        />,
        document.body,
      )}
    </>
  )
}

// ========== 种子菜单下拉（桌面右键 / 移动长按或点 ⋮） ==========
export function TorrentMenuDropdown({
  items,
  onClick,
  trigger = 'click',
  children,
  align = 'end',
}: {
  items: MenuItem[]
  onClick: (key: string) => void
  trigger?: 'click' | 'contextMenu'
  children: ReactNode
  align?: 'start' | 'center' | 'end'
}) {
  const [open, setOpen] = useState(false)

  if (trigger === 'contextMenu') {
    return <ContextMenuAnchor items={items} onClick={onClick}>{children}</ContextMenuAnchor>
  }

  return (
    <DropdownMenu open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger asChild>
        <div onClick={(e) => e.stopPropagation()}>
          {children}
        </div>
      </DropdownMenuTrigger>
      {/* 高度由基类的 --radix-dropdown-menu-content-available-height 约束，不再叠 vh 上限 */}
      <DropdownMenuContent className="glass-panel-strong min-w-44 overflow-y-auto" align={align} sideOffset={4}>
        {/* 选中后显式关闭：renderItems 内对 onSelect 调用了 preventDefault（Radix 会因此不自动关闭） */}
        {renderItems(items, (key) => { setOpen(false); onClick(key) })}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

// ========== 数字输入（替代 antd InputNumber） ==========
interface NumInputProps {
  value?: number | null
  min?: number
  step?: number
  disabled?: boolean
  placeholder?: string
  className?: string
  onChange?: (v?: number) => void
}

function NumInput({ value, min, step, disabled, placeholder, className, onChange }: NumInputProps) {
  return (
    <Input
      type="number"
      min={min}
      step={step}
      disabled={disabled}
      placeholder={placeholder}
      className={cn('h-8 w-24 text-footnote', className)}
      value={value === undefined || value === null ? '' : String(value)}
      onChange={(e) => {
        const raw = e.target.value
        if (raw === '') { onChange?.(undefined); return }
        const n = Number(raw)
        if (!Number.isNaN(n)) onChange?.(n)
      }}
    />
  )
}

// ========== 分段单选（替代 antd Radio.Group） ==========
function Segmented<T extends string | number>({ value, options, onChange }: {
  value: T
  options: { value: T; label: string }[]
  onChange: (v: T) => void
}) {
  return (
    <div className="inline-flex rounded-md border border-input bg-muted/50 p-0.5">
      {options.map((o) => (
        <button
          key={String(o.value)}
          type="button"
          onClick={() => onChange(o.value)}
          className={cn(
            'px-2.5 py-1 text-footnote rounded transition-colors',
            value === o.value
              ? 'bg-primary text-primary-foreground shadow-sm'
              : 'text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-200',
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  )
}

// ========== 编辑弹窗（路径/标签/Tracker/重命名/限速等） ==========
export function EditModals({ target, onClose }: { target: EditTarget | null; onClose: () => void }) {
  const { t } = useTranslation()
  const { can, pickFolder } = usePlatform()
  // 全库已有标签（供编辑标签时下拉选择，对齐 .ref-transmission-web 的 labelsOptions）
  const allTorrents = useAppStore((s) => s.torrents)
  // 当前下载器能力自述：不支持的项在限速弹窗里整项隐藏
  const caps = useAppStore((s) => s.session?.caps)
  const allLabels = useMemo(
    () => Array.from(new Set(allTorrents.flatMap((x) => x.labels ?? []))).sort((a, b) => a.localeCompare(b, 'zh')),
    [allTorrents],
  )
  const [path, setPath] = useState('')
  const [move, setMove] = useState(true)
  const [labels, setLabels] = useState<string[]>([])
  const [trackers, setTrackers] = useState<string[]>([])
  const [renameName, setRenameName] = useState('')
  const [priority, setPriority] = useState(0)
  const [sequential, setSequential] = useState(false)
  const [honor, setHonor] = useState(false)
  const [seedRatioMode, setSeedRatioMode] = useState(0)
  const [seedRatioLimit, setSeedRatioLimit] = useState<number | null>(null)
  const [dlEnabled, setDlEnabled] = useState(false)
  const [dlLimit, setDlLimit] = useState<number | null>(null)
  const [ulEnabled, setUlEnabled] = useState(false)
  const [ulLimit, setUlLimit] = useState<number | null>(null)
  const [peerLimit, setPeerLimit] = useState<number | null>(null)
  const [seedIdleEnabled, setSeedIdleEnabled] = useState(false)
  const [seedIdleLimit, setSeedIdleLimit] = useState<number | null>(null)
  // 带宽组（Transmission 4.x）：该种子归属的组 + 全库可用组列表
  const [torrentGroups, setTorrentGroups] = useState<string[]>([])
  const [allGroups, setAllGroups] = useState<string[]>([])
  const [loading, setLoading] = useState(false)
  const [fetching, setFetching] = useState(false)

  // 打开时初始化
  useEffect(() => {
    if (!target) return
    // 竞态保护：target 切换/关闭后，先前的详情请求不得再写入状态
    let cancelled = false
    const { torrent, mode } = target
    if (mode === 'path') {
      setPath(torrent.downloadDir || '')
      setMove(true)
    } else if (mode === 'labels') {
      setLabels([...(torrent.labels || [])])
    } else if (mode === 'trackers') {
      setTrackers([])
      setFetching(true)
      torrentApi
        .detail(torrent.id)
        .then((d) => { if (!cancelled) setTrackers((d.trackers ?? []).map((tr) => tr.announce)) })
        .catch(() => { if (!cancelled) toast.error(t('toast.loadFailed')) })
        .finally(() => { if (!cancelled) setFetching(false) })
    } else if (mode === 'rename') {
      setRenameName(torrent.name)
    } else if (mode === 'other') {
      setPriority(torrent.bandwidthPriority ?? 0)
      setSequential(torrent.sequentialDownload || false)
      setHonor(torrent.honorsSessionLimits || false)
      setSeedRatioMode(torrent.seedRatioMode ?? 0)
      setSeedRatioLimit(torrent.seedRatioLimit ?? null)
      setDlEnabled(torrent.downloadLimited || false)
      setDlLimit(torrent.downloadLimit ?? null)
      setUlEnabled(torrent.uploadLimited || false)
      setUlLimit(torrent.uploadLimit ?? null)
      setPeerLimit(torrent.peerLimit || null)
      setSeedIdleEnabled((torrent.seedIdleLimit ?? 0) > 0)
      setSeedIdleLimit(torrent.seedIdleLimit || null)
      setTorrentGroups(torrent.groups ?? [])
      // 列表不含 sequentialDownload / groups 的完整口径，拉取详情补全
      torrentApi.detail(torrent.id).then((d) => {
        if (cancelled) return
        setSequential(d.sequentialDownload || false)
        if (d.groups) setTorrentGroups(d.groups)
      }).catch(() => {})
      // 带宽组列表（Transmission 4.x；旧版本返回失败时静默隐藏该区块）
      if (useAppStore.getState().session?.caps?.bandwidthGroups !== false) {
        sessionApi.groups().then((gs) => { if (!cancelled) setAllGroups(gs.map((g) => g.name)) }).catch(() => {})
      }
    }
    return () => { cancelled = true }
  }, [target, t])

  if (!target) return null
  const { torrent, mode } = target

  const save = async () => {
    setLoading(true)
    try {
      if (mode === 'path') {
        await torrentApi.move(torrent.id, path, move)
      } else if (mode === 'labels') {
        await torrentApi.update(torrent.id, { labels })
      } else if (mode === 'trackers') {
        await torrentApi.update(torrent.id, { trackerList: trackers.filter((x) => x.trim()) })
      } else if (mode === 'rename') {
        if (!renameName.trim()) {
          toast.warning(t('action.rename'))
          return
        }
        // 顶层重命名：path 传当前名称
        await torrentApi.rename(torrent.id, torrent.name, renameName.trim())
      } else if (mode === 'other') {
        const body: Record<string, unknown> = {}
        body.bandwidthPriority = priority
        body.sequentialDownload = sequential
        body.honorsSessionLimits = honor
        body.seedRatioMode = seedRatioMode
        if (seedRatioMode === 1 && seedRatioLimit != null) body.seedRatioLimit = seedRatioLimit
        body.downloadLimited = dlEnabled
        if (dlEnabled && dlLimit != null) body.downloadLimit = dlLimit
        body.uploadLimited = ulEnabled
        if (ulEnabled && ulLimit != null) body.uploadLimit = ulLimit
        if (peerLimit != null && peerLimit >= 0) body.peerLimit = peerLimit
        body.seedIdleMode = seedIdleEnabled ? 1 : 0
        if (seedIdleEnabled && seedIdleLimit != null) body.seedIdleLimit = seedIdleLimit
        // 仅在带宽组列表可用（Transmission 4.x）时才提交组归属：
        // 旧版本或拉取失败时发送空数组会意外清空已有归属
        if (allGroups.length > 0) body.groups = torrentGroups
        await torrentApi.update(torrent.id, body)
      }
      toast.success(t('toast.updated'))
      onClose()
    } catch {
      // 拦截器已提示
    } finally {
      setLoading(false)
    }
  }

  const titleMap: Record<EditMode, string> = {
    path: t('action.changePath'),
    labels: t('action.editLabels'),
    trackers: t('action.editTrackers'),
    rename: t('action.rename'),
    other: t('action.other'),
  }

  const row = 'flex items-center justify-between py-1.5'
  const label = 'text-body text-gray-600 dark:text-gray-300'

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{titleMap[mode]}</DialogTitle>
        </DialogHeader>

        <div className="space-y-3">
          {mode === 'path' && (
            <div className="space-y-3">
              <div className="flex gap-2">
                <Input value={path} onChange={(e) => setPath(e.target.value)} placeholder="/downloads" />
                {can('fs.pickFolder') && (
                  <Button
                    variant="outline"
                    onClick={async () => {
                      const p = await pickFolder()
                      if (p) setPath(p)
                    }}
                  >
                    {t('action.selectDir')}
                  </Button>
                )}
              </div>
              <label className="flex items-center gap-2 text-body text-gray-600 dark:text-gray-300 cursor-pointer">
                <input type="checkbox" checked={move} onChange={(e) => setMove(e.target.checked)} />
                {t('action.moveData')}
              </label>
            </div>
          )}
          {mode === 'labels' && (
            <TagInput
              key={`labels-${torrent.id}`}
              value={labels}
              onChange={setLabels}
              placeholder={t('action.editLabels')}
              suggestions={allLabels}
            />
          )}
          {mode === 'trackers' && (
            <div className="space-y-2">
              <textarea
                rows={8}
                autoCapitalize="off"
                autoCorrect="off"
                spellCheck={false}
                value={trackers.join('\n')}
                onChange={(e) => setTrackers(e.target.value.split('\n'))}
                placeholder="http://tracker1/announce"
                className="w-full rounded-md border border-input bg-transparent px-3 py-2 text-body shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              />
              <div className="text-footnote text-gray-400">{fetching ? t('common.loading') : t('common.eachLineTier')}</div>
            </div>
          )}
          {mode === 'rename' && (
            <div className="space-y-2">
              <Input value={renameName} onChange={(e) => setRenameName(e.target.value)} placeholder={t('action.rename')} />
              <div className="text-footnote text-gray-400">{t('rename.hint')}</div>
            </div>
          )}
          {mode === 'other' && (
            <div className="space-y-2">
              {caps?.perTorrentLimits !== false && (
                <>
                  <div className={row}>
                    <span className={label}>{t('action.priority')}</span>
                    <Segmented
                      value={priority}
                      options={[
                        { value: 1, label: t('action.priorityHigh') },
                        { value: 0, label: t('action.priorityNormal') },
                        { value: -1, label: t('action.priorityLow') },
                      ]}
                      onChange={setPriority}
                    />
                  </div>
                  <div className={row}>
                    <span className={label}>{t('limits.honorSession')}</span>
                    <Switch checked={honor} onCheckedChange={setHonor} />
                  </div>
                </>
              )}
              <div className={row}>
                <span className={label}>{t('limits.sequential')}</span>
                <Switch checked={sequential} onCheckedChange={setSequential} />
              </div>
              {allGroups.length > 0 && (
                <div className={row}>
                  <span className={label}>{t('limits.bandwidthGroup')}</span>
                  <div className="flex flex-wrap justify-end gap-1.5 max-w-[60%]">
                    {allGroups.map((g) => {
                      const active = torrentGroups.includes(g)
                      return (
                        <button
                          key={g}
                          type="button"
                          onClick={() =>
                            setTorrentGroups((prev) => (prev.includes(g) ? prev.filter((x) => x !== g) : [...prev, g]))
                          }
                          className={cn(
                            'px-2 py-0.5 text-footnote rounded-full border transition-colors',
                            active
                              ? 'bg-primary text-primary-foreground border-primary'
                              : 'text-gray-500 dark:text-gray-400 border-input hover:border-gray-400',
                          )}
                        >
                          {g}
                        </button>
                      )
                    })}
                  </div>
                </div>
              )}
              <div className={row}>
                <span className={label}>{t('limits.seedRatio')}</span>
                <div className="flex items-center gap-2">
                  <Select value={String(seedRatioMode)} onValueChange={(v) => setSeedRatioMode(Number(v))}>
                    <SelectTrigger className="h-8 w-28 text-footnote">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent className="glass-panel-solid">
                      <SelectItem value="0">{t('limits.ratioGlobal')}</SelectItem>
                      <SelectItem value="1">{t('limits.ratioTorrent')}</SelectItem>
                      <SelectItem value="2">{t('limits.ratioUnlimited')}</SelectItem>
                    </SelectContent>
                  </Select>
                  <NumInput value={seedRatioLimit} min={0} step={0.1} disabled={seedRatioMode !== 1} placeholder="0.0" onChange={(v) => setSeedRatioLimit(v ?? null)} />
                </div>
              </div>
              <div className={row}>
                <span className={label}>{t('limits.download')}</span>
                <div className="flex items-center gap-2">
                  <NumInput value={dlLimit} min={0} disabled={!dlEnabled} placeholder="KB/s" onChange={(v) => setDlLimit(v ?? null)} />
                  <Switch checked={dlEnabled} onCheckedChange={setDlEnabled} />
                </div>
              </div>
              <div className={row}>
                <span className={label}>{t('limits.upload')}</span>
                <div className="flex items-center gap-2">
                  <NumInput value={ulLimit} min={0} disabled={!ulEnabled} placeholder="KB/s" onChange={(v) => setUlLimit(v ?? null)} />
                  <Switch checked={ulEnabled} onCheckedChange={setUlEnabled} />
                </div>
              </div>
              {caps?.peerLimit !== false && (
                <div className={row}>
                  <span className={label}>{t('limits.peers')}</span>
                  {/* 连接数 0 = 不连接任何 Peer（并非不限），因此最小值为 1；留空表示不修改 */}
                  <NumInput value={peerLimit} min={1} placeholder={t('limits.unlimitedHint')} onChange={(v) => setPeerLimit(v ?? null)} />
                </div>
              )}
              <div className={row}>
                <span className={label}>{t('limits.seedIdle')}</span>
                <div className="flex items-center gap-2">
                  <NumInput value={seedIdleLimit} min={0} disabled={!seedIdleEnabled} placeholder="min" onChange={(v) => setSeedIdleLimit(v ?? null)} />
                  <Switch checked={seedIdleEnabled} onCheckedChange={setSeedIdleEnabled} />
                </div>
              </div>
              <div className="text-footnote text-gray-400">{t('limits.tip')}</div>
            </div>
          )}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>{t('common.cancel')}</Button>
          {/* fetching 期间禁止保存：否则会把尚未加载的 tracker 列表当空数组提交，清空全部 tracker */}
          <Button disabled={loading || fetching} onClick={save}>{loading || fetching ? t('common.loading') : t('common.confirm')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
