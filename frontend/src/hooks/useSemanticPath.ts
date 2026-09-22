import { useCallback, useEffect, useRef, useState } from 'react'
import { torrentApi } from '@/api/torrent'
import { usePlatform } from '@/platform'
import { useAppStore } from '@/stores/appStore'

/** 展示层路径转换：命中语义映射则显示宿主展示名，否则回退原始路径（纯读取，无副作用） */
export function useSemanticPath() {
  const semanticDirs = useAppStore((s) => s.semanticDirs)
  return useCallback((raw: string) => semanticDirs[raw] ?? raw, [semanticDirs])
}

/**
 * 确保这批路径已请求过语义转换（仅 fnOS；结果写入全局映射，由 useSemanticPath 展示）。
 *
 * 把 /vol1/... 转成宿主展示名属增强：失败按「暂不可用」处理，退避重试后仍失败才
 * 本会话放弃——此前任何一次失败都直接永久放弃，宿主一次瞬时抖动就会让整个会话都
 * 看不到语义路径（只有切语言才能重置）。故用指数退避：瞬时故障可自愈，持续故障也
 * 不会在 2s 轮询下反复打转换接口。连续失败 3 次即认为宿主侧不可用，放弃本会话。
 *
 * 入参不必 memo：内部按「去重后的内容」做依赖，路径集合没变就不会重复请求。
 */
export function useEnsureSemanticPaths(paths: string[]) {
  const { ready, can } = usePlatform()
  const canSemantic = ready && can('paths.semantic')
  const language = useAppStore((s) => s.language)
  const setSemanticDirs = useAppStore((s) => s.setSemanticDirs)
  const [attempt, setAttempt] = useState(0)

  // 语言切换后旧映射全部失效（宿主按语言返回展示名）：清空并重置退避，让下方
  // effect 重新全量拉取。首挂载不重置——映射是全局共享的，调用方（种子列表之外
  // 还有设置面板）后挂载时清空，会让已经在展示的语义路径短暂退回原始路径。
  const langRef = useRef(language)
  useEffect(() => {
    if (langRef.current === language) return
    langRef.current = language
    useAppStore.getState().resetSemantic()
    setAttempt(0)
  }, [language])

  const key = Array.from(new Set(paths.filter(Boolean))).join('\u0000')

  useEffect(() => {
    if (!canSemantic) return
    const known = useAppStore.getState().semanticDirs
    const missing = (key ? key.split('\u0000') : []).filter((p) => !(p in known))
    if (missing.length === 0) return
    // 退避档位：0s（首次）→ 3s → 15s；用尽后本会话不再重试
    const backoff = [0, 3000, 15000][attempt]
    if (backoff === undefined) return
    const timer = setTimeout(() => {
      torrentApi
        .semanticPaths(missing, language)
        .then((res) => {
          if (res.available) {
            setSemanticDirs(res.map)
            // 成功即复位：后续新出现的路径仍走首次快速路径
            setAttempt(0)
          } else {
            setAttempt((n) => n + 1)
          }
        })
        .catch(() => setAttempt((n) => n + 1))
    }, backoff)
    return () => clearTimeout(timer)
  }, [key, language, canSemantic, setSemanticDirs, attempt])
}
