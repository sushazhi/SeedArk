import { useEffect, useRef, useState, type CSSProperties, type RefObject } from 'react'

/** 视口安全边距 */
const MARGIN = 8

export interface MenuPos {
  x: number
  y: number
}

/**
 * 让浮动层在视口内自动翻转 + 双向夹取，并给出可用的 transform-origin。
 *
 * 抽成公共 hook 的原因：右键菜单在 SeedMenu / 侧栏 / 表头三处各有一份实现，
 * 侧栏那两份此前用 `Math.min(x, innerWidth - 180)` 这类写法——尺寸写死、
 * 只用 innerWidth/innerHeight（iOS 键盘弹出时不变，visualViewport 才反映可见区），
 * 且只夹取右上、左下溢出仍然出屏。这里统一按实测尺寸处理。
 *
 * 返回的 ref 必须挂到浮动层根节点上：位置在首帧渲染后按真实尺寸修正。
 */
export function useFloatingMenuPosition<T extends HTMLElement = HTMLDivElement>(pos: MenuPos) {
  const ref = useRef<T>(null)
  const [style, setStyle] = useState<CSSProperties>({ left: pos.x, top: pos.y })

  useEffect(() => {
    const el = ref.current
    if (!el) return
    const rect = el.getBoundingClientRect()
    // iOS 键盘弹出时 innerHeight 不变、visualViewport 才反映可见区域
    const vw = window.visualViewport?.width ?? window.innerWidth
    const vh = window.visualViewport?.height ?? window.innerHeight
    const flipX = pos.x + rect.width > vw - MARGIN ? pos.x - rect.width : pos.x
    const flipY = pos.y + rect.height > vh - MARGIN ? pos.y - rect.height : pos.y
    // 两侧都夹取：触屏长按落点常贴着屏幕右缘，只翻转不夹取仍会溢出
    setStyle({
      left: Math.min(Math.max(MARGIN, flipX), Math.max(MARGIN, vw - rect.width - MARGIN)),
      top: Math.min(Math.max(MARGIN, flipY), Math.max(MARGIN, vh - rect.height - MARGIN)),
      // 入场缩放从落点长出：未翻转的方向取贴近落点的一侧为原点
      transformOrigin: `${flipX === pos.x ? 'left' : 'right'} ${flipY === pos.y ? 'top' : 'bottom'}`,
    })
  }, [pos])

  return { ref, style }
}

/**
 * 点击外部 / Esc / 滚动即关闭，窗口失焦也关闭。
 * pointerdown 同时覆盖鼠标与手指：mousedown 在触屏上迟到，会让菜单关不掉。
 */
export function useDismissOnOutside(
  ref: RefObject<HTMLElement | null>,
  onClose: () => void,
  enabled = true,
) {
  useEffect(() => {
    if (!enabled) return
    const close = () => onClose()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    // 滚动/触屏滑动仅当发生在菜单外部时关闭，菜单内部滚动不受影响
    const onWheel = (e: WheelEvent | TouchEvent) => {
      if (!ref.current?.contains(e.target as Node)) onClose()
    }
    window.addEventListener('pointerdown', close)
    window.addEventListener('wheel', onWheel, { passive: true })
    window.addEventListener('touchmove', onWheel, { passive: true })
    window.addEventListener('keydown', onKey)
    window.addEventListener('blur', close)
    return () => {
      window.removeEventListener('pointerdown', close)
      window.removeEventListener('wheel', onWheel)
      window.removeEventListener('touchmove', onWheel)
      window.removeEventListener('keydown', onKey)
      window.removeEventListener('blur', close)
    }
  }, [ref, onClose, enabled])
}
