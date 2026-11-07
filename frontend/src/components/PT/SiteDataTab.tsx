import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertTriangle, Plus, RefreshCw, Save, Trash2 } from 'lucide-react'
import { ptApi } from '@/api/pt'
import type { PTExam, PTExamItem, PTSettings, PTSnapshot, PTSnapshotDiff } from '@/types/pt'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import { formatDate } from '@/utils/format'

// 「站点数据」页签：手工录入站点侧数字（快照）、看本地 vs 站点偏差、录入考核目标与进度、PT 全局阈值。
//
// 为什么全要手工：这些数字只存在于站点账号页，而本面板按设计不登录站点。
// 参考 PTMyBonusCalc 的先例——它同样要求首次使用时去站点抄一次参数。
// 面板只负责「存快照 + 算差值 + 标口径」，不据此推断站点行为。

function useLoader<T>(fn: () => Promise<T>) {
  const [state, setState] = useState<{ loading: boolean; error: boolean; data: T | null }>({
    loading: true,
    error: false,
    data: null,
  })
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
  }, [nonce])
  return { ...state, reload: () => setNonce((n) => n + 1) }
}

const GB = 1 << 30

// sumAGap 本地复刻 ΣA 与站点页 (A = xx.x) 的差，写作百分比（正数=本地偏高）。
// 为什么比的是 A 而不是 B：曲线项会把 A 的差压小（atan 有饱和，压得最狠），
// 用 B 对账会把「参数录错了一点」看成「大致对得上」。未抄 A 值则给「—」。
function sumAGap(d: PTSnapshotDiff): string {
  const observed = d.bonusASumObserved ?? 0
  if (observed <= 0) return '—'
  const local = d.bonusSumA ?? 0
  return (((local - observed) / observed) * 100).toFixed(1)
}

// ---------- 设置 ----------

function SettingsPanel() {
  const { t } = useTranslation()
  const { loading, data, reload } = useLoader<PTSettings>(() => ptApi.settings())
  const [draft, setDraft] = useState<PTSettings | null>(null)
  const [saved, setSaved] = useState(false)
  useEffect(() => {
    if (data) setDraft(data)
  }, [data])
  if (loading || !draft) return null
  const save = async () => {
    await ptApi.saveSettings(draft)
    setSaved(true)
    reload()
    setTimeout(() => setSaved(false), 2000)
  }
  return (
    <div className="glass-subcard rounded-panel p-3">
      <div className="mb-2 text-footnote font-medium">{t('pt.site.settings')}</div>
      <div className="space-y-2">
        <label className="block text-caption1 text-gray-500 dark:text-gray-400">
          {t('pt.site.scarceThreshold')}
          <Input
            type="number"
            value={draft.scarceSeeders}
            onChange={(e) => setDraft({ ...draft, scarceSeeders: Number(e.target.value) })}
            className="mt-0.5 h-8 text-footnote"
          />
        </label>
        <p className="text-caption2 text-gray-400 dark:text-gray-500">{t('pt.site.scarceHint')}</p>
        <label className="block text-caption1 text-gray-500 dark:text-gray-400">
          {t('pt.site.releaseTarget')}
          <Input
            type="number"
            value={draft.releaseTargetFreeGb}
            onChange={(e) => setDraft({ ...draft, releaseTargetFreeGb: Number(e.target.value) })}
            className="mt-0.5 h-8 text-footnote"
          />
        </label>
      </div>
      <div className="mt-2 flex items-center gap-2">
        <Button size="sm" className="h-7 gap-1.5 text-caption1" onClick={save}>
          <Save className="h-3 w-3" />
          {t('pt.contract.save')}
        </Button>
        {saved && <span className="text-caption1" style={{ color: 'var(--color-status-seeding)' }}>{t('pt.site.saved')}</span>}
      </div>
    </div>
  )
}

// ---------- 站点快照 ----------

function SnapshotPanel() {
  const { t } = useTranslation()
  const { loading, data, reload } = useLoader(() => ptApi.snapshots())
  const [form, setForm] = useState({ site: '', uploadedGb: '', downloadedGb: '', bonus: '', levelName: '', seedingCount: '', seedingSizeGb: '', hnrPreWarning: '', note: '' })
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')

  const submit = async () => {
    if (!form.site.trim()) return
    setBusy(true)
    try {
      await ptApi.addSnapshot({
        site: form.site.trim(),
        uploaded: form.uploadedGb ? Math.round(Number(form.uploadedGb) * GB) : undefined,
        downloaded: form.downloadedGb ? Math.round(Number(form.downloadedGb) * GB) : undefined,
        bonus: form.bonus ? Number(form.bonus) : undefined,
        levelName: form.levelName || undefined,
        seedingCount: form.seedingCount ? Number(form.seedingCount) : undefined,
        seedingSize: form.seedingSizeGb ? Math.round(Number(form.seedingSizeGb) * GB) : undefined,
        hnrPreWarning: form.hnrPreWarning ? Number(form.hnrPreWarning) : undefined,
        note: form.note || undefined,
      })
      setMsg(t('pt.site.snapshotSaved'))
      setForm({ site: '', uploadedGb: '', downloadedGb: '', bonus: '', levelName: '', seedingCount: '', seedingSizeGb: '', hnrPreWarning: '', note: '' })
      reload()
      setTimeout(() => setMsg(''), 2500)
    } finally {
      setBusy(false)
    }
  }

  const items: PTSnapshot[] = data?.items ?? []
  const diffs: PTSnapshotDiff[] = data?.diff ?? []

  return (
    <div className="glass-subcard rounded-panel p-3">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-footnote font-medium">{t('pt.site.snapshot')}</span>
        <Button variant="ghost" size="sm" className="h-7 w-7 p-0" onClick={reload} aria-label={t('pt.refresh')}>
          <RefreshCw className="h-3.5 w-3.5" />
        </Button>
      </div>

      {/* 登记表单：只填关键几项，其余留空 */}
      <div className="mb-3 grid grid-cols-2 gap-1.5 sm:grid-cols-4">
        <Input placeholder={t('pt.site.snapshotSite')} value={form.site} onChange={(e) => setForm({ ...form, site: e.target.value })} className="col-span-2 h-7 text-caption1" />
        <Input type="number" placeholder={t('pt.site.snapshotUploaded')} value={form.uploadedGb} onChange={(e) => setForm({ ...form, uploadedGb: e.target.value })} className="h-7 text-caption1" />
        <Input type="number" placeholder={t('pt.site.snapshotDownloaded')} value={form.downloadedGb} onChange={(e) => setForm({ ...form, downloadedGb: e.target.value })} className="h-7 text-caption1" />
        <Input type="number" placeholder={t('pt.site.snapshotBonus')} value={form.bonus} onChange={(e) => setForm({ ...form, bonus: e.target.value })} className="h-7 text-caption1" />
        <Input placeholder={t('pt.site.snapshotLevel')} value={form.levelName} onChange={(e) => setForm({ ...form, levelName: e.target.value })} className="h-7 text-caption1" />
        <Input type="number" placeholder={t('pt.site.snapshotSeedingCount')} value={form.seedingCount} onChange={(e) => setForm({ ...form, seedingCount: e.target.value })} className="h-7 text-caption1" />
        <Input type="number" placeholder={t('pt.site.snapshotSeedingSize')} value={form.seedingSizeGb} onChange={(e) => setForm({ ...form, seedingSizeGb: e.target.value })} className="h-7 text-caption1" />
      </div>
      <div className="mb-2 flex items-center gap-2">
        <Button size="sm" className="h-7 gap-1 text-caption1" onClick={submit} disabled={busy || !form.site.trim()}>
          <Plus className="h-3 w-3" />
          {t('pt.site.snapshotAdd')}
        </Button>
        {msg && <span className="text-caption1" style={{ color: 'var(--color-status-seeding)' }}>{msg}</span>}
      </div>

      {loading ? null : items.length === 0 ? (
        <p className="py-4 text-center text-caption1 text-gray-400 dark:text-gray-500">{t('pt.site.snapshotEmpty')}</p>
      ) : (
        <div className="max-h-[22vh] space-y-1 overflow-auto">
          {items.slice(0, 50).map((it, i) => {
            const d = diffs.find((x) => x.site === it.site)
            return (
              <div key={`${it.site}-${it.at}-${i}`} className="flex items-center gap-2 rounded-md px-1.5 py-1 text-caption1 hover:bg-gray-500/5">
                <span className="min-w-0 flex-1 truncate">{it.site}</span>
                {it.levelName && <span className="shrink-0 text-gray-500 dark:text-gray-400">{it.levelName}</span>}
                {typeof it.bonus === 'number' && <span className="shrink-0 tabular-nums text-gray-500 dark:text-gray-400">{it.bonus.toLocaleString()}</span>}
                <span className="shrink-0 text-gray-400 dark:text-gray-500">{formatDate(it.at, 'M-D HH:mm')}</span>
                {d && d.localVsSiteRatio > 0 && (
                  <span className="shrink-0 tabular-nums text-caption2 text-gray-500 dark:text-gray-400" title={t('pt.site.diffHint')}>
                    ×{d.localVsSiteRatio.toFixed(2)}
                  </span>
                )}
                {/* 时魔三通道：实测（快照差分）/ 复刻（NPHP 公式）/ 对账（页面 A 值）。
                    三者独立：实测对魔改公式站也成立，复刻能定位是参数错还是公式被改。 */}
                {d && (d.bonusPerHourMeasured ?? 0) > 0 && (
                  <span
                    className="shrink-0 tabular-nums text-caption2"
                    style={{ color: 'var(--color-status-seeding)' }}
                    title={t('pt.site.bonusMeasuredHint', { hours: (d.bonusMeasuredHours ?? 0).toFixed(0) })}
                  >
                    {d.bonusPerHourMeasured!.toFixed(1)}/h
                  </span>
                )}
                {d && (d.bonusPerHourMeasured ?? 0) <= 0 && (d.bonusMeasuredHours ?? 0) > 0 && (
                  <span className="shrink-0 text-caption2 text-amber-600 dark:text-amber-300" title={t('pt.site.bonusMeasuredBad')}>
                    {t('pt.site.bonusMeasuredBadShort')}
                  </span>
                )}
                {d && (d.bonusPerHourFormula ?? 0) > 0 && (
                  <span
                    className="shrink-0 tabular-nums text-caption2 text-gray-500 dark:text-gray-400"
                    title={t('pt.site.bonusFormulaHint', {
                      sumA: (d.bonusSumA ?? 0).toFixed(1),
                      observed: (d.bonusASumObserved ?? 0).toFixed(1),
                      // 直接把「本地 ΣA 与页面 A 差几个百分点」算出来：对账要比的是 A，
                      // 让用户拿 B 去比会被 atan 压扁后的 0.2% 骗过去（原因见 docs 1.6）
                      delta: sumAGap(d),
                    })}
                  >
                    {d.bonusPerHourFormula!.toFixed(1)}/h
                  </span>
                )}
                <button
                  type="button"
                  className="shrink-0 text-gray-400 hover:text-red-500"
                  aria-label={t('pt.contract.clear')}
                  onClick={async () => {
                    await ptApi.deleteSnapshots(it.site)
                    reload()
                  }}
                >
                  <Trash2 className="h-3 w-3" />
                </button>
              </div>
            )
          })}
        </div>
      )}
      <p className="mt-1.5 text-caption2 text-gray-400 dark:text-gray-500">{t('pt.site.diffHint')}</p>
    </div>
  )
}

// ---------- 考核 ----------

function ExamPanel() {
  const { t } = useTranslation()
  const { loading, data, reload } = useLoader(() => ptApi.exams())
  const [form, setForm] = useState({ site: '', name: '', deadline: '', minRatio: '', minUploadGb: '', minSeedingSizeGb: '' })
  const [busy, setBusy] = useState(false)

  const submit = async () => {
    if (!form.site.trim()) return
    setBusy(true)
    try {
      const body: PTExam = {
        site: form.site.trim(),
        name: form.name || form.site.trim(),
        deadline: form.deadline ? Math.floor(new Date(form.deadline).getTime() / 1000) : undefined,
        minRatio: form.minRatio ? Number(form.minRatio) : undefined,
        minUploadGb: form.minUploadGb ? Number(form.minUploadGb) : undefined,
        minSeedingSizeGb: form.minSeedingSizeGb ? Number(form.minSeedingSizeGb) : undefined,
      }
      await ptApi.saveExam(body)
      setForm({ site: '', name: '', deadline: '', minRatio: '', minUploadGb: '', minSeedingSizeGb: '' })
      reload()
    } finally {
      setBusy(false)
    }
  }

  const items: PTExamItem[] = data?.items ?? []
  return (
    <div className="glass-subcard rounded-panel p-3">
      <div className="mb-1 text-footnote font-medium">{t('pt.site.exam')}</div>
      <p className="mb-2 text-caption1 text-gray-500 dark:text-gray-400">{t('pt.site.examIntro')}</p>
      <div className="mb-2 grid grid-cols-2 gap-1.5 sm:grid-cols-3">
        <Input placeholder={t('pt.site.examSite')} value={form.site} onChange={(e) => setForm({ ...form, site: e.target.value })} className="h-7 text-caption1" />
        <Input placeholder={t('pt.site.examName')} value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} className="h-7 text-caption1" />
        <Input type="date" value={form.deadline} onChange={(e) => setForm({ ...form, deadline: e.target.value })} className="h-7 text-caption1" />
        <Input type="number" step="0.01" placeholder={t('pt.site.examMinRatio')} value={form.minRatio} onChange={(e) => setForm({ ...form, minRatio: e.target.value })} className="h-7 text-caption1" />
        <Input type="number" placeholder={t('pt.site.examMinUpload')} value={form.minUploadGb} onChange={(e) => setForm({ ...form, minUploadGb: e.target.value })} className="h-7 text-caption1" />
        <Input type="number" placeholder={t('pt.site.examMinSeedSize')} value={form.minSeedingSizeGb} onChange={(e) => setForm({ ...form, minSeedingSizeGb: e.target.value })} className="h-7 text-caption1" />
      </div>
      <Button size="sm" className="mb-2 h-7 gap-1 text-caption1" onClick={submit} disabled={busy || !form.site.trim()}>
        <Plus className="h-3 w-3" />
        {t('pt.site.examAdd')}
      </Button>

      {loading ? null : items.length === 0 ? (
        <p className="py-4 text-center text-caption1 text-gray-400 dark:text-gray-500">{t('pt.site.examEmpty')}</p>
      ) : (
        <div className="space-y-1.5">
          {items.map((e) => {
            const p = e.progress
            return (
              <div key={e.site} className="rounded-md px-1.5 py-1 text-caption1 hover:bg-gray-500/5">
                <div className="flex items-center gap-2">
                  <span className="min-w-0 flex-1 truncate font-medium">{e.name || e.site}</span>
                  {p.daysLeft !== 0 && (
                    <span className={cn('shrink-0 tabular-nums', p.daysLeft < 3 && 'text-amber-600 dark:text-amber-300')}>
                      {t('pt.site.examDaysLeft', { days: Math.round(p.daysLeft) })}
                    </span>
                  )}
                  <button
                    type="button"
                    className="shrink-0 text-gray-400 hover:text-red-500"
                    aria-label={t('pt.contract.clear')}
                    onClick={async () => {
                      await ptApi.deleteExam(e.site)
                      reload()
                    }}
                  >
                    <Trash2 className="h-3 w-3" />
                  </button>
                </div>
                <div className="mt-0.5 flex flex-wrap gap-x-3 gap-y-0.5 text-caption2">
                  {p.met.length > 0 && (
                    <span style={{ color: 'var(--color-status-seeding)' }}>
                      {t('pt.site.examMet')}：{p.met.join(' / ')}
                    </span>
                  )}
                  {p.missing.length > 0 && (
                    <span className="text-amber-600 dark:text-amber-300">
                      {t('pt.site.examMissing')}：{p.missing.join(' / ')}
                    </span>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      )}
      {/* 「下界」这条口径必须常驻：否则用户会拿本地数字去对站点，然后不信整个面板 */}
      <p className="mt-1.5 flex items-start gap-1 text-caption2 text-gray-400 dark:text-gray-500">
        <AlertTriangle className="mt-0.5 h-3 w-3 shrink-0" />
        {t('pt.site.examLowerBound')}
      </p>
    </div>
  )
}

export function SiteDataTab() {
  const { t } = useTranslation()
  return (
    <div className="space-y-3">
      <p className="text-caption1 text-gray-500 dark:text-gray-400">{t('pt.site.intro')}</p>
      <div className="grid gap-3 lg:grid-cols-2">
        <div className="min-w-0 space-y-3">
          <SnapshotPanel />
          <SettingsPanel />
        </div>
        <div className="min-w-0">
          <ExamPanel />
        </div>
      </div>
    </div>
  )
}
