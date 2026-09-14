import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { torrentApi } from '@/api/torrent'
import type { EditMode, EditTarget } from '@/components/TorrentMenu'
import { useTorrentActions } from '@/hooks/useTorrentActions'
import { useRevealPath } from '@/hooks/useRevealPath'
import { toast } from '@/lib/toast'
import type { Torrent } from '@/types'
import { copyText } from '@/utils/clipboard'
import { mapPath } from '@/utils/pathMapping'

interface Options {
  onOpenDetail?: (t: Torrent) => void
  onOpenBatchClean?: () => void
  onEdit: (target: EditTarget) => void
  onRemove: (id: number) => void
  onReplaceTrackers: () => void
  /** 宿主是否支持「打开所在文件夹」；不支持时菜单项本身已隐藏，这里只做兜底 */
  canRevealPath?: boolean
}

/**
 * 种子菜单的动作分发。
 *
 * 桌面表格与卡片列表此前各抄了一份 28 行的 if-else 链，两份内容完全一致——
 * 新增一个菜单项必须记得同时改两处，否则该视图下点了没反应。
 * 这里收敛为单一实现，两个视图只负责把自己的弹窗状态交进来。
 */
export function useTorrentMenuHandler({
  onOpenDetail,
  onOpenBatchClean,
  onEdit,
  onRemove,
  onReplaceTrackers,
  canRevealPath,
}: Options) {
  const { t } = useTranslation()
  const actions = useTorrentActions()
  const revealPath = useRevealPath()

  return useCallback(
    (torrent: Torrent) => (key: string) => {
      const id = torrent.id
      const reportCopy = (ok: boolean) =>
        ok ? toast.success(t('toast.copied')) : toast.error(t('toast.copyFailed'))

      switch (key) {
        case 'start':
          return actions.singleStart(id)
        case 'startNow':
          return actions.singleStartNow(id)
        case 'stop':
          return actions.singleStop(id)
        case 'verify':
          return actions.verify(id)
        case 'reannounce':
          return actions.reannounce(id)
        case 'path':
          return onEdit({ torrent, mode: 'path' })
        case 'rename':
          return onEdit({ torrent, mode: 'rename' })
        case 'other':
          return onEdit({ torrent, mode: 'other' })
        case 'labels':
          return onEdit({ torrent, mode: 'labels' })
        case 'trackers':
          return onEdit({ torrent, mode: 'trackers' })
        case 'replaceTrackers':
          return onReplaceTrackers()
        case 'remove':
          return onRemove(id)
        case 'openDir':
          if (!canRevealPath) return
          void revealPath(torrent.downloadDir || '')
          return
        case 'copyMagnet':
          void copyText(torrent.magnetLink).then(reportCopy)
          return
        case 'copyName':
          void copyText(torrent.name).then(reportCopy)
          return
        case 'copyPath':
          void mapPath(torrent.downloadDir).then(copyText).then(reportCopy)
          return
        case 'deleteCompleted':
          onOpenBatchClean?.()
          return
        default:
          if (key.startsWith('queue:')) {
            actions.queue(id, key.split(':')[1] as 'top' | 'up' | 'down' | 'bottom')
          }
      }
    },
    [
      actions,
      t,
      onEdit,
      onRemove,
      onReplaceTrackers,
      onOpenBatchClean,
      canRevealPath,
      revealPath,
    ],
  )
}

export type { EditMode, Torrent }
