import { useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import {
  AlertTriangle,
  CalendarDays,
  Check,
  ChevronLeft,
  ChevronRight,
  Copy,
  Download,
  FileArchive,
  HardDrive,
  Layers,
  Loader2,
  Package,
  Plus,
  RefreshCw,
  RotateCcw,
  Save,
  Search,
  ShieldAlert,
  Sprout,
  Trash2,
} from 'lucide-react'
import { ptApi } from '@/api/pt'
import type {
  PTCalendarDay,
  PTLedger,
  PTOverview,
  PTSiteProfile,
  PTStatus,
  PTTorrentRef,
  PTVaultDetail,
  PTVaultEntry,
} from '@/types/pt'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { confirm } from '@/lib/confirm'
import { toast } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { formatBytes, formatDate, formatRatio } from '@/utils/format'
import { copyText } from '@/utils/clipboard'

// PT 状态色只引用全站已定义的状态变量，不在这里写十六进制
const STATUS_VAR: Record<PTStatus, string> = {
  seeding: 'var(--color-status-seeding)',
  downloading: 'var(--color-status-downloading)',
  paused: 'var(--color-status-stopped)',
  error: 'var(--color-status-error)',
  other: 'var(--color-status-verifying)',
}

const TABS = [
  { id: 'overview', icon: Sprout },
  { id: 'calendar', icon: CalendarDays },
  { id: 'ledger', icon: HardDrive },
  { id: 'vault', icon: FileArchive },
  { id: 'matrix', icon: Layers },
  { id: 'publish', icon: Package },
  { id: 'sites', icon: ShieldAlert },
] as const

interface LoadState<T> {
  loading: boolean
  error: boolean
  data: T | null
}

// 通用读取器：进入页签即拉取，失败给重试入口而不是抛错炸掉弹窗
function useLoader<T>(fn: () => Promise<T>, deps: unknown[]): LoadState<T> & { reload: () => void } {
  const [state, setState] = useState<LoadState<T>>({ loading: true, error: false, data: null })
  const [nonce, setNonce] = useState(0)
  useEffect(() => {
    let alive = true
    setState((s) => ({ ...s, loading: true, error: false }))
    fn()
      .then((d) => {
        if (alive) setState({ loading: false, error: false, data: d })
      })
      .catch(() => {
        if (alive) setState({ loading: false, error: true, data: null })
      })
    return () => {
      alive = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, nonce])
  return { ...state, reload: () => setNonce((n) => n + 1) }
}

function Loading() {
  const { t } = useTranslation()
  return (
    <div className="flex items-center justify-center gap-2 py-10 text-footnote text-gray-500 dark:text-gray-400">
      <Loader2 className="h-4 w-4 animate-spin" />
      {t('pt.loading')}
    </div>
  )
}

function LoadError({ onRetry }: { onRetry: () => void }) {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col items-center gap-3 py-10 text-center">
      <AlertTriangle className="h-6 w-6" style={{ color: 'var(--color-status-error)' }} />
      <p className="text-footnote text-gray-500 dark:text-gray-400">{t('pt.loadFailed')}</p>
      <Button variant="outline" size="sm" onClick={onRetry} className="gap-1.5">
        <RefreshCw className="h-3.5 w-3.5" />
        {t('pt.retry')}
      </Button>
    </div>
  )
}

function Empty({ label }: { label: string }) {
  return <p className="py-6 text-center text-footnote text-gray-400 dark:text-gray-500">{label}</p>
}

function Panel({ children, className }: { children: ReactNode; className?: string }) {
  // 弹窗根容器是 glass-panel-strong（float 档，带 40px backdrop-filter），
  // 这里的子面板若再用 glass-panel（dock 档，同样带 backdrop-filter）就是嵌套模糊，
  // 属规范禁止清单第 1 条。玻璃容器内的子卡片一律用 .glass-subcard（实色半透明 + 内高光）。
  return <div className={cn('glass-subcard rounded-tile p-3', className)}>{children}</div>
}

function SectionTitle({ icon: Icon, text }: { icon: typeof Sprout; text: string }) {
  return (
    <div className="mb-2 flex items-center gap-1.5 text-footnote font-semibold">
      <Icon className="h-4 w-4 text-primary" />
      {text}
    </div>
  )
}

function StatCard({ label, value, hint, tone }: { label: string; value: string; hint?: string; tone?: 'warn' | 'good' }) {
  return (
    <div className="glass-subcard rounded-tile px-3 py-2">
      <p className="truncate text-caption1 text-gray-500 dark:text-gray-400">{label}</p>
      <p
        className={cn('mt-0.5 truncate text-title2 font-semibold tabular-nums')}
        style={tone ? { color: tone === 'warn' ? 'var(--color-status-error)' : 'var(--color-status-seeding)' } : undefined}
      >
        {value}
      </p>
      {hint && <p className="truncate text-caption2 text-gray-400 dark:text-gray-500">{hint}</p>}
    </div>
  )
}

function StatusChip({ status }: { status: PTStatus }) {
  const { t } = useTranslation()
  return (
    <span className="inline-flex items-center gap-1 whitespace-nowrap text-caption1" style={{ color: STATUS_VAR[status] }}>
      <span className="h-1.5 w-1.5 rounded-full" style={{ background: STATUS_VAR[status] }} />
      {t(`pt.status.${status}`)}
    </span>
  )
}

// ---------- 概览 ----------

function OverviewTab() {
  const { t } = useTranslation()
  const { loading, error, data, reload } = useLoader(() => ptApi.overview(), [])
  if (loading) return <Loading />
  if (error || !data) return <LoadError onRetry={reload} />
  const o: PTOverview = data
  // 后端约定这两个字段是数组，但历史/演示数据可能是 null：先收敛再渲染，避免空列表直接崩
  const sites = o.sites ?? []
  const upcoming = o.upcoming ?? []
  const warnings = o.warnings ?? []
  const coverage = o.coverage
  const cards = [
    { label: t('pt.card.torrents'), value: String(o.torrentCount) },
    { label: t('pt.card.seeding'), value: String(o.seedCount) },
    { label: t('pt.card.totalSize'), value: formatBytes(o.totalSize) },
    { label: t('pt.card.uploaded30d'), value: formatBytes(o.uploaded30d) },
    // 今日增量：不可比时不当作「今天没上传」，把口径直接挂在卡片的 hint 上
    {
      label: t('pt.card.uploadedToday'),
      value: formatBytes(o.uploadedToday ?? 0),
      hint: t(`pt.today.${o.todayQuality ?? 'unknown'}`),
      tone: (o.todayQuality ?? 'unknown') === 'exact' ? ('good' as const) : undefined,
    },
    { label: t('pt.card.avgEfficiency'), value: `${o.avgEfficiency.toFixed(1)} MB/GB·d` },
    { label: t('pt.card.ready'), value: `${o.readyCount} · ${formatBytes(o.readyBytes)}`, tone: 'good' as const },
    { label: t('pt.card.hrRisk'), value: String(o.hrRiskCount), tone: o.hrRiskCount > 0 ? ('warn' as const) : undefined },
    // ② 僵尸种子：有采样数据但连续多天没有上传增长
    {
      label: t('pt.card.idle'),
      value: String(o.idleCount ?? 0),
      hint: (o.idleCount ?? 0) > 0 ? formatBytes(o.idleBytes ?? 0) : undefined,
      tone: (o.idleCount ?? 0) > 0 ? ('warn' as const) : undefined,
    },
    // ③ 站点已删种
    {
      label: t('pt.card.invalid'),
      value: String(o.invalidCount ?? 0),
      hint: (o.invalidCount ?? 0) > 0 ? formatBytes(o.invalidBytes ?? 0) : undefined,
      tone: (o.invalidCount ?? 0) > 0 ? ('warn' as const) : undefined,
    },
    // ① 保种契约
    {
      label: t('pt.card.contract'),
      value: String(o.contractCount ?? 0),
      hint: (o.contractDueCount ?? 0) > 0 ? `${t('pt.card.contractDue')} ${o.contractDueCount}` : undefined,
      tone: (o.contractBreach ?? 0) > 0 ? ('warn' as const) : undefined,
    },
    // ⑦ 站点保种预算
    {
      label: t('pt.card.overBudget'),
      value: String(o.overBudgetCount ?? 0),
      hint: (o.overBudgetCount ?? 0) > 0 ? formatBytes(o.overBudgetBytes ?? 0) : undefined,
      tone: (o.overBudgetCount ?? 0) > 0 ? ('warn' as const) : undefined,
    },
    { label: t('pt.card.vault'), value: `${o.vaultCount} · ${formatBytes(o.vaultBytes)}` },
    { label: t('pt.card.groups'), value: String(o.groupCount) },
    { label: t('pt.card.duplicate'), value: formatBytes(o.duplicateBytes), tone: 'warn' as const },
  ]
  return (
    <div className="space-y-3">
      {/* 告警：把 ②③⑦ 的结论直接摊开，而不是让用户自己去各个页签里翻 */}
      {warnings.length > 0 && (
        <div className="rounded-lg border border-amber-300/60 bg-amber-50/60 px-3 py-2 dark:border-amber-500/30 dark:bg-amber-500/10">
          <div className="mb-1 text-footnote font-medium text-amber-700 dark:text-amber-300">{t('pt.warnings.title')}</div>
          <ul className="space-y-0.5">
            {warnings.map((w) => (
              <li key={w} className="text-caption1 text-amber-800 dark:text-amber-200">
                · {w}
              </li>
            ))}
          </ul>
        </div>
      )}
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5">
        {cards.map((c) => (
          <StatCard key={c.label} label={c.label} value={c.value} hint={c.hint} tone={c.tone} />
        ))}
      </div>
      {/* ② 采样覆盖：解释「≈」的可信度 */}
      {coverage && (
        <p className="text-caption1 text-gray-500 dark:text-gray-400">
          {t('pt.coverage.label')}：
          {coverage.days === 0
            ? t('pt.coverage.none')
            : coverage.full
              ? t('pt.coverage.full')
              : t('pt.coverage.partial', { days: coverage.days, from: coverage.from })}
          {coverage.lastSample > 0 && ` · ${t('pt.coverage.lastSample', { time: formatDate(coverage.lastSample) })}`}
        </p>
      )}
      {/* ② 今日增量的口径：为什么是这个数、可不可比 */}
      {o.todayNote && (
        <p className="text-caption1 text-gray-500 dark:text-gray-400">
          {t('pt.card.uploadedToday')}：{o.todayNote}
        </p>
      )}
      <div className="grid gap-3 lg:grid-cols-2">
        <Panel>
          <SectionTitle icon={HardDrive} text={t('pt.sites.title')} />
          {sites.length === 0 ? (
            <Empty label={t('pt.sites.empty')} />
          ) : (
            // 用真表格而不是一排 flex 行：这几列都是数字，没有表头根本看不出哪列是哪列
            <table className="w-full border-collapse text-footnote">
              <thead className="text-caption1 text-gray-500 dark:text-gray-400">
                <tr>
                  {/* 表头必须 nowrap：首列用 w-full max-w-0 吃掉剩余宽度后，数字列会被压到
                      内容最小宽度，表头文字（「种子数」「今日上传」）就会逐字换行 */}
                  <th className="whitespace-nowrap py-1 pr-2 text-left font-medium">{t('pt.sites.colName')}</th>
                  <th className="whitespace-nowrap py-1 px-2 text-right font-medium">{t('pt.sites.colCount')}</th>
                  <th className="whitespace-nowrap py-1 px-2 text-right font-medium">{t('pt.sites.colToday')}</th>
                  <th className="whitespace-nowrap py-1 px-2 text-right font-medium">{t('pt.sites.colSize')}</th>
                  <th className="whitespace-nowrap py-1 pl-2 text-right font-medium">{t('pt.sites.colRatio')}</th>
                </tr>
              </thead>
              <tbody>
                {sites.map((s) => (
                  <tr key={s.site} className="border-t border-gray-200/60 dark:border-white/10">
                    {/* w-full + max-w-0 让站点名吃掉剩余宽度并截断，数字列保持稳定 */}
                    <td className="w-full max-w-0 py-1.5 pr-2">
                      <span className="flex items-center gap-1.5">
                        <span className="truncate" title={s.site}>
                          {s.site}
                        </span>
                        {/* ⑦ 超预算标记：只提示，不触发清理 */}
                        {s.overBudget && (
                          <span className="shrink-0 rounded-full bg-amber-100 px-1 text-caption2 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300">
                            {t('pt.sites.overBudget')}
                          </span>
                        )}
                      </span>
                    </td>
                    <td className="whitespace-nowrap px-2 py-1.5 text-right tabular-nums text-gray-500 dark:text-gray-400">{s.count}</td>
                    {/* 今日上传增量：非 exact 时挂 ≈ 并把口径放进 title，避免把它读成精确值 */}
                    <td
                      className="whitespace-nowrap px-2 py-1.5 text-right tabular-nums text-green-600 dark:text-green-400"
                      title={t(`pt.today.${s.todayQuality}`)}
                    >
                      {s.uploadedToday > 0 ? (
                        <>
                          {formatBytes(s.uploadedToday)}
                          {s.todayQuality !== 'exact' && <span className="ml-0.5 text-caption2">≈</span>}
                        </>
                      ) : (
                        <span className="text-gray-400 dark:text-gray-500">—</span>
                      )}
                    </td>
                    <td className="whitespace-nowrap px-2 py-1.5 text-right tabular-nums">
                      {formatBytes(s.size)}
                      {s.budgetBytes > 0 && (
                        <span className="ml-1 text-caption2 text-gray-400">/ {formatBytes(s.budgetBytes)}</span>
                      )}
                    </td>
                    <td className="whitespace-nowrap pl-2 py-1.5 text-right tabular-nums text-gray-500 dark:text-gray-400">{formatRatio(s.ratio)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>
        <Panel>
          <SectionTitle icon={CalendarDays} text={t('pt.upcoming.title')} />
          {upcoming.length === 0 ? (
            <Empty label={t('pt.upcoming.empty')} />
          ) : (
            <table className="w-full border-collapse text-footnote">
              <thead className="text-caption1 text-gray-500 dark:text-gray-400">
                <tr>
                  <th className="whitespace-nowrap py-1 pr-2 text-left font-medium">{t('pt.upcoming.colDate')}</th>
                  <th className="whitespace-nowrap py-1 px-2 text-right font-medium">{t('pt.upcoming.colCount')}</th>
                  <th className="whitespace-nowrap py-1 pl-2 text-right font-medium">{t('pt.upcoming.colBytes')}</th>
                </tr>
              </thead>
              <tbody>
                {upcoming.map((d) => (
                  <tr key={d.date} className="border-t border-gray-200/60 dark:border-white/10">
                    <td className="py-1.5 pr-2 tabular-nums">{d.date}</td>
                    <td className="whitespace-nowrap px-2 py-1.5 text-right tabular-nums text-gray-500 dark:text-gray-400">{d.count}</td>
                    <td className="whitespace-nowrap pl-2 py-1.5 text-right tabular-nums">{formatBytes(d.bytes)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>
      </div>
      <p className="text-caption1 text-gray-500 dark:text-gray-400">{t('pt.estimateNote')}</p>
      <p className="text-caption1 text-gray-400 dark:text-gray-500">
        {t('pt.generatedAt', { time: formatDate(o.generatedAt) })}
      </p>
    </div>
  )
}

// ---------- 释放日历 ----------

function CalendarTab() {
  const { t } = useTranslation()
  const now = new Date()
  const [month, setMonth] = useState(`${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`)
  const [selected, setSelected] = useState<number | null>(null)
  const { loading, error, data, reload } = useLoader(() => ptApi.calendar(month), [month])

  const byDay = useMemo(() => {
    const m = new Map<number, PTCalendarDay>()
    for (const d of data?.days ?? []) m.set(Number(d.date.slice(8, 10)), d)
    return m
  }, [data])

  const shift = (delta: number) => {
    const [y, m] = month.split('-').map(Number)
    const d = new Date(y, m - 1 + delta, 1)
    setMonth(`${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`)
    setSelected(null)
  }

  const [y, m] = month.split('-').map(Number)
  const daysInMonth = new Date(y, m, 0).getDate()
  const offset = (new Date(y, m - 1, 1).getDay() + 6) % 7 // 周一为一周起点
  const todayKey = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
  const weekLabels = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'].map((k) => t(`pt.week.${k}`))

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="ghost" size="sm" className="h-8 w-8 p-0" onClick={() => shift(-1)} aria-label={t('pt.calendar.prev')}>
          <ChevronLeft className="h-4 w-4" />
        </Button>
        <span className="min-w-[6.5rem] text-center text-body font-semibold tabular-nums">{month}</span>
        <Button variant="ghost" size="sm" className="h-8 w-8 p-0" onClick={() => shift(1)} aria-label={t('pt.calendar.next')}>
          <ChevronRight className="h-4 w-4" />
        </Button>
        <div className="ml-auto flex flex-wrap gap-2 text-caption1 text-gray-500 dark:text-gray-400">
          <span>{t('pt.calendar.today')} {formatBytes(data?.summary.todayBytes ?? 0)}</span>
          <span>{t('pt.calendar.week')} {formatBytes(data?.summary.weekBytes ?? 0)}</span>
          <span>{t('pt.calendar.month')} {formatBytes(data?.summary.monthBytes ?? 0)}</span>
          <span>{t('pt.calendar.hr')} {data?.summary.hrCount ?? 0}</span>
          {/* ① 保种契约到期：与「可释放」是两个概念，单独列出来 */}
          {(data?.summary.contractDue ?? 0) > 0 && <span>{t('pt.card.contractDue')} {data?.summary.contractDue}</span>}
          {(data?.summary.contractBreach ?? 0) > 0 && (
            <span style={{ color: 'var(--color-status-error)' }}>
              {t('pt.calendar.contractBreach')} {data?.summary.contractBreach}
            </span>
          )}
        </div>
      </div>

      {loading ? (
        <Loading />
      ) : error || !data ? (
        <LoadError onRetry={reload} />
      ) : (
        <>
          <div className="grid grid-cols-7 gap-1 text-center text-caption1 text-gray-500 dark:text-gray-400">
            {weekLabels.map((w) => (
              <div key={w} className="py-1">
                {w}
              </div>
            ))}
          </div>
          <div className="grid grid-cols-7 gap-1">
            {Array.from({ length: offset }).map((_, i) => (
              <div key={`pad-${i}`} />
            ))}
            {Array.from({ length: daysInMonth }).map((_, i) => {
              const day = i + 1
              const key = `${month}-${String(day).padStart(2, '0')}`
              const entry = byDay.get(day)
              const isToday = key === todayKey
              const active = selected === day
              return (
                <button
                  key={key}
                  type="button"
                  onClick={() => setSelected(entry ? day : null)}
                  disabled={!entry}
                  className={cn(
                    'flex min-h-[3.5rem] flex-col items-start gap-0.5 rounded-tile border p-1.5 text-left transition-colors',
                    entry ? 'glass-subcard hover:border-primary/50' : 'border-transparent opacity-40',
                    active && 'ring-1 ring-primary',
                    isToday && !active && 'border-primary/40',
                  )}
                >
                  <span className="text-caption1 tabular-nums text-gray-500 dark:text-gray-400">{day}</span>
                  {entry && (
                    <>
                      <span className="text-caption1 font-semibold tabular-nums">{formatBytes(entry.bytes)}</span>
                      <span className="flex items-center gap-1 text-caption2 text-gray-500 dark:text-gray-400">
                        {entry.count}
                        {entry.ready && (
                          <span className="rounded-full px-1" style={{ background: 'color-mix(in srgb, var(--color-status-seeding) 18%, transparent)', color: 'var(--color-status-seeding)' }}>
                            {t('pt.calendar.readyShort')}
                          </span>
                        )}
                        {entry.hrRisk > 0 && <span style={{ color: 'var(--color-status-error)' }}>H&R{entry.hrRisk}</span>}
                        {/* ① 当日到期的保种契约数：用图标避免在此处塞进语言相关文案 */}
                        {entry.contractDue > 0 && (
                          <span className="inline-flex items-center gap-0.5" title={t('pt.card.contractDue')}>
                            <Sprout className="h-2.5 w-2.5" />
                            {entry.contractDue}
                          </span>
                        )}
                      </span>
                    </>
                  )}
                </button>
              )
            })}
          </div>
          {data.unknown.count > 0 && (
            <p className="text-caption1 text-gray-500 dark:text-gray-400">
              {t('pt.calendar.unknown', { count: data.unknown.count, size: formatBytes(data.unknown.bytes) })}
            </p>
          )}
          {selected !== null && byDay.get(selected) && (
            <Panel>
              <SectionTitle icon={CalendarDays} text={`${byDay.get(selected)!.date} · ${t('pt.calendar.dayList')}`} />
              {byDay.get(selected)!.torrents.length === 0 && (
                // 契约到期日可以与「可释放」不同天，因此存在只有契约到期的日子
                <p className="mb-1.5 text-caption1 text-gray-500 dark:text-gray-400">
                  {t('pt.calendar.dayContractOnly', { count: byDay.get(selected)!.contractDue })}
                </p>
              )}
              <div className="space-y-1.5">
                {byDay.get(selected)!.torrents.map((tor) => (
                  <div key={tor.id} className="flex items-center gap-2 text-footnote">
                    <span
                      className="h-1.5 w-1.5 shrink-0 rounded-full"
                      style={{ background: tor.hrRisk ? 'var(--color-status-error)' : 'var(--color-status-seeding)' }}
                    />
                    <span className="min-w-0 flex-1 truncate" title={tor.name}>
                      {tor.name}
                    </span>
                    <span className="hidden shrink-0 text-gray-500 dark:text-gray-400 sm:inline">{tor.site || t('pt.unknownSite')}</span>
                    <span className="w-20 shrink-0 text-right tabular-nums">{formatBytes(tor.size)}</span>
                    <span className="hidden w-24 shrink-0 text-right text-caption1 text-gray-500 dark:text-gray-400 sm:inline">
                      {t(`pt.reason.${tor.reason}`)}
                    </span>
                  </div>
                ))}
              </div>
            </Panel>
          )}
        </>
      )}
    </div>
  )
}

// ---------- 保种契约（①） ----------

// 契约只是「承诺保到什么时候」的本地记录：写它不会暂停、不会删除、也不上报站点，
// 因此这里不做任何二次确认，只把口径在弹窗里说明白。
function ContractDialog({
  row,
  onClose,
  onSaved,
}: {
  row: PTTorrentRef | null
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const [days, setDays] = useState('30')
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!row) return
    setDays(row.contractDaysLeft > 0 ? String(row.contractDaysLeft) : '30')
    setNote('')
    setBusy(false)
  }, [row])

  const save = async () => {
    if (!row) return
    const n = Math.round(Number(days))
    if (!Number.isFinite(n) || n <= 0) {
      toast.error(t('pt.contract.daysPh'))
      return
    }
    setBusy(true)
    try {
      await ptApi.setContract({ hash: row.hash, days: n, note })
      toast.success(t('pt.contract.saved'))
      onSaved()
      onClose()
    } catch {
      /* 拦截器已提示 */
    } finally {
      setBusy(false)
    }
  }

  const clear = async () => {
    if (!row) return
    setBusy(true)
    try {
      await ptApi.deleteContract(row.hash)
      toast.success(t('pt.contract.cleared'))
      onSaved()
      onClose()
    } catch {
      /* 拦截器已提示 */
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={!!row} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{t('pt.contract.title')}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className="rounded-lg bg-gray-100/70 px-3 py-2 text-footnote dark:bg-white/5">
            <div className="text-caption1 text-gray-500 dark:text-gray-400">{t('pt.contract.name')}</div>
            <div className="truncate" title={row?.name}>
              {row?.name}
            </div>
            {row?.hasContract && (
              <div className={cn('mt-1 text-caption1', row.contractBreach ? 'text-amber-600 dark:text-amber-300' : 'text-gray-500 dark:text-gray-400')}>
                {row.contractBreach
                  ? t('pt.contract.breach', { days: Math.abs(row.contractDaysLeft) })
                  : t('pt.contract.remaining', { days: row.contractDaysLeft })}
              </div>
            )}
          </div>
          <div className="space-y-1">
            <label className="text-footnote text-gray-600 dark:text-gray-300">{t('pt.contract.days')}</label>
            <Input value={days} onChange={(e) => setDays(e.target.value)} placeholder={t('pt.contract.daysPh')} className="h-8 text-footnote" />
          </div>
          <div className="space-y-1">
            <label className="text-footnote text-gray-600 dark:text-gray-300">{t('pt.contract.note')}</label>
            <Input value={note} onChange={(e) => setNote(e.target.value)} className="h-8 text-footnote" />
          </div>
          <p className="text-caption1 text-gray-500 dark:text-gray-400">{t('pt.contract.intro')}</p>
          <div className="flex items-center justify-end gap-2">
            {row?.hasContract && (
              <Button variant="ghost" size="sm" disabled={busy} onClick={clear}>
                {t('pt.contract.clear')}
              </Button>
            )}
            <Button size="sm" disabled={busy} onClick={save}>
              {busy ? <Loader2 className="mr-1 h-3.5 w-3.5 animate-spin" /> : <Save className="mr-1 h-3.5 w-3.5" />}
              {t('pt.contract.save')}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 资产台账 ----------

type LedgerSortKey =
  | 'name'
  | 'size'
  | 'seedDays'
  | 'ratio'
  | 'uploaded30d'
  | 'uploadedToday'
  | 'efficiency'
  | 'score'
  | 'releaseAt'
  | 'idleDays'
  | 'contractDaysLeft'

function LedgerTab() {
  const { t } = useTranslation()
  const { loading, error, data, reload } = useLoader(() => ptApi.ledger(), [])
  const [q, setQ] = useState('')
  const [site, setSite] = useState('')
  const [sortKey, setSortKey] = useState<LedgerSortKey>('score')
  const [dir, setDir] = useState<'asc' | 'desc'>('desc')
  const [contractRow, setContractRow] = useState<PTTorrentRef | null>(null)

  const sites = useMemo(() => {
    const set = new Set<string>()
    for (const r of data?.rows ?? []) if (r.site) set.add(r.site)
    return Array.from(set).sort()
  }, [data])

  const rows = useMemo(() => {
    const query = q.trim().toLowerCase()
    const list = (data?.rows ?? []).filter((r) => {
      if (site && r.site !== site) return false
      if (!query) return true
      return r.name.toLowerCase().includes(query) || r.hash.toLowerCase().includes(query) || r.site.toLowerCase().includes(query)
    })
    const sign = dir === 'asc' ? 1 : -1
    return [...list].sort((a, b) => {
      const av = a[sortKey]
      const bv = b[sortKey]
      if (typeof av === 'number' && typeof bv === 'number') return (av - bv) * sign
      return String(av).localeCompare(String(bv)) * sign
    })
  }, [data, q, site, sortKey, dir])

  const toggleSort = (key: LedgerSortKey) => {
    if (key === sortKey) setDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    else {
      setSortKey(key)
      setDir('desc')
    }
  }

  if (loading) return <Loading />
  if (error || !data) return <LoadError onRetry={reload} />
  const ledger: PTLedger = data

  const header = (key: LedgerSortKey, label: string, className?: string) => (
    <th className={cn('cursor-pointer select-none whitespace-nowrap px-2 py-1.5 text-left font-medium', className)} onClick={() => toggleSort(key)}>
      {label}
      {sortKey === key && <span className="ml-0.5">{dir === 'asc' ? '↑' : '↓'}</span>}
    </th>
  )

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-[12rem] flex-1">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-gray-400" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t('pt.ledger.search')} className="h-8 pl-8 text-footnote" />
        </div>
        <select
          value={site}
          onChange={(e) => setSite(e.target.value)}
          className="h-8 rounded-md border border-input bg-transparent px-2 text-footnote"
        >
          <option value="">{t('pt.ledger.allSites')}</option>
          {sites.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
        <span className="text-caption1 text-gray-500 dark:text-gray-400">
          {t('pt.ledger.summary', {
            count: ledger.totals.count,
            size: formatBytes(ledger.totals.size),
            upload: formatBytes(ledger.totals.uploaded30d),
            zero: ledger.totals.zeroUpload,
            idle: ledger.totals.idleCount ?? 0,
          })}
        </span>
        <Button variant="ghost" size="sm" className="h-8 w-8 p-0" onClick={reload} aria-label={t('pt.refresh')}>
          <RefreshCw className="h-3.5 w-3.5" />
        </Button>
      </div>

      {rows.length === 0 ? (
        <Empty label={t('pt.ledger.empty')} />
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[1040px] border-collapse text-footnote">
            <thead className="text-caption1 text-gray-500 dark:text-gray-400">
              <tr>
                {header('name', t('pt.ledger.name'), 'min-w-[16rem]')}
                <th className="px-2 py-1.5 text-left font-medium">{t('pt.ledger.site')}</th>
                {header('size', t('pt.ledger.size'), 'text-right')}
                {header('seedDays', t('pt.ledger.seedDays'), 'text-right')}
                {header('ratio', t('pt.ledger.ratio'), 'text-right')}
                {header('uploaded30d', t('pt.ledger.uploaded30d'), 'text-right')}
                {header('uploadedToday', t('pt.ledger.today'), 'text-right')}
                {header('efficiency', t('pt.ledger.efficiency'), 'text-right')}
                {header('score', t('pt.ledger.score'), 'text-left')}
                <th className="whitespace-nowrap px-2 py-1.5 text-left font-medium">{t('pt.ledger.status')}</th>
                {header('releaseAt', t('pt.ledger.release'), 'text-left')}
                {header('idleDays', t('pt.ledger.idle'), 'text-right')}
                <th className="whitespace-nowrap px-2 py-1.5 text-left font-medium">{t('pt.ledger.invalid')}</th>
                <th className="whitespace-nowrap px-2 py-1.5 text-left font-medium">{t('pt.ledger.contract')}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r: PTTorrentRef) => (
                <tr key={`${r.id}-${r.hash}`} className="border-t border-gray-200/60 dark:border-white/10">
                  <td className="max-w-[22rem] truncate px-2 py-1.5" title={r.name}>
                    {r.name}
                  </td>
                  <td className="whitespace-nowrap px-2 py-1.5 text-gray-500 dark:text-gray-400">{r.site || t('pt.unknownSite')}</td>
                  <td className="whitespace-nowrap px-2 py-1.5 text-right tabular-nums">{formatBytes(r.size)}</td>
                  <td className="whitespace-nowrap px-2 py-1.5 text-right tabular-nums">{r.seedDays.toFixed(1)}</td>
                  <td className="whitespace-nowrap px-2 py-1.5 text-right tabular-nums">{formatRatio(r.ratio)}</td>
                  <td className="whitespace-nowrap px-2 py-1.5 text-right tabular-nums">
                    {r.estimated && <span title={t('pt.estimatedHint')}>≈</span>}
                    {formatBytes(r.uploaded30d)}
                  </td>
                  {/* 今日增量：非 exact 时挂 ≈ 并给出原因 */}
                  <td className="whitespace-nowrap px-2 py-1.5 text-right tabular-nums" title={t(`pt.today.${r.todayQuality}`)}>
                    {r.uploadedToday > 0 ? (
                      <span className="text-green-600 dark:text-green-400">
                        {formatBytes(r.uploadedToday)}
                        {r.todayQuality !== 'exact' && <span className="ml-0.5 text-caption2">≈</span>}
                      </span>
                    ) : (
                      <span className="text-gray-400 dark:text-gray-500">—</span>
                    )}
                  </td>
                  <td className="whitespace-nowrap px-2 py-1.5 text-right tabular-nums">{r.efficiency.toFixed(1)}</td>
                  <td className="whitespace-nowrap px-2 py-1.5">
                    <div className="flex items-center gap-1.5">
                      <div className="h-1.5 w-14 overflow-hidden rounded-full bg-gray-200 dark:bg-white/10">
                        <div className="h-full rounded-full" style={{ width: `${Math.min(100, Math.max(0, r.score))}%`, background: 'var(--color-primary)' }} />
                      </div>
                      <span className="tabular-nums">{r.score}</span>
                    </div>
                  </td>
                  <td className="whitespace-nowrap px-2 py-1.5">
                    <StatusChip status={r.status} />
                  </td>
                  <td className="whitespace-nowrap px-2 py-1.5">
                    {r.releaseAt > 0 ? (
                      <span title={`${t(`pt.reason.${r.releaseReason}`)}${r.hrRisk ? ` · ${t('pt.hrRisk')}` : ''}`}>
                        {formatDate(r.releaseAt, 'YYYY-MM-DD')}
                        {r.hrRisk && <ShieldAlert className="ml-1 inline h-3 w-3" style={{ color: 'var(--color-status-error)' }} />}
                      </span>
                    ) : (
                      <span className="text-gray-400 dark:text-gray-500">{t('pt.reason.unknown')}</span>
                    )}
                  </td>
                  {/* ② 连续无上传天数；0 表示还没有足够的采样数据，不判断 */}
                  <td className="whitespace-nowrap px-2 py-1.5 text-right tabular-nums" title={t('pt.ledger.idleHint')}>
                    {r.idleDays > 0 ? (
                      <span className={cn(r.idleDays >= 7 && 'text-amber-600 dark:text-amber-300')}>{r.idleDays}</span>
                    ) : (
                      <span className="text-gray-400 dark:text-gray-500">—</span>
                    )}
                  </td>
                  {/* ③ 站点已删种 */}
                  <td className="whitespace-nowrap px-2 py-1.5">
                    {r.invalidSeed ? (
                      <span
                        className="inline-flex items-center gap-1 rounded-full bg-amber-100 px-1.5 py-0.5 text-caption1 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300"
                        title={r.invalidReason || t('pt.ledger.invalidHint')}
                      >
                        <AlertTriangle className="h-3 w-3" />
                        {t('pt.ledger.invalid')}
                      </span>
                    ) : (
                      <span className="text-gray-400 dark:text-gray-500">—</span>
                    )}
                  </td>
                  {/* ① 保种契约：点击设置/修改 */}
                  <td className="whitespace-nowrap px-2 py-1.5">
                    <button
                      type="button"
                      className="rounded-md px-1.5 py-0.5 text-caption1 transition-colors hover:bg-primary/10 hover:text-primary focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-primary"
                      onClick={() => setContractRow(r)}
                    >
                      {r.hasContract ? (
                        <span className={cn(r.contractBreach && 'text-amber-600 dark:text-amber-300')}>
                          {r.contractBreach
                            ? t('pt.contract.breach', { days: Math.abs(r.contractDaysLeft) })
                            : t('pt.contract.remaining', { days: r.contractDaysLeft })}
                        </span>
                      ) : (
                        <span className="text-gray-400 dark:text-gray-500">{t('pt.contract.set')}</span>
                      )}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <p className="text-caption1 text-gray-500 dark:text-gray-400">{t('pt.estimateNote')}</p>
      <ContractDialog row={contractRow} onClose={() => setContractRow(null)} onSaved={reload} />
    </div>
  )
}

// ---------- 种子档案库 ----------

function VaultTab() {
  const { t } = useTranslation()
  const [q, setQ] = useState('')
  const [expanded, setExpanded] = useState<string | null>(null)
  const [detail, setDetail] = useState<PTVaultDetail | null>(null)
  const { loading, error, data, reload } = useLoader(() => ptApi.vault(), [])

  const entries = useMemo(() => {
    const query = q.trim().toLowerCase()
    const list = data?.entries ?? []
    if (!query) return list
    return list.filter((e) => `${e.name} ${e.hash} ${e.site}`.toLowerCase().includes(query))
  }, [data, q])

  const openDetail = async (id: string) => {
    if (expanded === id) {
      setExpanded(null)
      setDetail(null)
      return
    }
    setExpanded(id)
    setDetail(null)
    try {
      setDetail(await ptApi.vaultDetail(id))
    } catch {
      setDetail(null)
    }
  }

  const revive = async (e: PTVaultEntry) => {
    const ok = await confirm({ title: t('pt.vault.reviveTitle'), content: t('pt.vault.reviveConfirm', { name: e.name }) })
    if (!ok) return
    try {
      const r = await ptApi.vaultRevive(e.id)
      toast.success(t('pt.vault.reviveDone', { id: r.id, via: r.via === 'torrent' ? t('pt.vault.viaTorrent') : t('pt.vault.viaMagnet') }))
    } catch {
      /* 拦截器已提示 */
    }
  }

  const remove = async (e: PTVaultEntry) => {
    const ok = await confirm({ title: t('pt.vault.deleteTitle'), content: t('pt.vault.deleteConfirm', { name: e.name }), danger: true })
    if (!ok) return
    try {
      await ptApi.vaultRemove(e.id)
      toast.success(t('pt.vault.deleteDone'))
      if (expanded === e.id) {
        setExpanded(null)
        setDetail(null)
      }
      reload()
    } catch {
      /* 拦截器已提示 */
    }
  }

  const download = async (e: PTVaultEntry) => {
    try {
      const blob = await ptApi.vaultTorrent(e.id)
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `${e.name || e.hash || e.id}.torrent`
      a.click()
      URL.revokeObjectURL(url)
    } catch {
      toast.error(t('pt.vault.noTorrent'))
    }
  }

  if (loading) return <Loading />
  if (error || !data) return <LoadError onRetry={reload} />

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-[12rem] flex-1">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-gray-400" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t('pt.vault.search')} className="h-8 pl-8 text-footnote" />
        </div>
        <span className="text-caption1 text-gray-500 dark:text-gray-400">
          {t('pt.vault.summary', { total: data.total, size: formatBytes(data.bytes) })}
        </span>
        <Button variant="ghost" size="sm" className="h-8 w-8 p-0" onClick={reload} aria-label={t('pt.refresh')}>
          <RefreshCw className="h-3.5 w-3.5" />
        </Button>
      </div>
      <p className="text-caption1 text-gray-500 dark:text-gray-400">{t('pt.vault.intro')}</p>

      {entries.length === 0 ? (
        <Empty label={t('pt.vault.empty')} />
      ) : (
        <div className="space-y-2">
          {entries.map((e) => (
            <Panel key={e.id}>
              <div className="flex flex-wrap items-center gap-2">
                <button type="button" className="min-w-0 flex-1 text-left" onClick={() => void openDetail(e.id)}>
                  <span className="block truncate text-footnote font-medium" title={e.name}>
                    {e.name || e.hash}
                  </span>
                  <span className="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-caption1 text-gray-500 dark:text-gray-400">
                    <span>{e.site || t('pt.unknownSite')}</span>
                    <span>{formatBytes(e.size)}</span>
                    <span>{t('pt.vault.deletedAt', { time: formatDate(e.deletedAt, 'YYYY-MM-DD HH:mm') })}</span>
                    <span>{t('pt.vault.seedDays', { days: e.seedDays.toFixed(1) })}</span>
                    <span>{t('pt.vault.ratio', { ratio: formatRatio(e.ratio) })}</span>
                  </span>
                </button>
                <div className="flex shrink-0 items-center gap-1">
                  <Button
                    variant="outline"
                    size="sm"
                    className="h-7 gap-1 px-2 text-caption1"
                    disabled={!e.canRevive}
                    onClick={() => void revive(e)}
                  >
                    <RotateCcw className="h-3 w-3" />
                    {t('pt.vault.revive')}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-7 gap-1 px-2 text-caption1"
                    disabled={!e.hasTorrent}
                    title={e.hasTorrent ? undefined : t('pt.vault.noTorrent')}
                    onClick={() => void download(e)}
                  >
                    <Download className="h-3 w-3" />
                    {t('pt.vault.torrent')}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-7 gap-1 px-2 text-caption1"
                    style={{ color: 'var(--color-status-error)' }}
                    onClick={() => void remove(e)}
                  >
                    <Trash2 className="h-3 w-3" />
                    {t('pt.vault.delete')}
                  </Button>
                </div>
              </div>
              {e.labels.length > 0 && (
                <div className="mt-1.5 flex flex-wrap gap-1">
                  {e.labels.map((l) => (
                    <span key={l} className="rounded-full bg-gray-500/10 px-1.5 py-0.5 text-caption2 text-gray-500 dark:text-gray-400">
                      {l}
                    </span>
                  ))}
                </div>
              )}
              {expanded === e.id && (
                <div className="mt-2 border-t border-gray-200/60 pt-2 dark:border-white/10">
                  {!detail ? (
                    <p className="py-2 text-center text-caption1 text-gray-400">{t('pt.loading')}</p>
                  ) : detail.files.length === 0 ? (
                    <p className="py-2 text-caption1 text-gray-400">{t('pt.vault.noFiles')}</p>
                  ) : (
                    <div className="max-h-48 space-y-1 overflow-y-auto">
                      {detail.files.map((f) => (
                        <div key={f.name} className="flex items-center gap-2 text-caption1">
                          <span className="min-w-0 flex-1 truncate" title={f.name}>
                            {f.name}
                          </span>
                          <span className="shrink-0 tabular-nums text-gray-500 dark:text-gray-400">{formatBytes(f.size)}</span>
                        </div>
                      ))}
                    </div>
                  )}
                  <p className="mt-1 text-caption2 text-gray-400 dark:text-gray-500">
                    {e.savePath ? t('pt.vault.savePath', { path: e.savePath }) : ''}
                  </p>
                </div>
              )}
            </Panel>
          ))}
        </div>
      )}
      <p className="text-caption1 text-gray-500 dark:text-gray-400">{t('pt.vault.disclaimer')}</p>
    </div>
  )
}

// ---------- 跨站同内容矩阵 ----------

function MatrixTab() {
  const { t } = useTranslation()
  const { loading, error, data, reload } = useLoader(() => ptApi.matrix(), [])
  const [openKey, setOpenKey] = useState<string | null>(null)

  if (loading) return <Loading />
  if (error || !data) return <LoadError onRetry={reload} />

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-3">
        <span className="text-footnote text-gray-500 dark:text-gray-400">
          {t('pt.matrix.summary', { groups: data.summary.groups, dup: formatBytes(data.summary.duplicateBytes) })}
        </span>
        <Button variant="ghost" size="sm" className="h-8 w-8 p-0" onClick={reload} aria-label={t('pt.refresh')}>
          <RefreshCw className="h-3.5 w-3.5" />
        </Button>
      </div>
      <p className="text-caption1 text-gray-500 dark:text-gray-400">{t('pt.matrix.intro')}</p>
      {data.groups.length === 0 ? (
        <Empty label={t('pt.matrix.empty')} />
      ) : (
        <div className="space-y-2">
          {data.groups.map((g) => (
            <Panel key={g.key}>
              <button type="button" className="flex w-full flex-wrap items-center gap-2 text-left" onClick={() => setOpenKey(openKey === g.key ? null : g.key)}>
                <span className="min-w-0 flex-1 truncate text-footnote font-medium" title={g.name}>
                  {g.name}
                </span>
                {g.sameDownloader && (
                  <span className="flex shrink-0 items-center gap-1 text-caption1" style={{ color: 'var(--color-status-error)' }}>
                    <AlertTriangle className="h-3 w-3" />
                    {t('pt.matrix.sameDownloader')}
                  </span>
                )}
                <span className="shrink-0 text-caption1 text-gray-500 dark:text-gray-400">
                  {t('pt.matrix.copies', { count: g.copies.length })}
                </span>
                <span className="shrink-0 text-caption1 tabular-nums">{formatBytes(g.totalBytes)}</span>
                <span className="shrink-0 text-caption1 tabular-nums" style={{ color: 'var(--color-status-error)' }}>
                  {t('pt.matrix.dup', { size: formatBytes(g.duplicateBytes) })}
                </span>
              </button>
              {openKey === g.key && (
                <div className="mt-2 space-y-1.5 border-t border-gray-200/60 pt-2 dark:border-white/10">
                  {g.copies.map((c) => (
                    <div key={`${c.id}-${c.serverName}-${c.kind}`} className="flex flex-wrap items-center gap-2 text-caption1">
                      <span className="hidden shrink-0 text-gray-500 dark:text-gray-400 sm:inline">
                        {c.serverName || c.kind || '-'}
                      </span>
                      <span className="min-w-0 flex-1 truncate">{c.site || t('pt.unknownSite')}</span>
                      <StatusChip status={c.status} />
                      <span className="shrink-0 tabular-nums">{formatBytes(c.size)}</span>
                      <span className="shrink-0 tabular-nums text-gray-500 dark:text-gray-400">↑{formatBytes(c.uploaded)}</span>
                    </div>
                  ))}
                </div>
              )}
            </Panel>
          ))}
        </div>
      )}
      <p className="text-caption1 text-gray-500 dark:text-gray-400">{t('pt.matrix.disclaimer')}</p>
    </div>
  )
}

// ---------- 发种工作台 ----------

const EMPTY_PUBLISH = {
  title: '',
  name: '',
  site: '',
  category: '',
  sizeBytes: '',
  mediaInfo: '',
  screenshots: '',
  subtitle: '',
  video: '',
  audio: '',
  resolution: '',
  year: '',
  country: '',
  genre: '',
  description: '',
}

function PublishTab() {
  const { t } = useTranslation()
  const [form, setForm] = useState(EMPTY_PUBLISH)
  const [torrent, setTorrent] = useState<File | null>(null)
  const [preview, setPreview] = useState<{ bbcode: string; warnings: string[] } | null>(null)
  const [busy, setBusy] = useState(false)

  const payload = () => ({
    title: form.title.trim(),
    name: form.name.trim(),
    site: form.site.trim(),
    category: form.category.trim(),
    sizeBytes: Math.max(0, Math.round(Number(form.sizeBytes) * 1024 * 1024 * 1024) || 0),
    mediaInfo: form.mediaInfo,
    screenshots: form.screenshots
      .split('\n')
      .map((s) => s.trim())
      .filter(Boolean),
    subtitle: form.subtitle.trim(),
    video: form.video.trim(),
    audio: form.audio.trim(),
    resolution: form.resolution.trim(),
    year: Number(form.year) || 0,
    country: form.country.trim(),
    genre: form.genre.trim(),
    description: form.description.trim(),
  })

  const doPreview = async () => {
    setBusy(true)
    try {
      setPreview(await ptApi.publishPreview(payload()))
    } catch {
      /* 拦截器已提示 */
    } finally {
      setBusy(false)
    }
  }

  const doKit = async () => {
    setBusy(true)
    try {
      const blob = await ptApi.publishKit(payload(), torrent)
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `seedark-publish-${(form.name || 'kit').replace(/[\\/:*?"<>|]/g, '_')}.zip`
      a.click()
      URL.revokeObjectURL(url)
      toast.success(t('pt.publish.kitDone'))
    } catch {
      /* 拦截器已提示 */
    } finally {
      setBusy(false)
    }
  }

  const copy = async () => {
    if (!preview) return
    const ok = await copyText(preview.bbcode)
    if (ok) toast.success(t('pt.publish.copied'))
    else toast.error(t('pt.publish.copyFailed'))
  }

  const field = (key: keyof typeof EMPTY_PUBLISH, label: string, placeholder?: string, textarea = false) => (
    <label className="block space-y-1">
      <span className="text-caption1 text-gray-500 dark:text-gray-400">{label}</span>
      {textarea ? (
        <textarea
          value={form[key]}
          onChange={(e) => setForm((f) => ({ ...f, [key]: e.target.value }) as typeof EMPTY_PUBLISH)}
          placeholder={placeholder}
          rows={key === 'mediaInfo' ? 8 : 3}
          className="w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-caption1 shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        />
      ) : (
        <Input value={form[key]} onChange={(e) => setForm((f) => ({ ...f, [key]: e.target.value }) as typeof EMPTY_PUBLISH)} placeholder={placeholder} className="h-8 text-footnote" />
      )}
    </label>
  )

  return (
    <div className="grid gap-3 lg:grid-cols-2">
      <div className="space-y-2">
        <p className="text-caption1 text-gray-500 dark:text-gray-400">{t('pt.publish.intro')}</p>
        {field('name', t('pt.publish.name'), t('pt.publish.namePh'))}
        <div className="grid grid-cols-2 gap-2">
          {field('title', t('pt.publish.title'))}
          {field('site', t('pt.publish.site'))}
          {field('category', t('pt.publish.category'))}
          {field('resolution', t('pt.publish.resolution'))}
          {field('year', t('pt.publish.year'))}
          {field('sizeBytes', t('pt.publish.sizeGiB'))}
          {field('video', t('pt.publish.video'))}
          {field('audio', t('pt.publish.audio'))}
          {field('subtitle', t('pt.publish.subtitle'))}
          {field('country', t('pt.publish.country'))}
          {field('genre', t('pt.publish.genre'))}
        </div>
        {field('screenshots', t('pt.publish.screenshots'), t('pt.publish.screenshotsPh'), true)}
        {field('mediaInfo', t('pt.publish.mediaInfo'), t('pt.publish.mediaInfoPh'), true)}
        {field('description', t('pt.publish.description'), undefined, true)}
        <label className="block space-y-1">
          <span className="text-caption1 text-gray-500 dark:text-gray-400">{t('pt.publish.torrentFile')}</span>
          <input
            type="file"
            accept=".torrent,application/x-bittorrent"
            onChange={(e) => setTorrent(e.target.files?.[0] ?? null)}
            className="block w-full text-caption1 file:mr-2 file:rounded-md file:border-0 file:bg-primary/10 file:px-2 file:py-1 file:text-caption1 file:text-primary"
          />
        </label>
        <div className="flex flex-wrap gap-2">
          <Button size="sm" className="gap-1.5" disabled={busy} onClick={() => void doPreview()}>
            {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Sprout className="h-3.5 w-3.5" />}
            {t('pt.publish.preview')}
          </Button>
          <Button variant="outline" size="sm" className="gap-1.5" disabled={busy} onClick={() => void doKit()}>
            <FileArchive className="h-3.5 w-3.5" />
            {t('pt.publish.kit')}
          </Button>
        </div>
      </div>

      <div className="space-y-2">
        {preview && preview.warnings.length > 0 && (
          <div className="rounded-tile border border-amber-400/40 bg-amber-400/10 p-2">
            <p className="mb-1 text-caption1 font-medium">{t('pt.publish.warnings')}</p>
            <ul className="list-inside list-disc space-y-0.5 text-caption1 text-gray-600 dark:text-gray-300">
              {preview.warnings.map((w) => (
                <li key={w}>{w}</li>
              ))}
            </ul>
          </div>
        )}
        <div className="flex items-center justify-between">
          <span className="text-caption1 text-gray-500 dark:text-gray-400">{t('pt.publish.bbcode')}</span>
          <Button variant="ghost" size="sm" className="h-7 gap-1 px-2 text-caption1" disabled={!preview} onClick={() => void copy()}>
            {preview ? <Copy className="h-3 w-3" /> : <Check className="h-3 w-3" />}
            {t('pt.publish.copy')}
          </Button>
        </div>
        <textarea
          readOnly
          value={preview?.bbcode ?? ''}
          placeholder={t('pt.publish.previewPh')}
          rows={18}
          className="w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-caption1 shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        />
        <p className="text-caption1 text-gray-500 dark:text-gray-400">{t('pt.publish.disclaimer')}</p>
      </div>
    </div>
  )
}

// ---------- 站点规则 ----------

const EMPTY_PROFILE: PTSiteProfile = {
  id: '',
  name: '',
  match: '',
  minSeedDays: 0,
  minRatio: 0,
  hrSeedHours: 0,
  budgetGb: 0,
  enabled: true,
}

function SitesTab() {
  const { t } = useTranslation()
  const { loading, error, data, reload } = useLoader(() => ptApi.sites(), [])
  const [draft, setDraft] = useState<PTSiteProfile>(EMPTY_PROFILE)
  const [busy, setBusy] = useState(false)

  const edit = (p: PTSiteProfile) => setDraft({ ...p })

  const save = async () => {
    if (!draft.name.trim()) {
      toast.error(t('pt.sites.nameRequired'))
      return
    }
    setBusy(true)
    try {
      await ptApi.saveSite({ ...draft, name: draft.name.trim(), match: draft.match.trim() })
      toast.success(t('pt.sites.saved'))
      setDraft(EMPTY_PROFILE)
      reload()
    } catch {
      /* 拦截器已提示 */
    } finally {
      setBusy(false)
    }
  }

  const remove = async (p: PTSiteProfile) => {
    const ok = await confirm({ title: t('pt.sites.deleteTitle'), content: t('pt.sites.deleteConfirm', { name: p.name }), danger: true })
    if (!ok) return
    try {
      await ptApi.deleteSite(p.id)
      if (draft.id === p.id) setDraft(EMPTY_PROFILE)
      reload()
    } catch {
      /* 拦截器已提示 */
    }
  }

  const seed = async () => {
    setBusy(true)
    try {
      const r = await ptApi.seedSites()
      toast.success(t('pt.sites.seeded', { count: r.created }))
      reload()
    } catch {
      /* 拦截器已提示 */
    } finally {
      setBusy(false)
    }
  }

  if (loading) return <Loading />
  if (error || !data) return <LoadError onRetry={reload} />

  const num = (key: 'minSeedDays' | 'minRatio' | 'hrSeedHours' | 'budgetGb', label: string, hint?: string) => (
    <label className="block space-y-1">
      <span className="text-caption1 text-gray-500 dark:text-gray-400">{label}</span>
      <Input
        type="number"
        min={0}
        step={key === 'minRatio' ? 0.1 : 1}
        value={String(draft[key])}
        onChange={(e) => setDraft((d) => ({ ...d, [key]: Number(e.target.value) || 0 }) as PTSiteProfile)}
        className="h-8 text-footnote"
      />
      {hint && <span className="block text-caption2 text-gray-400 dark:text-gray-500">{hint}</span>}
    </label>
  )

  return (
    <div className="space-y-3">
      <p className="text-caption1 text-gray-500 dark:text-gray-400">{t('pt.sites.intro')}</p>

      <Panel>
        <SectionTitle icon={ShieldAlert} text={draft.id ? t('pt.sites.editTitle') : t('pt.sites.addTitle')} />
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
          <label className="block space-y-1">
            <span className="text-caption1 text-gray-500 dark:text-gray-400">{t('pt.sites.name')}</span>
            <Input value={draft.name} onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))} placeholder={t('pt.sites.namePh')} className="h-8 text-footnote" />
          </label>
          <label className="block space-y-1">
            <span className="text-caption1 text-gray-500 dark:text-gray-400">{t('pt.sites.match')}</span>
            <Input value={draft.match} onChange={(e) => setDraft((d) => ({ ...d, match: e.target.value }))} placeholder={t('pt.sites.matchPh')} className="h-8 text-footnote" />
          </label>
          {num('minSeedDays', t('pt.sites.minSeedDays'))}
          {num('minRatio', t('pt.sites.minRatio'))}
          {num('hrSeedHours', t('pt.sites.hrSeedHours'))}
          {/* ⑦ 保种预算：只提示超支，不触发清理 */}
          {num('budgetGb', t('pt.sites.budget'), t('pt.sites.budgetHint'))}
          <label className="flex items-center gap-2 self-end pb-1 text-footnote">
            <Switch checked={draft.enabled} onCheckedChange={(v) => setDraft((d) => ({ ...d, enabled: v }))} />
            {t('pt.sites.enabled')}
          </label>
        </div>
        <div className="mt-2 flex flex-wrap gap-2">
          <Button size="sm" className="gap-1.5" disabled={busy} onClick={() => void save()}>
            <Save className="h-3.5 w-3.5" />
            {t('pt.sites.save')}
          </Button>
          <Button variant="ghost" size="sm" className="gap-1.5" onClick={() => setDraft(EMPTY_PROFILE)}>
            <Plus className="h-3.5 w-3.5" />
            {t('pt.sites.new')}
          </Button>
        </div>
        <p className="mt-1 text-caption2 text-gray-500 dark:text-gray-400">{t('pt.sites.hint')}</p>
      </Panel>

      <div className="flex flex-wrap items-center gap-2">
        <span className="text-footnote text-gray-500 dark:text-gray-400">
          {t('pt.sites.detected', { count: data.detected.length })}
        </span>
        <Button variant="outline" size="sm" className="h-7 gap-1 px-2 text-caption1" disabled={busy || data.detected.length === 0} onClick={() => void seed()}>
          <Plus className="h-3 w-3" />
          {t('pt.sites.seed')}
        </Button>
        {data.detected.map((d) => (
          <span key={d.site} className="rounded-full bg-gray-500/10 px-2 py-0.5 text-caption1 text-gray-600 dark:text-gray-300">
            {d.site} · {d.count}
          </span>
        ))}
      </div>

      {data.profiles.length === 0 ? (
        <Empty label={t('pt.sites.empty')} />
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[640px] border-collapse text-footnote">
            <thead className="text-caption1 text-gray-500 dark:text-gray-400">
              <tr>
                <th className="px-2 py-1.5 text-left font-medium">{t('pt.sites.name')}</th>
                <th className="px-2 py-1.5 text-left font-medium">{t('pt.sites.match')}</th>
                <th className="px-2 py-1.5 text-right font-medium">{t('pt.sites.minSeedDays')}</th>
                <th className="px-2 py-1.5 text-right font-medium">{t('pt.sites.minRatio')}</th>
                <th className="px-2 py-1.5 text-right font-medium">{t('pt.sites.hrSeedHours')}</th>
                <th className="px-2 py-1.5 text-left font-medium">{t('pt.sites.enabled')}</th>
                <th className="px-2 py-1.5 text-right font-medium">{t('pt.sites.actions')}</th>
              </tr>
            </thead>
            <tbody>
              {data.profiles.map((p) => (
                <tr key={p.id} className="border-t border-gray-200/60 dark:border-white/10">
                  <td className="px-2 py-1.5">{p.name}</td>
                  <td className="px-2 py-1.5 text-gray-500 dark:text-gray-400">{p.match}</td>
                  <td className="px-2 py-1.5 text-right tabular-nums">{p.minSeedDays}</td>
                  <td className="px-2 py-1.5 text-right tabular-nums">{p.minRatio}</td>
                  <td className="px-2 py-1.5 text-right tabular-nums">{p.hrSeedHours}</td>
                  <td className="px-2 py-1.5">{p.enabled ? t('pt.sites.on') : t('pt.sites.off')}</td>
                  <td className="px-2 py-1.5 text-right">
                    <Button variant="ghost" size="sm" className="h-7 px-2 text-caption1" onClick={() => edit(p)}>
                      {t('pt.sites.edit')}
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-7 px-2 text-caption1"
                      style={{ color: 'var(--color-status-error)' }}
                      onClick={() => void remove(p)}
                    >
                      <Trash2 className="h-3 w-3" />
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

// ---------- 外壳 ----------

export function PTDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t } = useTranslation()
  const [tab, setTab] = useState<string>('overview')

  useEffect(() => {
    if (open) setTab('overview')
  }, [open])

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="flex max-h-[88vh] w-[min(96vw,68rem)] max-w-6xl flex-col gap-3 overflow-hidden p-4 sm:p-6">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Sprout className="h-5 w-5 text-primary" />
            {t('pt.title')}
          </DialogTitle>
          <p className="text-caption1 text-gray-500 dark:text-gray-400">{t('pt.subtitle')}</p>
        </DialogHeader>
        <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col">
          <TabsList className="glass-subcard h-auto w-full justify-start gap-1 overflow-x-auto rounded-tile p-1">
            {TABS.map(({ id, icon: Icon }) => (
              <TabsTrigger key={id} value={id} className="shrink-0 gap-1.5 text-caption1 data-[state=active]:bg-background">
                <Icon className="h-3.5 w-3.5" />
                {t(`pt.tab.${id}`)}
              </TabsTrigger>
            ))}
          </TabsList>
          <div className="min-h-0 flex-1 overflow-y-auto px-0.5 py-3">
            <TabsContent value="overview">
              <OverviewTab />
            </TabsContent>
            <TabsContent value="calendar">
              <CalendarTab />
            </TabsContent>
            <TabsContent value="ledger">
              <LedgerTab />
            </TabsContent>
            <TabsContent value="vault">
              <VaultTab />
            </TabsContent>
            <TabsContent value="matrix">
              <MatrixTab />
            </TabsContent>
            <TabsContent value="publish">
              <PublishTab />
            </TabsContent>
            <TabsContent value="sites">
              <SitesTab />
            </TabsContent>
          </div>
        </Tabs>
      </DialogContent>
    </Dialog>
  )
}