import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import type { UpdateStatus } from '@/api/torrent'

// 线路信息：说明本次更新「走的是直连还是加速源」，并列出各线路实际承担的体积。
//
// 线路名由后端给出（直连 GitHub / 加速源 1/4（gh-proxy.com）），前端不重新拼装，
// 保证与进度文案、后端日志里的线路名完全一致；这里只补徽标与提示。
const UPDATE_MB = 1024 * 1024

function formatSize(bytes: number): string {
  if (!bytes || bytes <= 0) return ''
  const mb = bytes / UPDATE_MB
  return mb >= 100 ? `${Math.round(mb)}MB` : `${mb.toFixed(1)}MB`
}

export function UpdateLineInfo({ status }: { status?: UpdateStatus | null }) {
  const { t } = useTranslation()
  if (!status) return null

  const line = status.line?.trim() ?? ''
  const lines = status.lines ?? []
  const direct = status.lineKind === 'direct'
  // 直连被墙（探测超时）时才提示：这种情况下面会落到加速源，值得说明一句
  const directTimeout = status.directProbe === 'timeout' && !direct
  if (!line && lines.length === 0 && !directTimeout) return null

  return (
    <div className="space-y-0.5">
      {line && (
        <div className="flex items-center gap-1.5 flex-wrap text-caption1 text-gray-500 dark:text-gray-400">
          <span>{t('session.updateLine')}</span>
          <Badge
            className={
              direct
                ? 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300'
                : 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300'
            }
          >
            {direct ? t('session.updateLineDirect') : t('session.updateLineMirror')}
          </Badge>
          <span className="break-all">{line}</span>
        </div>
      )}
      {directTimeout && (
        <p className="text-caption1 text-amber-600 dark:text-amber-400">{t('session.updateDirectProbeTimeout')}</p>
      )}
      {lines.length > 0 && (
        <p className="text-caption1 text-gray-400 dark:text-gray-500">
          {t('session.updateLinesUsed')}
          {lines.map((l) => {
            const size = formatSize(l.bytes)
            return (
              <span key={`${l.kind}-${l.host}`} className="ml-1">
                {l.name}
                {size ? ` ${size}` : ''}
              </span>
            )
          })}
        </p>
      )}
      {!!status.retried && (
        <p className="text-caption1 text-gray-400 dark:text-gray-500">
          {t('session.updateRetried', { count: status.retried })}
        </p>
      )}
    </div>
  )
}
