import * as React from 'react'
import * as SwitchPrimitives from '@radix-ui/react-switch'
import { cn } from '@/lib/utils'

const Switch = React.forwardRef<
  React.ElementRef<typeof SwitchPrimitives.Root>,
  React.ComponentPropsWithoutRef<typeof SwitchPrimitives.Root>
>(({ className, ...props }, ref) => (
  <SwitchPrimitives.Root
    className={cn(
      'peer inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-full border-2 border-transparent transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:cursor-not-allowed disabled:opacity-50 data-[state=checked]:bg-primary data-[state=unchecked]:bg-input',
      className
    )}
    {...props}
    ref={ref}
  >
    <SwitchPrimitives.Thumb
      className={cn(
        // iOS 开关手感：滑块位移走 spring（带回弹），与全站位移类动效同一曲线语言。
        // 阴影写成具体值而非 shadow-lg：滑块是开关里唯一的浮起元素，
        // 需要与玻璃阴影令牌同一档，shadow-lg 的默认黑色投影在浅色玻璃上过重
        'pointer-events-none block h-4 w-4 rounded-full bg-background shadow-[0_2px_6px_rgba(60,80,120,0.28)] dark:shadow-[0_2px_6px_rgba(0,0,0,0.45)] ring-0 transition-transform duration-[250ms] [transition-timing-function:var(--ease-spring)] data-[state=checked]:translate-x-4 data-[state=unchecked]:translate-x-0'
      )}
    />
  </SwitchPrimitives.Root>
))
Switch.displayName = SwitchPrimitives.Root.displayName

export { Switch }
