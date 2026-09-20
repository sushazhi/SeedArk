import React, { useEffect, useMemo, useState } from 'react'
import {
  Check,
  FilePlus2,
  FolderCog,
  FolderInput,
  FolderOpen,
  Gauge,
  Globe,
  Menu,
  MoreHorizontal,
  Pause,
  Play,
  Plus,
  Repeat,
  RotateCcw,
  Search,
  Settings,
  ShieldCheck,
  SquareCheck,
  SquareX,
  Tags,
  Trash2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { torrentApi } from '@/api/torrent'
import { useTorrentActions } from '@/hooks/useTorrentActions'
import { useRevealPath } from '@/hooks/useRevealPath'
import { useSemanticPath } from '@/hooks/useSemanticPath'
import { usePlatform } from '@/platform'
import { hasFilterConditions, useAppStore } from '@/stores/appStore'
import { DEMO_MODE } from '@/demo'
import { cn, cssVars } from '@/lib/utils'
import { tagColor } from '@/utils/tagColor'
import { translateError } from '@/utils/errorText'
import { toast } from '@/lib/toast'
import { copyText } from '@/utils/clipboard'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { ListControls } from '@/components/ListHeader'
import { BatchLimitDialog, BatchMoveDialog, RemoveTorrentDialog } from '@/components/ToolsDialogs'

interface Props {
  onOpenSettings: () => void
  onOpenAdd: () => void
  onOpenDashboard: () => void
  onOpenCreate: () => void
  onOpenLabels: () => void
  filteredIds: number[]
  isMobile?: boolean
  onOpenDrawer?: () => void
}

// iOS 风格圆形图标按钮：无边框、悬停着色、移动端 44pt 触控目标
function ToolBtn({ icon: Icon, title, onClick, danger, disabled, large }: {
  icon: React.ElementType
  title: string
  onClick: () => void
  danger?: boolean
  disabled?: boolean
  large?: boolean
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      title={title}
      aria-label={title}
      className={cn(
        large ? 'h-11 w-11' : 'h-9 w-9',
        'tm-press flex items-center justify-center rounded-full shrink-0',
        disabled
          ? 'text-gray-300 dark:text-gray-600 cursor-not-allowed'
          : danger
            ? 'text-gray-500 dark:text-gray-400 hover:text-red-500 hover:bg-red-500/10'
            : 'text-gray-500 dark:text-gray-400 hover:text-primary hover:bg-primary/10',
      )}
    >
      <Icon className={large ? 'w-6 h-6' : 'w-5 h-5'} strokeWidth={1.75} />
    </button>
  )
}

// 预设主题（与 index.css 中 html[data-theme='x'] 一一对应）
const THEME_PRESETS: { id: string; from: string; to: string }[] = [
  { id: 'blue', from: '#007aff', to: '#8b5cf6' },
  { id: 'green', from: '#34c759', to: '#30d158' },
  { id: 'purple', from: '#af52de', to: '#7d5fff' },
  { id: 'orange', from: '#ff9500', to: '#ffb340' },
  { id: 'pink', from: '#ff2d55', to: '#ff5e8a' },
  { id: 'teal', from: '#30b0c7', to: '#5ac8fa' },
  { id: 'graphite', from: '#6e6e73', to: '#aeaeb2' },
]

// 玻璃悬浮顶栏：单行 —— Logo + 搜索 + 批量操作（选中时出现在同行）+ 排序/视图/刷新 + 添加 + 头像
export const TopBar: React.FC<Props> = ({ onOpenSettings, onOpenAdd, onOpenDashboard, onOpenCreate, onOpenLabels, filteredIds, isMobile, onOpenDrawer }) => {
  const { t } = useTranslation()
  const search = useAppStore((s) => s.filters.search)
  const filters = useAppStore((s) => s.filters)
  const setFilters = useAppStore((s) => s.setFilters)
  const clearAllFilters = useAppStore((s) => s.clearAllFilters)
  const selectedIds = useAppStore((s) => s.selectedIds)
  const clearSelection = useAppStore((s) => s.clearSelection)
  const setSelection = useAppStore((s) => s.setSelection)
  const session = useAppStore((s) => s.session)
  const theme = useAppStore((s) => s.theme)
  const themePreset = useAppStore((s) => s.themePreset)
  const setTheme = useAppStore((s) => s.setTheme)
  const setThemePreset = useAppStore((s) => s.setThemePreset)
  const language = useAppStore((s) => s.language)
  const setLanguage = useAppStore((s) => s.setLanguage)
  const actions = useTorrentActions()
  const { can, pickFolder } = usePlatform()
  const revealPath = useRevealPath()
  const sem = useSemanticPath()
  const [avatarOpen, setAvatarOpen] = useState(false)
  const [removeOpen, setRemoveOpen] = useState(false)
  const [moveOpen, setMoveOpen] = useState(false)
  const [limitOpen, setLimitOpen] = useState(false)
  const [busy, setBusy] = useState(false)

  const hasSelection = selectedIds.length > 0
  // 与空列表里的「清除全部筛选」用同一判定：状态 / 站点 / 目录 / 标签 / 错误 / 搜索
  const hasActiveFilters = useMemo(() => hasFilterConditions(filters), [filters])

  const clearAll = clearAllFilters

  // 触屏既没有 Ctrl+A 也没有 Shift 连选，这颗按钮是手机端唯一的分组批量入口
  const allFilteredSelected = useMemo(() => {
    if (filteredIds.length === 0) return false
    const selected = new Set(selectedIds)
    return filteredIds.every((id) => selected.has(id))
  }, [filteredIds, selectedIds])

  const toggleSelectAll = () => {
    if (!allFilteredSelected) {
      setSelection(filteredIds)
      return
    }
    // 只摘掉可见部分：换筛选条件前选中的种子仍在选区里，静默清空会让用户丢掉看不见的选择
    const visible = new Set(filteredIds)
    setSelection(selectedIds.filter((id) => !visible.has(id)))
  }

  // 宿主快捷操作：打开下载目录 / 选取配置目录（无宿主能力时复制路径兜底）
  const openDownloadDir = async () => {
    const dir = session?.downloadDir
    if (!dir) return
    await revealPath(dir)
  }
  const configDir = async () => {
    const picked = await pickFolder()
    // 取消或宿主没返回：与设置面板、种子菜单的选择器一致，不打扰
    if (!picked) return
    void copyText(picked).then((ok) => { if (ok) toast.success(t('sidebar.dirCopied', { dir: picked })) })
  }

  // 搜索框是压扁而非卸载，光标否则会留在看不见的输入框里
  useEffect(() => {
    if (!isMobile || !hasSelection) return
    const el = document.activeElement
    if (el instanceof HTMLElement && el.dataset.searchInput !== undefined) el.blur()
  }, [isMobile, hasSelection])

  // 批量校验 / 重新汇报（循环调用单种子接口，统一提示）
  const runLoop = async (fn: (id: number) => Promise<unknown>, key: string) => {
    if (selectedIds.length === 0) return
    setBusy(true)
    try {
      for (const id of selectedIds) await fn(id)
      toast.success(t(key))
      clearSelection()
    } catch {
      // 拦截器已提示
    } finally {
      setBusy(false)
    }
  }

  return (
    <header className="tm-dock glass-panel rounded-dock px-3 sm:px-4 py-1.5 sm:py-2 select-none">
      <div className="flex items-center gap-1.5 sm:gap-2 min-w-0">
        {/* 移动端抽屉按钮 */}
        {isMobile && onOpenDrawer && (
          <button
            onClick={onOpenDrawer}
            className="h-11 w-11 shrink-0 rounded-full flex items-center justify-center text-gray-600 dark:text-gray-300 hover:bg-white/60 dark:hover:bg-white/10 transition-colors"
            aria-label="menu"
          >
            <Menu className="w-5 h-5" />
          </button>
        )}

        {/* Logo + 标题 */}
        <div className="flex items-center gap-2.5 shrink-0">
          <span className="w-9 h-9 rounded-full bg-gradient-to-br from-[var(--brand-grad-from)] to-[var(--brand-grad-to)] flex items-center justify-center shadow-[0_4px_14px_color-mix(in_srgb,var(--color-primary)_34%,transparent)]">
            <span className="text-white font-bold text-body tracking-tight">SA</span>
          </span>
          <div className="leading-tight hidden sm:flex items-center gap-2">
            <span className="font-semibold text-subhead text-gray-800 dark:text-gray-100">
              <span className="text-primary">trpanel</span>
              {can('app.update') && <span className="text-gray-400 dark:text-gray-500 font-medium text-body"> for fnOS</span>}
            </span>
            {DEMO_MODE && (
              <span
                title={t('demo.tip')}
                className="hidden md:inline-flex items-center px-2 py-0.5 rounded-full border border-amber-500/40 bg-amber-500/10 text-amber-600 dark:text-amber-400 text-caption2 font-medium whitespace-nowrap"
              >
                {t('demo.badge')}
              </span>
            )}
          </div>
        </div>

        {/* 搜索：选中时收缩让位给批量操作（移动端直接收起）；无选中时相对整行居中 */}
        <div
          className={cn(
            'min-w-0 flex items-center transition-[width,opacity,transform,scale] duration-[350ms] [transition-timing-function:var(--ease-spring)]',
            isMobile && hasSelection
              ? 'w-0 opacity-0 scale-90 pointer-events-none'
              : hasSelection
                ? 'w-56 shrink-0'
                : 'mx-auto w-full max-w-md',
          )}
        >
          <div className="relative w-full">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-400" />
            <Input
              id="search-input"
              data-search-input
              enterKeyHint="search"
              aria-label={t('topbar.searchAria')}
              value={search}
              onChange={(e) => setFilters({ search: e.target.value })}
              placeholder={t('topbar.searchPlaceholder')}
              className={cn(
                isMobile ? 'h-11' : 'h-9',
                'pl-9 pr-3 rounded-full bg-white/60 dark:bg-white/10 border-transparent shadow-inner text-body focus-visible:ring-primary/50',
              )}
            />
          </div>
        </div>

        {/* 批量操作 + 标签筛选 + 重置（选中时出现在同一行，窄屏横向滚动） */}
        {hasSelection && (
          <div className="flex-1 min-w-0 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
            <div className="flex items-center gap-0.5 w-max">
              <ToolBtn
                icon={allFilteredSelected ? SquareX : SquareCheck}
                large={isMobile}
                title={t(allFilteredSelected ? 'topbar.deselectAllFiltered' : 'topbar.selectAllFiltered', { n: filteredIds.length })}
                disabled={busy || filteredIds.length === 0}
                onClick={toggleSelectAll}
              />

              <span className="w-px h-5 bg-gray-200 dark:bg-white/10 mx-1 shrink-0" aria-hidden />

              <ToolBtn icon={Play} large={isMobile} title={t('action.start')} disabled={busy} onClick={() => void actions.start(selectedIds)} />
              <ToolBtn icon={Pause} large={isMobile} title={t('action.stop')} disabled={busy} onClick={() => void actions.stop(selectedIds)} />
              <ToolBtn icon={Trash2} large={isMobile} title={t('action.remove')} danger disabled={busy} onClick={() => setRemoveOpen(true)} />
              <ToolBtn icon={FolderInput} large={isMobile} title={t('action.changePath')} disabled={busy} onClick={() => setMoveOpen(true)} />
              <ToolBtn icon={Gauge} large={isMobile} title={t('action.batchLimit')} disabled={busy} onClick={() => setLimitOpen(true)} />
              <ToolBtn icon={Tags} large={isMobile} title={t('action.editLabels')} disabled={busy} onClick={onOpenLabels} />
              <ToolBtn icon={ShieldCheck} large={isMobile} title={t('action.verify')} disabled={busy} onClick={() => runLoop((id) => torrentApi.verify(id), 'toast.verifyStarted')} />
              <ToolBtn icon={Repeat} large={isMobile} title={t('action.reannounce')} disabled={busy} onClick={() => runLoop((id) => torrentApi.reannounce(id), 'toast.reannounced')} />

              <span className="h-5 px-2 ml-1 rounded-full bg-primary/10 text-primary text-caption1 font-semibold flex items-center whitespace-nowrap">
                {selectedIds.length}
              </span>

              <span className="w-px h-5 bg-gray-200 dark:bg-white/10 mx-1.5 shrink-0" />

              <ToolBtn icon={RotateCcw} large={isMobile} title={t('filter.clear')} disabled={!hasActiveFilters} onClick={clearAll} />
            </div>
          </div>
        )}

        {/* 右侧操作：只保留高频项（排序/视图/刷新/添加/头像），
            低频的宿主目录与创建种子收进 ⋯ 溢出菜单。
            此前这条常驻 9 颗图标按钮，顶栏永远处于"满配"状态，视觉上没有落点 */}
        <div className="flex items-center gap-1.5 sm:gap-2 shrink-0">
          {/* 排序 / 视图 / 刷新（桌面端并入顶栏，移动端在 ListHeader） */}
          {!isMobile && <ListControls compact onOpenDashboard={onOpenDashboard} />}

          {!isMobile && (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <button
                  className="tm-press h-9 w-9 rounded-full flex items-center justify-center text-gray-500 dark:text-gray-400 hover:text-primary hover:bg-primary/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/60"
                  aria-label={t('topbar.moreActions')}
                  title={t('topbar.moreActions')}
                >
                  <MoreHorizontal className="w-5 h-5" strokeWidth={1.75} />
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent className="glass-panel-strong w-56" align="end">
                <DropdownMenuItem className="text-footnote gap-2" onSelect={onOpenCreate}>
                  <FilePlus2 className="w-4 h-4 text-primary" />
                  {t('createTorrent.title')}
                </DropdownMenuItem>
                {/* 宿主快捷操作：仅宿主支持文件管理器时显示 */}
                {can('fs.revealPath') && (
                  <>
                    <DropdownMenuSeparator className="my-0.5" />
                    <DropdownMenuItem className="text-footnote gap-2" onSelect={() => void openDownloadDir()}>
                      <FolderOpen className="w-4 h-4 text-primary" />
                      {t('action.openDownloadDir')}
                    </DropdownMenuItem>
                    <DropdownMenuItem className="text-footnote gap-2" onSelect={() => void configDir()}>
                      <FolderCog className="w-4 h-4 text-primary" />
                      {t('action.configDir')}
                    </DropdownMenuItem>
                  </>
                )}
              </DropdownMenuContent>
            </DropdownMenu>
          )}

          {!isMobile && (
            <Button
              onClick={onOpenAdd}
              className="tm-btn-primary tm-press h-9 sm:h-10 w-9 sm:w-auto px-0 sm:px-4 rounded-full text-white gap-1.5 text-body font-medium"
              aria-label={t('topbar.addTask')}
            >
              <Plus className="w-4 h-4" />
              <span className="hidden sm:inline">{t('topbar.addTask')}</span>
            </Button>
          )}

          {/* 头像菜单：统计 / 主题 / 语言 / 设置 */}
          <Popover open={avatarOpen} onOpenChange={setAvatarOpen}>
            <PopoverTrigger asChild>
              <button
                className="h-11 w-11 sm:h-10 sm:w-10 rounded-full bg-gradient-to-br from-[var(--brand-grad-to)] to-[var(--brand-grad-from)] text-white text-body font-semibold flex items-center justify-center shadow-[0_4px_14px_color-mix(in_srgb,var(--color-primary)_34%,transparent)] hover:scale-105 transition-transform duration-[220ms] [transition-timing-function:var(--ease-spring)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/70 focus-visible:ring-offset-2 focus-visible:ring-offset-white/70 dark:focus-visible:ring-offset-gray-900"
                aria-label={t('topbar.quickSettings')}
              >
                {language === 'zh' ? '中' : 'EN'}
              </button>
            </PopoverTrigger>
            <PopoverContent className="glass-panel-strong w-64 p-1.5" align="end">
              {/* 主题：浅色/深色 + 预设色板（选择不关闭弹层，便于实时预览） */}
              <div className="px-3 pt-2.5 pb-2">
                <p className="text-caption1 font-medium text-gray-400 mb-1.5">{t('theme.title')}</p>
                <div className="flex rounded-lg bg-gray-100 dark:bg-gray-800/80 p-0.5 mb-2.5">
                  {(['light', 'dark'] as const).map((m) => (
                    <button
                      key={m}
                      onClick={() => setTheme(m)}
                      className={cn(
                        'flex-1 h-8 rounded-md text-footnote font-medium transition-colors',
                        theme === m
                          ? 'bg-white dark:bg-gray-700 text-primary shadow-sm'
                          : 'text-gray-500 hover:text-gray-700 dark:text-gray-400',
                      )}
                    >
                      {t(`theme.${m}`)}
                    </button>
                  ))}
                </div>
                <div className="flex items-center justify-between">
                  {THEME_PRESETS.map((p) => (
                    <button
                      key={p.id}
                      onClick={() => setThemePreset(p.id)}
                      title={t(`theme.preset.${p.id}`)}
                      aria-label={t(`theme.preset.${p.id}`)}
                      className={cn(
                        // tm-hug：色板视觉 32px，触屏用透明 ::after 外扩到 44pt 命中区。
                        // 弹层走 glass-panel-strong（非 .tm-dialog），吃不到全局的 40px 提升
                        'tm-hug w-8 h-8 rounded-full flex items-center justify-center transition-transform duration-[220ms] [transition-timing-function:var(--ease-spring)] hover:scale-110 active:scale-90',
                        themePreset === p.id && 'ring-2 ring-offset-2 ring-offset-white/80 dark:ring-offset-gray-900',
                      )}
                      style={{
                        background: `linear-gradient(135deg, ${p.from}, ${p.to})`,
                        ...(themePreset === p.id ? { '--tw-ring-color': p.from } : {}),
                      } as React.CSSProperties}
                    >
                      {themePreset === p.id && <Check className="w-3.5 h-3.5 text-white" strokeWidth={3.5} />}
                    </button>
                  ))}
                </div>
              </div>
              <div className="mx-1.5 mb-1 h-px bg-gray-200/70 dark:bg-white/10" />

              <button
                onClick={() => { setLanguage(language === 'zh' ? 'en' : 'zh'); setAvatarOpen(false) }}
                className="w-full flex items-center gap-2 px-3 py-3 rounded-lg text-body text-left hover:bg-white/60 dark:hover:bg-white/10"
              >
                <Globe className="w-4 h-4 text-primary" />
                {language === 'zh' ? 'English' : '中文'}
              </button>
              <button
                onClick={() => { setAvatarOpen(false); onOpenSettings() }}
                className="w-full flex items-center gap-2 px-3 py-3 rounded-lg text-body text-left hover:bg-white/60 dark:hover:bg-white/10"
              >
                <Settings className="w-4 h-4 text-gray-500" />
                {t('common.settings')}
              </button>
            </PopoverContent>
          </Popover>
        </div>
      </div>

      {/* 激活筛选标签（仅在有筛选时显示） */}
      {hasActiveFilters && (
        <div className="w-full flex flex-wrap items-center gap-1 px-1 pt-2">
          {filters.labels.map((label) => (
            <FilterChip
              key={label}
              label={label === '__none__' ? t('nav.noLabel') : `#${label}`}
              onRemove={() => setFilters({ labels: filters.labels.filter((l) => l !== label) })}
              color={label === '__none__' ? '#9ca3af' : tagColor(label)}
            />
          ))}
          {filters.sites.map((siteId) => (
            <FilterChip
              key={siteId}
              label={siteId === '__other__' ? t('site.other') : `#${siteId}`}
              onRemove={() => setFilters({ sites: filters.sites.filter((s) => s !== siteId) })}
              color="var(--hue-blue)"
            />
          ))}
          {filters.downloadDirs.map((dir) => (
            <FilterChip
              key={dir}
              label={sem(dir)}
              onRemove={() => setFilters({ downloadDirs: filters.downloadDirs.filter((d) => d !== dir) })}
              color="var(--hue-green)"
            />
          ))}
          {filters.error.map((err) => (
            <FilterChip
              key={err}
              label={translateError(err, t)}
              onRemove={() => setFilters({ error: filters.error.filter((e) => e !== err) })}
              color="var(--hue-red)"
            />
          ))}
          {filters.search && (
            <FilterChip label={`"${filters.search}"`} onRemove={() => setFilters({ search: '' })} color="var(--hue-orange)" />
          )}
        </div>
      )}

      {/* 对话框 */}
      <RemoveTorrentDialog open={removeOpen} ids={selectedIds} onClose={() => setRemoveOpen(false)} />
      <BatchMoveDialog open={moveOpen} ids={selectedIds} onClose={() => setMoveOpen(false)} />
      <BatchLimitDialog open={limitOpen} ids={selectedIds} onClose={() => setLimitOpen(false)} />
    </header>
  )
}

// ========== FilterChip ==========
interface FilterChipProps {
  label: string
  onRemove: () => void
  color: string
}

const FilterChip: React.FC<FilterChipProps> = ({ label, onRemove, color }) => {
  const { t } = useTranslation()
  return (
    <span
      className="tm-chip inline-flex items-center gap-1 px-2 py-1 rounded-full border text-footnote font-medium"
      style={cssVars({ '--chip': color })}
    >
      {label}
      <button
        onClick={onRemove}
        aria-label={t('common.remove')}
        className="tm-hug -mr-1 h-6 w-6 grid place-items-center rounded-full opacity-70 hover:opacity-100"
      >
        ×
      </button>
    </span>
  )
}
