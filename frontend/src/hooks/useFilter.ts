import { useMemo } from 'react'
import type { FilterOptions, Torrent } from '@/types'

// 状态优先级（多级排序的第一级：下载中 > 做种中 > 暂停等）
const STATUS_RANK: Record<number, number> = {
  4: 0, // 下载中
  3: 1, // 等待下载
  6: 2, // 做种中
  5: 3, // 等待做种
  1: 4, // 校验中
  2: 5, // 等待校验
  0: 6, // 暂停
}

// 状态优先排序权重（错误状态排最后）
export function statusRank(t: Torrent): number {
  if (t.error > 0) return 7
  return STATUS_RANK[t.status] ?? 8
}

// 二级排序字段取值（多级排序的第二级）
export function sortValue(t: Torrent, field: string): number {
  switch (field) {
    case 'name':
      return 0
    case 'size':
      return t.totalSize
    case 'progress':
      return t.percentDone
    case 'status':
      return statusRank(t)
    case 'download':
      return t.rateDownload
    case 'upload':
      return t.rateUpload
    case 'ratio':
      return t.uploadRatio
    case 'secondsSeeding':
      return t.secondsSeeding
    case 'eta':
      return t.eta
    case 'peers':
      return t.peersSendingToUs + t.peersGettingFromUs
    case 'uploaded':
      return t.uploadedEver
    case 'downloaded':
      return t.downloadedEver
    case 'added':
      return t.addedDate
    case 'doneDate':
      return t.doneDate
    case 'tracker':
      return 0
    case 'label':
      return 0
    case 'queuePosition':
      return t.queuePosition
    case 'error':
      return t.error
    case 'priority':
      return t.bandwidthPriority ?? 0
    case 'limits':
      return t.downloadLimited ? t.downloadLimit : -1
    case 'fileCount':
      return t.fileCount
    default:
      return 0
  }
}

// 搜索规范化：小写并去掉空格/标点，保留字母、数字与中日韩文字
export function normalizeSearch(s: string): string {
  return s.toLowerCase().replace(/[^\p{L}\p{N}]+/gu, '')
}

// 活跃 = 实际有传输流量（下载或上传），处于下载/做种状态但没有流量的种子不算
export function isActiveTorrent(t: Torrent): boolean {
  return t.rateDownload > 0 || t.rateUpload > 0
}

// 状态过滤匹配
export function matchesStatus(t: Torrent, s: string): boolean {
  switch (s) {
    case 'all':
      return true
    case 'active':
      return isActiveTorrent(t)
    case 'downloading':
      return t.status === 3 || t.status === 4
    case 'seeding':
      return t.status === 6
    case 'waiting-seed':
      return t.status === 5
    case 'completed':
      return t.percentDone >= 1
    case 'paused':
      return t.status === 0
    case 'error':
      return t.error > 0
    case 'verifying':
      return t.status === 1 || t.status === 2
    default:
      return true
  }
}

// 过滤 + 排序（多级排序：先按状态优先级，再按 sortField 二级排序）
export function useFilter(
  torrents: Torrent[],
  filters: FilterOptions,
  torrentSites: Record<number, string[]>,
  sortField = 'name',
  sortOrder: 'asc' | 'desc' = 'asc',
) {
  return useMemo(() => {
    let list = torrents

    // 状态过滤
    if (filters.status.length > 0 && !filters.status.includes('all')) {
      list = list.filter((t) => filters.status.some((s) => matchesStatus(t, s)))
    }

    // 标签过滤
    if (filters.labels.length > 0) {
      list = list.filter((t) =>
        filters.labels.some((label) => {
          if (label === '__none__') return !t.labels || t.labels.length === 0
          return t.labels?.includes(label) ?? false
        }),
      )
    }

    // 站点过滤（__other__ 表示无站点归属）
    if (filters.sites.length > 0) {
      list = list.filter((t) => {
        const sites = torrentSites[t.id] ?? []
        return filters.sites.some((s) => (s === '__other__' ? sites.length === 0 : sites.includes(s)))
      })
    }

    // 下载目录过滤
    if (filters.downloadDirs.length > 0) {
      list = list.filter((t) => filters.downloadDirs.includes(t.downloadDir))
    }

    // 错误信息过滤（错误分布分组）
    if (filters.error.length > 0) {
      list = list.filter((t) => filters.error.includes(t.errorString))
    }

    // 归属下载器过滤（多服务器聚合）。按服务器名匹配：
    // 非聚合部署下不给归属字段，此时 servers 恒为空、这段不生效。
    if (filters.servers.length > 0) {
      list = list.filter((t) => t.serverName != null && filters.servers.includes(t.serverName))
    }

    // 搜索（名称/哈希，模糊匹配：忽略空格与标点、不区分大小写）
    const q = normalizeSearch(filters.search)
    if (q) {
      list = list.filter(
        (t) => normalizeSearch(t.name).includes(q) || normalizeSearch(t.hashString).includes(q),
      )
    }

    // 多级排序：第一级状态优先级（下载中 > 做种中 > 暂停等），第二级所选字段
    const dir = sortOrder === 'asc' ? 1 : -1
    const strSortFields = ['name', 'tracker', 'label', 'downloadDir', 'hashString', 'server']
    list = [...list].sort((a, b) => {
      let r = statusRank(a) - statusRank(b)
      if (r === 0) {
        if (strSortFields.includes(sortField)) {
          const strValue = (t: Torrent): string => {
            switch (sortField) {
              case 'name': return t.name
              case 'tracker': return t.trackerStats?.find((x) => !x.isBackup)?.host ?? t.trackerStats?.[0]?.host ?? ''
              case 'label': return t.labels?.[0] || ''
              case 'downloadDir': return t.downloadDir
              // 归属列按显示名排序；无数值的排在最前，与「-」占位一致
              case 'server': return t.serverName || ''
              default: return t.hashString
            }
          }
          r = strValue(a).localeCompare(strValue(b), undefined, { numeric: true })
        } else {
          r = sortValue(a, sortField) - sortValue(b, sortField)
        }
      }
      return r * dir
    })

    return list
  }, [torrents, filters, torrentSites, sortField, sortOrder])
}
