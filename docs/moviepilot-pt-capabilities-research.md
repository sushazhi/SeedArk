# MoviePilot (jxxghp/MoviePilot) PT 能力盘点报告

> 调研对象：`jxxghp/MoviePilot` 主程序（`v3` 分支，`version.py` = **v3.2.0**）、官方 Wiki（`jxxghp/MoviePilot-Wiki`）、官方插件市场（`jxxghp/MoviePilot-Plugins`，含 `plugins.v2` / `plugins.v3` 两套索引）。
> 调研时间：2026-10-26。方法：拉取 `v3` 分支与插件仓完整源码 + Wiki 原始 Markdown，按源码/配置项/插件表单字段逐一取证；对无法从源码确证的内容明确标注“未验证”。
> 用途：与 SeedArk「保种台」现有功能清单做对比，找出差距并给出对「只通过 Transmission/qBittorrent API 通信的 Web 面板」可落地的实现建议。

---

## 0. 架构定性（决定后面所有对比的结论）

MoviePilot 的 PT 能力全部围绕**站点侧（Site）与账号侧（Account）**构建，而不是围绕**下载器侧的种子资产**：

```
站点资源包(索引+认证) → 聚合搜索/爬虫/RSS → 过滤规则 → 下载器添加 → 整理入库 → 站点数据快照/统计
        ↑                                                    ↓
    站点签到/保号                                    刷流/删种/限速插件（挂在下载器上，但以“MoviePilot 下载过的种子”为边界）
```

三个结构性事实，后面反复用到：

1. **必须做站点用户认证才有站点能力**。没有通过 `AUTH_SITE`（用户认证）时，私有站和公开站都无法维护，已维护的站点也无法使用（Wiki 原文警告框）。
2. **站点的“真实数据”来自站点侧快照**，不是下载器。`SiteUserData` 表存的是站点返回的 upload/download/ratio/bonus/等级/做种数/做种体积，默认 6 小时刷新，保留 180 天。
3. **下载器侧只做“它自己下载过的种子”**。刷流种子由刷流任务自己打标签托管；限速插件以“MoviePilot 整理入库成功”为前置门槛；H&R 屏蔽只影响“新选种”。手动加的种子、辅种来的种子、非影视资源，长期不在 MoviePilot 的 PT 视图里。

---

## 1. MoviePilot PT 功能清单（按领域）

### 1.1 站点管理 / 站点维护

| 能力 | 具体行为 |
|:--|:--|
| 站点对象字段 | `name/domain/url/pri(搜索优先级)/rss/cookie/ua/apikey/token/proxy/filter/render/public/note/timeout(默认15s)/limit_interval/limit_count/limit_seconds(流控)/is_active/downloader(绑定下载器)` —— 见 `app/schemas/site.py` |
| 内置站点索引 | 资源包 `resources.v3` + 站点索引包 `user.sites.v3.bin`（索引版本 `3.0.0`），不再回退 V2 资源。解析器族：`nexus_php / nexus_project / nexus_rabbit / nexus_hhanclub / nexus_audiences / unit3d / discuz / gazelle / mtorrent / ipt / hddolby / file_list / rousi / small_horse / sunnypt / tnode / torrent_leech / yema / zhixing / bitpt`；蜘蛛（HTML 爬）额外有 `haidan / hddolby / milkie / mtorrent / rousi / sunnypt / tnode / torrentleech / yema`（`app/modules/indexer/parser|spider/`）。Wiki 列出 170+ 私有站、6 个公开站（Nyaa/TorrentGalaxy/ACG.RIP/MiKan/动漫花园/AniBT） |
| 域名归一键 | `extract_domain()` 把站点 URL 归并成注册域名作为匹配键，含特殊白名单 `u2.dmhy.org / pt.ecust.pp.ua / pt.gtkpw.xyz / pt.gtk.pw`（`app/domain/site.py`） |
| Cookie 获取 | ① 手工从浏览器开发者工具粘贴；② 个别站点用 `Authorization` 请求头或 `apikey`（已知：馒头）；③ 用户名+密码+二步验证码自动模拟登录（`POST /api/v1/site/cookie/{site_id}`，字段 username/password/code）；④ CookieCloud 批量同步 |
| CookieCloud | 定时把浏览器 Cookie 同步进 MP，自动建站、生成站点 RSS、抓站点图标；间隔默认 1440 分钟；支持本地/远端服务、域名黑名单；有一次性服务「同步CookieCloud站点」 |
| 反爬/连通性 | `站点User-Agent`（与 Cookie 配套以跳过 Cloudflare 人机验证）、`浏览器仿真`（CloakBrowser 或 FlareSolverr）、`代理`（按站点开关，走 `PROXY_HOST`）、`OCR_HOST` 验证码识别（默认 `https://movie-pilot.org`，可自建 `jxxghp/moviepilot-ocr`） |
| 站点健康 | `SiteStatistic` 表：成功次数/失败次数/平均耗时秒/最后状态/最后时间；UI 用绿（正常）/黄（可连通但平均>5s）/红（不通或超时）标识。可单站连接测试 `GET /site/test/{id}` |
| 站点流控 | 三件套：`limit_seconds`（相邻请求最小间隔）、`limit_interval`+`limit_count`（窗口内最大请求数）。实现见 `app/chain/search/provider.py`：并发搜索时按站点**原子预约**下一次可请求时间，避免并发绕过；被站点拒绝时解析站点返回的「访问间隔不得小于…／访问次数不得超过…／上次访问时间：…」计算剩余等待，并在真实等待时间上加 1 秒余量 |
| 站点资源浏览 | `GET /site/resource/{site_id}?keyword&mtype&cat&page`、`GET /site/category/{site_id}`、站点图标缓存 |
| 优先级语义 | 站点展示顺序 = 优先级顺序（可拖拽）；V3 中参与资源选择排序，同时音乐分类参与媒体类型过滤 |
| 其它 | 每站点可挂过滤规则（`site.filter`）、可绑定指定下载器（`site.downloader`）、可标记公开站（`public`） |

关键源码：`app/schemas/site.py`、`app/domain/site.py`、`app/chain/site.py`(1412 行)、`app/application/site/{contract,auth,health,observation,query,mutation}.py`、`app/api/endpoints/site.py`(922 行)、`app/db/models/{site,sitestatistic,siteuserdata}.py`。

### 1.2 站点签到 / 自动签到

**主程序本身只提供“登录态判定”，不提供签到调度**：
- `SiteUtils.is_logged_in(html)`：页面无 `input[type=password]` 且含 `logout/usercp/mybonus/user-info-side/myitem` 等标记 → 已登录；
- `SiteUtils.is_checkin(html)`：匹配 13 种站点签到标记（`a#signed`、`attendance`、`签到 / 签 到`、`span#sign_in a`、`addbonus`、`input.dt_button[value=打卡]`、`do_signin`、`a#do-attendance`、`shark-icon-button[href=attendance.php]` 等）→ 未签到。

**官方插件 `AutoSignIn`（v3.0.5，作者 thsrite）**：
- 配置项：`启用插件`、`发送通知`、`优化消息通知样式`、`立即运行一次`、`清理本日缓存`、`执行周期`、`队列数量`、`重试关键词`、`自动优选`、`签到站点`（多选，含“全部”）、`登录站点`（多选）。**签到与模拟登录是两份独立站点列表**（可只登录保号不签到）。
- 执行周期三种写法：① 5 位 cron；② 间隔小时（如 `2.3/9-23` = 9~23 点间每 2.3 小时）；③ **留空 = 默认 9–23 点随机执行 2 次**（`TimerUtils.random_scheduler`，随机间隔区间 2–6 小时）。默认值本身就是“避开深夜 + 不固定时刻”的防封设计。
- 结果判定纪律（很值得抄）：三类结果 —— `签到成功` / `登录成功但签到未确认`（可用于登录保号，不计入签到成功） / `登录或访问失败`。明确声明“HTTP 200、签到按钮消失、跳回签到页、脚本或隐藏模板中的文案都不能单独证明成功”。
- 站点特例处理：馒头无独立签到接口 → 用前端 `updateLastBrowse` 生成签名更新访问时间保号（每轮只请求一次）；海胆之家 `signin.php` + 带 Cookie 读 `index.php`，只有出现“已经打卡”才确认；学校（btschool）只认“今天签到您获得X点魔力值”一次性到账提示；PTZONE/AGSVPT 走 `attendance.php` POST 并在返回页同时确认登录与签到完成；未确认登录的失败结果下次自动重试（不受重试关键词限制）。
- 历史：保留含当天的最近 14 天；详情页分 `签到状态` 与 `登录状态` 两张表。

其它签到类插件：`HDBlueSignin`（蓝影论坛，专用密钥签到，随机错峰，限流时等待不少于 `Retry-After`，网络故障最多重试 2 次/间隔 10、30 分钟，**当天自动任务最多 3 轮且预算跨重启保留**，限流冷却不可绕过）；`InvitesSignin`（药丸论坛）。

### 1.3 数据统计与站点数据

**主程序内置 `SiteUserData`（站点数据快照）**，字段：
`username / userid / user_level / join_at / bonus(魔力) / upload / download / ratio / seeding(做种数) / leeching(下载数) / seeding_size / leeching_size / seeding_info(做种人数+种子大小的 JSON 列表) / message_unread / message_unread_contents(未读站内信内容) / err_msg / updated_day / updated_time`。
- 刷新：`SITEDATA_REFRESH_INTERVAL` 默认 **6 小时**（调度任务「站点数据刷新」），也可单站手动刷新 `POST /site/userdata/{site_id}`；查询接口 `/site/userdata/latest`、`/site/userdata/{site_id}?workdate=`；保留 `DATA_CLEANUP_SITE_USERDATA_DAYS` 默认 **180 天**。
- 站内信：`SITE_MESSAGE` 默认 `true` —— 读取站点未读消息并推送通知，也能发站内信。
- 站点访问统计：`SiteStatistic`（见 1.1）。
- 事件 `site.refreshed` / `site.updated` / `site.deleted` 供插件挂钩。

**插件 `SiteStatistic`（站点数据统计，V2）**：站点统计数据图表（Wiki 明确写 `v2.0+已内置此功能`，即统计能力已在主程序内）。
**插件 `PTDataStatistics`（PT数据统计，v2.1.8，V2/V3 共用实现）** —— 这是 MoviePilot 生态里最完整的“站点数据大屏”：
- 数据源纪律：**只读 MoviePilot 的 `SiteUserData`，不抓站点、不读下载器**；通过 `SiteRefreshed` 事件触发；插件本地 SQLite 只存小时快照，不复制日级历史。
- 当日上传/下载 = 当日累计 − **严格前一自然日**累计；缺少前一天成功快照时显示“基线不足”；累计值下降按负增量记 0，不用更早日期补算。
- 视图：数据总览（累计+今日、可整框点击选择月份/日期的站点上传下载分布、响应式站点卡片）、数据统计（按日/周/月、点击站点切换曲线并展开明细）、**养老进度**（十二大站点按真实加入时间排布的里程碑；等级路线从 User 到最高级、下一等级条件、保号目标、完整等级要求表；未合格显示实际累计天数，合格只显示“达成”）、仪表板（仅显示今日有增量的站点）、运行日志。
- 等级规则：**内置 234 份站点等级规则**（由 `PTDepilerMp` 固定提交生成，含 `min_upload / min_download / min_ratio / min_join_days / min_torrent_uploads / min_average_seeding_time_days / min_seeding_size / min_seeding_points / monthly_seeding_upload / adoptions(保号) / is_dead` 等），支持上传 JSON 覆盖或补充（含站点别名、域名、等级别名、VIP、任选条件）。任选条件按“满足任一项”算；MoviePilot 没提供的指标（发布数、平均做种时间）明确显示“数据未提供”。
- 通知：标准五段式 cron，每个服务器自然日最多一次、不补发、无数据站点不通知。
- PTD 兼容：可选开启本插件自带的 CookieCloud 兼容接收端，接收 PTD 备份并补充**时魔**与**做种积分**（两者增速分开读，不混用）。
- 隐私：所有页面与通知不展示站点域名（域名只参与后端等级规则匹配）。

### 1.4 保种 / 做种管理

主程序在**账号维度**做站点数据，在**种子维度**只提供下载器原语，不做保种台账。下载器抽象暴露的原语（`app/modules/qbittorrent`、`app/modules/transmission`）：
- 查询：全量/已完成/下载中 `get_torrents|get_completed_torrents|get_downloading_torrents`，支持按标签筛选；
- 逐种子策略：`change_torrent(upload_limit, download_limit, ratio_limit, seeding_time_limit)`。qB 走 `torrents_set_share_limits(ratio_limit, seeding_time_limit, inactive_seeding_time_limit)` / `torrents_set_upload_limit(limit=KB/s*1024)`。注释明确：**只改做种时间时分享率发送 `-2`**（qB 特殊值语义），同时设置时 `seeding_time_limit` 缺省或 0 发送 `-1`；
- 任务操作：`add_torrent(…, is_skip_checking)`（qB 5.3 起同时发 `skip_checking` 与 `seedMode` 两个字段，避免逐个转种任务查版本）、`start/stop/delete_torrents(delete_file)`、`recheck_torrents`、`set_torrent_location`、`set_torrent_category`、`set_torrents_tag / remove_torrents_tag / delete_torrents_tag`、`set_files`（文件优先级）、`update_tracker / get_trackers`、`transfer_info`、`set_speed_limit / get_speed_limit`、`get_content_layout`；
- Transmission 侧：`torrent-get` 取字段 `id/name/status/labels/hashString/totalSize/percentDone/addedDate/trackerList/trackerStats/leftUntilDone/rateDownload/rateUpload/recheckProgress/peersGettingFromUs/peersSendingToUs/uploadRatio/uploadedEver/downloadedEver/downloadDir/error/errorString/doneDate/queuePosition/activityDate/downloadLimit/uploadLimit/seedRatioLimit/seedIdleLimit`；状态映射 `0 stopped / 1 check_pending / 2 checking / 3 download_pending / 4 downloading / 5 seed_pending / 6 seeding`（TR 无失败状态，用 `error/error_string` 判失败）；
- 事件：`download.added` / `download.deleted` / `downloadfile.deleted`。

**保种相关的官方插件**：

| 插件 | 版本 | 行为（具体） |
|:--|:--|:--|
| `SeedStats` 站点保种统计 | 1.6.0（V3） | 按 tracker 把下载器全部任务归属到站点，统计每站 `任务数/做种/暂停/其他`、`辅种(做种中)`、`辅种暂停`、`辅种合计`、**做种体积**；合并站点账户信息（上传/下载/分享率/等级/魔力/数据时间）；**时魔估算**（自己按刷新周期记魔力快照，用 0.5~12 小时窗口估算，不足时回退日级快照并标“粗估”；魔力下降标“异常”；无魔力站点标“无魔力”）；**效率\* = 每小时魔力 ÷ 做种体积(TB)**；14 天小时快照 + 7 天折线；告警：站点数据过期、零做种、tracker 未归属、已删除站点残留；**声明只读，不修改/暂停/删除任何任务**；配置：刷新间隔(默认 60 分钟，最小 5)/统计的下载器(多选)/站点数据过期阈值(默认 24h)/合计是否含已删除站点/tracker 站点映射（`域名=站点名`）；远程命令 `/保种统计` |
| `ContractCheck` 契约检查 | 2.0.0（V2） | **手工录入站点契约**，格式 `站点名\|是否官种\|体积(GB)\|数量\|周期(天)\|开始时间(YYYY/MM/DD)`，例 `憨憨\|是\|2048\|200\|365\|2024/2/6`；用 CloakBrowser + 站点专用解析器抓站点页面上的**做种体积/做种数量**，给出「是否达成 / 还差多少体积 / 还差多少数量 / 还剩多少天」；仪表板 + 简洁/详细报表；远程命令 `/contract_check` |
| `TorrentTransfer` 自动转移做种 | 1.10.3（V2） | 定期把下载器中的做种任务**转移到另一个下载器** |
| `CleanInvalidSeed` 清理QB无效做种 | 2.0（V2） | 判定「无效做种」= 该种子所有 tracker 都处于错误态且 msg 命中已知错误集合，**并且同一域名下存在其他正常工作的种子**（防止站点临时关站误删）；两种模式：仅打标签（默认标签「无效做种」）/ 删种但**不删文件**（防止其他站辅种）；支持按分类、标签排除，支持自定义错误文案；仅支持 QB |
| `TorrentRemover` 自动删种 | 2.3（V2） | 删除条件（全部为“与”门槛）：下载器(多选)、种子大小(GB 区间 `1-10`)、分享率、做种时间(小时)、平均上传速度、标签、路径关键字、tracker 关键字、种子状态(含 TR 状态映射)、分类；命中后 `delete_torrents(delete_file=True, …)` 并通知 |
| `AutoClean` 定时清理媒体库 | 3.1.0 | 定时清理用户下载的种子、源文件、媒体库文件 |
| `StorageCleanup` 存储清理 | 1.0.15（V2，第三方维护） | 自动发现 MP/qB/媒体目录/硬链接关系，**联动核验做种与 H&R 后**才释放空间；需先部署 NAS 清理台服务 |

数值口径提示：qB 的 `completion_on` 为 0 时用 `added_on` 兜底算做种时长；TR 用 `done_date/date_done` 或退到 `added_date`；平均上传速度 = `uploaded / seeding_time`（TR 侧 `upload_ratio * total_size / seeding_time`）。见 `plugins.v2/torrentremover/__init__.py` `__get_qb_torrent/__get_tr_torrent`。

### 1.5 H&R 处理

- **主程序**：`TorrentInfo.hit_and_run`（“种子是否存在下载后做种义务”）由索引/爬虫解析得出；同族字段还有 `downloadvolumefactor`（0=免费、0.5=半价）、`uploadvolumefactor`（2=双倍上传）、`freedate`（免费截止）、`volume_factor`（促销文本）、`freedate_diff`（剩余免费时间）。即 **H&R 与促销信息是“选种时”就带上的元数据**，而不是下载后再去站点查。
- **`HRBlocker`（H&R Blocker，2.0.0，V3）**：在**所有选种路径**（搜索、订阅等）剔除带 H&R 标记的种子，并支持“逐种子 H&R 标记屏蔽”开关 —— 目标是**不产生新的 H&R 义务**，而不是管理已有义务。
- **`BrushFlow`**：任务级 `hr=yes/no`（选种时排除 H&R）+ `hr_seed_time`（H&R 种子达到该做种小时数才允许删除）+ `site_hr_active`（把该站所有种子当 H&R 处理，用于 RSS/手动加种拿不到标记的情况）。
- **`StorageCleanup`** 在释放空间前核验 H&R。
- **不确定项**：官方插件仓只有 `HRBlocker` 这一个纯 H&R 插件；「H&R 助手 / 做种守卫」一类第三方插件不在官方仓内（见第 5 节）。

### 1.6 分享率与流量管理

| 层次 | 机制 |
|:--|:--|
| 账号侧 | `SiteUserData.ratio/upload/download` 快照 + `PTDataStatistics` 的每日增量与趋势 |
| 站点账号分享率闸门（刷流） | BrushFlow 任务级 `site_ratio_control` + `site_ratio_target`：读 MoviePilot 最新站点统计，**达到目标就停加新种，低于目标自动恢复**；启用控制但无可用统计时**等待数据更新**而不是盲目加种 |
| 逐种子上传限速 | `QbUploadLimiter`（3.1.0，作者 xlmc，支持 qB 与 TR） |
| 全局/线路限速 | MP 下载器配置里的上传/下载限速；`SpeedLimiter`（播放限速）在外网播放媒体库时自动给下载器限速 |
| 刷流带宽/并发预算 | BrushFlow 任务级 `maxupspeed/maxdlspeed/maxdlcount` + 全局 `global_maxupspeed/global_maxdlspeed/global_maxdlcount/global_disksize`（留空或 0 = 不限制） |

**`QbUploadLimiter` 的具体设计（本报告里最值得抄的一个）**：
- **入库门槛**：只处理 MoviePilot **整理入库成功**的种子；种子入库成功前对插件完全不可见（等同插件未开启）。
- 阈值：种子分享率（上传量/下载量）≥ 阈值（正数、最多 1 位小数）→ 把该种子上传限速到指定值（KB/s，默认 2000，`0` 表示不限速）；**支持按站点单独阈值**，一行一条 `站点名=阈值`（不区分大小写，重复以最后一条为准，识别不到就回退全局阈值）。
- 与 qB 全局上传限速**取较小值**；定时检测（默认 30 秒，最短 10 秒）持续施加，防止被其他插件或手动覆盖。
- **限速有效性判定**（无需配置）：为每颗种子维护「未被本插件限速时的上传峰值」（24 小时半衰期；限速期间不采样，避免被自己压塌）；若峰值本身够不到目标限速值，就不限速（限速是空操作）；**刻意不看瞬时速度**（怕“暂时没下载者”被误判放行后长期满速）。
- **限速所有权 + 兜底恢复**：只登记“本插件实际设置过限速”的种子；停用/卸载时自动恢复不限速，下载器离线则定时重试；状态与已通知记录落盘，重启不重复通知。
- **AI 智能限速（可选）**：调用系统已配置的大模型，按「种子分享率 + 上传活跃度 + 站点账号分享率」逐种子决策；只看活跃种子；已限速种子每轮复核（加限/减限/解限，带 20% 防抖）；决策值被自动收敛到该种子实测峰值的 **20%~80%** 区间，**并叠加 100 KB/s 合规下限**；可选「AI 账号分享率门槛」，只有站点账号分享率达标才对站内种子启用 AI；大模型未配置/调用失败/输出解析失败一律**回退常规阈值规则**。

### 1.7 刷流（BrushFlow，官方，6.1.4 / V5 架构）

每项任务**独立绑定一个站点 + 一个下载器**，有自己的调度、选种规则、删种规则、托管记录、统计与运行诊断。任务配置字段全集（`plugins.v2/brushflow/models.py` `BrushTaskPayload`）：

`enabled, notify, site_id, downloader, brush_interval(默认10分钟), check_interval(默认5分钟), cron, active_time_range, site_ratio_control, site_ratio_target, disksize, maxupspeed, maxdlspeed, maxdlcount, freeleech(""/free/2xfree), hr(yes/no), include, exclude, size, seeder, timezone_offset, pubtime, seed_time, hr_seed_time, seed_ratio, seed_size, download_time, seed_avgspeed, seed_inactivetime, delete_size_range, up_speed, dl_speed, auto_archive_days, save_path, delete_except_tags, except_subscribe, proxy_delete, del_no_free, qb_category, site_hr_active, site_skip_tips, rss_support`

**选种**（`__evaluate_conditions`）按顺序否决：重复种子（同站标题/详情页已托管）→ **其他站点存在尚未下载完成的同名种子**（跨站防重复）→ 非免费/非 2X 免费（`task.freeleech`）→ **存在 H&R**（`task.hr == "yes"`）→ 不含 include 正则 → 命中 exclude 正则 → 大小超出/低于 `size` 区间 → 做种人数超出 `seeder` 区间 → 发布时间超出 `pubtime` 区间（含 `timezone_offset` 修正）。

**删种**（`__evaluate_conditions_for_delete`，**H&R 优先后短路**）：若种子是 H&R 且配置了 `hr_seed_time` 或 `seed_ratio`，则**只有**做种时间达到 `hr_seed_time` 小时 **或** 分享率达到 `seed_ratio` 才删，否则直接返回“H&R 种子尚未满足删除条件”；非 H&R 则依次判断：促销到期 → 做种时间 ≥ `seed_time` 小时 → 分享率 ≥ `seed_ratio` → 上传量 ≥ `seed_size` GB → 未完成且下载耗时 ≥ `download_time` 小时 → 平均上传速度 ≤ `seed_avgspeed` KB/s 且已做种 ≥30 分钟 → 未活动 ≥ `seed_inactivetime` 分钟。命中后删种子+文件，并写归档（`auto_archive_days`）。

**全局动态删种**：汇总所有启用任务的做种体积，单值阈值（如 `100`）表示达到 100 GB 后删到 100 GB；区间阈值（如 `50-100`）表示达到 100 GB 后删到 50 GB；启用动态删种的任务里已完成非 H&R 种子参与全局兜底淘汰；未启用任务只在全局阈值触发后按自身条件处理。

**托管与标签**：任务给种子打唯一标签（V5 新增，避免多任务互相接管）+ 兼容旧全局「刷流」标签；维护 managed / unmanaged / 归档 三态；任务删除后清理 qB 中不再被任何种子引用的 `刷流-*` 标签；同一下载器共享种子做跨任务保护。

**运行时**：`brush_interval` 与 `check_interval` 各注册为独立调度服务；促销有明确到期时间时额外注册一次性 `date` 触发器在到期瞬间检查未完成下载（保留周期检查兜底）。种子进度与是否下载完成按下载器**实际文件完成度**判断（复用文件/跨种时下载量为 0 也能正确显示为做种）。另有 `累计下载流量` 独立统计、运行诊断、Vue 工作台（侧栏入口 `get_sidebar_nav`）+ 仪表板卡片（刷新 30 秒）+ 插件 API（`/status /settings /tasks /tasks/{id} /state /run /check /clear`）。

`BrushDigest`（V2）把刷流的逐条通知汇总成一条（可配汇总周期、立即推送一次、自动检测数据来源插件）。

### 1.8 发布 / 转种 / 辅种

| 子能力 | MoviePilot 现状 |
|:--|:--|
| **辅种（自动）** | `IYUUAutoSeed`（2.19，官方）：基于 IYUU 官方 API 自动辅种。`CrossSeed`（3.0.3，青蛙辅种助手）：用站点 **pieces-hash API**（`{site}nexusapi/pieces-hash`，憨憨用 nexus 前缀，其余用 `api/pieces-hash`）把本地种子的 info-hash + pieces-hash 与站点上的种子比对，命中后下载 .torrent、以“不删文件”的方式加入并强制校验；有校验后复查（`check_recheck`）机制、已辅种 info-hash 缓存、站点删除事件处理；支持站点：青蛙、AGSVPT、麒麟、UBits、聆音、憨憨等 |
| **转种（下载器间转移）** | `TorrentTransfer`（1.10.3）：定期把做种任务从一个下载器搬到另一个下载器 |
| **同 infohash 补 tracker** | 由辅种链与 `update_tracker` 原语覆盖；没有独立的“给已有种子补 tracker”开关式功能 |
| **发种 / 发布到站点** | **未发现**。全仓检索关键词「发种 / 发布种子 / 制作种子 / create_torrent / upload_torrent」在 `app/`、`docs/`、Wiki、官方插件仓中均无 PT 发布能力；插件仓里命中 `发种` 的只有 `PTDataStatistics` 的**等级规则文案**（某站等级权限里写“可以直接发布种子”）与站点等级说明。**没有** MediaInfo/截图/BBCode/发布包生成 |
| 跨站同内容**视图** | **未发现**。辅种是自动化动作，不是资产视图；聚合搜索会把同一资源在各站的候选并列展示，并由站点优先级/下载规则挑一个下载，但没有“同内容矩阵”页面 |
| 客户端原语 | qB `add_torrent(skip_checking/seedMode)`、`recheck_torrents`、`set_torrent_location`、`addTrackers` 类操作即转种/复用文件的底层能力 |

### 1.9 订阅与搜索（索引搜索 / RSS）

- **聚合搜索**：站点范围可选（越多越慢）；站点优先级参与排序；**过滤规则**全局生效，规则集内置：`BLU / 4K / 1080P / 720P / CNSUB / GZ(官种,匹配 labels) / SPECSUB / BLURAY / UHD / H265 / H264 / DOLBY / ATMOS / HDR / SDR / REMUX / WEBDL / FREE(downloadvolumefactor=0) / CNVOI / HKVOI / 60FPS / 3D / HIRES / LOSSLESS / FLAC / ALAC / APE / WAV / DSD / MP3 / AAC / OPUS / BITRATE320|256|192`（`app/application/rules.py` `BUILTIN_RULE_SET`）；规则表达式支持 `A > B` 按序择优、`size_range`、`seeders` 下限、`labels`、`tmdb` 条件；支持自定义规则与规则组。
- **搜索配置**：`SEARCH_MULTIPLE_NAME`（默认 false）、`MAX_SEARCH_NAME_LIMIT`（默认 3）、`SEARCH_RESOURCE_PAGES`（默认 1）；V3 有流式搜索结果页（进度 + 实时预览），代理不支持流式时自动回退。
- **模糊搜索**（“站点资源”）不过滤、不排序，直接展示站点返回。
- **订阅刷新两种模式**（Wiki 原文，含安全含义）：
  - `自动`（spider）：爬种子列表页，**随机 20–40 分钟间隔，全天 32 次列表页请求，每天 07:00 开始，32 次完成后停止**；**支持促销标识与做种数**，可用于优先级/过滤规则；
  - `站点RSS`：用站点维护里的 RSS 链接，间隔必须大于 5 分钟；**不支持促销检测与做种数判定**。
- **订阅搜索**：`SUBSCRIBE_SEARCH`（默认 false）+ `SUBSCRIBE_SEARCH_INTERVAL` 默认 24 小时；Wiki 明确写**该时间间隔无法调整**，并建议“如没有出现过漏订阅的问题**不要开启**”（原因就是避免对站点造成压力）。单条订阅可设 1–8760 小时的「搜索周期」（需开启订阅搜索），系统每 5 分钟检查到期订阅、按站点限流安排搜索，站点忙时延后；最近一次搜索时间持久化，重启不重新计时。
- 其它：洗版（`洗版优先级`，仅完结剧集，需配合整理覆盖模式）、订阅日历缓存、订阅历史、订阅分享（`SUBSCRIBE_STATISTIC_SHARE` 默认 true）、`RssSubscribe` 自定义订阅（定时刷新 RSS 报文 → 识别 → 加订阅或直接下载）、`TvFirstWatch`（只下前 N 集）。
- **认证前置**：`AUTH_SITE` / 用户认证是站点功能的前提（站点页首条警告）。

### 1.10 下载器集成

- 类型：`qbittorrent` / `transmission` / `rtorrent`（`DownloaderConf.type`）；多实例；`default` 标记；`path_mapping`（主机↔容器路径映射）；`enabled`。配置属“结构化系统配置”，存 DB 而非环境变量。
- qB 专属能力：API Key 或 Cookie 登录；**把站点 Cookie 同步进 qB**（用站点 Cookie 下载需要鉴权的 .torrent）；未完成文件后缀同步（`.!qb`）；分类与标签管理；强制开始（`is_force_resume`/`set_force_start`）；按标签批量查任务与最后添加的种子；文件优先级；内容布局（`get_content_layout`）；新版本字段兼容（qB 5.2.3 / 5.3 `seedMode`）。
- TR 专属：`rename_partial_files` 同步（是否给未完成文件加 `.part`）；状态映射与 `error/error_string` 失败判定；路径来自 `downloadDir`。
- 全局：种子标签 `TORRENT_TAG` 默认 `MOVIEPILOT`（MP 自己加的种子带该标签）；`DOWNLOAD_TMPEXT` 默认 `[".!qb", ".part"]`；`DOWNLOAD_SUBTITLE` 默认 true（下载站点字幕）；调度任务「下载文件整理」周期 5 分钟（可用 qB「下载完成时运行外部程序」调 `/api/v1/transfer/now` 立即整理）。
- 与保种的耦合（`reorganize.md`）：整理优先**硬链接/复制**，明确警告**`移动`会影响原文件做种**；音乐标签写入会对硬链接目标做副本保护，以免修改到做种文件。

### 1.11 自动化 / 工作流

**调度器任务目录**（`app/scheduler/catalog.py`，直接给出“有哪些自动化”）：数据库备份、恢复订阅搜索队列、重试未完成的后台处理(Outbox)、同步CookieCloud站点、媒体服务器同步、新增订阅搜索、订阅元数据更新、订阅搜索补全、订阅刷新 / RSS订阅刷新、关注的订阅分享、下载文件整理、壁纸缓存、公共定时服务、数据表清理、用户认证检查、**站点数据刷新**、推荐缓存、插件市场缓存、检查系统更新、订阅日历缓存、主动内存回收、智能体定时任务、安装版本统计上报。

**工作流引擎（V3 一等公民）**：
- 画布式 DAG：动作带坐标 `position`、上游/下游 `flows`（边可带 `condition` 表达式）、节点属性 `inputs/outputs/join_policy/fail_policy/branch_policy/concurrency_key/timeout/retry{max_attempts,interval,backoff}`；
- 触发：`timer`（cron）/ `event`（可带 `event_conditions` JSON 过滤）/ `manual`；状态机 `W等待/R运行中/P暂停/S成功/F失败`；`run_count`、`current_action`、`result`；
- 可恢复执行：`execution_state{version, nodes{state,attempt,started_at,finished_at,message}, outputs, errors, runtime{progress,finished_actions,running_tasks,errors,node_states,attempts}}`；
- 动作集（14 个，`app/workflow/actions/`）：`获取RSS资源 / 获取媒体数据 / 获取下载任务 / 搜索站点资源 / 过滤媒体数据 / 过滤资源 / 添加订阅 / 添加下载 / 整理文件 / 扫描目录 / 刮削文件 / 发送消息 / 发送事件 / 调用插件 / 备注`；
- 上下文（`ActionContext`）：`content / torrents / medias / fileitems / downloads / sites / subscribes / node_outputs / artifacts`；
- 分享：`WorkflowShare`（工作流模板分享），`WORKFLOW_STATISTIC_SHARE` 默认 true（匿名统计）。

**插件自注册服务**：插件通过 `get_service()` 返回 `{id,name,trigger(cron|interval|date|CronTrigger),func,kwargs}`，即“每个插件自己有定时器”——刷流、签到、保种统计都挂在这上面。

**事件总线**：`site.updated/deleted/refreshed`、`download.added/deleted`、`downloadfile.deleted`、`transfer.complete/failed`、`subtitle.*`、`audio.*`、`subscribe.added/modified/deleted/complete`、`history.deleted`、`notice.message`、`user.message`、`webhook.message`、`system.error`、`metadata.scrape`、`config.updated`、`module.reload`、`plugin.reload/action/triggered`、`command.excute`。

**V3 智能助手 / MCP**：内置工具覆盖媒体、订阅、下载、整理、站点、目录、工作流、插件；可暴露 MCP 供第三方 Agent 调用（**故意隐藏** `execute_command / read_file / write_file / edit_file / search_web` 等本地高风险工具）；有权限/guardrails 概念，高危操作通常需管理员。

**远程命令**：系统与插件均可注册 `/命令`（如 `/保种统计`、`/contract_check`），可通过微信/Telegram/Slack/SynologyChat/VoceChat/QQ 交互。

### 1.12 消息通知

- 渠道（主程序 `NotificationConf.type`）：`telegram / wechat(企业微信) / feishu / vocechat / synologychat / slack / webpush / qqbot / apppush`；插件补充 `bark / email(SMTP) / dingding / wecom / pushdeer / pushplus / ntfy / mqtt / wxpusher / WPUSH / NapCat(QQ OneBot11) / messageforward / 企业微信 IP 更新` 等。
- 路由：按消息类型设「发送范围」（`all/user/admin`）+ 用户个人信息里的渠道 ID；交互类消息只有操作人能收到；超级管理员有兜底 ID；渠道默认 ID 只属管理员范围。
- 模板：`{{ instance_name }}` 显示实例主机名，便于多实例共用一个通知群。
- 富文本：Telegram Rich Message / 飞书 Markdown 卡片 / Slack Block Kit / 钉钉 / Discord / VoceChat / Synology Chat / 企业微信机器人 / 有 Markdown 权限的 QQ Bot；不支持的渠道自动回退纯文本（AutoSignIn 的「优化消息通知样式」开关即此）。
- 站内信：`SITE_MESSAGE` 读取站点未读消息并推送；`SiteUserData.message_unread_contents` 存内容。

### 1.13 插件体系

- **市场**：`PLUGIN_MARKET` 配置插件仓库列表（默认官方仓 + Wiki `plugin.md` 列出的第三方仓，可在设置里“同步 Wiki”自动合并去重）；`GITHUB_TOKEN` 提升 GitHub API 配额避免限流；`GITHUB_PROXY` / `PIP_PROXY` 解决下载与依赖。
- **插件契约**（`app/sdk/plugin/base.py` `_PluginBase`）：元信息 `plugin_name/desc/icon/version/author/author_url/config_prefix/order/auth_level`；生命周期 `init_plugin/get_state/stop_service/get_config/update_config/get_data_path`；
  **能力声明点**：`get_form`(Vuetify 表单) + `get_render_mode`(支持 `vue` 模块联邦) / `get_page`(详情页) / `get_dashboard` + `get_dashboard_meta`(仪表板卡片) / `get_api`(path/endpoint/methods/auth=apikey|bear/summary) / `get_service`(定时器) / `get_command`(远程命令) / `get_actions`(工作流动作) / `get_module`(覆写系统模块实现) / `get_auth_providers`(登录入口) / `get_media_source`(媒体数据源) / `get_agent_pets` / `get_sidebar_nav`(侧栏入口)；
  数据面：`plugindata`(插件 KV)、插件独立 DB handle、`systemconfig`、`eventmanager`、`systemmessage`。
- **插件分身**：`is_clone` + 克隆/实例/文件夹/日志级别/目标 等一整套端点，可对同一插件起多份独立配置实例。
- **V2 兼容**：官方仓同时维护 `plugins.v2`（73 个）与 `plugins.v3`（45 个）索引，V3 宿主通过向后兼容机制加载 V2 插件（`PTDataStatistics` 即 V2 实现、V2/V3 共用）。
- 规模感：光官方仓就有 ~118 个插件；Wiki 列出的第三方插件仓库 60+ 个。

### 1.14 运维/数据面（与“安全默认”相关）

数据库（默认 SQLite / 可选 PostgreSQL、WAL、连接池）、自动备份（`DB_BACKUP_ENABLE/CRON/ON_UPGRADE/PATH/RETENTION_DAYS/MAX_COUNT`）、数据清理（消息 90 天、下载历史 180 天、**站点用户数据 180 天**、整理历史 3 年、下载失败冷却 7 天、订阅历史 3 年、Agent 会话/任务 180 天、Outbox 30/90 天）、Doctor 诊断（不自动删库、不自动回滚迁移，只给建议）、`MOVIEPILOT_SAFE_MODE`（只保留核心 API，跳过插件/调度器/监控/命令/工作流）。

---

## 2. MoviePilot 做得好/做得不一样的地方，以及「保种台」的缺口

先把两边的范式说清楚：**MoviePilot 是“站点+账号+流水线”视角；SeedArk 保种台是“下载器+种子资产”视角。** 因此下面的“更好”分两类：(A) 它有你完全没有的能力；(B) 同一件事它的做法更深，值得抄。

### 2.1 A 类：MoviePilot 有、保种台完全没有

| # | 能力 | 为什么值钱 |
|:--|:--|:--|
| A1 | **站点账号真实数据与历史**（upload/download/ratio/bonus/等级/加入时间/做种数/做种体积，6 小时刷新、180 天保留、每日增量、按日/周/月图表） | 保种台的“近 30 天上传量/分享率”来自下载器，**与站点记账不同**（站点计促销倍率、可能含其他客户端/设备流量）。用户真正被考核的是站点侧数字 |
| A2 | **等级/保号进度（养老进度）+ 234 站等级规则** | 明确回答“我离升级还差多少、这站会不会被删号”。保种台完全没有“账号资格”维度 |
| A3 | **魔力 / 时魔 / 效率\***（每小时魔力 ÷ 做种体积 TB） | PT 用户优化的真实目标是「单位盘位换多少魔力」，而不是「单位盘位换多少上传」。这是保种分/上传效率之上的一层 |
| A4 | **站点契约检查**（手工录 体积/数量/周期/开始时间 → 对比站点当前做种体积/数量 → 还差多少、还剩几天） | 这是 MoviePilot 里最接近「释放日历」的东西，但它是**账号级契约**；保种台的日历是**种子级**，两者互补而非重复 |
| A5 | **促销与 H&R 元数据驱动的选种**（`downloadvolumefactor/uploadvolumefactor/freedate/hit_and_run` + HRBlocker + 过滤规则里的 `FREE`） | 保种台只有「H&R 风险」展示，没有「促销到期」这个**可以合法释放空间的时点**概念，也没有“加种时就不碰 H&R”的闸门 |
| A6 | **刷流**（多站点独立任务 + 选种/删种全参数 + 全局体积/并发/带宽预算 + 动态删种 + 归档 + 托管标签 + 诊断） | 一个完整的产品级子系统，约等于 保种台 3~4 个模块的复杂度 |
| A7 | **自动辅种 / 转种**（IYUU、pieces-hash 辅种、下载器间转移做种） | 保种台只有“同内容矩阵”这个**视图**；MoviePilot 是**动作**（而且能自动判断“这份数据在别的站可以用来辅种”） |
| A8 | **无效做种清理（站点已删种）** | 保种台的档案库处理“已删除的种子”，但不处理“站点已删、本地还占着盘”的僵尸做种 |
| A9 | **逐种子上传限速 + 恶意限速红线保护** | 见 1.6；这既是功能也是**封号风险控制**（M-Team 规则） |
| A10 | **站点连通性/流控/反爬基础设施**（健康三色、成功率、平均耗时、请求窗口限流 + 原子预约、UA 绑定、浏览器仿真、OCR、CookieCloud、按站代理） | 任何“面板去访问站点”的功能都必须先有这一层，否则就是把用户账号往封禁上推 |
| A11 | **站内信读取** | 站点通知（警告、H&R 通知、活动）常只发站内信 |
| A12 | **工作流/DAG + 插件服务 + 事件总线 + 20+ 通知渠道 + 远程命令** | 平台化能力；保种台是固定功能集 |
| A13 | **下载器间/多下载器的种子搬运**（TorrentTransfer） | 保种台的“多下载器聚合”目前是**看**，MoviePilot 能**搬** |
| A14 | **路径映射与文件级操作**（`path_mapping`、文件优先级、set location、内容布局、recheck） | 做复活/辅种/搬家都需要这些原语治理 |

### 2.2 B 类：同一件事，MoviePilot 做得更深的地方（保种台可直接抄设计）

| # | 现在的 保种台 | MoviePilot 的做法 | 差距 |
|:--|:--|:--|:--|
| B1 | 站点规则 = 最小做种天数 / 目标分享率 / H&R 小时 | BrushFlow 任务级 30+ 字段；删种条件**有序评估 + H&R 短路**；还有“促销到期”“未活动时间”“平均上传速度下限”“下载耗时上限”“大小区间” | 规则维度少；缺“按条件组合成策略”和“命中原因可解释” |
| B2 | 释放日历 = 义务到期日 | BrushFlow 的 H&R 短路 + 促销到期即时检查 + 全局动态删种（单值/区间阈值） | 缺“**释放预算**”：今天能腾多少盘、删哪些最划算；缺促销到期这一释放时点 |
| B3 | 保种分 / 上传效率（客户端口径） | SeedStats 的「效率 = 魔力/TB」+「时魔估算 + 粗估/异常/无魔力」标记 + 「数据过期 / 零做种 / tracker 未归属」告警 | 缺“置信度/口径声明”（估算 vs 实测）与“异常检测（魔力下降）” |
| B4 | H&R 风险（展示） | HRBlocker 在选种阶段剔除；BrushFlow `site_hr_active` 兜底；AutoSignIn“不确定就不算成功”的保守判定 | 缺“**失败关闭（fail-closed）**”原则：不确定时保持做种而不是允许删 |
| B5 | 无 | CleanInvalidSeed 的「同域名还有别的种子在工作」交叉验证 + 「仅打标签」两段式 | 缺僵尸做种的识别，且缺少防误删的两段式处置 |
| B6 | 无 | QbUploadLimiter 的「限速有效性判定」（用 24h 半衰期峰值而非瞬速）+ 限速所有权与卸载兜底恢复 | 任何限速功能都要有“不误伤 + 可完全回滚” |
| B7 | 无 | PTDataStatistics 的数据纪律：当日增量只与**严格前一自然日**比、基线不足要明说、累计下降记 0 不补算 | 趋势/增量指标都需要这套口径声明 |
| B8 | 站点规则是纯手工配置 | ContractCheck 也是**手工录契约**（`站点\|官种\|体积\|数量\|周期\|开始时间`）+ 抓站点现状做差值 | 说明“手工规则 + 本地实况”这条路是有用户验证的；保种台可以更进一步：**本地实况已全在手里，根本不需要抓站点** |
| B9 | 无 | AutoSignIn/HDBlueSignin 的调度安全默认：默认 9–23 点随机 2 次、2–6 小时随机间隔、429 尊重 `Retry-After`、当天预算跨重启保留、冷却不可绕过 | 保种台的定时/自动化功能应内建同类“抖动 + 预算 + 冷却” |
| B10 | 档案库（删除的种子元数据+磁链+复活） | BrushFlow 有 `auto_archive_days` 归档（**只归档它自己托管的刷流种子**）；MP 无通用“删除种子仓库” | 这是 保种台的真优势；可借鉴的点是“归档顺带记录**删除原因**” |
| B11 | 发种资料包（BBCode+zip） | **MoviePilot 完全没有**（已在 1.8 取证） | 保种台的真优势 |
| B12 | 跨站同内容矩阵（视图） | MP 是 pieces-hash 辅种（动作），需要站点 API | 保种台的真优势是“看得见”；可增强为“一键把某站的 .torrent 落到已有数据上并校验” |
| B13 | 多下载器聚合视图 | MP 支持多下载器配置，但 PT 视图以“它自己下载的种子”为边界，没有跨客户端的做种资产视图 | 保种台的真优势 |

### 2.3 保种台功能清单里明确缺的东西（逐项）

1. **账号侧数据层**：站点上传/下载/分享率/魔力/等级/加入时间/做种数/做种体积的**快照与历史**（现在只有下载器口径的近 30 天与总分享率）。
2. **等级与保号**（升级差多少、会不会被降级/删号、契约还差多少体积/数量/天数）。
3. **魔力与效率**（时魔、魔力/TB、魔力异常检测）。
4. **促销维度**（免费/50%/2×免费/免费截止时间）—— 影响“什么时候可以合法删”和“加种是否值得”。
5. **H&R 的“闸门”而非“报表”**（选种阶段就不碰 H&R；不确定时 fail-closed）。
6. **僵尸做种/站点已删种子清理**（含防误删交叉验证 + 仅打标签）。
7. **释放预算**（按天聚合可释放容量、按优先级排队、全局保种体积上限与动态删种）。
8. **逐种子上传限速与站点恶意限速红线保护**。
9. **刷流**（多站点、独立调度、促销/H&R/做种数/发布时间选种、自动删种、托管标签、诊断）。
10. **辅种/转种动作**（现在只有视图；缺“补 tracker / 落地校验 / 下载器间搬运”的动作能力）。
11. **站点连通性与流控**（现在完全没有“面板会不会因为访问站点而害用户”的答案）。
12. **调度/通知/自动化平台**（定时策略执行、Web Push/Webhook 通知、失败重试与幂等）。
13. **数据保留与快照策略**（趋势指标依赖历史采样，需要明确保留策略与清空入口）。

---

## 3. 面向「只用 Transmission/qB API 的 Web 面板」的落地建议

约定：**可行 = 纯下载器 API 就能做**；**有限可行 = 需要一个额外输入（手工规则 / 用户粘贴 .torrent / 导入文件）**；**不可行 = 需要站点登录态或站点私有 API，除非用户明确接受风险并自行提供凭据**。

### 3.1 台账增强：把“站点维度”从下载器数据里挤出来（可行）

用 `tracker` / `trackers` 把种子归属到站点域名（qB: `/api/v2/torrents/info` 的 `tracker`，或 `/api/v2/torrents/trackers?hash=`；TR: `trackerStats[].host`），做：

- 每站：`种子数 / 做种中 / 暂停 / 已完成未做种 / 做种体积 / 累计上传 / 累计下载 / 平均分享率`；
- **效率指标升级**：除“上传量/TB·天”，加“**单位盘位产出**”= 该站 30 天上传增量 ÷ 平均做种体积（GB），用于回答“哪些种子在占盘不干活”；
- **告警**：站点数据过期（N 天无上传增量）、零做种、tracker 未归属（域名映射表兜底，抄 SeedStats 的 `域名=站点名`）、**站点下所有种子 tracker 都不工作**（可能的关站/换域名）；
- 保留策略：小时级 7 天 + 天级 90~180 天（对齐 MoviePilot 的 180 天站点数据保留），并提供“清空历史”。

数据源：qB `/api/v2/torrents/info` 字段 `hash,name,size,total_size,progress,ratio,uploaded,downloaded,seeding_time,completion_on,added_on,last_activity,state,category,tags,save_path,content_path,tracker,num_seeds,num_leechs,upspeed,dlspeed,amount_left,eta`；TR `torrent-get`（字段见 1.4）。**注意**：qB 的 `ratio_limit/seeding_time_limit` 字段名与可用性随版本变化（`seeding_time_limit` 在较新版本才有），上线前用实际版本核对；`magnet_uri` 同理（没有就退到 `/api/v2/torrents/export` 拿 .torrent）。

### 3.2 释放日历升级：义务模型 + 释放预算（可行，最大价值）

把“每个种子的义务”建成显式结构，而不是单一“最小做种天数”：

```
obligation = {
  site,                       # 由 tracker 归属
  min_seed_hours,             # 来自站点规则 / 手动覆盖
  target_ratio,               # 来自站点规则
  hr_hours,                   # H&R 专用，优先于上面两项（照抄 BrushFlow 短路逻辑）
  promotion_expires_at,       # 有限可行：手工/粘贴 .torrent 时读 comment/source，或用户手填
  size,                       # 释放后能腾出多少
  priority,                   # 站点的保号/升级价值（来自站点规则里的“保号目标/等级门槛”）
}
```

- **到期时间**：时间义务 = `completion_on`(qB) / `doneDate`(TR) + `min_seed_hours` − now。若该种子已设了客户端分享限制，优先用客户端值（qB `seeding_time_limit` 分钟 / TR `seedRatioLimit`）。
- **分享率义务**：没有解析解 → 用该种子近 7 天上传速率做**带标记的估算**（“按当前速率预计 X 天后达标”），并把估算口径写在 UI 上（照抄 PTDataStatistics 的“粗估/基线不足”处理方式：**明说不确定性，不要假装精确**）。
- **日历 = 按天聚合的可释放容量**：每天显示“可释放 X GB / Y 个种子”，并支持“只释放达到全部义务的种子”这一**默认安全模式**；对未达义务的显示“最早可释放时间”而不是空白。
- **促销到期即时检查**：若已知免费截止时间，在该时点前后做一次检查（BrushFlow 用一次性 `date` 触发器实现），到期且义务已满足 → 进入“建议释放”，而不是自动删（默认手动确认）。

数据源：纯客户端字段 + `elapsed/now` 计算，全部可行；只需要在“站点规则配置”里把字段从 3 个扩到“H&R 小时 / 最小做种小时 / 目标分享率 / 是否官种要求 / 是否必须免费才加种 / 保号目标”。**没有站点登录态也能算**。

### 3.3 契约/保号视图（有限可行 → 强烈推荐）

抄 `ContractCheck` 的输入形态，但**不需要抓站点**（这是它比 MoviePilot 更占便宜的地方）：

- 规则：`站点 | 是否官种 | 契约体积(GB) | 契约数量 | 契约周期(天) | 开始时间`；
- 实况：由 3.1 的按站统计直接得到（做种体积、做种数、已持续天数）；
- 输出：`已达成 / 还差 X GB / 还差 N 个 / 还剩 D 天`，并把它接到释放日历的 `priority` 上（契约吃紧的站点 → 其种子不允许被释放）。

**魔力/等级（不可行 → 有限可行）**：魔力、等级、加入时间、站内信都拿不到（qB/TR 不存）。可选路径：
- 最小代价：让用户在 保种台**手工/CSV 导入**每站 `魔力 / 等级 / 加入时间`，面板存快照后即可算「时魔 = Δ魔力/Δt（0.5~12h 窗口）」「效率 = 每小时魔力 ÷ 做种体积 TB」，并对“魔力下降 / 数据过期 / 无魔力站点”打标（照抄 SeedStats 的表述，包括**明说不是官方时魔**）。
- 等级门槛表：可像 MoviePilot 一样内置站点等级要求表，但**注意版权**——MoviePilot 的 234 站规则数据取自 `PTDepilerMp`（GPL-3.0），若要内置需遵守其许可或自行整理；更稳的做法是“支持用户上传 JSON 规则覆盖”。
- 需要站点 Cookie 才能自动化的部分：**明确不做或做成可选的“自带凭据”实验功能**，并且在文档里写清封号风险（见第 4 节）。

### 3.4 僵尸/失效种子清理（可行，对标 CleanInvalidSeed）

判定条件（必须全部满足才动作）：
1. 该种子所有 tracker 处于错误态（qB tracker `status == 4`，`msg` 命中已知错误文案集；TR `trackerStats[].lastAnnounceSucceeded == false` 且 `lastAnnounceResult` 命中）；
2. **同一域名下存在至少一个 tracker 正常工作的种子**（避免站点临时关站批量误删）；
3. 不在排除的分类/标签内。

处置分两段（重要）：
- 第一段：**只打标签**（如 `无效做种`/`site-deleted`），并在 UI 里列出；
- 第二段：人工或策略确认后 `deleteFiles=false` 删种（保留文件，防其他站辅种），并**顺手写入档案库（含磁链）**——正好接上 保种台已有的“复活”。

qB：`/api/v2/torrents/trackers`、`/api/v2/torrents/addTags`、`/api/v2/torrents/delete?deleteFiles=false`；TR：`torrent-get{trackerStats}`、`torrent-set{labels}`、`torrent-remove{delete-local-data:false}`。全部可行。

### 3.5 同内容矩阵 → 从“看”升级为“一键落地”（有限可行，含诚实边界）

- **自动发现同内容（不可行）**：MoviePilot 的 CrossSeed 靠站点 `pieces-hash` API（或 IYUU API）才做到“自动找”。面板拿不到，**不要假装能做到**。
- **可行的高价值版本**：用户把另一站的 `.torrent` 拖进矩阵单元格，面板：
  1. 解析其文件清单（qB 加种后 `/api/v2/torrents/files`；TR `torrent-get{files}`）与已有种子的文件清单做 `名称+大小` 匹配；
  2. 若 info-hash 已存在（同一发布被两市收录）→ 直接给现有种子补 tracker（qB `addTrackers` / TR `torrent-set{trackerList}`），日后再拿辅种流量；
  3. 若 info-hash 不同但文件清单一致 → 以 `paused=true` + 目标 `savepath`（已有种子的 `save_path`）加种，然后 `recheck`，校验通过再 `resume`；
  4. 校验失败 → 立即回滚删除（不动文件）。
- 这套流程的两个"原语"MoviePilot 已经在用：`add_torrent(... is_skip_checking / seedMode)`（qB 5.3 的 `seedMode` 就是“复用已有数据”语义）与 `recheck_torrents`。**答辩口径**：面板做的是“**手动/半自动落地 + 校验**”，不是“自动发现同内容”。

### 3.6 上传限速与红线保护（可行，建议做成“有下限的限速”）

- qB：`/api/v2/torrents/setUploadLimit`（`limit` 单位 B/s，`hashes` 支持多哈希）；TR：`torrent-set{uploadLimit, uploadLimitEnabled}`。
- **必须抄的三条**：
  1. **下限护栏**：任何自动限速都不得把“账号合计均速”压到站点红线附近。MoviePilot 的硬编码是 **100 KB/s 下限**，理由是 M-Team 把“账号当日所有有上传种子的合计均速 < 10 KB/s、单月累计 15 天”判定为恶意限速并封号（取其 10 倍余量）。规范化建议：下限 = f(站点红线, 种子数)，并在 UI 上显示“当前账号合计均速 / 可压到的下限”。
  2. **限速有效性判定**：为每个种子维护“未被限速时的上传峰值”（24 小时半衰期衰减，限速期间不采样），峰值够不到限速值就不限速；不要用瞬时速度判断。
  3. **所有权与回滚**：只记录本插件设置过的限速，停用/卸载自动恢复不限速，下载器离线持续重试；状态落盘。
- 阈值维度：全局 + 按站点覆盖（`站点名=阈值`），并且**与 qB 全局上传限速取较小值**。

### 3.7 全局保种预算与动态删种（可行）

- 全局“保种体积上限”（所有做种任务 `size` 之和）；
- 动态删种两种语义（照抄 BrushFlow）：**单值 `100`** = 达到 100 GB 后删到 100 GB；**区间 `50-100`** = 达到 100 GB 后删到 50 GB；
- 候选集合纪律：只允许**义务已满足**的种子参与淘汰（H&R 未清的绝对排除；契约吃紧站点的排除）；
- 淘汰排序建议：`义务完成度 → 单位盘位产出（3.1 的效率）→ 站点优先级 → 体积`，并给出“为什么删这颗”的可解释原因（BrushFlow 会记录实际删除原因）。

### 3.8 调度、通知与失败处置（可行）

- 面板已有实时推送；补三类通知即可覆盖 MoviePilot 通知的大半价值：
  1. **义务即将到期/已满足**（“今天有 X GB 可以释放”）；
  2. **站点下全部 tracker 失效**（可能的关站/换域名）；
  3. **保种预算/磁盘阈值触发**、**限速护栏触发**。
- 渠道：浏览器 Web Push（Web Push API）+ 通用 Webhook（兼容 Telegram/Bark/Server 酱等）即可，**不必**复刻 MoviePilot 的 20+ 渠道。
- 安全默认：所有自动化策略默认**关闭**；提供 `dry-run`（只报告不动作）；执行前做范围预览；每个动作记录命中原因与回滚方式；限速/删种类动作提供**一键全量回滚**（对照 MoviePilot 的 `MOVIEPILOT_SAFE_MODE` 思路：一个开关关掉所有自动动作）。

### 3.9 多下载器搬运（可行）

- TR 侧取种子文件/数据目录，qB 侧 `add_torrent(paused) + set_torrent_location + recheck`（或反向），配合 `path_mapping` 概念做“主机路径 ↔ 容器路径”映射（MoviePilot 的 `DownloaderConf.path_mapping` 就是这个问题的标准解法，面板一定要有等价物）。
- 注意：跨下载器搬运必须保留 info-hash（否则等于重新下载），且搬运期间不能有两个客户端同时做种同一份数据（MoviePilot 用“转移”语义，即搬完停旧的）。

### 3.10 明确不可行 / 不建议做

| 想法 | 结论 |
|:--|:--|
| 面板自动登录站点、自动签到、自动抓站点用户数据 | **不可行/不建议**：需要站点凭据 + 反爬对抗（CloakBrowser、Cloudflare、验证码 OCR），且这是**封号风险最集中的地方**。MoviePilot 自己都声明自动登录“并不是 100% 成功”，并把浏览器仿真作为“减少失败率”而非“绕过检测”的手段。若要做，只能是用户自带凭据、默认关闭、严格限流、逐站白名单 |
| 自动发现跨站同内容（pieces-hash / IYUU） | **不可行**（需站点私有 API）。改用 3.5 的手动落地 + 校验路径 |
| 站内信读取 | 不可行（需站点 Cookie）。用 3.8 的 Webhook 之外无替代 |
| 魔力/等级/时魔自动采集 | 不可行（同上）。用 3.3 的手工/CSV 导入 |
| 全功能刷流 | 技术上可行（见 3.11），但**产品上建议不做**（与现有定位重叠且风险高）；若做，只做“免费种 + 低并发 + 预算上限 + 义务优先”的安全子集 |

### 3.11 如果一定要做“刷流”的安全子集（可行，但需明确边界）

最小可用且安全的版本：
- 数据源：站点 RSS 地址（用户填，URL 里自带 passkey，**不需要 Cookie**；这是 PT RSS 的常态，也是唯一不需要站点登录态的资源入口）；
- 加种：仅 `downloadvolumefactor == 0`（免费）或 `2x`（若 RSS 提供该字段）；用磁链或 torrent URL 交给下载器；打专属标签 `刷流-<任务>`；
- 预算：单任务并发上限、全局保种体积上限、全局带宽上限、**时间窗**（默认不进深夜，抄 AutoSignIn 的 9–23 点）；
- 删种：义务优先（H&R 未清绝不删）+ 促销到期 + 最小做种时间 + 达标分享率 + 平均上传速度过低（带最小做种时长） + 未活动时间；
- 防重复：同站 / 跨站“同名且未下载完成”不重复加种（抄 BrushFlow）；
- 兜底：`dry-run` 模式、每轮命中的条件写日志并在 UI 可解释、所有动作可回滚；
- **明确告知**：自动刷流本身就是站点规则敏感行为，默认关闭，且提供“站点是否允许自动刷流”的用户自述开关。

---

## 4. MoviePilot 刻意警告 / 安全默认可照抄的点

### 4.1 站点动作与封号风险

1. **认证前置**：站点页首条警告 —— 没有通过用户认证，私有站和公开站都无法维护到系统中，已维护的站点实际也无法使用（用户认证参考 Wiki「配置参考」）。
2. **签到是为了保号**：Wiki 插件页对 `站点自动签到` 的定位就是“自动完成站点签到或者模拟登录站点，**避免长时间未登录被封禁**”（即签到是合规保号动作，而**不是**刷数据）。
3. **恶意限速会被封号（最具体的一条）**：`QbUploadLimiter` README 明确写出 —— 站点（如 M-Team）按「**账号当日所有有上传种子的合计均速低于 10 KB/s、单月累计达 15 天**」判定为恶意限速并**封号**。插件的应对是**固定 100 KB/s 下限**（10 倍余量），保证账号合计均速不会掉到红线以下。
4. **限速必须可回滚**：该插件强调“停用/卸载自动恢复不限速”“只对真正会被限速影响的种子限速”“不认领外部设置的限速”，避免把用户账号置于长期低速状态。
5. **H&R 义务优先**：BrushFlow 对 H&R 种子短路 —— 未满足 `hr_seed_time` 或 `seed_ratio` 之前**拒绝删除**；HRBlocker 直接在选种阶段不碰 H&R 种子。
6. **失败关闭（fail-closed）**：AutoSignIn 明确“HTTP 200、按钮消失、脚本文案都不能单独证明签到或登录成功”，不确定就不算成功并重试；`CleanInvalidSeed` 要求同域名有正常种子才判定失效做种 —— 两个例子都是**宁可少做事，不要做错事**。

### 4.2 请求频率与站点压力（MoviePilot 的默认值本身就是安全默认）

| 项 | MoviePilot 的默认/做法 | 含义 |
|:--|:--|:--|
| 站点流控 | 每站可配 `limit_seconds`（相邻请求最小间隔）、`limit_interval + limit_count`（窗口内次数）；搜索时**原子预约**请求时间，并发不绕过；被拒后解析站点返回的「上次访问时间」精确退避 +1 秒 | 面板若要做任何站点请求，必须有等价的 per-domain token bucket |
| 订阅爬虫（自动模式） | **随机 20–40 分钟**间隔、全天 **32 次**列表页请求、**07:00 起**、完成即停 | 不做“固定高频轮询” |
| RSS 模式 | 间隔**必须 > 5 分钟** | 同样避免高压 |
| 订阅搜索 | 间隔 **24 小时且不可调整**，Wiki 直接建议“**没有漏订阅问题就不要开启**”，理由写明“为了避免对站点造成压力” | 功能存在≠该开启；默认关闭 |
| 自动签到 | 默认**9–23 点随机执行 2 次**，随机间隔 2–6 小时；HDBlueSignin 进一步做随机错峰、429 尊重 `Retry-After`、**当天最多 3 轮且预算跨重启保留**、限流冷却不可绕过 | 定时任务要“抖动 + 每日预算 + 冷却持久化” |
| 浏览器仿真 | `BROWSER_EMULATION`（CloakBrowser 或 FlareSolverr）、`CLOAKBROWSER_HUMANIZE` 默认 true、预设 `default/careful`；Wiki 提醒“会减慢站点访问速度，同时占用更多系统资源”“需要正常下载浏览器内核环境” | 反爬是有代价的；面板不适合承担这层 |
| 验证码 OCR | `OCR_HOST` 默认 `https://movie-pilot.org`（可自建）；Wiki 明确“受站点人机验证、登录验证码识别等原因，**并不是 100% 成功**，如多次重试无法获取，仍需要手工登录获取 Cookie” | 自动登录是“尽力而为”，不是能力承诺 |
| 数据刷新 | 站点数据 6 小时；媒体服务器 6 小时 | 不要做分钟级站点轮询 |

### 4.3 凭据与隐私

- `API_TOKEN` 在代码里被标为 `SettingPolicy(sensitive=True)`；工具/接口从不回显令牌。
- `PTDataStatistics`：所有页面与通知**不展示站点域名**（域名只在后端参与等级匹配）；PTD 备份的密码、UUID、鉴权值、密文一律不写日志；`AutoSignIn` 通知也做站点名缩短、详情留全量。
- 面板若将来存站点 Cookie：必须加密存储、永不入日志、可一键清除，并默认不启用。

### 4.4 文件与做种安全

- `reorganize.md`：整理优先**硬链接/复制**；**`移动`会影响原文件做种**；硬链接可改文件名但**不能改内容**；音乐标签写入对硬链接目标做副本保护。
- 推论到面板：**任何涉及做种文件的动作（归档、复活、搬家、清理）都不得 move 正在做种的数据**；删种默认 `deleteFiles=false`。

### 4.5 社区/合规姿态

- `home.md`：**不要在任何国内平台发布或引用此 Wiki，尽量避免在国内公共场合提及 MoviePilot 全称，必要时使用简称 MP**。
- `README.md`：「仅用于学习交流使用，请勿在任何国内平台宣传该项目」。
- 这与 SeedArk 要做 PT 相关功能时的**对外口径/文档口径**相关（尤其发种、刷流这类敏感功能）。

---

## 5. 不确定 / 未验证 / 需要注意的地方

1. **Wiki 与代码的默认值不一致**：Wiki「配置参考」写 `BROWSER_EMULATION` 默认 `playwright`，`v3.2.0` 代码默认 `cloakbrowser`（并支持 `flaresolverr`）。以代码为准，但说明 Wiki 存在滞后。
2. **站点索引清单未逐站核对**：Parser/Spider 家族与 Wiki 的站点列表已列，但我没有逐站验证解析器是否仍可用（MoviePilot 自己也在 Wiki 里写 V3 索引“不再持续更新，请在 设定→关于 中查看最新支持的站点”）。
3. **下载器 API 端点名称需按版本核对**：qB 的逐种子分享限制是通过 `qbittorrent-api` 客户端的 `torrents_set_share_limits / torrents_set_upload_limit` 间接取证（对应 Web API 的 `setShareLimits` / `setUploadLimit`）；`seeding_time_limit`、`ratio_limit`、`magnet_uri` 等 `torrents/info` 字段在不同 qB 版本存在/命名差异。TR 的 `seedRatioLimit/seedIdleLimit/uploadRatio/uploadedEver/doneDate/trackerStats` 已从 MoviePilot 的 `torrent-get` 参数表确证；但 `magnetLink` 不在该参数表中，**需用你的 TR RPC 版本核对**。
4. **第三方插件生态未取证**：现有 SeedArk 提案里提到的 `HitAndRunQ（H&R 助手）`、`SeedSourceGuard（做种守卫）`、`PTDepilerMp` 等不在官方仓内，我**没有**拉取验证其具体行为。本报告关于 H&R 的结论只覆盖官方仓（`HRBlocker`、`BrushFlow`、`CleanInvalidSeed`、`TorrentRemover`、`StorageCleanup`）。
5. **等级规则数据的许可**：`PTDataStatistics` 内置的 234 站等级规则来自 `PTDepilerMp`（GPL-3.0，仓内有 `site_rules.LICENSE` / `site_rules.NOTICE.md`）。SeedArk 若要内置类似数据，需自行整理或遵守许可。
6. **刷流版本口径**：BrushFlow 的标签/托管模型在 V4→V5 有迁移（README 有「升级兼容」章节），本报告描述的是 **V5（插件 6.1.4）**语义。
7. **数字类断言**：官方插件仓 V3 索引 45 个、V2 索引 73 个（`package.v3.json` / `package.v2.json` 计数）；234 站等级规则为脚本统计（`SITE_LEVEL_RULES` 条目数）。
8. **“MoviePilot 没有发种能力”的取证方式**：对主程序 `app/`、`docs/`、Wiki 全量 Markdown、官方插件仓 `plugins.v2|plugins.v3` 做过关键词检索（发种/发布种子/制作种子/BBCode/create_torrent/upload_torrent），无命中（只有等级文案里的“可以直接发布种子”）。仍存在“某第三方插件仓里有”的可能 —— 但那不属于 MoviePilot 本身。

---

## 6. 主要信源

**主程序源码**（`jxxghp/MoviePilot`，`v3` 分支，`APP_VERSION = v3.2.0`）
- `app/schemas/site.py`、`app/domain/site.py`、`app/schemas/context.py`（`TorrentInfo.hit_and_run / downloadvolumefactor / freedate`）
- `app/chain/site.py`、`app/chain/search/provider.py`（站点流控/退避）、`app/application/site/*`
- `app/api/endpoints/site.py`、`app/api/endpoints/dashboard.py`
- `app/db/models/{site,sitestatistic,siteuserdata,workflow}.py`
- `app/runtime/config.py`（配置全集）、`app/scheduler/catalog.py`（调度任务目录）
- `app/modules/indexer/{parser,spider}/*`、`app/modules/{qbittorrent,transmission,rtorrent}/*`
- `app/application/rules.py`（`BUILTIN_RULE_SET`）、`app/modules/filter/__init__.py`
- `app/workflow/actions/*`、`app/schemas/workflow.py`、`app/sdk/plugin/base.py`
- https://github.com/jxxghp/MoviePilot

**Wiki**
- https://wiki.movie-pilot.org/ （首页，含“勿在国内平台传播”警告）
- https://wiki.movie-pilot.org/site （站点维护：站点字段、用户认证警告、流控、浏览器仿真、CookieCloud、图标/分类/资源）
- https://wiki.movie-pilot.org/configuration （环境变量全集：站点/订阅/搜索/下载、OCR、兼容对照表）
- https://wiki.movie-pilot.org/subscribe （两种订阅模式、随机 20–40 分钟、32 次/天、订阅搜索不可调、洗版）
- https://wiki.movie-pilot.org/search （聚合搜索、过滤规则、流式搜索、音乐搜索）
- https://wiki.movie-pilot.org/reorganize （整理方式与保种的关系、硬链接/移动对做种的影响）
- https://wiki.movie-pilot.org/plugin （插件市场、常用插件介绍、GITHUB_TOKEN/GITHUB_PROXY 限流）
- https://wiki.movie-pilot.org/notification （通知渠道、通知隔离与发送范围、远程交互）
- https://wiki.movie-pilot.org/agent 、https://wiki.movie-pilot.org/mcp （智能助手与 MCP 暴露面收敛）
- Wiki 源仓库：https://github.com/jxxghp/MoviePilot-Wiki （`site.md` / `configuration.md` / `subscribe.md` / `plugin.md` 等原始 Markdown）

**官方插件市场**（`jxxghp/MoviePilot-Plugins`，`main`）
- 索引：`package.v3.json`（45 个）、`package.v2.json`（73 个）
- `plugins.v3/seedstats`（站点保种统计 1.6.0）、`plugins.v3/hrblocker`（H&R Blocker 2.0.0）、`plugins.v3/qbuploadlimiter`（QB上传限速 3.1.0，含恶意限速红线说明）、`plugins.v3/autosignin`（站点自动签到 3.0.5）、`plugins.v3/downloadsitetag`（3.2.0）
- `plugins.v2/brushflow`（站点刷流 6.1.4，含 `models.py` 字段全集与 README）、`plugins.v2/contractcheck`（契约检查 2.0.0）、`plugins.v2/torrentremover`（自动删种 2.3）、`plugins.v2/cleaninvalidseed`（清理QB无效做种 2.0）、`plugins.v2/crossseed`（青蛙辅种助手 3.0.3）、`plugins.v2/iyuuautoseed`（IYUU自动辅种 2.19）、`plugins.v2/torrenttransfer`（自动转移做种 1.10.3）、`plugins.v2/ptdatastatistics`（PT数据统计 2.1.8，含 `site_rules_builtin.py` 234 站等级规则）、`plugins.v2/storagecleanup`、`plugins.v2/hdbluesignin`、`plugins.v2/promotiontag`、`plugins.v2/speedlimiter`、`plugins.v2/siterefresh`（站点自动更新/模拟登录取 Cookie）
- 仓库：https://github.com/jxxghp/MoviePilot-Plugins
