import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { client } from '@/api/client'
import { updateApi, type UpdateCheckResult, type UpdateStatus } from '@/api/torrent'
import type { ApiResponse } from '@/types'
import { usePlatform } from '@/platform'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

// 打开 UI 自动检查应用更新并弹窗提醒。仅宿主提供 app.update 能力（fnOS）时生效。
// 行为对齐参考实现：页面加载稍后自动查一次；结果 24h 缓存；「忽略此版本」持久化；
// 关闭（稍后 / 右上 × / Esc / 遮罩）后 24h 内不再弹。
const DAY_MS = 24 * 60 * 60 * 1000
const CHECK_DELAY_MS = 1500
const CACHE_KEY = 'tm_update_cache'
const IGNORED_KEY = 'tm_update_ignored'
const CLOSED_KEY = 'tm_update_closed_at'

function lsGet(key: string): string | null {
  try {
    return window.localStorage.getItem(key)
  } catch {
    return null
  }
}

function lsSet(key: string, value: string): void {
  try {
    window.localStorage.setItem(key, value)
  } catch {
    // 无痕模式等场景下存不下，仅影响防打扰记忆，不阻断提醒
  }
}

interface CachedCheck {
  timestamp: number
  data: UpdateCheckResult
}

function readCache(): CachedCheck | null {
  const raw = lsGet(CACHE_KEY)
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw) as CachedCheck
    if (parsed && typeof parsed.timestamp === 'number' && parsed.data) return parsed
    return null
  } catch {
    return null
  }
}

// 后台静默检查：直接走 client 而非 request()，失败不弹错误 toast（自动检查不该打扰用户）。
async function silentCheck(): Promise<UpdateCheckResult | null> {
  try {
    const resp = await client.get<ApiResponse<UpdateCheckResult>>('/update/check')
    if (resp.data.code === 0) return resp.data.data
    return null
  } catch {
    return null
  }
}

export function AppUpdatePrompt() {
  const { t } = useTranslation()
  const { ready, can } = usePlatform()
  const [info, setInfo] = useState<UpdateCheckResult | null>(null)
  const [open, setOpen] = useState(false)
  const [updStatus, setUpdStatus] = useState<UpdateStatus | null>(null)
  const timer = useRef<ReturnType<typeof setInterval> | null>(null)

  const stopPoll = () => {
    if (timer.current) {
      clearInterval(timer.current)
      timer.current = null
    }
  }
  useEffect(() => stopPoll, [])

  useEffect(() => {
    if (!ready || !can('app.update')) return
    let cancelled = false

    const run = async () => {
      const closedAt = Number(lsGet(CLOSED_KEY) || 0)
      if (closedAt && Date.now() - closedAt < DAY_MS) return

      let result: UpdateCheckResult | null = null
      const cached = readCache()
      if (cached && Date.now() - cached.timestamp < DAY_MS) {
        result = cached.data
      } else {
        result = await silentCheck()
        if (result) lsSet(CACHE_KEY, JSON.stringify({ timestamp: Date.now(), data: result } satisfies CachedCheck))
      }
      if (cancelled || !result || !result.hasUpdate) return
      if (result.latestVersion === lsGet(IGNORED_KEY)) return
      setInfo(result)
      setOpen(true)
    }

    const id = setTimeout(() => {
      void run()
    }, CHECK_DELAY_MS)
    return () => {
      cancelled = true
      clearTimeout(id)
    }
  }, [ready, can])

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

  // 下载 fpk 给应用中心手动安装：走 updateApi 以便带上鉴权头
  const downloadFpk = async () => {
    try {
      await updateApi.download(updStatus?.fpkFilename)
    } catch {
      // 拦截器已提示
    }
  }

  // 「稍后」/右上 ×/Esc/遮罩：记录关闭时间，24h 内不再弹
  const snooze = () => {
    lsSet(CLOSED_KEY, String(Date.now()))
    stopPoll()
    setOpen(false)
  }

  const ignoreVersion = () => {
    if (info) lsSet(IGNORED_KEY, info.latestVersion)
    stopPoll()
    setOpen(false)
  }

  const downloading = !!updStatus?.updating
  const done = !!updStatus && !updStatus.updating && updStatus.progress >= 100
  const failed = !!updStatus && !updStatus.updating && updStatus.failed
  const readyWithoutInstall = !!info?.downloadReady && !updStatus

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) snooze()
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('session.newVersionFound')}</DialogTitle>
          <DialogDescription>
            {info ? `v${info.currentVersion} → v${info.latestVersion} (${info.arch})` : ''}
          </DialogDescription>
        </DialogHeader>

        {info?.changelog && (
          <div className="max-h-32 overflow-y-auto whitespace-pre-line text-body text-gray-600 dark:text-gray-400">
            {info.changelog}
          </div>
        )}

        {downloading && (
          <div>
            <div className="h-1.5 rounded-full bg-gray-200/80 dark:bg-gray-700/60 overflow-hidden">
              <div className="h-full bg-primary transition-all" style={{ width: `${updStatus?.progress ?? 0}%` }} />
            </div>
            <p className="mt-1 text-footnote text-gray-500 dark:text-gray-400">
              {updStatus?.progress ?? 0}% · {updStatus?.message}
            </p>
          </div>
        )}
        {failed && <p className="text-body text-red-500">{updStatus?.message || t('session.updateFailed')}</p>}

        {done || readyWithoutInstall ? (
          <div className="space-y-1">
            <Button size="sm" className="h-8 text-footnote w-full" onClick={() => void downloadFpk()}>
              {t('session.downloadFpk')}
            </Button>
            <p className="text-footnote text-gray-500 dark:text-gray-400">{t('session.fpkInstallHint')}</p>
          </div>
        ) : (
          <div className="flex flex-wrap items-center justify-end gap-2">
            <Button variant="ghost" size="sm" className="h-8 text-footnote" onClick={ignoreVersion}>
              {t('session.ignoreVersion')}
            </Button>
            <Button variant="outline" size="sm" className="h-8 text-footnote" onClick={snooze}>
              {t('session.updateLater')}
            </Button>
            {info?.releaseUrl && (
              <Button asChild variant="outline" size="sm" className="h-8 text-footnote">
                <a href={info.releaseUrl} target="_blank" rel="noreferrer">
                  {t('session.viewRelease')}
                </a>
              </Button>
            )}
            {info?.fpkUrl && (
              <Button size="sm" className="h-8 text-footnote" disabled={downloading} onClick={() => void install()}>
                {downloading ? t('session.updating') : t('session.updateNow')}
              </Button>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
