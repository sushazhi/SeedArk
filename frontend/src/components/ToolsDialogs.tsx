import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { torrentApi } from '@/api/torrent'
import { useTorrentActions } from '@/hooks/useTorrentActions'
import { useResponsive } from '@/hooks/useResponsive'
import { useSemanticPath } from '@/hooks/useSemanticPath'
import { useAppStore } from '@/stores/appStore'
import { confirm } from '@/lib/confirm'
import { toast } from '@/lib/toast'
import { ClearDirHistory, DIR_HISTORY_KEY } from '@/components/DirHistory'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { statusColor, statusLabelKey } from '@/utils/status'
import { cssVars } from '@/lib/utils'

// 批量替换 Tracker（参考应用语义：搜索旧地址 → 匹配种子列表+数量 → 替换为 → 逐个执行）
// 状态色统一取自 utils/status，这里不再抄一份色表

export function ReplaceTrackerDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t } = useTranslation()
  const { isMobile, isCoarse } = useResponsive()
  const torrents = useAppStore((s) => s.torrents)
  const setTorrents = useAppStore((s) => s.setTorrents)
  const [search, setSearch] = useState('')
  const [replace, setReplace] = useState('')
  const [loading, setLoading] = useState(false)
  const isTouchUi = isMobile || isCoarse

  useEffect(() => {
    if (open) {
      setSearch('')
      setReplace('')
      setLoading(false)
    }
  }, [open])

  // 所有 Tracker 地址（去重排序，供输入补全）
  const allTrackers = useMemo(() => {
    const set = new Set<string>()
    for (const tor of torrents) for (const ts of tor.trackerStats ?? []) if (ts.announce) set.add(ts.announce)
    return Array.from(set).sort()
  }, [torrents])

  // 匹配的种子（Tracker URL 包含搜索词）
  const matched = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return []
    return torrents.filter((tor) =>
      (tor.trackerStats ?? []).some((ts) => ts.announce?.toLowerCase().includes(q)),
    )
  }, [torrents, search])

  const canSubmit = matched.length > 0 && !!replace.trim() && replace.trim() !== search.trim()

  const submit = async () => {
    if (!canSubmit) return
    setLoading(true)
    const q = search.trim().toLowerCase()
    const to = replace.trim()
    let success = 0
    let fail = 0
    try {
      for (const tor of matched) {
        const updated = (tor.trackerStats ?? [])
          .map((ts) => (ts.announce?.toLowerCase().includes(q) ? to : ts.announce))
          .filter((u): u is string => !!u)
        try {
          await torrentApi.update(tor.id, { trackerList: updated })
          success++
        } catch {
          fail++
        }
      }
      const list = await torrentApi.list().catch(() => null)
      if (list) setTorrents(list)
      if (fail === 0) toast.success(t('replaceTracker.replaceSuccess', { count: success }))
      else toast.warning(t('replaceTracker.replacePartial', { success, fail }))
      onClose()
    } catch {
      toast.error(t('replaceTracker.replaceFailed'))
    } finally {
      setLoading(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent
        className="sm:max-w-md"
        // 触屏浏览器把「输入框获得焦点」当成展开候选项的信号，datalist 里整库的 Tracker
        // 地址会连同软键盘一起盖住刚进的弹窗；手指端改成不自动聚焦（进来即看到完整弹窗，
        // 要打字再点搜索框），桌面端保留聚焦以便直接开搜
        onOpenAutoFocus={isTouchUi ? (e) => e.preventDefault() : undefined}
      >
        <DialogHeader>
          <DialogTitle>{t('replaceTracker.title')}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <p className="text-footnote text-gray-400">{t('replaceTracker.description')}</p>

          {/* 搜索 Tracker */}
          <div className="space-y-1">
            <label className="text-footnote font-medium text-gray-500">{t('replaceTracker.searchTracker')}</label>
            <Input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder={t('replaceTracker.searchPlaceholder')}
              className="h-8 text-footnote"
              list="rt-tracker-list"
            />
          </div>

          {/* 匹配的种子列表（数量恒显） */}
          {search.trim() && (
            <div className="rounded-lg border border-gray-200 dark:border-gray-700 p-2">
              <div className="flex items-center justify-between mb-1">
                <span className="text-footnote font-medium">{t('replaceTracker.matchedTorrents')}</span>
                <span className="h-4 px-1.5 rounded-full bg-primary/10 text-primary text-caption2 font-semibold flex items-center">
                  {matched.length}
                </span>
              </div>
              {matched.length > 0 ? (
                <div className="max-h-32 overflow-y-auto space-y-1">
                  {matched.map((tor) => (
                    <div key={tor.id} className="flex items-center gap-2 min-w-0 text-footnote">
                      <span
                        className="tm-chip shrink-0 px-1.5 py-px rounded-full border text-caption2"
                        style={cssVars({ '--chip': statusColor(tor) })}
                      >
                        {t(statusLabelKey(tor))}
                      </span>
                      <span className="min-w-0 flex-1 truncate text-gray-600 dark:text-gray-300" title={tor.name}>{tor.name}</span>
                    </div>
                  ))}
                </div>
              ) : (
                <div className="text-footnote text-gray-400 py-1">{t('replaceTracker.noMatchedTorrents')}</div>
              )}
            </div>
          )}

          {/* 替换为 */}
          {matched.length > 0 && (
            <div className="space-y-1">
              <label className="text-footnote font-medium text-gray-500">{t('replaceTracker.replaceWith')}</label>
              <Input
                value={replace}
                onChange={(e) => setReplace(e.target.value)}
                placeholder={t('replaceTracker.replacePlaceholder')}
                className="h-8 text-footnote"
                list="rt-tracker-list"
              />
            </div>
          )}

          <datalist id="rt-tracker-list">
            {allTrackers.map((u) => <option key={u} value={u} />)}
          </datalist>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>{t('common.cancel')}</Button>
          <Button disabled={!canSubmit || loading} onClick={submit}>
            {loading ? t('common.loading') : t('replaceTracker.replaceButton', { count: matched.length })}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// 批量删除已完成种子（按分享率/做种时间过滤）
export function BatchCleanDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t } = useTranslation()
  const torrents = useAppStore((s) => s.torrents)
  const actions = useTorrentActions()
  const [ratio, setRatio] = useState(1)
  const [hours, setHours] = useState(24)
  const [deleteData, setDeleteData] = useState(false)

  const matched = useMemo(
    () =>
      torrents.filter((x) => {
        if (x.percentDone < 1) return false
        if (x.doneDate <= 0) return false
        if (x.uploadRatio < ratio) return false
        const seedHours = (Date.now() / 1000 - x.doneDate) / 3600
        return seedHours >= hours
      }),
    [torrents, ratio, hours],
  )

  const run = () => {
    const ids = matched.map((x) => x.id)
    if (ids.length === 0) return
    void confirm({
      title: t('batchClean.confirmTitle'),
      content: `${t('batchClean.confirmContent')} (${ids.length})`,
      danger: true,
    }).then((ok) => {
      if (ok) void actions.remove(ids, deleteData)
    })
  }

  const row = 'flex items-center justify-between gap-3'

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('batchClean.title')}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className={row}>
            <span className="text-body">{t('batchClean.ratio')}</span>
            <Input
              type="number"
              min={0}
              step={0.5}
              className="h-8 w-28 text-footnote"
              value={String(ratio)}
              onChange={(e) => {
                const n = Number(e.target.value)
                if (!Number.isNaN(n)) setRatio(n)
              }}
            />
          </div>
          <div className={row}>
            <span className="text-body">{t('batchClean.hours')}</span>
            <Input
              type="number"
              min={0}
              className="h-8 w-28 text-footnote"
              value={String(hours)}
              onChange={(e) => {
                const n = Number(e.target.value)
                if (!Number.isNaN(n)) setHours(n)
              }}
            />
          </div>
          <div className={row}>
            <span className="text-body">{t('batchClean.deleteData')}</span>
            <Switch checked={deleteData} onCheckedChange={setDeleteData} />
          </div>
          <div className="text-body">
            {t('batchClean.matched')}: <b className="text-primary">{matched.length}</b>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>{t('common.cancel')}</Button>
          <Button variant="destructive" disabled={matched.length === 0} onClick={run}>{t('batchClean.run')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// 批量限速：对选中种子统一设置 / 解除下载与上传限速（可选连接数上限）
export function BatchLimitDialog({ open, ids, onClose }: { open: boolean; ids: number[]; onClose: () => void }) {
  const { t } = useTranslation()
  const [dlOn, setDlOn] = useState(false)
  const [dlValue, setDlValue] = useState<number | undefined>()
  const [ulOn, setUlOn] = useState(false)
  const [ulValue, setUlValue] = useState<number | undefined>()
  const [peerOn, setPeerOn] = useState(false)
  const [peerValue, setPeerValue] = useState<number | undefined>()
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (open) {
      setDlOn(false)
      setDlValue(undefined)
      setUlOn(false)
      setUlValue(undefined)
      setPeerOn(false)
      setPeerValue(undefined)
      setLoading(false)
    }
  }, [open])

  const requireValue = (on: boolean, value: number | undefined) => {
    if (on && (value == null || value < 1)) return false
    return true
  }

  const submit = async () => {
    if (ids.length === 0) return
    if (!requireValue(dlOn, dlValue) || !requireValue(ulOn, ulValue) || !requireValue(peerOn, peerValue)) {
      toast.warning(t('limits.tip'))
      return
    }
    setLoading(true)
    try {
      const body: Record<string, unknown> = {}
      // 勾选 → 设置单种限速；未勾选 → 解除该方向的单种限速（跟随全局限速）
      body.downloadLimited = dlOn
      if (dlOn) body.downloadLimit = Math.round(dlValue as number)
      body.uploadLimited = ulOn
      if (ulOn) body.uploadLimit = Math.round(ulValue as number)
      if (peerOn) body.peerLimit = Math.round(peerValue as number)
      await torrentApi.updateMany(ids, body)
      toast.success(t('toast.updated'))
      onClose()
    } catch {
      // 拦截器已提示
    } finally {
      setLoading(false)
    }
  }

  const row = 'flex items-center justify-between gap-3'
  const label = 'text-body text-gray-600 dark:text-gray-300'

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{`${t('action.batchLimit')} (${ids.length})`}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className={row}>
            <span className={label}>{t('limits.download')}</span>
            <div className="flex items-center gap-2">
              <Input
                type="number"
                min={1}
                step={1}
                disabled={!dlOn}
                className="h-8 w-24 text-footnote"
                value={dlValue === undefined ? '' : String(dlValue)}
                placeholder="KB/s"
                onChange={(e) => {
                  const raw = e.target.value
                  if (raw === '') { setDlValue(undefined); return }
                  const n = Number(raw)
                  if (!Number.isNaN(n)) setDlValue(n)
                }}
              />
              <Switch checked={dlOn} onCheckedChange={setDlOn} />
            </div>
          </div>
          <div className={row}>
            <span className={label}>{t('limits.upload')}</span>
            <div className="flex items-center gap-2">
              <Input
                type="number"
                min={1}
                step={1}
                disabled={!ulOn}
                className="h-8 w-24 text-footnote"
                value={ulValue === undefined ? '' : String(ulValue)}
                placeholder="KB/s"
                onChange={(e) => {
                  const raw = e.target.value
                  if (raw === '') { setUlValue(undefined); return }
                  const n = Number(raw)
                  if (!Number.isNaN(n)) setUlValue(n)
                }}
              />
              <Switch checked={ulOn} onCheckedChange={setUlOn} />
            </div>
          </div>
          <div className={row}>
            <span className={label}>{t('limits.peers')}</span>
            <div className="flex items-center gap-2">
              <Input
                type="number"
                min={1}
                step={1}
                disabled={!peerOn}
                className="h-8 w-24 text-footnote"
                value={peerValue === undefined ? '' : String(peerValue)}
                placeholder={t('limits.unlimitedHint')}
                onChange={(e) => {
                  const raw = e.target.value
                  if (raw === '') { setPeerValue(undefined); return }
                  const n = Number(raw)
                  if (!Number.isNaN(n)) setPeerValue(n)
                }}
              />
              <Switch checked={peerOn} onCheckedChange={setPeerOn} />
            </div>
          </div>
          <div className="text-footnote text-gray-400">{t('batch.limitHint')}</div>
          <div className="text-footnote text-gray-400">{t('limits.tip')}</div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>{t('common.cancel')}</Button>
          <Button disabled={loading || ids.length === 0} onClick={submit}>{loading ? t('common.loading') : t('common.confirm')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// 批量修改下载目录（含"仅移动已完成文件"选项）
export function BatchMoveDialog({ open, ids, onClose }: { open: boolean; ids: number[]; onClose: () => void }) {
  const { t } = useTranslation()
  const sem = useSemanticPath()
  const torrents = useAppStore((s) => s.torrents)
  const [path, setPath] = useState('')
  const [moveData, setMoveData] = useState(true)
  const [loading, setLoading] = useState(false)
  const [history, setHistory] = useState<string[]>([])

  useEffect(() => {
    if (open) {
      setPath('')
      setMoveData(true)
      try {
        const h = localStorage.getItem(DIR_HISTORY_KEY)
        if (h) {
          const parsed = JSON.parse(h) as string[]
          if (Array.isArray(parsed)) setHistory(parsed.slice(0, 8))
        }
      } catch {}
    }
  }, [open])

  const selected = useMemo(() => torrents.filter((x) => ids.includes(x.id)), [torrents, ids])
  const currentDir = selected[0]?.downloadDir

  const submit = async () => {
    if (!path.trim()) {
      toast.warning(t('batch.moveHint'))
      return
    }
    setLoading(true)
    try {
      await torrentApi.moveMany(ids, path.trim(), moveData)
      // 记录历史目录
      const h = [path.trim(), ...history.filter((x) => x !== path.trim())].slice(0, 8)
      localStorage.setItem(DIR_HISTORY_KEY, JSON.stringify(h))
      toast.success(t('toast.updated'))
      onClose()
    } catch {
      // 拦截器已提示
    } finally {
      setLoading(false)
    }
  }

  const row = 'flex items-center justify-between py-1.5'
  const label = 'text-body text-gray-600 dark:text-gray-300'

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('action.changePath')} ({ids.length})</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          {currentDir && (
            <div className="text-footnote text-gray-500">
              {t('detail.downloadDir')}: <span className="tm-mono" title={currentDir}>{sem(currentDir)}</span>
            </div>
          )}
          <div className="space-y-1">
            <Input
              value={path}
              onChange={(e) => setPath(e.target.value)}
              placeholder="/downloads/xxx"
              className="h-8 text-footnote"
              list="tm-dir-history"
            />
            <datalist id="tm-dir-history">
              {history.map((h) => <option key={h} value={h} />)}
            </datalist>
            <ClearDirHistory count={history.length} onClear={() => setHistory([])} />
          </div>
          <div className={row}>
            <span className={label}>{t('action.moveData')}</span>
            <Switch checked={moveData} onCheckedChange={setMoveData} />
          </div>
          <div className="text-footnote text-gray-400">
            {moveData ? t('batch.moveAllHint') : t('batch.moveCompletedOnlyHint')}
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>{t('common.cancel')}</Button>
          <Button disabled={loading} onClick={submit}>{loading ? t('common.loading') : t('common.confirm')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// 删除种子（可选同时删除本地数据 / 仅当无做种时删除）
export function RemoveTorrentDialog({ open, ids, onClose }: { open: boolean; ids: number[]; onClose: () => void }) {
  const { t } = useTranslation()
  const actions = useTorrentActions()
  const torrents = useAppStore((s) => s.torrents)
  const [deleteData, setDeleteData] = useState(false)
  const [onlyNonSeeding, setOnlyNonSeeding] = useState(false)

  // 对话框打开时重置状态，避免上次操作残留
  useEffect(() => {
    if (open) {
      setDeleteData(false)
      setOnlyNonSeeding(false)
    }
  }, [open])
  const [loading, setLoading] = useState(false)

  const selected = useMemo(() => torrents.filter((x) => ids.includes(x.id)), [torrents, ids])
  const seedingCount = useMemo(() => selected.filter((x) => x.status === 6).length, [selected])

  const submit = async () => {
    setLoading(true)
    try {
      let target = ids
      if (onlyNonSeeding) {
        target = selected.filter((x) => x.status !== 6).map((x) => x.id)
        if (target.length === 0) {
          toast.warning(t('confirm.allSeeding'))
          return
        }
      }
      // 失败时不关闭弹窗，让用户可以改条件重试；成功提示由 actions 统一给出
      const ok = await actions.remove(target, deleteData)
      if (!ok) return
      onClose()
    } catch {
      // 拦截器已提示
    } finally {
      setLoading(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('confirm.removeTitle')}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className="text-body">{t('confirm.removeContent')}</div>
          {selected.length > 0 && (
            <div className="text-footnote text-gray-500 dark:text-gray-400">
              {t('confirm.selectedCount')}: {selected.length} · {t('confirm.seedingCount')}: {seedingCount}
            </div>
          )}
          {selected.length > 0 && (
            <div className="rounded-lg border border-gray-200 dark:border-gray-700 p-2">
              <div className="max-h-32 overflow-y-auto space-y-0.5">
                {selected.map((tor) => (
                  <div key={tor.id} className="text-footnote text-gray-600 dark:text-gray-300 truncate" title={tor.name}>
                    {tor.name}
                  </div>
                ))}
              </div>
            </div>
          )}
          <label className="flex items-center gap-2 text-body text-gray-600 dark:text-gray-300 cursor-pointer">
            <input type="checkbox" checked={deleteData} onChange={(e) => setDeleteData(e.target.checked)} />
            {t('confirm.removeWithData')}
          </label>
          <label className="flex items-center gap-2 text-body text-gray-600 dark:text-gray-300 cursor-pointer">
            <input type="checkbox" checked={onlyNonSeeding} onChange={(e) => setOnlyNonSeeding(e.target.checked)} />
            {t('confirm.onlyNonSeeding')}
          </label>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>{t('common.cancel')}</Button>
          <Button variant="destructive" disabled={loading} onClick={submit}>{loading ? t('common.loading') : t('common.delete')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
