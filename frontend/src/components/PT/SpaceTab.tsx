import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertTriangle, HardDrive, RefreshCw, Trash2 } from 'lucide-react'
import { ptApi } from '@/api/pt'
import type { PTActivity, PTEviction, PTLone } from '@/types/pt'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { formatBytes } from '@/utils/format'

// 「腾空间」页签：磁盘压力 / 淘汰建议 / 有文件没做种 / 活跃热力图。
//
// 设计约束（与本项目「只读」定位一致）：
//   - 全部是只读分析，一行代码都不发删除指令；
//   - 候选只来自「义务已满足且做种人数充足」的种子，稀缺与未知的一律不进；
//   - 每条建议都带一句人话理由，用户不必自己推断「为什么先放这颗」；
//   - 时魔只在**站内**比：各站货币不可通约，跨站比绝对值没有意义。

// bonusPosKey 把站内相对位置（0=本站最该先放，1=本站最后才放）翻成 i18n 键。
// 不直接在这里取 t：跨模块传 t 的签名（TFunction 重载）很容易在严格模式下类型不兼容，
// 让组件自己调 t 更稳，也让文案留在 i18n 里。
function bonusPosKey(pos: number): { key: string; n?: number } {
  if (pos <= 0.001) return { key: 'pt.bonusPos.lowest' }
  if (pos >= 0.999) return { key: 'pt.bonusPos.highest' }
  return { key: 'pt.bonusPos.percent', n: Math.round(pos * 100) }
}

function Loading() {
  return (
    <div className="flex items-center justify-center gap-2 py-10 text-footnote text-gray-500 dark:text-gray-400">
      <RefreshCw className="h-4 w-4 animate-spin" />
    </div>
  )
}

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
    // fn 是模块级闭包，只需在 nonce 变化时重跑
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nonce])
  return { ...state, reload: () => setNonce((n) => n + 1) }
}

// ---------- 磁盘 + 淘汰建议 ----------

function EvictionPanel() {
  const { t } = useTranslation()
  const { loading, error, data, reload } = useLoader<PTEviction>(() => ptApi.eviction())
  if (loading) return <Loading />
  if (error || !data) {
    return (
      <div className="flex flex-col items-center gap-3 py-8">
        <AlertTriangle className="h-6 w-6" style={{ color: 'var(--color-status-error)' }} />
        <Button variant="outline" size="sm" onClick={reload} className="gap-1.5">
          <RefreshCw className="h-3.5 w-3.5" />
          {t('pt.retry')}
        </Button>
      </div>
    )
  }
  const trig = data.trigger
  return (
    <div className="space-y-3">
      {/* 触发：磁盘剩余 vs 目标。空间充足时也要显示，否则用户不知道「为什么没建议」 */}
      <div className="glass-subcard rounded-panel p-3">
        <div className="mb-1 flex items-center gap-1.5 text-footnote font-medium">
          <HardDrive className="h-3.5 w-3.5" />
          {t('pt.space.diskUsage')}
        </div>
        {trig.targetFreeGb > 0 ? (
          <>
            <div className="text-body tabular-nums">{formatBytes(trig.freeBytes)}</div>
            <p className="mt-0.5 text-caption1 text-gray-500 dark:text-gray-400">
              {t('pt.space.target', { gb: trig.targetFreeGb })}
            </p>
            <p
              className={cn('mt-1 text-caption1', trig.low ? 'text-amber-600 dark:text-amber-300' : 'text-gray-500 dark:text-gray-400')}
            >
              {trig.low ? `${t('pt.space.lowSpace')} · ${t('pt.space.needBytes', { size: formatBytes(Math.max(0, trig.needBytes)) })}` : t('pt.space.enough')}
            </p>
          </>
        ) : (
          <p className="text-caption1 text-gray-500 dark:text-gray-400">{t('pt.space.targetUnset')}</p>
        )}
      </div>

      <div>
        <div className="mb-1 flex items-center justify-between">
          <span className="text-footnote font-medium">{t('pt.space.candidates')}</span>
          <Button variant="ghost" size="sm" className="h-7 w-7 p-0" onClick={reload} aria-label={t('pt.refresh')}>
            <RefreshCw className="h-3.5 w-3.5" />
          </Button>
        </div>
        {!trig.low && <p className="mb-1.5 text-caption1 text-gray-500 dark:text-gray-400">{t('pt.space.noTrigger')}</p>}
        {data.candidates.length === 0 ? (
          <p className="py-6 text-center text-footnote text-gray-400 dark:text-gray-500">{t('pt.space.empty')}</p>
        ) : (
          <div className="max-h-[32vh] overflow-auto rounded-lg">
            <table className="w-full min-w-[42rem] border-collapse text-footnote">
              <thead className="sticky top-0 z-10 bg-white/95 text-caption1 text-gray-500 backdrop-blur dark:bg-gray-900/95 dark:text-gray-400">
                <tr>
                  <th className="whitespace-nowrap px-2 py-1.5 text-left font-medium">{t('pt.ledger.name')}</th>
                  <th className="whitespace-nowrap px-2 py-1.5 text-right font-medium">{t('pt.ledger.size')}</th>
                  <th className="whitespace-nowrap px-2 py-1.5 text-right font-medium">{t('pt.ledger.swarm')}</th>
                  <th className="whitespace-nowrap px-2 py-1.5 text-right font-medium">{t('pt.ledger.bonus')}</th>
                  <th className="whitespace-nowrap px-2 py-1.5 text-left font-medium">{t('pt.ledger.release')}</th>
                </tr>
              </thead>
              <tbody>
                {data.candidates.map((c) => (
                  <tr key={c.hash} className="border-t border-gray-200/60 dark:border-white/10">
                    <td className="max-w-[18rem] truncate px-2 py-1.5" title={c.name}>
                      {c.name}
                    </td>
                    <td className="whitespace-nowrap px-2 py-1.5 text-right tabular-nums">{formatBytes(c.size)}</td>
                    <td className="whitespace-nowrap px-2 py-1.5 text-right tabular-nums">{c.seeders > 0 ? c.seeders : '—'}</td>
                    {/* 时魔·GB 与「本站第几便宜」并排：各站点数不可通约，绝对值只在站内有意义，
                        真正决定「先放哪颗」的是站内相对位置（后端按站内分位算好） */}
                    <td className="whitespace-nowrap px-2 py-1.5 text-right tabular-nums">
                      <span className="inline-flex flex-col items-end leading-tight">
                        <span>{c.bonusPerGb > 0 ? c.bonusPerGb.toFixed(3) : '—'}</span>
                        {typeof c.bonusEff === 'number' && (
                          <span className="text-caption2 text-gray-400 dark:text-gray-500">
                            {(() => {
                              const l = bonusPosKey(c.bonusEff)
                              return t(l.key, l.n === undefined ? {} : { n: l.n })
                            })()}
                          </span>
                        )}
                      </span>
                    </td>
                    {/* 理由与「删到本行可腾出多少」放在同一格：用户按行读就是一条完整建议 */}
                    <td className="px-2 py-1.5 text-caption1 text-gray-500 dark:text-gray-400">
                      <div className="truncate" title={c.reason}>
                        {c.reason}
                      </div>
                      <div className="text-gray-400 dark:text-gray-500">
                        {t('pt.space.cumulative', { size: formatBytes(c.cumulativeBytes) })}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}

// ---------- 有文件没做种 ----------

function LonePanel() {
  const { t } = useTranslation()
  const { loading, error, data, reload } = useLoader<PTLone>(() => ptApi.lone())
  if (loading) return <Loading />
  if (error || !data) return <p className="py-6 text-center text-footnote text-gray-400">{t('pt.loadFailed')}</p>
  return (
    <div>
      <div className="mb-1 flex items-center justify-between">
        <span className="text-footnote font-medium">{t('pt.space.lone')}</span>
        <Button variant="ghost" size="sm" className="h-7 w-7 p-0" onClick={reload} aria-label={t('pt.refresh')}>
          <RefreshCw className="h-3.5 w-3.5" />
        </Button>
      </div>
      <p className="mb-1.5 text-caption1 text-gray-500 dark:text-gray-400">{t('pt.space.loneIntro')}</p>
      {data.items.length === 0 ? (
        <p className="py-5 text-center text-footnote text-gray-400 dark:text-gray-500">{t('pt.space.loneEmpty')}</p>
      ) : (
        <div className="max-h-[26vh] space-y-1 overflow-auto">
          {data.items.map((it) => (
            <div key={it.path} className="flex items-center gap-2 rounded-md px-1.5 py-1 text-caption1 hover:bg-gray-500/5">
              <Trash2 className="h-3 w-3 shrink-0 text-gray-400" />
              <span className="min-w-0 flex-1 truncate" title={it.path}>
                {it.path}
              </span>
              <span className="shrink-0 tabular-nums text-gray-500 dark:text-gray-400">{formatBytes(it.size)}</span>
            </div>
          ))}
        </div>
      )}
      <p className="mt-1 text-caption2 text-gray-400 dark:text-gray-500">{t('pt.space.loneScan', { n: data.scanned })}</p>
    </div>
  )
}

// ---------- 活跃热力图 ----------

function HeatmapPanel() {
  const { t } = useTranslation()
  const { loading, error, data } = useLoader<PTActivity>(() => ptApi.activity())
  if (loading) return <Loading />
  if (error || !data) return <p className="py-6 text-center text-footnote text-gray-400">{t('pt.loadFailed')}</p>
  // 取最大值做归一化；全 0 时避免除零
  let max = 0
  for (const row of data.cells) for (const c of row) if (c.bytes > max) max = c.bytes
  const days = [t('pt.week.mon'), t('pt.week.tue'), t('pt.week.wed'), t('pt.week.thu'), t('pt.week.fri'), t('pt.week.sat'), t('pt.week.sun')]
  return (
    <div>
      <div className="mb-1 text-footnote font-medium">{t('pt.space.activity')}</div>
      <p className="mb-2 text-caption1 text-gray-500 dark:text-gray-400">{t('pt.space.activityHint')}</p>
      {max === 0 ? (
        <p className="py-5 text-center text-footnote text-gray-400 dark:text-gray-500">{t('pt.space.noActivity')}</p>
      ) : (
        <>
          <div className="overflow-x-auto">
            <div className="inline-block min-w-[26rem]">
              {/* 表头：每 3 小时标一次，避免挤成一团 */}
              <div className="mb-0.5 flex pl-7 text-caption2 text-gray-400 dark:text-gray-500">
                {Array.from({ length: 24 }, (_, h) => (
                  <span key={h} className="w-[1.6%] min-w-[0.9rem] text-center">
                    {h % 3 === 0 ? h : ''}
                  </span>
                ))}
              </div>
              {data.cells.map((row, d) => (
                <div key={d} className="mb-0.5 flex items-center">
                  <span className="w-7 shrink-0 text-caption2 text-gray-400 dark:text-gray-500">{days[d]}</span>
                  {row.map((c, h) => (
                    <span
                      key={h}
                      title={`${days[d]} ${h}:00 · ${c.count}`}
                      className="mr-0.5 h-3 w-[1.6%] min-w-[0.9rem] rounded-sm"
                      style={{
                        background: c.bytes > 0 ? `color-mix(in srgb, var(--color-primary) ${Math.max(18, Math.round((c.bytes / max) * 100))}%, transparent)` : 'rgba(128,128,128,0.12)',
                      }}
                    />
                  ))}
                </div>
              ))}
            </div>
          </div>
          <p className="mt-1 text-caption2 text-gray-400 dark:text-gray-500">
            {data.peakHour >= 0 ? t('pt.space.peakHour', { hour: data.peakHour }) : ''} · {data.note}
          </p>
        </>
      )}
    </div>
  )
}

export function SpaceTab() {
  const { t } = useTranslation()
  return (
    <div className="space-y-4">
      <p className="text-caption1 text-gray-500 dark:text-gray-400">{t('pt.space.intro')}</p>
      <div className="grid gap-3 lg:grid-cols-2">
        <div className="min-w-0">
          <EvictionPanel />
        </div>
        <div className="min-w-0 space-y-4">
          <div className="glass-subcard rounded-panel p-3">
            <HeatmapPanel />
          </div>
          <div className="glass-subcard rounded-panel p-3">
            <LonePanel />
          </div>
        </div>
      </div>
      <p className="text-caption1 text-gray-400 dark:text-gray-500">{t('pt.space.noDeleteNote')}</p>
    </div>
  )
}
