package qbittorrent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/sushazhi/seedark/backend/internal/driver"
)

// qBittorrent 偏好字段自述。
//
// 键名、类型、枚举取值对齐 qBittorrent 5.x 的 app/preferences（GET）与
// app/setPreferences（POST），分节对齐官方 WebUI Options 的标签页。
// 界面只做「按类型渲染 + 按原生键读写」，不硬编码任何一条键：
// 上游增删字段时改本文件即可，前端无需跟着改。
//
// 有意未收录的项：
//   - 浏览器本地偏好（语言、配色、日期格式、双击行为等，存于 WebUI 的
//     localStorage，不属于服务器偏好）
//   - 已废弃项 scan_dirs（5.x 仅保留兼容读取，WebUI 已改用 watched folders
//     独立接口）、random_port（读为 listen_port==0，写只认 true 表示随机）、
//     ssl_enabled / ssl_listen_port（BT over SSL 已废弃）
//
// 版本边界（本表按已发布的 5.2.3 及更早版本的键名收录）：
// 5.3 起上游改名了 export_dir / export_dir_fin → torrent_files_backup_*、
// mail_notification_ssl_enabled → mail_notification_encryption_type，并新增
// start_paused、shutdown_timeout、web_ui_sessions_count_limit 等键。届时按
// compat.go 的版本门控模式接入，不要在表里同时挂新旧两套键——旧服务器下发
// 不认识的键会被白名单丢弃，界面会出现永远空着的重复项。

// ---- 枚举取值 ----

const (
	// bittorrent_protocol
	protoBoth = 0
	protoTCP  = 1
	protoUTP  = 2
	// encryption
	encAllow   = 0
	encRequire = 1
	encDisable = 2
	// max_ratio_act
	ratioStop   = 0
	ratioRemove = 1
	ratioSuper  = 2
	ratioWipe   = 3
	// auto_delete_mode（TorrentFileGuard）
	autoDeleteNever   = 0
	autoDeleteIfAdded = 1
	autoDeleteAlways  = 2
	// dyndns_service（-1 表示不启用，勾选框控制）
	dyndnsDynDNS = 0
	dyndnsNoIP   = 1
	// disk_io_type
	diskIODefault = 0
	diskIOMmap    = 1
	diskIOPosix   = 2
	diskIOSimple  = 3
	// disk_io_read_mode / disk_io_write_mode
	ioOSCacheOff   = 0
	ioOSCacheOn    = 1
	ioWriteThrough = 2
	// utp_tcp_mixed_mode（MixedModeAlgorithm）
	mixedPreferTCP = 0
	mixedPeerProp  = 1
	// upload_slots_behavior
	slotsFixed = 0
	slotsRate  = 1
	// upload_choking_algorithm
	chokeRoundRobin = 0
	chokeFastest    = 1
	chokeAntiLeech  = 2
)

// ---- 构造助手（让上面那张表接近声明式） ----

func optInt(v int64, label, labelEn string) driver.SettingsOption {
	return driver.SettingsOption{Value: strconv.FormatInt(v, 10), Label: label, LabelEn: labelEn}
}

func optStr(v, label, labelEn string) driver.SettingsOption {
	return driver.SettingsOption{Value: v, Label: label, LabelEn: labelEn}
}

type optFn func(label, labelEn string) driver.SettingsOption

// fb 布尔开关
func fb(key, label, labelEn string) driver.SettingsField {
	return driver.SettingsField{Key: key, Type: driver.FieldBool, Label: label, LabelEn: labelEn}
}

// fi 整数
func fi(key, label, labelEn, unit string) driver.SettingsField {
	return driver.SettingsField{Key: key, Type: driver.FieldInt, Label: label, LabelEn: labelEn, Unit: unit}
}

// ff 小数
func ff(key, label, labelEn, unit string) driver.SettingsField {
	return driver.SettingsField{Key: key, Type: driver.FieldFloat, Label: label, LabelEn: labelEn, Unit: unit}
}

// fs 单行文本
func fs(key, label, labelEn string) driver.SettingsField {
	return driver.SettingsField{Key: key, Type: driver.FieldString, Label: label, LabelEn: labelEn}
}

// fpath 宿主文件系统上的目录路径：读写与单行文本同形，界面据类型提供
// 整行宽（路径通常较长）、宿主目录选择器与语义路径提示
func fpath(key, label, labelEn string) driver.SettingsField {
	return driver.SettingsField{Key: key, Type: driver.FieldPath, Label: label, LabelEn: labelEn}
}

// ft 多行文本
func ft(key, label, labelEn string) driver.SettingsField {
	return driver.SettingsField{Key: key, Type: driver.FieldText, Label: label, LabelEn: labelEn}
}

// ftime 时刻（HH:MM）
func ftime(key, label, labelEn string) driver.SettingsField {
	return driver.SettingsField{Key: key, Type: driver.FieldTime, Label: label, LabelEn: labelEn}
}

// fenum 整数枚举
func fenum(key, label, labelEn string, opts ...driver.SettingsOption) driver.SettingsField {
	return driver.SettingsField{
		Key: key, Type: driver.FieldSelect, Label: label, LabelEn: labelEn,
		Options: opts, ValueType: driver.FieldInt,
	}
}

// fenumStr 字符串枚举
func fenumStr(key, label, labelEn string, opts ...driver.SettingsOption) driver.SettingsField {
	return driver.SettingsField{
		Key: key, Type: driver.FieldSelect, Label: label, LabelEn: labelEn,
		Options: opts, ValueType: driver.FieldString,
	}
}

// fenumBool 布尔枚举：上游界面用下拉表达的二值选项（如「手动 / 自动」），
// 传输时仍是 JSON 布尔与 "true"/"false" 字符串，但界面上必须呈现为两项下拉
// 才能与官方 UI 的选项文案一一对应。
func fenumBool(key, label, labelEn string, opts ...driver.SettingsOption) driver.SettingsField {
	return driver.SettingsField{
		Key: key, Type: driver.FieldSelect, Label: label, LabelEn: labelEn,
		Options: opts, ValueType: driver.FieldBool,
	}
}

func boolOpt(v bool, label, labelEn string) driver.SettingsOption {
	return driver.SettingsOption{Value: strconv.FormatBool(v), Label: label, LabelEn: labelEn}
}

func rng(f driver.SettingsField, min, max int64) driver.SettingsField {
	f.Min, f.Max = &min, &max
	return f
}

func scale(f driver.SettingsField, s int64) driver.SettingsField {
	f.Scale = s
	return f
}

func hint(f driver.SettingsField, zh, en string) driver.SettingsField {
	f.Hint, f.HintEn = zh, en
	return f
}

func secret(f driver.SettingsField) driver.SettingsField {
	f.Secret = true
	return f
}

func writeOnly(f driver.SettingsField) driver.SettingsField {
	f.WriteOnly = true
	return f
}

func readOnly(f driver.SettingsField) driver.SettingsField {
	f.ReadOnly = true
	return f
}

func danger(f driver.SettingsField) driver.SettingsField {
	f.Danger = true
	return f
}

// qbSchema 设置分节（与官方 WebUI 的标签页一一对应）
var qbSchema = []driver.SettingsSection{
	{
		Key: "behavior", Label: "行为", LabelEn: "Behavior", Icon: driver.SettingIconBehavior,
		Fields: []driver.SettingsField{
			fb("confirm_torrent_deletion", "删除种子时提示确认", "Confirm when deleting torrents"),
			fb("confirm_torrent_recheck", "重新校验种子时提示确认", "Confirm torrent recheck"),
			fb("recheck_completed_torrents", "下载完成后自动重新校验", "Recheck torrents on completion"),
			fb("performance_warning", "记录性能告警", "Log performance warnings"),
			fb("status_bar_external_ip", "状态栏显示外部 IP", "Show external IP in status bar"),
			fb("file_log_enabled", "启用日志文件", "Enable log file"),
			fs("file_log_path", "日志文件路径", "Log file path"),
			rng(fi("file_log_max_size", "当大于指定大小时备份日志文件", "Backup the log file after", "KiB"), 1, 1024000),
			fb("file_log_backup_enabled", "启用日志文件备份", "Backup the log file"),
			fb("file_log_delete_old", "删除早于指定时间的备份日志文件", "Delete backup logs older than"),
			rng(fi("file_log_age", "备份日志保留时长", "Backup log retention", ""), 1, 365),
			fenum("file_log_age_type", "保留时长单位", "Retention unit",
				optInt(0, "天", "days"), optInt(1, "月", "months"), optInt(2, "年", "years")),
		},
	},
	{
		Key: "downloads", Label: "下载", LabelEn: "Downloads", Icon: driver.SettingIconDownload,
		Fields: []driver.SettingsField{
			fenumStr("torrent_content_layout", "种子内容布局", "Torrent content layout",
				optStr("Original", "原始（种子内结构）", "Original"),
				optStr("Subfolder", "创建子文件夹", "Create subfolder"),
				optStr("NoSubfolder", "不创建子文件夹", "Don't create subfolder")),
			fenumStr("torrent_stop_condition", "种子停止条件", "Torrent stop condition",
				optStr("None", "不停止", "None"),
				optStr("MetadataReceived", "已收到元数据", "Metadata received"),
				optStr("FilesChecked", "文件校验完成", "Files checked")),
			fb("add_to_top_of_queue", "新种子加入队列顶部", "Add to top of queue"),
			fb("add_stopped_enabled", "添加后不自动开始下载", "Do not start the download automatically"),
			fb("merge_trackers", "合并 Tracker 到已有种子", "Merge trackers to existing torrent"),
			fenum("auto_delete_mode", "添加完成后删除 .torrent 文件", "Delete .torrent files afterwards",
				optInt(autoDeleteNever, "从不", "Never"),
				optInt(autoDeleteIfAdded, "成功添加后", "If added"),
				optInt(autoDeleteAlways, "总是", "Always")),
			fb("preallocate_all", "为所有文件预分配磁盘空间", "Pre-allocate disk space for all files"),
			fb("incomplete_files_ext", "为不完整的文件添加扩展名 .!qB", "Append .!qB extension to incomplete files"),
			fb("use_unwanted_folder", "将未选中的文件保留在 \".unwanted\" 文件夹中", "Keep unselected files in \".unwanted\" folder"),
			fenumBool("auto_tmm_enabled", "默认种子管理模式", "Default Torrent Management Mode",
				boolOpt(false, "手动", "Manual"),
				boolOpt(true, "自动", "Automatic")),
			fenumBool("torrent_changed_tmm_enabled", "种子分类变化时", "When Torrent Category changed",
				boolOpt(true, "重新定位种子", "Relocate torrent"),
				boolOpt(false, "切换种子到手动模式", "Switch torrent to Manual Mode")),
			fenumBool("save_path_changed_tmm_enabled", "默认保存路径变化时", "When Default Save Path changed",
				boolOpt(true, "重新定位受影响的种子", "Relocate affected torrents"),
				boolOpt(false, "切换受影响的种子到手动模式", "Switch affected torrents to Manual Mode")),
			fenumBool("category_changed_tmm_enabled", "分类保存路径变化时", "When Category Save Path changed",
				boolOpt(true, "重新定位受影响的种子", "Relocate affected torrents"),
				boolOpt(false, "切换受影响的种子到手动模式", "Switch affected torrents to Manual Mode")),
			hint(fb("use_category_paths_in_manual_mode", "手动模式下使用分类路径", "Use Category paths in Manual Mode"),
				"相对保存路径按对应分类路径解析，而非默认路径", "Resolve relative Save Path against the category path instead of the default one"),
			fpath("save_path", "默认保存路径", "Default Save Path"),
			fb("temp_path_enabled", "保存未完成的种子到", "Keep incomplete torrents in"),
			fpath("temp_path", "未完成文件目录", "Incomplete files path"),
			hint(fpath("export_dir", "复制 .torrent 文件到", "Copy .torrent files to"),
				"留空表示不导出", "Leave empty to disable"),
			hint(fpath("export_dir_fin", "复制已完成下载的 .torrent 文件到", "Copy .torrent files for finished downloads to"),
				"留空表示不导出", "Leave empty to disable"),
			fb("excluded_file_names_enabled", "排除的文件名", "Excluded file names"),
			hint(ft("excluded_file_names", "排除的文件名（每行一个）", "Excluded file names (one per line)"),
				"支持通配符，如 *.exe", "Wildcards are supported, e.g. *.exe"),
			fb("mail_notification_enabled", "下载完成时发送电子邮件通知", "Email notification upon download completion"),
			fs("mail_notification_sender", "发件人", "From"),
			fs("mail_notification_email", "收件人", "To"),
			fs("mail_notification_smtp", "SMTP 服务器", "SMTP server"),
			fb("mail_notification_ssl_enabled", "此服务器需要安全连接（SSL）", "This server requires a secure connection (SSL)"),
			fb("mail_notification_auth_enabled", "认证", "Authentication"),
			fs("mail_notification_username", "用户名", "Username"),
			secret(fs("mail_notification_password", "密码", "Password")),
			fb("autorun_on_torrent_added_enabled", "添加种子时运行", "Run on torrent added"),
			fs("autorun_on_torrent_added_program", "添加时运行的程序", "Program to run on torrent added"),
			fb("autorun_enabled", "种子完成时运行", "Run on torrent finished"),
			fs("autorun_program", "完成时运行的程序", "Program to run on torrent finished"),
		},
	},
	{
		Key: "connection", Label: "连接", LabelEn: "Connection", Icon: driver.SettingIconConnect,
		Fields: []driver.SettingsField{
			fenum("bittorrent_protocol", "Peer 连接协议", "Peer connection protocol",
				optInt(protoBoth, "TCP 和 µTP", "TCP and µTP"),
				optInt(protoTCP, "仅 TCP", "TCP only"),
				optInt(protoUTP, "仅 µTP", "µTP only")),
			hint(rng(fi("listen_port", "传入连接使用的端口", "Port used for incoming connections", ""), 0, 65535),
				"设为 0 由系统自动选择未占用端口", "Set to 0 to let your system pick an unused port"),
			fb("upnp", "使用路由器的 UPnP / NAT-PMP 端口转发", "Use UPnP / NAT-PMP port forwarding from my router"),
			hint(rng(fi("max_connec", "全局最大连接数", "Global maximum number of connections", ""), 0, 100000),
				"0 表示不限制", "0 means unlimited"),
			rng(fi("max_connec_per_torrent", "单种最大连接数", "Maximum number of connections per torrent", ""), 0, 100000),
			hint(rng(fi("max_uploads", "全局最大上传窗口数", "Global maximum number of upload slots", ""), 0, 100000),
				"0 表示不限制", "0 means unlimited"),
			rng(fi("max_uploads_per_torrent", "单种最大上传窗口数", "Maximum number of upload slots per torrent", ""), 0, 100000),
			fb("i2p_enabled", "启用 I2P", "Enable I2P"),
			fs("i2p_address", "I2P 主机", "I2P host"),
			rng(fi("i2p_port", "I2P 端口", "I2P port", ""), 0, 65535),
			hint(fb("i2p_mixed_mode", "混合模式", "Mixed mode"),
				"启用后 I2P 种子也会从其他来源获取 Peer 并连接普通 IP，不再提供匿名保护",
				"I2P torrents may also get peers from other sources than the tracker and connect to regular IPs, providing no anonymization"),
			// 量与长度的上限取官方 WebUI 的输入框限制（1..16 / 0..7）
			rng(fi("i2p_inbound_quantity", "I2P 入站隧道数", "I2P inbound quantity", ""), 1, 16),
			rng(fi("i2p_outbound_quantity", "I2P 出站隧道数", "I2P outbound quantity", ""), 1, 16),
			rng(fi("i2p_inbound_length", "I2P 入站隧道长度", "I2P inbound length", ""), 0, 7),
			rng(fi("i2p_outbound_length", "I2P 出站隧道长度", "I2P outbound length", ""), 0, 7),
			fenumStr("proxy_type", "代理类型", "Proxy type",
				optStr("None", "不使用", "(None)"),
				optStr("SOCKS4", "SOCKS4", "SOCKS4"),
				optStr("SOCKS5", "SOCKS5", "SOCKS5"),
				optStr("HTTP", "HTTP", "HTTP")),
			fs("proxy_ip", "代理主机", "Proxy host"),
			rng(fi("proxy_port", "代理端口", "Proxy port", ""), 0, 65535),
			fb("proxy_auth_enabled", "代理需要认证", "Proxy requires authentication"),
			fs("proxy_username", "代理用户名", "Proxy username"),
			secret(fs("proxy_password", "代理密码", "Proxy password")),
			fb("proxy_hostname_lookup", "经代理解析主机名", "Perform hostname lookup via proxy"),
			fb("proxy_bittorrent", "使用代理处理 BitTorrent 流量", "Use proxy for BitTorrent purposes"),
			hint(fb("proxy_peer_connections", "使用代理建立 Peer 连接", "Use proxy for peer connections"),
				"uTP 连接无法经代理，会直接建立", "uTP connections ignore the proxy and are made directly"),
			fb("proxy_rss", "使用代理获取 RSS", "Use proxy for RSS purposes"),
			fb("proxy_misc", "使用代理获取其他内容", "Use proxy for general purposes"),
			fb("ip_filter_enabled", "启用 IP 过滤", "Enable IP filtering"),
			hint(fs("ip_filter_path", "IP 过滤规则文件", "Filter path"),
				"qBittorrent 服务端本地路径", "Path on the qBittorrent host"),
			fb("ip_filter_trackers", "匹配 Tracker", "Apply to trackers"),
			ft("banned_IPs", "手动屏蔽 IP 地址（每行一个）", "Manually banned IP addresses (one per line)"),
		},
	},
	{
		Key: "speed", Label: "速度", LabelEn: "Speed", Icon: driver.SettingIconSpeed,
		Fields: []driver.SettingsField{
			hint(scale(fi("dl_limit", "全局下载限速", "Global download rate limit", "KiB/s"), 1024),
				"0 表示不限制", "0 means unlimited"),
			hint(scale(fi("up_limit", "全局上传限速", "Global upload rate limit", "KiB/s"), 1024),
				"0 表示不限制", "0 means unlimited"),
			hint(scale(fi("alt_dl_limit", "备用下载限速", "Alternative download rate limit", "KiB/s"), 1024),
				"0 表示不限制", "0 means unlimited"),
			hint(scale(fi("alt_up_limit", "备用上传限速", "Alternative upload rate limit", "KiB/s"), 1024),
				"0 表示不限制", "0 means unlimited"),
			fb("scheduler_enabled", "按计划启用备用限速", "Schedule the use of alternative rate limits"),
			ftime("schedule_from", "计划开始时刻", "Schedule from"),
			ftime("schedule_to", "计划结束时刻", "Schedule to"),
			fenum("scheduler_days", "计划生效日", "When",
				optInt(0, "每天", "Every day"),
				optInt(1, "工作日", "Weekdays"),
				optInt(2, "周末", "Weekends"),
				optInt(3, "周一", "Monday"),
				optInt(4, "周二", "Tuesday"),
				optInt(5, "周三", "Wednesday"),
				optInt(6, "周四", "Thursday"),
				optInt(7, "周五", "Friday"),
				optInt(8, "周六", "Saturday"),
				optInt(9, "周日", "Sunday")),
			fb("limit_utp_rate", "对 µTP 协议应用限速", "Apply rate limit to µTP protocol"),
			fb("limit_tcp_overhead", "限速计入传输开销", "Apply rate limit to transport overhead"),
			fb("limit_lan_peers", "局域网 Peer 也计入限速", "Apply rate limit to peers on LAN"),
		},
	},
	{
		Key: "bittorrent", Label: "BitTorrent", LabelEn: "BitTorrent", Icon: driver.SettingIconBitTorrent,
		Fields: []driver.SettingsField{
			fb("dht", "启用 DHT (去中心化网络) 以找到更多用户", "Enable DHT (decentralized network) to find more peers"),
			fb("pex", "启用用户交换 (PeX) 以找到更多用户", "Enable Peer Exchange (PeX) to find more peers"),
			fb("lsd", "启用本地用户发现以找到更多用户", "Enable Local Peer Discovery to find more peers"),
			fenum("encryption", "加密模式", "Encryption mode",
				optInt(encAllow, "允许加密", "Allow encryption"),
				optInt(encRequire, "强制加密", "Require encryption"),
				optInt(encDisable, "禁用加密", "Disable encryption")),
			fb("anonymous_mode", "启用匿名模式", "Enable anonymous mode"),
			// 下限取官方输入框的 -1（按官方输入框范围收录，不额外解释取值语义）
			rng(fi("max_active_checking_torrents", "最大活跃检查种子数", "Max active checking torrents", ""), -1, 10000),
			fb("queueing_enabled", "种子排队", "Torrent Queueing"),
			hint(rng(fi("max_active_downloads", "最大同时下载数", "Maximum active downloads", ""), 0, 10000),
				"-1 表示不限制", "-1 means unlimited"),
			hint(rng(fi("max_active_uploads", "最大同时做种数", "Maximum active uploads", ""), 0, 10000),
				"-1 表示不限制", "-1 means unlimited"),
			hint(rng(fi("max_active_torrents", "最大同时活动种子数", "Maximum active torrents", ""), 0, 10000),
				"-1 表示不限制", "-1 means unlimited"),
			fb("dont_count_slow_torrents", "慢速种子不计入限制内", "Do not count slow torrents in these limits"),
			scale(fi("slow_torrent_dl_rate_threshold", "下载速率阈值", "Download rate threshold", "KiB/s"), 1024),
			scale(fi("slow_torrent_ul_rate_threshold", "上传速率阈值", "Upload rate threshold", "KiB/s"), 1024),
			rng(fi("slow_torrent_inactive_timer", "种子非活动计时器", "Torrent inactivity timer", "seconds"), 0, 1<<20),
			fb("max_ratio_enabled", "当分享率达到", "When ratio reaches"),
			hint(ff("max_ratio", "分享率上限", "Share ratio limit", ""),
				"-1 表示不限制", "-1 means unlimited"),
			fb("max_seeding_time_enabled", "达到总做种时间时", "When total seeding time reaches"),
			hint(rng(fi("max_seeding_time", "做种时长上限", "Seeding time limit", "minutes"), -1, 1<<20),
				"-1 表示不限制", "-1 means unlimited"),
			fb("max_inactive_seeding_time_enabled", "达到不活跃做种时间时", "When inactive seeding time reaches"),
			hint(rng(fi("max_inactive_seeding_time", "闲置做种时长上限", "Inactive seeding time limit", "minutes"), -1, 1<<20),
				"-1 表示不限制", "-1 means unlimited"),
			hint(fenum("max_ratio_act", "达到上述任一上限时", "When any of the above limits are reached",
				optInt(ratioStop, "停止种子", "Stop torrent"),
				optInt(ratioRemove, "删除种子", "Remove torrent"),
				optInt(ratioWipe, "删除种子及所属文件", "Remove torrent and its files"),
				optInt(ratioSuper, "为种子启用超级做种", "Enable super seeding for torrent")),
				"分享率 / 做种时长 / 闲置做种时长任一触顶后执行所选动作",
				"Runs the selected action once the share ratio, seeding time or inactive seeding time limit is hit"),
			fb("add_trackers_enabled", "自动为下载添加以下 Tracker", "Automatically append these trackers to new downloads"),
			ft("add_trackers", "Tracker 列表（每行一个）", "Tracker list (one per line)"),
			fb("add_trackers_from_url_enabled", "自动为下载添加订阅地址中的 Tracker", "Automatically append trackers from URL to new downloads"),
			fs("add_trackers_url", "订阅地址", "URL"),
			readOnly(ft("add_trackers_url_list", "获取到的 Tracker", "Fetched trackers")),
		},
	},
	{
		Key: "rss", Label: "RSS", LabelEn: "RSS", Icon: driver.SettingIconRSS,
		Fields: []driver.SettingsField{
			fb("rss_processing_enabled", "启用 RSS 抓取", "Enable fetching RSS feeds"),
			rng(fi("rss_refresh_interval", "订阅刷新间隔", "Feeds refresh interval", "min"), 1, 1<<20),
			rng(fi("rss_fetch_delay", "相同主机请求延迟", "Same host request delay", "sec"), 0, 1<<20),
			rng(fi("rss_max_articles_per_feed", "每个订阅最多保留文章数", "Maximum number of articles per feed", ""), 0, 1<<20),
			fb("rss_auto_downloading_enabled", "启用 RSS 种子自动下载", "Enable auto downloading of RSS torrents"),
			fb("rss_download_repack_proper_episodes", "下载 REPACK / PROPER 剧集", "Download REPACK/PROPER episodes"),
			ft("rss_smart_episode_filters", "过滤规则（每行一条）", "Filters (one per line)"),
		},
	},
	{
		Key: "webui", Label: "WebUI", LabelEn: "WebUI", Icon: driver.SettingIconWebUI,
		Fields: []driver.SettingsField{
			danger(fs("web_ui_address", "监听地址", "IP address")),
			danger(rng(fi("web_ui_port", "监听端口", "Port", ""), 1, 65535)),
			fb("web_ui_upnp", "使用路由器的 UPnP / NAT-PMP 转发端口", "Use UPnP / NAT-PMP to forward the port from my router"),
			danger(fb("use_https", "启用 HTTPS", "Use HTTPS instead of HTTP")),
			danger(fs("web_ui_https_cert_path", "证书", "Certificate")),
			danger(fs("web_ui_https_key_path", "私钥", "Key")),
			danger(fs("web_ui_username", "用户名", "Username")),
			danger(writeOnly(secret(fs("web_ui_password", "密码", "Password")))),
			readOnly(fs("web_ui_api_key", "API 密钥（本应用中即连接时填写的密码）", "API key (in this app: the password entered when connecting)")),
			danger(fb("bypass_local_auth", "本机客户端免认证", "Bypass authentication for clients on localhost")),
			fb("bypass_auth_subnet_whitelist_enabled", "白名单网段的客户端免认证", "Bypass authentication for clients in whitelisted IP subnets"),
			hint(ft("bypass_auth_subnet_whitelist", "免认证网段（每行一个）", "Whitelisted IP subnets (one per line)"),
				"示例：172.17.32.0/24, fdff:ffff:c8::/40", "Example: 172.17.32.0/24, fdff:ffff:c8::/40"),
			hint(rng(fi("web_ui_max_auth_fail_count", "连续失败多少次后封禁客户端", "Ban client after consecutive failures", ""), 0, 10000),
				"0 表示不封禁", "0 disables banning"),
			rng(fi("web_ui_ban_duration", "封禁时长", "Ban for", "sec"), 1, 1<<24),
			rng(fi("web_ui_session_timeout", "会话超时", "Session timeout", "sec"), 0, 1<<24),
			fb("alternative_webui_enabled", "使用备选 WebUI", "Use alternative WebUI"),
			fs("alternative_webui_path", "文件路径", "Files location"),
			fb("web_ui_clickjacking_protection_enabled", "启用点击劫持保护", "Enable clickjacking protection"),
			fb("web_ui_csrf_protection_enabled", "启用跨站请求伪造（CSRF）保护", "Enable Cross-Site Request Forgery (CSRF) protection"),
			fb("web_ui_secure_cookie_enabled", "启用 cookie 安全标志（需要 HTTPS 或本地连接）", "Enable cookie Secure flag (requires HTTPS or localhost connection)"),
			fb("web_ui_host_header_validation_enabled", "启用 Host 头校验", "Enable Host header validation"),
			hint(ft("web_ui_domain_list", "服务器域名（每行一个）", "Server domains (one per line)"),
				"用 ';' 分隔多个条目，可使用通配符 '*'", "Use ';' to split multiple entries. Can use wildcard '*'"),
			fb("web_ui_use_custom_http_headers_enabled", "添加自定义 HTTP 头", "Add custom HTTP headers"),
			hint(ft("web_ui_custom_http_headers", "自定义 HTTP 头", "Custom HTTP headers"),
				"每行一个「Header: value」", "Header: value pairs, one per line"),
			fb("web_ui_reverse_proxy_enabled", "启用反向代理支持", "Enable reverse proxy support"),
			hint(fs("web_ui_reverse_proxies_list", "受信任的代理列表", "Trusted proxies list"),
				"填写反向代理 IP（或子网，如 0.0.0.0/24）以采用 X-Forwarded-For 中的客户端地址；用 ';' 分隔多个条目",
				"Reverse proxy IPs (or subnets, e.g. 0.0.0.0/24) whose X-Forwarded-For client address is trusted; use ';' to split multiple entries"),
			fb("dyndns_enabled", "更新我的动态域名", "Update my dynamic domain name"),
			fenum("dyndns_service", "动态 DNS 服务商", "Service",
				optInt(dyndnsDynDNS, "DynDNS", "DynDNS"), optInt(dyndnsNoIP, "NO-IP", "NO-IP")),
			fs("dyndns_domain", "域名", "Domain name"),
			fs("dyndns_username", "用户名", "Username"),
			secret(fs("dyndns_password", "密码", "Password")),
		},
	},
	{
		Key: "advanced", Label: "高级", LabelEn: "Advanced", Icon: driver.SettingIconAdvanced,
		Fields: []driver.SettingsField{
			hint(fenumStr("resume_data_storage_type", "续传数据存储类型", "Resume data storage type",
				optStr("Legacy", "Fastresume 文件", "Fastresume files"),
				optStr("SQLite", "SQLite 数据库（实验性）", "SQLite database (experimental)")),
				"修改后需要重启 qBittorrent", "Requires a qBittorrent restart"),
			fenumStr("torrent_content_remove_option", "删除种子内容的方式", "Torrent content removing mode",
				optStr("Delete", "永久删除文件", "Delete files permanently"),
				optStr("MoveToTrash", "移入回收站（如可用）", "Move files to trash (if possible)")),
			rng(fi("memory_working_set_limit", "物理内存（RAM）占用上限", "Physical memory (RAM) usage limit", "MiB"), 0, 1<<20),
			fs("current_network_interface", "网络接口", "Network interface"),
			readOnly(fs("current_interface_name", "当前接口名称", "Interface name")),
			hint(fs("current_interface_address", "可选：绑定的 IP 地址", "Optional IP address to bind to"),
				"留空表示自动", "Leave empty for any"),
			rng(fi("save_resume_data_interval", "保存续传数据间隔", "Save resume data interval", "min"), 1, 1<<20),
			rng(fi("save_statistics_interval", "保存统计信息间隔", "Save statistics interval", "min"), 1, 1<<20),
			hint(rng(fi("torrent_file_size_limit", ".torrent 文件大小上限", ".torrent file size limit", "MiB"), 0, 1<<20),
				"超过则拒绝添加", "Reject larger torrents"),
			hint(fs("app_instance_name", "自定义应用实例名称", "Customize application instance name"),
				"会追加到窗口标题，便于区分多个实例", "Appended to the window title to distinguish instances"),
			rng(fi("refresh_interval", "界面刷新间隔", "Refresh interval", "ms"), 100, 60000),
			fb("resolve_peer_host_names", "解析 Peer 主机名", "Resolve peer host names"),
			fb("resolve_peer_countries", "解析 Peer 国家/地区", "Resolve peer countries"),
			fb("reannounce_when_address_changed", "IP 或端口变化时向所有 Tracker 重新汇报", "Reannounce to all trackers when IP or port changed"),
			fb("enable_embedded_tracker", "启用内置 Tracker", "Enable embedded tracker"),
			rng(fi("embedded_tracker_port", "内置 Tracker 端口", "Embedded tracker port", ""), 1, 65535),
			fb("embedded_tracker_port_forwarding", "为内置 Tracker 启用端口转发", "Enable port forwarding for embedded tracker"),
			fb("mark_of_the_web", "为下载的文件启用 Mark-of-the-Web（MOTW）（需要 macOS 或 Windows）", "Enable Mark-of-the-Web (MOTW) for downloaded files (require macOS or Windows)"),
			fb("ignore_ssl_errors", "忽略 SSL 错误", "Ignore SSL errors"),
			hint(fs("python_executable_path", "Python 可执行文件路径（可能需要重启）", "Python executable path (may require restart)"),
				"留空则自动探测", "Auto detect if empty"),
			rng(fi("bdecode_depth_limit", "bdecode 嵌套深度上限", "Bdecode depth limit", ""), 0, 1<<20),
			rng(fi("bdecode_token_limit", "bdecode 令牌数量上限", "Bdecode token limit", ""), 0, 1<<30),
			rng(fi("async_io_threads", "异步 IO 线程数", "Asynchronous I/O threads", ""), 0, 1024),
			rng(fi("hashing_threads", "散列线程数", "Hashing threads", ""), 0, 1024),
			rng(fi("file_pool_size", "文件句柄池大小", "File pool size", ""), 1, 1<<20),
			rng(fi("checking_memory_use", "校验时内存占用上限", "Outstanding memory when checking torrents", "MiB"), 0, 1<<20),
			rng(fi("disk_cache", "磁盘缓存", "Disk cache", "MiB"), -1, 1<<20),
			rng(fi("disk_cache_ttl", "磁盘缓存过期间隔", "Disk cache expiry interval", "sec"), 0, 1<<24),
			rng(fi("disk_queue_size", "磁盘队列大小", "Disk queue size", "KiB"), 0, 1<<24),
			fenum("disk_io_type", "磁盘 IO 类型（需要重启）", "Disk IO type (requires restart)",
				optInt(diskIODefault, "默认", "Default"),
				optInt(diskIOMmap, "内存映射文件", "Memory mapped files"),
				optInt(diskIOPosix, "遵循 POSIX", "POSIX-compliant"),
				optInt(diskIOSimple, "简单预读/预写", "Simple pread/pwrite")),
			fenum("disk_io_read_mode", "磁盘 IO 读取模式", "Disk IO read mode",
				optInt(ioOSCacheOff, "禁用操作系统缓存", "Disable OS cache"),
				optInt(ioOSCacheOn, "启用操作系统缓存", "Enable OS cache")),
			fenum("disk_io_write_mode", "磁盘 IO 写入模式", "Disk IO write mode",
				optInt(ioOSCacheOff, "禁用操作系统缓存", "Disable OS cache"),
				optInt(ioOSCacheOn, "启用操作系统缓存", "Enable OS cache"),
				optInt(ioWriteThrough, "连续写入", "Write-through")),
			fb("enable_coalesce_read_write", "合并读写", "Coalesce reads & writes"), fb("enable_piece_extent_affinity", "启用相连分块下载模式", "Use piece extent affinity"),
			fb("enable_upload_suggestions", "发送分块上传建议", "Send upload piece suggestions"),
			rng(fi("send_buffer_watermark", "发送缓冲区上限", "Send buffer watermark", "KiB"), 0, 1<<20),
			rng(fi("send_buffer_low_watermark", "发送缓冲区下限", "Send buffer low watermark", "KiB"), 0, 1<<20),
			rng(fi("send_buffer_watermark_factor", "发送缓冲区增长系数", "Send buffer watermark factor", "%"), 50, 500),
			rng(fi("connection_speed", "每秒传出连接数", "Outgoing connections per second", ""), 0, 1<<10),
			hint(rng(fi("socket_send_buffer_size", "Socket 发送缓冲区大小", "Socket send buffer size", "KiB"), 0, 1<<20),
				"0 表示使用系统默认值", "0 uses the system default"),
			hint(rng(fi("socket_receive_buffer_size", "Socket 接收缓冲区大小", "Socket receive buffer size", "KiB"), 0, 1<<20),
				"0 表示使用系统默认值", "0 uses the system default"),
			rng(fi("socket_backlog_size", "Socket 积压队列大小", "Socket backlog size", ""), 1, 1<<20),
			hint(rng(fi("outgoing_ports_min", "出站端口（下限）", "Outgoing ports (Min)", ""), 0, 65535),
				"0 表示禁用", "0 disables it"),
			hint(rng(fi("outgoing_ports_max", "出站端口（上限）", "Outgoing ports (Max)", ""), 0, 65535),
				"0 表示禁用", "0 disables it"),
			hint(rng(fi("upnp_lease_duration", "UPnP 租约时长", "UPnP lease duration", "sec"), 0, 1<<24),
				"0 表示永久租约", "0 means a permanent lease"),
			hint(rng(fi("peer_tos", "Peer 连接使用的差分服务代码点（DSCP）", "Differentiated Services Code Point (DSCP) for connections to peers", ""), -1, 63),
				"用于 QoS 流量分类", "Used for QoS classification"),
			fenum("utp_tcp_mixed_mode", "µTP-TCP 混合模式策略", "µTP-TCP mixed mode algorithm",
				optInt(mixedPreferTCP, "优先使用 TCP", "Prefer TCP"),
				optInt(mixedPeerProp, "按 Peer 比重（抑制 TCP）", "Peer proportional (throttles TCP)")),
			rng(fi("hostname_cache_ttl", "内部主机名解析缓存过期时间", "Internal hostname resolver cache expiry interval", "sec"), 0, 1<<24),
			fb("idn_support_enabled", "支持国际化域名（IDN）", "Support internationalized domain name (IDN)"),
			fb("enable_multi_connections_from_same_ip", "允许来自同一 IP 地址的多条连接", "Allow multiple connections from the same IP address"),
			fb("validate_https_tracker_certificate", "校验 HTTPS Tracker 证书", "Validate HTTPS tracker certificate"),
			hint(fb("ssrf_mitigation", "服务器端请求伪造（SSRF）缓解", "Server-side request forgery (SSRF) mitigation"),
				"缓解服务器端请求伪造攻击", "Mitigates server-side request forgery attacks"),
			fb("block_peers_on_privileged_ports", "禁止连接特权端口上的 Peer", "Disallow connection to peers on privileged ports"),
			fenum("upload_slots_behavior", "上传窗口策略", "Upload slots behavior",
				optInt(slotsFixed, "固定窗口数", "Fixed slots"),
				optInt(slotsRate, "基于上传速度", "Upload rate based")),
			fenum("upload_choking_algorithm", "上传连接策略", "Upload choking algorithm",
				optInt(chokeRoundRobin, "轮流上传", "Round-robin"),
				optInt(chokeFastest, "最快上传", "Fastest upload"),
				optInt(chokeAntiLeech, "反吸血", "Anti-leech")),
			fb("announce_to_all_trackers", "始终向同级的所有 Tracker 汇报", "Always announce to all trackers in a tier"),
			fb("announce_to_all_tiers", "始终向所有层级汇报", "Always announce to all tiers"),
			hint(fs("announce_ip", "汇报给 Tracker 的 IP 地址（需要重启）", "IP address reported to trackers (requires restart)"),
				"留空表示自动", "Leave empty for automatic"),
			hint(rng(fi("announce_port", "汇报给 Tracker 的端口（需要重启）", "Port reported to trackers (requires restart)", ""), 0, 65535),
				"0 表示使用监听端口", "0 means the listening port"),
			rng(fi("max_concurrent_http_announces", "并发 HTTP 汇报数上限", "Max concurrent HTTP announces", ""), 1, 1<<20),
			hint(rng(fi("stop_tracker_timeout", "停止时等待 Tracker 响应的超时", "Stop tracker timeout", "sec"), 0, 1<<20),
				"0 表示禁用", "0 disables it"),
			rng(fi("peer_turnover", "Peer 轮换断开百分比", "Peer turnover disconnect percentage", "%"), 0, 100),
			rng(fi("peer_turnover_cutoff", "Peer 轮换阈值百分比", "Peer turnover threshold percentage", "%"), 0, 100),
			rng(fi("peer_turnover_interval", "Peer 轮换间隔", "Peer turnover disconnect interval", "sec"), 0, 3600),
			rng(fi("request_queue_size", "单个 Peer 的最大未完成请求", "Maximum outstanding requests to a single peer", ""), 1, 1<<20),
			hint(ft("dht_bootstrap_nodes", "DHT Bootstrap 节点", "DHT bootstrap nodes"), "留空则恢复默认", "Resets to default if empty"),
		},
	},
}

// ---- 索引与校验 ----

var (
	qbIndexOnce sync.Once
	qbIndex     map[string]driver.SettingsField
)

// qbPrefIndex Key → 字段描述（读取过滤 / 写入白名单都以此为准）
func qbPrefIndex() map[string]driver.SettingsField {
	qbIndexOnce.Do(func() {
		qbIndex = make(map[string]driver.SettingsField)
		for _, sec := range qbSchema {
			for _, f := range sec.Fields {
				qbIndex[f.Key] = f
			}
		}
		// 虚拟时刻字段：原生键是 4 个「时 / 分」整数，这里按一个控件提交
		// （见 readQBPreferences / applyQBPreferences 的转换）
		qbIndex["schedule_from"] = driver.SettingsField{Key: "schedule_from", Type: driver.FieldTime, ValueType: driver.FieldInt}
		qbIndex["schedule_to"] = driver.SettingsField{Key: "schedule_to", Type: driver.FieldTime, ValueType: driver.FieldInt}
	})
	return qbIndex
}

// SettingsSchema 设置界面字段自述（qBittorrent 完整偏好清单）
func (c *Client) SettingsSchema() []driver.SettingsSection {
	return qbSchema
}

// readQBPreferences 从 app/preferences 原始响应里取出自述覆盖的字段，
// 并把取值规整成界面约定的类型与单位（限速换算为 KiB/s、时刻合并为 HH:MM）。
// 未收录的键原样丢弃：界面上不存在的东西不该被回显，写入时也会被白名单拦住。
func readQBPreferences(raw map[string]any) map[string]any {
	out := make(map[string]any, len(qbIndex))
	for key, f := range qbPrefIndex() {
		if strings.HasPrefix(key, "schedule_") {
			continue // 时刻字段来自 hour/min 四键，单独处理
		}
		if f.WriteOnly {
			continue // 只写字段永不回显（即使上游将来回传了明文）
		}
		v, ok := raw[key]
		if !ok {
			continue
		}
		val, ok := normalizeRead(f, v)
		if !ok {
			continue
		}
		out[key] = val
	}
	for _, pair := range []struct{ key, hKey, mKey string }{
		{"schedule_from", "schedule_from_hour", "schedule_from_min"},
		{"schedule_to", "schedule_to_hour", "schedule_to_min"},
	} {
		h, hOK := raw[pair.hKey]
		m, mOK := raw[pair.mKey]
		if !hOK || !mOK {
			continue
		}
		hh, ok1 := toInt64(h)
		mm, ok2 := toInt64(m)
		if ok1 && ok2 {
			out[pair.key] = fmt.Sprintf("%02d:%02d", hh, mm)
		}
	}
	return out
}

// decodePrefs 把原始偏好表解成通用映射用的结构体（字段在 appPreferences 里声明）
func decodePrefs(raw map[string]any) (appPreferences, error) {
	var out appPreferences
	data, err := json.Marshal(raw)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("解析偏好失败: %w", err)
	}
	return out, nil
}

// normalizeRead 单字段读取归一：数值统一为 int64 / float64，枚举按 ValueType
func normalizeRead(f driver.SettingsField, v any) (any, bool) {
	switch f.Type {
	case driver.FieldBool:
		b, ok := v.(bool)
		return b, ok
	case driver.FieldInt:
		n, ok := toInt64(v)
		if !ok {
			return nil, false
		}
		if f.Scale > 0 {
			return n / f.Scale, true
		}
		return n, true
	case driver.FieldFloat:
		n, ok := toFloat64(v)
		return n, ok
	case driver.FieldSelect:
		if f.ValueType == driver.FieldInt {
			n, ok := toInt64(v)
			if ok && hasOption(f, strconv.FormatInt(n, 10)) {
				return n, true
			}
		} else if f.ValueType == driver.FieldBool {
			// 上游 app/preferences 对这些项回传 JSON 布尔；qbmock 与部分版本回传字符串
			b, ok := v.(bool)
			if !ok {
				if s, isStr := v.(string); isStr {
					if parsed, err := strconv.ParseBool(s); err == nil {
						b, ok = parsed, true
					}
				}
			}
			if ok && hasOption(f, strconv.FormatBool(b)) {
				return b, true
			}
		} else if s, ok := v.(string); ok && hasOption(f, s) {
			return s, true
		}
		// 取值不在自述的枚举里（如未启用时的 dyndns_service = -1、
		// 未配置代理时的空类型）：回落到首项，界面拿不到无法渲染的值
		if o, found := firstOption(f); found {
			return optionValue(f, o), true
		}
		return nil, false
	default: // string / text / time
		s, ok := v.(string)
		return s, ok
	}
}

func firstOption(f driver.SettingsField) (driver.SettingsOption, bool) {
	if len(f.Options) == 0 {
		return driver.SettingsOption{}, false
	}
	return f.Options[0], true
}

// optionValue 把枚举项还原成字段的传输类型（数值枚举是 int64，
// 枚举项里始终以字符串保存），保证回退取值与正常取值同型
func optionValue(f driver.SettingsField, o driver.SettingsOption) any {
	if f.ValueType == driver.FieldInt {
		if n, err := strconv.ParseInt(o.Value, 10, 64); err == nil {
			return n
		}
	}
	return o.Value
}

// applyQBPreferences 校验并归一写入补丁，返回可直接投给 app/setPreferences 的
// 原生键值表。未知键与只读键直接报错——界面永远不会提交它们，提交了说明
// 调用方有问题，静默忽略只会让「设置没生效」变得难查。
func applyQBPreferences(patch map[string]any) (map[string]any, error) {
	idx := qbPrefIndex()
	// 键名排序遍历：报错信息稳定，便于测试与排查
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make(map[string]any, len(patch)+4)
	for _, key := range keys {
		val := patch[key]
		f, ok := idx[key]
		if !ok {
			return nil, fmt.Errorf("%w: 未知设置项 %s", driver.ErrInvalid, key)
		}
		if f.ReadOnly {
			return nil, fmt.Errorf("%w: 设置项 %s 为只读", driver.ErrInvalid, key)
		}
		if f.Type == driver.FieldTime {
			s, ok := val.(string)
			if !ok {
				return nil, fmt.Errorf("%w: 设置项 %s 需要 HH:MM 格式", driver.ErrInvalid, key)
			}
			hh, mm, err := parseClock(s)
			if err != nil {
				return nil, fmt.Errorf("%w: 设置项 %s 的%s", driver.ErrInvalid, key, err.Error())
			}
			// schedule_from → schedule_from_hour / schedule_from_min
			out[key+"_hour"] = int64(hh)
			out[key+"_min"] = int64(mm)
			continue
		}
		coerced, err := coerce(f, val)
		if err != nil {
			return nil, err
		}
		out[key] = coerced
	}

	// 上游把「值」与「启用开关」拆成两个键，且 setPreferences 里开关优先级更高
	// （开关为 false 会直接置 -1）。只提交值时补上启用位，否则用户的修改会被
	// 静默丢弃（值写进去了，但限制仍处于关闭状态）。
	for valueKey, enabledKey := range map[string]string{
		"max_ratio":                 "max_ratio_enabled",
		"max_seeding_time":          "max_seeding_time_enabled",
		"max_inactive_seeding_time": "max_inactive_seeding_time_enabled",
	} {
		if _, hasValue := out[valueKey]; !hasValue {
			continue
		}
		if _, hasSwitch := out[enabledKey]; !hasSwitch {
			out[enabledKey] = true
		}
	}
	return out, nil
}

// coerce 按字段类型校验并转换单个取值
func coerce(f driver.SettingsField, v any) (any, error) {
	switch f.Type {
	case driver.FieldBool:
		switch t := v.(type) {
		case bool:
			return t, nil
		default:
			return nil, fmt.Errorf("%w: 设置项 %s 需要布尔值", driver.ErrInvalid, f.Key)
		}
	case driver.FieldInt:
		n, ok := toInt64(v)
		if !ok {
			return nil, fmt.Errorf("%w: 设置项 %s 需要整数", driver.ErrInvalid, f.Key)
		}
		if f.Min != nil && n < *f.Min {
			return nil, fmt.Errorf("%w: 设置项 %s 不能小于 %d", driver.ErrInvalid, f.Key, *f.Min)
		}
		if f.Max != nil && n > *f.Max {
			return nil, fmt.Errorf("%w: 设置项 %s 不能大于 %d", driver.ErrInvalid, f.Key, *f.Max)
		}
		if f.Scale > 0 {
			return n * f.Scale, nil
		}
		return n, nil
	case driver.FieldFloat:
		n, ok := toFloat64(v)
		if !ok {
			return nil, fmt.Errorf("%w: 设置项 %s 需要数值", driver.ErrInvalid, f.Key)
		}
		return n, nil
	case driver.FieldSelect:
		if f.ValueType == driver.FieldInt {
			n, ok := toInt64(v)
			if !ok {
				return nil, fmt.Errorf("%w: 设置项 %s 需要枚举序号", driver.ErrInvalid, f.Key)
			}
			if !hasOption(f, strconv.FormatInt(n, 10)) {
				return nil, fmt.Errorf("%w: 设置项 %s 的取值 %d 不在可选范围内", driver.ErrInvalid, f.Key, n)
			}
			return n, nil
		}
		if f.ValueType == driver.FieldBool {
			// 布尔枚举：接受 JSON 布尔，也接受上游 WebUI 传回的 "true"/"false" 字符串
			b, ok := v.(bool)
			if !ok {
				s, isStr := v.(string)
				if !isStr {
					return nil, fmt.Errorf("%w: 设置项 %s 需要布尔值", driver.ErrInvalid, f.Key)
				}
				parsed, err := strconv.ParseBool(s)
				if err != nil {
					return nil, fmt.Errorf("%w: 设置项 %s 需要布尔值", driver.ErrInvalid, f.Key)
				}
				b = parsed
			}
			if !hasOption(f, strconv.FormatBool(b)) {
				return nil, fmt.Errorf("%w: 设置项 %s 的取值 %t 不在可选范围内", driver.ErrInvalid, f.Key, b)
			}
			return b, nil
		}
		s, ok := v.(string)
		if !ok || !hasOption(f, s) {
			return nil, fmt.Errorf("%w: 设置项 %s 的取值无效", driver.ErrInvalid, f.Key)
		}
		return s, nil
	default: // string / text
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%w: 设置项 %s 需要文本", driver.ErrInvalid, f.Key)
		}
		return s, nil
	}
}

func hasOption(f driver.SettingsField, value string) bool {
	for _, o := range f.Options {
		if o.Value == value {
			return true
		}
	}
	return false
}

// parseClock 解析 HH:MM
func parseClock(s string) (int, int, error) {
	parts := strings.SplitN(strings.TrimSpace(s), ":", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("需要 HH:MM 格式，收到 %q", s)
	}
	hh, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	mm, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, fmt.Errorf("时刻 %q 超范围", s)
	}
	return hh, mm, nil
}

func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		if n != float64(int64(n)) {
			return 0, false
		}
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i, err == nil
	default:
		return 0, false
	}
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	default:
		return 0, false
	}
}
