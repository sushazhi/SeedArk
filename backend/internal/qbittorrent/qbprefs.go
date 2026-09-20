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
			fb("confirm_torrent_deletion", "删除种子前需要确认", "Confirm torrent deletion"),
			fb("confirm_torrent_recheck", "重新校验前需要确认", "Confirm torrent recheck"),
			fb("recheck_completed_torrents", "下载完成后自动重新校验", "Recheck torrents on completion"),
			fb("performance_warning", "显示性能告警", "Show performance warning"),
			fb("status_bar_external_ip", "状态栏显示外部 IP", "Show external IP in status bar"),
			fb("file_log_enabled", "启用日志文件", "Enable log file"),
			fs("file_log_path", "日志文件路径", "Log file path"),
			fb("file_log_backup_enabled", "日志轮转备份", "Backup log files"),
			rng(fi("file_log_max_size", "单个日志文件上限", "Log file max size", "KiB"), 1, 1<<30),
			rng(fi("file_log_age", "删除超过此期限的备份日志", "Delete old logs older than", ""), 1, 365),
			fenum("file_log_age_type", "备份日志期限单位", "Old log age units",
				optInt(0, "天", "days"), optInt(1, "月", "months"), optInt(2, "年", "years")),
		},
	},
	{
		Key: "downloads", Label: "下载", LabelEn: "Downloads", Icon: driver.SettingIconDownload,
		Fields: []driver.SettingsField{
			fenumStr("torrent_content_layout", "默认内容布局", "Default torrent content layout",
				optStr("Original", "原始（种子内结构）", "Original"),
				optStr("Subfolder", "创建子文件夹", "Create subfolder"),
				optStr("NoSubfolder", "不创建子文件夹", "Don't create subfolder")),
			fenumStr("torrent_stop_condition", "添加后暂停于", "Torrent stop condition",
				optStr("None", "不暂停", "None"),
				optStr("MetadataReceived", "收到元数据", "Metadata received"),
				optStr("FilesChecked", "文件校验完成", "Files checked")),
			fb("add_to_top_of_queue", "新种子加入队列顶部", "Add to top of queue"),
			fb("add_stopped_enabled", "添加后不自动开始", "Do not start automatically"),
			fb("merge_trackers", "添加种子时合并 Trackers", "Merge trackers on add"),
			fenum("auto_delete_mode", "添加完成后删除 .torrent 文件", "Delete .torrent files afterwards",
				optInt(autoDeleteNever, "从不", "Never"),
				optInt(autoDeleteIfAdded, "成功添加后", "If added"),
				optInt(autoDeleteAlways, "总是", "Always")),
			fb("preallocate_all", "预分配磁盘空间", "Pre-allocate disk space"),
			fb("incomplete_files_ext", "未完成文件追加 !qB 后缀", "Append .!qB extension"),
			fb("use_unwanted_folder", "未选择的文件放入 .unwanted 目录", "Use .unwanted folder"),
			fb("auto_tmm_enabled", "默认使用自动管理模式", "Torrent management mode: automatic"),
			fb("torrent_changed_tmm_enabled", "种子变化时重新定位（否则转为手动模式）", "Relocate on torrent change"),
			fb("save_path_changed_tmm_enabled", "默认保存路径变化时迁移受影响种子", "Relocate on save path change"),
			fb("category_changed_tmm_enabled", "分类路径变化时迁移受影响种子", "Relocate on category change"),
			fb("use_category_paths_in_manual_mode", "手动模式下也使用分类保存路径", "Use category paths in manual mode"),
			fs("save_path", "默认保存路径", "Default save path"),
			fb("temp_path_enabled", "使用未完成文件目录", "Keep incomplete torrents in"),
			fs("temp_path", "未完成文件目录", "Incomplete files path"),
			hint(fs("export_dir", "导出 .torrent 副本到目录", "Copy .torrent files to"),
				"留空表示不导出", "Leave empty to disable"),
			hint(fs("export_dir_fin", "下载完成时导出 .torrent 副本到目录", "Copy .torrent files for finished downloads to"),
				"留空表示不导出", "Leave empty to disable"),
			fb("excluded_file_names_enabled", "排除指定文件名", "Exclude file names"),
			hint(ft("excluded_file_names", "排除的文件名（每行一个）", "Excluded file names (one per line)"),
				"支持通配符，如 *.exe", "Wildcards are supported, e.g. *.exe"),
			fb("mail_notification_enabled", "邮件通知（下载完成时）", "Email notification on download finish"),
			fs("mail_notification_sender", "发件人", "Sender"),
			fs("mail_notification_email", "收件人", "Recipient"),
			fs("mail_notification_smtp", "SMTP 服务器", "SMTP server"),
			fb("mail_notification_ssl_enabled", "使用 SSL", "Use SSL"),
			fb("mail_notification_auth_enabled", "需要认证", "Authentication required"),
			fs("mail_notification_username", "SMTP 用户名", "SMTP username"),
			secret(fs("mail_notification_password", "SMTP 密码", "SMTP password")),
			fb("autorun_on_torrent_added_enabled", "添加种子时运行外部程序", "Run external program on torrent added"),
			fs("autorun_on_torrent_added_program", "添加时运行的程序", "Program to run on torrent added"),
			fb("autorun_enabled", "下载完成时运行外部程序", "Run external program on torrent finished"),
			fs("autorun_program", "完成时运行的程序", "Program to run on torrent finished"),
		},
	},
	{
		Key: "connection", Label: "连接", LabelEn: "Connection", Icon: driver.SettingIconConnect,
		Fields: []driver.SettingsField{
			fenum("bittorrent_protocol", "传输协议", "Transport protocol",
				optInt(protoBoth, "TCP 与 uTP", "TCP and uTP"),
				optInt(protoTCP, "仅 TCP", "TCP only"),
				optInt(protoUTP, "仅 uTP", "uTP only")),
			rng(fi("listen_port", "监听端口", "Listening port", ""), 0, 65535),
			fb("upnp", "使用 UPnP / NAT-PMP 端口转发", "Use UPnP / NAT-PMP port forwarding"),
			hint(rng(fi("max_connec", "全局最大连接数", "Global maximum connections", ""), 0, 100000),
				"0 表示不限制", "0 means unlimited"),
			rng(fi("max_connec_per_torrent", "单种最大连接数", "Maximum connections per torrent", ""), 0, 100000),
			hint(rng(fi("max_uploads", "全局最大上传槽", "Global maximum upload slots", ""), 0, 100000),
				"0 表示不限制", "0 means unlimited"),
			rng(fi("max_uploads_per_torrent", "单种最大上传槽", "Maximum upload slots per torrent", ""), 0, 100000),
			fb("i2p_enabled", "启用 I2P", "Enable I2P"),
			fs("i2p_address", "I2P 地址", "I2P address"),
			rng(fi("i2p_port", "I2P 端口", "I2P port", ""), 0, 65535),
			fb("i2p_mixed_mode", "I2P 混合模式", "I2P mixed mode"),
			rng(fi("i2p_inbound_quantity", "I2P 入站隧道数", "I2P inbound quantity", ""), 0, 100),
			rng(fi("i2p_outbound_quantity", "I2P 出站隧道数", "I2P outbound quantity", ""), 0, 100),
			rng(fi("i2p_inbound_length", "I2P 入站隧道长度", "I2P inbound length", ""), 0, 10),
			rng(fi("i2p_outbound_length", "I2P 出站隧道长度", "I2P outbound length", ""), 0, 10),
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
			fb("proxy_bittorrent", "使用代理连接 Peer", "Use proxy for peer connections"),
			fb("proxy_peer_connections", "经代理的 Peer 连接（uTP 不可用）", "Disable connections not supported by proxies"),
			fb("proxy_rss", "使用代理获取 RSS", "Use proxy for RSS"),
			fb("proxy_misc", "使用代理获取其他内容", "Use proxy for general purposes"),
			fb("ip_filter_enabled", "启用 IP 过滤", "Enable IP filtering"),
			hint(fs("ip_filter_path", "IP 过滤规则文件", "Filter path"),
				"qBittorrent 服务端本地路径", "Path on the qBittorrent host"),
			fb("ip_filter_trackers", "同时过滤 Tracker", "Apply filter to trackers"),
			ft("banned_IPs", "手动封禁的 IP（每行一个）", "Manually banned IP addresses (one per line)"),
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
			fb("limit_utp_rate", "备用限速时限制 uTP 流量", "Apply rate limit to uTP protocol"),
			fb("limit_tcp_overhead", "限速计入 TCP 协议开销", "Apply rate limit to transport overhead"),
			fb("limit_lan_peers", "局域网 Peer 也计入限速", "Apply rate limit to peers on LAN"),
		},
	},
	{
		Key: "bittorrent", Label: "BitTorrent", LabelEn: "BitTorrent", Icon: driver.SettingIconBitTorrent,
		Fields: []driver.SettingsField{
			fb("dht", "启用 DHT", "Enable DHT"),
			fb("pex", "启用 Peer 交换（PeX）", "Enable PeX"),
			fb("lsd", "启用本地 Peer 发现（LSD）", "Enable LSD"),
			fenum("encryption", "加密模式", "Encryption mode",
				optInt(encAllow, "允许加密", "Allow encryption"),
				optInt(encRequire, "强制加密", "Require encryption"),
				optInt(encDisable, "禁用加密", "Disable encryption")),
			fb("anonymous_mode", "匿名模式", "Anonymous mode"),
			rng(fi("max_active_checking_torrents", "同时校验种子数上限", "Max active checking torrents", ""), 0, 10000),
			fb("queueing_enabled", "启用队列", "Enable queueing"),
			hint(rng(fi("max_active_downloads", "最大同时下载数", "Maximum active downloads", ""), 0, 10000),
				"-1 表示不限制", "-1 means unlimited"),
			hint(rng(fi("max_active_uploads", "最大同时做种数", "Maximum active uploads", ""), 0, 10000),
				"-1 表示不限制", "-1 means unlimited"),
			hint(rng(fi("max_active_torrents", "最大同时活动种子数", "Maximum active torrents", ""), 0, 10000),
				"-1 表示不限制", "-1 means unlimited"),
			fb("dont_count_slow_torrents", "慢速种子不计入队列上限", "Do not count slow torrents"),
			scale(fi("slow_torrent_dl_rate_threshold", "慢速判定：下载速率低于", "Download rate threshold", "KiB/s"), 1024),
			scale(fi("slow_torrent_ul_rate_threshold", "慢速判定：上传速率低于", "Upload rate threshold", "KiB/s"), 1024),
			rng(fi("slow_torrent_inactive_timer", "无传输多久后视为不活跃", "Torrent inactivity timer", "秒"), 0, 1<<20),
			fb("max_ratio_enabled", "启用全局分享率限制", "Enable share ratio limit"),
			hint(ff("max_ratio", "分享率上限", "Share ratio limit", ""),
				"-1 表示不限制", "-1 means unlimited"),
			fb("max_seeding_time_enabled", "启用全局做种时长限制", "Enable seeding time limit"),
			hint(rng(fi("max_seeding_time", "做种时长上限", "Seeding time limit", "分钟"), -1, 1<<20),
				"-1 表示不限制", "-1 means unlimited"),
			fb("max_inactive_seeding_time_enabled", "启用全局闲置做种限制", "Enable inactive seeding time limit"),
			hint(rng(fi("max_inactive_seeding_time", "闲置做种时长上限", "Inactive seeding time limit", "分钟"), -1, 1<<20),
				"-1 表示不限制", "-1 means unlimited"),
			fenum("max_ratio_act", "达到限制时的动作", "When ratio/time limit reached",
				optInt(ratioStop, "停止种子", "Stop torrent"),
				optInt(ratioRemove, "删除种子", "Remove torrent"),
				optInt(ratioWipe, "删除种子及其文件", "Remove torrent and its files"),
				optInt(ratioSuper, "启用超级做种", "Enable super seeding")),
			fb("add_trackers_enabled", "自动为种子添加 Tracker", "Add trackers to new downloads"),
			ft("add_trackers", "追加的 Tracker（每行一个）", "Additional trackers (one per line)"),
			fb("add_trackers_from_url_enabled", "从订阅地址获取 Tracker", "Add trackers from URL"),
			fs("add_trackers_url", "Tracker 订阅地址", "Trackers URL"),
			readOnly(ft("add_trackers_url_list", "订阅地址解析结果", "Trackers fetched from URL")),
		},
	},
	{
		Key: "rss", Label: "RSS", LabelEn: "RSS", Icon: driver.SettingIconRSS,
		Fields: []driver.SettingsField{
			fb("rss_processing_enabled", "启用 RSS 抓取", "Enable fetching RSS feeds"),
			rng(fi("rss_refresh_interval", "抓取间隔", "Feeds refresh interval", "分钟"), 1, 1<<20),
			rng(fi("rss_fetch_delay", "同站请求延迟", "Same host request delay", "秒"), 0, 1<<20),
			rng(fi("rss_max_articles_per_feed", "每个订阅最多保留文章数", "Maximum articles per feed", ""), 0, 1<<20),
			fb("rss_auto_downloading_enabled", "启用 RSS 自动下载", "Enable auto downloading of RSS torrents"),
			fb("rss_download_repack_proper_episodes", "下载 REPACK / PROPER 剧集", "Download REPACK/PROPER episodes"),
			ft("rss_smart_episode_filters", "智能剧集过滤规则（每行一条）", "Smart episode filters (one per line)"),
		},
	},
	{
		Key: "webui", Label: "WebUI", LabelEn: "WebUI", Icon: driver.SettingIconWebUI,
		Fields: []driver.SettingsField{
			danger(fs("web_ui_address", "监听地址", "IP address")),
			danger(rng(fi("web_ui_port", "监听端口", "Port", ""), 1, 65535)),
			fb("web_ui_upnp", "用 UPnP / NAT-PMP 转发端口", "Use UPnP / NAT-PMP to forward the port"),
			danger(fb("use_https", "启用 HTTPS", "Use HTTPS instead of HTTP")),
			danger(fs("web_ui_https_cert_path", "证书文件", "Certificate")),
			danger(fs("web_ui_https_key_path", "私钥文件", "Key")),
			danger(fs("web_ui_username", "用户名", "Username")),
			danger(writeOnly(secret(fs("web_ui_password", "密码", "Password")))),
			readOnly(ft("web_ui_api_key", "API 密钥（本应用中即填入的 API 令牌）", "API key")),
			danger(fb("bypass_local_auth", "本机访问免认证", "Bypass authentication for clients on localhost")),
			fb("bypass_auth_subnet_whitelist_enabled", "指定网段免认证", "Bypass authentication for clients in whitelisted IP subnets"),
			ft("bypass_auth_subnet_whitelist", "免认证网段（每行一个）", "Whitelisted IP subnets (one per line)"),
			rng(fi("web_ui_max_auth_fail_count", "连续失败多少次后封禁", "Ban client after consecutive failures", ""), 1, 10000),
			rng(fi("web_ui_ban_duration", "封禁时长", "Ban for", "秒"), 1, 1<<24),
			rng(fi("web_ui_session_timeout", "会话超时", "Session timeout", "秒"), 0, 1<<24),
			fb("alternative_webui_enabled", "使用备用 WebUI", "Use alternative WebUI"),
			fs("alternative_webui_path", "备用 WebUI 目录", "Files location"),
			fb("web_ui_clickjacking_protection_enabled", "点击劫持保护", "Clickjacking protection"),
			fb("web_ui_csrf_protection_enabled", "CSRF 保护", "CSRF protection"),
			fb("web_ui_secure_cookie_enabled", "Secure Cookie", "Enable Host header validation"),
			fb("web_ui_host_header_validation_enabled", "校验 Host 头", "Enable Host header validation"),
			ft("web_ui_domain_list", "允许的域名（每行一个）", "Server domains (one per line)"),
			fb("web_ui_use_custom_http_headers_enabled", "使用自定义 HTTP 头", "Use custom HTTP headers"),
			ft("web_ui_custom_http_headers", "自定义 HTTP 头", "Custom HTTP headers"),
			fb("web_ui_reverse_proxy_enabled", "支持反向代理", "Enable reverse proxy support"),
			ft("web_ui_reverse_proxies_list", "受信任的反向代理（每行一个）", "Trusted reverse proxies (one per line)"),
			fb("dyndns_enabled", "启用动态 DNS", "Use dynamic DNS service"),
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
			fenumStr("resume_data_storage_type", "续传数据存储格式", "Resume data storage type",
				optStr("Legacy", "Fastresume 文件", "Fastresume files"),
				optStr("SQLite", "SQLite 数据库（实验性）", "SQLite database (experimental)")),
			fenumStr("torrent_content_remove_option", "删除种子内容的方式", "Torrent content removing mode",
				optStr("Delete", "永久删除文件", "Delete files permanently"),
				optStr("MoveToTrash", "移入回收站（如可用）", "Move files to trash (if possible)")),
			rng(fi("memory_working_set_limit", "物理内存占用上限", "Physical memory usage limit", "MiB"), 0, 1<<20),
			fs("current_network_interface", "网络接口", "Network interface"),
			readOnly(fs("current_interface_name", "当前接口名称", "Interface name")),
			hint(fs("current_interface_address", "绑定地址", "Optional IP address to bind to"),
				"留空表示自动", "Leave empty for any"),
			rng(fi("save_resume_data_interval", "保存续传数据间隔", "Save resume data interval", "分钟"), 1, 1<<20),
			rng(fi("save_statistics_interval", "保存统计信息间隔", "Save statistics interval", "分钟"), 1, 1<<20),
			hint(rng(fi("torrent_file_size_limit", "种子文件大小上限", "Torrent file size limit", "MiB"), 0, 1<<20),
				"超过则拒绝添加", "Reject larger torrents"),
			fs("app_instance_name", "实例名称（窗口标题后缀）", "Instance name"),
			rng(fi("refresh_interval", "界面刷新间隔", "Refresh interval", "毫秒"), 100, 60000),
			fb("resolve_peer_host_names", "解析 Peer 主机名", "Resolve peer host names"),
			fb("resolve_peer_countries", "解析 Peer 国家/地区", "Resolve peer countries"),
			fb("reannounce_when_address_changed", "网络地址变化时重新汇报", "Reannounce to trackers when address changes"),
			fb("enable_embedded_tracker", "启用内置 Tracker", "Enable embedded tracker"),
			rng(fi("embedded_tracker_port", "内置 Tracker 端口", "Embedded tracker port", ""), 1, 65535),
			fb("embedded_tracker_port_forwarding", "为内置 Tracker 端口转发", "Enable port forwarding for embedded tracker"),
			fb("mark_of_the_web", "为下载文件添加 Mark-of-the-Web", "Enable Mark-of-the-Web"),
			fb("ignore_ssl_errors", "忽略 SSL 证书错误", "Ignore SSL certificate errors"),
			fs("python_executable_path", "Python 可执行文件路径", "Python executable path"),
			rng(fi("bdecode_depth_limit", "bdecode 嵌套深度上限", "Bdecode depth limit", ""), 0, 1<<20),
			rng(fi("bdecode_token_limit", "bdecode Token 数量上限", "Bdecode token limit", ""), 0, 1<<30),
			rng(fi("async_io_threads", "异步 IO 线程数", "Asynchronous I/O threads", ""), 0, 1024),
			rng(fi("hashing_threads", "校验线程数", "Hashing threads", ""), 0, 1024),
			rng(fi("file_pool_size", "文件句柄池大小", "File pool size", ""), 1, 1<<20),
			rng(fi("checking_memory_use", "校验时内存占用上限", "Outstanding memory when checking torrents", "MiB"), 0, 1<<20),
			rng(fi("disk_cache", "磁盘缓存", "Disk cache", "MiB"), -1, 1<<20),
			rng(fi("disk_cache_ttl", "磁盘缓存写入间隔", "Disk cache expiry interval", "秒"), 0, 1<<24),
			rng(fi("disk_queue_size", "磁盘队列大小", "Disk queue size", "KiB"), 0, 1<<24),
			fenum("disk_io_type", "磁盘 IO 类型", "Disk IO type",
				optInt(diskIODefault, "默认", "Default"),
				optInt(diskIOMmap, "内存映射文件", "Memory mapped files"),
				optInt(diskIOPosix, "POSIX 兼容", "POSIX-compliant"),
				optInt(diskIOSimple, "简单 pread/pwrite", "Simple pread/pwrite")),
			fenum("disk_io_read_mode", "磁盘读模式", "Disk IO read mode",
				optInt(ioOSCacheOff, "禁用系统缓存", "Disable OS cache"),
				optInt(ioOSCacheOn, "启用系统缓存", "Enable OS cache")),
			fenum("disk_io_write_mode", "磁盘写模式", "Disk IO write mode",
				optInt(ioOSCacheOff, "禁用系统缓存", "Disable OS cache"),
				optInt(ioOSCacheOn, "启用系统缓存", "Enable OS cache"),
				optInt(ioWriteThrough, "直写（Write-through）", "Write-through")),
			fb("enable_coalesce_read_write", "合并读写操作", "Coalesce reads & writes"),
			fb("enable_piece_extent_affinity", "启用分片区段亲和", "Enable piece extent affinity"),
			fb("enable_upload_suggestions", "启用上传建议（Suggest）", "Send upload piece suggestions"),
			rng(fi("send_buffer_watermark", "发送缓冲区高水位", "Send buffer watermark", "KiB"), 0, 1<<20),
			rng(fi("send_buffer_low_watermark", "发送缓冲区低水位", "Send buffer low watermark", "KiB"), 0, 1<<20),
			rng(fi("send_buffer_watermark_factor", "水位调节系数", "Send buffer watermark factor", "%"), 50, 500),
			rng(fi("connection_speed", "每秒新建连接数", "Outgoing connections per second", ""), 0, 1<<10),
			rng(fi("socket_send_buffer_size", "Socket 发送缓冲", "Socket send buffer size", "KiB"), 0, 1<<20),
			rng(fi("socket_receive_buffer_size", "Socket 接收缓冲", "Socket receive buffer size", "KiB"), 0, 1<<20),
			rng(fi("socket_backlog_size", "Socket 连接积压队列", "Socket backlog size", ""), 1, 1<<20),
			rng(fi("outgoing_ports_min", "出站端口下限", "Outgoing ports (Min)", ""), 0, 65535),
			rng(fi("outgoing_ports_max", "出站端口上限", "Outgoing ports (Max)", ""), 0, 65535),
			rng(fi("upnp_lease_duration", "UPnP 租约时长", "UPnP lease duration", "秒"), 0, 1<<24),
			hint(rng(fi("peer_tos", "Peer 流量的 DSCP 标记", "Peer DSCP", ""), -1, 63),
				"用于 QoS 流量分类", "Used for QoS classification"),
			fenum("utp_tcp_mixed_mode", "uTP-TCP 混合模式", "uTP-TCP mixed mode algorithm",
				optInt(mixedPreferTCP, "偏向 TCP", "Prefer TCP"),
				optInt(mixedPeerProp, "按 Peer 比例（限制 TCP）", "Peer proportional (throttles TCP)")),
			rng(fi("hostname_cache_ttl", "主机名解析缓存有效期", "Internal hostname resolver cache expiry interval", "秒"), 0, 1<<24),
			fb("idn_support_enabled", "支持国际化域名（IDN）", "Support internationalized domain name"),
			fb("enable_multi_connections_from_same_ip", "允许同一 IP 的多条连接", "Allow multiple connections from the same IP address"),
			fb("validate_https_tracker_certificate", "校验 HTTPS Tracker 证书", "Validate HTTPS tracker certificate"),
			fb("ssrf_mitigation", "启用 SSRF 缓解", "Enable SSRF mitigation"),
			fb("block_peers_on_privileged_ports", "拒绝特权端口上的 Peer", "Block peers on privileged ports"),
			fenum("upload_slots_behavior", "上传槽分配策略", "Upload slots behavior",
				optInt(slotsFixed, "固定槽位", "Fixed slots"),
				optInt(slotsRate, "按上传速率", "Upload rate based")),
			fenum("upload_choking_algorithm", "上传阻塞算法", "Upload choking algorithm",
				optInt(chokeRoundRobin, "轮询", "Round-robin"),
				optInt(chokeFastest, "最快上传", "Fastest upload"),
				optInt(chokeAntiLeech, "反吸血", "Anti-leech")),
			fb("announce_to_all_trackers", "向所有 Tracker 汇报", "Always announce to all trackers"),
			fb("announce_to_all_tiers", "向所有层级汇报", "Always announce to all tiers"),
			hint(fs("announce_ip", "汇报给 Tracker 的 IP", "IP address reported to trackers"),
				"留空表示自动", "Leave empty for automatic"),
			hint(rng(fi("announce_port", "汇报给 Tracker 的端口", "Port reported to trackers", ""), 0, 65535),
				"0 表示使用监听端口", "0 means the listening port"),
			rng(fi("max_concurrent_http_announces", "并发 HTTP 汇报数上限", "Max concurrent HTTP announces", ""), 1, 1<<20),
			rng(fi("stop_tracker_timeout", "停止时等待 Tracker 响应的超时", "Stop tracker timeout", "秒"), 0, 1<<20),
			rng(fi("peer_turnover", "Peer 连接更替比例", "Peer turnover", "%"), 0, 100),
			rng(fi("peer_turnover_cutoff", "超过多少比例才触发更替", "Peer turnover threshold", "%"), 0, 100),
			rng(fi("peer_turnover_interval", "Peer 更替检查间隔", "Peer turnover disconnect interval", "秒"), 0, 1<<20),
			rng(fi("request_queue_size", "请求队列大小", "Request queue size", ""), 1, 1<<20),
			fs("dht_bootstrap_nodes", "DHT 引导节点", "DHT bootstrap nodes"),
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
