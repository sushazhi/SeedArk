// qBittorrent 设置面板：完全按驱动自述（schema）通用渲染。
// 键名、枚举、单位、换算系数、只读/只写/危险标记都来自驱动一侧，
// 本组件不含任何 qBittorrent 专有知识——上游增删偏好键只需改驱动，
// 面板与翻译都不用动（字段标题由 schema 自带 zh/en 文案）。
import { useEffect, useState, type ReactNode } from 'react'
import { FolderOpen } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { useEnsureSemanticPaths, useSemanticPath } from '@/hooks/useSemanticPath'
import { usePlatform } from '@/platform'
import { cn } from '@/lib/utils'
import type { SettingsField, SettingsSection } from '@/types'
import { Group, NumInput, Row, Section, SmallSelect } from './controls'

// 一节里键太多时按「是否启用 / 是否危险 / 只读」自动分组。
// 这不是 qBittorrent 专有知识，而是通用的信息分层：一屏十几个同权重的行，
// 用户没有任何线索知道哪几行是"当前生效的总开关"。阈值取 9，
// 低于它的节（如「行为」）保持平铺，不为了分组而分组。
const GROUP_THRESHOLD = 9

// 时刻选项：15 分钟一档（qBittorrent WebUI 的调度器粒度）
const TIME_OPTIONS = Array.from({ length: 96 }, (_, i) => {
  const s = `${String(Math.floor(i / 4)).padStart(2, '0')}:${String((i % 4) * 15).padStart(2, '0')}`
  return { value: s, label: s }
})

// 路径字段（驱动自述的 path 类型）。单独占一整行：路径普遍偏长，与标签同行只能
// 看见开头。有宿主能力时给出目录选择器，选中即填入草稿——仍走面板顶部的「保存」
// 提交，与同面板其它键一致。下方灰字是宿主语义路径（如飞牛的存储空间展示名），
// 只作提示：提交给下载器的始终是它命名空间里的原始路径。
function PathField({ label, value, disabled, onChange }: {
  label: string
  value: string
  disabled?: boolean
  onChange: (v: string) => void
}) {
  const { t } = useTranslation()
  const { can, pickFolder } = usePlatform()
  const sem = useSemanticPath()
  const semantic = value ? sem(value) : ''

  return (
    <div className="w-full min-w-0 flex flex-col gap-1">
      <div className="flex items-center gap-1.5 w-full">
        <Input
          aria-label={label}
          value={value}
          disabled={disabled}
          autoCapitalize="off"
          autoCorrect="off"
          spellCheck={false}
          title={value || undefined}
          onChange={(e) => onChange(e.target.value)}
          className="h-8 text-footnote flex-1 min-w-0"
        />
        {can('fs.pickFolder') && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={disabled}
            title={t('action.selectDir')}
            onClick={async () => {
              const picked = await pickFolder()
              // 取消选择（宿主没回路径）不动现有取值
              if (picked) onChange(picked)
            }}
            className="h-8 shrink-0 gap-1 px-2"
          >
            <FolderOpen className="w-3.5 h-3.5" />
            <span className="hidden sm:inline">{t('action.selectDir')}</span>
          </Button>
        )}
      </div>
      {semantic && semantic !== value && (
        <p className="text-caption1 text-gray-400 truncate" title={semantic}>→ {semantic}</p>
      )}
    </div>
  )
}

interface QBFieldProps {
  field: SettingsField
  lang: 'zh' | 'en'
  value: unknown
  changed: boolean
  onChange: (v: unknown) => void
}

function QBField({ field, lang, value, changed, onChange }: QBFieldProps) {
  const { t } = useTranslation()
  const label = lang === 'en' && field.labelEn ? field.labelEn : field.label
  const hint = lang === 'en' && field.hintEn ? field.hintEn : field.hint

  let control: ReactNode
  switch (field.type) {
    case 'bool':
      control = (
        <Switch
          aria-label={label}
          checked={value === true}
          disabled={field.readOnly}
          onCheckedChange={(v) => onChange(v)}
        />
      )
      break
    case 'int':
    case 'float':
      if (field.readOnly) {
        control = <span className="text-body tm-mono text-gray-500">{String(value ?? '')}</span>
      } else {
        control = (
          <div className="flex items-center gap-1.5">
            <NumInput
              aria-label={label}
              value={typeof value === 'number' ? value : undefined}
              min={field.min}
              max={field.max}
              step={field.type === 'float' ? 0.1 : 1}
              onChange={(v) => onChange(v == null ? null : v)}
            />
            {field.unit && <span className="text-caption1 text-gray-400 w-16">{field.unit}</span>}
          </div>
        )
      }
      break
    case 'select': {
      const opts = (field.options ?? []).map((o) => ({
        value: o.value,
        label: lang === 'en' && o.labelEn ? o.labelEn : o.label,
      }))
      // 上游取值可能不在自述的枚举里（版本差异/自定义配置）：补进选项，
      // 否则 Select 会显示空白，用户一保存就把这个值悄悄改掉
      const cur = value == null ? '' : String(value)
      if (cur && !opts.some((o) => o.value === cur)) opts.push({ value: cur, label: `${cur}（未知）` })
      control = (
        <SmallSelect
          aria-label={label}
          value={cur}
          onValueChange={(v) => onChange(field.valueType === 'int' ? Number(v) : v)}
          options={opts}
          className="w-40"
        />
      )
      break
    }
    case 'time': {
      const cur = typeof value === 'string' ? value : ''
      const opts = cur && !TIME_OPTIONS.some((o) => o.value === cur) ? [...TIME_OPTIONS, { value: cur, label: cur }] : TIME_OPTIONS
      control = <SmallSelect aria-label={label} value={cur} onValueChange={onChange} options={opts} />
      break
    }
    case 'text': {
      // 上游的列表型偏好以换行分隔的字符串传输
      const cur = typeof value === 'string' ? value : Array.isArray(value) ? value.join('\n') : ''
      control = (
        <textarea
          aria-label={label}
          rows={3}
          autoCapitalize="off"
          autoCorrect="off"
          spellCheck={false}
          value={cur}
          disabled={field.readOnly}
          placeholder={t('common.eachLineOne')}
          onChange={(e) => onChange(e.target.value)}
          className="w-full max-h-40 appearance-none resize-y rounded-md border border-input bg-transparent px-3 py-2 text-footnote shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-60"
        />
      )
      break
    }
    case 'path': {
      control = (
        <PathField
          label={label}
          value={typeof value === 'string' ? value : value == null ? '' : String(value)}
          disabled={field.readOnly}
          onChange={onChange}
        />
      )
      break
    }
    default: {
      if (field.readOnly) {
        control = <span className="text-caption1 tm-mono text-gray-500 break-all max-w-[16rem] text-right">{String(value ?? '')}</span>
      } else {
        control = (
          <Input
            aria-label={label}
            type={field.secret ? 'password' : 'text'}
            value={typeof value === 'string' ? value : value == null ? '' : String(value)}
            autoComplete={field.secret ? 'new-password' : 'off'}
            placeholder={field.writeOnly ? t('session.qbKeepUnchanged') : undefined}
            onChange={(e) => onChange(e.target.value)}
            className="h-8 text-footnote w-full md:w-1/2"
          />
        )
      }
    }
  }

  return (
    <>
      <Row label={field.danger ? `${label} ⚠` : label} hint={hint}>
        {/* 这层包装必须跟着 path 撑满整行：它是 Row 的收缩包裹项，
            里面的 w-full 只会退化成控件固有宽度（约 154px），路径照样看不全 */}
        <div className={cn('flex items-center gap-1.5', field.type === 'path' && 'w-full')}>
          {changed && <span className="h-1.5 w-1.5 rounded-full bg-amber-500 shrink-0" aria-hidden />}
          {control}
        </div>
      </Row>
      {field.danger && !field.readOnly && (
        <p className="text-caption1 text-amber-600 dark:text-amber-400 -mt-1 mb-1">{t('session.qbDangerHint')}</p>
      )}
    </>
  )
}

// qB 草稿 + 保存条。草稿必须跨节存活：桌面端同一时刻只渲染左栏选中的那一节，
// 若草稿挂在节里，改完「连接」翻到「速度」再点保存，前一批改动就丢了。
// 所以草稿状态由调用方（SettingsModal）持有，本 hook 只提供状态与提交逻辑。
export function useQBDraft(values: Record<string, unknown>, onSave: (patch: Record<string, unknown>) => Promise<boolean>) {
  const [draft, setDraft] = useState<Record<string, unknown>>({})
  const [saving, setSaving] = useState(false)

  // 服务端取值换了（切换服务器标签、保存后回读）就丢弃草稿：
  // 草稿只是「相对当前取值的改动」，换了基准再留着会把旧值写回新服务器。
  //
  // 依赖必须用「取值的指纹」而不是 values 这个对象本身：调用方写的是
  // `session?.qb ?? {}`，当会话里没有 qb 字段（Transmission 连接、或 qB 会话
  // 还没读回来）时每次渲染都会新建一个 {}，以对象为依赖会让 effect 每轮都跑，
  // setDraft({}) 又触发下一轮渲染 —— 直接打成 "Maximum update depth exceeded"。
  // 指纹不进 useMemo：算它本身很便宜，而 memo 的依赖照样是那个每次新建的对象，
  // 等于没省。
  const valuesKey = (() => {
    try {
      return JSON.stringify(values ?? {})
    } catch {
      // 取值理论上都是 JSON 可序列化的；真出意外时退回空串，宁可不清草稿也不要死循环
      return ''
    }
  })()
  useEffect(() => {
    setDraft({})
  }, [valuesKey])

  return { draft, setDraft, saving, submit: onSave }
}

// 改动条：有未保存改动时吸顶显示（跨节改动时用户在别的节也要能保存）
export function QBSaveBar({ draft, saving, setDraft, submit }: {
  draft: Record<string, unknown>
  saving: boolean
  setDraft: (d: Record<string, unknown>) => void
  submit: (patch: Record<string, unknown>) => Promise<boolean>
}) {
  const { t } = useTranslation()
  const changedKeys = Object.keys(draft)
  if (changedKeys.length === 0) return null

  const save = async () => {
    if (saving) return
    // 失败时保留草稿，让用户修正后重试（错误提示由请求拦截器统一弹出）
    if (await submit(draft)) setDraft({})
  }

  return (
    <div className="sticky top-0 z-10 -mx-1 px-2 py-2 mb-1 rounded-lg glass-panel-solid border border-amber-300/60 dark:border-amber-500/40 flex items-center justify-between gap-2">
      <span className="text-footnote text-amber-700 dark:text-amber-300">
        {t('session.qbPending', { count: changedKeys.length })}
      </span>
      <div className="flex items-center gap-1.5 shrink-0">
        <Button size="sm" variant="outline" className="h-8 text-footnote" disabled={saving} onClick={() => setDraft({})}>
          {t('session.qbDiscard')}
        </Button>
        <Button size="sm" className="h-8 text-footnote" disabled={saving} onClick={() => void save()}>
          {saving ? t('common.loading') : t('session.qbSave')}
        </Button>
      </div>
    </div>
  )
}

interface QBSettingsProps {
  schema: SettingsSection[]
  values: Record<string, unknown>
  // 提交改动（只含改过的键，原生键名 → 原生取值）；返回是否成功，失败时草稿保留
  onSave: (patch: Record<string, unknown>) => Promise<boolean>
}

// 移动端路径：单列长滚动，一次渲染完整份 schema。
// 桌面端不走这里——那个布局要把每节拆成独立的导航项（见 SettingsModal）。
export function QBSettings({ schema, values, onSave }: QBSettingsProps) {
  const { i18n } = useTranslation()
  const lang: 'zh' | 'en' = (i18n.language || 'zh').startsWith('en') ? 'en' : 'zh'
  const { draft, setDraft, saving, submit } = useQBDraft(values, onSave)
  const [busy, setBusy] = useState(false)

  return (
    <>
      <QBSaveBar
        draft={draft}
        saving={busy || saving}
        setDraft={setDraft}
        submit={async (patch) => {
          setBusy(true)
          try {
            return await submit(patch)
          } finally {
            setBusy(false)
          }
        }}
      />
      {schema.map((sec) => (
        <Section key={sec.key} id={`qb-${sec.key}`} title={lang === 'en' && sec.labelEn ? sec.labelEn : sec.label}>
          <QBFields sec={sec} lang={lang} values={values} draft={draft} setDraft={setDraft} />
        </Section>
      ))}
    </>
  )
}

// 字段渲染：把一行字段画出来（含危险项折叠分组）
export type DraftSetter = (fn: (d: Record<string, unknown>) => Record<string, unknown>) => void

// 一节的主体：键少时平铺，键多时把只读/危险项收进折叠组。
// 导出给桌面端的分节视图复用（它以自身的分节清单渲染，绕开 QBSettings 那层 schema 循环）。
export function QBFields({ sec, lang, values, draft, setDraft }: {
  sec: SettingsSection
  lang: 'zh' | 'en'
  values: Record<string, unknown>
  draft: Record<string, unknown>
  setDraft: DraftSetter
}) {
  const { t } = useTranslation()
  // 本节路径字段的语义映射：种子列表之外，设置面板里的路径同样显示宿主展示名
  useEnsureSemanticPaths(
    sec.fields
      .filter((f) => f.type === 'path')
      .map((f) => String((f.key in draft ? draft[f.key] : values[f.key]) ?? '')),
  )
  const render = (f: SettingsField) => (
    <QBField
      key={f.key}
      field={f}
      lang={lang}
      value={f.key in draft ? draft[f.key] : values[f.key]}
      changed={f.key in draft}
      onChange={(v) => setDraft((d) => ({ ...d, [f.key]: v }))}
    />
  )

  if (sec.fields.length <= GROUP_THRESHOLD) return <>{sec.fields.map(render)}</>

  // 只读与危险项排在最后：要看先看能改的
  const danger = sec.fields.filter((f) => f.danger || f.readOnly)
  const rest = sec.fields.filter((f) => !(f.danger || f.readOnly))

  return (
    <>
      {rest.map(render)}
      {danger.length > 0 && (
        <Group title={t('session.qbGroupDanger', { count: danger.length })}>{danger.map(render)}</Group>
      )}
    </>
  )
}
