import { useId, useState } from 'react'
import { X } from 'lucide-react'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'

// 标签输入（替代 antd Select mode="tags"）
export function TagInput({
  value,
  onChange,
  placeholder,
  suggestions,
  className,
}: {
  value: string[]
  onChange: (v: string[]) => void
  placeholder?: string
  suggestions?: string[]
  className?: string
}) {
  const [text, setText] = useState('')
  const [focused, setFocused] = useState(false)
  const inputId = useId()

  const commit = (raw: string) => {
    const tags = raw.split(/[,，\n]/).map((s) => s.trim()).filter(Boolean)
    if (tags.length > 0) {
      onChange(Array.from(new Set([...value, ...tags])))
      setText('')
    }
  }

  const shownSuggestions = focused && suggestions ? suggestions.filter((s) => !value.includes(s) && s.toLowerCase().includes(text.toLowerCase())).slice(0, 200) : []

  return (
    <div
      className={cn(
        'flex flex-wrap items-center gap-1 rounded-md border border-input bg-transparent px-2 py-1 min-h-9 transition-colors relative',
        focused && 'ring-1 ring-ring',
        className,
      )}
      onClick={() => document.getElementById(inputId)?.focus()}
    >
      {value.map((tag) => (
        <Badge key={tag} className="bg-primary/10 text-primary border-transparent gap-1 pr-1">
          {tag}
          <button
            onClick={(e) => { e.stopPropagation(); onChange(value.filter((x) => x !== tag)) }}
            className="hover:text-red-500"
          >
            <X className="w-3 h-3" />
          </button>
        </Badge>
      ))}
      <Input
        id={inputId}
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ',') {
            e.preventDefault()
            commit(text)
          } else if (e.key === 'Backspace' && !text && value.length > 0) {
            onChange(value.slice(0, -1))
          }
        }}
        onBlur={() => { commit(text); setFocused(false) }}
        onFocus={() => setFocused(true)}
        placeholder={value.length === 0 ? placeholder : ''}
        className="flex-1 min-w-24 h-8 border-0 bg-transparent px-1 text-body shadow-none focus-visible:ring-0 focus-visible:outline-none"
      />
      {shownSuggestions.length > 0 && (
        <div className="absolute left-0 right-0 top-full mt-1 z-20 rounded-md glass-panel-solid max-h-56 overflow-y-auto overscroll-contain">
          {shownSuggestions.map((s) => (
            <button
              key={s}
              className="w-full text-left px-3 py-1.5 text-body hover:bg-accent focus-visible:outline-none focus-visible:bg-accent"
              onMouseDown={(e) => { e.preventDefault(); commit(s) }}
            >
              {s}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

export default TagInput
