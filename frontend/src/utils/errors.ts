// 接口错误 → 当前语言提示文案。
// 两条路：后端能自行判定的错误带稳定 errorCode，直接查码表；其余（尤其是下载器
// 回传的英文原文）按模式表兜底翻译，都没命中就原样显示，避免吞掉未知错误。
// 码表须与 backend/internal/models/errcode.go 保持一致。
import i18n from '@/i18n'

const CODE_KEYS: Record<string, string> = {
  unauthorized: 'errors.unauthorized',
  cross_site_blocked: 'errors.crossSiteBlocked',
  server_unreachable: 'errors.serverUnreachable',
  unsupported: 'errors.unsupported',
  invalid_argument: 'errors.invalidArgument',
  path_not_allowed: 'errors.pathNotAllowed',
  torrent_too_large: 'errors.torrentTooLarge',
}

// 顺序敏感：具体规则在前，宽泛规则在后
const ERROR_PATTERNS: Array<[RegExp, string]> = [
  [/permission denied|directory is not writable|not writable|无法写入|不可写/i, 'errors.dirNotWritable'],
  [/no space left|not enough space|disk full|空间不足/i, 'errors.noSpace'],
  [/duplicate torrent|already exists|重复添加/i, 'errors.duplicateTorrent'],
  [/invalid.*torrent|bad torrent|无效的种子|metadata.*invalid/i, 'errors.invalidTorrent'],
  [/timeout|timed out|超时/i, 'errors.requestTimeout'],
  [/connection refused|connect.*failed|无法连接/i, 'errors.connectFailed'],
  [/not found|找不到|不存在/i, 'errors.notFound'],
  [/unauthorized|401|未授权/i, 'errors.authFailed'],
  [/torrent.*removed|种子已删除/i, 'errors.torrentRemoved'],
  [/verif|recheck/i, 'errors.verifyFailed'],
  [/invalid.*url|bad url/i, 'errors.invalidUrl'],
  [/invalid.*magnet/i, 'errors.invalidMagnet'],
  [/too many|limit/i, 'errors.tooMany'],
]

/**
 * 把接口错误翻译成当前语言的友好提示。
 * @param msg 服务端 message（可能是中文，也可能是下载器的英文原文）
 * @param code 服务端 errorCode；未提供或本端不认识时退回模式表
 */
export function translateApiError(msg: string, code?: string): string {
  if (code) {
    const key = CODE_KEYS[code]
    if (key) return i18n.t(key)
  }
  if (!msg) return msg
  for (const [re, key] of ERROR_PATTERNS) {
    if (re.test(msg)) return i18n.t(key)
  }
  return msg
}
