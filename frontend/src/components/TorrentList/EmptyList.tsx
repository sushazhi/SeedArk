import { Inbox, SearchX, RotateCcw, WifiOff } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { hasFilterConditions, useAppStore } from '@/stores/appStore'

// 列表空态：区分「确实没有种子」与「被筛选条件过滤掉了」。
// 后者必须给出一键清除入口——筛选条件会被持久化，而「错误分布」这类条件会随种子
// 错误信息变化而失效；失效后列表长期空白，侧栏/状态栏却仍显示种子总数，
// 用户看不到任何筛选标记（历史上错误分布的筛选在顶栏没有对应胶囊），只会以为种子丢了。
export function EmptyList() {
  const { t } = useTranslation()
  const filters = useAppStore((s) => s.filters)
  const clearAllFilters = useAppStore((s) => s.clearAllFilters)
  const filtered = hasFilterConditions(filters)
  // 两种空态用不同图标：此前都是 AlertCircle，看上去都像"出错了"，
  // 而"还没有添加任何任务"是正常状态，不该带告警语义
  const Icon = filtered ? SearchX : Inbox

  return (
    <div
      className="flex flex-col items-center justify-center h-full gap-2 text-gray-400"
      style={{ paddingTop: 'var(--pad-top)', paddingBottom: 'var(--pad-bottom)' }}
    >
      <Icon className="w-10 h-10 opacity-40" aria-hidden />
      <span className="text-body">{filtered ? t('common.noMatch') : t('common.empty')}</span>
      {filtered && (
        <button
          type="button"
          onClick={clearAllFilters}
          className="tm-press inline-flex items-center gap-1.5 h-8 px-3 rounded-full border border-gray-200/80 dark:border-white/12 text-footnote text-primary hover:bg-primary/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/60"
        >
          <RotateCcw className="w-3.5 h-3.5" />
          {t('filter.clear')}
        </button>
      )}
    </div>
  )
}

// 断线且库中还没有数据时的空态。与「筛掉了一条不剩」和「确实没有任务」都不同：
// 连不上下载器时库本来就是空的，说「还没有添加任何任务」是错的，而且会给出一条
// 走不通的下一步。这里给出真实状态与唯一有用的动作（去设置里核对地址与凭据）
export function DisconnectedList({ onOpenSettings }: { onOpenSettings: () => void }) {
  const { t } = useTranslation()
  return (
    <div
      className="flex flex-col items-center justify-center h-full gap-2 text-gray-400"
      style={{ paddingTop: 'var(--pad-top)', paddingBottom: 'var(--pad-bottom)' }}
    >
      <WifiOff className="w-10 h-10 opacity-40" aria-hidden />
      <span className="text-body">{t('common.disconnected')}</span>
      <span className="max-w-xs text-center text-footnote text-gray-500 dark:text-gray-400">{t('common.disconnectedHint')}</span>
      <button
        type="button"
        onClick={onOpenSettings}
        className="tm-press mt-1 inline-flex items-center gap-1.5 h-8 px-3 rounded-full border border-gray-200/80 dark:border-white/12 text-footnote text-primary hover:bg-primary/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/60"
      >
        {t('common.settings')}
      </button>
    </div>
  )
}
