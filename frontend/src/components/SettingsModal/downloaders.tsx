import type { ReactNode } from 'react'
import {
  Download,
  Gauge,
  Monitor,
  Network,
  Plug,
  Rss,
  SlidersHorizontal,
  Workflow,
} from 'lucide-react'
import type { DownloaderKind } from '@/types'

/**
 * 下载器注册表：一处声明「界面需要知道的那点下载器元信息」。
 *
 * 存在的理由是消除重复。此前同一个事实散落在多个地方：
 * 默认地址写在 DEFAULT_URL、类型下拉在「连接栏」和「多服务器」各抄了一份
 * options、显示名与短标（TR / QB）在状态栏与归属列里各写一次三元判断。
 * 接入第三个下载器就要同时改五六处，漏一处就出现「下拉里能选、默认地址却空着」
 * 这类不一致。
 *
 * 边界（重要）：这里只放**前端渲染必须知道**的元信息。
 * 真正的能力差异与设置项一律由后端驱动的自述提供：
 *   - 功能有无 → driver.Capabilities（caps）
 *   - 设置面板 → driver.SettingsSchema（schema）
 * 新增下载器若复用既有能力与字段类型，**只需在这里加一条**；
 * 只有在界面需要全新的控件类型时才回来改渲染层。
 */
export interface DownloaderMeta {
  /** 连接地址默认值：地址栏是必填项，给一个能直接用的初值 */
  defaultUrl: string
  /** 显示名（品牌名，两种语言一致，不进 i18n） */
  label: string
  /** 极短标：状态栏与归属列用 */
  short: string
  /**
   * 该驱动的设置是否由字段自述（schema）渲染。
   * true  —— 有 schema 就按自述通用渲染（qBittorrent 上百个偏好键）
   * false —— 沿用内置的通用会话表单（Transmission）
   * 这是**当前实现**的过渡标记：等 Transmission 也提供 schema 后即可去掉。
   */
  schemaDriven: boolean
  /**
   * 是否要手填磁盘总容量。「多服务器管理」据此决定显不显示该输入项：
   * 只有自己报不出总容量的下载器才需要（qBittorrent 的 free_space_on_disk
   * 只有剩余空间），报得出的（Transmission 的 free-space 带 total）填了也不会用。
   */
  manualDiskTotal: boolean
}

export const DOWNLOADERS: Record<DownloaderKind, DownloaderMeta> = {
  transmission: {
    defaultUrl: 'http://localhost:9091/transmission/rpc',
    label: 'Transmission',
    short: 'TR',
    schemaDriven: false,
    manualDiskTotal: false,
  },
  qbittorrent: {
    defaultUrl: 'http://localhost:8080',
    label: 'qBittorrent',
    short: 'QB',
    schemaDriven: true,
    manualDiskTotal: true,
  },
}

/** 全部下载器的 (值, 显示名) 列表，供类型下拉直接铺开 */
export const DOWNLOADER_OPTIONS = (Object.keys(DOWNLOADERS) as DownloaderKind[]).map((k) => ({
  value: k,
  label: DOWNLOADERS[k].label,
}))

/** 取元信息；未知类型回落到 Transmission（与后端 NormalizeKind 同口径） */
export function downloaderMeta(kind?: DownloaderKind | string | null): DownloaderMeta {
  if (kind === 'qbittorrent') return DOWNLOADERS.qbittorrent
  return DOWNLOADERS.transmission
}

export const defaultUrlFor = (kind?: DownloaderKind | string | null): string =>
  downloaderMeta(kind).defaultUrl

/**
 * 判断当前地址是不是「另一类的默认值」——切换下载器类型时应该跟着换掉，
 * 否则会留下一个明显不属于新类型的地址（选了 qB 却还写着 /transmission/rpc）。
 */
export function isOtherDefault(url: string, kind: DownloaderKind): boolean {
  const u = url.trim()
  return (Object.keys(DOWNLOADERS) as DownloaderKind[]).some(
    (k) => k !== kind && DOWNLOADERS[k].defaultUrl === u,
  )
}

/**
 * 分节图标：按**语义名**取，而不是按 qBittorrent 的节名。
 *
 * 语义名由驱动在 SettingsSection.Icon 里声明（见 models.SettingIcon*），
 * 新驱动按语义挑名字即可，不必回前端加对照表；认不出的名字回落通用图标。
 */
const SECTION_ICONS: Record<string, ReactNode> = {
  behavior: <Workflow />,
  download: <Download />,
  connect: <Plug />,
  speed: <Gauge />,
  bittorrent: <Network />,
  rss: <Rss />,
  webui: <Monitor />,
  advanced: <SlidersHorizontal />,
  network: <Network />,
  queue: <Workflow />,
  peers: <Network />,
  scripts: <SlidersHorizontal />,
  storage: <Download />,
}

export const sectionIcon = (icon?: string): ReactNode =>
  (icon && SECTION_ICONS[icon]) || <SlidersHorizontal />
