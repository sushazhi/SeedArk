import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

// PWA 更新提示（检测到新版本时提示刷新）
export function PwaUpdatePrompt() {
  const { t } = useTranslation()
  const [show, setShow] = useState(false)

  useEffect(() => {
    if (!('serviceWorker' in navigator)) return
    let refreshing = false
    // 页面刷新完成后重置
    navigator.serviceWorker.addEventListener('controllerchange', () => {
      if (refreshing) return
      refreshing = true
      window.location.reload()
    })

    let deferredPrompt: BeforeInstallPromptEvent | null = null
    const handleBeforeInstall = (e: Event) => {
      e.preventDefault()
      deferredPrompt = e as BeforeInstallPromptEvent
    }
    window.addEventListener('beforeinstallprompt', handleBeforeInstall)

    navigator.serviceWorker.ready.then((reg) => {
      reg.addEventListener('updatefound', () => {
        const newWorker = reg.installing
        if (!newWorker) return
        newWorker.addEventListener('statechange', () => {
          if (newWorker.state === 'installed' && navigator.serviceWorker.controller) {
            setShow(true)
          }
        })
      })
    })

    return () => {
      window.removeEventListener('beforeinstallprompt', handleBeforeInstall)
    }
  }, [])

  if (!show) return null
  return (
    // 与 Toaster 同一档材质：此前是不透明灰块，贴在玻璃界面上会断层
    <div className="fixed bottom-4 left-1/2 -translate-x-1/2 z-50 flex items-center gap-3 px-4 py-2.5 rounded-panel glass-panel-strong text-body text-gray-700 dark:text-gray-200">
      <span>{t('pwa.newVersion')}</span>
      <button className="font-medium text-primary hover:opacity-80" onClick={() => navigator.serviceWorker.getRegistration().then((r) => r?.update())}>
        {t('pwa.refresh')}
      </button>
      <button
        className="tm-hug grid h-6 w-6 place-items-center rounded-full text-gray-400 hover:text-gray-600 dark:hover:text-gray-200"
        aria-label={t('common.close')}
        onClick={() => setShow(false)}
      >
        ✕
      </button>
    </div>
  )
}

interface BeforeInstallPromptEvent extends Event {
  prompt: () => Promise<void>
}
