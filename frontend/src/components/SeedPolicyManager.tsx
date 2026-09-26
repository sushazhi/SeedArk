import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { seedPolicyApi } from '@/api/torrent'
import type { SeedPolicyGuard, SeedPolicyLog, SeedPolicyReasonPart, SeedPolicyResult, SeedPolicyRule } from '@/types'
import { useAppStore } from '@/stores/appStore'
import { confirm } from '@/lib/confirm'
import { toast } from '@/lib/toast'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import TagInput from '@/components/TagInput'

const emptyRule = (): SeedPolicyRule => ({
  id: '',
  name: '',
  enabled: true,
  sites: [],
  labels: [],
  nameMatch: '',
  minRatio: 5,
  minSeedDays: 0,
  minUploadGB: 0,
  action: 'pause',
})

const emptyGuard = (): SeedPolicyGuard => ({
  enforce: false,
  minSeedHours: 0,
  excludeSites: [],
  excludeLabels: [],
})

// 删除类动作统一走标红与保存前确认
const isDelete = (action: string) => action === 'delete' || action === 'deleteData'

// 数字输入：空串与非法值不回写，避免把「清空」当成 0 立即保存
function NumInput({ value, min, step, className, onChange }: {
  value: number
  min?: number
  step?: number
  className?: string
  onChange: (v: number) => void
}) {
  return (
    <Input
      type="number"
      min={min}
      step={step}
      className={className}
      value={Number.isFinite(value) ? String(value) : '0'}
      onChange={(e) => {
        const n = Number(e.target.value)
        if (!Number.isNaN(n)) onChange(n)
      }}
    />
  )
}

// 做种策略：按站点设置分享率 / 做种时长 / 上传量目标，达标后暂停或删除种子
export function SeedPolicyManager({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t, i18n } = useTranslation()
  const [rules, setRules] = useState<SeedPolicyRule[]>([])
  const [guard, setGuard] = useState<SeedPolicyGuard>(emptyGuard())
  const [logs, setLogs] = useState<SeedPolicyLog[]>([])
  const [editing, setEditing] = useState<SeedPolicyRule | null>(null)
  const [saving, setSaving] = useState(false)
  const [running, setRunning] = useState(false)
  const allTorrents = useAppStore((s) => s.torrents)
  // 站点候选与自动文件管理/侧边栏同一来源（中文站点名）：
  // 引擎侧已把站点名纳入匹配键，这里不再用 tracker 主机名，否则与另两处对不上
  const torrentSites = useAppStore((s) => s.torrentSites)

  const allLabels = useMemo(
    () => Array.from(new Set(allTorrents.flatMap((x) => x.labels ?? []))).sort((a, b) => a.localeCompare(b, i18n.language)),
    [allTorrents, i18n.language],
  )
  const allSites = useMemo(
    () => Array.from(new Set(Object.values(torrentSites).flat())).sort((a, b) => a.localeCompare(b, i18n.language)),
    [torrentSites, i18n.language],
  )

  const load = useCallback(async () => {
    const res = await seedPolicyApi.list()
    setRules(res.rules ?? [])
    // 安全保护未保存过时数组为 null，需兜底后才能 join / 受控输入
    const g = res.guard ?? emptyGuard()
    setGuard({ ...g, excludeSites: g.excludeSites ?? [], excludeLabels: g.excludeLabels ?? [] })
    setLogs(res.logs ?? [])
  }, [])

  useEffect(() => {
    if (open) {
      load().catch(() => {})
      setEditing(null)
    }
  }, [open, load])

  // 面板打开期间低频轮询：后台引擎每 60s 评估一轮，
  // 新产生的执行记录与规则开关状态要能跟着出现，而不是关掉重开才看得到
  useEffect(() => {
    if (!open) return
    const timer = window.setInterval(() => { load().catch(() => {}) }, 15000)
    return () => window.clearInterval(timer)
  }, [open, load])

  // 目标值与达标依据都按当前语言组装：后端只回 kind/actual/target 数值
  const goalText = (kind: 'ratio' | 'days' | 'upload', v: number) => {
    const num = kind === 'ratio' ? v.toFixed(2) : v.toFixed(1)
    if (kind === 'days') return t('seedPolicy.goalDays', { v: num })
    if (kind === 'upload') return t('seedPolicy.goalUpload', { v: num })
    return t('seedPolicy.goalRatio', { v: num })
  }

  const reasonText = (parts: SeedPolicyReasonPart[]) => parts.map((p) => {
    const actual = p.kind === 'ratio' ? p.actual.toFixed(2) : p.actual.toFixed(1)
    const target = p.kind === 'ratio' ? p.target.toFixed(2) : p.target.toFixed(1)
    if (p.kind === 'days') return t('seedPolicy.reasonDays', { actual, target })
    if (p.kind === 'upload') return t('seedPolicy.reasonUpload', { actual, target })
    return t('seedPolicy.reasonRatio', { actual, target })
  }).join(' · ')

  const summary = (r: SeedPolicyResult) => {
    const parts: string[] = []
    if (r.previewed) parts.push(`${t('seedPolicy.previewed')} ${r.previewed}`)
    if (r.paused) parts.push(`${t('seedPolicy.paused')} ${r.paused}`)
    if (r.deleted) parts.push(`${t('seedPolicy.deleted')} ${r.deleted}`)
    if (r.failed) parts.push(`${t('seedPolicy.failed')} ${r.failed}`)
    return parts.length ? parts.join(' · ') : t('seedPolicy.noHit')
  }

  const save = async () => {
    if (!editing) return
    if (editing.minRatio <= 0 && editing.minSeedDays <= 0 && editing.minUploadGB <= 0) {
      toast.warning(t('seedPolicy.conditionRequired'))
      return
    }
    // 删除不可逆，保存前单独确认一次
    if (isDelete(editing.action)) {
      const ok = await confirm({
        title: t('seedPolicy.deleteRuleWarn'),
        content: editing.action === 'deleteData' ? t('seedPolicy.deleteDataWarn') : t('seedPolicy.deleteKeepDataHint'),
        danger: true,
      })
      if (!ok) return
    }
    setSaving(true)
    try {
      await seedPolicyApi.save({ ...editing, sites: editing.sites.filter(Boolean), labels: editing.labels.filter(Boolean) })
      toast.success(t('toast.updated'))
      setEditing(null)
      await load()
    } catch {
      // 拦截器已提示
    } finally {
      setSaving(false)
    }
  }

  const toggleRule = (rule: SeedPolicyRule, enabled: boolean) => {
    void seedPolicyApi.save({ ...rule, enabled }).then(load).catch(() => {})
  }

  const remove = async (id: string) => {
    try {
      await seedPolicyApi.remove(id)
      toast.success(t('toast.removed'))
      await load()
    } catch {
      // 拦截器已提示
    }
  }

  const saveGuard = async () => {
    try {
      await seedPolicyApi.saveGuard({
        ...guard,
        excludeSites: guard.excludeSites.filter(Boolean),
        excludeLabels: guard.excludeLabels.filter(Boolean),
      })
      toast.success(t('toast.updated'))
      await load()
    } catch {
      // 拦截器已提示
    }
  }

  const runNow = async () => {
    setRunning(true)
    try {
      const res = await seedPolicyApi.run()
      toast.success(summary(res.result))
      if (res.result.persistFailed) toast.warning(t('seedPolicy.persistFailed'))
      await load()
    } catch {
      // 拦截器已提示
    } finally {
      setRunning(false)
    }
  }

  const resetProcessed = async () => {
    const ok = await confirm({ title: t('seedPolicy.reset'), content: t('seedPolicy.resetHint'), danger: true })
    if (!ok) return
    try {
      const res = await seedPolicyApi.reset()
      toast.success(t('seedPolicy.resetDone', { n: res.cleared }))
      await load()
    } catch {
      // 拦截器已提示
    }
  }

  const clearLogs = async () => {
    try {
      await seedPolicyApi.clearLogs()
      await load()
    } catch {
      // 拦截器已提示
    }
  }

  const row = 'flex items-center justify-between gap-2 py-1.5'
  const label = 'text-body text-gray-600 dark:text-gray-300'
  const sectionTitle = 'text-footnote font-medium mb-2 text-gray-500'
  const actionText = (a: string) =>
    a === 'deleteData' ? t('seedPolicy.actionDeleteData')
      : a === 'delete' ? t('seedPolicy.actionDelete')
      : a === 'pause' ? t('seedPolicy.actionPause')
      : a // 历史记录里可能有已下线的动作，原样展示

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="sm:max-w-[680px] h-[82dvh] flex flex-col p-0">
        <DialogHeader className="px-4 pt-4 pb-2 border-b border-gray-200/40 dark:border-gray-700/30">
          <DialogTitle>{t('seedPolicy.title')}</DialogTitle>
        </DialogHeader>
        {/* flex-1 min-h-0：没有 min-h-0 时该 flex 项会被内容撑到超出固定高度，
            overflow-y-auto 失效（自身不溢出），下方的编辑表单/新规则会落到可视区外 */}
        <div className="flex-1 min-h-0 overflow-y-auto px-4 py-3 space-y-4">
          <p className="text-footnote text-gray-500 leading-relaxed">{t('seedPolicy.intro')}</p>

          {/* 安全保护：对所有规则生效的安全下限 */}
          <div className="rounded-xl glass-section p-3 space-y-1">
            <div className={sectionTitle}>{t('seedPolicy.guard')}</div>
            <div className={row}>
              <span className={label}>{t('seedPolicy.enforce')}</span>
              <Switch checked={guard.enforce} onCheckedChange={(v) => setGuard({ ...guard, enforce: v })} />
            </div>
            <div className={row}>
              <span className={label}>{t('seedPolicy.minSeedHours')}</span>
              <NumInput value={guard.minSeedHours} min={0} step={1} className="w-24 h-8 text-footnote" onChange={(v) => setGuard({ ...guard, minSeedHours: v })} />
            </div>
            <TagInput
              value={guard.excludeSites}
              onChange={(v) => setGuard({ ...guard, excludeSites: v })}
              suggestions={allSites}
              placeholder={t('seedPolicy.excludeSites')}
              className="text-footnote"
            />
            <TagInput
              value={guard.excludeLabels}
              onChange={(v) => setGuard({ ...guard, excludeLabels: v })}
              suggestions={allLabels}
              placeholder={t('seedPolicy.excludeLabels')}
              className="text-footnote"
            />
            <div className="flex items-center justify-between gap-2 pt-1">
              <span className="text-footnote text-gray-400">{t('seedPolicy.guardHint')}</span>
              <Button size="sm" variant="outline" className="h-8 text-footnote shrink-0" onClick={saveGuard}>{t('seedPolicy.saveGuard')}</Button>
            </div>
          </div>

          {/* 规则列表 */}
          <div className="space-y-2">
            <div className={sectionTitle}>{t('seedPolicy.rules')}</div>
            {rules.length === 0 && (
              <div className="py-6 text-center text-body text-gray-400">{t('seedPolicy.empty')}</div>
            )}
            {rules.map((rule) => (
              <div key={rule.id} className="rounded-xl glass-section p-3">
                <div className="flex items-center gap-2 mb-1">
                  <Switch checked={rule.enabled} onCheckedChange={(v) => toggleRule(rule, v)} />
                  <span className="font-medium text-body flex-1 truncate">{rule.name || t('seedPolicy.untitled')}</span>
                  <span className={`text-footnote shrink-0 ${isDelete(rule.action) ? 'text-red-500' : 'text-gray-500'}`}>{actionText(rule.action)}</span>
                  <Button size="sm" variant="outline" className="h-8 text-footnote" onClick={() => setEditing({ ...rule })}>{t('common.edit')}</Button>
                  <Button
                    size="sm"
                    variant="destructive"
                    className="h-8 text-footnote"
                    onClick={() => {
                      void confirm({ title: t('common.confirm'), danger: true }).then((ok) => {
                        if (ok) void remove(rule.id)
                      })
                    }}
                  >
                    {t('common.delete')}
                  </Button>
                </div>
                <div className="text-footnote text-gray-500 flex flex-wrap gap-x-3 gap-y-0.5">
                  {rule.minRatio > 0 && <span>{goalText('ratio', rule.minRatio)}</span>}
                  {rule.minSeedDays > 0 && <span>{goalText('days', rule.minSeedDays)}</span>}
                  {rule.minUploadGB > 0 && <span>{goalText('upload', rule.minUploadGB)}</span>}
                  {rule.sites.length > 0 && <span>{t('seedPolicy.sites')}: {rule.sites.join(', ')}</span>}
                  {rule.labels.length > 0 && <span>{t('seedPolicy.labels')}: {rule.labels.join(', ')}</span>}
                  {rule.nameMatch && <span>{t('seedPolicy.nameMatch')}: {rule.nameMatch}</span>}
                </div>
              </div>
            ))}
            {!editing && (
              <Button variant="outline" className="w-full h-8 text-footnote" onClick={() => setEditing(emptyRule())}>
                + {t('seedPolicy.addRule')}
              </Button>
            )}
          </div>

          {/* 编辑表单 */}
          {editing && (
            <div className="rounded-xl border border-primary/30 p-3 space-y-2">
              <div className="text-body font-medium">{editing.id ? t('seedPolicy.editRule') : t('seedPolicy.addRule')}</div>
              <div className="grid grid-cols-2 gap-2">
                <Input value={editing.name} onChange={(e) => setEditing({ ...editing, name: e.target.value })} placeholder={t('seedPolicy.ruleName')} className="h-8 text-footnote" />
                <Input value={editing.nameMatch} onChange={(e) => setEditing({ ...editing, nameMatch: e.target.value })} placeholder={t('seedPolicy.nameMatchPlaceholder')} className="h-8 text-footnote" />
              </div>
              <div className="grid grid-cols-2 gap-2">
                <TagInput value={editing.sites} onChange={(v) => setEditing({ ...editing, sites: v })} suggestions={allSites} placeholder={t('seedPolicy.sites')} className="text-footnote" />
                <TagInput value={editing.labels} onChange={(v) => setEditing({ ...editing, labels: v })} suggestions={allLabels} placeholder={t('seedPolicy.labels')} className="text-footnote" />
              </div>
              <div className="text-footnote text-gray-400">{t('seedPolicy.sitesHint')}</div>
              <div className="grid grid-cols-3 gap-2">
                <div>
                  <div className="text-footnote text-gray-500 mb-1">{t('seedPolicy.minRatio')}</div>
                  <NumInput value={editing.minRatio} min={0} step={0.1} className="h-8 text-footnote" onChange={(v) => setEditing({ ...editing, minRatio: v })} />
                </div>
                <div>
                  <div className="text-footnote text-gray-500 mb-1">{t('seedPolicy.minSeedDays')}</div>
                  <NumInput value={editing.minSeedDays} min={0} step={1} className="h-8 text-footnote" onChange={(v) => setEditing({ ...editing, minSeedDays: v })} />
                </div>
                <div>
                  <div className="text-footnote text-gray-500 mb-1">{t('seedPolicy.minUploadGB')}</div>
                  <NumInput value={editing.minUploadGB} min={0} step={1} className="h-8 text-footnote" onChange={(v) => setEditing({ ...editing, minUploadGB: v })} />
                </div>
              </div>
              <div className={row}>
                <span className={label}>{t('seedPolicy.action')}</span>
                <Select value={editing.action} onValueChange={(v) => setEditing({ ...editing, action: v as SeedPolicyRule['action'] })}>
                  <SelectTrigger className="h-8 w-40 text-footnote">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent className="glass-panel-solid">
                    <SelectItem value="pause">{t('seedPolicy.actionPause')}</SelectItem>
                    <SelectItem value="delete">{t('seedPolicy.actionDelete')}</SelectItem>
                    <SelectItem value="deleteData">{t('seedPolicy.actionDeleteData')}</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              {isDelete(editing.action) && (
                <div className="rounded-lg bg-red-500/10 border border-red-500/30 p-2 text-footnote text-red-600 dark:text-red-400">
                  {editing.action === 'deleteData' ? t('seedPolicy.deleteDataWarn') : t('seedPolicy.deleteKeepDataHint')}
                </div>
              )}
              <div className={row}>
                <span className={label}>{t('common.enabled')}</span>
                <Switch checked={editing.enabled} onCheckedChange={(v) => setEditing({ ...editing, enabled: v })} />
              </div>
              <div className="flex justify-end gap-2 pt-1">
                <Button size="sm" variant="outline" className="h-8 text-footnote" onClick={() => setEditing(null)}>{t('common.cancel')}</Button>
                <Button size="sm" className="h-8 text-footnote" disabled={saving} onClick={save}>{saving ? t('common.loading') : t('common.save')}</Button>
              </div>
            </div>
          )}

          {/* 执行记录 */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <div className={sectionTitle}>{t('seedPolicy.logs')}</div>
              {logs.length > 0 && (
                <Button size="sm" variant="ghost" className="h-8 text-footnote" onClick={clearLogs}>{t('seedPolicy.clearLogs')}</Button>
              )}
            </div>
            {logs.length === 0 && (
              <div className="py-4 text-center text-footnote text-gray-400">{t('seedPolicy.noLogs')}</div>
            )}
            <div className="space-y-1">
              {logs.map((l, i) => (
                <div key={`${l.time}-${i}`} className="flex items-baseline gap-2 text-footnote border-b border-gray-100 dark:border-gray-700/40 pb-1">
                  <span className="text-gray-400 shrink-0 tabular-nums">{new Date(l.time * 1000).toLocaleString(i18n.language)}</span>
                  {l.dryRun && <span className="text-amber-600 dark:text-amber-400 shrink-0">{t('seedPolicy.previewTag')}</span>}
                  <span className={isDelete(l.action) ? 'text-red-500 shrink-0' : 'shrink-0'}>{actionText(l.action)}</span>
                  <span className="truncate flex-1" title={l.torrent}>{l.torrent}</span>
                  <span className="text-gray-400 truncate max-w-[40%]" title={`${l.site} · ${l.rule}`}>{reasonText(l.reason ?? []) || l.site}</span>
                </div>
              ))}
            </div>
          </div>

          <div className="flex gap-2">
            <Button className="flex-1 h-8 text-footnote" disabled={running} onClick={runNow}>
              {running ? t('common.loading') : t('seedPolicy.runNow')}
            </Button>
            <Button variant="outline" className="flex-1 h-8 text-footnote" onClick={resetProcessed}>
              {t('seedPolicy.reset')}
            </Button>
          </div>
        </div>
        <DialogFooter className="px-4 pb-4">
          <Button variant="outline" onClick={onClose}>{t('common.close')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
