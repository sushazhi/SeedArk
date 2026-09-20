import { useEffect, useState } from 'react'
import { Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { sessionApi } from '@/api/torrent'
import { useAppStore } from '@/stores/appStore'
import type { BandwidthGroup, SessionStats } from '@/types'
import { formatBytes, formatEta, formatSpeed } from '@/utils/format'
import { toast } from '@/lib/toast'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'

// 会话统计 + 备用带宽/全局限速控制 + 带宽组管理
export function SessionPanel() {
  const { t } = useTranslation()
  const session = useAppStore((s) => s.session)
  const setSession = useAppStore((s) => s.setSession)
  const [stats, setStats] = useState<SessionStats | null>(null)

  const refreshStats = () => sessionApi.stats().then(setStats).catch(() => {})

  useEffect(() => {
    refreshStats()
    const iv = window.setInterval(refreshStats, 5000)
    return () => window.clearInterval(iv)
  }, [])

  const patchSession = async (patch: Record<string, unknown>) => {
    try {
      await sessionApi.update(patch)
      setSession(session ? { ...session, ...patch } : session)
      toast.success(t('toast.updated'))
    } catch {
      // 拦截器已提示
    }
  }

  const row = 'flex items-center justify-between py-1.5'
  const label = 'text-body text-gray-600 dark:text-gray-300'
  const value = 'text-body text-gray-800 dark:text-gray-100'

  return (
    <div className="space-y-4">
      {/* 带宽控制 */}
      <div>
        <div className="text-footnote font-semibold text-gray-400 mb-1">{t('session.bandwidth')}</div>
        <div className={row}>
          <span className={label}>{t('session.altSpeed')}</span>
          <Switch
            checked={session?.altSpeedEnabled ?? false}
            onCheckedChange={(checked) => patchSession({ altSpeedEnabled: checked })}
          />
        </div>
        <div className={row}>
          <span className={label}>{t('session.speedLimitDown')}</span>
          <div className="flex items-center gap-2">
            <Input
              type="number"
              min={0}
              className="h-8 w-24 text-footnote"
              value={session?.speedLimitDownOn ? String(session.speedLimitDown) : ''}
              disabled={!session?.speedLimitDownOn}
              onChange={(e) => {
                const n = Number(e.target.value)
                if (!Number.isNaN(n)) patchSession({ speedLimitDown: n })
              }}
            />
            <Switch
              checked={session?.speedLimitDownOn ?? false}
              onCheckedChange={(checked) => patchSession({ speedLimitDownOn: checked })}
            />
          </div>
        </div>
        <div className={row}>
          <span className={label}>{t('session.speedLimitUp')}</span>
          <div className="flex items-center gap-2">
            <Input
              type="number"
              min={0}
              className="h-8 w-24 text-footnote"
              value={session?.speedLimitUpOn ? String(session.speedLimitUp) : ''}
              disabled={!session?.speedLimitUpOn}
              onChange={(e) => {
                const n = Number(e.target.value)
                if (!Number.isNaN(n)) patchSession({ speedLimitUp: n })
              }}
            />
            <Switch
              checked={session?.speedLimitUpOn ?? false}
              onCheckedChange={(checked) => patchSession({ speedLimitUpOn: checked })}
            />
          </div>
        </div>
      </div>

      {/* qBittorrent 没有带宽组，按能力自述隐藏整个面板 */}
      {session?.caps?.bandwidthGroups !== false && <BandwidthGroupsPanel />}

      {/* 会话统计 */}
      {stats && (
        <div>
          <div className="text-footnote font-semibold text-gray-400 mb-1">{t('session.stats')}</div>
          <div className={row}>
            <span className={label}>{t('session.downloaded')}</span>
            <span className={value}>{formatBytes(stats.cumulative.downloadedBytes)}</span>
          </div>
          <div className={row}>
            <span className={label}>{t('session.uploaded')}</span>
            <span className={value}>{formatBytes(stats.cumulative.uploadedBytes)}</span>
          </div>
          <div className={row}>
            <span className={label}>{t('session.filesAdded')}</span>
            <span className={value}>{stats.cumulative.filesAdded}</span>
          </div>
          <div className={row}>
            <span className={label}>{t('session.sessionCount')}</span>
            <span className={value}>{stats.cumulative.sessionCount}</span>
          </div>
          <div className={row}>
            <span className={label}>{t('session.secondsActive')}</span>
            <span className={value}>{formatEta(stats.cumulative.secondsActive)}</span>
          </div>
          <div className={row}>
            <span className={label}>{t('session.current')}</span>
            <span className={value}>↓ {formatSpeed(stats.downloadSpeed)} · ↑ {formatSpeed(stats.uploadSpeed)}</span>
          </div>
        </div>
      )}
    </div>
  )
}

// 带宽组管理（Transmission 4.x）：每组独立限速，种子可归属多个组
function BandwidthGroupsPanel() {
  const { t } = useTranslation()
  const [groups, setGroups] = useState<BandwidthGroup[]>([])
  const [newName, setNewName] = useState('')

  const refresh = () => sessionApi.groups().then(setGroups).catch(() => {})

  useEffect(() => {
    refresh()
  }, [])

  const save = async (g: Partial<BandwidthGroup> & { name: string }) => {
    try {
      await sessionApi.saveGroup(g)
      toast.success(t('toast.updated'))
      refresh()
    } catch {
      // 拦截器已提示
    }
  }

  const addGroup = async () => {
    const name = newName.trim()
    if (!name) return
    await save({ name, downKB: 0, upKB: 0, downEnabled: false, upEnabled: false, honorsSessionLimits: true })
    setNewName('')
  }

  const input = 'h-8 w-20 text-footnote'

  return (
    <div>
      <div className="text-footnote font-semibold text-gray-400 mb-1">{t('session.groupsTitle')}</div>
      {groups.length === 0 && (
        <div className="text-footnote text-gray-400 py-1">{t('session.groupsEmpty')}</div>
      )}
      {groups.map((g) => (
        <div key={g.name} className="py-1.5 border-b border-gray-200/30 dark:border-gray-700/30 last:border-0">
          <div className="flex items-center justify-between">
            <span className="text-body font-medium text-gray-800 dark:text-gray-100 truncate">{g.name}</span>
            <Switch
              checked={g.honorsSessionLimits}
              onCheckedChange={(checked) => save({ name: g.name, honorsSessionLimits: checked })}
            />
          </div>
          <div className="flex items-center gap-3 mt-1">
            <span className="text-footnote text-gray-400 w-6">↓</span>
            <Input
              type="number"
              min={0}
              className={input}
              value={String(g.downKB)}
              disabled={!g.downEnabled}
              onChange={(e) => {
                const n = Number(e.target.value)
                if (!Number.isNaN(n)) setGroups((prev) => prev.map((x) => (x.name === g.name ? { ...x, downKB: n } : x)))
              }}
              onBlur={() => save({ name: g.name, downKB: g.downKB })}
            />
            <Switch
              checked={g.downEnabled}
              onCheckedChange={(checked) => save({ name: g.name, downEnabled: checked })}
            />
            <span className="text-footnote text-gray-400 w-6">↑</span>
            <Input
              type="number"
              min={0}
              className={input}
              value={String(g.upKB)}
              disabled={!g.upEnabled}
              onChange={(e) => {
                const n = Number(e.target.value)
                if (!Number.isNaN(n)) setGroups((prev) => prev.map((x) => (x.name === g.name ? { ...x, upKB: n } : x)))
              }}
              onBlur={() => save({ name: g.name, upKB: g.upKB })}
            />
            <Switch
              checked={g.upEnabled}
              onCheckedChange={(checked) => save({ name: g.name, upEnabled: checked })}
            />
          </div>
        </div>
      ))}
      <div className="flex items-center gap-2 mt-2">
        <Input
          className="h-8 flex-1 text-footnote"
          placeholder={t('session.groupNamePlaceholder')}
          value={newName}
          onChange={(e) => setNewName(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') void addGroup() }}
        />
        <Button variant="outline" size="sm" disabled={!newName.trim()} onClick={addGroup}>
          <Plus className="w-3.5 h-3.5" />
          {t('session.groupAdd')}
        </Button>
      </div>
      <div className="text-footnote text-gray-400 mt-1">{t('session.groupsHint')}</div>
    </div>
  )
}
