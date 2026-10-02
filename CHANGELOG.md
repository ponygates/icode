# 更新日志

> **版本说明**：v0.1.0 是一次**版本归零与结构整理**——将历史迭代（v0.2.x–v0.53.x）的全部成果整合为统一的 0.1.0 功能基线。完整的历史版本记录见 [docs/archive/CHANGELOG_HISTORY.md](docs/archive/CHANGELOG_HISTORY.md)，历次对标审查与差距分析见 [docs/archive/](docs/archive/)。

## v0.1.3 — 提权端点 Token 收权：本机进程也不能再裸调 shell/config/permission (2026-10-02)

> 背景审计（docs/改进建议清单_2026-09-29.md #19）确认：CSRF/同源守卫、fetch SSRF 阻断（internal/netsec）、目录沙箱（AllowedPaths）、registry 锁、密钥脱敏**均已落地**（README Roadmap 旧条目过时，本轮已勾选）。真正残余的边界是 **G1：loopback 上任意本机进程可免鉴权调用全部 API**——包括 `/api/shell` 任意命令执行。本轮把所有「改变系统状态」的端点收进 Bearer token 门禁，**本机也不豁免**。

### 后端：13 个提权端点全部要求 Bearer token

- **新增 `requireAPITokenForMutating` 中间件**（handlers_remote.go）：GET/HEAD/OPTIONS 放行（读取类保持免鉴权），mutating 请求必须携带 `Authorization: Bearer <apiToken>`——**loopback 也不例外**。`/api/chat`、`/api/slash` 维持开放：工具层有权限门（AllowedPaths/审批）兜底，curl/CLI 冒烟便利性保留，信任模型在 CHANGELOG 注明。
- **收权名单（13 个）**：`/api/shell`、`/api/config`（PUT）、`/api/config/reset`、`/api/config/key`、`/api/config/model`、`/api/config/provider`、`/api/permission/mode`、`/api/permission/rules`、`/api/permission/allow-tool`、`/api/permission/respond`、`/api/permission/session-allow`、`/api/update/apply`、`/api/update/restart`。其中 permission/rules 是审批名单外的补充：`SetToolRule(tool, "allow")` 同样改变权限边界。
- **端口文件升级为 JSON `{"port":N,"token":"..."}`**（server.go writePortFile）：先删再写刷新 0600 权限位；token 即每启动随机生成的 32 字节 apiToken，供配套客户端（桌面/VS Code）自动取用。
- **`icode server` 打印带 token 的 Web UI URL**（`Web UI: http://127.0.0.1:<port>/?token=...`）：浏览器打开即用，无需手工拼。

### 桌面 Web UI：URL 摘取 → sessionStorage → fetch 注入

- 后端启动 URL 携带 `?token=`（bootDesktopBackend 组装，Windows WebView2 与 macOS/Linux 系统浏览器两条路径零平台特判同时生效）。
- 新增 `desktop/src/lib/authFetch.ts`：挂载前摘取 URL query 中的 token → 存 sessionStorage（F5 存活）→ `replaceState` 清理地址栏（保留 HashRouter hash；复制/分享链接不再带 token）→ patch `window.fetch` **仅对同源请求**注入 `Authorization` 头（跨域绝不外发 token）。30+ 处既有裸 fetch 调用点零改动。

### VS Code 扩展：读 JSON 端口文件 + 自动带 token

- `readPortInfo()` 解析新版 JSON 端口文件；**旧版纯数字文件向后兼容**（token 缺失则不带头，与旧后端双向兼容）。
- `backendToken` 全局 + `refreshTokenFor(port)`：每次（重新）采纳后端端口时，若端口文件属于该端口则采用其 token，否则清空（well-known 端口探测路径正确降级）。
- `httpGetJSON` / `proxyJSON` / `proxyChat` / `chatComplete` 四个请求函数统一注入 `Authorization`；`ensureBackend` 五条采纳分支全部刷新 token。

### 防回归测试与既有测试适配

- 新增 `TestPrivilegedEndpointsRequireTokenOnLoopback`（12 端点无 token 全 401）、`TestShellAllowedWithToken`（带 token echo 200）、`TestVoiceKeysNotLeaked`（voice 四项密钥不出现于 GET /api/config，补 G3）。
- 测试基建：包级 `testTokens sync.Map`（base URL → token）+ `httpDo` 自动注入 Bearer 头——30+ 处存量测试调用零改动；三个测试工厂（handlers/security/models_fetch）统一注册。CSRF 用例不受影响：evil Origin 在 sameOriginGuard 中间件层即 403，先于 handler 级 token 检查。

### 文档与运维提示

- README Roadmap 勾选已落地的安全项（G4）；`/mesh token` 输出补充轮换提示：token 为静态共享密钥，怀疑泄漏时直接替换 `~/.icode/mesh.token` 并同步对端（G2 的文档级缓解，轮换机制留待后续）。
- 验证：`go test ./...` 52 包全过；desktop tsc + 164 测试全过；vscode tsc 通过；真机验收（无 token POST /api/shell → 401，带 token → 200，桌面 `!` 命令与设置页无感）。

## v0.1.2 — TUI 输入错位/乱码根因修复 (2026-09-18)

修复「在输入框打字时，上部内容区轮流出现文字和乱码」的完整根因链（证据：`~/.icode/screen-dump.txt` 外部写入检测 + `cli.log` IME 漏字母），以及实测反馈的「Delete 键/鼠标滚轮有时乱码」二级根因。

### 根因修复

- **CLI 回归控制台子系统（根因①）**：`build.bat` 去掉 `-H windowsgui`。GUI 子系统进程即使继承了 console 句柄，conhost/ConPTY 也不把它注册为正常控制台客户端——**中文 IME 组合通道断裂**：拼音字母（q、[）直接漏进 stdin（乱码①），IME 上屏串被 conhost 旁路直接写进 console buffer（screen-dump row 9 的 `w^¿w^¿^¿好，还这？` 即外部写入，文字跑到内容区），antiPoison 周期重绘清掉又重现 = **"轮流出现"**；编码损坏字节（`�`）= 乱码②。双击 `icode.exe` 现在由 Windows 自动开终端窗口跑 TUI（期望行为）。桌面版 `desktop_only` 构建保持 windowsgui 不变。历史教训回环：v0.53.x 曾因同样问题改回控制台子系统，后被"双击不闪黑窗"需求覆盖——本次在 ICODE.md 固化"勿加 -H windowsgui"约定，并新增**启动自检**（`cmd/subsystem_check.go`：`debug/pe` 检测自身 PE 子系统，GUI 构建启动 chat 时弹醒目警告 + 修复命令，诱饵二进制端到端实测通过）。
- **键泵转义序列重组（根因②，Delete/滚轮乱码）**：此前 keyPump 逐字节转发，`handleKey` 靠 10ms 超时猜"ESC 后是否有后续字节"。ConPTY 管道拆包或负载延迟使后续字节晚到时，ESC 被误判为孤立 Esc，**序列的剩余可打印部分（`[3~`、`[<64;103;15M`）被当文字插进输入框** = Delete/滚轮偶发乱码。重构为**双 goroutine 字节管道**：`rawBytePump`（唯一读者，原始字节→带缓冲 `byteCh`）+ `keyPump`（消费 byteCh，`pumpEscape` 在泵层把 ESC 序列**重组为原子整包**再派发）。bufio 无超时读，channel select 有——这正是拆泵的意义。附带修复：**X10 鼠标坐标字节（col+32/row+32 ≥0x80）不再过 UTF-8 解码器**（旧实现吞字节 = 宽终端右侧滚轮失灵/吞输入）；ESC 后紧跟 IME 汉字（requeue 路径）不再误判 Alt+<字节>。重组超时/超长时整包回落（与旧逐字节行为等同，永不吞字节）。
- **流式/权限提示的序列消费端修复（根因③，"对话生成时滚滚轮输入框出现乱码"+权限误触高危项）**：根因②把序列重组为原子整包交付，但两个**消费端**只认单一形态——`drainStream`（流式期间）只识别 `ESC [ A`（↑召回队列草稿），其他序列读到第二个字节非 'A' 即丢弃，**剩余可打印尾（滚轮坐标 `64;103;15` 等）经 handleQueueKey 逐字插进输入框**；`PromptPermission`（权限提示期间）更是把 `ESC` 直接当 deny，而后续**坐标数字撞决策键**——滚轮报告的行/列坐标个位为 '1'/'2'/'3' 时分别触发 Allow/AllowAll/Deny，**滚滚轮可能静默授予工具权限**！修复：新增共享 helper `swallowCSI(ch, c2)`（X10 特判吞 3 原始字节 / final 字节 0x40-0x7E 直收 / 参数字节循环至 final，截断返回 0）+ `readRuneTimeout(ch, d)`；drainStream 的 `[` 分支整体吞序列（保留 ↑ 召回语义）、新增 `O` 分支（SS3 final 字母不再泄漏）；PromptPermission 的 Esc case 用跟进超时区分**序列**（整体吞掉继续等待）与**孤立 Esc**（仍 deny，语义不变）。
- **拆帧窗口修复（根因④，F12 取证定位：inputBuf 出现 `64;34;29M`×18）**：ConPTY/WT 会把一个滚轮事件**拆成两次写入且间隙可超 15ms**（复现形态：`ESC[<` + 40ms + `64;34;29M`）。泵层 15ms 重组窗口超时 → pumpEscape **回落只发半个序列**；消费端 swallowCSI 的 10ms 窗口 < 拆帧间隙 → 接不住晚到的尾段 → 坐标数字被 handleQueueKey 逐字插入 inputBuf（= F12 dump 里的乱码铁证）。同理，间隙超过 30ms 首字节窗口时整包退化为**孤立 ESC**——drainStream 10ms 判定"用户按了 Esc" → **流被幽灵中断**（cli.log 里 4 条用户从未按过的 drain 站点 ESC 记录）。修复：泵层 `escAggSeqWait` 15→40ms（吸收常见拆帧，整包路径零开销——窗口只在字节饥饿时生效）；新增 `escSwallowWait` = 100ms（**必须 > 泵层窗口**，泵回落后的尾段仍在窗口内被接住）用于 swallowCSI 参数循环/X10 坐标读取/drainStream 的 u/c2 读取/PromptPermission 序列吞咽；孤立 Esc 首字节判定仍为 10ms（按键手感不受影响）。新增 `repro_wheel_test.go` 五变体复现矩阵：整包/拆帧 × 流式/主循环 + ESC 单发 60ms（幽灵中断）——修复前变体 B 直接复现 dump 乱码形态，修复后全过。
- **状态栏重复渲染与底部空行（根因⑤，F12 取证：row 35/36 两帧 statusLine 残留）**：三个叠加缺陷。① **topRow 公式多减了 aux 行**——aux（ctx bar/thinking/queue）实际画在顶边框**上方**（借 inputRows 预留行），公式却把 ctxRows/qRows/thinkRows 也从 topRow 里减掉，等于整个输入框被每个 aux 行**抬起一行**：ctx 已知时状态栏浮在 H-1、最底行永久空白，且 aux 出现/消失时状态栏在 H↔H-N 之间**跳变**，旧位置无任何代码清理（增量 lastFrame diff 只覆盖会话区）——F12 dump 实锤：row 35（含 $cost 旧帧）与 row 36（含 [mode] 旧帧）两帧 statusLine 残留、row 31 残留旧 contextBar（`100% left`）。② **布局伸缩时"归还行"永不清**：contentRows 变化使 lastFrame 重置（全空），但会话区**空行**的 diff 判定 `""==""` 不重画——旧 prompt 内容钉死在借给过 prompt 块的行上。③ **statusLine 与 contextBar 双渲染 ctx%**。修复：topRow 锚底 `H - statusRows - len(visLines) - 2 + 1`（aux 借 inputRows 预留行）——**状态栏永远贴 H**、aux 开关不再引起位置跳变；drawInputBox 每帧先**全清 prompt 块区域**（clearTop..H 每行 `\x1b[N;1H\x1b[K`，约百字节开销）再绘制；布局变化（contentRows 变）时 render() 在帧首发 `\x1b[2J` 强制全量重画（与 resize 同机制，单次 flush 备用屏内无闪烁）；drawSearchBox 同步修复（旧 bottomRows 算法同样浮空一行且无清区）。

### 状态栏重设计（对标 Claude Code + opencode 取长补短）

- **左右分区布局**：旧版 13 种要素挤一行（title·mode·model·skill·branch·PR·security·tokens·ctx·cache·cost·todo·tool·bg·elapsed），72 列终端下右缘硬截断把 cost/todo/elapsed 切掉。重构为两区：**左区身份/工作**（`[mode]` · ⚙tool · ◑todo · 📎title · ⎇branch · PR · 🧩skills · 安全徽章 · ⚡bg）、**右区用量**（`●model` · $cost · ▸tokens · cache% · ⏱elapsed），中间 pad 到终端边缘。
- **窄终端智能裁剪**：放不下时按优先级**丢尾段**（PR → 🧩 → 安全徽章 → … → elapsed → notice），**mode/model/cost 永不丢**——窄屏丢徽章不丢关键信息；极窄兜底 fitVis 硬截断。
- **ctx% 去重**：上下文百分比从 statusLine 移除，`contextBar`（输入框上方进度条）专属——消除同一信息双渲染。
- 实现：`statusParts()`（锁内收集左右段，段序即丢弃优先级）+ `layoutStatusLine(W, left, right)`（纯函数布局，宽尾先丢、head 保底）。

### 界面全面中文化 + 菜单式设置 + 鼠标开关（2026-09-25 第二批）

- **contextBar 仪表盘渐变条**：`▓▓▓░░░░░░ 42% left` → `上下文 ██████▌░░░ 58% 剩余`——10 格渐变（满格 `█` + 八级部分块 `▏▎▍▌▋▊▉` 按余量渐变 + `░` 空格），中文标签走 i18n（`ctx.label`/`ctx.left`），阈值色保留（绿<60/黄<85/红≥85）；用量>0 恒显 sliver。
- **全界面简体中文化**（i18n 三表 zh-CN/zh-TW/en 同步补 key）：欢迎页（`Welcome back!/Model:/Provider:/Mode:/CWD:/Context:/Cache:` → `欢迎回来！/模型:/服务商:/模式:/目录:/上下文:/缓存:`，Tips 面板全中文）；**mode 徽章中文化**（`modeLabel()` 映射：plan→计划、agent→智能体、yolo→全自动、auto→自动、ask→问答——状态栏/欢迎页/设置面板/cycleMode notice 全走映射）；状态栏 `N% cache`→`缓存 N%`、`⚡N bg`→`⚡后台 N`、`⏱ 1m23s`→`⏱ 1分23秒`；thinkingBar `tok/s`→`tok/秒`、`12s`→`12 秒`；`/mode`/`/plan`/`/provider`/`/config set` 反馈 `Mode -> plan`→`模式 → 计划`（core/slashui 同步）；`[Multiline ON]`→`✓ 多行输入已开启`；`input.hint`（输入框暗提示）英文残留修复；设置面板值显示本地化（`zh-CN`→`简体中文`、`dark`→`深色`、`agent`→`智能体`——`langName()/themeName()` 映射，语言名按各自语言显示为 i18n 惯例）。
- **菜单式设置（opencode 风格）**：`/config`（无参）直接打开设置菜单面板（原设置清单挪到面板"高级配置…"行 + `/config set` 保留）；面板交互升级——**←→ / Enter 面板内直接切值不关闭**（模式 auto→plan→agent→yolo 循环、语言 zh-CN→zh-TW→en 循环、主题 深色↔浅色，切值即时生效：TUI 字段 + 后端 OnSetMode 同步 + persistSetting 持久化 + 面板快照同步刷新）；行结构加 `kind` 字段统一路由（model 行 Enter 开模型选择器，凭据行走 configDump，导航边界与行数同源不漂移）；面板 hint 走 i18n。
- **鼠标开关 `/mouse`**：`setMouseTracking()` 仅切换 ?1000/?1006 鼠标追踪（**括号粘贴 ?2004 不动**，大粘贴不受影响）；关=恢复终端原生拖选，开=点击定位/滚轮/滚动条/工具卡折叠/右键粘贴全套交互；`mouseOn` 字段 gate **动作不 gate 解析**（handleMouse 恒解析消费 SGR 字节防泄漏，动作层判断——测试实证：gate 放入口会泄漏 `64;34;29M` 到输入框）；`New()` 构造置 true（零值安全）；帮助面板新增 `Shift+鼠标拖动` 选择文本说明 + `/mouse` 条目。
- **/mouse 持久化 + 深层英文清尾**：`config.TUI.Mouse *bool`（nil=默认开），`/mouse` 切换即 `persistSetting` 记住、重启保留；Run() 改按 config 态启用（不再硬覆盖）。深层英文清尾 9 处：`Not enough messages to compact.`→中文、`Export failed:`→`导出失败:`、`git diff:`→`git diff 失败:`（TUI+slashui 两处）、`staged edits: /review...`→中文、窄终端 fallback `Welcome to iCode`→`欢迎使用 iCode`（2 处）、slashui `Model ->`→`模型 →`。
- 测试适配：欢迎页断言中文化（TestWelcomeAdaptive/TestWelcomeScreen）、状态栏断言 `[智能体]`（TestStatusBarAnchoredToBottom/TestStatusPartsZonesAndNoCtx）、渐变条断言改"上下文"标签（frameRowRe 只捕获行首 SGR 段，`░` 在第二段——探针实证后改用首段标签定位）、滚轮测试补 `mouseOn: true`；52 包全过。

### 状态栏思考滑块（opencode 同款，2026-09-25 第三批）

- **生成中动画**：最底行状态栏左区（模式徽章后）新增 4 格渐变滑块在 10 格暗色轨道上**来回扫描**（三角波 ping-pong，80ms/帧，由既有 33ms 流式渲染循环驱动，零新定时器）；槽宽恒定 10 格——**零布局抖动**（只动块位置不动槽宽）；渐变双通道：颜色 青→浅青→紫→洋红（`36m`→`38;5;81m`→`38;5;141m`→`35m`）+ 字符密度 `░▒▓█`——**无色模式退化为纯密度渐变**依然可读；轨道底 `·` dim；非 streaming 自动隐藏；窄屏可被 layoutStatusLine 按序裁剪（装饰性段，可接受）。
- 实现于 `render.go` `thinkingSlider()`（statusParts 锁内快照调用），输入框上方 thinkingBar 不动；turnStart IsZero 容错（帧 0 起步）。
- 新增测试 `TestThinkingSlider`（槽宽恒 10、帧 0/1 左缘推进、帧 6 右缘/帧 7 回弹 ping-pong 边界、无色零 SGR）+ `TestThinkingSliderInStatusBar`（idle 无滑块、streaming 左区 left[1] 恰在徽章后、全帧状态栏仍锚底 H 不被滑块顶起）；52 包全过。

### 滑块改版粗杠滑线 + 上下文百分比化（2026-09-25 第四批）

- **滑块改版**（风哥审美拍板：opencode 视觉语言同款）：`░▒▓█` 四色渐变块+`·` 点轨 → **`━` 粗杠在 `─` 细线轨道上滑行**——单色青、粗细对比、低调内敛；几何 10+4 → **14 格轨道 + 3 格粗杠**（span 11，ping-pong 80ms/帧不变，零新定时器）；无色模式靠 `─`/`━` 字形对比仍可读；槽宽恒定 14——零布局抖动特性保留。
- **上下文百分比化**（opencode 同款：右下角裸百分比）：删除输入框上方**整行渐变 contextBar**（`上下文 ██████▌░░░ 58% 剩余`）——提示块整体**省出一行**给会话区；上下文状态唯一归属地 = **状态栏右区**（token 流之后）：`58%` 裸百分比，阈值变色 **<50% 绿 / 50–80% 黄 / ≥80% 红**（opencode 风格，比旧 60/85 阈值更早预警）；thinkingBar 里的重复 context% 同步删除（`⠋ 正在生成… 32% 12s` → `⠋ 正在生成… 12s`）——同一信息**零双渲染**。
- **联动清理**：`contextBar()` 函数删除；`drawInputBox`/`drawSearchBox` 的 ctx 行预留与绘制删除；`render()` inputRows 与 `convHeight()` 的 ctx 减行删除；i18n 三表 `ctx.label`/`ctx.left` 键删除（zh-CN/zh-TW/en）。
- 测试联动 7 处：`TestThinkingSlider`（新几何 14+3、帧 0/1/11/12 断言）、`TestThinkingSliderInStatusBar`（`━━━` 断言）、`TestStatusBarAnchoredToBottom`（ctx case 改断言状态栏含 `45%`——frame 原文断言，避开 frameRowRe 只捕行首 SGR 段的坑）、`TestRenderLayoutShiftWipesScreen`（frame3 布局位移触发源 ctx 行→streaming 思考行）、`TestDrawInputBoxClearsBlockRows`（clearTop 重算：think 6/top 7/bottom 9/status 10）、`TestStatusPartsZonesAndNoCtx`→`TestStatusPartsZonesAndCtxPct`（百分比断言反转：右区必须含 `50%`）、`TestClaudeStyleRender`（`█/░` 条与 `89% 剩余` 断言→`11%` 用量百分比）；52 包全过。


### 渲染层九处加固（对根因的纵深防御，防同类问题复发）

  - render() 写入循环加**行宽兜底截断**（fitVis，感知 ANSI/CJK）——任何超宽行（表格/长工具名/补全描述）触发终端 DECAWM 自动换行都会把整屏布局推乱。
  - render() 的 inputRows **补算 statusRows + thinkRows**——旧公式与 drawInputBox 的 topRow 不一致，streaming 时 spinner 直接画在正文上。
  - drawInputBox 的 thinking/ctx/queue 行序理顺为从边框向上紧密堆叠（旧公式 ctx 画在 thinking 上方且中间留空行）。
  - Markdown 表格列宽按可用宽度**比例收缩 + 超宽单元格截断 + 极窄终端丢尾列**（旧实现 5 列可达 166 列宽）。
  - 自动补全/工具卡头行/设置面板全部限宽；设置面板 padding 从 `len()`（字节）改 `visibleWidth`（中文对齐）。
  - **数据竞争修复**：handleKey/vim/历史/搜索等 80 处对 `inputBuf`/`cursor` 的无锁写统一收拢到带 `t.mu` 的 `setInput`/`moveCursor`（与 33ms 定时渲染 goroutine 的撕裂读 = 偶发乱码）。
  - 设置面板渲染后光标驻留面板内并隐藏（旧版光标停在内容区，conhost ECHO/IME 落点错位）。

### 测试

- 新增 `render_layout_test.go`：帧不超宽（5 宽度×超宽夹具）、输入框与内容区不重叠（5 状态组合）、补全限宽、表格限宽、**状态栏贴底矩阵（4 aux 组合：状态栏恒在 H 行、底边框恒在 H-1、ctx bar 恒在框上方）**、**布局变化全清屏（4 帧序列：首帧全量→增量→ctx 出现触发 2J→增量）**、**drawInputBox 清区（clearTop..H 每行清行序列全覆盖）**、**layoutStatusLine 分区裁剪（宽/中/窄/空四档：宽满铺、中丢尾保 head、窄兜底截断、空不 panic）**、**statusParts 分区归属 + ctx% 去重**。
- 新增 `keypump_test.go` 20 例：泵层 11 例（Delete 整包/拆包重组、SGR 滚轮整包、X10 大坐标直通、孤立 Esc、Alt+key、Esc 后紧跟汉字、IME 突发、conhost 扩展键、bracketed paste 开头、SS3 方向键）+ 消费端 9 例（drainStream：滚轮 SGR/X10 不泄漏不误中断、SS3 不泄漏、滚轮后孤立 Esc 仍可中断；PromptPermission：滚轮数字不误触决策、滚轮后真实按键仍生效、孤立 Esc 仍 deny；swallowCSI：6 形态序列单测 + 截断返回 0）；`pump_test.go` 7 例适配双 goroutine 结构（OneByteReader 逐字节拆分验证不误判）。
- 新增 `repro_wheel_test.go` 五变体复现矩阵（F12 取证驱动）：滚轮整包/拆帧 40ms × 流式(drainStream)/主循环(handleKey) + ESC 单发 60ms 幽灵中断——修复前"流式+拆帧"直接复现用户 dump 的 `64;34;29M` 乱码形态。
- 新增 `cmd/subsystem_check_test.go` 2 例。
- Go 全仓编译 + vet + 测试通过（52 包）。

## v0.1.1 — 对标落地六项 (2026-09-17)

基于 08-29 对标差距报告的核验结论（11 项中 9 项已落地、2 项缺失）与 4 项新识别体验差距，本轮全部补齐：

### TUI（对标 Claude Code）

- **bash 实时输出（LiveTail）**：`tool_progress` 事件不再被折叠卡吞掉——工具运行中在工具卡片下方滚动展示**尾部 5 行**实时输出（`⎿ …` 前缀 dim 样式），bash 完成后由完整结果接管。进度与结果改走独立 `LiveTail` 字段（64KB 尾部截断、不进会话内容），**修复进度+结果重复拼接 bug**。
- **任务完成响铃**：一轮对话耗时 ≥30s 且结束时输出 `\x07` 终端铃声（长任务切走不漏看）；`/bell` 命令开关并持久化，默认开启。
- **`/share` 单文件 HTML 分享**：会话导出为自包含 HTML（内嵌深色主题样式 + chroma 代码高亮），用户气泡/助手块/思考过程/工具卡片分层呈现；escape-first 防 XSS、外链协议白名单（http/https/mailto）；输出至 `~/.icode/shares/` 并附 OSC 8 可点击路径。`/export` 保留 Markdown 语义不变。
- **`/replay` 检查点时间轴**：`checkpoint` 列表 overlay（新→旧带步号），Enter 查看单步 diff，`r` 两次确认回滚到任意检查点（预览改动后执行），Esc 关闭；行模式降级为纯列表。
- **`icode doctor` 启动性能基准**：Bootstrap 分 7 阶段打点（config/storage/providers/engine/skills\/teams\/hooks/updater/scheduler），doctor 输出各阶段耗时表（≥500ms 标 ⚠️）。本机实测**冷启动共 38ms**。

### 桌面版（对标 Reasonix 任务审查流）

- **任务审查条（ChangedFilesBar）**：一轮对话中使用文件修改工具（write_file / edit / search_replace）后，在助手消息下方内联渲染审查条——改动文件 chips（basename + 悬停全路径，>6 个折叠计数）+「查看 diff」（复用检查点 DiffViewer）+「回滚本轮」（一步检查点还原，确认后自动收起）。三语（zh-CN / zh-TW / en）完整接入。

### 其他

- 版本号三端统一升 **0.1.1**（`version.go` / `desktop/package.json` / `vscode/package.json`）。
- 测试：Go 全仓 61 包通过（新增 LiveTail×4、Bell×1、Share×4、Replay×3、BootTimings×2）；桌面 vitest 105 例通过（新增 ChangedFilesBar×6：路径提取映射、chips 渲染、+N 折叠、diff 打开、回滚清标签、取消保留）。

## v0.1.0 — 版本基线 (2026-09-15)

### 本版变更

- **移除 UI 版（simpleui）**：删除 `main_ui.go`、`cmd/simpleui_windows.go`（132KB）、`cmd/simpleui_stub.go` 及 CI 独立构建作业。产品聚焦 **CLI（TUI）+ 桌面版（WebView2）+ VS Code 扩展** 三端形态，双击 `icode.exe` 保持进入增强 TUI。
- **版本号统一归零**：`version.go`（原 0.53.8）、`desktop/package.json`（原 0.2.0）、`vscode/package.json`（原 0.22.0）统一为 **0.1.0**。
- **CLI 打磨（对标 Claude Code）**：
  - `icode --help` 全面中文化并修正过时描述（"Electron" → WebView2 桌面版 + VS Code 扩展；口径统一为 14 家内置提供商）。
  - `icode version` 增加平台信息（`windows/amd64`）与配置文件路径。
  - `icode doctor` 增加环境诊断头（版本/平台/配置路径/默认模型），非 `--verbose` 时静音 bootstrap 进度日志，输出干净可解析。
  - 全部子命令 Short/Long 描述与 persistent flags 统一中文。
  - `icode desktop` 帮助修正为实际行为（双击默认进 TUI，桌面需显式命令或 icode-desktop.exe）。
- **桌面版打磨**：
  - BootSplash「正在启动…」、更新检查「当前版本/新版本可用」文案接入 i18n（zh-CN / zh-TW / en 三语）。
  - 关于页增加开源协议（Apache-2.0）、源码仓库与问题反馈链接。
- **修复：legacy conhost 下「输入文字跑到上方空白区」**（cmd.exe / PowerShell 传统窗口）：
  - 根因一：conhost 的 ANSI 绝对定位以**屏幕缓冲区**（默认 9001 行）为坐标系，而尺寸测量返回**视口**（约 30 行）——整帧内容画进滚动历史，物理光标悬在屏幕外，按键回显/IME 组字全部错位。现进入全屏 TUI 时把缓冲区压缩到视口高度（微软全屏控制台标准做法），坐标系归一。
  - 根因二：旧逻辑会把用户终端强制拉伸到 120×36（`SetConsoleWindowInfo` 失败时留下 236 行缓冲区 + 原视口的错位组合），且 conhost 在 resize 事件后会部分重置控制台模式、复活 ECHO 位。现**不再改动用户终端尺寸**（对齐 Claude Code），并以 150ms 周期重申 raw 输入模式（ECHO/LINE 关闭）。
  - 兜底：启动时检测输出端 VT 处理是否真正启用；不可用（Win10 1511 前的老 conhost）时自动降级为行模式——朴素但文字永不错位，不再全屏乱码。
- **TUI 输入框对标 Claude Code 二轮细化**：
  - **圆角边框输入框**：`╭─╮ / │ / ╰─╯` 全宽圆角框取代 `❯ ` 裸提示符，`[MULTI]` 模式提醒嵌入顶边框（框标题式样，不再挤占内容宽度），dim 配色与工具框视觉统一。
  - **横向滚动**：超长输入行以光标为锚点窗口化显示（右侧留 4 列余量 + `…` 前缀），打字越过行尾不再"盲打"——修复旧行为截尾后新字符不可见的问题；非光标行尾部锚定。
  - **终端标题**：进入 TUI 时设置 `icode · <目录>` 标签/窗口标题（OSC 0），退出恢复；对齐 Claude Code。
  - **`\` 续行**：行尾反斜杠 + Enter 插入换行而非提交（Claude Code 同款），长提示词拆行无需记住 Ctrl+J。
  - **占位提示轮换**：空输入时随机展示一条 "Try …" 风格操作建议（进程内固定，不闪烁）。
  - 翻页/置顶滚动步进与边框高度对齐（convHeight 基准 -4 → -6）。
- **文档整合**：CHANGELOG 浓缩为 0.1.0 基线；历史版本与审查报告归档至 `docs/archive/`；README 中英文版重写。

### 功能基线（整合自历史版本）

#### 多模型接入
- **14 家内置 LLM 提供商（60+ 模型）**：DeepSeek、智谱、Kimi、火山方舟、腾讯混元、华为盘古、SCNET、NVIDIA、Ollama、OpenRouter、Anthropic、Agnes、SenseNova + 任意 OpenAI 兼容端点（openai_compat，50+）。
- **一键自动更新模型**：三阶段更新管线（API 获取 → 内置元数据富化 → llms.txt 文档补充）；Diff 检测新增/下架，连续 2 次缺失才标记下架（防抖动误判）；刷新结果跨端通知。
- **厂商实时模型获取**：13 家厂商 `/models` 实时拉取 + 勾选启用（`EnabledModels` 过滤器）；清单为「厂商实时 + 内置目录 + 用户自定义」三方并集；非对话模型（embedding/语音/图像/审核）自动过滤；厂商自报上下文窗口与最大输出自动生效。
- **每模型生成参数覆盖**：temperature / top_p / max_tokens 按模型配置并真正下发（含显式 0），子代理继承；`/api/config/model` 与 `/api/config/key` 职责分离。
- **智能路由三级**：`keyword`（关键词）→ `embedding`（本地零 token 语义分类，默认）→ `llm`（最高保真，失败回退）；只增不减设计（置信度不足回退基线）。

#### 缓存优先 Token 优化（核心优势，最高节省 94%）
- **不可变前缀**（system prompt + 工具定义）+ **仅追加日志** + **易失草稿区**；DeepSeek 字节稳定前缀，Anthropic `cache_control` 标记。
- **五层压缩管道**（全活）：Snip（空轮滤除）→ Dedup（工具输出去重）→ Microcompact（跨轮折叠）→ Context Fold（早期消息摘要）→ Budget（硬性上限：read 50K / bash 30K / grep 20K / 全局 200K，头尾保留中间省略）。
- **技能懒加载**：不可变前缀只放紧凑索引，`use_skill` 工具按需拉取正文进易失区——技能数量与缓存命中率解耦。
- **多模态附件淘汰**：新用户轮只保留最近 2 个附件，更旧替换为 ~30 token 占位符；附件感知 token 估算（base64 字节数 ÷750）。
- **可视化**：TUI `/token` 命令 + 桌面 TokenBar「🪙 已节省」+ 分析页全局/会话双 Tab（按日趋势，落库 `session_stats`）。

#### 工具系统（38 个内置工具）
- 文件与命令：bash（含 `run_in_background` 后台任务）、read_file、write_file、edit、search_replace（SEARCH/REPLACE 块 + staged diff）、grep、glob、ls、disk_cleanup（跨平台）。
- 代码智能：code_search（CodeGraph 符号索引，懒构建）、LSP 诊断（项目语言自动探测，文件改后注入编译错误）。
- 智能体：task 子代理 + 多智能体团队（`team:<name>`，内置 team:review）；并行工具执行（只读并发 ≤4，写类串行）。
- 多模态：image_gen / video_gen（OpenAI 兼容后端）、voice 语音转写（百度/智谱/讯飞）、browser、computer-use。
- 交互：ask_user_question、ask_user_form、todo。
- **MCP 客户端**：JSON-RPC stdio + SSE 传输，工具自动注入（`mcp_<server>_<tool>`）；WorkBuddy 桥接（自动导入 `~/.workbuddy/mcp.json` + 技能目录互通）；信任模式（ask / readonly）强制执行。

#### 对话引擎
- 流式事件推送（text / thinking / tool_use / tool_progress / permission / system / done / error）；默认最多 25 轮工具循环（`tools.max_tool_rounds` 可配）。
- **续轮健壮性**：限流重试（消费 `Retry-After`）+ 备用模型降级 + CacheTTL 全程生效；doom-loop 熔断（per-session 隔离）；截断恢复。
- 生命周期 Hooks（15 种事件，`PreToolUse` 可阻断）；Headless JSON 输出（`--output-format json|stream-json`，CI 可用）；双层 Memory（项目 ICODE.md + 用户 `~/.icode`）。

#### 三端体验
- **CLI / TUI**（对标 Claude Code）：双栏欢迎屏（ASCII Logo + Tips）、输入补全（斜杠命令 + N-more 分页）、Ctrl+R 历史搜索、Shift+Tab 切模式、状态栏（模型/模式/上下文/todo/git 分支/回合计时）、思考过程动画、权限审批条、语音输入。
- **桌面版**（对标 WorkBuddy/Claude Desktop）：WebView2 原生窗口（Windows）/ 系统托盘 + 浏览器（macOS/Linux）；全局热键；多标签会话 + 工作区（跨路由持久化）；分屏对话 + Git 工作台；技能市场（内置 catalog + 本地导入）；Token 节省仪表盘；自动化模板库；首跑向导；开机自启/端口配置；自动更新闭环（检查/一键更新/重启）。
- **VS Code 扩展**：侧栏聊天 WebView（流式回复 + 工具卡片 + 权限批准）；选中代码右键询问/解释/优化；状态栏后端健康指示；binPath/serverPort/autoStart 配置。
- **ACP 协议**：stdio JSON-RPC 服务端（Zed / Neovim 等编辑器接入）。

#### 安全与隐私
- 4 级权限模式（Plan / Agent / YOLO / Auto）+ 分类器 + AllowedPaths 沙箱；分级授权兜底（连续拦截自动退回手动模式，`permission.strike_threshold`）。
- 6 级隐私脱敏（本地处理 → 脱敏 → 本地大模型 → 代理模式）；密钥 DPAPI 加密（Windows）/ 跨平台兜底。
- HTTP 服务仅 loopback 绑定 + 同源校验 + Host 守卫；SSE 无界写（长回合不被硬超时斩断）+ 心跳。

#### 生态与工程
- **技能系统**：SKILL.md 格式 + 懒加载索引 + 市场分发（安装/卸载/导入）+ WorkBuddy 互通。
- **知识库 RAG**：本地文档检索，IDF/BM25 加权排序。
- **调度器**：RRULE 秒级定时 + 模板库 + 执行历史 + 闲时任务（`/idle` 跨午夜窗口）。
- **办公文档**：零依赖生成 docx / xlsx / pptx（OOXML）。
- **72+ 斜杠命令**（TUI 与桌面共享 slashui 层）：`/help` `/model` `/mode` `/session*` `/rewind` `/checkpoint` `/share` `/replay` `/token` `/cost` `/usage` `/preset` `/zen` `/goal` `/kb` `/lsp` `/mcp` `/agents` `/skills` `/teams` `/hooks` `/bug` 等。
- **国际化**：zh-CN / zh-TW / en 三语完整覆盖（TUI + 桌面 + CLI 帮助）。
- **会话持久化**：SQLite（WAL + busy_timeout）；跨端共享同一历史；软删除回收站；摘要/lite-resume/预算护栏。
- **自更新**：跨平台资产映射 + magic 校验 + 原地换二进制 + 重启；CI 多平台构建（桌面 GUI 原生 runner CGO + 无界面 CLI 纯 Go 交叉编译，11 个资产自动发布）。

### 验证

- `go build ./...` / `go vet ./...` / `gofmt -l` 全部通过；`go test ./...` 全绿。
- 前端 `tsc --noEmit` 零错误；`vitest run` 99 项全部通过。
- `icode version` / `icode doctor` / `icode --help` 实际输出核验。

---

## 历史版本

v0.2.0 – v0.53.8 的完整更新日志（约 2100 行）已归档至 [docs/archive/CHANGELOG_HISTORY.md](docs/archive/CHANGELOG_HISTORY.md)。

历次对标审查报告已归档：

- [对标差距分析（2026-08-11）](docs/archive/对标差距分析_2026-08-11.md)
- [对标差距分析（2026-08-29）](docs/archive/对标差距分析_2026-08-29.md)
- [项目审查（2026-08-31）](docs/archive/项目审查_2026-08-31.md)
- [项目审查（2026-09-06）](docs/archive/项目审查_2026-09-06.md)
