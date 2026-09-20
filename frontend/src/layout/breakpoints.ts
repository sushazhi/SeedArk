import type { ColumnConfig } from '@/types'

/**
 * 五档布局系统：档位以「内容区可用宽度」为基准，而不是视口宽度。
 * 纯逻辑，不碰 DOM —— 测量在 hooks/useLayoutRegion.ts，消费在 layout/layoutStore.ts。
 */
export type Tier = 'xs' | 'sm' | 'md' | 'lg' | 'xl'
export type SidebarForm = 'drawer' | 'rail' | 'full'
export type DetailVariant = 'sheet' | 'dialog' | 'pane'

const ORDER: Tier[] = ['xs', 'sm', 'md', 'lg', 'xl']

/** 各档下界（内容区宽度 px） */
export const TIER_FLOORS: Record<Tier, number> = { xs: 0, sm: 720, md: 1080, lg: 1600, xl: 2240 }

/** 跨档迟滞：升档要多走 48px、降档要少退 48px，拖分隔条压线时不会来回翻 */
export const HYSTERESIS = 48
export const HYSTERESIS_NARROW = 12

export const RAIL_W = 64
export const SIDEBAR_RANGE: [number, number] = [200, 400]
export const CHECKBOX_GUTTER = 32
export const NAME_MIN_W = 180
export const DEFAULT_COL_W = 100

/** 详情右栏：宽度按档位取；挂载条件见 computeLayout 里的像素闸门 */
export const PANE_W = { lg: 440, xl: 520 } as const
/** 挂上右栏后，列表区至少还得留出一个能读的下界 */
export const PANE_MIN_LIST = TIER_FLOORS.md

export const GRID_COLS: Record<Tier, number> = { xs: 1, sm: 1, md: 2, lg: 3, xl: 4 }
/** 密度刻度：乘在 --text-* 令牌上，刻意不参与任何宽度算术（见 --tm-scale） */
export const TIER_SCALE: Record<Tier, number> = { xs: 1, sm: 1, md: 0.94, lg: 0.9, xl: 0.86 }
export const ROW_HEIGHT: Record<Tier, { single: number; double: number }> = {
  xs: { single: 48, double: 68 },
  sm: { single: 48, double: 68 },
  md: { single: 48, double: 68 },
  lg: { single: 48, double: 68 },
  xl: { single: 44, double: 62 },
}

/**
 * 从"必留"到"最先让位"的列序。它同时承担两件事：
 * 前 N 项构成各档的列上限，让位顺序也按它来（与用户自己排的显示顺序无关，行为才可预测）。
 */
export const COLUMN_PRIORITY: string[] = [
  'name', 'progress', 'status', 'size', 'download', 'ratio', 'label',
  'upload', 'eta', 'secondsSeeding', 'priority', 'tracker',
  'peers', 'error', 'limits', 'fileCount', 'downloadDir', 'server',
  'uploaded', 'downloaded', 'added', 'doneDate', 'queuePosition', 'hashString',
]
/** 各档允许的列数上限（超出预算只会让位更靠后的列） */
export const TIER_COLUMN_CEILING: Record<Tier, number> = { xs: 3, sm: 7, md: 11, lg: 12, xl: COLUMN_PRIORITY.length }
/** 永远不让位的列：没有它们这一行就读不出"在下什么、下到哪" */
const PROTECTED = new Set(COLUMN_PRIORITY.slice(0, 3))

export function tierRank(t: Tier): number {
  return ORDER.indexOf(t)
}

function rawTier(w: number): Tier {
  if (w >= TIER_FLOORS.xl) return 'xl'
  if (w >= TIER_FLOORS.lg) return 'lg'
  if (w >= TIER_FLOORS.md) return 'md'
  if (w >= TIER_FLOORS.sm) return 'sm'
  return 'xs'
}

/** 带迟滞地取档：可跨多档跳变，但压线时保持原档 */
export function tierFor(width: number, prev: Tier | null): Tier {
  const raw = rawTier(width)
  if (!prev || raw === prev) return raw
  const h = raw === 'xs' || raw === 'sm' || prev === 'xs' || prev === 'sm' ? HYSTERESIS_NARROW : HYSTERESIS
  if (tierRank(raw) > tierRank(prev)) return width >= TIER_FLOORS[raw] + h ? raw : prev
  return width < TIER_FLOORS[prev] - h ? raw : prev
}

export interface LayoutFlags {
  paneMounted: boolean
  coarse: boolean
  singleLine: boolean
}

export interface LayoutDecision {
  tier: Tier
  dockTier: Tier
  sidebarForm: SidebarForm
  sidebarSlot: number
  paneSlot: number
  /** 内容区可用宽度：所有让位判断的预算基准 */
  free: number
  detailVariant: DetailVariant
  allowTable: boolean
  rowHeight: number
  gridCols: number
  columnCeiling: number
  tapOpensDetail: boolean
  populatePaneOnClick: boolean
  swipeEnabled: boolean
  floatingBar: boolean
  scale: number
}

export interface LayoutInput {
  /** .tm-shell 实测宽度：视口 / 内嵌 WebView / 分屏下都等于本应用真正能用的宽度 */
  shellW: number
  sidebarWidth: number
  detailOpen: boolean
  allowPane: boolean
  coarse: boolean
  singleLine: boolean
  gap: number
  /** 左右安全区之和 */
  safeX: number
  prevGateTier: Tier | null
  prevTier: Tier | null
}

export interface LayoutResult extends LayoutInput {
  gateTier: Tier
  decision: LayoutDecision
}

/**
 * 单向推导链：shellW ─→ gateTier ─→ 侧栏形态/槽宽 ─→ 右栏像素闸门 ─→ free ─→ tier。
 * 侧栏与右栏的挂载只由 gateTier（含侧栏、不含右栏的宽度）决定，因此"挂了右栏把内容区压窄"
 * 不会反过来撤销右栏本身——这条不变量是整个系统不振荡的根据。
 */
export function computeLayout(input: LayoutInput): LayoutResult {
  const chromeW = Math.max(0, input.shellW - input.gap * 2 - input.safeX)
  const gateTier = tierFor(chromeW, input.prevGateTier)

  const sidebarForm: SidebarForm = gateTier === 'xs' ? 'drawer' : gateTier === 'sm' ? 'rail' : 'full'
  const sidebarSlot =
    sidebarForm === 'drawer' ? 0 : sidebarForm === 'rail' ? RAIL_W : clamp(input.sidebarWidth, SIDEBAR_RANGE[0], SIDEBAR_RANGE[1])

  const base = chromeW - sidebarSlot
  // 顶栏/状态栏这类横跨整幅的 dock 拿得到 chromeW，让它们按 chromeW 取内容集，
  // 才不会被"侧栏占掉的宽度"误降级
  const paneW = !input.allowPane || !input.detailOpen || base - PANE_W.lg < PANE_MIN_LIST
    ? 0
    : base - PANE_W.xl >= PANE_MIN_LIST
      ? PANE_W.xl
      : PANE_W.lg

  const free = Math.max(0, base - paneW)
  const tier = tierFor(free, input.prevTier)

  return {
    ...input,
    gateTier,
    decision: resolveLayout(tier, {
      paneMounted: paneW > 0,
      coarse: input.coarse,
      singleLine: input.singleLine,
      gateTier,
      sidebarForm,
      sidebarSlot,
      paneSlot: paneW,
      free,
    }),
  }
}

/** 结构性结果的唯一来源：同一帧内 JS 与 CSS 消费的都是这一个对象，不会互相矛盾 */
export function resolveLayout(
  tier: Tier,
  ctx: {
    paneMounted: boolean
    coarse: boolean
    singleLine: boolean
    gateTier: Tier
    sidebarForm: SidebarForm
    sidebarSlot: number
    paneSlot: number
    free: number
  },
): LayoutDecision {
  const narrow = tierRank(tier) <= tierRank('sm')
  return {
    tier,
    dockTier: ctx.gateTier,
    sidebarForm: ctx.sidebarForm,
    sidebarSlot: ctx.sidebarSlot,
    paneSlot: ctx.paneSlot,
    free: ctx.free,
    detailVariant: ctx.paneMounted ? 'pane' : narrow ? 'sheet' : 'dialog',
    allowTable: tierRank(tier) >= tierRank('sm'),
    rowHeight: ctx.singleLine ? ROW_HEIGHT[tier].single : ROW_HEIGHT[tier].double,
    gridCols: GRID_COLS[tier],
    columnCeiling: TIER_COLUMN_CEILING[tier],
    // 右栏常驻时单击就该把种子填进栏里；窄档没有栏，点按直接开详情
    tapOpensDetail: !ctx.paneMounted && (narrow || ctx.coarse),
    populatePaneOnClick: ctx.paneMounted,
    swipeEnabled: narrow && ctx.coarse,
    floatingBar: narrow,
    scale: TIER_SCALE[tier],
  }
}

/**
 * 有效列 = 用户勾选 ∩ 档位上限，再按像素预算从最次要的列开始让位。
 * 只读不改：用户的 columns 偏好永不被本函数写回，所以扫过任何宽度都不会污染存储。
 */
export function fitColumns(cols: ColumnConfig[], tier: Tier, budget: number): ColumnConfig[] {
  const ceiling = TIER_COLUMN_CEILING[tier]
  const known = (key: string) => COLUMN_PRIORITY.indexOf(key)
  // 优先级表之外的列（后续新增的）不参与档位封顶，否则会被静默挡掉；
  // 只在预算不足需要让位时排在已知列之后
  const rankOf = (key: string, at: number) => {
    const k = known(key)
    return k < 0 ? COLUMN_PRIORITY.length + at : k
  }
  const widthOf = (c: ColumnConfig) => (c.key === 'name' ? Math.max(c.width ?? 320, NAME_MIN_W) : c.width ?? DEFAULT_COL_W)

  const allowed = cols.filter((c) => c.visible && (known(c.key) < 0 || known(c.key) <= ceiling))
  const order = allowed.map((c, i) => ({ c, w: widthOf(c), r: rankOf(c.key, i) }))
  const byImportance = [...order].sort((a, b) => a.r - b.r)

  let total = byImportance.reduce((s, x) => s + x.w, 0)
  const dropped = new Set<string>()
  for (let i = byImportance.length - 1; i > 0 && total > budget; i--) {
    if (PROTECTED.has(byImportance[i].c.key)) continue
    total -= byImportance[i].w
    dropped.add(byImportance[i].c.key)
  }
  return allowed.filter((c) => !dropped.has(c.key))
}

/** 列少到这个数就没意义了，回落网格视图 */
export const MIN_TABLE_COLUMNS = 3

export function clamp(v: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, v))
}
