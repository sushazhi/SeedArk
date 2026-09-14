import React, { useEffect, useMemo, useRef, useState } from 'react'
import { ChevronDown, ChevronRight, ChevronUp, ChevronsUpDown, File, Folder, FolderOpen, Pencil, Search } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { torrentApi } from '@/api/torrent'
import { useRevealPath } from '@/hooks/useRevealPath'
import { useSemanticPath } from '@/hooks/useSemanticPath'
import { useTorrentActions } from '@/hooks/useTorrentActions'
import { usePlatform } from '@/platform'
import { useAppStore } from '@/stores/appStore'
import type { Torrent } from '@/types'
import { formatBytes, formatDate, formatDuration, formatPercent, formatSpeed } from '@/utils/format'
import { translateError } from '@/utils/errorText'
import { toast } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { EditModals } from '@/components/TorrentMenu'
import type { EditTarget } from '@/components/TorrentMenu'
import { SpeedHistory } from '@/components/StatusBar/SpeedHistory'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

// 文件树节点（目录节点 _i = -1）
type FileNode = {
  key: string
  name: string
  path: string
  _i: number
  length: number
  bytesCompleted: number
  wanted: boolean
  priority: number
  children?: FileNode[]
}

// 收集节点下所有叶子文件的索引
function collectLeaves(n: FileNode): number[] {
  return n.children ? n.children.flatMap(collectLeaves) : [n._i]
}

// ISO 国家码 → 本地化国家名（浏览器内置 Intl.DisplayNames，无需维护 200+ 国家的映射表）
function countryName(code: string, lang: string): string {
  if (!/^[A-Za-z]{2}$/.test(code)) return ''
  try {
    return new Intl.DisplayNames([lang], { type: 'region' }).of(code.toUpperCase()) ?? ''
  } catch {
    return ''
  }
}

// ========== 详情信息网格（替代 antd Descriptions） ==========
function InfoGrid({ items }: { items: { key: string; label: string; children: React.ReactNode }[] }) {
  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-1.5 glass-subcard rounded-tile p-3">
      {items.map((item) => (
        <div key={item.key} className="flex justify-between gap-3 min-w-0">
          <span className="text-gray-500 dark:text-gray-400 text-footnote shrink-0 pt-0.5">{item.label}</span>
          <span className="text-body text-right break-all">{item.children}</span>
        </div>
      ))}
    </div>
  )
}

// ========== 文件进度条 ==========
function FileProgress({ done, total }: { done: number; total: number }) {
  const pct = Math.round((done / (total || 1)) * 1000) / 10
  return (
    <div className="w-20">
      <div className="tm-progress-track">
        <div className="tm-progress-fill" style={{ width: `${pct}%` }} />
      </div>
    </div>
  )
}

// ========== 文件树行 ==========
interface FileRowProps {
  node: FileNode
  depth: number
  wanted: boolean[]
  priorities: number[]
  selectedRows: Set<number>
  onToggleFile: (i: number) => void
  onToggleDir: (n: FileNode) => void
  onChangePriority: (n: FileNode, v: number) => void
  onToggleRow: (i: number) => void
  onRename: (n: FileNode) => void
}

function FileRow({ node, depth, wanted, priorities, selectedRows, onToggleFile, onToggleDir, onChangePriority, onToggleRow, onRename }: FileRowProps) {
  const { t } = useTranslation()
  const isDir = !!node.children
  const [open, setOpen] = useState(true)
  const pct = Math.round((node.bytesCompleted / (node.length || 1)) * 1000) / 10
  const indeterminate = isDir && node.children?.some((c) => c.wanted) && node.children?.some((c) => !c.wanted)

  return (
    <>
      <div
        className={cn(
          // flex-wrap：窄屏定宽列（大小/进度/勾选/优先级）占满整行时折到第二行，
          // 保住文件名的最小宽度，而不是把它压成 0
          'group flex flex-wrap items-center gap-x-2 gap-y-1 px-2 py-1.5 text-footnote border-b border-gray-100/70 dark:border-gray-800 hover:bg-gray-50 dark:hover:bg-gray-800/50',
          selectedRows.has(node._i) && 'bg-primary/5',
        )}
        style={{ paddingLeft: depth * 20 + 8 }}
      >
        {isDir ? (
          // 命中区抬到 32px（桌面最小档），负边距抵消视觉尺寸，不改变树行高度
          <button onClick={() => setOpen(!open)} className="w-8 h-8 -m-2 flex items-center justify-center text-gray-400 shrink-0 rounded-md hover:text-primary focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring">
            <ChevronRight className={cn('w-3.5 h-3.5 transition-transform', open && 'rotate-90')} />
          </button>
        ) : (
          <span className="w-3.5 shrink-0" />
        )}
        {isDir
          ? (open ? <FolderOpen className="w-3.5 h-3.5 text-primary/70 shrink-0" /> : <Folder className="w-3.5 h-3.5 text-primary/70 shrink-0" />)
          : <File className="w-3.5 h-3.5 text-gray-400 shrink-0" />}
        {/* min-w：flex-1(basis 0) 在定宽列挤占下会塌缩到 0，真机上文件名因此不可见 */}
        <span className="min-w-[8rem] flex-1 truncate" title={node.path}>{node.name}</span>
        <button
          onClick={() => onRename(node)}
          className="p-2 -m-2 rounded-md opacity-0 group-hover:opacity-100 focus-visible:opacity-100 text-gray-400 hover:text-primary shrink-0 transition-opacity focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          title={t('detail.rename')}
        >
          <Pencil className="w-3 h-3" />
        </button>
        <span className="text-gray-500 shrink-0 w-16 text-right">{formatBytes(node.length)}</span>
        <FileProgress done={node.bytesCompleted} total={node.length} />
        {isDir ? (
          <Checkbox
            checked={indeterminate ? 'indeterminate' : node.wanted}
            onCheckedChange={() => onToggleDir(node)}
            className="shrink-0"
          />
        ) : (
          <Checkbox
            checked={wanted[node._i]}
            onCheckedChange={() => onToggleFile(node._i)}
            className="shrink-0"
          />
        )}
        <Select
          value={node._i >= 0 ? String(priorities[node._i]) : node.priority >= 0 ? String(node.priority) : 'none'}
          onValueChange={(v) => onChangePriority(node, Number(v))}
        >
          <SelectTrigger className="h-8 w-24 text-footnote shrink-0">
            <SelectValue />
          </SelectTrigger>
          <SelectContent className="glass-panel-solid">
            <SelectItem value="-1">{t('action.priorityLow')}</SelectItem>
            <SelectItem value="0">{t('action.priorityNormal')}</SelectItem>
            <SelectItem value="1">{t('action.priorityHigh')}</SelectItem>
          </SelectContent>
        </Select>
      </div>
      {isDir && open && node.children?.map((c) => (
        <FileRow
          key={c.key}
          node={c}
          depth={depth + 1}
          wanted={wanted}
          priorities={priorities}
          selectedRows={selectedRows}
          onToggleFile={onToggleFile}
          onToggleDir={onToggleDir}
          onChangePriority={onChangePriority}
          onToggleRow={onToggleRow}
          onRename={onRename}
        />
      ))}
    </>
  )
}

// ========== 通用表格（替代 antd Table） ==========
interface TableColumn<T> {
  key: string
  title: string
  render: (row: T) => React.ReactNode
  className?: string
  // 排序键；未设置则该列不可排序
  sortValue?: (row: T) => string | number
}

function SimpleTable<T>({ columns, data, emptyText }: { columns: TableColumn<T>[]; data: T[]; emptyText?: string }) {
  const { t } = useTranslation()
  const [sort, setSort] = useState<{ key: string; asc: boolean } | null>(null)

  const sorted = useMemo(() => {
    if (!sort) return data
    const get = columns.find((c) => c.key === sort.key)?.sortValue
    if (!get) return data
    return [...data].sort((a, b) => {
      const va = get(a)
      const vb = get(b)
      const cmp = typeof va === 'number' && typeof vb === 'number' ? va - vb : String(va).localeCompare(String(vb))
      return sort.asc ? cmp : -cmp
    })
  }, [data, columns, sort])

  if (data.length === 0) {
    return <div className="text-center text-gray-400 text-body py-8">{emptyText ?? t('common.empty')}</div>
  }

  const toggleSort = (key: string) =>
    setSort((s) => (s?.key === key ? { key, asc: !s.asc } : { key, asc: true }))

  const sortIcon = (c: TableColumn<T>) => {
    if (!c.sortValue) return null
    if (sort?.key === c.key) {
      return sort.asc
        ? <ChevronUp className="w-3 h-3 ml-0.5 text-primary" />
        : <ChevronDown className="w-3 h-3 ml-0.5 text-primary" />
    }
    return <ChevronsUpDown className="w-3 h-3 ml-0.5 opacity-30" />
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-footnote">
        <thead>
          <tr className="border-b border-gray-200 dark:border-gray-700 text-gray-500 dark:text-gray-400">
            {columns.map((c) => (
              <th
                key={c.key}
                onClick={c.sortValue ? () => toggleSort(c.key) : undefined}
                className={cn('text-left font-medium px-2 py-1.5 whitespace-nowrap', c.sortValue && 'cursor-pointer select-none hover:text-gray-700 dark:hover:text-gray-200', c.className)}
              >
                {c.title}
                {sortIcon(c)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {sorted.map((row, idx) => (
            <tr key={idx} className="border-b border-gray-100/70 dark:border-gray-800 hover:bg-gray-50 dark:hover:bg-gray-800/50">
              {columns.map((c) => (
                <td key={c.key} className={cn('px-2 py-1.5 whitespace-nowrap', c.className)}>{c.render(row)}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

// ========== 块位图（对齐 PiecesTab：canvas 渲染） ==========
function PiecesView({ torrent }: { torrent: Torrent }) {
  const { t } = useTranslation()
  const theme = useAppStore((s) => s.theme)
  const themePreset = useAppStore((s) => s.themePreset)
  const containerRef = useRef<HTMLDivElement>(null)
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const [hover, setHover] = useState(-1)
  const [cols, setCols] = useState(64)

  const CELL = 4
  const GAP = 1

  const bits = useMemo(() => {
    if (!torrent.pieces) return null
    try {
      const bin = atob(torrent.pieces)
      const out: boolean[] = []
      for (let i = 0; i < bin.length; i++) {
        const byte = bin.charCodeAt(i)
        for (let b = 7; b >= 0; b--) {
          out.push(((byte >> b) & 1) === 1)
        }
      }
      return out
    } catch {
      return null
    }
  }, [torrent.pieces])

  const count = torrent.pieceCount ?? 0

  // 容器宽度 → 列数（自适应）
  useEffect(() => {
    const el = containerRef.current
    if (!el) return
    const update = () => setCols(Math.max(16, Math.floor(el.clientWidth / (CELL + GAP))))
    update()
    const ro = new ResizeObserver(update)
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  // 已下载块数 = 位图中置位的数量（截断到实际块数）
  const doneCount = useMemo(() => (bits ? bits.slice(0, count).filter(Boolean).length : 0), [bits, count])
  const donePct = count > 0 ? Math.round((doneCount / count) * 1000) / 10 : 0
  // 正在下载中的块（近似：已下载块之后的第一个未下载块），仅下载状态下高亮
  const isDownloading = torrent.status === 3 || torrent.status === 4
  const activeIdx = isDownloading && doneCount < count ? doneCount : -1

  // 绘制位图
  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas || !bits) return
    const rows = Math.ceil(count / cols)
    canvas.width = cols * (CELL + GAP)
    canvas.height = rows * (CELL + GAP)
    const ctx = canvas.getContext('2d')
    if (!ctx) return
    const css = getComputedStyle(document.documentElement)
    const token = (name: string, fallback: string) => css.getPropertyValue(name).trim() || fallback
    const doneColor = token('--color-green-500', '#22c55e')
    const activeColor = token('--color-blue-500', '#3b82f6')
    const pendingColor = theme === 'dark' ? token('--color-gray-700', '#3f3f46') : token('--color-gray-200', '#e5e7eb')
    ctx.clearRect(0, 0, canvas.width, canvas.height)
    for (let i = 0; i < count; i++) {
      const x = (i % cols) * (CELL + GAP)
      const y = Math.floor(i / cols) * (CELL + GAP)
      ctx.fillStyle = bits[i] ? doneColor : i === activeIdx ? activeColor : pendingColor
      ctx.fillRect(x, y, CELL, CELL)
    }
  }, [bits, count, cols, activeIdx, theme, themePreset])

  const onMouseMove = (e: React.MouseEvent<HTMLCanvasElement>) => {
    const canvas = canvasRef.current
    if (!canvas) return
    const rect = canvas.getBoundingClientRect()
    const x = Math.floor((e.clientX - rect.left) / (CELL + GAP))
    const y = Math.floor((e.clientY - rect.top) / (CELL + GAP))
    const idx = y * cols + x
    setHover(idx >= 0 && idx < count ? idx : -1)
  }

  return (
    <div className="space-y-3">
      <div className="text-body">
        {t('detail.piecesProgress')}: {doneCount}/{count} ({donePct}%)
      </div>
      <div className="tm-progress-track">
        <div className="tm-progress-fill" style={{ width: `${Math.min(100, donePct)}%` }} />
      </div>
      {bits ? (
        <div ref={containerRef} className="overflow-auto rounded-lg border border-gray-200/60 dark:border-gray-700/50 p-2">
          <canvas
            ref={canvasRef}
            onMouseMove={onMouseMove}
            onMouseLeave={() => setHover(-1)}
            style={{ cursor: 'crosshair' }}
          />
        </div>
      ) : (
        <div className="text-body text-gray-400 py-4 text-center">{t('detail.piecesUnavailable')}</div>
      )}
      {hover >= 0 && bits && (
        <div className="text-footnote text-gray-500">
          {t('detail.pieceIndex')}: <span className="tm-mono">{hover}</span> ·{' '}
          {bits[hover] ? t('detail.pieceDone') : hover === activeIdx ? t('detail.pieceDownloading') : t('detail.piecePending')}
        </div>
      )}
      <div className="flex items-center gap-3 text-footnote text-gray-500">
        <span className="flex items-center gap-1">
          <span className="w-2.5 h-2.5 rounded-full bg-green-500 inline-block" />
          {t('detail.pieceDone')}
        </span>
        <span className="flex items-center gap-1">
          <span className="w-2.5 h-2.5 rounded-full bg-blue-500 inline-block" />
          {t('detail.pieceDownloading')}
        </span>
        <span className="flex items-center gap-1">
          <span className="w-2.5 h-2.5 rounded-full bg-gray-200 inline-block" />
          {t('detail.piecePending')}
        </span>
      </div>
    </div>
  )
}

// 种子详情（文件/Peers/Trackers）；移动端以底部 Sheet 呈现，桌面端为居中 Dialog
export function TorrentDetail({ torrent, onClose, onOpenChange, isMobile }: { torrent: Torrent | null; onClose: () => void; onOpenChange?: (open: boolean) => void; isMobile?: boolean }) {
  const { t, i18n } = useTranslation()
  const { can } = usePlatform()
  const revealPath = useRevealPath()
  const sem = useSemanticPath()
  const actions = useTorrentActions()
  const [detail, setDetail] = useState<Torrent | null>(null)
  const [loading, setLoading] = useState(false)
  const [editTarget, setEditTarget] = useState<EditTarget | null>(null)

  // 文件选择状态（本地可编辑）
  const [wanted, setWanted] = useState<boolean[]>([])
  const [priorities, setPriorities] = useState<number[]>([])
  const [fileQuery, setFileQuery] = useState('')
  // Peer 地理位置缓存（geoMapRef 记录已查询过的 IP，避免轮询重复请求）
  const [geoMap, setGeoMap] = useState<Record<string, { country: string; city: string }>>({})
  const geoMapRef = useRef<Record<string, boolean>>({})
  // 多选文件优先级
  const [selectedRows, setSelectedRows] = useState<Set<number>>(new Set())
  // 文件/目录重命名
  const [renameTarget, setRenameTarget] = useState<{ path: string; name: string } | null>(null)

  useEffect(() => {
    if (!torrent) return
    // cancelled 竞态保护：快速切换种子时旧请求可能晚于新请求返回，
    // 否则会把旧种子的文件勾选/优先级覆盖到新种子上，保存时写错对象
    let cancelled = false
    setDetail(null)
    setGeoMap({})
    geoMapRef.current = {}
    setFileQuery('')
    setSelectedRows(new Set())
    setLoading(true)
    torrentApi
      .detail(torrent.id)
      .then((d) => {
        if (cancelled) return
        setDetail(d)
        setWanted((d.fileStats ?? []).map((f) => f.wanted))
        setPriorities((d.fileStats ?? []).map((f) => f.priority))
      })
      .catch(() => {
        if (!cancelled) toast.error(t('common.loading'))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [torrent, t])

  // 批量查询 Peer 地理位置（按 IP 集合去重，避免 3s 轮询重复请求）
  const geoKey = useMemo(() => {
    if (!detail?.peers?.length) return ''
    return Array.from(new Set(detail.peers.map((p) => p.address).filter(Boolean))).sort().join(',')
  }, [detail])

  useEffect(() => {
    if (!geoKey) return
    const ips = geoKey.split(',')
    const pending = ips.filter((ip) => !(ip in geoMapRef.current))
    if (pending.length === 0) return
    let cancelled = false
    torrentApi.peersGeo(pending).then((m) => {
      if (cancelled) return
      setGeoMap((prev) => ({ ...prev, ...m }))
      for (const ip of pending) geoMapRef.current[ip] = true
    }).catch(() => {})
    return () => { cancelled = true }
  }, [geoKey])

  // 定时刷新 Peers / Trackers，保持详情实时
  useEffect(() => {
    if (!torrent) return
    let cancelled = false
    const id = setInterval(() => {
      torrentApi.detail(torrent.id)
        .then((d) => { if (!cancelled) setDetail(d) })
        .catch(() => {})
    }, 3000)
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [torrent])

  const saveFiles = async () => {
    if (!detail) return
    const filesWanted = wanted.map((w, i) => (w ? i : -1)).filter((i) => i >= 0)
    const filesUnwanted = wanted.map((w, i) => (w ? -1 : i)).filter((i) => i >= 0)
    const priorityHigh = priorities.map((p, i) => (p === 1 ? i : -1)).filter((i) => i >= 0)
    const priorityLow = priorities.map((p, i) => (p === -1 ? i : -1)).filter((i) => i >= 0)
    const priorityNormal = priorities.map((p, i) => (p === 0 ? i : -1)).filter((i) => i >= 0)
    await actions.update(detail.id, {
      filesWanted,
      filesUnwanted,
      priorityHigh,
      priorityLow,
      priorityNormal,
    })
  }

  const infoItems = useMemo(() => {
    if (!detail) return []
    const statusKey: Record<number, string> = { 0: 'paused', 1: 'verifying', 2: 'verifying', 3: 'downloading', 4: 'downloading', 5: 'seeding', 6: 'seeding' }
    const priorityLabel =
      detail.bandwidthPriority === 1 ? t('action.priorityHigh') : detail.bandwidthPriority === -1 ? t('action.priorityLow') : t('action.priorityNormal')
    return [
      { key: 'status', label: t('detail.status'), children: t(`nav.${statusKey[detail.status] ?? 'paused'}`) },
      { key: 'priority', label: t('detail.priority'), children: priorityLabel },
      { key: 'queuePosition', label: t('detail.queuePosition'), children: detail.queuePosition + 1 },
      { key: 'size', label: t('columns.size'), children: formatBytes(detail.totalSize) },
      { key: 'fileCount', label: t('detail.fileCount'), children: detail.fileCount || detail.files?.length || 0 },
      { key: 'downloaded', label: t('detail.downloaded'), children: formatBytes(detail.downloadedEver) },
      { key: 'uploaded', label: t('detail.uploaded'), children: formatBytes(detail.uploadedEver) },
      { key: 'left', label: t('detail.leftUntilDone'), children: formatBytes(detail.leftUntilDone) },
      { key: 'progress', label: t('columns.progress'), children: formatPercent(detail.percentDone) },
      { key: 'ratio', label: t('columns.ratio'), children: detail.uploadRatio < 0 ? '\u221e' : detail.uploadRatio.toFixed(2) },
      { key: 'seedingTime', label: t('detail.seedingTime'), children: formatDuration(detail.secondsSeeding) },
      { key: 'downloadSpeed', label: t('detail.downloadSpeed'), children: formatSpeed(detail.rateDownload) },
      { key: 'uploadSpeed', label: t('detail.uploadSpeed'), children: formatSpeed(detail.rateUpload) },
      { key: 'connectedPeers', label: t('detail.connectedPeers'), children: detail.peersConnected },
      { key: 'error', label: t('detail.error'), children: translateError(detail.errorString, t) || '-' },
      { key: 'labels', label: t('detail.labels'), children: detail.labels?.length ? detail.labels.join(', ') : '-' },
      { key: 'private', label: t('detail.private'), children: detail.isPrivate ? t('common.yes') : t('common.no') },
      { key: 'mainTracker', label: t('detail.mainTracker'), children: detail.trackerStats?.[0]?.host || detail.trackerStats?.[0]?.announce || '-' },
      { key: 'dir', label: t('detail.downloadDir'), children: sem(detail.downloadDir) || '-' },
      { key: 'creator', label: t('detail.creator'), children: detail.creator || '-' },
      { key: 'comment', label: t('detail.comment'), children: detail.comment || '-' },
      { key: 'added', label: t('detail.addedDate'), children: formatDate(detail.addedDate) },
      { key: 'done', label: t('detail.doneDate'), children: detail.doneDate ? formatDate(detail.doneDate) : '-' },
      { key: 'activity', label: t('detail.activityDate'), children: formatDate(detail.activityDate) },
      { key: 'hash', label: t('detail.hash'), children: detail.hashString || '-' },
    ]
  }, [detail, t, sem])

  // 文件树（目录聚合）
  const fileTree = useMemo<FileNode[]>(() => {
    if (!detail?.files || !detail.fileStats) return []
    const stats = detail.fileStats
    const root: FileNode[] = []
    const map = new Map<string, FileNode>()
    detail.files.forEach((f, i) => {
      const parts = f.name.split('/').filter(Boolean)
      let cur = root
      let acc = ''
      parts.forEach((part, idx) => {
        acc = acc ? `${acc}/${part}` : part
        const isLeaf = idx === parts.length - 1
        let node = map.get(acc)
        if (!node) {
          node = {
            key: acc,
            name: part,
            path: acc,
            _i: isLeaf ? i : -1,
            length: 0,
            bytesCompleted: 0,
            wanted: isLeaf ? wanted[i] : false,
            priority: isLeaf ? priorities[i] : -1,
            children: isLeaf ? undefined : [],
          }
          map.set(acc, node)
          cur.push(node)
        }
        node.length += f.length
        node.bytesCompleted += f.bytesCompleted
        if (isLeaf) {
          node.wanted = wanted[i]
          node.priority = priorities[i]
        }
        cur = node.children ?? []
      })
    })
    // 目录节点聚合 wanted / priority
    const agg = (nodes: FileNode[]): { wanted: boolean; priority: number } => {
      const results: { wanted: boolean; priority: number }[] = []
      for (const n of nodes) {
        if (n.children) {
          const a = agg(n.children)
          n.wanted = a.wanted
          n.priority = a.priority
          results.push(a)
        } else {
          results.push({ wanted: n.wanted, priority: n.priority })
        }
      }
      if (results.length === 0) return { wanted: false, priority: -1 }
      return {
        wanted: results.every((r) => r.wanted),
        priority: results.every((r) => r.priority === results[0].priority) ? results[0].priority : -1,
      }
    }
    agg(root)
    return root
  }, [detail, wanted, priorities])

  // 文件搜索过滤（保留目录层级）
  const filteredTree = useMemo<FileNode[]>(() => {
    const q = fileQuery.trim().toLowerCase()
    if (!q) return fileTree
    const walk = (nodes: FileNode[]): FileNode[] =>
      nodes
        .filter((n) => n.path.toLowerCase().includes(q) || (n.children ? walk(n.children).length > 0 : false))
        .map((n) => (n.children ? { ...n, children: walk(n.children) } : n))
    return walk(fileTree)
  }, [fileTree, fileQuery])

  const setAllWanted = (v: boolean) => setWanted((w) => w.map(() => v))

  const batchChangePriority = (v: number) => {
    if (selectedRows.size === 0) return
    setPriorities((p) => p.map((x, idx) => (selectedRows.has(idx) ? v : x)))
    setSelectedRows(new Set())
  }

  const batchChangeWanted = (v: boolean) => {
    if (selectedRows.size === 0) return
    setWanted((w) => w.map((x, idx) => (selectedRows.has(idx) ? v : x)))
    setSelectedRows(new Set())
  }

  const toggleFile = (i: number) => setWanted((w) => w.map((x, idx) => (idx === i ? !x : x)))
  const toggleDir = (n: FileNode) => {
    const leaves = collectLeaves(n)
    const target = !n.wanted
    setWanted((w) => w.map((x, idx) => (leaves.includes(idx) ? target : x)))
  }
  const changePriority = (n: FileNode, v: number) => {
    if (n._i >= 0) {
      setPriorities((p) => p.map((x, idx) => (idx === n._i ? v : x)))
    } else {
      const leaves = collectLeaves(n)
      setPriorities((p) => p.map((x, idx) => (leaves.includes(idx) ? v : x)))
    }
  }
  const toggleRow = (i: number) => {
    setSelectedRows((prev) => { const next = new Set(prev); next.has(i) ? next.delete(i) : next.add(i); return next })
  }

  // 重命名文件/目录并刷新详情（Transmission 会在重命名后自动重新校验）
  const submitRename = async () => {
    if (!detail || !renameTarget) return
    try {
      await torrentApi.rename(detail.id, renameTarget.path, renameTarget.name)
      toast.success(t('toast.renamed'))
      const d = await torrentApi.detail(detail.id)
      setDetail(d)
      setWanted((d.fileStats ?? []).map((f) => f.wanted))
      setPriorities((d.fileStats ?? []).map((f) => f.priority))
      setRenameTarget(null)
    } catch {
      // handled by interceptor
    }
  }

  const prioOptions = [
    { value: -1, label: t('action.priorityLow') },
    { value: 0, label: t('action.priorityNormal') },
    { value: 1, label: t('action.priorityHigh') },
  ]

  const fileColumns = [
    { key: 'name', title: t('detail.fileName') },
    { key: 'size', title: t('detail.fileSize') },
    { key: 'progress', title: t('detail.fileProgress') },
    { key: 'wanted', title: t('detail.fileSelection') },
    { key: 'priority', title: t('detail.filePriority') },
  ]

  const peerColumns: TableColumn<Record<string, unknown>>[] = [
    {
      key: 'location',
      title: t('detail.peerLocation'),
      render: (p) => {
        const code = (geoMap[p.address as string]?.country || '').toUpperCase()
        // 无归属地（常见于内网出口 / 私有 IP）：与参考实现一致，地球占位
        if (!/^[A-Z]{2}$/.test(code)) return <span className="text-gray-400">🌐</span>
        const name = countryName(code, i18n.language)
        // 自托管 SVG 国旗：Windows 无彩色国旗字体，emoji 会退化成字母
        return (
          <span className="inline-flex items-center gap-1.5">
            <span className={`fi fi-${code.toLowerCase()}`} style={{ width: 20, height: 15 }} />
            <span>{name || code}</span>
          </span>
        )
      },
      sortValue: (p) => {
        const code = (geoMap[p.address as string]?.country || '').toUpperCase()
        return countryName(code, i18n.language) || code
      },
    },
    { key: 'address', title: t('detail.peerAddress'), render: (p) => <span>{(p.address as string) || '-'}</span>, sortValue: (p) => (p.address as string) || '' },
    {
      key: 'connection',
      title: t('detail.peerConnection'),
      render: (p) => {
        const downloading = !!p.isDownloadingFrom
        const uploading = !!p.isUploadingTo
        if (downloading && uploading) return <span className="text-green-600 dark:text-green-400">{t('detail.peerBoth')}</span>
        if (downloading) return <span className="text-green-600 dark:text-green-400">{t('detail.peerDownloading')}</span>
        if (uploading) return <span className="text-blue-600 dark:text-blue-400">{t('detail.peerUploading')}</span>
        return <span className="text-gray-400">{t('detail.peerIdle')}</span>
      },
      // 排序顺序：双向 → 下载中 → 上传中 → 空闲
      sortValue: (p) => {
        const downloading = !!p.isDownloadingFrom
        const uploading = !!p.isUploadingTo
        return downloading && uploading ? 0 : downloading ? 1 : uploading ? 2 : 3
      },
    },
    { key: 'clientName', title: t('detail.peerClient'), render: (p) => <span>{(p.clientName as string) || '-'}</span>, sortValue: (p) => (p.clientName as string) || '' },
    { key: 'progress', title: t('detail.peerProgress'), render: (p) => formatPercent(p.progress as number), sortValue: (p) => (p.progress as number) || 0 },
    { key: 'down', title: t('detail.peerDown'), render: (p) => <span className="text-green-600 dark:text-green-400">{formatSpeed(p.rateToClient as number)}</span>, sortValue: (p) => (p.rateToClient as number) || 0 },
    { key: 'up', title: t('detail.peerUp'), render: (p) => <span className="text-blue-600 dark:text-blue-400">{formatSpeed(p.rateToPeer as number)}</span>, sortValue: (p) => (p.rateToPeer as number) || 0 },
    { key: 'flags', title: t('detail.peerFlags'), render: (p) => <span>{p.flagStr as string}</span> },
  ]

  // Tracker 状态（工作中/超时/失败/未汇报）
  const renderTrackerStatus = (r: Record<string, unknown>) => {
    if (r.lastAnnounceTimedOut) return <span className="text-yellow-600 dark:text-yellow-400">{t('detail.trackerStatusTimeout')}</span>
    if (r.lastAnnounceSucceeded) return <span className="text-green-600 dark:text-green-400">{t('detail.trackerStatusOk')}</span>
    if (!(r.lastAnnounceTime as number)) return <span className="text-gray-400">{t('detail.trackerStatusPending')}</span>
    return <span className="text-red-500">{t('detail.trackerStatusFailed')}</span>
  }

  const trackerColumns: TableColumn<Record<string, unknown>>[] = [
    { key: 'announce', title: t('detail.trackerAnnounce'), render: (r) => <span className="truncate inline-block max-w-64 align-bottom">{r.announce as string}</span> },
    { key: 'status', title: t('detail.trackerStatus'), render: renderTrackerStatus },
    { key: 'seeders', title: t('detail.trackerSeeders'), render: (r) => r.seederCount as number },
    { key: 'leechers', title: t('detail.trackerLeechers'), render: (r) => r.leecherCount as number },
    { key: 'last', title: t('detail.trackerLastAnnounce'), render: (r) => (r.lastAnnounceTime ? formatDate(r.lastAnnounceTime as number) : '-') },
    { key: 'next', title: t('detail.trackerNextAnnounce'), render: (r) => (r.nextAnnounceTime ? formatDate(r.nextAnnounceTime as number) : '-') },
    {
      key: 'scrape',
      title: t('detail.trackerScrape'),
      render: (r) => {
        const result = r.lastScrapeSucceeded ? 'OK' : ((r.lastScrapeResult as string) || '')
        const time = r.lastScrapeTime ? formatDate(r.lastScrapeTime as number) : ''
        if (!result && !time) return '-'
        return [result, time].filter(Boolean).join(' · ')
      },
    },
  ]

  const closeDetail = () => { onClose(); onOpenChange?.(false) }

  const body = detail ? (
    <div className="space-y-3">
      {can('fs.revealPath') && detail.downloadDir && (
        <div className="flex justify-end">
          <Button size="sm" variant="outline" className="h-8 text-footnote" onClick={() => void revealPath(detail.downloadDir)}>
            <FolderOpen className="w-3.5 h-3.5" />
            {t('action.openDir')}
          </Button>
        </div>
      )}
      <InfoGrid items={infoItems} />
      <Tabs defaultValue="files">
        <TabsList className="w-full justify-start h-auto flex-wrap bg-transparent border-b border-gray-200/40 dark:border-gray-700/30 rounded-none gap-1">
          <TabsTrigger value="files">{t('detail.files')} ({detail.files?.length ?? 0})</TabsTrigger>
          <TabsTrigger value="peers">{t('detail.peersWithCount', { count: detail.peers?.length ?? 0 })}</TabsTrigger>
          <TabsTrigger value="trackers">{t('detail.trackers')} ({detail.trackerStats?.length ?? 0})</TabsTrigger>
          <TabsTrigger value="pieces">{t('detail.pieces')}</TabsTrigger>
          <TabsTrigger value="history">{t('detail.speedHistory')}</TabsTrigger>
        </TabsList>

        <TabsContent value="files" className="pt-2">
          <div className="flex items-center gap-2 mb-2 flex-wrap">
            <div className="relative max-w-48">
              <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-gray-400" />
              <Input
                value={fileQuery}
                onChange={(e) => setFileQuery(e.target.value)}
                placeholder={t('detail.fileSearch')}
                className="h-8 pl-8 text-footnote"
              />
            </div>
            <Button size="sm" variant="outline" className="h-8 text-footnote" onClick={() => setAllWanted(true)}>{t('detail.selectAllFiles')}</Button>
            <Button size="sm" variant="outline" className="h-8 text-footnote" onClick={() => setAllWanted(false)}>{t('detail.deselectAllFiles')}</Button>
            <Select onValueChange={(v) => setPriorities((p) => p.map(() => Number(v)))}>
              <SelectTrigger className="h-8 w-28 text-footnote">
                <SelectValue placeholder={t('detail.setAllPriority')} />
              </SelectTrigger>
              <SelectContent className="glass-panel-solid">
                {prioOptions.map((o) => (
                  <SelectItem key={o.value} value={String(o.value)}>{o.label}</SelectItem>
                ))}
              </SelectContent>
            </Select>
            {selectedRows.size > 0 && (
              <>
                <span className="text-footnote text-blue-500">{`${t('detail.selectedFiles')} ${selectedRows.size}`}</span>
                {prioOptions.map((o) => (
                  <Button key={o.value} size="sm" variant="outline" className="h-8 text-footnote" onClick={() => batchChangePriority(o.value)}>{o.label}</Button>
                ))}
                <Button size="sm" variant="outline" className="h-8 text-footnote" onClick={() => batchChangeWanted(true)}>{t('detail.download')}</Button>
                <Button size="sm" variant="outline" className="h-8 text-footnote" onClick={() => batchChangeWanted(false)}>{t('detail.skip')}</Button>
              </>
            )}
            <div className="flex-1" />
            <Button size="sm" className="h-8 text-footnote" disabled={loading} onClick={saveFiles}>{t('common.save')}</Button>
          </div>

          {/* 文件表头：手机端行会折行，列对不上，只在桌面显示 */}
          <div className="hidden md:flex items-center gap-2 px-2 py-1.5 text-footnote text-gray-500 dark:text-gray-400 border-b border-gray-200 dark:border-gray-700 font-medium">
            <span className="flex-1">{t('detail.fileName')}</span>
            <span className="w-16 text-right">{t('detail.fileSize')}</span>
            <span className="w-20">{t('detail.fileProgress')}</span>
            <span className="w-4">{t('detail.fileSelection')}</span>
            <span className="w-24">{t('detail.filePriority')}</span>
          </div>

          {loading && filteredTree.length === 0 ? (
            <div className="text-center text-gray-400 text-body py-8">{t('common.loading')}</div>
          ) : (
            <div className="max-h-80 overflow-y-auto">
              {filteredTree.map((n) => (
                <FileRow
                  key={n.key}
                  node={n}
                  depth={0}
                  wanted={wanted}
                  priorities={priorities}
                  selectedRows={selectedRows}
                  onToggleFile={toggleFile}
                  onToggleDir={toggleDir}
                  onChangePriority={changePriority}
                  onToggleRow={toggleRow}
                  onRename={(n2) => setRenameTarget({ path: n2.path, name: n2.name })}
                />
              ))}
            </div>
          )}
        </TabsContent>

        <TabsContent value="peers" className="pt-2">
          <SimpleTable columns={peerColumns} data={(detail.peers ?? []) as unknown as Record<string, unknown>[]} emptyText={t('common.empty')} />
        </TabsContent>

        <TabsContent value="trackers" className="pt-2">
          <div className="flex justify-end gap-2 mb-2">
            <Button
              size="sm"
              variant="outline"
              className="h-8 text-footnote"
              onClick={() => { void actions.reannounce(detail.id); toast.success(t('toast.reannounced')) }}
            >
              {t('detail.reannounce')}
            </Button>
            <Button size="sm" variant="outline" className="h-8 text-footnote" onClick={() => setEditTarget({ torrent: detail, mode: 'trackers' })}>{t('action.editTrackers')}</Button>
          </div>
          {(() => {
            const stats = detail.trackerStats ?? []
            const tiers = Array.from(new Set(stats.map((s) => s.tier))).sort((a, b) => a - b)
            if (tiers.length === 0) {
              return <SimpleTable columns={trackerColumns} data={[]} emptyText={t('common.empty')} />
            }
            return tiers.map((tier) => (
              <div key={tier} className="mb-3">
                <div className="text-footnote text-gray-500 font-medium mb-1">{t('detail.trackerTier', { n: tier + 1 })}</div>
                <SimpleTable
                  columns={trackerColumns}
                  data={stats.filter((s) => s.tier === tier) as unknown as Record<string, unknown>[]}
                />
              </div>
            ))
          })()}
        </TabsContent>

        <TabsContent value="pieces" className="pt-2">
          <PiecesView torrent={detail} />
        </TabsContent>

        <TabsContent value="history" className="pt-2">
          <SpeedHistory />
        </TabsContent>
      </Tabs>
    </div>
  ) : (
    <div className="text-center text-gray-400 text-body py-8">{t('common.loading')}</div>
  )

  return (
    <>
      {isMobile ? (
        <Sheet open={!!torrent} onOpenChange={(o) => { if (!o) closeDetail() }}>
          <SheetContent onDismiss={closeDetail} className="max-w-none max-h-[88dvh] flex flex-col">
            <SheetHeader className="px-4 pb-2 border-b border-gray-200/40 dark:border-gray-700/30">
              <SheetTitle className="pr-8">
                <span className="block truncate">{torrent?.name}</span>
              </SheetTitle>
            </SheetHeader>
            <div className="flex-1 min-h-0 overflow-y-auto px-4 py-3">{body}</div>
          </SheetContent>
        </Sheet>
      ) : (
        <Dialog open={!!torrent} onOpenChange={(o) => { if (!o) closeDetail() }}>
          <DialogContent className="sm:max-w-3xl h-[90dvh] sm:h-auto sm:max-h-[85dvh] flex flex-col p-0">
            <DialogHeader className="px-4 pt-4 pb-2 border-b border-gray-200/40 dark:border-gray-700/30">
              <DialogTitle className="flex items-center gap-2 pr-8">
                <span className="truncate">{torrent?.name}</span>
              </DialogTitle>
            </DialogHeader>
            <div className="flex-1 overflow-y-auto px-4 py-3">{body}</div>
          </DialogContent>
        </Dialog>
      )}
      {/* 文件/目录重命名 */}
      <Dialog open={!!renameTarget} onOpenChange={(o) => { if (!o) setRenameTarget(null) }}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t('detail.rename')}</DialogTitle>
          </DialogHeader>
          <div className="space-y-3">
            <div className="text-footnote text-gray-500 break-all bg-gray-100 dark:bg-gray-800 rounded-md px-2 py-1.5">
              {renameTarget?.path}
            </div>
            <Input
              value={renameTarget?.name ?? ''}
              onChange={(e) => setRenameTarget((r) => (r ? { ...r, name: e.target.value } : r))}
              placeholder={t('detail.rename')}
            />
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setRenameTarget(null)}>{t('common.cancel')}</Button>
              <Button disabled={!renameTarget?.name.trim()} onClick={submitRename}>{t('common.confirm')}</Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
      <EditModals target={editTarget} onClose={() => setEditTarget(null)} />
    </>
  )
}
