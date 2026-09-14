import * as React from "react"

import { cn } from "@/lib/utils"

const Input = React.forwardRef<HTMLInputElement, React.ComponentProps<"input">>(
  ({ className, type, ...props }, ref) => {
    // 默认写在前、{...props} 在后：调用方需要时仍可覆盖
    const inputMode =
      props.inputMode ??
      (type === "number" ? "decimal" : type === "url" ? "url" : type === "email" ? "email" : undefined)
    return (
      <input
        type={type}
        inputMode={inputMode}
        autoCapitalize="off"
        autoCorrect="off"
        spellCheck={false}
        className={cn(
          // 字号固定 text-body：此前是 text-subhead md:text-body，同一个输入框在
          // 窄屏与宽屏下相差一档，与其它控件（Select/Button 均 text-body）不同源。
          // 触屏端 ≥16px 的防缩放规则由 index.css 的 (pointer: coarse) 块统一承担
          "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-body shadow-sm transition-colors file:border-0 file:bg-transparent file:text-body file:font-medium file:text-foreground placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50",
          className
        )}
        ref={ref}
        {...props}
      />
    )
  }
)
Input.displayName = "Input"

export { Input }
