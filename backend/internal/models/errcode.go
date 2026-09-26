package models

// 错误码：前端按它出当前语言的提示（码表在 frontend/src/utils/errors.ts）。
// 只给「界面高频可见、且后端自己能判定」的错误配码；上游下载器返回的文本
// （如 Tracker 报错、qBittorrent 的偏好校验）仍由前端的模式表兜底翻译。
// 新增或改名必须同步前端码表，否则界面会退回显示 Message 原文
const (
	ErrCodeUnauthorized      = "unauthorized"        // 令牌缺失或错误
	ErrCodeCrossSiteBlocked  = "cross_site_blocked"  // 跨站写请求被拒
	ErrCodeServerUnreachable = "server_unreachable"  // 下载器连不上 / 切换失败
	ErrCodeUnsupported       = "unsupported"         // 当前下载器不支持该能力
	ErrCodeInvalidArgument   = "invalid_argument"    // 提交的参数不被下载器接受
	ErrCodePathNotAllowed    = "path_not_allowed"    // 种子路径不在白名单或不可读
	ErrCodeTorrentTooLarge   = "torrent_too_large"   // 按路径读取的种子超过体积上限
)
