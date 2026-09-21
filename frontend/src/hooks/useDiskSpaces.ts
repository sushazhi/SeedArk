import { useEffect, useState } from 'react'
import { serverApi, sessionApi } from '@/api/torrent'
import { useAppStore } from '@/stores/appStore'
import type { DownloaderKind } from '@/types'

// 一台服务器的磁盘余量。ok=false 表示这台没查到（连不上等），界面显示 '--'
export interface DiskSpaceRow {
  key: string
  index: number | null
  name: string
  kind: DownloaderKind
  freeSpace: number
  totalSize: number
  ok: boolean
}

export interface DiskSpaces {
  // 聚合视图（≥2 台启用的服务器）：rows 逐台列出；单台部署走 single（环形图）
  aggregate: boolean
  rows: DiskSpaceRow[]
  single: { freeSpace: number; totalSize: number } | null
}

const EMPTY: DiskSpaces = { aggregate: false, rows: [], single: null }

// 磁盘余量（60s 刷新）。单台部署查活动连接；聚合视图逐台查各自的下载目录——
// /session/free-space 只覆盖活动那台，用它会把所有服务器都显示成同一块盘。
export function useDiskSpaces(active = true): DiskSpaces {
  const downloadDir = useAppStore((s) => s.session?.downloadDir ?? '')
  const [state, setState] = useState<DiskSpaces>(EMPTY)

  useEffect(() => {
    if (!active) {
      // 抽屉关闭等场景：清空，避免残留上一台服务器的数据
      setState(EMPTY)
      return
    }
    let cancelled = false
    let busy = false

    const load = async () => {
      if (busy) return
      busy = true
      try {
        let servers: { index: number; name: string; kind: DownloaderKind; enabled: boolean; url: string }[] = []
        try {
          const d = await serverApi.list()
          servers = (d.servers ?? []).map((s, i) => ({
            index: s.index ?? i,
            name: s.name,
            kind: (s.type ?? 'transmission') as DownloaderKind,
            enabled: s.enabled,
            url: s.url,
          }))
        } catch {
          // 列表读不到（旧版后端 / 网络抖动）就按单台处理，至少活动那台能显示
        }
        // 与后端 SyncAggregateTargets 同口径：启用且填了地址的才算成员
        const members = servers.filter((s) => s.enabled && s.url.trim() !== '')
        if (members.length >= 2) {
          const rows = await Promise.all(
            members.map(async (s): Promise<DiskSpaceRow> => {
              const base = { key: `srv-${s.index}`, index: s.index, name: s.name, kind: s.kind }
              try {
                const d = await sessionApi.freeSpaceAt(s.index)
                return { ...base, freeSpace: d.freeSpace, totalSize: d.totalSize, ok: true }
              } catch {
                return { ...base, freeSpace: 0, totalSize: 0, ok: false }
              }
            }),
          )
          if (!cancelled) setState({ aggregate: true, rows, single: null })
          return
        }
        if (!downloadDir) {
          if (!cancelled) setState(EMPTY)
          return
        }
        try {
          const d = await sessionApi.freeSpace(downloadDir)
          if (!cancelled) setState({ aggregate: false, rows: [], single: d })
        } catch {
          if (!cancelled) setState(EMPTY)
        }
      } finally {
        busy = false
      }
    }

    load()
    const id = setInterval(load, 60000)
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [active, downloadDir])

  return state
}
