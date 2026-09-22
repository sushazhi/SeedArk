# qBittorrent 设置面板对齐报告

**基准**：qBittorrent `release-5.2.3`
- `src/webui/www/private/views/preferences.html`（3449 行，完整取得）
- `src/webui/www/translations/webui_zh_CN.ts`（5178 行，官方简体中文）
- `src/webui/.../appcontroller.cpp`（API 键名与写入校验）
- `src/gui/preferences.cpp`（出厂默认值）

**改动文件**：`backend/internal/qbittorrent/qbprefs.go`、`backend/internal/qbittorrent/qbprefs_test.go`

前端 `frontend/src/components/SettingsModal/QBSettings.tsx` 完全按后端 schema 通用渲染，界面文案 100% 来自 `qbprefs.go` 的 `Label` / `LabelEn` / `Hint` / `HintEn`，因此对齐工作全部落在后端，前端零改动。

---

## 一、结构性差异（控件类型错误）

### TMM 四项在上游是下拉框，不是复选框

上游 `preferences.html` 中这四项均为 `<select>`，原来的实现渲染成了 bool 复选框，「手动/自动」「重新定位/切换为手动模式」的语义完全看不到。

| 键 | 上游标签 | 选项（前者为默认） |
|---|---|---|
| `auto_tmm_enabled` | Default Torrent Management Mode | Manual / Automatic |
| `torrent_changed_tmm_enabled` | When Torrent Category changed | Switch torrent to Manual Mode / Relocate torrent |
| `save_path_changed_tmm_enabled` | When Default Save Path changed | Switch affected torrents to Manual Mode / Relocate affected torrents |
| `category_changed_tmm_enabled` | When Category Save Path changed | 同上 |

为此在 `qbprefs.go` 新增**布尔枚举**能力：

```go
func fenumBool(key, label, labelEn string, opts ...driver.SettingsOption) driver.SettingsField  // Type=FieldSelect, ValueType=FieldBool
func boolOpt(v bool, label, labelEn string) driver.SettingsOption                                // Value 用 strconv.FormatBool(v)
```

`coerce` 与 `normalizeRead` 的 `FieldSelect` 分支都增加了 `f.ValueType == driver.FieldBool` 处理：接受 JSON 布尔或 `"true"`/`"false"` 字符串，经 `hasOption` 校验，越界报 `"%w: 设置项 %s 的取值 %t 不在可选范围内"`。

### 其余控件类型修正

| 键 | 原 | 现 | 依据 |
|---|---|---|---|
| `web_ui_reverse_proxies_list` | `ft`（textarea） | `fs`（单行） | 上游是 `<input>`，不是 textarea |
| `dht_bootstrap_nodes` | `fs` | `ft` | 上游是单行 input，但 qB 写入时按换行 join，多值场景用 textarea 更合适 |
| `web_ui_api_key` | `readOnly(ft(...))` | `readOnly(fs(...))` | 上游是单行只读 |

---

## 二、此前因截断而未取得的三个标签（本轮补全）

`web_fetch` 对 raw.githubusercontent.com 有约 50KB 截断，`preferences.html` 两个版本都断在 `peerTurnoverCutoff`。改用 **GitHub API contents 端点**（返回 base64，不受截断）后取得全文：

| 键 | 上游标签 | 备注 |
|---|---|---|
| `peer_turnover_interval` | Peer turnover disconnect interval: | 单位 "sec"，JS 校验范围 **0..3600**（原先设的 1<<20 过宽） |
| `request_queue_size` | **Maximum outstanding requests to a single peer:** | 此前误用 "Request queue size" |
| `dht_bootstrap_nodes` | DHT bootstrap nodes: | placeholder = "Resets to default if empty" |

---

## 三、文案对齐（按官方 zh_CN 而非自译）

典型错误类型：

1. **中文与英文语义不符**
   - `disk_cache_ttl`：英文 "Disk cache expiry interval"（过期间隔），中文原写「磁盘缓存写入间隔」→ 改「磁盘缓存过期间隔」。

2. **自造内容**
   - `i2p_enabled` 自加了「实验性」，上游无此词 → 改「启用 I2P」。
   - `dht` / `pex` / `lsd` 原先自造 hint，上游无 → 删除，并采用上游整句：
     - 「启用 DHT (去中心化网络) 以找到更多用户」/ "Enable DHT (decentralized network) to find more peers"

3. **术语与官方译法不一致**（官方 zh_CN 用「用户」而非「Peer」、「Torrent」不译）
   - `torrent_content_layout` → "Torrent 内容布局"
   - `max_active_checking_torrents` → "最大活跃检查 Torrent 数"
   - `queueing_enabled` → "Torrent 排队"
   - `slow_torrent_inactive_timer` → "Torrent 非活动计时器"
   - `ip_filter_trackers` → "匹配 tracker"
   - `temp_path_enabled` → "保存未完成的 torrent 到"
   - `incomplete_files_ext` → "为不完整的文件添加扩展名 .!qB"
   - `use_unwanted_folder` → "将未选中的文件保留在 \".unwanted\" 文件夹中"

   > 2026-09-22 更新：本组里把 torrent 改成「Torrent」的条目已按应用内用词统一回「种子」（见第七节），
   > `ip_filter_trackers` 只保留官方的「匹配」措辞、大小写按本表风格作 "Tracker"。
   > `incomplete_files_ext` / `use_unwanted_folder` 两条不受影响。

3b. **枚举选项中文对齐官方 zh_CN**（本轮补齐）

| 键 | 选项 | 原 | 现（官方 zh_CN） |
|---|---|---|---|
| `torrent_changed_tmm_enabled` | Relocate torrent | 重新定位种子 | 重新定位 Torrent |
| `torrent_changed_tmm_enabled` | Switch torrent to Manual Mode | 将种子切换为手动模式 | 切换 Torrent 到手动模式 |
| `save_path_changed_tmm_enabled` | Relocate affected torrents | 重新定位受影响的种子 | 重新定位受影响的 Torrent |
| 同上 | Switch affected torrents… | 将受影响的种子切换为手动模式 | 切换受影响的 Torrent 至手动模式 |
| `max_ratio_act` | Stop / Remove / Remove and files / Super seeding | 停止种子 / 删除种子 / 删除种子及其文件 / 为种子启用超级做种 | 停止 Torrent / 删除 Torrent / 删除 Torrent 及所属文件 / 为 Torrent 启用超级做种 |
| `upload_slots_behavior` | Fixed slots | 固定槽位 | 固定窗口数 |
| `upload_slots_behavior` | Upload rate based | 按上传速率 | 基于上传速度 |
| `upload_choking_algorithm` | Round-robin | 轮询 | 轮流上传 |
| `utp_tcp_mixed_mode` | Prefer TCP | 偏向 TCP | 优先使用 TCP |
| `utp_tcp_mixed_mode` | Peer proportional | 按 Peer 比例（限制 TCP） | 按对等节点比重（抑制 TCP） |
| `disk_io_read_mode` / `disk_io_write_mode` | Disable/Enable OS cache | 禁用/启用系统缓存 | 禁用/启用操作系统缓存 |
| `disk_io_write_mode` | Write-through | 直写（Write-through） | 连续写入 |

（`dyndns_service` 的 DynDNS / NO-IP 上游为裸文本无 QBT_TR，保持原样。）

> 2026-09-22 更新：本表中改出「Torrent」的四行已回改为「种子」（第七节）；`utp_tcp_mixed_mode` 的
> "Peer proportional" 选项改按第七节的 Peer 统一用词（「按 Peer 比重（抑制 TCP）」）；其余各行不受影响。

3c. **`web_ui_api_key` 标签改为应用内自述**
   - 上游仅作 "Key:"。SeedArk 的 qB 驱动把 `qbt_` 前缀 + 32 位的密码识别为 5.2+ API Key（`backend/internal/qbittorrent/client.go:103` `isAPIKey(pass)` → `c.apiKey = pass`，:319 以 `Authorization: Bearer <key>` 发送），用户在「连接设置」里就是把 API Key 填在密码栏。
   - 故标签改为「API 密钥（本应用中即连接时填写的密码）」/ "API key (in this app: the password entered when connecting)"，比裸 "Key" 更能说明该值在本应用中的来源。

4. **单位/量词错误**
   - `slow_torrent_inactive_timer` 单位原写 "sec"，上游同页明确是 **"seconds"**（`rss_fetch_delay` 才是 " sec"，带前导空格）。
   - `max_connec` / `max_connec_per_torrent` / `max_uploads` / `max_uploads_per_torrent` 英文漏 "number of"。

4b. **中文单位在国际化界面下会串到英文（本轮发现并修正）**

`frontend/src/components/SettingsModal/QBSettings.tsx:65` 把 `field.unit` 原样渲染成固定宽度标签（`<span className="text-caption1 text-gray-400 w-16">{field.unit}</span>`），**单位不做语言切换**。原先 11 个字段的单位写成中文（「分钟」「秒」「毫秒」），英文界面下同样显示中文。上游这些单位的写法本身就是语言中立的符号：

| 键 | 原单位 | 现（= 上游） |
|---|---|---|
| `rss_refresh_interval` | 分钟 | min |
| `rss_fetch_delay` | 秒 | sec |
| `web_ui_ban_duration` | 秒 | sec |
| `web_ui_session_timeout` | 秒 | sec |
| `save_resume_data_interval` | 分钟 | min |
| `save_statistics_interval` | 分钟 | min |
| `refresh_interval` | 毫秒 | ms |
| `disk_cache_ttl` | 秒 | sec |
| `upnp_lease_duration` | 秒 | sec |
| `hostname_cache_ttl` | 秒 | sec |
| `stop_tracker_timeout` | 秒 | sec |

（`feed_refresh_interval` 上游是 " min"、`feedFetchDelay` 是 " sec"、其余为 "sec"/"min"，这里统一去掉前导空格以适配固定宽度标签。）

5. **限定语位置错误**
   - 上游把 "(requires restart)"、"(require macOS or Windows)" 放在 **label 文本内**，而不是 tooltip。已修正 `announce_ip`、`announce_port`、`mark_of_the_web`、`disk_io_type`。

6. **`max_ratio_act` 曾是整句，实际只是连接词**
   - 上游 L842 该下拉的标签就是 **"then"**（官方译「则」），而 "When ratio reaches" 是**另一个控件** `max_ratio_enabled` 复选框的标签。二者是独立的两个控件，原先合并成整句是错的。

7. **复选框 legend 句子**
   - `max_ratio_enabled` → "When ratio reaches" / 「当分享率达到」
   - `max_seeding_time_enabled` → "When total seeding time reaches" / 「达到总做种时间时」
   - `max_inactive_seeding_time_enabled` → "When inactive seeding time reaches" / 「达到不活跃做种时间时」

8. **邮件 SSL 整句**
   - `mail_notification_ssl_enabled` 原「需要 SSL」→ 上游整句 "This server requires a secure connection (SSL)"。

---

## 四、有意保留的偏差（非缺陷）

上游把 `dl_limit` / `up_limit` / `alt_dl_limit` / `alt_up_limit` 的标签只写 **"Download:" / "Upload:"**，靠外层 `<legend>Global Rate Limits</legend>` / `Alternative Rate Limits` 提供上下文。同样地，`i2p_address` = "Host:"、`i2p_port` = "Port:"、`proxy_type` = "Type:"、`schedule_from` / `schedule_to` = "From:" / "To:"，都依赖分组标题。

SeedArk 的 schema 以**扁平列表**渲染全部 216 个字段，没有嵌套 legend。若照搬 "Download:" 这类单词标签，界面上会出现多个无法区分的同名项。因此这些字段保留带上下文的完整表述（如「全局限速：下载」「I2P 主机」），**属于渲染模型差异导致的有意适配，不是不对齐**。

同理，上游的 `[0: listening port]`、`[0: disabled]` 之类的方括号后缀已转写为 hint 文案（「0 表示使用监听端口」）。

## 四·补、上游 tooltip 的吸收

上游 `preferences.html` 共 13 处 `title=` 属性，本轮逐一核对后吸收了以下三条（其余为按钮文案如 "Copy API key" 或与字段无关的分组说明）：

| 字段 | 上游 tooltip | 落地为 |
|---|---|---|
| `listen_port` | Set to 0 to let your system pick an unused port | hint「设为 0 由系统自动选择未占用端口」 |
| `use_category_paths_in_manual_mode` | Resolve relative Save Path against appropriate Category path instead of Default one | hint「相对保存路径按对应分类路径解析，而非默认路径」 |
| `web_ui_reverse_proxies_list` | Specify reverse proxy IPs (or subnets, e.g. 0.0.0.0/24) in order to use forwarded client address (X-Forwarded-For header). Use ';' to split multiple entries. | hint 补全 X-Forwarded-For 语义与 ';' 分隔说明 |

`i2p_mixed_mode`、`app_instance_name`、`refresh_interval` 的 tooltip 此前已吸收。`confirm_torrent_deletion` 的 tooltip（"Shows a confirmation dialog upon torrent deletion"）与标签同义，未重复成 hint。

全部 41 条 hint 均为补充说明，与上游文案无冲突。

---

## 五、验证

| 检查 | 结果 |
|---|---|
| `go build ./...` | 0 |
| `go test ./...` | 全部 ok（10 个包） |
| `go test ./internal/qbittorrent/` 7 个测试 | 全部 PASS |
| `npx tsc --noEmit` | 0 |
| `gofmt -l internal/qbittorrent/qbprefs.go` | 无输出 |
| 最终 schema | 8 分节 / **216 项**（behavior 12、downloads 33、connection 30、speed 11、bittorrent 26、rss 7、webui 31、advanced 66） |
| 枚举选项英文 ↔ 上游 option 文本 | 22 个枚举全部逐项一致 |
| 中英双语成对完整性 | 216 个字段的 Label/LabelEn 与 41 条 Hint/HintEn、全部 Options 的 Label/LabelEn 均无缺失 |
| 单位语言中立性 | 无中文单位残留（英文界面不再串出「分钟/秒」） |

测试新增断言：`ValueType == FieldBool` 时 `len(f.Options) != 2 || !optSeen["true"] || !optSeen["false"]` 即报错「布尔枚举 %s 必须恰好有 true/false 两个取值」。

---

## 六、方法论备注

- 本机 `curl` / `Invoke-WebRequest` 访问 raw.githubusercontent.com 失败（exit 35 / SEC_E_NO_CREDENTIALS），但 **api.github.com 可直连**。遇 GitHub 原文截断时改用 contents 端点 + base64 解码可拿全文：

```powershell
$u = "https://api.github.com/repos/qbittorrent/qBittorrent/contents/src/webui/www/private/views/preferences.html?ref=release-5.2.3"
$j = (Invoke-WebRequest -Uri $u -UseBasicParsing).Content | ConvertFrom-Json
$txt = [System.Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($j.content))
[System.IO.File]::WriteAllText($out, $txt, (New-Object System.Text.UTF8Encoding($false)))
```

- PowerShell 控制台显示 UTF-8 中文会 mojibake，但写入文件后再用 read 工具读取是正确的。
- 版本边界：`release-5.2.3` 的 Advanced 页**没有** `startPaused` / `sessionShutdownTimeout` / `web_ui_sessions_count_limit`，Behavior 页**没有** Search 分节与 `addTorrentWindowEnabled`（这些是 master/5.3 才有的），当前 schema 的版本取向正确。

---

## 七、2026-09-22 第三轮：用词统一与漏网自译

复查 215 条标签时发现两类问题：一是仍有官方有现成译法却自译的条目（「发送缓冲区高水位」这类），
二是面板内同一概念两套叫法。本轮逐条重对 `release-5.2.3` 的 `webui_zh_CN.ts` / `qbittorrent_zh_CN.ts`。

### 7.1 用词统一（政策，覆盖第三节相关条目）

| 概念 | 统一为 | 理由与改动面 |
|---|---|---|
| torrent | **种子** | 与本应用其他界面一致（前端中文包 56 处「种子」，列表 / 右键 / 添加弹窗均如此）。据此回改第三节 3.3 与 3b 中此前改成「Torrent」的条目：`torrent_content_layout`、`torrent_stop_condition`、`max_active_checking_torrents`、`queueing_enabled`、`slow_torrent_inactive_timer`、`temp_path_enabled`、`dont_count_slow_torrents`，以及 TMM 四项与 `max_ratio_act` 的枚举选项；`confirm_torrent_*`、`autorun_*`、`add_to_top_of_queue`、`merge_trackers`、`max_active_torrents`、`rss_auto_downloading_enabled`、`torrent_content_remove_option` 本来就是「种子」，未动。`.torrent 文件`、`BitTorrent`、`qBittorrent` 等专名不动 |
| peer | **Peer** | 与本应用其他界面一致（「已连接 Peer」「Peer 连接端口」）。官方中文自身是 对等节点 / 用户 / peer 三套混用，无法照抄。改 `mixedPeerProp` 选项、`peer_turnover` ×3、`request_queue_size`；`peer_turnover` 三项顺带从官方直译的「对等节点进出…」改写为「Peer 轮换…」——英文 turnover 在此是"轮换/更替"（超限时断开旧 Peer 给新 Peer 让位），「进出」读不出这层语义 |

### 7.2 按官方中文纠正的自译条目

| 键 | 原 | 现（官方 zh_CN） |
|---|---|---|
| `send_buffer_watermark` | 发送缓冲区高水位 | 发送缓冲区上限 |
| `send_buffer_low_watermark` | 发送缓冲区低水位 | 发送缓冲区下限 |
| `send_buffer_watermark_factor` | 发送缓冲区水位系数 | 发送缓冲区增长系数 |
| `max_uploads` / `max_uploads_per_torrent` | 全局 / 单种最大上传槽 | 全局 / 单种最大上传窗口数 |
| `upload_slots_behavior` | 上传槽位行为 | 上传窗口策略 |
| `upload_choking_algorithm` | 上传阻塞算法 | 上传连接策略 |
| `utp_tcp_mixed_mode` | µTP-TCP 混合模式算法 | µTP-TCP 混合模式策略 |
| `enable_piece_extent_affinity` | 使用分片区段亲和 | 启用相连分块下载模式（官方作「文件块」，此处随本表统一用「分块」） |
| `enable_upload_suggestions` | 发送上传分块建议 | 发送分块上传建议 |
| `bdecode_token_limit` | bdecode Token 数量上限 | bdecode 令牌数量上限 |
| `dyndns_domain` | 域名名称 | 域名 |
| `ip_filter_trackers` | 匹配 tracker | 匹配 Tracker（仅大小写跟本表风格） |

「上传槽」一组是上一轮的半成品：当时已把选项改成官方的「固定窗口数」，字段标签却还留着自译的「上传槽位行为」。

### 7.3 有意保留（官方译法更差，或与扁平列表渲染模型不符）

- `mail_notification_sender` / `mail_notification_email`：官方作「从：/ 到：」→ 保留「发件人 / 收件人」。
- `checking_memory_use`：官方作「校验时内存使用扩增量」（outstanding 的误译）→ 保留「校验时内存占用上限」。
- `save_resume_data_interval` / `resume_data_storage_type`：官方作「恢复数据」，与"恢复出厂/还原"易混 → 保留「续传数据」。
- `file_pool_size` / `socket_backlog_size`：官方作「文件池大小」「Socket backlog 大小」→ 保留加了限定语的「文件句柄池大小」「Socket 积压队列大小」。
- `listen_port`、`dl_limit` 等上游「主机：」「Download:」式行内片段 → 仍按第四节保留带上下文的完整表述。

### 7.4 同日续：`max_ratio_act` 与演示模式对齐

- **`max_ratio_act` 的连接词标签已改写**。官方 5.2.3 WebUI 作「然后」（源码即 `QBT_TR(then)`），桌面端作「达到上限后：」——两者都是靠上游的分组框撑住的行内片段，在本面板的扁平列表里像半句话。现改为「达到上述任一上限时」/ "When any of the above limits are reached"，并按第四节的约定把适用条件移到小字提示：「分享率 / 做种时长 / 闲置做种时长任一触顶后执行所选动作」。上游 `preferences.html` 里该下拉的禁用条件是三个开关的或（`isMaxRatioEnabled || isMaxSeedingTimeEnabled || isMaxInactiveSeedingTimeEnabled`），故用「任一上限」而非只指分享率。
- **演示模式（`frontend/src/demo/state.ts`）的 qB 自述已按驱动逐字段对齐**：56 处中文 / 英文标签与单位、`save_path` / `temp_path` 的类型（`string` → `path`，否则演示里走不到整行路径渲染）、`torrent_content_layout` 的枚举文案，另修正 5 个键名（`scheduler_from` / `scheduler_to` → 真实键 `schedule_from` / `schedule_to`，`web_ui_use_https` → 真实键 `use_https`，删掉真实 schema 里没有的 `start_paused_enabled`、`random_port` 两行——前者属 5.3 边界项、后者是上游已废弃项），并清掉演示偏好回读表里 11 个真实 schema 之外的键（这些不会显示，写入也会被白名单拦下，留着只会误导后续维护）。对齐后演示 65 个字段与真实自述零差异。
  - 有意未同步的两类：演示是每节挑几项的精简版，**字段取值范围（min/max）与小字提示（hint）仍按演示自己的一套**——它们不是文案问题，全量照搬会把演示撑得跟真实面板一样长。
  - 该文件已加注释声明这条约束：在线演示是公开预览页，收录进来的项不许另写一套文案。

### 7.5 仍待定

- `dht` / `pex` / `lsd` 三条沿用官方整句（「…以找到更多用户」），官方在句中把 peers 译作「用户」，与 7.1 已统一的 Peer 并存。改法待定；**真实驱动与演示 schema 现在是同一份文案，改时要一起改**（演示按 7.4 已照抄驱动）。

### 7.6 验证方式

- 界面侧没有硬编码文案（`QBSettings.tsx` 只按 `lang` 在 `field.label` / `field.labelEn` 间二选一），所以下发什么就显示什么；核对以接口为准：用当前源码单独编译一个后端实例接 qbmock，读 `GET /api/session` 的 `data.schema`，把 8 节 216 个字段的 label / labelEn / unit / hint / 枚举选项全量走一遍字符串扫描——「水位 / 上传槽 / 分片区段 / 阻塞算法 / 对等节点」零命中，含中文的串里除 `.torrent 文件`、`BitTorrent`、`qBittorrent` 外无 Torrent 字样。
- 演示模式另做了一次真渲染核对：`npm run build:demo` + `vite preview`，脚本点开「设置 → 演示 qBittorrent」后取弹窗 `textContent`，确认「全局最大上传窗口数」「发送缓冲区上限」「计划开始时刻」「种子内容布局 / 原始（种子内结构）」「可选：绑定的 IP 地址」等在页面上实际出现，旧写法仅剩「种子内容布局」这种包含关系内的字面重合。
- 注意：`dev.sh` 起着的老后端是改动之前的二进制，界面上会新旧混排（如 `peer_turnover` 仍显示「对等节点进出断开百分比」），**看到旧文案先重启 dev 后端再判定制**。

