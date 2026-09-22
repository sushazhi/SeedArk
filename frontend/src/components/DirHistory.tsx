import { useTranslation } from 'react-i18next'
import { toast } from '@/lib/toast'

// 目录历史（记住的用过的下载目录）在本地存储的 key，添加种子与批量改目录共用一份
export const DIR_HISTORY_KEY = 'tm_dirs'

// 就近清除入口。历史建议由浏览器原生 datalist 渲染，里面塞不进动作项（也无法监听点击），
// 所以只能挂在输入框旁边；无历史时不出现，避免一排输入框下面多一行常年灰着的空操作。
export function ClearDirHistory({ count, onClear }: { count: number; onClear: () => void }) {
  const { t } = useTranslation()
  if (count === 0) return null
  return (
    <button
      type="button"
      onClick={() => {
        localStorage.removeItem(DIR_HISTORY_KEY)
        // 调用方的列表 state 是本组件挂载时读进来的，只清存储会让本次会话继续留着旧项
        onClear()
        toast.success(t('toast.updated'))
      }}
      className="tm-hug block w-fit py-1 text-caption1 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300"
    >
      {t('common.clearDirHistory')}
    </button>
  )
}
