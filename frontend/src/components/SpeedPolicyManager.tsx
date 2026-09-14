import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { speedPolicyApi } from '@/api/torrent'
import type { SpeedPolicyGuard, SpeedPolicyResult, SpeedPolicyRule } from '@/types'
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
import { Switch } from '@/components/ui/switch'
import TagInput from '@/components/TagInput'

const emptyRule = (preset?: { sites?: string[]; labels?: string[] } | null): SpeedPolicyRule => ({
  id: '',
  name: '',
  enabled: true,
  downLimit: 0,
  upLimit: 0,
  sites: preset?.sites ?? [],
  labels: preset?.labels ?? [],
  nameMatch: '',
})

const emptyGuard = (): SpeedPolicyGuard => ({ enforce: false })

// 组内总限速管理：为站点 / 标签分组的种子设置共享带宽上限
export function SpeedPolicyManager({ open, onClose, preset }: {
  open: boolean
  onClose: () => void
  preset?: { sites: string[]; labels: string[] } | null
}) {
  const { t, i18n } = useTranslation()
  const allTorrents = useAppStore((s) => s.torrents)
  const torrentSites = useAppStore((s) => s.torrentSites)
  const [rules, setRules] = useState<SpeedPolicyRule[]>([])
  const [guard, setGuard] = useState<SpeedPolicyGuard>(emptyGuard())
  const [editing, setEditing] = useState<SpeedPolicyRule | null>(null)
  const [saving, setSaving] = useState(false)
  const [running, setRunning] = useState(false)
  // 最近一轮「立即应用」结果，用于提示
  const [lastRun, setLastRun] = useState<SpeedPolicyResult | null>(null)

  const allLabels = useMemo(
    () => Array.from(new Set(allTorrents.flatMap((x) => x.labels ?? []))).sort((a, b) => a.localeCompare(b, i18n.language)),
    [allTorrents, i18n.language],
  )
  const allSites = useMemo(
    () => Array.from(new Set(Object.values(torrentSites).flat())).sort((a, b) => a.localeCompare(b, i18n.language)),
    [torrentSites, i18n.language],
  )

  const load = useCallback(async () => {
    const res = await speedPolicyApi.list()
    setRules(res.rules ?? [])
    setGuard(res.guard ?? emptyGuard())
  }, [])

  useEffect(() => {
    if (open) {
      load().catch(() => {})
      setLastRun(null)
      // 从侧边栏分组右键进入时，预填一条新规则
      if (preset && (preset.sites.length > 0 || preset.labels.length > 0)) {
        setEditing(emptyRule(preset))
      } else {
        setEditing(null)
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, load])

  const save = async () => {
    if (!editing) return
    if (editing.downLimit <= 0 && editing.upLimit <= 0) {
      toast.warning(t('speedPolicy.limitRequired'))
      return
    }
    setSaving(true)
    try {
      await speedPolicyApi.save({
        ...editing,
        downLimit: Math.max(0, Math.round(editing.downLimit)),
        upLimit: Math.max(0, Math.round(editing.upLimit)),
        sites: editing.sites.filter(Boolean),
        labels: editing.labels.filter(Boolean),
      })
      toast.success(t('toast.updated'))
      setEditing(null)
      await load()
    } catch {
      // 拦截器已提示
    } finally {
      setSaving(false)
    }
  }

  const toggleRule = (rule: SpeedPolicyRule, enabled: boolean) => {
    void speedPolicyApi.save({ ...rule, enabled }).then(load).catch(() => {})
  }

  const remove = async (id: string) => {
    try {
      await speedPolicyApi.remove(id)
      toast.success(t('toast.removed'))
      await load()
    } catch {
      // 拦截器已提示
    }
  }

  const saveGuard = async (enforce: boolean) => {
    const g = { ...guard, enforce }
    setGuard(g)
    try {
      await speedPolicyApi.saveGuard(g)
      toast.success(t('toast.updated'))
    } catch {
      // 拦截器已提示
    }
  }

  const runNow = async () => {
    setRunning(true)
    try {
      const res = await speedPolicyApi.run()
      setLastRun(res.result)
      toast.success(runSummary(res.result))
      await load()
    } catch {
      // 拦截器已提示
    } finally {
      setRunning(false)
    }
  }

  const runSummary = (r: SpeedPolicyResult) => {
    const parts: string[] = []
    if (r.matched) parts.push(`${t('speedPolicy.managed')} ${r.matched}`)
    if (r.applied) parts.push(`${t('speedPolicy.applied')} ${r.applied}`)
    if (r.released) parts.push(`${t('speedPolicy.released')} ${r.released}`)
    if (r.failed) parts.push(`${t('speedPolicy.failed')} ${r.failed}`)
    return parts.length ? parts.join(' · ') : t('speedPolicy.noHit')
  }

  const row = 'flex items-center justify-between gap-2 py-1.5'
  const label = 'text-body text-gray-600 dark:text-gray-300'
  const sectionTitle = 'text-footnote font-medium mb-2 text-gray-500'
  // 规则摘要：↓100 / ↑50 KB/s，0 的方向不展示
  const capsText = (r: SpeedPolicyRule) => {
    const parts: string[] = []
    if (r.downLimit > 0) parts.push(`↓${r.downLimit}`)
    if (r.upLimit > 0) parts.push(`↑${r.upLimit}`)
    return parts.length ? parts.join(' / ') + ' KB/s' : t('speedPolicy.unlimitedHint')
  }

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="sm:max-w-[620px] h-[80dvh] flex flex-col p-0">
        <DialogHeader className="px-4 pt-4 pb-2 border-b border-gray-200/40 dark:border-gray-700/30">
          <DialogTitle>{t('speedPolicy.title')}</DialogTitle>
        </DialogHeader>
        <div className="flex-1 min-h-0 overflow-y-auto px-4 py-3 space-y-4">
          <p className="text-footnote text-gray-500 leading-relaxed">{t('speedPolicy.intro')}</p>

          {/* 引擎开关 */}
          <div className="rounded-xl glass-section p-3 space-y-1">
            <div className={row}>
              <div>
                <span className={label}>{t('speedPolicy.enforce')}</span>
                <p className="text-caption1 text-gray-400 mt-0.5">{t('speedPolicy.enforceHint')}</p>
              </div>
              <Switch checked={guard.enforce} onCheckedChange={(v) => void saveGuard(v)} />
            </div>
            <div className="flex items-center justify-end gap-2 pt-1">
              <Button size="sm" className="h-8 text-footnote" disabled={running || !guard.enforce} onClick={runNow}>
                {running ? t('common.loading') : t('speedPolicy.runNow')}
              </Button>
            </div>
            {lastRun && <div className="text-footnote text-gray-500 text-right">{runSummary(lastRun)}</div>}
          </div>

          {/* 规则列表 */}
          <div className="space-y-2">
            <div className={sectionTitle}>{t('speedPolicy.rules')}</div>
            {rules.length === 0 && (
              <div className="py-6 text-center text-body text-gray-400">{t('speedPolicy.empty')}</div>
            )}
            {rules.map((rule) => (
              <div key={rule.id} className="rounded-xl glass-section p-3">
                <div className="flex items-center gap-2 mb-1">
                  <Switch checked={rule.enabled} onCheckedChange={(v) => toggleRule(rule, v)} />
                  <span className="font-medium text-body flex-1 truncate">{rule.name || t('speedPolicy.untitled')}</span>
                  <span className="text-footnote shrink-0 text-gray-500 tm-mono">
                    {capsText(rule)}
                  </span>
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
                  {rule.sites.length > 0 && <span>{t('speedPolicy.sites')}: {rule.sites.join(', ')}</span>}
                  {rule.labels.length > 0 && <span>{t('speedPolicy.labels')}: {rule.labels.join(', ')}</span>}
                  {rule.nameMatch && <span>{t('speedPolicy.nameMatch')}: {rule.nameMatch}</span>}
                  {rule.sites.length === 0 && rule.labels.length === 0 && !rule.nameMatch && (
                    <span className="text-gray-400">{t('speedPolicy.allScope')}</span>
                  )}
                </div>
              </div>
            ))}
            {!editing && (
              <Button variant="outline" className="w-full h-8 text-footnote" onClick={() => setEditing(emptyRule())}>
                + {t('speedPolicy.addRule')}
              </Button>
            )}
          </div>

          {/* 编辑表单 */}
          {editing && (
            <div className="rounded-xl border border-primary/30 p-3 space-y-2">
              <div className="text-body font-medium">{editing.id ? t('speedPolicy.editRule') : t('speedPolicy.addRule')}</div>
              <div className="grid grid-cols-2 gap-2">
                <Input value={editing.name} onChange={(e) => setEditing({ ...editing, name: e.target.value })} placeholder={t('speedPolicy.ruleName')} className="h-8 text-footnote" />
                <Input value={editing.nameMatch} onChange={(e) => setEditing({ ...editing, nameMatch: e.target.value })} placeholder={t('speedPolicy.nameMatchPlaceholder')} className="h-8 text-footnote" />
              </div>
              <div className="grid grid-cols-2 gap-2">
                <TagInput value={editing.sites} onChange={(v) => setEditing({ ...editing, sites: v })} suggestions={allSites} placeholder={t('speedPolicy.sites')} className="text-footnote" />
                <TagInput value={editing.labels} onChange={(v) => setEditing({ ...editing, labels: v })} suggestions={allLabels} placeholder={t('speedPolicy.labels')} className="text-footnote" />
              </div>
              <div className="text-footnote text-gray-400">{t('speedPolicy.scopeHint')}</div>
              <div className="grid grid-cols-2 gap-2">
                <div>
                  <div className="text-footnote text-gray-500 mb-1">{t('speedPolicy.downLimit')} (KB/s)</div>
                  <Input
                    type="number"
                    min={0}
                    step={1}
                    className="h-8 text-footnote"
                    value={editing.downLimit > 0 ? String(editing.downLimit) : ''}
                    placeholder={t('speedPolicy.unlimitedHint')}
                    onChange={(e) => {
                      const n = Number(e.target.value)
                      if (!Number.isNaN(n)) setEditing({ ...editing, downLimit: n })
                    }}
                  />
                </div>
                <div>
                  <div className="text-footnote text-gray-500 mb-1">{t('speedPolicy.upLimit')} (KB/s)</div>
                  <Input
                    type="number"
                    min={0}
                    step={1}
                    className="h-8 text-footnote"
                    value={editing.upLimit > 0 ? String(editing.upLimit) : ''}
                    placeholder={t('speedPolicy.unlimitedHint')}
                    onChange={(e) => {
                      const n = Number(e.target.value)
                      if (!Number.isNaN(n)) setEditing({ ...editing, upLimit: n })
                    }}
                  />
                </div>
              </div>
              <div className="text-footnote text-gray-400">{t('speedPolicy.unlimitedHintNote')}</div>
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
        </div>
        <DialogFooter className="px-4 pb-4">
          <Button variant="outline" onClick={onClose}>{t('common.close')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
