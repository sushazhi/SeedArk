import { useEffect, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { client, request } from '@/api/client'
import { APP_BASE } from '@/platform/appBase'
import { toast } from '@/lib/toast'
import { copyTextDetailed } from '@/utils/clipboard'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Button } from '@/components/ui/button'
import { Copy } from 'lucide-react'

// MCP 服务：AI 客户端接入的开关与凭据（状态与持久化都在后端 /settings，全部即时保存）
export function McpManager({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t } = useTranslation()
  const [enabled, setEnabled] = useState(false)
  const [allowDelete, setAllowDelete] = useState(false)
  const [allowDangerous, setAllowDangerous] = useState(false)
  const [token, setToken] = useState('')
  const [draft, setDraft] = useState('')
  const [mcpPort, setMcpPort] = useState('')
  const [portDraft, setPortDraft] = useState('')

  useEffect(() => {
    if (!open) return
    let cancelled = false
    request(clientGetSettings()).then((d) => {
      if (cancelled) return
      const data = d as { mcpEnabled?: boolean; mcpAllowDelete?: boolean; mcpAllowDangerous?: boolean; mcpToken?: string; mcpPort?: string }
      setEnabled(!!data.mcpEnabled)
      setAllowDelete(!!data.mcpAllowDelete)
      setAllowDangerous(!!data.mcpAllowDangerous)
      setToken(data.mcpToken ?? '')
      setDraft(data.mcpToken ?? '')
      setMcpPort(data.mcpPort ?? '')
      setPortDraft(data.mcpPort ?? '')
    }).catch(() => {})
    return () => { cancelled = true }
  }, [open])

  const patch = async (
    body: { mcpEnabled?: boolean; mcpAllowDelete?: boolean; mcpAllowDangerous?: boolean; mcpToken?: string; mcpPort?: string },
    apply: () => void,
  ) => {
    try {
      await request(clientPutSettings(body))
      apply()
      toast.success(t('toast.updated'))
    } catch {
      // 拦截器已提示
    }
  }

  // 令牌草稿：失焦/回车且内容变化时提交一次，Esc 还原。
  // 清空令牌前必须先关掉直连端口：该端口不受面板认证保护，无令牌即等于对外开放
  const commitToken = () => {
    const next = draft.trim()
    if (next === token) return
    if (!next && mcpPort) {
      toast.error(t('session.mcp.tokenNeedsPort'))
      setDraft(token)
      return
    }
    setDraft(next)
    void patch({ mcpToken: next }, () => setToken(next))
  }

  // 随机生成新令牌并直接提交生效；客户端需同步更新
  const regenerateToken = () => {
    const rand = Array.from(crypto.getRandomValues(new Uint8Array(24)), (b) => b.toString(16).padStart(2, '0')).join('')
    const next = `trp_${rand}`
    setDraft(next)
    void patch({ mcpToken: next }, () => setToken(next))
  }

  // 端口草稿：失焦/回车提交；监听器随进程绑定，保存后重启才生效。
  // 直连端口不受面板认证保护，后端要求「端口非空则令牌必须非空」；令牌若还是
  // 未提交的草稿，必须与端口一次性提交（分两次会整文件重写互相覆盖）
  const commitPort = () => {
    const next = portDraft.trim()
    if (next === mcpPort) return
    const pendingToken = draft.trim()
    if (next && !token && !pendingToken) {
      toast.error(t('session.mcp.portNeedsToken'))
      setPortDraft(mcpPort)
      return
    }
    setPortDraft(next)
    if (next && pendingToken && pendingToken !== token) {
      void patch({ mcpToken: pendingToken, mcpPort: next }, () => { setToken(pendingToken); setMcpPort(next) })
      return
    }
    void patch({ mcpPort: next }, () => setMcpPort(next))
  }

  const copy = (text: string) => {
    void copyTextDetailed(text).then((r) => {
      if (r === 'ok') toast.success(t('toast.copied'))
      else if (r === 'blocked') toast.error(t('toast.copyBlocked'))
      else toast.error(t('toast.copyFailed'))
    })
  }

  // 网关部署（APP_BASE 非空，如飞牛的 /app/transmission）下，外部 AI 客户端没有宿主
  // 登录态：实测 fnOS 1.2.0604 的网关对 /app/** 未携带宿主票据的请求一律短路返回纯文本
  // "invalid token"（HTTP 200），走网关的 /mcp 根本到不了本服务。因此网关模式必须使用
  // 直连端口；启用 MCP 却未配置端口时不再展示一个注定连不上的地址与配置，改为提示先填端口。
  // 未启用 MCP 时不提示——那是尚未开始配置，凭空催促没有意义。
  const gatewayMode = APP_BASE !== ''
  const needsDirectPort = gatewayMode && enabled && !mcpPort
  const endpoint = mcpPort
    ? `http://${window.location.hostname}:${mcpPort}/mcp`
    : `${window.location.origin}${APP_BASE}/mcp`
  const serverConfig: { type: string; url: string; headers?: Record<string, string> } = { type: 'http', url: endpoint }
  if (token) serverConfig.headers = { Authorization: `Bearer ${token}` }
  const clientConfig = JSON.stringify({ mcpServers: { seedark: serverConfig } })

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="sm:max-w-[540px] h-[80dvh] flex flex-col p-0">
        <DialogHeader className="px-4 pt-4 pb-2 border-b border-gray-200/40 dark:border-gray-700/30">
          <DialogTitle>{t('session.mcp.title')}</DialogTitle>
        </DialogHeader>
        {/* flex-1 min-h-0：固定高度面板超高时由本区内部滚动，同自动文件管理/做种策略面板
            （WebView 下靠外层 fixed 容器的页面级滚动会失效，内容超出后被裁住） */}
        <div className="flex-1 min-h-0 overflow-y-auto px-4 py-3 space-y-4">
          <div className="space-y-1">
            <Row label={t('session.mcp.enabled')} hint={t('session.mcp.enabledHint')}>
              <Switch checked={enabled} onCheckedChange={(v) => void patch({ mcpEnabled: v }, () => setEnabled(v))} />
            </Row>
            <Row label={t('session.mcp.allowDelete')} hint={t('session.mcp.allowDeleteHint')}>
              <Switch checked={allowDelete} disabled={!enabled} onCheckedChange={(v) => void patch({ mcpAllowDelete: v }, () => setAllowDelete(v))} />
            </Row>
            <Row label={t('session.mcp.allowDangerous')} hint={t('session.mcp.allowDangerousHint')}>
              <Switch checked={allowDangerous} disabled={!enabled} onCheckedChange={(v) => void patch({ mcpAllowDangerous: v }, () => setAllowDangerous(v))} />
            </Row>
            <Row label={t('session.mcp.token')} hint={t('session.mcp.tokenHint')}>
              <div className="flex items-center gap-2 shrink-0">
                <Input
                  aria-label={t('session.mcp.token')}
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                  onBlur={commitToken}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') { e.preventDefault(); commitToken() }
                    if (e.key === 'Escape') { setDraft(token); e.currentTarget.blur() }
                  }}
                  autoComplete="off"
                  autoCapitalize="off"
                  spellCheck={false}
                  placeholder={t('session.mcp.tokenPlaceholder')}
                  className="h-8 text-footnote w-44"
                />
                <Button variant="outline" size="sm" className="h-8 shrink-0" onClick={regenerateToken}>
                  {t('session.mcp.regenerate')}
                </Button>
              </div>
            </Row>
            <Row label={t('session.mcp.port')} hint={t('session.mcp.portHint')}>
              <Input
                aria-label={t('session.mcp.port')}
                value={portDraft}
                onChange={(e) => setPortDraft(e.target.value)}
                onBlur={commitPort}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') { e.preventDefault(); commitPort() }
                  if (e.key === 'Escape') { setPortDraft(mcpPort); e.currentTarget.blur() }
                }}
                autoComplete="off"
                inputMode="numeric"
                placeholder="如 8082"
                className="h-8 text-footnote w-28 tm-mono"
              />
            </Row>
          </div>
          <div className="space-y-2.5">
            {/* 接入信息只在「MCP 已启用且地址真的可用」时展示：未启用时是尚未开始配置，
                网关部署缺端口时是注定连不上，两种情况都不该给出可照抄的地址与配置 */}
            {!enabled ? (
              <p className="text-caption1 text-gray-400">{t('session.mcp.disabledHint')}</p>
            ) : needsDirectPort ? (
              <p className="text-caption1 text-amber-600 dark:text-amber-400">{t('session.mcp.gatewayNeedsPort')}</p>
            ) : (
              <>
                <div>
                  <div className="flex items-center justify-between gap-3">
                    <span className="text-body text-gray-600 dark:text-gray-300">{t('session.mcp.endpoint')}</span>
                    <CopyButton label={t('session.mcp.endpointCopy')} onClick={() => copy(endpoint)} />
                  </div>
                  <p className="tm-mono text-caption1 text-gray-500 dark:text-gray-400 break-all mt-1">{endpoint}</p>
                </div>
                <div>
                  <div className="flex items-center justify-between gap-3">
                    <span className="text-body text-gray-600 dark:text-gray-300">{t('session.mcp.configJson')}</span>
                    <CopyButton label={t('session.mcp.configJsonCopy')} onClick={() => copy(clientConfig)} />
                  </div>
                  <pre className="tm-mono text-caption1 text-gray-500 dark:text-gray-400 bg-gray-100 dark:bg-gray-800 rounded-lg p-2.5 break-all whitespace-pre-wrap mt-1.5">
                    {clientConfig}
                  </pre>
                </div>
                <p className="text-caption1 text-gray-400">{t('session.mcp.configJsonHint')}</p>
                {/* gatewayHint 讲的是网关部署的接入方式：直连部署（Docker 等）下与用户无关，不展示 */}
                {gatewayMode && <p className="text-caption1 text-gray-400">{t('session.mcp.gatewayHint')}</p>}
              </>
            )}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}

// 与 SettingsModal 的 Row 同构：标题与说明在左、控件在右。
// flex-wrap：窄屏下右侧定宽控件放不下时整块掉到标签下方，否则标签被挤成一字一行的竖排，行高爆炸（WebView 真机曾出现）
function Row({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1.5 py-1.5">
      <div className="min-w-[10rem] max-w-full">
        <span className="text-body text-gray-600 dark:text-gray-300">{label}</span>
        {hint && <p className="text-caption1 text-gray-400 mt-0.5">{hint}</p>}
      </div>
      {children}
    </div>
  )
}

function CopyButton({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      aria-label={label}
      onClick={onClick}
      // 32px：规范里桌面最小命中区。此前 28px，与设置面板内其它图标按钮不同档
      className="tm-hug w-8 h-8 shrink-0 rounded-full flex items-center justify-center text-gray-400 hover:text-primary hover:bg-primary/10 active:bg-primary/10"
    >
      <Copy className="w-3.5 h-3.5" aria-hidden />
    </button>
  )
}

const clientGetSettings = () => client.get('/settings')
const clientPutSettings = (body: { mcpEnabled?: boolean; mcpAllowDelete?: boolean; mcpAllowDangerous?: boolean; mcpToken?: string; mcpPort?: string }) =>
  client.put('/settings', body)
