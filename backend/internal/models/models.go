package models

// ApiResponse 统一响应格式
type ApiResponse struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
	// ErrorCode 稳定的机器可读错误码（见 errcode.go）。界面按它选择文案语言，
	// Message 只作日志与兜底：英文界面下直接展示中文 Message 会串语言
	ErrorCode string `json:"errorCode,omitempty"`
}

// OK 构造成功响应
func OK(data interface{}) ApiResponse {
	return ApiResponse{Code: 0, Message: "success", Data: data}
}

// Error 构造失败响应
func Error(msg string) ApiResponse {
	return ApiResponse{Code: 1, Message: msg}
}

// ErrorWithCode 构造带错误码的失败响应
func ErrorWithCode(code, msg string) ApiResponse {
	return ApiResponse{Code: 1, Message: msg, ErrorCode: code}
}

// Torrent 种子数据（与前端 TS 类型对齐）
type Torrent struct {
	ID                  int64    `json:"id"`
	Name                string   `json:"name"`
	HashString          string   `json:"hashString"`
	Creator             string   `json:"creator"`
	TotalSize           int64    `json:"totalSize"`
	SizeWhenDone        int64    `json:"sizeWhenDone"`
	PercentDone         float64  `json:"percentDone"`
	Status              int64    `json:"status"`
	RateDownload        int64    `json:"rateDownload"`
	RateUpload          int64    `json:"rateUpload"`
	ETA                 int64    `json:"eta"`
	UploadedEver        int64    `json:"uploadedEver"`
	DownloadedEver      int64    `json:"downloadedEver"`
	UploadRatio         float64  `json:"uploadRatio"`
	SecondsSeeding      int64    `json:"secondsSeeding"`
	Error               int64    `json:"error"`
	ErrorString         string   `json:"errorString"`
	Labels              []string `json:"labels"`
	QueuePosition       int64    `json:"queuePosition"`
	PeersConnected      int64    `json:"peersConnected"`
	PeersSendingToUs    int64    `json:"peersSendingToUs"`
	PeersGettingFromUs  int64    `json:"peersGettingFromUs"`
	DownloadDir         string   `json:"downloadDir"`
	AddedDate           int64    `json:"addedDate"`
	DoneDate            int64    `json:"doneDate"`
	ActivityDate        int64    `json:"activityDate"`
	IsFinished          bool     `json:"isFinished"`
	IsStalled           bool     `json:"isStalled"`
	IsPrivate           bool     `json:"isPrivate"`
	MagnetLink          string   `json:"magnetLink"`
	FileCount           int64    `json:"fileCount"`
	HaveValid           int64    `json:"haveValid"`
	HaveUnchecked       int64    `json:"haveUnchecked"`
	LeftUntilDone       int64    `json:"leftUntilDone"`
	Comment             string   `json:"comment"`
	PeerLimit           int64    `json:"peerLimit"`
	SeedIdleLimit       int64    `json:"seedIdleLimit"`
	SeedIdleMode        int64    `json:"seedIdleMode"`
	SeedRatioLimit      float64  `json:"seedRatioLimit"`
	SeedRatioMode       int64    `json:"seedRatioMode"`
	BandwidthPriority   int64    `json:"bandwidthPriority"`
	DownloadLimited     bool     `json:"downloadLimited"`
	DownloadLimit       int64    `json:"downloadLimit"`
	UploadLimited       bool     `json:"uploadLimited"`
	UploadLimit         int64    `json:"uploadLimit"`
	HonorsSessionLimits bool     `json:"honorsSessionLimits"`

	// 带宽组（Transmission 4.x），列表与详情均返回（raw RPC 合并）
	Groups []string `json:"groups,omitempty"`

	// 归属服务器（多下载器聚合视图）。无归属时 ServerIndex 为 nil、其余为空串，
	// 序列化时省略，既有前端与状态文件不受影响。
	//
	// ServerIndex 是「服务器列表」里的下标（与 /api/servers 的 index 一致），
	// 也是批量操作用来路由回各自下载器的依据。
	// 用指针而不是 int + omitempty：0 是合法索引（第一台服务器），
	// 值类型会把 0 号当成「没有归属」而丢掉该字段，界面就分不出第一台了。
	ServerIndex *int   `json:"serverIndex,omitempty"`
	ServerName  string `json:"serverName,omitempty"`
	Kind        string `json:"kind,omitempty"`

	// TrackerStats 列表与详情均返回（列表列显示主 Tracker 主机名）
	TrackerStats []TrackerStat `json:"trackerStats,omitempty"`

	// 以下为详情字段，列表接口不返回
	Files              []FileInfo `json:"files,omitempty"`
	FileStats          []FileStat `json:"fileStats,omitempty"`
	Trackers           []Tracker  `json:"trackers,omitempty"`
	Peers              []Peer     `json:"peers,omitempty"`
	SequentialDownload bool       `json:"sequentialDownload,omitempty"`

	// 块位图（详情接口返回，base64 编码）
	Pieces     string `json:"pieces,omitempty"`
	PieceCount int64  `json:"pieceCount,omitempty"`
	PieceSize  int64  `json:"pieceSize,omitempty"`
}

// BandwidthGroup 带宽组（Transmission 4.x group-get / group-set）
type BandwidthGroup struct {
	Name                string `json:"name"`
	DownKB              int64  `json:"downKB"` // KB/s，0 = 不限
	UpKB                int64  `json:"upKB"`   // KB/s，0 = 不限
	DownEnabled         bool   `json:"downEnabled"`
	UpEnabled           bool   `json:"upEnabled"`
	HonorsSessionLimits bool   `json:"honorsSessionLimits"`
}

// PathMapping 远端（Transmission 视角）→ 本地（宿主视角）路径映射
type PathMapping struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// FileInfo 文件信息
type FileInfo struct {
	BytesCompleted int64  `json:"bytesCompleted"`
	Length         int64  `json:"length"`
	Name           string `json:"name"`
}

// FileStat 文件统计（是否选择下载、优先级）
type FileStat struct {
	BytesCompleted int64 `json:"bytesCompleted"`
	Wanted         bool  `json:"wanted"`
	Priority       int64 `json:"priority"`
}

// Tracker Tracker 基础信息
type Tracker struct {
	Announce string `json:"announce"`
	ID       int64  `json:"id"`
	Scrape   string `json:"scrape"`
	SiteName string `json:"sitename"`
	Tier     int64  `json:"tier"`
}

// TrackerStat Tracker 状态
type TrackerStat struct {
	ID                    int64  `json:"id"`
	Host                  string `json:"host"`
	Announce              string `json:"announce"`
	AnnounceState         int64  `json:"announceState"`
	Tier                  int64  `json:"tier"`
	IsBackup              bool   `json:"isBackup"`
	LastAnnounceResult    string `json:"lastAnnounceResult"`
	LastAnnounceSucceeded bool   `json:"lastAnnounceSucceeded"`
	LastAnnounceTimedOut  bool   `json:"lastAnnounceTimedOut"`
	LastAnnounceTime      int64  `json:"lastAnnounceTime"`
	LastAnnouncePeerCount int64  `json:"lastAnnouncePeerCount"`
	NextAnnounceTime      int64  `json:"nextAnnounceTime"`
	ScrapeState           int64  `json:"scrapeState"`
	LastScrapeResult      string `json:"lastScrapeResult"`
	LastScrapeSucceeded   bool   `json:"lastScrapeSucceeded"`
	LastScrapeTime        int64  `json:"lastScrapeTime"`
	NextScrapeTime        int64  `json:"nextScrapeTime"`
	SeederCount           int64  `json:"seederCount"`
	LeecherCount          int64  `json:"leecherCount"`
	DownloadCount         int64  `json:"downloadCount"`
}

// Peer Peer 信息
type Peer struct {
	Address           string  `json:"address"`
	ClientName        string  `json:"clientName"`
	FlagStr           string  `json:"flagStr"`
	Progress          float64 `json:"progress"`
	RateToClient      int64   `json:"rateToClient"`
	RateToPeer        int64   `json:"rateToPeer"`
	IsDownloadingFrom bool    `json:"isDownloadingFrom"`
	IsUploadingTo     bool    `json:"isUploadingTo"`
	IsEncrypted       bool    `json:"isEncrypted"`
	IsIncoming        bool    `json:"isIncoming"`
	IsUTP             bool    `json:"isUTP"`
	Port              int64   `json:"port"`
}

// Session 会话信息
type Session struct {
	Version string `json:"version"`
	// Type 当前下载器类型（transmission / qbittorrent），由 API 层填充
	Type string `json:"type"`
	// Caps 该下载器的能力自述，前端据此隐藏不支持的入口
	Caps *Capabilities `json:"caps,omitempty"`
	// Schema 该下载器的设置界面字段自述。前端按它通用渲染设置面板，
	// 取值走 QB 这类原生键值通道；为空时回退到下方的通用会话字段
	Schema                           []SettingsSection `json:"schema,omitempty"`
	RPCVersion                       int64             `json:"rpcVersion"`
	DownloadDir                      string            `json:"downloadDir"`
	SpeedLimitDown                   int64             `json:"speedLimitDown"`
	SpeedLimitDownOn                 bool              `json:"speedLimitDownOn"`
	SpeedLimitUp                     int64             `json:"speedLimitUp"`
	SpeedLimitUpOn                   bool              `json:"speedLimitUpOn"`
	AltSpeedDown                     int64             `json:"altSpeedDown"`
	AltSpeedUp                       int64             `json:"altSpeedUp"`
	AltSpeedEnabled                  bool              `json:"altSpeedEnabled"`
	PeerLimitGlobal                  int64             `json:"peerLimitGlobal"`
	PeerPort                         int64             `json:"peerPort"`
	PeerPortRandomOnStart            bool              `json:"peerPortRandomOnStart"`
	PEXEnabled                       bool              `json:"pexEnabled"`
	DHTEnabled                       bool              `json:"dhtEnabled"`
	LPDEnabled                       bool              `json:"lpdEnabled"`
	UTPEnabled                       bool              `json:"utpEnabled"`
	Encryption                       string            `json:"encryption"`
	SeedRatioLimit                   float64           `json:"seedRatioLimit"`
	StartAdded                       bool              `json:"startAdded"`
	IncompleteDir                    string            `json:"incompleteDir"`
	DownloadQueueSize                int64             `json:"downloadQueueSize"`
	DownloadQueueEnabled             bool              `json:"downloadQueueEnabled"`
	SeedQueueSize                    int64             `json:"seedQueueSize"`
	SeedQueueEnabled                 bool              `json:"seedQueueEnabled"`
	QueueStalledEnabled              bool              `json:"queueStalledEnabled"`
	QueueStalledMinutes              int64             `json:"queueStalledMinutes"`
	BlocklistEnabled                 bool              `json:"blocklistEnabled"`
	BlocklistURL                     string            `json:"blocklistUrl"`
	BlocklistSize                    int64             `json:"blocklistSize"`
	PortForwardingEnabled            bool              `json:"portForwardingEnabled"`
	IncompleteDirEnabled             bool              `json:"incompleteDirEnabled"`
	CacheSizeMB                      int64             `json:"cacheSizeMB"`
	AltSpeedTimeEnabled              bool              `json:"altSpeedTimeEnabled"`
	AltSpeedTimeBegin                int64             `json:"altSpeedTimeBegin"`
	AltSpeedTimeEnd                  int64             `json:"altSpeedTimeEnd"`
	AltSpeedTimeDay                  int64             `json:"altSpeedTimeDay"`
	ScriptTorrentAddedEnabled        bool              `json:"scriptTorrentAddedEnabled"`
	ScriptTorrentAddedFilename       string            `json:"scriptTorrentAddedFilename"`
	ScriptTorrentDoneEnabled         bool              `json:"scriptTorrentDoneEnabled"`
	ScriptTorrentDoneFilename        string            `json:"scriptTorrentDoneFilename"`
	ScriptTorrentDoneSeedingEnabled  bool              `json:"scriptTorrentDoneSeedingEnabled"`
	ScriptTorrentDoneSeedingFilename string            `json:"scriptTorrentDoneSeedingFilename"`
	DefaultTrackers                  []string          `json:"defaultTrackers"`
	RenamePartialFiles               bool              `json:"renamePartialFiles"`
	TrashOriginalTorrentFiles        bool              `json:"trashOriginalTorrentFiles"`
	IdleSeedingLimitEnabled          bool              `json:"idleSeedingLimitEnabled"`
	IdleSeedingLimit                 int64             `json:"idleSeedingLimit"`
	// Prefs 该驱动的原生偏好键值对（Schema 里字段的取值来源）。
	// 键名与取值对齐各下载器自己的原生配置接口——qBittorrent 即 app/preferences
	// 的 snake_case 键。前端只按 Schema 通用渲染，不关心是谁的键。
	//
	// 中性命名是刻意的：原先叫 QB，接第二个下载器时这个名字就成了阻碍
	// （Transmission 若也走自述通道，总不能在 QB 字段里传 session-* 键）。
	Prefs map[string]any `json:"prefs,omitempty"`
	// QB 与 Prefs 同义，保留只为兼容尚未迁移的调用方（驱动与既有测试）。
	// 新代码一律用 Prefs；序列化时不再单独输出 qb 键。
	QB map[string]any `json:"-"`
}

// SessionStatus 连接状态
type SessionStatus struct {
	Connected bool   `json:"connected"`
	Version   string `json:"version"`
	// Type 当前下载器类型（transmission / qbittorrent）
	Type  string        `json:"type"`
	Caps  *Capabilities `json:"caps,omitempty"`
	Error string        `json:"error,omitempty"`
	// Aggregate 是否开启了多服务器聚合视图（同时管理多台 tr / qb）
	Aggregate bool `json:"aggregate,omitempty"`
	// AggregateErrors 聚合成员最近一次拉取失败的原因（界面提示用）
	AggregateErrors []string `json:"aggregateErrors,omitempty"`
}

// Capabilities 下载器能力自述。界面按能力显示入口，
// 未支持的能力自动隐藏，而不是等用户点了才报「不支持」。
type Capabilities struct {
	// 带宽组（Transmission 4.x）
	BandwidthGroups bool `json:"bandwidthGroups"`
	// 黑名单（IP 过滤规则）
	Blocklist bool `json:"blocklist"`
	// 目录剩余空间查询
	FreeSpace bool `json:"freeSpace"`
	// 监听端口外网可达性检测
	PortTest bool `json:"portTest"`
	// 顺序下载
	SequentialDownload bool `json:"sequentialDownload"`
	// 队列排序
	QueueMove bool `json:"queueMove"`
	// 重命名种子内文件 / 目录
	RenameFile bool `json:"renameFile"`
	// 系统命令（关闭下载器）
	SystemCommand bool `json:"systemCommand"`
	// 备用限速定时调度
	AltSpeedSchedule bool `json:"altSpeedSchedule"`
	// Tracker 批量替换
	TrackerReplace bool `json:"trackerReplace"`
	// 块位图
	PieceBitmap bool `json:"pieceBitmap"`
	// 未完成目录
	IncompleteDir bool `json:"incompleteDir"`
	// 种子事件脚本钩子
	ScriptHooks bool `json:"scriptHooks"`
	// 全局分享率上限
	GlobalSeedRatio bool `json:"globalSeedRatio"`
	// 队列停滞判定（闲置多少分钟后暂停）
	QueueStalled bool `json:"queueStalled"`
	// 连接数上限（全局 / 单种）
	PeerLimit bool `json:"peerLimit"`
	// 单种限速细节（带宽优先级、是否遵循全局限速）
	PerTorrentLimits bool `json:"perTorrentLimits"`
	// 「遵循全局限速」标记（honorsSessionLimits）。
	// 组内总限速引擎靠关闭该标记 + 下发单种限速来接管种子，因此没有该语义的
	// 下载器（qBittorrent 的单种限速是绝对值）不能启用分组限速。
	HonorsSessionLimits bool `json:"honorsSessionLimits"`
	// 下载文件处理项（未完成文件重命名、回收源种子文件）
	FileHandling bool `json:"fileHandling"`
	// uTP 传输协议开关
	UtpToggle bool `json:"utpToggle"`
}

// 设置项字段类型（driver.Field* 常量与之对应）
const (
	SettingBool   = "bool"   // 开关
	SettingInt    = "int"    // 整数
	SettingFloat  = "float"  // 小数
	SettingString = "string" // 单行文本
	SettingSelect = "select" // 枚举下拉
	SettingText   = "text"   // 多行文本（换行分隔的列表）
	SettingTime   = "time"   // 时刻（HH:MM）
	// SettingPath 宿主文件系统路径（目录 / 文件）。与单行文本同形，但界面
	// 知道它是路径：整行宽呈现便于看全，并提供宿主目录选择器与语义路径提示。
	// 驱动只在该值确实是宿主路径时声明，纯文本键（如程序参数）不得使用。
	SettingPath = "path"
)

// SettingsOption 枚举项
type SettingsOption struct {
	Value   string `json:"value"`
	Label   string `json:"label"`
	LabelEn string `json:"labelEn"`
}

// SettingsField 一个设置项。Key 为下载器的原生键名（qBittorrent 即
// app/preferences 的 snake_case 键），前端原样读写，不做二次命名。
type SettingsField struct {
	Key     string `json:"key"`
	Type    string `json:"type"`
	Label   string `json:"label"`
	LabelEn string `json:"labelEn"`
	Hint    string `json:"hint,omitempty"`
	HintEn  string `json:"hintEn,omitempty"`
	// Unit 数值单位（MiB / KiB / 秒 / 分钟 / 个 …），前端展示用
	Unit string `json:"unit,omitempty"`
	Min  *int64 `json:"min,omitempty"`
	Max  *int64 `json:"max,omitempty"`
	// Scale 界面值与原生值的换算系数：界面值 = 原生值 ÷ Scale。
	// 仅用于上游以字节为单位、而界面向用户展示 KiB/MiB 的字段（如限速）
	Scale int64 `json:"scale,omitempty"`
	// Options 仅 select 使用
	Options []SettingsOption `json:"options,omitempty"`
	// ValueType 仅 select 使用：枚举值的传输类型，"int" 表示该枚举在
	// 上游 JSON 里是整数（qBittorrent 多数枚举如此），缺省为字符串
	ValueType string `json:"valueType,omitempty"`
	// ReadOnly 只读字段（上游只给出、不接受修改）
	ReadOnly bool `json:"readOnly,omitempty"`
	// WriteOnly 只写字段（密码等）：读取时不返回，前端显示为「留空即不改」
	WriteOnly bool `json:"writeOnly,omitempty"`
	// Secret 敏感值：前端按密码控件渲染
	Secret bool `json:"secret,omitempty"`
	// Danger 修改后可能断开当前面板连接（如 WebUI 端口 / 账号密码）
	Danger bool `json:"danger,omitempty"`
}

// SettingsSection 设置分节（对齐 qBittorrent 官方 Options 的分节）。
//
// Icon 是**语义图标名**（见下方 SettingIcon* 常量），由驱动声明、前端映射成图形：
// 前端不该知道「qBittorrent 的 behavior 节该配什么图标」，否则每接入一个下载器
// 都要回前端加一张对照表。留空时前端落回通用图标。
type SettingsSection struct {
	Key     string          `json:"key"`
	Label   string          `json:"label"`
	LabelEn string          `json:"labelEn"`
	Icon    string          `json:"icon,omitempty"`
	Fields  []SettingsField `json:"fields"`
}

// 分节语义图标名。这是一份**跨驱动的通用词汇表**，不是 qBittorrent 的节名：
// 新驱动按语义挑名字即可，前端只为这些名字准备图形，加下载器无需改前端。
const (
	SettingIconBehavior   = "behavior"   // 常规行为 / 启动
	SettingIconDownload   = "download"   // 下载行为与保存路径
	SettingIconConnect    = "connect"    // 连接 / 端口 / 代理
	SettingIconSpeed      = "speed"      // 速率限制
	SettingIconBitTorrent = "bittorrent" // BitTorrent 协议细节
	SettingIconRSS        = "rss"        // RSS / 订阅
	SettingIconWebUI      = "webui"      // 远程访问 / WebUI
	SettingIconAdvanced   = "advanced"   // 高级
	SettingIconNetwork    = "network"    // 网络（Transmission 的 network 节）
	SettingIconQueue      = "queue"      // 队列
	SettingIconPeers      = "peers"      // 连接数 / Peer
	SettingIconScripts    = "scripts"    // 脚本钩子
	SettingIconStorage    = "storage"    // 存储 / 缓存
)

// SessionStats 会话统计（累计 / 当前）
type SessionStats struct {
	ActiveTorrentCount int64               `json:"activeTorrentCount"`
	DownloadSpeed      int64               `json:"downloadSpeed"`
	PausedTorrentCount int64               `json:"pausedTorrentCount"`
	TorrentCount       int64               `json:"torrentCount"`
	UploadSpeed        int64               `json:"uploadSpeed"`
	Cumulative         SessionStatsDetails `json:"cumulative"`
	Current            SessionStatsDetails `json:"current"`
}

// SessionStatsDetails 会话统计明细
type SessionStatsDetails struct {
	DownloadedBytes int64 `json:"downloadedBytes"`
	FilesAdded      int64 `json:"filesAdded"`
	SecondsActive   int64 `json:"secondsActive"`
	SessionCount    int64 `json:"sessionCount"`
	UploadedBytes   int64 `json:"uploadedBytes"`
}
