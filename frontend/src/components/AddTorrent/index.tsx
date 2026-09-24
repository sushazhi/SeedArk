import { useEffect, useRef, useState } from 'react'
import { FolderOpen, Inbox, Search, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { torrentApi } from '@/api/torrent'
import { usePlatform } from '@/platform'
import { toast } from '@/lib/toast'
import { TagInput } from '@/components/TagInput'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useAppStore } from '@/stores/appStore'
import { ClearDirHistory, DIR_HISTORY_KEY } from '@/components/DirHistory'
import { formatBytes } from '@/utils/format'
import { parseTorrentFile } from '@/utils/torrentParser'
import type { ParsedTorrent } from '@/utils/torrentParser'

function loadDirHistory(): string[] {
  try {
    const arr = JSON.parse(localStorage.getItem(DIR_HISTORY_KEY) ?? '[]') as string[]
    return Array.isArray(arr) ? arr.filter(Boolean) : []
  } catch {
    return []
  }
}

// 添加种子（支持批量：多文件 / 多磁力链接；拖拽内容自动填入）
export function AddTorrent({ open, onClose, initialFiles, initialText }: {
  open: boolean
  onClose: () => void
  initialFiles?: File[]
  initialText?: string
}) {
  const { t } = useTranslation()
  const { can, pickFiles, pickFolder } = usePlatform()
  const session = useAppStore((s) => s.session)
  // 当前下载器是否接受单种带宽优先级（qBittorrent 无此概念，见驱动 Capabilities）
  const canBandwidthPriority = session?.caps?.perTorrentLimits !== false

  const [tab, setTab] = useState<'file' | 'url'>('file')
  const [files, setFiles] = useState<File[]>([])
  // 飞牛环境通过文件选择器选中的 NAS 种子路径
  const [torrentPaths, setTorrentPaths] = useState<string[]>([])
  const [urlText, setUrlText] = useState('')
  const [downloadDir, setDownloadDir] = useState('')
  const [paused, setPaused] = useState(false)
  const [verify, setVerify] = useState(false)
  const [labels, setLabels] = useState<string[]>([])
  const [priority, setPriority] = useState(0)
  const [dragging, setDragging] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [dirHistory, setDirHistory] = useState<string[]>(loadDirHistory)
  const [parsed, setParsed] = useState<ParsedTorrent[]>([])
  // parsed[i] 对应的原始 File。用对象引用而非下标建立映射：解析是异步的，期间
  // files 可能被增删（例如部分失败后移除），下标会错位甚至越界；File 引用始终可靠
  const [parsedFiles, setParsedFiles] = useState<File[]>([])
  // 每个种子的文件勾选状态（与 parsed 对齐）；搜索词独立于每个种子
  const [selections, setSelections] = useState<boolean[][]>([])
  const [searches, setSearches] = useState<string[]>([])
  const fileInputRef = useRef<HTMLInputElement>(null)

  const reset = () => {
    setFiles([])
    setTorrentPaths([])
    setUrlText('')
    setDownloadDir('')
    setPaused(false)
    setVerify(false)
    setLabels([])
    setPriority(0)
    setParsed([])
    setParsedFiles([])
    setSelections([])
    setSearches([])
    setTab('file')
  }

  // 打开时消费拖拽初始内容，并自动填充默认下载目录
  useEffect(() => {
    if (!open) return
    if (initialFiles?.length) {
      setFiles((prev) => [...prev, ...initialFiles])
      setTab('file')
    }
    if (initialText) {
      setUrlText((prev) => (prev ? prev + '\n' : '') + initialText)
      setTab('url')
    }
    setDownloadDir((cur) => cur || session?.downloadDir || dirHistory[0] || '')
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, initialFiles, initialText, session?.downloadDir])

  // 解析选中的 .torrent 文件（预览文件名/大小/文件数/Tracker）
  useEffect(() => {
    if (files.length === 0) {
      setParsed([])
      return
    }
    const load = async () => {
      const results: ParsedTorrent[] = []
      const sources: File[] = []
      for (let i = 0; i < files.length; i++) {
        try {
          const f = files[i]
          const buf = new Uint8Array(await f.arrayBuffer())
          const p = parseTorrentFile(buf)
          if (p) {
            results.push(p)
            sources.push(f)
          }
        } catch {
          // 非 torrent 文件忽略（提交时仍会上传一次，由后端给出明确报错）
        }
      }
      setParsed(results)
      setParsedFiles(sources)
    }
    void load()
  }, [files])

  // 解析结果变化时重置勾选状态为全选（默认下载全部文件）
  useEffect(() => {
    setSelections(parsed.map((p) => p.files.map(() => true)))
    setSearches(parsed.map(() => ''))
  }, [parsed])

  // 切换单个文件勾选
  const toggleFile = (pi: number, fi: number, v: boolean) => {
    setSelections((prev) => {
      const next = prev.map((row) => [...row])
      if (!next[pi]) next[pi] = []
      next[pi][fi] = v
      return next
    })
  }

  // 全选 / 反选
  const toggleAll = (pi: number) => {
    setSelections((prev) => {
      const next = prev.map((row) => [...row])
      const all = (next[pi] ?? []).every(Boolean)
      next[pi] = (parsed[pi]?.files ?? []).map(() => !all)
      return next
    })
  }

  // 保存目录历史（去重，最多 10 条）
  const rememberDir = (dir: string) => {
    if (!dir) return
    const next = [dir, ...dirHistory.filter((x) => x !== dir)].slice(0, 10)
    setDirHistory(next)
    localStorage.setItem(DIR_HISTORY_KEY, JSON.stringify(next))
  }

  // 手动上传：本地文件选择 / 拖拽
  const handlePickFiles = () => {
    fileInputRef.current?.click()
  }

  // 从宿主选择种子：宿主文件选择器（返回宿主路径，后端按白名单读取 .torrent 添加）
  const handlePickFromHost = async () => {
    if (!can('fs.pickFiles')) return
    const picked = await pickFiles()
    if (picked?.length) {
      setTorrentPaths((prev) => [...prev, ...picked])
    }
  }

  const submit = async () => {
    const urls = urlText
      .split('\n')
      .map((x) => x.trim())
      .filter(Boolean)
    if (tab === 'file' && files.length === 0) {
      toast.warning(t('addTorrent.dropHint'))
      return
    }
    if (tab === 'url' && urls.length === 0) {
      toast.warning(t('addTorrent.urlPlaceholder'))
      return
    }
    setSubmitting(true)
    let ok = 0
    let fail = 0
    // 首个失败原因：批量接口返回的失败明细较多，只在汇总提示里带一条
    let firstError = ''
    try {
      if (tab === 'file') {
        // 勾选状态按 parsed 组织，提交前按 File 引用映射回实际要上传的文件
        const selByFile = new Map<File, boolean[]>()
        parsed.forEach((_, pi) => {
          const f = parsedFiles[pi]
          if (f) selByFile.set(f, selections[pi] ?? [])
        })
        const results: boolean[] = new Array(files.length).fill(false)
        for (let i = 0; i < files.length; i++) {
          try {
            const sel = selByFile.get(files[i]) ?? []
            const wanted: number[] = []
            const unwanted: number[] = []
            sel.forEach((v, idx) => (v ? wanted : unwanted).push(idx))
            const allSelected = sel.length > 0 && wanted.length === sel.length
            await torrentApi.addFile(
              files[i], downloadDir || undefined, paused, labels, priority, verify,
              allSelected ? undefined : wanted,
              allSelected ? undefined : unwanted,
            )
            results[i] = true
            ok++
          } catch {
            fail++
          }
        }
        if (fail > 0) setFiles((prev) => prev.filter((_, i) => !results[i]))
        // NAS 路径种子（飞牛文件选择器选中）：后端直接读取路径文件添加。
        // 已成功的从列表中移除，避免用户重试时把同一路径再加一遍
        const failedPaths: string[] = []
        for (const p of torrentPaths) {
          try {
            await torrentApi.addByPath(p, downloadDir || undefined, paused, labels, priority, verify)
            ok++
          } catch {
            fail++
            failedPaths.push(p)
          }
        }
        if (failedPaths.length !== torrentPaths.length) setTorrentPaths(failedPaths)
      } else if (urls.length > 1) {
        // 批量接口逐条尝试并返回失败明细，一次调用即可拿到成败结果。
        // 不要在此失败后逐条重试：后端可能已成功添加了前几条，重试会插入重复种子
        const res = await torrentApi.addUrls(urls, downloadDir || undefined, paused, labels, priority, verify)
        ok += res?.ids?.length ?? 0
        fail += res?.failed?.length ?? 0
        firstError = res.failed?.[0]?.error ?? ''
        if (res.failed?.length) {
          // 输入框只保留失败的链接，用户修正后重试不会重复添加已成功的
          const failedSet = new Set(res.failed.map((f) => f.url))
          setUrlText(urls.filter((u) => failedSet.has(u)).join('\n'))
        }
      } else {
        for (const u of urls) {
          try {
            await torrentApi.addUrl(u, downloadDir || undefined, paused, labels, priority, verify)
            ok++
          } catch {
            fail++
          }
        }
      }
      if (fail === 0) {
        toast.success(`${t('addTorrent.added')} × ${ok}`)
        rememberDir(downloadDir)
        reset()
        onClose()
      } else {
        toast.warning(`${t('addTorrent.added')} ${ok}，${t('common.failed')} ${fail}${firstError ? `（${firstError}）` : ''}`)
      }
    } finally {
      setSubmitting(false)
    }
  }

  const urlCount = urlText.split('\n').filter((x) => x.trim()).length

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="sm:max-w-[480px]">
        <DialogHeader>
          <DialogTitle>{t('addTorrent.title')}</DialogTitle>
        </DialogHeader>

        <div className="space-y-4">
          <Tabs value={tab} onValueChange={(k) => setTab(k as 'file' | 'url')}>
            <TabsList className="grid grid-cols-2 w-full">
              <TabsTrigger value="file">{t('addTorrent.fileTab')} ({files.length})</TabsTrigger>
              <TabsTrigger value="url">{t('addTorrent.urlTab')} ({urlCount})</TabsTrigger>
            </TabsList>
          </Tabs>

          {tab === 'file' ? (
            <>
              <input
                ref={fileInputRef}
                type="file"
                accept=".torrent"
                multiple
                className="hidden"
                onChange={(e) => {
                  const fs = Array.from(e.target.files ?? [])
                  setFiles((prev) => [...prev, ...fs])
                }}
              />
              <div
                className={`border-2 border-dashed rounded-xl p-8 text-center cursor-pointer transition-colors ${
                  dragging ? 'border-primary bg-primary/5' : 'border-gray-300 dark:border-gray-600 hover:border-primary/60'
                }`}
                onClick={handlePickFiles}
                onDragOver={(e) => {
                  e.preventDefault()
                  setDragging(true)
                }}
                onDragLeave={() => setDragging(false)}
                onDrop={(e) => {
                  e.preventDefault()
                  setDragging(false)
                  const fs = Array.from(e.dataTransfer.files ?? [])
                  if (fs.length) setFiles((prev) => [...prev, ...fs])
                }}
              >
                <Inbox className="w-8 h-8 text-gray-400 mx-auto" />
                <p className="mt-2 text-body text-gray-500">{t('addTorrent.dropHint')}</p>
              </div>
              {can('fs.pickFiles') && (
                <Button variant="outline" className="w-full" onClick={handlePickFromHost}>
                  <FolderOpen className="w-4 h-4 mr-2" />
                  {t('addTorrent.pickFromNas')}
                </Button>
              )}
              {files.length > 0 && (
                <div className="space-y-1 max-h-40 overflow-y-auto">
                  {files.map((f, i) => (
                    <div key={`${f.name}-${i}`} className="flex items-center gap-2 text-body">
                      <Badge className="bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-300 truncate flex-1 max-w-60">{f.name}</Badge>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-6 w-6 text-gray-400 hover:text-red-500 shrink-0"
                        onClick={() => setFiles((prev) => prev.filter((_, idx) => idx !== i))}
                      >
                        <X className="w-3.5 h-3.5" />
                      </Button>
                    </div>
                  ))}
                </div>
              )}
              {torrentPaths.length > 0 && (
                <div className="space-y-1 max-h-40 overflow-y-auto">
                  {torrentPaths.map((p, i) => (
                    <div key={p} className="flex items-center gap-2 text-body">
                      <Badge className="bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300 truncate flex-1 max-w-60">
                        {p.split('/').pop() || p}
                      </Badge>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-6 w-6 text-gray-400 hover:text-red-500 shrink-0"
                        onClick={() => setTorrentPaths((prev) => prev.filter((_, idx) => idx !== i))}
                      >
                        <X className="w-3.5 h-3.5" />
                      </Button>
                    </div>
                  ))}
                </div>
              )}
              {/* 种子内容：文件列表 + 勾选下载 + 搜索（qB 风格） */}
              {parsed.length > 0 && (
                <div className="space-y-2 max-h-[32dvh] overflow-y-auto pr-1">
                  {parsed.map((p, pi) => {
                    const sel = selections[pi] ?? []
                    const q = (searches[pi] ?? '').trim().toLowerCase()
                    const shown = p.files
                      .map((f, fi) => ({ f, fi }))
                      .filter(({ f }) => !q || f.name.toLowerCase().includes(q))
                    const wantedCount = sel.filter(Boolean).length
                    const wantedSize = p.files.reduce((s, f, fi) => s + (sel[fi] ? f.length : 0), 0)
                    const allSelected = wantedCount === p.files.length
                    return (
                      <div key={pi} className="rounded-xl border border-gray-200 dark:border-gray-700 p-2.5 space-y-2">
                        <div className="flex items-center gap-2 min-w-0">
                          <span className="flex-1 min-w-0 truncate text-footnote font-medium" title={p.name}>{p.name}</span>
                          <button
                            type="button"
                            onClick={() => toggleAll(pi)}
                            className="text-caption1 text-primary hover:underline shrink-0"
                          >
                            {allSelected ? t('addTorrent.deselectAll') : t('addTorrent.selectAll')}
                          </button>
                        </div>
                        {p.private && (
                          <div className="text-caption2 text-primary">{t('addTorrent.private')}</div>
                        )}
                        <div className="relative">
                          <Search className="absolute left-2 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-gray-400" />
                          <Input
                            value={searches[pi] ?? ''}
                            onChange={(e) => setSearches((prev) => {
                              const next = [...prev]
                              next[pi] = e.target.value
                              return next
                            })}
                            placeholder={t('addTorrent.searchFiles')}
                            className="h-8 pl-8 text-footnote bg-white/50 dark:bg-white/5 border-white/60 dark:border-white/10 rounded-lg"
                          />
                        </div>
                        <div className="max-h-44 overflow-y-auto space-y-0.5 pr-1">
                          {shown.length === 0 && (
                            <div className="text-footnote text-gray-400 text-center py-3">{t('addTorrent.noFiles')}</div>
                          )}
                          {shown.map(({ f, fi }) => (
                            <label
                              key={fi}
                              className="flex items-center gap-2 min-w-0 text-footnote cursor-pointer rounded-md px-1 py-1 hover:bg-white/60 dark:hover:bg-white/8"
                            >
                              <Checkbox
                                checked={sel[fi] ?? false}
                                onCheckedChange={(v) => toggleFile(pi, fi, v === true)}
                              />
                              <span className="flex-1 min-w-0 truncate" title={f.name}>{f.name}</span>
                              <span className="text-gray-400 tm-mono text-caption2 shrink-0">{formatBytes(f.length)}</span>
                            </label>
                          ))}
                        </div>
                        <div className="flex items-center justify-between text-caption1 text-gray-400">
                          <span>{t('addTorrent.selectedFiles', { n: wantedCount, total: p.files.length })}</span>
                          <span className="tm-mono">{formatBytes(wantedSize)} / {formatBytes(p.totalSize)}</span>
                        </div>
                      </div>
                    )
                  })}
                </div>
              )}
            </>
          ) : (
            <textarea
              rows={4}
              autoCapitalize="off"
              autoCorrect="off"
              spellCheck={false}
              placeholder={t('addTorrent.urlPlaceholder') + '（' + t('common.eachLineOne') + '）'}
              value={urlText}
              onChange={(e) => setUrlText(e.target.value)}
              className="w-full rounded-md border border-input bg-transparent px-3 py-2 text-body shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            />
          )}

          <div className="space-y-2">
            <div className="flex gap-2">
              <Input
                placeholder={t('addTorrent.downloadDir')}
                value={downloadDir}
                onChange={(e) => setDownloadDir(e.target.value)}
                list="tm-dir-history"
                className="flex-1 min-w-0"
              />
              {/* 宿主目录选择器：仅飞牛环境显示，浏览器 / 演示环境自动隐藏，仍可手输 */}
              {can('fs.pickFolder') && (
                <Button
                  type="button"
                  variant="outline"
                  className="shrink-0"
                  onClick={async () => {
                    const p = await pickFolder()
                    if (p) setDownloadDir(p)
                  }}
                >
                  <FolderOpen className="w-4 h-4 mr-1.5" />
                  {t('action.selectDir')}
                </Button>
              )}
            </div>
            <datalist id="tm-dir-history">
              {dirHistory.map((d) => <option key={d} value={d} />)}
            </datalist>
            <ClearDirHistory count={dirHistory.length} onClear={() => setDirHistory([])} />
            <div className={`grid gap-2 ${canBandwidthPriority ? 'grid-cols-2' : 'grid-cols-1'}`}>
              <TagInput value={labels} onChange={setLabels} placeholder={t('addTorrent.labels')} />
              {/* 带宽优先级是 Transmission 的单种属性（能力自述里归在 perTorrentLimits）：
                  qBittorrent 没有该概念，传过去会被驱动静默丢弃，界面上就别给这个入口 */}
              {canBandwidthPriority && (
                <Select value={String(priority)} onValueChange={(v) => setPriority(Number(v))}>
                  <SelectTrigger className="h-9 text-footnote">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent className="glass-panel-solid">
                    <SelectItem value="1">{t('action.priorityHigh')}</SelectItem>
                    <SelectItem value="0">{t('action.priorityNormal')}</SelectItem>
                    <SelectItem value="-1">{t('action.priorityLow')}</SelectItem>
                  </SelectContent>
                </Select>
              )}
            </div>
            <div className="flex items-center justify-between">
              <span className="text-body text-gray-600 dark:text-gray-300">{t('addTorrent.paused')}</span>
              <Switch checked={paused} onCheckedChange={setPaused} />
            </div>
            <div className="flex items-center justify-between">
              <span className="text-body text-gray-600 dark:text-gray-300">{t('addTorrent.verify')}</span>
              <Switch checked={verify} onCheckedChange={setVerify} />
            </div>
          </div>

          <Button className="w-full" disabled={submitting} onClick={submit}>
            {submitting ? t('addTorrent.submitting') : t('common.add')}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
