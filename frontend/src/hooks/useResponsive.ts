import { useEffect, useState } from 'react'

const MOBILE_MQ = '(max-width: 767px)'
// 与 index.css 里的触屏适配块同一口径：无 hover 能力或粗指针即按触屏处理
const COARSE_MQ = '(hover: none), (pointer: coarse)'

function subscribe(query: string, set: (v: boolean) => void) {
  const mq = window.matchMedia(query)
  const handler = (e: MediaQueryListEvent) => set(e.matches)
  set(mq.matches)
  mq.addEventListener('change', handler)
  return () => mq.removeEventListener('change', handler)
}

/**
 * 布局断点与输入能力两件事分开判：
 * isMobile 只看视口宽度（决定抽屉/侧栏这类布局），
 * isCoarse 看指针能力（决定长按、命中区、表格还是卡片这类交互）。
 * 平板两个都可能是 false/true 的组合，混用会拿鼠标交互的表格给触屏用户。
 */
export function useResponsive() {
  // 首帧也走同一条 MQ：此前 isMobile 用 window.innerWidth < 768 初始化，而订阅的是
  // (max-width: 767px)——两者口径不同（innerWidth 含滚动条、断点边界 767/768 互斥），
  // 带滚动条的桌面端会出现"首帧按手机渲染、effect 后才切回"的布局闪烁
  const [isMobile, setIsMobile] = useState(() =>
    typeof window !== 'undefined' ? window.matchMedia(MOBILE_MQ).matches : false,
  )
  const [isCoarse, setIsCoarse] = useState(() =>
    typeof window !== 'undefined' ? window.matchMedia(COARSE_MQ).matches : false,
  )

  useEffect(() => {
    const offMobile = subscribe(MOBILE_MQ, setIsMobile)
    const offCoarse = subscribe(COARSE_MQ, setIsCoarse)
    return () => {
      offMobile()
      offCoarse()
    }
  }, [])

  return { isMobile, isCoarse }
}
