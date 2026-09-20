// Package driver 定义下载器后端抽象。
//
// trpanel 原先只对接 Transmission，rpc.Client 的方法签名直接透出了 hekmon 库的类型
// （TorrentSetPayload / SessionArguments），上层 API、做种策略、限速引擎、MCP 工具
// 全都依赖它们，换一个下载器就要改遍全仓。
//
// 这里把「面板真正需要的下载器能力」抽成接口：每个下载器（Transmission、
// qBittorrent……）各自实现，业务层只面向接口。约定：
//   - 单位统一：单种限速、全局限速一律 KB/s（与 Transmission RPC 一致），
//     各驱动自行换算成自己的单位（qBittorrent 用 bytes/s）；
//   - 种子 ID 统一为 int64：Transmission 原生就是数字 ID，qBittorrent 用
//     infohash 作主键，由驱动内部做 hash↔ID 的稳定映射（见 qbittorrent 包）；
//   - 能力自述：接口覆盖全部功能，但并非每个下载器都能实现（如 qBittorrent
//     没有带宽组与黑名单），不支持的能力返回 ErrUnsupported 并在 Capabilities
//     中声明，界面据此隐藏入口而不是报错。
package driver

import (
	"context"
	"errors"

	"github.com/trpanel/backend/internal/models"
)

// Kind 下载器类型
type Kind string

const (
	KindTransmission Kind = "transmission"
	KindQBittorrent  Kind = "qbittorrent"
)

// NormalizeKind 规范化类型字符串，空值与未知值回落到 Transmission
// （既有部署的连接配置里没有类型字段，必须保持原有行为）
func NormalizeKind(s string) Kind {
	switch Kind(s) {
	case KindTransmission:
		return KindTransmission
	case KindQBittorrent:
		return KindQBittorrent
	default:
		return KindTransmission
	}
}

// String 类型字符串（便于直接写入 JSON / 状态文件）
func (k Kind) String() string { return string(k) }

// Label 类型显示名（日志与界面回显用）
func (k Kind) Label() string {
	switch k {
	case KindQBittorrent:
		return "qBittorrent"
	default:
		return "Transmission"
	}
}

// ErrUnsupported 该下载器不支持此能力。
// 调用方（API 层）应据此返回 400/501 并附说明，而不是当成上游故障。
var ErrUnsupported = errors.New("当前下载器不支持该能力")

// Capabilities 下载器能力自述（定义见 models，前端直接消费同一份 JSON 标签）
type Capabilities = models.Capabilities

// TorrentPatch 种子属性修改请求。字段为指针以区分「未提交」与「显式置零」，
// 与 Transmission RPC 的 torrent-set 语义一致；限速单位 KB/s。
type TorrentPatch struct {
	Labels              []string
	TrackerList         []string
	BandwidthPriority   *int64
	DownloadLimited     *bool
	DownloadLimit       *int64 // KB/s
	UploadLimited       *bool
	UploadLimit         *int64 // KB/s
	HonorsSessionLimits *bool
	PeerLimit           *int64
	SeedRatioLimit      *float64
	SeedRatioMode       *int64 // 0 跟随全局 / 1 单种覆盖 / 2 不限
	QueuePosition       *int64
	FilesWanted         []int64
	FilesUnwanted       []int64
	PriorityHigh        []int64
	PriorityLow         []int64
	PriorityNormal      []int64
	SeedIdleMode        *int64
	SeedIdleLimitMin    *int64 // 分钟
}

// TorrentFlagPatch 语义通用、但 Transmission RPC 库未封装的开关。
// Groups 在 Transmission 里是带宽组（可多值）；qBittorrent 没有对应概念，
// 映射为分类（单值，取第一个，空数组表示清空分类）。
type TorrentFlagPatch struct {
	SequentialDownload *bool
	Groups             []string
}

// Empty 是否没有任何改动（避免无意义的上游请求）
func (p TorrentFlagPatch) Empty() bool {
	return p.SequentialDownload == nil && p.Groups == nil
}

// SessionPatch 会话配置修改请求（字段语义与 models.Session 对齐）
type SessionPatch struct {
	DownloadDir                      *string
	SpeedLimitDown                   *int64 // KB/s
	SpeedLimitDownOn                 *bool
	SpeedLimitUp                     *int64 // KB/s
	SpeedLimitUpOn                   *bool
	AltSpeedDown                     *int64 // KB/s
	AltSpeedUp                       *int64 // KB/s
	AltSpeedEnabled                  *bool
	StartAdded                       *bool
	PeerLimitGlobal                  *int64
	PeerLimitPerTorrent              *int64
	PEXEnabled                       *bool
	DHTEnabled                       *bool
	LPDEnabled                       *bool
	UTPEnabled                       *bool
	SeedRatioLimit                   *float64
	SeedRatioLimited                 *bool
	Encryption                       *string // required / preferred / tolerated
	DownloadQueueEnabled             *bool
	DownloadQueueSize                *int64
	SeedQueueEnabled                 *bool
	SeedQueueSize                    *int64
	QueueStalledEnabled              *bool
	QueueStalledMinutes              *int64
	BlocklistEnabled                 *bool
	BlocklistURL                     *string
	PortForwardingEnabled            *bool
	IncompleteDir                    *string
	IncompleteDirEnabled             *bool
	CacheSizeMB                      *int64
	AltSpeedTimeEnabled              *bool
	AltSpeedTimeBegin                *int64 // 自 0 点起的分钟数
	AltSpeedTimeEnd                  *int64
	AltSpeedTimeDay                  *int64
	ScriptTorrentAddedEnabled        *bool
	ScriptTorrentAddedFilename       *string
	ScriptTorrentDoneEnabled         *bool
	ScriptTorrentDoneFilename        *string
	ScriptTorrentDoneSeedingEnabled  *bool
	ScriptTorrentDoneSeedingFilename *string
	DefaultTrackers                  []string
	RenamePartialFiles               *bool
	TrashOriginalTorrentFiles        *bool
	IdleSeedingLimitEnabled          *bool
	IdleSeedingLimit                 *int64
	PeerPort                         *int64
	PeerPortRandomOnStart            *bool
}

// Backend 下载器后端。所有方法以种子 ID（int64）寻址，
// 空 ids 切片在 Transmission 语义下表示「全部种子」。
type Backend interface {
	// Kind 下载器类型
	Kind() Kind
	// Capabilities 能力自述
	Capabilities() Capabilities
	// Ping 连通性检测，返回版本信息
	Ping(ctx context.Context) (string, error)

	// ---- 种子 ----

	GetTorrents(ctx context.Context) ([]*models.Torrent, error)
	// GetTorrentsFresh 忽略读缓存强制拉取（写操作后的刷新必须走这里）
	GetTorrentsFresh(ctx context.Context) ([]*models.Torrent, error)
	GetTorrentDetail(ctx context.Context, id int64) (*models.Torrent, error)
	// GetTorrentSites 每个种子关联的 Tracker 站点（站点维度过滤 / 做种策略）
	GetTorrentSites(ctx context.Context) (map[int64][]string, error)

	// AddTorrentByFile 以 .torrent 文件内容添加，返回种子 ID
	AddTorrentByFile(ctx context.Context, data []byte, downloadDir string, paused bool, labels []string, priority *int64, filesWanted, filesUnwanted []int64) (int64, error)
	// AddTorrentByURL 以 http(s) 链接或磁力链接添加，返回种子 ID
	AddTorrentByURL(ctx context.Context, link, downloadDir string, paused bool, labels []string, priority *int64) (int64, error)

	StartTorrents(ctx context.Context, ids []int64) error
	// StartTorrentsNow 强制开始（忽略队列限制）
	StartTorrentsNow(ctx context.Context, ids []int64) error
	StopTorrents(ctx context.Context, ids []int64) error
	VerifyTorrents(ctx context.Context, ids []int64) error
	ReannounceTorrents(ctx context.Context, ids []int64) error
	RemoveTorrents(ctx context.Context, ids []int64, deleteData bool) error

	SetTorrent(ctx context.Context, ids []int64, patch TorrentPatch) error
	SetTorrentFlags(ctx context.Context, ids []int64, flags TorrentFlagPatch) error
	// SetTorrentLocation 迁移存储位置（move=false 时仅改指向）
	SetTorrentLocation(ctx context.Context, id int64, location string, move bool) error
	// QueueMove 队列排序：top / up / down / bottom
	QueueMove(ctx context.Context, ids []int64, direction string) error
	// RenameFile 重命名种子内文件 / 目录（path 为种子内相对路径）
	RenameFile(ctx context.Context, id int64, path, name string) error
	// ReplaceTracker 批量替换 / 追加 Tracker，返回受影响数量与种子名
	ReplaceTracker(ctx context.Context, from, to string, appendMode bool) (int64, []string, error)

	// ---- 会话 ----

	GetSession(ctx context.Context) (*models.Session, error)
	SetSession(ctx context.Context, patch SessionPatch) error
	GetSessionStats(ctx context.Context) (*models.SessionStats, error)
	TestPort(ctx context.Context) (bool, error)
	UpdateBlocklist(ctx context.Context) (int64, error)
	GetFreeSpace(ctx context.Context, path string) (freeSpace, totalSize int64, err error)
	GetSessionGroups(ctx context.Context) ([]models.BandwidthGroup, error)
	SetSessionGroup(ctx context.Context, name string, fields map[string]any) error
	SystemCommand(ctx context.Context, action string) error
}
