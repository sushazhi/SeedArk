// 设置弹窗的通用表单件：驱动自述的通用面板（QBSettings）与内置表单
// 共用同一套控件，抽出成独立文件避免与 index.tsx 互相 import。
import { Children, cloneElement, isValidElement, useEffect, useRef, useState, type ReactNode } from 'react'
import { ChevronDown, ChevronRight } from 'lucide-react'
import { cn } from '@/lib/utils'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

// 设置行。不用 aria-labelledby 指向标题容器：那会把 hint 提示文字一起算进名称，
// 而且目录/滑块这类一行多控件的行根本无法与单个控件建立关联。
export function Row({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  const labelled = Children.map(children, (child) => {
    if (!isValidElement<{ 'aria-label'?: string; 'aria-labelledby'?: string }>(child)) return child
    const type = child.type
    // 纯容器不具名：aria-label 挂在没有角色的节点上只是噪声
    if (typeof type === 'string' && ['div', 'span', 'p', 'svg'].includes(type)) return child
    if (child.props['aria-label'] || child.props['aria-labelledby']) return child
    return cloneElement(child, { 'aria-label': label })
  })
  return (
    // flex-wrap：窄屏下右侧定宽控件放不下时整块掉到标签下方，
    // 否则标签被挤成一字一行的竖排，行高爆炸（WebView 真机曾出现）
    <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1.5 py-1.5">
      <div className="min-w-[10rem] max-w-full">
        <span className="text-body text-gray-600 dark:text-gray-300">{label}</span>
        {hint && <p className="text-caption1 text-gray-400 mt-0.5">{hint}</p>}
      </div>
      {labelled}
    </div>
  )
}

// 数字输入（替代 antd InputNumber）
export interface NumInputProps {
  value?: number | null
  min?: number
  max?: number
  // 'any' 供不限定小数位的字段（如磁盘容量按 GB 填）；数字则按该步长校验
  step?: number | 'any'
  disabled?: boolean
  className?: string
  'aria-label'?: string
  onChange?: (v?: number) => void
}

export function NumInput({ value, min, max, step, disabled, className, onChange, ...rest }: NumInputProps) {
  return (
    <Input
      type="number"
      min={min}
      max={max}
      step={step}
      disabled={disabled}
      className={cn('h-8 w-20', className)}
      value={value === undefined || value === null ? '' : String(value)}
      onChange={(e) => {
        const raw = e.target.value
        if (raw === '') { onChange?.(undefined); return }
        const n = Number(raw)
        if (!Number.isNaN(n)) onChange?.(n)
      }}
      {...rest}
    />
  )
}

// 折叠面板：展开/折叠状态按 id 持久化，刷新或重开弹窗后保持
const SECTION_STATE_KEY = 'tm-settings-sections'

function readSectionState(): Record<string, boolean> {
  try {
    return JSON.parse(localStorage.getItem(SECTION_STATE_KEY) || '{}') as Record<string, boolean>
  } catch {
    return {}
  }
}

export function Section({ id, title, children, defaultOpen, collapsible = true, icon }: {
  id: string
  title: string
  children: ReactNode
  defaultOpen?: boolean
  // 桌面端由左侧导航定位，不再需要折叠：给某一节加折叠会让用户在「导航已经指明看哪一节」
  // 的前提下再点一次，且折叠状态与导航选中态互相打架。移动端仍是单列长滚动，保留折叠
  collapsible?: boolean
  icon?: ReactNode
}) {
  const [open, setOpen] = useState(() => readSectionState()[id] ?? !!defaultOpen)
  // 桌面端恒开；移动端才读用户/默认的折叠状态
  const expanded = collapsible ? open : true

  return (
    <details
      className="group rounded-xl border border-gray-200/40 dark:border-gray-700/30 bg-white/50 dark:bg-gray-800/40 overflow-hidden"
      open={expanded}
      onToggle={(e) => {
        if (!collapsible) return
        const next = e.currentTarget.open
        if (next === open) return
        setOpen(next)
        try {
          const all = readSectionState()
          all[id] = next
          localStorage.setItem(SECTION_STATE_KEY, JSON.stringify(all))
        } catch {
          // 持久化失败不影响交互
        }
      }}
    >
      <summary
        className={cn(
          'flex items-center gap-2 px-4 py-3 text-body font-medium select-none list-none [&::-webkit-details-marker]:hidden',
          collapsible ? 'cursor-pointer' : 'cursor-default',
        )}
      >
        {icon && <span className="shrink-0 text-gray-400 [&>svg]:w-4 [&>svg]:h-4">{icon}</span>}
        <span className="flex-1 min-w-0 truncate">{title}</span>
        {collapsible && <ChevronRight className="w-4 h-4 shrink-0 transition-transform group-open:rotate-90 text-gray-400" />}
      </summary>
      <div className="px-4 pb-4 space-y-1 border-t border-gray-100 dark:border-gray-700/40 pt-2">{children}</div>
    </details>
  )
}

// 二级折叠组：设置弹窗里「一节之内还要再分组」时才用（qBittorrent 面板由驱动自述，
// 一节可能挂十几个键）。默认收起。
export function Group({ title, children }: { title: string; children: ReactNode }) {
  const [open, setOpen] = useState(false)
  const next = !open
  return (
    <div className="space-y-0.5">
      <div
        role="button"
        tabIndex={0}
        aria-expanded={open}
        onClick={() => setOpen(next)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setOpen(next) }
        }}
        className="w-full flex items-center gap-1.5 py-1.5 text-footnote font-medium text-gray-500 dark:text-gray-400 hover:text-primary dark:hover:text-primary rounded-md px-1 -mx-1 cursor-pointer select-none transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
      >
        {open ? <ChevronDown className="w-3.5 h-3.5 shrink-0" aria-hidden /> : <ChevronRight className="w-3.5 h-3.5 shrink-0" aria-hidden />}
        <span className="truncate">{title}</span>
      </div>
      {/* 收起时整块从渲染树里摘掉：一节几十行全留在 DOM 里，桌面端向下翻要跨过大量空区 */}
      {open && <div className="space-y-0.5 pl-3 border-l border-gray-200/70 dark:border-gray-700/50">{children}</div>}
    </div>
  )
}

// 自适应滚动区：内容超出可用高度时本区滚动，否则自然撑开。
// 不用写死的 dvh——内容更矮时会留一大片空白，弹窗里看着就是"这一节底下空着半屏"。
// 可用高度 = 父级（flex 布局里已被定高的那一列）的实测高度，
// ResizeObserver 跟着窗口变化与分节切换重算。
export function ScrollArea({ children, className }: { children: ReactNode; className?: string }) {
  const ref = useRef<HTMLDivElement>(null)
  const [max, setMax] = useState<number | undefined>(undefined)

  useEffect(() => {
    const node = ref.current
    const host = node?.parentElement
    if (!node || !host) return
    const measure = () => {
      // 父级的内容盒高度：本区是它唯一的子元素，减掉父级自身的内边距即为可用高度
      const cs = getComputedStyle(host)
      const avail = host.clientHeight - parseFloat(cs.paddingTop || '0') - parseFloat(cs.paddingBottom || '0')
      if (avail > 0) setMax(Math.floor(avail))
    }
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(host)
    return () => ro.disconnect()
  }, [])

  return (
    <div ref={ref} className={cn('overflow-y-auto overscroll-contain', className)} style={max ? { maxHeight: max } : undefined}>
      {children}
    </div>
  )
}

// 左侧导航：桌面端把「节」提升为常驻入口，同一时刻只渲染选中那节的表单。
// 移动端不用它——窄屏放不下双列，仍走单列长滚动 + 折叠分节。
// 断点用 md（768）而不是 sm（640）：显示与否必须与 useResponsive 的 isMobile
// 完全同口径，否则 640–767px 之间会出现"导航已出现、右侧却按移动端长滚动渲染"
// 的两不像布局。
export interface PaneNavItem {
  id: string
  label: string
  icon: ReactNode
}

export function PaneNav({ items, active, onSelect, label }: {
  items: PaneNavItem[]
  active: string
  onSelect: (id: string) => void
  label: string
}) {
  return (
    <nav
      role="tablist"
      aria-orientation="vertical"
      aria-label={label}
      className="hidden md:flex flex-col shrink-0 w-[168px] lg:w-[188px] gap-0.5 pr-1 overflow-y-auto overscroll-contain border-r border-gray-200/60 dark:border-gray-700/40"
    >
      {items.map((it) => {
        const on = it.id === active
        return (
          <button
            key={it.id}
            type="button"
            role="tab"
            aria-selected={on}
            onClick={() => onSelect(it.id)}
            className={cn(
              'flex items-center gap-2 h-8 shrink-0 px-2.5 rounded-md text-footnote text-left transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring',
              on
                ? 'bg-primary/10 text-primary font-medium'
                : 'text-gray-600 dark:text-gray-300 hover:bg-gray-100/70 dark:hover:bg-white/5',
            )}
          >
            <span className="shrink-0 opacity-80 [&>svg]:w-3.5 [&>svg]:h-3.5">{it.icon}</span>
            <span className="truncate">{it.label}</span>
          </button>
        )
      })}
    </nav>
  )
}

// 带标签的 Select
export interface LabeledSelectProps {
  value: string
  onValueChange: (v: string) => void
  options: { value: string; label: string }[]
  className?: string
  'aria-label'?: string
}

export function SmallSelect({ value, onValueChange, options, className, 'aria-label': ariaLabel }: LabeledSelectProps) {
  return (
    <Select value={value} onValueChange={onValueChange}>
      <SelectTrigger aria-label={ariaLabel} className={cn('h-8 w-24 text-footnote', className)}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent className="glass-panel-solid">
        {options.map((o) => (
          <SelectItem key={o.value} value={o.value}>{o.label}</SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
