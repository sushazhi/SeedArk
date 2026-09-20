package qbmock

import "sync"

// 偏好默认值：对齐 qBittorrent 5.2.3 的原生偏好键与出厂默认
// （src/base/preferences.h + appcontroller.cpp 的 app/preferences 响应）。
//
// 这里逐键补齐，是因为驱动的设置面板按「驱动自述的键清单」原样读写原生键：
// 缺键会让面板读到空值、写进去也存不下来（真实 qBittorrent 的
// setPreferences 同样只认自己知道的键，未知键静默忽略），
// 因此这份表就是面板读写的落地清单。
//
// 单位与原值都按 API 口径给出（限速是 bytes/s，界面层再换算 KiB/s），
// 与 qbittorrent 包的自述表一一对应。

// defaultPrefs 返回一份全新的默认偏好表（apiKey 回填到 web_ui_api_key，
// 与真实实例把当前 API 密钥回报给界面的行为一致）
func defaultPrefs(apiKey string) map[string]any {
	return map[string]any{
		// ---- 行为 ----
		"confirm_torrent_deletion":   false,
		"confirm_torrent_recheck":    false,
		"recheck_completed_torrents": false,
		"performance_warning":        true,
		"status_bar_external_ip":     false,
		"file_log_enabled":           false,
		"file_log_path":              "/config/qbittorrent/logs",
		"file_log_backup_enabled":    true,
		"file_log_max_size":          int64(65),
		"file_log_age":               int64(1),
		"file_log_age_type":          int64(0),

		// ---- 下载 ----
		"torrent_content_layout":            "Original",
		"torrent_stop_condition":            "None",
		"add_to_top_of_queue":               true,
		"add_stopped_enabled":               false,
		"merge_trackers":                    false,
		"auto_delete_mode":                  int64(0),
		"preallocate_all":                   false,
		"incomplete_files_ext":              false,
		"use_unwanted_folder":               true,
		"auto_tmm_enabled":                  true,
		"torrent_changed_tmm_enabled":       false,
		"save_path_changed_tmm_enabled":     true,
		"category_changed_tmm_enabled":      true,
		"use_category_paths_in_manual_mode": false,
		"save_path":                         "/downloads",
		"temp_path_enabled":                 false,
		"temp_path":                         "/downloads/.incomplete",
		"export_dir":                        "",
		"export_dir_fin":                    "",
		"excluded_file_names_enabled":       false,
		"excluded_file_names":               "",
		"mail_notification_enabled":         false,
		"mail_notification_sender":          "",
		"mail_notification_email":           "",
		"mail_notification_smtp":            "smtp.changeme.com",
		"mail_notification_ssl_enabled":     false,
		"mail_notification_auth_enabled":    false,
		"mail_notification_username":        "",
		"mail_notification_password":        "",
		"autorun_on_torrent_added_enabled":  false,
		"autorun_on_torrent_added_program":  "",
		"autorun_enabled":                   false,
		"autorun_program":                   "",

		// ---- 连接 ----
		"bittorrent_protocol":     int64(0),
		"listen_port":             int64(6881),
		"upnp":                    true,
		"max_connec":              int64(500),
		"max_connec_per_torrent":  int64(100),
		"max_uploads":             int64(20),
		"max_uploads_per_torrent": int64(4),
		"i2p_enabled":             false,
		"i2p_address":             "127.0.0.1",
		"i2p_port":                int64(7656),
		"i2p_mixed_mode":          false,
		"i2p_inbound_quantity":    int64(2),
		"i2p_outbound_quantity":   int64(2),
		"i2p_inbound_length":      int64(3),
		"i2p_outbound_length":     int64(3),
		"proxy_type":              "None",
		"proxy_ip":                "",
		"proxy_port":              int64(8080),
		"proxy_auth_enabled":      false,
		"proxy_username":          "",
		"proxy_password":          "",
		"proxy_hostname_lookup":   false,
		"proxy_bittorrent":        true,
		"proxy_peer_connections":  false,
		"proxy_rss":               true,
		"proxy_misc":              false,
		"ip_filter_enabled":       false,
		"ip_filter_path":          "",
		"ip_filter_trackers":      false,
		"banned_IPs":              "",

		// ---- 速度（API 口径：bytes/s）----
		"dl_limit":           int64(0),
		"up_limit":           int64(0),
		"alt_dl_limit":       int64(10240),
		"alt_up_limit":       int64(10240),
		"scheduler_enabled":  false,
		"schedule_from_hour": int64(8),
		"schedule_from_min":  int64(0),
		"schedule_to_hour":   int64(20),
		"schedule_to_min":    int64(0),
		"scheduler_days":     int64(0),
		"limit_utp_rate":     true,
		"limit_tcp_overhead": false,
		"limit_lan_peers":    true,

		// ---- BitTorrent ----
		"dht":                               true,
		"pex":                               true,
		"lsd":                               true,
		"encryption":                        int64(0),
		"anonymous_mode":                    false,
		"max_active_checking_torrents":      int64(1),
		"queueing_enabled":                  true,
		"max_active_downloads":              int64(3),
		"max_active_uploads":                int64(5),
		"max_active_torrents":               int64(5),
		"dont_count_slow_torrents":          false,
		"slow_torrent_dl_rate_threshold":    int64(2 * 1024),
		"slow_torrent_ul_rate_threshold":    int64(2 * 1024),
		"slow_torrent_inactive_timer":       int64(60),
		"max_ratio_enabled":                 false,
		"max_ratio":                         float64(-1),
		"max_seeding_time_enabled":          false,
		"max_seeding_time":                  int64(-1),
		"max_inactive_seeding_time_enabled": false,
		"max_inactive_seeding_time":         int64(-1),
		"max_ratio_act":                     int64(0),
		"add_trackers_enabled":              false,
		"add_trackers":                      defaultTrackers,
		"add_trackers_from_url_enabled":     false,
		"add_trackers_url":                  "https://raw.githubusercontent.com/ngosang/trackerslist/master/trackers_all.txt",
		"add_trackers_url_list":             defaultTrackers,

		// ---- RSS ----
		"rss_processing_enabled":              true,
		"rss_refresh_interval":                int64(30),
		"rss_fetch_delay":                     int64(2),
		"rss_max_articles_per_feed":           int64(500),
		"rss_auto_downloading_enabled":        true,
		"rss_download_repack_proper_episodes": true,
		"rss_smart_episode_filters":           defaultSmartEpisodeFilters,

		// ---- WebUI ----
		"web_ui_address":         "*",
		"web_ui_port":            int64(8080),
		"web_ui_upnp":            false,
		"use_https":              false,
		"web_ui_https_cert_path": "",
		"web_ui_https_key_path":  "",
		"web_ui_username":        "admin",
		// 与真实实例一致：密码从不回传（qBittorrent 固定给 null）
		"web_ui_password":                        nil,
		"web_ui_api_key":                         apiKey,
		"bypass_local_auth":                      false,
		"bypass_auth_subnet_whitelist_enabled":   false,
		"bypass_auth_subnet_whitelist":           "172.17.0.0/16\n192.168.0.0/16\n192.168.1.0/24",
		"web_ui_max_auth_fail_count":             int64(5),
		"web_ui_ban_duration":                    int64(3600),
		"web_ui_session_timeout":                 int64(3600),
		"alternative_webui_enabled":              false,
		"alternative_webui_path":                 "/opt/qbittorrent/webui",
		"web_ui_clickjacking_protection_enabled": true,
		"web_ui_csrf_protection_enabled":         true,
		"web_ui_secure_cookie_enabled":           true,
		"web_ui_host_header_validation_enabled":  true,
		"web_ui_domain_list":                     "*",
		"web_ui_use_custom_http_headers_enabled": false,
		"web_ui_custom_http_headers":             "",
		"web_ui_reverse_proxy_enabled":           false,
		"web_ui_reverse_proxies_list":            "",
		"dyndns_enabled":                         false,
		// 未启用动态 DNS 时上游给 -1，面板读取会回落到首项（服务的空值）
		"dyndns_service":  int64(-1),
		"dyndns_domain":   "changeme.dyndns.org",
		"dyndns_username": "",
		"dyndns_password": "",

		// ---- 高级 ----
		"resume_data_storage_type":              "Legacy",
		"torrent_content_remove_option":         "Delete",
		"memory_working_set_limit":              int64(512),
		"current_network_interface":             "",
		"current_interface_name":                "",
		"current_interface_address":             "",
		"save_resume_data_interval":             int64(60),
		"save_statistics_interval":              int64(15),
		"torrent_file_size_limit":               int64(100),
		"app_instance_name":                     "",
		"refresh_interval":                      int64(1500),
		"resolve_peer_host_names":               false,
		"resolve_peer_countries":                true,
		"reannounce_when_address_changed":       false,
		"enable_embedded_tracker":               false,
		"embedded_tracker_port":                 int64(9000),
		"embedded_tracker_port_forwarding":      false,
		"mark_of_the_web":                       false,
		"ignore_ssl_errors":                     false,
		"python_executable_path":                "",
		"bdecode_depth_limit":                   int64(100),
		"bdecode_token_limit":                   int64(10000000),
		"async_io_threads":                      int64(10),
		"hashing_threads":                       int64(1),
		"file_pool_size":                        int64(40),
		"checking_memory_use":                   int64(32),
		"disk_cache":                            int64(-1),
		"disk_cache_ttl":                        int64(60),
		"disk_queue_size":                       int64(1024),
		"disk_io_type":                          int64(0),
		"disk_io_read_mode":                     int64(1),
		"disk_io_write_mode":                    int64(1),
		"enable_coalesce_read_write":            true,
		"enable_piece_extent_affinity":          false,
		"enable_upload_suggestions":             false,
		"send_buffer_watermark":                 int64(500),
		"send_buffer_low_watermark":             int64(10),
		"send_buffer_watermark_factor":          int64(50),
		"connection_speed":                      int64(30),
		"socket_send_buffer_size":               int64(0),
		"socket_receive_buffer_size":            int64(0),
		"socket_backlog_size":                   int64(30),
		"outgoing_ports_min":                    int64(0),
		"outgoing_ports_max":                    int64(0),
		"upnp_lease_duration":                   int64(0),
		"peer_tos":                              int64(4),
		"utp_tcp_mixed_mode":                    int64(0),
		"hostname_cache_ttl":                    int64(60),
		"idn_support_enabled":                   true,
		"enable_multi_connections_from_same_ip": false,
		"validate_https_tracker_certificate":    true,
		"ssrf_mitigation":                       true,
		"block_peers_on_privileged_ports":       false,
		"upload_slots_behavior":                 int64(0),
		"upload_choking_algorithm":              int64(1),
		"announce_to_all_trackers":              false,
		"announce_to_all_tiers":                 true,
		"announce_ip":                           "",
		"announce_port":                         int64(0),
		"max_concurrent_http_announces":         int64(50),
		"stop_tracker_timeout":                  int64(5),
		"peer_turnover":                         int64(4),
		"peer_turnover_cutoff":                  int64(90),
		"peer_turnover_interval":                int64(300),
		"request_queue_size":                    int64(500),
		"dht_bootstrap_nodes":                   "dht.libtorrent.org:25401,router.bittorrent.com:6881,router.utorrent.com:6881,dht.transmissionbt.com:6881,dht.aelitis.com:6881",

		// ---- 面板外仍被其它端点读取的键（会话映射用）----
		"start_paused_enabled": false,
	}
}

// defaultTrackers qBittorrent 出厂的内置 Tracker 列表（截取常用项）
const defaultTrackers = "http://tracker.openbittorrent.com:80/announce\n" +
	"udp://tracker.openbittorrent.com:80/announce\n" +
	"udp://tracker.opentrackr.org:1337/announce\n" +
	"udp://open.tracker.cl:1337/announce\n" +
	"udp://9.rarbg.com:2810/announce"

// defaultSmartEpisodeFilters qBittorrent 出厂的智能剧集过滤规则
const defaultSmartEpisodeFilters = "s(\\d+)e(\\d+)\n" +
	"(\\d+)x(\\d+)\n" +
	"date.*(\\d{4}).*?(\\d{2}).*?(\\d{2})"

// knownPrefKeys 键表：与 defaultPrefs 同步，setPreferences 只认识这里的键
// （真实 qBittorrent 对未知键静默忽略）
func knownPrefKeys() map[string]bool {
	knownOnce.Do(func() {
		prefs := defaultPrefs("")
		knownKeys = make(map[string]bool, len(prefs))
		for k := range prefs {
			knownKeys[k] = true
		}
	})
	return knownKeys
}

var (
	knownOnce sync.Once
	knownKeys map[string]bool
)
