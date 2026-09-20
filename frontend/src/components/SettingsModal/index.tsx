import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import {
  Accessibility,
  Bug,
  ExternalLink,
  FolderOpen,
  Info,
  Palette,
  Plug,
  Server,
  SlidersHorizontal,
  Workflow,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { client, request } from '@/api/client'
import { APP_BASE } from '@/platform/appBase'
import { serverApi, sessionApi, torrentApi, updateApi, type UpdateCheckResult, type UpdateStatus } from '@/api/torrent'
import { AutoMoveManager } from '@/components/AutoMoveManager'
import { McpManager } from '@/components/McpManager'
import { SeedPolicyManager } from '@/components/SeedPolicyManager'
import { QBFields, QBSaveBar, useQBDraft } from './QBSettings'
import { NumInput, PaneNav, Row, ScrollArea, Section, SmallSelect } from './controls'
import { usePlatform } from '@/platform'
import { useResponsive } from '@/hooks/useResponsive'
import { useAppStore } from '@/stores/appStore'
import { toast } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import type { DownloaderKind, ServerInfo, Session, SessionStatus, SettingsSection } from '@/types'
import {
  DOWNLOADER_OPTIONS,
  defaultUrlFor,
  downloaderMeta,
  isOtherDefault,
  sectionIcon,
} from '@/components/SettingsModal/downloaders'

// 设置弹窗的一节：既是移动端长滚动里的一个分节，也是桌面端左栏的一个导航项。
// 两者同源——加/删一节只改这一份数据，状态不会漂移。
interface PaneDef {
  id: string
  label: string
  icon: ReactNode
  node: ReactNode
}

// qBittorrent 自述分节的图标已改为按语义名由驱动声明（见 downloaders.tsx 的 sectionIcon）

// 定时限速：周几位掩码（Transmission 语义：Mon=1 ... Sun=64，0=每天）
const DAY_BITS = [
  { bit: 1, label: 'Mon' },
  { bit: 2, label: 'Tue' },
  { bit: 4, label: 'Wed' },
  { bit: 8, label: 'Thu' },
  { bit: 16, label: 'Fri' },
  { bit: 32, label: 'Sat' },
  { bit: 64, label: 'Sun' },
]
// 全部七天的掩码（0 表示"每天"，等价于七天全选）
const DAY_ALL = DAY_BITS.reduce((acc, d) => acc | d.bit, 0)

// 每 30 分钟一个时间选项
const TIME_OPTIONS = Array.from({ length: 48 }, (_, i) => {
  const h = Math.floor(i / 2)
  const m = (i % 2) * 30
  const s = `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`
  return { value: s, label: s }
})
const timeToStr = (min: number) => `${String(Math.floor(min / 60)).padStart(2, '0')}:${String(min % 60).padStart(2, '0')}`
const strToMin = (s: string) => { const [h, m] = s.split(':').map(Number); return h * 60 + (m || 0) }

// 设置面板的一个目标：index 为服务器索引，null 表示「仅 .env 配置的当前连接」
interface SettingsTarget {
  key: string
  label: string
  kind: DownloaderKind
  index: number | null
}

// ========== 壁纸：本地图片 → 压缩 data URL ==========
// 壁纸走 localStorage 持久化，原图动辄数 MB 会撑爆配额；
// 统一缩到长边 1920 + JPEG 0.82，通常落到 200–400KB。PNG 的透明区填白避免糊黑
function fileToWallpaper(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const img = new Image()
    const objUrl = URL.createObjectURL(file)
    img.onload = () => {
      const scale = Math.min(1, 1920 / Math.max(img.width, img.height))
      const w = Math.max(1, Math.round(img.width * scale))
      const h = Math.max(1, Math.round(img.height * scale))
      const canvas = document.createElement('canvas')
      canvas.width = w
      canvas.height = h
      const ctx = canvas.getContext('2d')
      if (!ctx) {
        URL.revokeObjectURL(objUrl)
        reject(new Error('canvas unavailable'))
        return
      }
      ctx.fillStyle = '#ffffff'
      ctx.fillRect(0, 0, w, h)
      ctx.drawImage(img, 0, 0, w, h)
      URL.revokeObjectURL(objUrl)
      resolve(canvas.toDataURL('image/jpeg', 0.82))
    }
    img.onerror = () => {
      URL.revokeObjectURL(objUrl)
      reject(new Error('invalid image'))
    }
    img.src = objUrl
  })
}

// ========== 路径输入框 ==========
// 受控于本地 state，仅在失焦/回车且内容变化时提交一次，
// 避免目录类字段每敲一个字符就发一次 PUT /session（请求洪水 + 输入跳动）。
// 会话数据与提交动作由调用方注入：设置面板按服务器标签切换后，
// 目录字段必须跟随当前选中的那台服务器，不能去读全局会话。
function DirInput({ field, session, onCommit, 'aria-label': ariaLabel }: {
  field: 'downloadDir' | 'incompleteDir'
  session: Session | null
  onCommit: (patch: Record<string, unknown>) => Promise<boolean>
  'aria-label'?: string
}) {
  const { t } = useTranslation()
  const { can, pickFolder } = usePlatform()
  const serverValue = session?.[field] ?? ''
  const [draft, setDraft] = useState(serverValue)
  const [editing, setEditing] = useState(false)

  // 外部值变化（切换服务器/刷新）且当前未在编辑时同步
  useEffect(() => {
    if (!editing) setDraft(serverValue)
  }, [serverValue, editing])

  const commit = async (value: string) => {
    setEditing(false)
    const next = value.trim()
    if (next === serverValue || !session) return
    // 提交失败时回滚本地输入，否则界面显示的是没保存成功的新路径
    if (!(await onCommit({ [field]: next }))) setDraft(serverValue)
  }

  // 宿主目录选择器：选中后直接填入并提交
  const pick = async () => {
    const p = await pickFolder()
    if (!p) return
    setDraft(p)
    await commit(p)
  }

  return (
    // w-full：窄屏换行到标签下方后占满整行；桌面保持半宽
    <div className="flex items-center gap-1.5 w-full md:w-1/2">
      <Input
        aria-label={ariaLabel}
        value={draft}
        onFocus={() => setEditing(true)}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={() => void commit(draft)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') { e.preventDefault(); void commit(draft) }
          if (e.key === 'Escape') { setDraft(serverValue); setEditing(false) }
        }}
        className="h-8 text-footnote flex-1 min-w-0"
      />
      {can('fs.pickFolder') && (
        <Button
          type="button"
          variant="outline"
          size="sm"
          title={t('action.selectDir')}
          onClick={() => void pick()}
          className="h-8 shrink-0 gap-1 px-2"
        >
          <FolderOpen className="w-3.5 h-3.5" />
          <span className="hidden sm:inline">{t('action.selectDir')}</span>
        </Button>
      )}
    </div>
  )
}

// ========== 关于与检查更新 ==========
// 检查更新依赖宿主的 app.update 能力（后端也只在对应平台注册 /api/update 路由）
// 问题反馈渠道：界面（本仓库，已由 Transmission-WebUI-for-fnOS 更名为 SeedArk）
// 与飞牛应用分发（fpk 下载）分属两个 GitHub 仓库，
// 与后端 fnos 平台的 updateRepo（sushazhi/fnos-transmission）保持一致
const UI_REPO_ISSUES = 'https://github.com/sushazhi/seedark/issues'
const FPK_REPO_ISSUES = 'https://github.com/sushazhi/fnos-transmission/issues'

// framed=false 供桌面端双列布局使用：那时左栏导航已经写着「关于」，
// 再套一层同名可折叠卡片就是"点进去又套了一层"
function AboutSection({ transmissionVersion, framed = true }: { transmissionVersion?: string; framed?: boolean }) {
  const { t } = useTranslation()
  const { can } = usePlatform()
  const canUpdate = can('app.update')
  const [info, setInfo] = useState<UpdateCheckResult | null>(null)
  const [checking, setChecking] = useState(false)
  const [updStatus, setUpdStatus] = useState<UpdateStatus | null>(null)
  const timer = useRef<ReturnType<typeof setInterval> | null>(null)

  const stopPoll = () => {
    if (timer.current) {
      clearInterval(timer.current)
      timer.current = null
    }
  }
  useEffect(() => stopPoll, [])

  const check = async () => {
    setChecking(true)
    setUpdStatus(null)
    try {
      setInfo(await updateApi.check())
    } catch {
      // 拦截器已提示
    } finally {
      setChecking(false)
    }
  }

  const startPolling = () => {
    stopPoll()
    timer.current = setInterval(async () => {
      try {
        const s = await updateApi.status()
        setUpdStatus(s)
        if (!s.updating) stopPoll()
      } catch {
        // 单次轮询失败不中断整体进度跟踪
      }
    }, 2000)
  }

  const install = async () => {
    try {
      await updateApi.install()
      setUpdStatus({ updating: true, failed: false, progress: 0, message: '', latestVersion: info?.latestVersion ?? '', fpkFilename: '' })
      startPolling()
    } catch {
      // 拦截器已提示
    }
  }

  const downloading = !!updStatus?.updating
  const done = !!updStatus && !updStatus.updating && updStatus.progress >= 100
  const failed = !!updStatus && !updStatus.updating && updStatus.failed
  // 本次检查前服务端已下载好更新包：无需再点一键更新，直接下载
  const readyWithoutInstall = !!info?.downloadReady && !updStatus

  const body = (
    <>
      {/* 产品与版本：标题行右侧放检查更新，版本号与说明作为次要信息收进 hint */}
      <div className="flex items-center justify-between gap-3 py-1.5">
        <div className="min-w-0">
          <span className="text-body text-gray-600 dark:text-gray-300">
            SeedArk{canUpdate ? ' for fnOS' : ''}
          </span>
          <p className="text-caption1 text-gray-400 mt-0.5">
            {transmissionVersion ? `Transmission ${transmissionVersion}` : t('session.checkUpdateHint')}
          </p>
        </div>
        {canUpdate && (
          <Button size="sm" variant="outline" className="h-8 text-footnote shrink-0" disabled={checking || downloading} onClick={() => void check()}>
            {checking ? t('common.loading') : t('session.checkUpdate')}
          </Button>
        )}
      </div>
      {canUpdate && transmissionVersion && <p className="text-caption1 text-gray-400">{t('session.checkUpdateHint')}</p>}
      {info && (
        <div className="rounded-lg glass-section p-3 space-y-2">
          <div className="flex items-center gap-2 flex-wrap">
            {info.hasUpdate ? (
              <Badge className="bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-300">{t('session.newVersionFound')}</Badge>
            ) : (
              <Badge className="bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300">{t('session.alreadyLatest')}</Badge>
            )}
            <span className="text-footnote">v{info.currentVersion} → v{info.latestVersion} ({info.arch})</span>
          </div>
          {info.hasUpdate && info.changelog && (
            <div className="max-h-28 overflow-y-auto whitespace-pre-line text-caption1 text-gray-600 dark:text-gray-400">{info.changelog}</div>
          )}
          {info.hasUpdate && (
            <div className="space-y-2">
              {downloading && (
                <div>
                  <div className="h-1.5 rounded-full bg-gray-200/80 dark:bg-gray-700/60 overflow-hidden">
                    <div className="h-full bg-primary transition-all" style={{ width: `${updStatus?.progress ?? 0}%` }} />
                  </div>
                  <p className="mt-1 text-footnote">{updStatus?.progress ?? 0}% · {updStatus?.message}</p>
                </div>
              )}
              {failed && <p className="text-red-500 text-footnote">{updStatus?.message || t('session.updateFailed')}</p>}
              {done || readyWithoutInstall ? (
                <div className="space-y-1">
                  <Button asChild size="sm" className="h-8 text-footnote">
                    <a href={APP_BASE + '/api/update/download'} download={updStatus?.fpkFilename || undefined}>
                      {t('session.downloadFpk')}
                    </a>
                  </Button>
                  <p className="text-caption1 text-gray-400">{t('session.fpkInstallHint')}</p>
                </div>
              ) : info.fpkUrl ? (
                <div className="flex items-center gap-2 flex-wrap">
                  <Button size="sm" className="h-8 text-footnote" disabled={downloading || checking} onClick={() => void install()}>
                    {downloading ? t('session.updating') : t('session.updateNow')}
                  </Button>
                  {info.releaseUrl && (
                    <Button asChild size="sm" variant="outline" className="h-8 text-footnote">
                      <a href={info.releaseUrl} target="_blank" rel="noreferrer">{t('session.viewRelease')}</a>
                    </Button>
                  )}
                </div>
              ) : null}
            </div>
          )}
        </div>
      )}

      {/* 问题反馈：界面与飞牛应用分属两处仓库，按问题类型引导到对应的 Issue 区 */}
      <div className="rounded-lg glass-section p-3 space-y-2">
        <div className="flex items-center gap-1.5 text-footnote font-medium text-gray-700 dark:text-gray-200">
          <Bug className="w-3.5 h-3.5 text-primary" />
          {t('session.feedback')}
        </div>
        <p className="text-caption1 text-gray-400">{t(canUpdate ? 'session.feedbackHint' : 'session.feedbackHintUi')}</p>
        <div className="space-y-1.5">
          <FeedbackLink href={UI_REPO_ISSUES} label={t('session.feedbackUi')} />
          {/* fpk 安装/更新仅在飞牛环境存在，渠道行随之显隐 */}
          {canUpdate && <FeedbackLink href={FPK_REPO_ISSUES} label={t('session.feedbackFpk')} />}
        </div>
      </div>
    </>
  )

  // 移动端仍是可折叠分节；桌面端由左栏导航充当标题，直接给内容
  return framed ? <Section id="about" title={t('session.about')}>{body}</Section> : <>{body}</>
}

// 反馈渠道行：与弹窗内子卡片同一套边框/底色，整行可点 + 外链图标收在行尾
function FeedbackLink({ href, label }: { href: string; label: string }) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer"
      className="flex items-center gap-2 rounded-lg px-2.5 py-2 text-footnote bg-white/60 dark:bg-white/5 border border-gray-200/60 dark:border-white/10 hover:bg-white/90 dark:hover:bg-white/10 transition-colors"
    >
      <span className="min-w-0 truncate">{label}</span>
      <ExternalLink className="w-3 h-3 shrink-0 text-gray-400 ml-auto" />
    </a>
  )
}

// 服务端错误响应体里那句人话。axios 的 error.message 只有 "Request failed with
// status code 400"，把它贴到界面上等于什么都没说；后端在 data.message 里给的是
// 「当前连接与「xxx」不一致，请重新切换到该服务器」这类能指导下一步动作的原因。
function readApiMessage(err: unknown): string | undefined {
  const msg = (err as { response?: { data?: { message?: unknown } } })?.response?.data?.message
  return typeof msg === 'string' && msg.trim() !== '' ? msg : undefined
}

// 「当前连接与这台不一致」——由后端 memberBackend 抛出。用它决定是否给恢复按钮。
function isConnMismatch(err: unknown): boolean {
  return /不一致/.test(readApiMessage(err) ?? '')
}

// 下载器元信息（默认地址 / 显示名 / 短标 / 是否 schema 驱动）集中在注册表

// 设置弹窗：连接配置 + 轮询 + 队列 + Blocklist + 端口测试 + 界面 + 高级会话
export function SettingsModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t, i18n } = useTranslation()
  const { isMobile } = useResponsive()
  // 桌面端左栏选中的节。移动端不用它（窄屏放不下双列，走单列长滚动）
  const [pane, setPane] = useState('connection')
  const storeSession = useAppStore((s) => s.session)
  const setSession = useAppStore((s) => s.setSession)
  const setTorrentSites = useAppStore((s) => s.setTorrentSites)
  const setTorrents = useAppStore((s) => s.setTorrents)
  const clearSelection = useAppStore((s) => s.clearSelection)
  const setStorePollInterval = useAppStore((s) => s.setPollInterval)
  const fontSize = useAppStore((s) => s.fontSize)
  const setFontSize = useAppStore((s) => s.setFontSize)
  const singleLine = useAppStore((s) => s.singleLine)
  const setSingleLine = useAppStore((s) => s.setSingleLine)
  const showCheckboxes = useAppStore((s) => s.showCheckboxes)
  const setShowCheckboxes = useAppStore((s) => s.setShowCheckboxes)
  const reduceGlass = useAppStore((s) => s.reduceGlass)
  const reduceMotion = useAppStore((s) => s.reduceMotion)
  const moreContrast = useAppStore((s) => s.moreContrast)
  const setReduceGlass = useAppStore((s) => s.setReduceGlass)
  const setReduceMotion = useAppStore((s) => s.setReduceMotion)
  const setMoreContrast = useAppStore((s) => s.setMoreContrast)
  const glassOpacity = useAppStore((s) => s.glassOpacity)
  const setGlassOpacity = useAppStore((s) => s.setGlassOpacity)
  const wallpaper = useAppStore((s) => s.wallpaper)
  const setWallpaper = useAppStore((s) => s.setWallpaper)
  const wallpaperInputRef = useRef<HTMLInputElement>(null)

  // 壁过大时拒绝而不是硬塞：写满 localStorage 会把整个持久化 store 一起弄坏
  const pickWallpaper = async (file: File | undefined) => {
    if (!file) return
    if (!file.type.startsWith('image/')) {
      toast.error(t('session.wallpaperInvalid'))
      return
    }
    try {
      const dataUrl = await fileToWallpaper(file)
      if (dataUrl.length > 4_500_000) {
        toast.error(t('session.wallpaperTooLarge'))
        return
      }
      setWallpaper(dataUrl)
    } catch {
      toast.error(t('session.wallpaperInvalid'))
    }
  }

  const [url, setUrl] = useState('')
  const [user, setUser] = useState('')
  const [pass, setPass] = useState('')
  const [saving, setSaving] = useState(false)
  const [pollInterval, setPollInterval] = useState('2s')
  // 下载器类型：决定地址占位符与提交的 SA_TYPE（qBittorrent 填 WebUI 根地址）
  const [kind, setKind] = useState<DownloaderKind>('transmission')
  // 连接栏是本弹窗唯一的草稿区，存一份已保存快照用于判断是否"有未保存改动"
  const [connSnapshot, setConnSnapshot] = useState({ url: '', user: '', pollInterval: '2s', type: 'transmission' as DownloaderKind })
  const [status, setStatus] = useState<SessionStatus | null>(null)
  const [portOpen, setPortOpen] = useState<boolean | null>(null)
  const [testingPort, setTestingPort] = useState(false)
  const [blocklistUpdating, setBlocklistUpdating] = useState(false)
  const [blocklistUrl, setBlocklistUrl] = useState('')
  const [blocklistEnabled, setBlocklistEnabled] = useState(false)
  // 多服务器管理
  const [servers, setServers] = useState<ServerInfo[]>([])
  const [currentServerIndex, setCurrentServerIndex] = useState(0)
  const [openMove, setOpenMove] = useState(false)
  const [openPolicy, setOpenPolicy] = useState(false)
  const [openMcp, setOpenMcp] = useState(false)

  // ---- 设置目标（顶部标签）：同时连接多台时，一台一个标签，各读各的设置 ----
  const [targetKey, setTargetKey] = useState('')
  const [targetSession, setTargetSession] = useState<Session | null>(null)
  // 读取该服务器会话失败的原因（连不上、连接与服务器不一致等），
  // 直接显示在设置区，否则用户只看到一片空白
  const [targetError, setTargetError] = useState('')
  // 失败原因是「当前连接与这台服务器不一致」：这是唯一能自助恢复的一类，
  // 界面要给一个「重新切换」按钮，而不是让用户干看着报错
  const [targetConnMismatch, setTargetConnMismatch] = useState(false)
  // 恢复动作进行中：按钮置灰防连点
  const [switching, setSwitching] = useState(false)
  // 选中的服务器是不是「当前连接那台」。由服务端回报——状态索引与实际连接
  // 可能不同步（切换时写盘失败等），按索引猜会把设置同步到另一台上
  const [targetActive, setTargetActive] = useState(true)
  // 服务器列表加载完成前不渲染标签，避免先闪一个「当前连接」标签再跳成服务器标签
  const [serversLoaded, setServersLoaded] = useState(false)

  const targets = useMemo<SettingsTarget[]>(() => {
    if (!serversLoaded) return []
    const list = servers
      .map((s, index) => ({ ...s, index }))
      .filter((s) => s.enabled && s.url.trim() !== '')
    if (list.length === 0) {
      // 仅 .env 配置的部署没有服务器列表项，退化为单一「当前连接」标签
      return [{ key: 'env', label: t('session.targetCurrent'), kind: (status?.type ?? 'transmission') as DownloaderKind, index: null }]
    }
    return list.map((s) => ({
      key: `srv-${s.index}`,
      label: s.name || `${t('session.multiServer.server')} ${s.index + 1}`,
      kind: (s.type ?? 'transmission') as DownloaderKind,
      index: s.index,
    }))
  }, [serversLoaded, servers, status?.type, t])

  const target = targets.find((x) => x.key === targetKey) ?? targets.find((x) => x.index === currentServerIndex) ?? targets[0]

  // 选中的服务器被删掉后标签键会失效，回落到当前活动服务器
  useEffect(() => {
    if (!open || targets.length === 0) return
    if (targets.some((x) => x.key === targetKey)) return
    setTargetKey((targets.find((x) => x.index === currentServerIndex) ?? targets[0]).key)
  }, [open, targets, targetKey, currentServerIndex])

  // 当前标签的会话数据：仅 .env 配置的部署（没有服务器列表项）复用全局会话，
  // 其余一律用按索引读回的副本——写进全局会让仪表盘显示另一台的下载目录与限速
  const session = target?.index == null ? storeSession : targetSession

  // 按索引读一台服务器的会话。silent 用于写后回读：保留界面现值，
  // 避免刚保存完整块表单闪一下加载态
  const loadTargetSession = useCallback(async (index: number | null | undefined, silent = false) => {
    if (index == null) {
      setTargetSession(null)
      setTargetActive(true)
      return
    }
    if (!silent) {
      setTargetSession(null)
      setTargetError('')
    }
    try {
      const res = await sessionApi.getAt(index)
      setTargetSession(res.session)
      setTargetActive(res.active)
      setTargetError('')
      setTargetConnMismatch(false)
    } catch (err) {
      setTargetSession(null)
      // 直接渲染 err.message 会把 axios 的 "Request failed with status code 400" 原样贴到界面上，
      // 服务端返回的中文原因（如「当前连接与该服务器不一致，请重新切换」）反而被吞掉。
      // 响应体里的 message 才是能指导用户下一步动作的那句，优先取它。
      setTargetError(readApiMessage(err) ?? t('common.loadFailed'))
      // 「当前连接与这台不一致」是唯一可自助恢复的失败：给出重新切换的按钮，
      // 否则用户只能对着一句报错反复点同一个标签
      setTargetConnMismatch(isConnMismatch(err))
    }
  }, [t])

  // 切换标签即拉取对应服务器的会话
  useEffect(() => {
    if (!open) return
    void loadTargetSession(target?.index)
  }, [open, target?.index, loadTargetSession])

  // session 仅用于「打开弹窗时」初始化 blocklist，通过 ref 读取，
  // 避免 session 变化（如 patchSession 回写）导致整个表单被重置
  const sessionRef = useRef(session)
  sessionRef.current = session
  // 当前下载器能力自述：取当前标签自己那份（按索引读回，与取值同源）
  const caps = session?.caps
  // 驱动自述字段自带 zh/en 两套文案（见 QBSettings），这里定一次给分节标题用
  const lang: 'zh' | 'en' = (i18n.language || 'zh').startsWith('en') ? 'en' : 'zh'
  // 自述面板的草稿挂在这里而不是各节里：桌面端一次只渲染一节，草稿若随节卸载，
  // 翻页就会丢掉上一节的未保存改动
  const qb = useQBDraft(session?.prefs ?? {}, (patch) => patchSession({ prefs: patch }))

  useEffect(() => {
    if (!open) return
    let cancelled = false
    request(clientGetSettings()).then((d) => {
      if (cancelled) return
      const data = d as { url: string; user: string; type?: DownloaderKind; pollInterval?: string }
      const pi = data.pollInterval || '2s'
      const k: DownloaderKind = data.type === 'qbittorrent' ? 'qbittorrent' : 'transmission'
      // 后端没配地址时填上该类型的默认地址，而不是留个空框
      const u = data.url?.trim() ? data.url : defaultUrlFor(k)
      setUrl(u)
      setUser(data.user)
      setPass('')
      setKind(k)
      setPollInterval(pi)
      setConnSnapshot({ url: u, user: data.user, pollInterval: pi, type: k })
    }).catch(() => {})
    sessionApi.status().then((s) => { if (!cancelled) setStatus(s) }).catch(() => { if (!cancelled) setStatus(null) })
    setPortOpen(null)
    setBlocklistUrl(sessionRef.current?.blocklistUrl ?? '')
    setBlocklistEnabled(sessionRef.current?.blocklistEnabled ?? false)
    // 加载多服务器列表（后端持久化）
    serverApi.list().then((d) => {
      if (cancelled) return
      setServers(d.servers)
      setCurrentServerIndex(d.activeServer)
      setServersLoaded(true)
    }).catch(() => { if (!cancelled) setServersLoaded(true) })
    return () => { cancelled = true }
  }, [open])

  // 只提交「连接配置」这一栏：其余设置项都是即时保存，没有统一的提交动作。
  // 保存后留在弹窗里（后端不回读密码，本地清空即可），并刷新一次连接状态
  const save = async () => {
    if (!url.trim()) return
    setSaving(true)
    try {
      await request(clientPutSettings({ type: kind, url: url.trim(), user, pass, pollInterval }))
      toast.success(t('toast.updated'))
      setPass('')
      setConnSnapshot({ url: url.trim(), user, pollInterval, type: kind })
      // 兜底轮询间隔立即跟随新设置，无需刷新页面
      setStorePollInterval(pollInterval)
      sessionApi.get().then(setSession).catch(() => setSession(null))
      sessionApi.status().then(setStatus).catch(() => setStatus(null))
    } catch {
      // 拦截器已提示
    } finally {
      setSaving(false)
    }
  }

  // 连接栏是否存在未保存的改动
  const connDirty =
    url.trim() !== connSnapshot.url ||
    user !== connSnapshot.user ||
    pass !== '' ||
    kind !== connSnapshot.type ||
    pollInterval !== connSnapshot.pollInterval

  // 合并一次会话补丁。prefs 是嵌套的原生偏好字典，必须逐层合并：
  // 直接覆盖会把整份偏好替换成刚改的那一个键，其余全部丢失。
  const mergeSession = (cur: Session | null, patch: Record<string, unknown>): Session | null => {
    if (!cur) return cur
    const next = { ...cur, ...patch } as Session
    if (patch.prefs && typeof patch.prefs === 'object') {
      next.prefs = { ...(cur.prefs ?? {}), ...(patch.prefs as Record<string, unknown>) }
    }
    return next
  }

  // 设置项提交：没有服务器列表项的部署只有 /session 一条通道；有索引的一律写那台。
  // 返回是否成功——失败时调用方（目录输入、qB 草稿）需要保留本地输入以便重试。
  const patchSession = async (patch: Record<string, unknown>): Promise<boolean> => {
    const idx = target?.index
    try {
      if (idx == null) {
        await sessionApi.update(patch)
        // 取 store 里的最新值合并：同一 tick 内连续两次开关时，闭包里的 session
        // 仍是旧值，直接展开会把前一次改动覆盖掉
        setSession(mergeSession(useAppStore.getState().session, patch))
      } else {
        await sessionApi.updateAt(idx, patch)
        setTargetSession((cur) => mergeSession(cur, patch))
        // 写的就是当前连接那台时同步全局会话，仪表盘才会跟着变。
        // 凭服务端回报的 active，不按索引猜：状态索引与实际连接可能不同步
        if (targetActive) setSession(mergeSession(useAppStore.getState().session, patch))
        // 回读一次：服务端会做归一（如 qB 的 *_enabled 联动），
        // 只按提交值渲染会让界面与真实配置不一致
        void loadTargetSession(idx, true)
      }
      toast.success(t('toast.updated'))
      return true
    } catch {
      // 拦截器已提示
      return false
    }
  }

  const testPort = async () => {
    setTestingPort(true)
    try {
      const res = await sessionApi.portTest()
      setPortOpen(res.open)
    } catch {
      // 拦截器已提示
    } finally {
      setTestingPort(false)
    }
  }

  const updateBlocklist = async () => {
    setBlocklistUpdating(true)
    try {
      // 后端更新时读的是 session 里的 URL，输入框是草稿态，必须先回写再触发更新，
      // 否则改了地址点「立即更新」用的还是旧地址
      const current = sessionRef.current
      if (current && blocklistUrl !== (current.blocklistUrl ?? '')) {
        if (!(await patchSession({ blocklistUrl }))) return
      }
      const res = await sessionApi.blocklistUpdate()
      toast.success(`${t('toast.updated')} · ${res.entries}`)
    } catch {
      // 拦截器已提示
    } finally {
      setBlocklistUpdating(false)
    }
  }

  // 服务器列表改一行就是整表 PUT：逐键直接发请求会形成请求洪水，且乱序返回会把旧列表
  // 写回服务端；因此本地立即更新、请求按 400ms 防抖，只发最后一次
  const serverSaveTimer = useRef<number | null>(null)
  const persistServers = async (next: ServerInfo[]) => {
    try {
      const res = await serverApi.save(next)
      if (res?.warning) toast.warning(res.warning)
      else toast.success(t('toast.updated'))
    } catch {
      // 拦截器已提示。保存被拒时以服务端为准回读，避免本地残留后端没有的幽灵行
      // （幽灵行一删除就是「服务器不存在」）
      try {
        const list = await serverApi.list()
        setServers(list.servers)
        setCurrentServerIndex(list.activeServer)
      } catch {
        // 拦截器已提示
      }
    }
  }
  const saveServers = (next: ServerInfo[]) => {
    setServers(next)
    if (serverSaveTimer.current) window.clearTimeout(serverSaveTimer.current)
    serverSaveTimer.current = window.setTimeout(() => {
      serverSaveTimer.current = null
      void persistServers(next)
    }, 400)
  }

  // 删除服务器：走专用接口，后端会同步修正 activeServer 索引；若删的是当前服务器
  // 还会重连到备用服务器，因此这里必须重新拉取数据并清空选区
  const removeServer = async (idx: number) => {
    try {
      const res = await serverApi.remove(idx)
      const list = await serverApi.list()
      setServers(list.servers)
      setCurrentServerIndex(list.activeServer)
      // 后端可能只完成了部分动作（如删掉后切换备用服务器失败），必须显式告警
      if (res?.warning) toast.warning(res.warning)
      else toast.success(t('toast.updated'))
      clearSelection()
      torrentApi.list().then(setTorrents).catch(() => {})
      sessionApi.get().then(setSession).catch(() => setSession(null))
      sessionApi.status().then(setStatus).catch(() => setStatus(null))
      torrentApi.sites().then(setTorrentSites).catch(() => {})
    } catch {
      // 拦截器已提示
    }
  }

  const switchToServer = async (idx: number) => {
    try {
      const res = await serverApi.switch(idx)
      setCurrentServerIndex(res.index)
      if (res.warning) toast.warning(res.warning)
      else toast.success(t('common.connected'))
      // 不同服务器的种子 id 空间相互独立：必须清空选区并整表刷新，
      // 否则上一台的选中 id 会在新服务器上命中同号种子，被批量操作误伤
      clearSelection()
      torrentApi.list().then(setTorrents).catch(() => {})
      sessionApi.get().then(setSession).catch(() => setSession(null))
      sessionApi.status().then(setStatus).catch(() => setStatus(null))
      torrentApi.sites().then(setTorrentSites).catch(() => {})
    } catch {
      // 拦截器已提示
    }
  }

  // 恢复「当前连接 != 这台服务器」的失配：本质就是把活动连接真正切到这台。
  // 复用 switchToServer 的重连 + 清选区 + 整表刷新，最后把本面板的会话读回来。
  const reconnectTarget = async () => {
    const idx = target?.index
    if (idx == null) return
    setSwitching(true)
    try {
      // serverApi.switch 的 warning 语义是「部分成功」，此时连接可能已经过去，
      // 所以无论成败都回读一次，让界面显示真实的当前状态而不是停留在报错上
      const res = await serverApi.switch(idx)
      setCurrentServerIndex(res.index)
      if (res.warning) toast.warning(res.warning)
      clearSelection()
      torrentApi.list().then(setTorrents).catch(() => {})
      sessionApi.get().then(setSession).catch(() => setSession(null))
      sessionApi.status().then(setStatus).catch(() => setStatus(null))
      torrentApi.sites().then(setTorrentSites).catch(() => {})
      await loadTargetSession(idx)
    } catch {
      // 拦截器已提示；再读一次，失败原因会重新落到 targetError 上
      await loadTargetSession(idx)
    } finally {
      setSwitching(false)
    }
  }

  const connectionPane = (
    <div className="space-y-3">
      <Row label={t('session.downloaderType')} hint={t('session.typeHint')}>
        <SmallSelect
          value={kind}
          onValueChange={(v) => {
            const next = v as DownloaderKind
            // 地址框是空的、或还留着"另一类的默认值"时，跟着换成新类型的默认地址；
            // 用户自己填过的地址一律不动（不能把真实配置覆盖掉）
            if (!url.trim() || isOtherDefault(url, next)) setUrl(defaultUrlFor(next))
            setKind(next)
          }}
          options={DOWNLOADER_OPTIONS}

        />
      </Row>
      <div className="space-y-1">
        <span className="text-body text-gray-600 dark:text-gray-300">{t('session.transmissionUrl')}</span>
        <Input
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          placeholder={defaultUrlFor(kind)}
          className="h-8 text-footnote"
        />
      </div>
      <div className="space-y-1">
        <span className="text-body text-gray-600 dark:text-gray-300">{t('session.username')}</span>
        <Input value={user} onChange={(e) => setUser(e.target.value)} autoComplete="off" className="h-8 text-footnote" />
      </div>
      <div className="space-y-1">
        <span className="text-body text-gray-600 dark:text-gray-300">{t('session.password')}</span>
        <Input
          type="password"
          value={pass}
          onChange={(e) => setPass(e.target.value)}
          autoComplete="new-password"
          placeholder="••••••"
          className="h-8 text-footnote"
        />
      </div>
      <Row label={t('session.pollInterval')} hint={t('session.pollIntervalHint')}>
        <SmallSelect
          value={pollInterval}
          onValueChange={setPollInterval}
          options={[
            { value: '1s', label: '1s' },
            { value: '2s', label: '2s' },
            { value: '5s', label: '5s' },
            { value: '10s', label: '10s' },
          ]}
        />
      </Row>
      {/* 端口检测由后端活动连接执行，只在选中的就是活动服务器时才给按钮，
          否则按钮测的是另一台的端口 */}
      {targetActive && caps?.portTest !== false && (
      <Row label={t('session.portTest')} hint={t('session.portTestHint')}>
        <div className="flex items-center gap-2">
          {portOpen !== null && (
            portOpen
              ? <Badge className="bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300">{t('session.portOpen')}</Badge>
              : <Badge className="bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300">{t('session.portClosed')}</Badge>
          )}
          <Button size="sm" className="h-8 text-footnote" disabled={testingPort} onClick={testPort}>
            {testingPort ? t('common.loading') : t('session.portTest')}
          </Button>
        </div>
      </Row>
      )}
      {/* 保存只作用于本栏：放在栏内而不是全局页脚，避免被当成「整个设置的提交/取消」 */}
      <div className="flex items-center justify-end gap-2 pt-2">
        {connDirty && <span className="text-caption1 text-amber-600 dark:text-amber-400">{t('session.unsavedHint')}</span>}
        <Button size="sm" className="h-8 text-footnote" disabled={saving || !url.trim()} onClick={() => void save()}>
          {saving ? t('common.loading') : t('session.saveConfig')}
        </Button>
      </div>
    </div>
  )

  const uiPane = (
    <div className="space-y-1">
      <Row label={t('session.fontSize')} hint={t('session.fontSizeHint')}>
        <SmallSelect
          value={String(fontSize)}
          onValueChange={(v) => setFontSize(Number(v))}
          options={[12, 13, 14, 15, 16, 17, 18, 19, 20].map((n) => ({ value: String(n), label: `${n}px` }))}
        />
      </Row>
      <Row label={t('session.singleLine')} hint={t('session.singleLineHint')}>
        <Switch checked={singleLine} onCheckedChange={setSingleLine} />
      </Row>
      <Row label={t('session.showCheckboxes')} hint={t('session.showCheckboxesHint')}>
        <Switch checked={showCheckboxes} onCheckedChange={setShowCheckboxes} />
      </Row>
      <Row label={t('session.clearDirHistory')}>
        <Button size="sm" variant="outline" className="h-8 text-footnote" onClick={() => { localStorage.removeItem('tm_dirs'); toast.success(t('toast.updated')) }}>
          {t('session.clear')}
        </Button>
      </Row>
    </div>
  )

  // 无障碍与界面拆成两节：它们此前挤在同一个滚动区里，桌面端展开后是整页最长的一块
  const a11yPane = (
    <div className="space-y-1">
      <p className="text-caption1 text-gray-400 pb-1">{t('session.a11yHint')}</p>
      <Row label={t('session.reduceGlass')} hint={t('session.reduceGlassHint')}>
        <Switch checked={reduceGlass} onCheckedChange={setReduceGlass} />
      </Row>
      <Row label={t('session.reduceMotion')} hint={t('session.reduceMotionHint')}>
        <Switch checked={reduceMotion} onCheckedChange={setReduceMotion} />
      </Row>
      <Row label={t('session.moreContrast')} hint={t('session.moreContrastHint')}>
        <Switch checked={moreContrast} onCheckedChange={setMoreContrast} />
      </Row>
      <Row label={t('session.glassOpacity')} hint={t('session.glassOpacityHint')}>
        <div className="flex items-center gap-2 shrink-0">
          <input
            type="range"
            min={20}
            max={100}
            step={5}
            value={glassOpacity}
            disabled={reduceGlass}
            onChange={(e) => setGlassOpacity(Number(e.target.value))}
            className="w-36 h-2 appearance-none disabled:opacity-40 cursor-pointer"
            aria-label={t('session.glassOpacity')}
          />
          <span className="text-footnote text-gray-400 tm-mono w-10 text-right">{glassOpacity}%</span>
        </div>
      </Row>
      <Row label={t('session.wallpaper')} hint={t('session.wallpaperHint')}>
        <div className="flex items-center gap-2">
          {wallpaper && (
            <img
              src={wallpaper}
              alt=""
              className="h-8 w-12 rounded-md object-cover border border-gray-200 dark:border-gray-700 shrink-0"
            />
          )}
          <input
            ref={wallpaperInputRef}
            type="file"
            accept="image/*"
            className="hidden"
            onChange={(e) => {
              void pickWallpaper(e.target.files?.[0])
              // 允许选同一张图重试（失败后再次选择需触发 change）
              e.target.value = ''
            }}
          />
          <Button
            size="sm"
            variant="outline"
            className="h-8 text-footnote shrink-0"
            onClick={() => wallpaperInputRef.current?.click()}
          >
            {t('session.wallpaperChoose')}
          </Button>
          {wallpaper && (
            <Button
              size="sm"
              variant="outline"
              className="h-8 text-footnote shrink-0"
              onClick={() => setWallpaper('')}
            >
              {t('session.wallpaperClear')}
            </Button>
          )}
        </div>
      </Row>
    </div>
  )

  const sessionItems = session
    ? [
        {
          key: 'download',
          title: t('session.download'),
          children: (
            <div className="space-y-1">
              <Row label={t('session.downloadDir')}>
                <DirInput field="downloadDir" session={session} onCommit={patchSession} />
              </Row>
              <Row label={t('session.startAdded')} hint={t('session.startAddedHint')}>
                <Switch checked={session.startAdded} onCheckedChange={(v) => patchSession({ startAdded: v })} />
              </Row>
              {caps?.incompleteDir !== false && (
                <>
                  <Row label={t('session.incompleteDirEnabled')} hint={t('session.incompleteDirEnabledHint')}>
                    <Switch checked={session.incompleteDirEnabled} onCheckedChange={(v) => patchSession({ incompleteDirEnabled: v })} />
                  </Row>
                  <Row label={t('session.incompleteDir')}>
                    <DirInput field="incompleteDir" session={session} onCommit={patchSession} />
                  </Row>
                </>
              )}
              {caps?.fileHandling !== false && (
                <>
                  <Row label={t('session.renamePartialFiles')} hint={t('session.renamePartialFilesHint')}>
                    <Switch checked={session.renamePartialFiles} onCheckedChange={(v) => patchSession({ renamePartialFiles: v })} />
                  </Row>
                  <Row label={t('session.trashOriginalTorrentFiles')} hint={t('session.trashOriginalTorrentFilesHint')}>
                    <Switch checked={session.trashOriginalTorrentFiles} onCheckedChange={(v) => patchSession({ trashOriginalTorrentFiles: v })} />
                  </Row>
                </>
              )}
              <Row label={t('session.cacheSizeMB')} hint={t('session.cacheSizeMBHint')}>
                <NumInput value={session.cacheSizeMB} min={0} onChange={(v) => v != null && patchSession({ cacheSizeMB: v })} />
              </Row>
            </div>
          ),
        },
        {
          key: 'seeding',
          title: t('session.seeding'),
          children: (
            <div className="space-y-1">
              {caps?.globalSeedRatio !== false && (
                <Row label={t('session.seedRatioLimit')} hint={t('session.seedRatioLimitHint')}>
                  <NumInput value={session.seedRatioLimit} min={0} step={0.5} onChange={(v) => v != null && patchSession({ seedRatioLimit: v })} />
                </Row>
              )}
              <Row label={t('session.idleSeedingLimit')} hint={t('session.idleSeedingLimitHint')}>
                <div className="flex items-center gap-2">
                  <Switch checked={session.idleSeedingLimitEnabled} onCheckedChange={(v) => patchSession({ idleSeedingLimitEnabled: v })} />
                  <NumInput value={session.idleSeedingLimit} min={0} disabled={!session.idleSeedingLimitEnabled} onChange={(v) => v != null && patchSession({ idleSeedingLimit: v })} />
                </div>
              </Row>
            </div>
          ),
        },
        {
          key: 'queue',
          title: t('session.queue'),
          children: (
            <div className="space-y-1">
              <Row label={t('session.downloadQueueEnabled')} hint={t('session.downloadQueueEnabledHint')}>
                <Switch checked={session.downloadQueueEnabled} onCheckedChange={(v) => patchSession({ downloadQueueEnabled: v })} />
              </Row>
              <Row label={t('session.downloadQueueSize')} hint={t('session.downloadQueueSizeHint')}>
                <NumInput value={session.downloadQueueSize} min={0} disabled={!session.downloadQueueEnabled} onChange={(v) => v != null && patchSession({ downloadQueueSize: v })} />
              </Row>
              <Row label={t('session.seedQueueEnabled')} hint={t('session.seedQueueEnabledHint')}>
                <Switch checked={session.seedQueueEnabled} onCheckedChange={(v) => patchSession({ seedQueueEnabled: v })} />
              </Row>
              <Row label={t('session.seedQueueSize')} hint={t('session.seedQueueSizeHint')}>
                <NumInput value={session.seedQueueSize} min={0} disabled={!session.seedQueueEnabled} onChange={(v) => v != null && patchSession({ seedQueueSize: v })} />
              </Row>
              {caps?.queueStalled !== false && (
                <>
                  <Row label={t('session.queueStalledEnabled')} hint={t('session.queueStalledEnabledHint')}>
                    <Switch checked={session.queueStalledEnabled} onCheckedChange={(v) => patchSession({ queueStalledEnabled: v })} />
                  </Row>
                  <Row label={t('session.queueStalledMinutes')} hint={t('session.queueStalledMinutesHint')}>
                    <NumInput value={session.queueStalledMinutes} min={0} onChange={(v) => v != null && patchSession({ queueStalledMinutes: v })} />
                  </Row>
                </>
              )}
            </div>
          ),
        },
        {
          key: 'bandwidth',
          title: t('session.bandwidth'),
          children: (
            <div className="space-y-1">
              <Row label={t('session.speedLimitDown')} hint={t('session.speedLimitDownHint')}>
                <div className="flex items-center gap-2">
                  <Switch checked={session.speedLimitDownOn} onCheckedChange={(v) => patchSession({ speedLimitDownOn: v })} />
                  <NumInput value={session.speedLimitDown} min={0} disabled={!session.speedLimitDownOn} onChange={(v) => v != null && patchSession({ speedLimitDown: v })} />
                </div>
              </Row>
              <Row label={t('session.speedLimitUp')} hint={t('session.speedLimitUpHint')}>
                <div className="flex items-center gap-2">
                  <Switch checked={session.speedLimitUpOn} onCheckedChange={(v) => patchSession({ speedLimitUpOn: v })} />
                  <NumInput value={session.speedLimitUp} min={0} disabled={!session.speedLimitUpOn} onChange={(v) => v != null && patchSession({ speedLimitUp: v })} />
                </div>
              </Row>
              <div className="text-caption1 text-gray-400 pt-1">{t('session.altSpeed')}</div>
              <Row label={t('session.altSpeedDown')} hint={t('session.altSpeedDownHint')}>
                <NumInput value={session.altSpeedDown} min={0} onChange={(v) => v != null && patchSession({ altSpeedDown: v })} />
              </Row>
              <Row label={t('session.altSpeedUp')} hint={t('session.altSpeedUpHint')}>
                <NumInput value={session.altSpeedUp} min={0} onChange={(v) => v != null && patchSession({ altSpeedUp: v })} />
              </Row>
              <Row label={t('session.altSpeedTime')} hint={t('session.altSpeedTimeHint')}>
                <Switch checked={session.altSpeedTimeEnabled} onCheckedChange={(v) => patchSession({ altSpeedTimeEnabled: v })} />
              </Row>
              {/* 周几多选（0=每天，其余为位掩码） */}
              <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1.5 py-1.5">
                <div className="min-w-[10rem] max-w-full">
                  <span className="text-body text-gray-600 dark:text-gray-300">{t('session.scheduleDays')}</span>
                  <p className="text-caption1 text-gray-400 mt-0.5">{t('session.scheduleDaysHint')}</p>
                </div>
                <div className="flex gap-1 shrink-0">
                  {DAY_BITS.map((d) => {
                    const cur = session.altSpeedTimeDay
                    // 0（每天）在 UI 上等价于七天全选
                    const active = (cur === 0 ? DAY_ALL : cur) & d.bit ? true : false
                    // div 代替 button：老 WebView 里 button 上的 flex 居中/定高不可靠
                    return (
                      <div
                        key={d.bit}
                        role="button"
                        tabIndex={0}
                        title={t(`session.day${d.label}`)}
                        onClick={() => {
                          // 在全选（每天）状态下点击某天 = 取消那一天，而非"只选那一天"
                          const effective = cur === 0 ? DAY_ALL : cur
                          const next = effective ^ d.bit
                          if (next === 0) return // 至少保留一天
                          // 七天重新全选时写回 0（每天）
                          patchSession({ altSpeedTimeDay: next === DAY_ALL ? 0 : next })
                        }}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); e.currentTarget.click() }
                        }}
                        className={cn(
                          'flex h-8 w-8 shrink-0 cursor-pointer select-none items-center justify-center text-caption1 rounded-md border transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring',
                          active
                            ? 'bg-primary text-primary-foreground border-primary'
                            : 'border-gray-200 dark:border-gray-700 text-gray-500 hover:border-gray-300',
                        )}
                      >
                        {t(`session.dayShort${d.label}`)}
                      </div>
                    )
                  })}
                </div>
              </div>
              <Row label={t('session.scheduleTime')} hint={t('session.scheduleTimeHint')}>
                {/* 手机上整块占一行（w-full）让两个下拉平分宽度；桌面定宽 13rem，
                    否则 flex-1 的下拉在自适应容器里会被压到内容最小宽度 */}
                <div className="flex w-full items-center gap-1.5 md:w-52">
                  <SmallSelect
                    value={timeToStr(session.altSpeedTimeBegin)}
                    onValueChange={(v) => patchSession({ altSpeedTimeBegin: strToMin(v) })}
                    options={TIME_OPTIONS}
                    className="flex-1"
                  />
                  <span className="text-gray-400">-</span>
                  <SmallSelect
                    value={timeToStr(session.altSpeedTimeEnd)}
                    onValueChange={(v) => patchSession({ altSpeedTimeEnd: strToMin(v) })}
                    options={TIME_OPTIONS}
                    className="flex-1"
                  />
                </div>
              </Row>
            </div>
          ),
        },
        {
          key: 'network',
          title: t('session.network'),
          children: (
            <div className="space-y-1">
              <Row label={t('session.peerPort')} hint={t('session.peerPortHint')}>
                <NumInput value={session.peerPort} min={1} max={65535} disabled={session.peerPortRandomOnStart} onChange={(v) => v != null && patchSession({ peerPort: v })} />
              </Row>
              <Row label={t('session.peerPortRandomOnStart')} hint={t('session.peerPortRandomOnStartHint')}>
                <Switch checked={session.peerPortRandomOnStart} onCheckedChange={(v) => patchSession({ peerPortRandomOnStart: v })} />
              </Row>
              <Row label={t('session.upnp')}>
                <Switch checked={session.portForwardingEnabled} onCheckedChange={(v) => patchSession({ portForwardingEnabled: v })} />
              </Row>
              <p className="text-footnote text-gray-400 -mt-0.5 mb-1">{t('session.upnpHint')}</p>
              <Row label={t('session.encryption')} hint={t('session.encryptionHint')}>
                <SmallSelect
                  value={session.encryption || 'preferred'}
                  onValueChange={(v) => patchSession({ encryption: v })}
                  options={[
                    { value: 'required', label: t('session.encryptionRequired') },
                    { value: 'preferred', label: t('session.encryptionPreferred') },
                    { value: 'tolerated', label: t('session.encryptionTolerated') },
                  ]}
                />
              </Row>
              {caps?.peerLimit !== false && (
                <Row label={t('session.peerLimitGlobal')} hint={t('session.peerLimitGlobalHint')}>
                  <NumInput value={session.peerLimitGlobal} min={0} onChange={(v) => v != null && patchSession({ peerLimitGlobal: v })} />
                </Row>
              )}
              <div className="py-1">
                <div className="flex items-center justify-between">
                  <span className="text-body text-gray-600 dark:text-gray-300">{t('session.pexEnabled')}</span>
                  <Switch checked={session.pexEnabled} onCheckedChange={(v) => patchSession({ pexEnabled: v })} />
                </div>
                <p className="text-footnote text-gray-400 mt-0.5">{t('session.pexHint')}</p>
              </div>
              <div className="py-1">
                <div className="flex items-center justify-between">
                  <span className="text-body text-gray-600 dark:text-gray-300">{t('session.dhtEnabled')}</span>
                  <Switch checked={session.dhtEnabled} onCheckedChange={(v) => patchSession({ dhtEnabled: v })} />
                </div>
                <p className="text-footnote text-gray-400 mt-0.5">{t('session.dhtHint')}</p>
              </div>
              {caps?.utpToggle !== false && (
                <div className="py-1">
                  <div className="flex items-center justify-between">
                    <span className="text-body text-gray-600 dark:text-gray-300">{t('session.utpEnabled')}</span>
                    <Switch checked={session.utpEnabled} onCheckedChange={(v) => patchSession({ utpEnabled: v })} />
                  </div>
                  <p className="text-footnote text-gray-400 mt-0.5">{t('session.utpHint')}</p>
                </div>
              )}
              <div className="py-1">
                <div className="flex items-center justify-between">
                  <span className="text-body text-gray-600 dark:text-gray-300">{t('session.lpdEnabled')}</span>
                  <Switch checked={session.lpdEnabled} onCheckedChange={(v) => patchSession({ lpdEnabled: v })} />
                </div>
                <p className="text-footnote text-gray-400 mt-0.5">{t('session.lpdHint')}</p>
              </div>
            </div>
          ),
        },
        {
          key: 'other',
          title: t('session.other'),
          children: (
            <div className="space-y-1">
              {caps?.blocklist !== false && (
                <>
                  <Row label={t('session.blocklistEnabled')} hint={t('session.blocklistEnabledHint')}>
                    <Switch checked={blocklistEnabled} onCheckedChange={(v) => { setBlocklistEnabled(v); void patchSession({ blocklistEnabled: v }) }} />
                  </Row>
                  <Row label={t('session.blocklistSize')}>
                    <span className="text-body text-gray-500">{session.blocklistSize.toLocaleString()}</span>
                  </Row>
                  <div className="flex items-center gap-2 py-1.5">
                    <Input value={blocklistUrl} onChange={(e) => setBlocklistUrl(e.target.value)} placeholder={t('session.blocklistUrl')} className="h-8 text-footnote flex-1" />
                    {/* 立即更新由后端活动连接执行：选中的不是活动服务器时只允许改地址 */}
                    <Button size="sm" className="h-8 text-footnote shrink-0" disabled={blocklistUpdating || !targetActive} onClick={updateBlocklist}>
                      {blocklistUpdating ? t('common.loading') : t('session.blocklistUpdate')}
                    </Button>
                  </div>
                </>
              )}
              {caps?.scriptHooks !== false && (
                <>
                  <div className="pt-2 mt-1 border-t border-gray-100 dark:border-gray-700 space-y-1">
                    <Row label={t('session.scriptAdded')} hint={t('session.scriptAddedHint')}>
                      <Switch checked={session.scriptTorrentAddedEnabled} onCheckedChange={(v) => patchSession({ scriptTorrentAddedEnabled: v })} />
                    </Row>
                    <Input defaultValue={session.scriptTorrentAddedFilename} placeholder={t('session.scriptHint')} className="h-8 text-footnote" onBlur={(e) => patchSession({ scriptTorrentAddedFilename: e.target.value })} />
                  </div>
                  <div className="space-y-1">
                    <Row label={t('session.scriptDone')} hint={t('session.scriptDoneHint')}>
                      <Switch checked={session.scriptTorrentDoneEnabled} onCheckedChange={(v) => patchSession({ scriptTorrentDoneEnabled: v })} />
                    </Row>
                    <Input defaultValue={session.scriptTorrentDoneFilename} placeholder={t('session.scriptHint')} className="h-8 text-footnote" onBlur={(e) => patchSession({ scriptTorrentDoneFilename: e.target.value })} />
                  </div>
                  <div className="space-y-1">
                    <Row label={t('session.scriptDoneSeeding')} hint={t('session.scriptDoneSeedingHint')}>
                      <Switch checked={session.scriptTorrentDoneSeedingEnabled} onCheckedChange={(v) => patchSession({ scriptTorrentDoneSeedingEnabled: v })} />
                    </Row>
                    <Input defaultValue={session.scriptTorrentDoneSeedingFilename} placeholder={t('session.scriptHint')} className="h-8 text-footnote" onBlur={(e) => patchSession({ scriptTorrentDoneSeedingFilename: e.target.value })} />
                  </div>
                </>
              )}
              <div className="space-y-1 pt-1">
                <span className="text-body text-gray-600 dark:text-gray-300">{t('session.defaultTrackers')}</span>
                <textarea
                  rows={3}
                  autoCapitalize="off"
                  autoCorrect="off"
                  spellCheck={false}
                  defaultValue={session.defaultTrackers?.join('\n')}
                  placeholder={t('common.eachLineOne')}
                  className="w-full max-h-32 appearance-none resize-y rounded-md border border-input bg-transparent px-3 py-2 text-body shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                  onBlur={(e) => patchSession({ defaultTrackers: e.target.value.split('\n').map((s) => s.trim()).filter(Boolean) })}
                />
              </div>
            </div>
          ),
        },
      ]
    : []

  // ---- 节清单：桌面端左侧导航、移动端长滚动，用的是同一份数据 ----
  // 导航项与移动端分节一一对应：加一节只需在这里加一条，
  // 不至于出现"导航里有、页面上没有"或反之。
  //
  // 外框只在移动端加：桌面端左栏已经把节名写在导航上了，右栏再套一层同名卡片
  // 就是「点进去又套了一层」——多一次点击（summary 可折叠）、多一条边框、
  // 标题还重复两遍。这里用 chrome() 把外壳按断点收掉，两种布局共用同一份内容。
  // ---- 下载器设置面板：**按数据特征决定，而不是按下载器类型** ----
  //
  // 判据是「这个驱动有没有给字段自述（schema）」，不是「它是不是 qBittorrent」。
  // 用 kind 字符串比对的话，每接入一个下载器都要回来加一个分支，
  // 而自述机制本来就是为「界面不认识具体下载器」设计的。
  //
  // 两条路：
  //   schemaSections 非空 → 按自述通用渲染（qBittorrent 上百个偏好键走这条）
  //   否则               → 回退内置通用会话表单（Transmission 走这条）
  // 新驱动只要在后端声明 SettingsSchema，这里自动就走通，前端不用改。
  const schemaSections = session?.schema?.length ? session.schema : []
  // 是否有「未保存改动」这回事：自述面板走草稿 + 吸顶保存条，
  // 内置表单是即时保存的（没有改动条）。同样按数据判断而非按类型。
  const schemaDriven = schemaSections.length > 0
  // 移动端：套折叠分节；桌面端：裸内容（导航即标题）
  const chrome = (id: string, title: string, body: ReactNode) =>
    isMobile
      ? <Section id={id} title={title}>{body}</Section>
      : <div className="space-y-1">{body}</div>

  const panes: PaneDef[] = [
    {
      id: 'connection',
      label: t('session.connection'),
      icon: <Plug />,
      node: chrome('connection', t('session.connection'), targetActive ? connectionPane : (
        <p className="text-footnote text-gray-500">{t('session.targetConnHint')}</p>
      )),
    },
    { id: 'ui', label: t('session.ui'), icon: <Palette />, node: chrome('ui', t('session.ui'), uiPane) },
    { id: 'a11y', label: t('session.a11yTitle'), icon: <Accessibility />, node: chrome('a11y', t('session.a11yTitle'), a11yPane) },
    // 下载器设置：连接的是哪个就显示哪个的自述面板。
    // qBittorrent 有上百个偏好键，由驱动自述分节后通用渲染；
    // Transmission 沿用内置表单（没连 qB 时行为与改造前一致）。
    // target 在服务器列表读回前是 undefined——会话往往先到，
    // 这里必须按「还在加载」处理，否则会去读 undefined.kind
    ...(!session || !target ? [{
      id: 'downloader',
      label: t('session.downloaderSettings'),
      icon: <SlidersHorizontal />,
      node: chrome('downloader', t('session.downloaderSettings'),
        /* 失败原因原样显示（含服务端给的中文原因），连不上时给一句能落地的提示 */
        targetError ? (
          <div className="space-y-2">
            <p className="text-footnote text-gray-500">{targetError}</p>
            {targetConnMismatch && (
              <Button
                size="sm"
                variant="outline"
                className="h-8 text-footnote"
                disabled={switching}
                onClick={() => void reconnectTarget()}
              >
                {switching ? t('common.loading') : t('session.reconnectServer')}
              </Button>
            )}
          </div>
        ) : (
          <div className="px-1 text-footnote text-gray-400">{t('common.loading')}</div>
        )),
    }] : schemaDriven ? (
      // 驱动自述的每一节各做一个导航项：qBittorrent 上百个偏好键分十节，
      // 全塞进一个导航项等于没分。移动端没有导航，回到单列长滚动。
      //
      // 这段不认识任何具体下载器：节的 id、标题、图标全部来自 schema。
      schemaSections.map((sec) => {
        const label = lang === 'en' && sec.labelEn ? sec.labelEn : sec.label
        return {
          id: `dl-${sec.key}`,
          label,
          // 图标由驱动的语义名决定（见 downloaders.tsx 的 sectionIcon）
          icon: sectionIcon(sec.icon),
          node: chrome(
            `dl-${sec.key}`,
            label,
            <QBFields sec={sec} lang={lang} values={session.prefs ?? {}} draft={qb.draft} setDraft={qb.setDraft} />,
          ),
        }
      })
    ) : sessionItems.map((item) => ({
      id: item.key,
      label: item.title,
      icon: <SlidersHorizontal />,
      node: chrome(item.key, item.title, item.children),
    }))),
    {
      id: 'multiServer',
      label: t('session.multiServer.title'),
      icon: <Server />,
      node: chrome('multiServer', t('session.multiServer.title'),
        <><div className="text-footnote text-gray-500 mb-3">{t('session.multiServer.hint')}</div>
        {servers.map((server, idx) => (
          <div key={idx} className="rounded-lg glass-section p-2 mb-2 space-y-1.5">
            <div className="flex items-center gap-2">
              <Input
                value={server.name}
                onChange={(e) => { const s = [...servers]; s[idx] = { ...s[idx], name: e.target.value }; void saveServers(s) }}
                className="flex-1 h-8 text-footnote"
                placeholder={t('session.multiServer.serverName')}
              />
              <Switch checked={server.enabled} onCheckedChange={(v) => { const s = [...servers]; s[idx] = { ...s[idx], enabled: v }; void saveServers(s) }} />
              <Button size="sm" variant="destructive" className="h-8 text-footnote shrink-0" onClick={() => void removeServer(idx)}>
                {t('common.delete')}
              </Button>
            </div>
            <div className="flex items-center gap-2">
              <SmallSelect
                className="w-[130px] shrink-0"
                value={server.type ?? 'transmission'}
                onValueChange={(v) => {
                  const next = v as DownloaderKind
                  const s = [...servers]
                  // 同连接栏：空地址或另一类的默认地址就跟换成新类型的默认值
                  s[idx] = {
                    ...s[idx],
                    type: next,
                    url: (!s[idx].url.trim() || isOtherDefault(s[idx].url, next)) ? defaultUrlFor(next) : s[idx].url,
                  }
                  saveServers(s)
                }}
                options={DOWNLOADER_OPTIONS}
              />
              <Input
                value={server.url}
                onChange={(e) => { const s = [...servers]; s[idx] = { ...s[idx], url: e.target.value }; saveServers(s) }}
                className="w-full h-8 text-footnote"
                placeholder={defaultUrlFor(server.type)}
              />
            </div>
            <div className="flex items-center gap-2">
              <Input
                value={server.user}
                onChange={(e) => { const s = [...servers]; s[idx] = { ...s[idx], user: e.target.value }; void saveServers(s) }}
                className="flex-1 h-8 text-footnote"
                placeholder={t('session.username')}
              />
              <Input
                type="password"
                value={server.pass ?? ''}
                onChange={(e) => {
                  // pass 为 undefined 表示「未改动，沿用原密码」；一旦输入即为显式设置
                  const v = e.target.value
                  const s = [...servers]
                  s[idx] = { ...s[idx], pass: v, hasPass: v !== '' }
                  saveServers(s)
                }}
                className="flex-1 h-8 text-footnote"
                // 列表接口不返回密码，留空表示保持原密码不变
                placeholder={server.hasPass ? `${t('session.password')} · ${t('session.passwordSaved')}` : t('session.password')}
                autoComplete="new-password"
              />
            </div>
          </div>
        ))}
        <Button size="sm" variant="outline" className="w-full h-8 text-footnote" onClick={() => saveServers([...servers, { name: '', type: 'transmission', url: defaultUrlFor('transmission'), user: '', pass: '', hasPass: false, enabled: true }])}>
          + {t('session.multiServer.addServer')}
        </Button>
        {servers.length > 1 && (
          <div className="mt-3 pt-3 border-t border-gray-100 dark:border-gray-700">
            <div className="text-footnote font-medium mb-2">{t('session.multiServer.activeServer')}</div>
            <div className="space-y-1">
              {servers.map((server, idx) => (
                <Button
                  key={idx}
                  size="sm"
                  variant={idx === currentServerIndex ? 'default' : 'outline'}
                  className="w-full h-8 text-footnote truncate"
                  onClick={() => void switchToServer(idx)}
                  disabled={!server.enabled}
                >
                  {server.name || `${t('session.multiServer.server')} ${idx + 1}`} - {server.url}
                </Button>
              ))}
            </div>
          </div>
        )}
        </>),
    },
    {
      id: 'automation',
      label: t('session.automation'),
      icon: <Workflow />,
      node: chrome('automation', t('session.automation'),
        <div className="space-y-2">
          <Button size="sm" variant="outline" className="w-full h-8 text-footnote" onClick={() => setOpenMove(true)}>
            {t('autoMove.title')}
          </Button>
          <Button size="sm" variant="outline" className="w-full h-8 text-footnote" onClick={() => setOpenPolicy(true)}>
            {t('seedPolicy.title')}
          </Button>
          <Button size="sm" variant="outline" className="w-full h-8 text-footnote" onClick={() => useAppStore.getState().openSpeedPolicy()}>
            {t('speedPolicy.title')}
          </Button>
          <Button size="sm" variant="outline" className="w-full h-8 text-footnote" onClick={() => setOpenMcp(true)}>
            {t('session.mcp.title')}
          </Button>
        </div>),
    },
    {
      id: 'about',
      label: t('session.about'),
      icon: <Info />,
      // AboutSection 自身带外壳，这里不能再套一层（否则又是"点进去多一层"）
      node: <AboutSection transmissionVersion={status?.version} framed={isMobile} />,
    },
  ]

  // 选中的节已不在清单里（如切换服务器后 qB 的分节换了一批 key）时回落到第一节，
  // 否则右侧会是一片空白。这里只做渲染期兜底，不改 pane 状态——
  // 状态改了反而会在用户切回旧服务器时丢掉原来的位置。
  const current = panes.find((p) => p.id === pane) ?? panes[0]
  const visiblePanes = isMobile ? panes : current ? [current] : []

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      {/* 宽度断点与 useResponsive 的 isMobile 同口径（md/768）：
          窄屏是满屏单列，宽屏才撑到 960 给左栏导航留出位置 */}
      <DialogContent className="md:max-w-[960px] h-[85dvh] md:h-[78dvh] flex flex-col p-0 gap-0">
        <DialogHeader className="px-4 pt-4 pb-2 border-b border-gray-200/40 dark:border-gray-700/30">
          <DialogTitle className="pr-10">{t('session.title')}</DialogTitle>
        </DialogHeader>

        {/* 弹窗头下方的固定区（连接状态 / 服务器标签）：与右侧表单分开滚动，
            滚表单时不会把「我正在配置哪台」滚出视野 */}
        <div className="px-4 pt-2 pb-2 space-y-1.5 border-b border-gray-200/40 dark:border-gray-700/30 shrink-0">
          {/* 连接状态 */}
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-body shrink-0">{t('session.connectionStatus')}:</span>
            {status ? (
              status.connected ? (
                // nowrap：版本串较长时不允许在胶囊内折行，放不下就让整个徽章换到下一行
                <Badge className="min-w-0 max-w-full whitespace-nowrap bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300">
                  {t('common.connected')}{status.version ? ` · ${status.version}` : ''}{status.type ? ` · ${downloaderMeta(status.type).label}` : ''}
                </Badge>
              ) : (
                <Badge className="bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300">{t('common.disconnected')}</Badge>
              )
            ) : (
              <Badge variant="outline">{t('common.connecting')}</Badge>
            )}
          </div>

          {/* 设置目标标签：同时连接多台时一台一个标签，各读各写各的设置 */}
          {targets.length > 1 && target && (
            <div className="space-y-1">
              <div className="flex flex-wrap items-center gap-1.5" role="tablist" aria-label={t('session.targetTitle')}>
                {targets.map((x) => (
                  <button
                    key={x.key}
                    type="button"
                    role="tab"
                    aria-selected={x.key === target.key}
                    onClick={() => setTargetKey(x.key)}
                    className={cn(
                      'inline-flex h-8 items-center gap-1.5 rounded-md border px-2.5 text-footnote transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring',
                      x.key === target.key
                        ? 'border-primary bg-primary text-primary-foreground'
                        : 'border-gray-200 text-gray-600 hover:border-gray-300 dark:border-gray-700 dark:text-gray-300',
                    )}
                  >
                    <span className="max-w-[10rem] truncate">{x.label}</span>
                    <span className="text-caption1 opacity-70">{downloaderMeta(x.kind).short}</span>
                  </button>
                ))}
              </div>
              {!targetActive && (
                <p className="text-caption1 text-amber-600 dark:text-amber-400">{t('session.targetNotActive')}</p>
              )}
            </div>
          )}
        </div>

        {/* 双列：左侧节导航常驻，右侧只渲染当前节的表单。
            此前是单列长滚动，某一节展开后整页拉到十几屏，找下一节只能一路滚。 */}
        <div className="flex-1 min-h-0 flex gap-3 px-4 py-3">
          <PaneNav
            items={panes.map((p) => ({ id: p.id, label: p.label, icon: p.icon }))}
            active={visiblePanes[0]?.id ?? ''}
            onSelect={setPane}
            label={t('session.title')}
          />
          {/* 窄屏长滚动要的是浏览器原生惯性；桌面单节用 ScrollArea 把超长列表限制在弹窗内 */}
          {isMobile ? (
            <div className="flex-1 min-h-0 overflow-y-auto space-y-3 pr-1">
              {/* schema 驱动面板的未保存改动条：移动端长滚动，吸顶才一直可见 */}
              {schemaDriven && (
                <QBSaveBar draft={qb.draft} saving={qb.saving} setDraft={qb.setDraft} submit={qb.submit} />
              )}
              {visiblePanes.map((p) => <div key={p.id}>{p.node}</div>)}
            </div>
          ) : (
            <ScrollArea className="flex-1 min-w-0 pr-1 space-y-3">
              {schemaDriven && (
                <QBSaveBar draft={qb.draft} saving={qb.saving} setDraft={qb.setDraft} submit={qb.submit} />
              )}
              {visiblePanes.map((p) => <div key={p.id}>{p.node}</div>)}
            </ScrollArea>
          )}
        </div>
      </DialogContent>
      <AutoMoveManager open={openMove} onClose={() => setOpenMove(false)} />
      <SeedPolicyManager open={openPolicy} onClose={() => setOpenPolicy(false)} />
      <McpManager open={openMcp} onClose={() => setOpenMcp(false)} />
    </Dialog>
  )
}

const clientGetSettings = () => client.get('/settings')
const clientPutSettings = (body: {
  type?: DownloaderKind
  url?: string
  user?: string
  pass?: string
  pollInterval?: string
  mcpEnabled?: boolean
  mcpAllowDelete?: boolean
}) => client.put('/settings', body)
