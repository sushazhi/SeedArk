import { useTranslation } from 'react-i18next'
import type { DownloaderKind, Torrent } from '@/types'
import { cn } from '@/lib/utils'

/**
 * 归属服务器单元格：聚合视图下回答「这颗种子在哪个下载器上」。
 *
 * 后端在种子列表里带上 serverIndex / serverName / kind（见 rpc.Manager 的
 * tagTorrents），这里只做呈现。三者都缺省时（纯 .env 直连、没有服务器列表条目）
 * 显示占位符——不给假信息，也不让整列塌掉。
 *
 * 呈现原则：**同一信息不重复**。服务器名以 QB / TR 打头时（「QB-甲」）不再挂徽标，
 * 只把名字染成对应色相；名字看不出类型时才补徽标。
 */

// 下载器类型短标：表格里只用两个字母，全名放 title
const KIND_ABBR: Record<DownloaderKind, string> = {
  transmission: 'TR',
  qbittorrent: 'QB',
}

// 每种下载器一个固定色相，让同一个聚合列表里「哪台的」可以靠颜色扫读
const KIND_TONE: Record<DownloaderKind, string> = {
  transmission: 'bg-orange-500/12 text-orange-600 dark:text-orange-400',
  qbittorrent: 'bg-blue-500/12 text-blue-600 dark:text-blue-400',
}

export function KindBadge({ kind, className }: { kind?: DownloaderKind; className?: string }) {
  const { t } = useTranslation()
  if (!kind) return null
  return (
    <span
      className={cn(
        'tm-chip shrink-0 rounded px-1 py-0 text-caption2 font-semibold leading-none',
        KIND_TONE[kind] ?? 'bg-gray-500/12 text-gray-500',
        className,
      )}
      title={kind === 'qbittorrent' ? 'qBittorrent' : 'Transmission'}
    >
      {KIND_ABBR[kind] ?? t('columns.server')}
    </span>
  )
}

/**
 * 服务器名是否已经自带下载器前缀（如「QB-甲」「TR-丙」「qbittorrent 主机」）。
 *
 * 用户给服务器起名时十有八九会用 QB / TR 打头，此时再挂一枚 QB / TR 徽标，
 * 整格就成了「QB QB-甲」——看着像把类型显示了两遍。名字已经说清楚了就不再重复，
 * 只把颜色留在名字上；名字看不出类型的（「主机A」「黑群」）才补徽标。
 */
function nameSaysKind(name: string, kind?: DownloaderKind): boolean {
  if (!kind) return false
  const head = name.trim().toLowerCase()
  if (kind === 'qbittorrent') return head.startsWith('qb')
  return head.startsWith('tr')
}

/** 名字本身已经是「类型前缀」时，把名字染成该下载器的色相，保住颜色扫读 */
function toneFor(torrent: Torrent): string {
  if (!torrent.kind) return 'text-gray-600 dark:text-gray-300'
  return torrent.kind === 'qbittorrent'
    ? 'text-blue-600 dark:text-blue-400'
    : 'text-orange-600 dark:text-orange-400'
}

export function ServerCell({ torrent }: { torrent: Torrent }) {
  const name = torrent.serverName
  if (!name) {
    return <span className="text-gray-300 dark:text-gray-600">-</span>
  }
  // 名字已含类型前缀：只显示名字（染色），不挂徽标，避免「QB QB-甲」
  if (nameSaysKind(name, torrent.kind)) {
    return (
      <span className={cn('text-footnote font-medium truncate', toneFor(torrent))} title={name}>
        {name}
      </span>
    )
  }
  return (
    <span className="flex items-center gap-1.5 min-w-0" title={name}>
      <KindBadge kind={torrent.kind} />
      <span className="text-footnote text-gray-600 dark:text-gray-300 truncate">{name}</span>
    </span>
  )
}

/** 移动端卡片用：只有下载器类型时也要能显示出来 */
export function ServerInline({ torrent }: { torrent: Torrent }) {
  const name = torrent.serverName
  if (!torrent.kind && !name) return null
  if (name && nameSaysKind(name, torrent.kind)) {
    return <span className={cn('truncate text-caption1 font-medium', toneFor(torrent))}>{name}</span>
  }
  return (
    <span className="flex items-center gap-1 min-w-0">
      <KindBadge kind={torrent.kind} />
      {name && <span className="truncate text-caption1 text-gray-500">{name}</span>}
    </span>
  )
}
