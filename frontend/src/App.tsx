import { useEffect, useMemo, useRef, useState } from 'react'
import { Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { torrentApi, sessionApi, settingsApi } from '@/api/torrent'
import { AddTorrent } from '@/components/AddTorrent'
import { AppUpdatePrompt } from '@/components/AppUpdatePrompt'
import { AuthTokenDialog } from '@/components/AuthTokenDialog'
import { CreateTorrentDialog } from '@/components/CreateTorrentDialog'
import { Dashboard } from '@/components/Dashboard'
import { DesktopSidebar, MobileDrawer, STATUS_ITEMS } from '@/components/Sidebar'
import { PwaUpdatePrompt } from '@/components/PwaUpdatePrompt'
import { SettingsModal } from '@/components/SettingsModal'
import { SpeedPolicyManager } from '@/components/SpeedPolicyManager'
import { StatusBar } from '@/components/StatusBar'
import { TorrentDetail } from '@/components/TorrentDetail'
import { TorrentList } from '@/components/TorrentList'
import { BatchCleanDialog } from '@/components/ToolsDialogs'
import { useFilter } from '@/hooks/useFilter'
import { useGlassChrome } from '@/hooks/useGlassChrome'
import { useKeyboardShortcuts } from '@/hooks/useKeyboardShortcuts'
import { useResponsive } from '@/hooks/useResponsive'
import { useSafeAreaGuard } from '@/hooks/useSafeAreaGuard'
import { useSwipeGesture } from '@/hooks/useSwipeGesture'
import { useWebSocket } from '@/hooks/useWebSocket'
import { usePlatform } from '@/platform'
import { useAppStore } from '@/stores/appStore'
import type { AppState } from '@/stores/appStore'
import type { Torrent } from '@/types'
import { TopBar } from '@/components/TopBar'
import { ListHeader } from '@/components/ListHeader'
import { FloatingBar } from '@/components/FloatingBar'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { ConfirmHost } from '@/lib/confirm'
import { ThemeColors } from '@/lib/theme'
import { cn, cssVars } from '@/lib/utils'
import { Toaster } from 'sonner'
import '@/i18n'

// 玻璃折射强度由 CSS 消费（--glass-blur-*），系统值作为两个开关的初始状态
const SYS_TRANSPARENCY = '(prefers-reduced-transparency: reduce)'
const SYS_MOTION = '(prefers-reduced-motion: reduce)'
const SYS_CONTRAST = '(prefers-contrast: more)'

// 滑动切分类只属于列表区：顶栏搜索框里横向选字、抽屉里的下滑都不该被当成切分类
const SWIPE_CHROME = '.tm-dock-top, .tm-dock-bottom, [role="dialog"], [role="menu"], .tm-ctx'

export default function App() {
  const { t, i18n } = useTranslation()
  const { isMobile } = useResponsive()
  const { env, ready, can } = usePlatform()
  useWebSocket()

  const torrents = useAppStore((s) => s.torrents)
  const filters = useAppStore((s) => s.filters)
  const theme = useAppStore((s) => s.theme)
  const themePreset = useAppStore((s) => s.themePreset)
  const language = useAppStore((s) => s.language)
  const selectedIds = useAppStore((s) => s.selectedIds)
  const torrentSites = useAppStore((s) => s.torrentSites)
  const setTorrentSites = useAppStore((s) => s.setTorrentSites)
  const wsStatus = useAppStore((s) => s.wsStatus)
  const setTheme = useAppStore((s) => s.setTheme)
  const setLanguage = useAppStore((s) => s.setLanguage)
  const setSession = useAppStore((s) => s.setSession)
  const setSemanticDirs = useAppStore((s) => s.setSemanticDirs)
  const fontSize = useAppStore((s) => s.fontSize)
  const sortField = useAppStore((s) => s.sortField)
  const sortOrder = useAppStore((s) => s.sortOrder)
  const sidebarWidth = useAppStore((s) => s.sidebarWidth)
  const statusFilterVisible = useAppStore((s) => s.statusFilterVisible)
  const reduceGlass = useAppStore((s) => s.reduceGlass)
  const reduceMotion = useAppStore((s) => s.reduceMotion)
  const moreContrast = useAppStore((s) => s.moreContrast)
  const glassOpacity = useAppStore((s) => s.glassOpacity)
  const wallpaper = useAppStore((s) => s.wallpaper)
  const a11yTouched = useAppStore((s) => s.a11yTouched)
  const speedOpen = useAppStore((s) => s.speedOpen)
  const speedPreset = useAppStore((s) => s.speedPreset)
  const closeSpeedPolicy = useAppStore((s) => s.closeSpeedPolicy)

  const [drawerOpen, setDrawerOpen] = useState(false)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [dashboardOpen, setDashboardOpen] = useState(false)
  const [createOpen, setCreateOpen] = useState(false)
  const [cleanOpen, setCleanOpen] = useState(false)
  const [addOpen, setAddOpen] = useState(false)
  const [detailTorrent, setDetailTorrent] = useState<Torrent | null>(null)
  const [labelModalOpen, setLabelModalOpen] = useState(false)
  const [batchLabels, setBatchLabels] = useState<string[]>([])
  const [labelMode, setLabelMode] = useState<'set' | 'add' | 'remove'>('set')
  // 拖拽添加到对话框的初始内容
  const [addInitial, setAddInitial] = useState<{ files: File[]; text: string } | null>(null)
  // 供快捷键回调读取最新过滤结果（避免 hooks 顺序依赖）
  const filteredRef = useRef<Torrent[]>([])

  // 停靠玻璃的几何/材质联动：顶栏与底栏实测位置决定滚动层避让与渐隐
  const shellRef = useRef<HTMLDivElement>(null)
  const topDockRef = useRef<HTMLDivElement>(null)
  const statusDockRef = useRef<HTMLDivElement>(null)
  // 边界只留给状态栏。居中悬浮胶囊不占边界：它只有 56px 宽，但任何预留净空
  // 都是按整条列表的宽度算的，等于为一颗圆钮让出一条全宽空带。
  // 它不遮挡列表靠的是自身"滑动即隐藏"（见 useHideOnScroll），不是靠让位。
  useGlassChrome({
    shell: shellRef,
    top: topDockRef,
    bottoms: [statusDockRef],
  })

  // fnOS App 等内嵌 WebView 虚报 inset-top 时归零，顶栏不被推离屏幕顶
  useSafeAreaGuard()

  useKeyboardShortcuts({
    onAdd: () => setAddOpen(true),
    onSelectAll: () => {
      useAppStore.getState().setSelection(filteredRef.current.map((x) => x.id))
    },
  })

  // 移动端左右滑动切换分类：顺序与显隐都复用侧栏列表，滑得到的分类必然点得到
  const statusOrder = useMemo(
    () =>
      STATUS_ITEMS.filter((item) => item.key === 'all' || statusFilterVisible[item.key] !== false).map(
        (item) => item.key,
      ),
    [statusFilterVisible],
  )
  const swipeRef = useRef(filters.status[0] || 'all')
  useEffect(() => {
    swipeRef.current = filters.status[0] || 'all'
  }, [filters.status])

  const stepStatus = async (delta: number) => {
    const at = statusOrder.indexOf(swipeRef.current)
    // 当前分类已不在列表中（如刚被隐藏的分组）时从左端重新起步
    const next = statusOrder[(at < 0 ? 0 : at) + delta]
    if (!next) return
    await sessionApi.get().catch(() => {})
    useAppStore.getState().setFilters({ status: [next] })
  }

  useSwipeGesture({
    threshold: 80,
    ignoreSelector: SWIPE_CHROME,
    onSwipeLeft: () => void stepStatus(1),
    onSwipeRight: () => void stepStatus(-1),
  })

  const filtered = useFilter(torrents, filters, torrentSites, sortField, sortOrder)
  useEffect(() => {
    filteredRef.current = filtered
  }, [filtered])
  const filteredIds = useMemo(() => filtered.map((x) => x.id), [filtered])
  const allLabels = useMemo(
    () => Array.from(new Set(torrents.flatMap((x) => x.labels ?? []))).sort((a, b) => a.localeCompare(b, 'zh')),
    [torrents],
  )

  useEffect(() => {
    document.documentElement.classList.toggle('dark', theme === 'dark')
    document.documentElement.dataset.theme = themePreset
  }, [theme, themePreset])

  useEffect(() => {
    document.documentElement.style.fontSize = `${fontSize}px`
  }, [fontSize])

  // 未手动干预前跟随系统偏好；用户在「界面设置」里拨过任一开关后即由用户接管
  useEffect(() => {
    if (a11yTouched) return
    const queries = [
      { mql: window.matchMedia(SYS_TRANSPARENCY), key: 'reduceGlass' },
      { mql: window.matchMedia(SYS_MOTION), key: 'reduceMotion' },
      { mql: window.matchMedia(SYS_CONTRAST), key: 'moreContrast' },
    ] as const
    const sync = () => {
      const next = Object.fromEntries(queries.map(({ mql, key }) => [key, mql.matches]))
      const cur = useAppStore.getState()
      if (next.reduceGlass !== cur.reduceGlass || next.reduceMotion !== cur.reduceMotion || next.moreContrast !== cur.moreContrast) {
        useAppStore.setState(next as Pick<AppState, 'reduceGlass' | 'reduceMotion' | 'moreContrast'>)
      }
    }
    sync()
    queries.forEach(({ mql }) => mql.addEventListener('change', sync))
    return () => queries.forEach(({ mql }) => mql.removeEventListener('change', sync))
  }, [a11yTouched])

  useEffect(() => {
    const root = document.documentElement
    root.dataset.a11yGlass = reduceGlass ? 'reduce' : 'full'
    root.dataset.a11yMotion = reduceMotion ? 'reduce' : 'full'
    root.dataset.a11yContrast = moreContrast ? 'more' : 'normal'
    root.style.setProperty('--glass-user-opacity', String(glassOpacity / 100))
    // 浓度滑杆反向驱动折射增强：低填充必须配高模糊/饱和，玻璃才"有东西可透"
    root.style.setProperty('--glass-boost', String(1 - glassOpacity / 100))
  }, [reduceGlass, reduceMotion, moreContrast, glassOpacity])

  // 壁纸：data-wallpaper 开关控制 CSS 侧洗罩层，--shell-wallpaper 提供 url()
  useEffect(() => {
    const root = document.documentElement
    if (wallpaper) {
      root.dataset.wallpaper = 'on'
      root.style.setProperty('--shell-wallpaper', `url("${wallpaper}")`)
    } else {
      delete root.dataset.wallpaper
      root.style.removeProperty('--shell-wallpaper')
    }
  }, [wallpaper])

  // 跟随宿主环境（如 fnOS 的系统主题/语言）；通用平台拿不到 env，保持用户自设
  useEffect(() => {
    if (env.theme) {
      setTheme(env.theme === 'dark' ? 'dark' : 'light')
    }
  }, [env.theme, setTheme])

  useEffect(() => {
    if (env.language) {
      const lang = String(env.language)
      setLanguage(lang.startsWith('zh') ? 'zh' : 'en')
    }
  }, [env.language, setLanguage])

  useEffect(() => {
    torrentApi.sites().then(setTorrentSites).catch(() => {})
  }, [setTorrentSites])

  useEffect(() => {
    if (wsStatus === 'connected') {
      torrentApi.sites().then(setTorrentSites).catch(() => {})
    }
  }, [wsStatus, setTorrentSites])

  // 语义路径（仅 fnOS）：把 Transmission 报上的 /vol1/... 目录转成宿主展示名。
  // 展示属增强：失败按「暂不可用」处理，退避重试后仍失败才本会话放弃——
  // 此前任何一次失败都直接永久放弃，宿主一次瞬时抖动就会让整个会话都看不到语义路径
  // （只有切语言才能重置）。这里改为指数退避：瞬时故障可自愈，持续故障也不会在
  // 2s 轮询下反复打转换接口。连续失败 3 次即认为宿主侧不可用，放弃本会话。
  const [semanticAttempt, setSemanticAttempt] = useState(0)
  const canSemantic = ready && can('paths.semantic')
  const semanticDirsKey = useMemo(
    () => Array.from(new Set(torrents.map((x) => x.downloadDir).filter(Boolean))).join('\u0000'),
    [torrents],
  )

  // 语言切换后旧映射全部失效：清空并重置退避，让下方 effect 重新全量拉取
  useEffect(() => {
    useAppStore.getState().resetSemantic()
    setSemanticAttempt(0)
  }, [language])

  useEffect(() => {
    if (!canSemantic) return
    const known = useAppStore.getState().semanticDirs
    const missing = (semanticDirsKey ? semanticDirsKey.split('\u0000') : []).filter((d) => !(d in known))
    if (missing.length === 0) return
    // 退避档位：0s（首次）→ 3s → 15s；用尽后本会话不再重试
    const backoff = [0, 3000, 15000][semanticAttempt]
    if (backoff === undefined) return
    const timer = setTimeout(() => {
      torrentApi
        .semanticPaths(missing, language)
        .then((res) => {
          if (res.available) {
            setSemanticDirs(res.map)
            // 成功即复位：后续新出现的目录仍走首次快速路径
            setSemanticAttempt(0)
          } else {
            setSemanticAttempt((n) => n + 1)
          }
        })
        .catch(() => setSemanticAttempt((n) => n + 1))
    }, backoff)
    return () => clearTimeout(timer)
  }, [semanticDirsKey, language, canSemantic, setSemanticDirs, semanticAttempt])

  useEffect(() => {
    void i18n.changeLanguage(language)
  }, [language])

  useEffect(() => {
    sessionApi
      .get()
      .then(setSession)
      .catch(() => setSession(null))
  }, [setSession])

  // 轮询间隔：后端 WS 推流节奏与前端 REST 兜底轮询共用，启动时读一次并同步到 store
  useEffect(() => {
    settingsApi
      .get()
      .then((s) => useAppStore.getState().setPollInterval(s.pollInterval ?? ''))
      .catch(() => {})
  }, [])

  useEffect(() => {
    if (i18n.language) setLanguage(i18n.language.startsWith('zh') ? 'zh' : 'en')
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const saveBatchLabels = async () => {
    try {
      if (labelMode === 'set') {
        await torrentApi.updateMany(selectedIds, { labels: batchLabels })
      } else {
        const selected = torrents.filter((t) => selectedIds.includes(t.id))
        if (labelMode === 'add') {
          await Promise.all(selected.map((t) =>
            torrentApi.update(t.id, { labels: [...new Set([...(t.labels ?? []), ...batchLabels])] }),
          ))
        } else {
          const removeSet = new Set(batchLabels)
          for (const id of selectedIds) {
            const t = selected.find((x) => x.id === id)
            await torrentApi.update(id, { labels: (t?.labels ?? []).filter((l) => !removeSet.has(l)) })
          }
        }
      }
      setLabelModalOpen(false)
      setBatchLabels([])
    } catch {
      // handled by interceptor
    }
  }

  // 拖拽添加种子文件/磁力链接
  const dropRef = useRef<HTMLDivElement>(null)
  const [dragOver, setDragOver] = useState(false)
  const [, forceUpdate] = useState(0)
  const dragFiles = useRef<string[]>([])
  const dragText = useRef<string>('')

  useEffect(() => {
    const el = dropRef.current
    if (!el) return
    const prevent = (e: DragEvent) => { e.preventDefault(); e.stopPropagation() }
    const onDragOver = (e: DragEvent) => {
      prevent(e)
      setDragOver(true)
      dragFiles.current = Array.from(e.dataTransfer?.files || []).map(f => f.name)
      const text = e.dataTransfer?.getData('text/plain')
      if (text?.trim()) dragText.current = text.trim()
      forceUpdate(n => n + 1)
    }
    const onDragLeave = (e: DragEvent) => { if (!el.contains(e.relatedTarget as Node)) setDragOver(false) }
    const onDrop = (e: DragEvent) => {
      prevent(e)
      setDragOver(false)
      const files = Array.from(e.dataTransfer?.files || [])
      const text = e.dataTransfer?.getData('text/plain')?.trim() ?? ''
      if (files.length > 0) {
        setAddInitial({ files, text })
        setAddOpen(true)
        return
      }
      if (text) {
        setAddInitial({ files: [], text })
        setAddOpen(true)
      }
    }
    el.addEventListener('dragover', onDragOver)
    el.addEventListener('dragleave', onDragLeave)
    el.addEventListener('drop', onDrop)
    return () => {
      el.removeEventListener('dragover', onDragOver)
      el.removeEventListener('dragleave', onDragLeave)
      el.removeEventListener('drop', onDrop)
    }
  }, [])

  // 主内容列在侧栏右侧全出血铺开；移动端铺满整宽
  const contentLeft = isMobile
    ? 'calc(var(--shell-gap) + var(--safe-left))'
    : `calc(var(--shell-gap) * 2 + var(--safe-left) + ${sidebarWidth}px)`

  return (
    <>
      <div ref={shellRef} className="tm-shell h-full w-full text-gray-800 dark:text-gray-100" style={{ height: '100dvh' }}>
      {/* 内容层：全出血，列表从屏幕顶端开始滚动，才会真正穿过停靠玻璃 */}
      <div className="tm-content" style={{ left: contentLeft }} ref={dropRef}>
        {dragOver && (
          // 拖拽落点提示：此前是整屏 bg-primary/20 + 16px 图标 + 20px 标题，
          // 深色主题下大块品牌蓝非常刺眼，也与玻璃材质克制的观感冲突。
          // 改为「轻着色 + 一枚玻璃卡片」：底色降到 /8，提示内容收进 glass-panel-strong
          <div className="absolute inset-0 z-50 flex items-center justify-center bg-primary/[0.07] border-2 border-dashed border-primary/50 rounded-panel pointer-events-none">
            <div className="glass-panel-strong rounded-panel px-6 py-5 text-center max-w-md">
              <span className="mx-auto mb-3 flex h-11 w-11 items-center justify-center rounded-full bg-primary/12">
                <Plus className="w-6 h-6 text-primary" strokeWidth={2.2} />
              </span>
              <p className="text-subhead font-semibold text-gray-800 dark:text-gray-100">{t('common.dragToAdd')}</p>
              {dragFiles.current.length > 0 && (
                <p className="text-footnote mt-1.5 text-gray-500 dark:text-gray-400 break-all">{dragFiles.current.join(', ')}</p>
              )}
              {dragText.current && dragText.current.startsWith('magnet:') && (
                <p className="text-footnote mt-1 text-gray-500 dark:text-gray-400 truncate">{dragText.current}</p>
              )}
            </div>
          </div>
        )}
        <TorrentList torrents={filtered} isMobile={isMobile} onOpenDetail={setDetailTorrent} onOpenBatchClean={() => setCleanOpen(true)} />
      </div>

      {/* 侧栏：顶边与顶栏齐平，其内容同样穿过玻璃 */}
      {!isMobile && <DesktopSidebar />}

      {/* 顶部停靠栏 */}
      <div ref={topDockRef} className="tm-dock-top flex flex-col gap-3">
        <TopBar
          isMobile={isMobile}
          onOpenDrawer={() => setDrawerOpen(true)}
          onOpenSettings={() => setSettingsOpen(true)}
          onOpenAdd={() => setAddOpen(true)}
          onOpenDashboard={() => setDashboardOpen(true)}
          onOpenCreate={() => setCreateOpen(true)}
          onOpenLabels={() => {
            const first = torrents.find((t) => selectedIds.includes(t.id))
            setBatchLabels(first?.labels ?? [])
            setLabelModalOpen(true)
          }}
          filteredIds={filteredIds}
        />
        {/* 分类标题行仅移动端保留（桌面端排序/视图/刷新已并入顶栏） */}
        {isMobile && <ListHeader count={filtered.length} isMobile onOpenDashboard={() => setDashboardOpen(true)} onOpenCreate={() => setCreateOpen(true)} />}
      </div>

      {/* 底部停靠栏 */}
      <div ref={statusDockRef} className="tm-dock-bottom">
        <StatusBar isMobile={isMobile} />
      </div>

      {/* 悬浮工具胶囊（移动端）：收起为一个 +，展开为高频操作 */}
      {isMobile && (
        <FloatingBar
          isMobile
          onOpenAdd={() => setAddOpen(true)}
          onOpenClean={() => setCleanOpen(true)}
          onOpenSettings={() => setSettingsOpen(true)}
          scrollHost={dropRef}
        />
      )}

      {isMobile && (
        <MobileDrawer
          visible={drawerOpen}
          onClose={() => setDrawerOpen(false)}
          onOpenSettings={() => {
            setDrawerOpen(false)
            setSettingsOpen(true)
          }}
        />
      )}

      <AddTorrent
        open={addOpen}
        onClose={() => {
          setAddOpen(false)
          setAddInitial(null)
        }}
        initialFiles={addInitial?.files}
        initialText={addInitial?.text}
      />
      <Dashboard open={dashboardOpen} onClose={() => setDashboardOpen(false)} />
      <CreateTorrentDialog open={createOpen} onClose={() => setCreateOpen(false)} />
      <BatchCleanDialog open={cleanOpen} onClose={() => setCleanOpen(false)} />
      <TorrentDetail torrent={detailTorrent} onClose={() => setDetailTorrent(null)} onOpenChange={(open) => { if (!open) setDetailTorrent(null) }} isMobile={isMobile} />
      <SettingsModal open={settingsOpen} onClose={() => setSettingsOpen(false)} />
      {/* 组内总限速：设置页与侧边栏分组右键共用同一实例 */}
      <SpeedPolicyManager open={speedOpen} onClose={closeSpeedPolicy} preset={speedPreset} />
      <PwaUpdatePrompt />
      <AppUpdatePrompt />
      <AuthTokenDialog />
      <ConfirmHost />
      {/* 系统 chrome 颜色跟随品牌预设，否则 PWA 下顶栏色带断层会破坏玻璃延伸感 */}
      <ThemeColors />

      {/* 批量打标签（覆盖 / 添加 / 移除 三种模式） */}
      <Dialog open={labelModalOpen} onOpenChange={setLabelModalOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{`${t('action.editLabels')} (${selectedIds.length})`}</DialogTitle>
          </DialogHeader>
          <div className="grid grid-cols-3 gap-1 p-1 rounded-lg bg-gray-100 dark:bg-gray-800/70">
            {([
              ['set', t('labels.modeSet')],
              ['add', t('labels.modeAdd')],
              ['remove', t('labels.modeRemove')],
            ] as const).map(([mode, label]) => (
              <button
                key={mode}
                onClick={() => setLabelMode(mode)}
                className={cn(
                  'h-8 rounded-md text-footnote font-medium transition-colors',
                  labelMode === mode
                    ? 'bg-white dark:bg-gray-700 text-primary shadow-sm'
                    : 'text-gray-500 hover:text-gray-700 dark:text-gray-400',
                )}
              >
                {label}
              </button>
            ))}
          </div>
          <Select
            value={batchLabels[batchLabels.length - 1]}
            onValueChange={(v) => setBatchLabels((prev) => [...prev, v])}
          >
            <SelectTrigger className="w-full">
              <SelectValue placeholder={t('action.editLabels')} />
            </SelectTrigger>
            <SelectContent>
              {allLabels.map((l) => (
                <SelectItem key={l} value={l}>{l}</SelectItem>
              ))}
            </SelectContent>
          </Select>
          {batchLabels.length > 0 && (
            <div className="flex flex-wrap gap-1 mt-2">
              {batchLabels.map((label, idx) => (
                <span
                  key={idx}
                  className="tm-chip inline-flex items-center gap-1 px-2 py-0.5 rounded-full border text-footnote"
                  style={cssVars({ '--chip': 'var(--hue-purple)' })}
                >
                  {label}
                  <button
                    onClick={() => setBatchLabels((prev) => prev.filter((_, i) => i !== idx))}
                    aria-label={`${t('common.remove')} ${label}`}
                    className="tm-hug ml-0.5 hover:text-red-500"
                  >
                    ×
                  </button>
                </span>
              ))}
            </div>
          )}
          <div className="flex justify-end gap-2 mt-4">
            <Button variant="outline" onClick={() => setLabelModalOpen(false)}>{t('common.cancel')}</Button>
            <Button onClick={saveBatchLabels}>{t('common.confirm')}</Button>
          </div>
        </DialogContent>
      </Dialog>
      </div>
      {/* Toaster 必须挂在 .tm-shell 之外：shell 是 z-index:1 的层叠上下文，
          弹窗遮罩是 body 级 z-50 portal，留在 shell 里所有 toast 都会被弹窗盖住（如复制反馈） */}
      <Toaster
        position="top-center"
        offset={{ top: 'calc(var(--pad-top) + 8px)' }}
        mobileOffset={{ top: 'calc(var(--pad-top) + 8px)' }}
        toastOptions={{ classNames: { toast: 'tm-toast' } }}
      />
    </>
  )
}
