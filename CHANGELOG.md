# 更新日志

> **版本说明**：v0.1.0 是一次**版本归零与结构整理**——将历史迭代（v0.2.x–v0.53.x）的全部成果整合为统一的 0.1.0 功能基线。完整的历史版本记录见 [docs/archive/CHANGELOG_HISTORY.md](docs/archive/CHANGELOG_HISTORY.md)，历次对标审查与差距分析见 [docs/archive/](docs/archive/)。

## v0.3.4 — 沙箱（批次 5：Linux / macOS 后端 + 首次授权当场可见）(2026-10-17)

> 前三批只覆盖了 Windows。在 Linux 和 macOS 上，`mode: auto` 什么都保护不了——它老老实实说"没有后端"，但那意味着这两个平台的用户拿到的是一个只存在于文档里的安全承诺。这一版把后端补齐，并且把「首次授权」这件最容易被含糊掉的事摆到命令输出里。

### A · Linux：bubblewrap

- 新增 `internal/sandbox/platform_linux.go`。选 bwrap 而不是 Landlock，原因是 Landlock 只能收不能给：它没法在拒绝写入的进程上再开一个可写目录，而沙箱必须让工作区可写。bwrap 用一次挂载命名空间同时表达两半——`/` 只读、工作区 bind 成可写。
- 边界内容：`--ro-bind / /` + `--dev /dev` + `--proc /proc` + `--tmpfs /tmp`，工作区与工具链缓存在挂载后 bind 成可写，`--unshare-pid/ipc/uts` 隔开可见范围，默认 `--unshare-net`。`TMPDIR` 重指向私有 tmpfs，否则写入会落到沙箱内不可写的真实 `/tmp`。
- **不改任何文件权限**。Windows 必须往目录 ACL 里写 ACE，Linux 靠在进程自己的命名空间里表达同一条授权，所以这里没有授权记录可列、也没有东西需要撤销——这件事直接写在启动 notice 里，免得看过 Windows 版本的人以为也改了磁盘。

### B · macOS：sandbox-exec

- 新增 `internal/sandbox/platform_darwin.go`。SBPL 以 `(deny default)` 起步，放行进程、信号、sysctl、mach-lookup、共享内存这些"任何二进制启动都要用"的原语，再单独开写权限。
- 能做的：写限制在工作区 + 工具链缓存 + 用户的 0700 `$TMPDIR`；默认断网；`~/.ssh`、`~/.aws`、`~/.gnupg`、`~/.config/gh`、`~/.icode`、`Library/Keychains` 逐个 deny 读。
- **做不到的**：读不能整体限制。全盘放行再挖掉几个洞是唯一能让日常工具跑起来的形式，所以 notice 里明说"读视为不受限，只有凭据目录被单独拒绝"，而不是让表格看起来比实际更严。
- SBPL 的规则是**最后匹配者生效**，所以凭据 deny 必须写在 `(allow file-read*)` 之后。写反了不会报错，只会静默失效——测试断言的正是这条顺序，不只是"存在"。

### C · 自检决定可用性

- 两个后端都在构造时跑一次**真实调用**：用与运行时完全相同的构造逻辑跑 `/bin/sh -c "exit 0"`。探针通过才返回 runner；runner 非 nil 就等于可用，`Available()` 不再需要猜。
- 这抓的是两类只有真跑才暴露的问题：`bwrap` 不存在或**非特权 user namespace 被禁用**（Ubuntu 23.10 起的 AppArmor 限制），以及 SBPL 语法错误。用简化版探针会漏掉第二类——探针通过而真跑失败，比没有探针更糟。
- 自检失败的处理沿用既有语义：`auto` 降级并说明原因，`on` 直接拒绝执行 shell 命令。**没有一条路径会静默回退**。

### D · 首次授权，在命令输出里说

- 之前三批留下的最后一处含糊：默认 `mode: auto`，第一次在某目录跑命令时后端就写了 ACE，而用户既没被问、也没被告知，只能事后翻设置页才知道。
- 现在 `sandbox.RecordGrant` 把新授权记进一个待公告队列，`TakeNewGrants` 取出即清。bash 工具、后台任务、`!` 命令三条路径在执行点取一次，把一行「沙箱已为 X 添加访问权限，可在设置中撤销」写进**这条命令自己的输出**——并通过实时进度通道发一遍，因为 UI 渲染 bash 靠的是那条流而不是最终结果。
- 只在**真正新增**时公告；复用已有授权不重复。没写进记录文件的授权也不公告——那种情况本来就走 notice 报错了，再公告一次等于让用户去找一条查不到、也撤不掉的行。
- 启动失败不公告：`Start` 就失败时没有授权发生，多说一句只会让人以为磁盘被改了。

### E · 凭证卫生

- `.gitignore` 补上 `*.token`、`.netrc`、`.pypirc`、`credentials.json`、`gha-creds-*.json`、`*.pem`、`*.p12`（已确认不会误伤已跟踪文件）。
- 工作树中已无明文 PAT，仅测试夹具里的假样例（`ghp_abcdefghijklmnopqrstuvwxyz`）。但泄露仍留在两个历史提交（`93995c8`、`b772d7c`）里，**只能靠轮换使旧值失效**——清理历史会改写全部 commit hash，对已发布的仓库代价更大。轮换步骤见下方"待办"。

### 验证说明

- **Windows / Linux 后端已端到端验证**；**macOS 后端仅交叉编译 + 单元测试**（`GOOS=darwin` 编译与 vet 通过），尚未在 Mac 上实机跑过。README 已按此标注，不把未验证的东西写成已验证。
- 新增测试：Linux 6 项（只读根、工作区内可写、网络开关、TMPDIR 落在私有 tmpfs、不存在的可写路径被丢弃、命令分隔符位置）、macOS 7 项（deny default、写白名单、凭据 deny 的顺序、网络两态、路径转义、只列真实存在的凭据目录）、授权公告 2 项（只报一次、写失败不报）。
- `go build ./...`、`go vet ./...`、`gofmt`、`go test ./...` 全绿（Windows 侧）；`GOOS=linux` 与 `GOOS=darwin` 下 `go build ./internal/sandbox` 与 `go vet ./internal/sandbox` 均通过（全量交叉编译在 Linux 上仅卡在第三方 `systray` 的 cgo 依赖，与本次改动无关）。

## v0.3.3 — 沙箱（批次 4：`!` 命令范围 + 说清边界）(2026-10-17)

> 三个能到达 shell 的入口——bash 工具、后台任务、用户 `!` 命令——前两个已经封上，第三个一直开着，而且是**静默地开着**。最糟的不是它没受限，是「沙箱已启用」这句话让人以为它也受限了。这一版不是简单地把它关进去，而是让范围变成明确的、由用户决定的事。

### A · `!` 命令默认不受限，且明说

- 新增 `sandbox.confine_user_shell`，**默认 false**。理由不是偷懒：`!` 后面的命令是拥有这台机器的人亲手敲的，不是模型生成的。把它关进沙箱会直接破坏日常操作——`git push` 没网、`!code .` 起不了编辑器、`!cd ..` 之后什么都读不到。升级不该让 `!` 变成摆设。
- 真正的问题是不透明，所以：沙箱已启用而 `!` 不受限时，第一次执行 `!` 会打印一行说明，讲清"你自己的命令与模型发起的命令不同"、以及怎么改。**只提示一次**——每次刷屏会训练用户忽略它。
- 设为 true 后，`!` 与模型命令**完全同一条边界**：同样的 AppContainer、同样的网络策略、同样的可写范围。不放宽也不加严。

### B · 三个入口共用一套判定

- 新增 `internal/tui/shell_exec.go`：`shellArgv` 统一构造 argv（此前 raw 模式与 line 模式各写一遍平台判断，其中一处还在用 `os.Getenv("OS")` 猜 Windows）。`runUserShell` 统一决定走不走边界。
- 后端启动失败**不回退**，错误里明说"沙箱拒绝执行该命令"。三条入口同一条规则：会自己让路的边界不算边界。
- 空工作目录**拒绝执行**而不是继承父进程 cwd——继承来的目录对容器令牌没有权限，会失败在一个毫无指向性的错误上。
- 非零退出码转成 `exit status N`，与 `os/exec` 文本一致，转录看起来与未隔离时没有差别。

### C · 状态与设置

- `sandbox.StatusInfo` 新增 `confine_user_shell`，但**只在真的装了后端时才为 true**。记录了一个意图却没有边界，报成"已受限"正是沙箱本该消除的那种虚假保证。
- `sandbox.ConfineUserShell()` 走包级状态，由 `Init` 在 defer 里记录——所有提前返回路径（off / 不支持 / 失败）都会写上，不会有"设了但没记住"的分支。
- 设置页新增开关「用户命令（!）同样受限」，并在沙箱启用但该项关闭时显示一行警告，把缺口直接说出来。

### D · 验证

- Go 新增 8 项。TUI 层 6 项用假后端验证**路由决策**（默认不走后端 / 开启后走且传对 dir 与 argv / 非零退出文案 / 空目录拒绝 / 启动失败不回退），不依赖真实 ACL，因而不污染源码树；沙箱层 2 项覆盖 `Init` 记录与 `Status` 不越报；接口层 2 项覆盖持久化与默认值为 false。
- 前端新增 3 项：默认未勾选且显示缺口警告、勾选后发出 `{confine_user_shell:true}` 且**重启前警告仍在**（提示跟 runtime 走，不跟配置走）、无后端时不显示缺口警告。
- 全量 `go build` / `go vet` / `gofmt` / `go test ./...` 全绿；`tsc --noEmit` 零错误。



## v0.3.2 — 沙箱授权可见、可撤销（批次 3）(2026-10-17)

> 前两批把边界装上了，但留下一个说不过去的状态：为了在隔离里跑命令，后台会往项目目录的访问控制列表里写一条 ACE——这是**对用户磁盘的持久改动**，而用户既看不见，也撤不掉。一个只能通过 `icacls` 才能发现的改动，不能算"用户同意的改动"。这一版把它翻到台面上。

### A · 授权记录落盘

- 新增 `internal/sandbox/grants.go`，把「哪些目录被写过 ACE」记到 `~/.icode/sandbox_grants.json`。此前这份信息只活在 `acRunner.granted` 这个进程内 map 里，进程一退就归零。
- 写入用 tmp + rename，避免崩溃时留下截断文件——这份记录是"改过什么"的唯一证据，半个 JSON 等于没有。
- 记录损坏时**不阻断执行**，按空列表处理。丢了记录很糟，但因为簿记坏了就拒绝跑命令更糟；下一次授权会重新写入该条目。
- 路径比较跟随平台语义：Windows 大小写不敏感。否则用户按资源管理器里的写法撤销，会静默匹配不上。
- 记录写入失败不失败命令，改为在 `Status().notices` 里说明「已授权但无法记录」——ACE 已经在 DACL 里了，此时报错只会让用户以为命令没执行。

### B · 撤销链路

- `sandbox.Revoke(dir)` 先删 DACL 条目、再删记录。顺序反了会在 DACL 更新失败时留下一条"未记录的真实授权"，那正是本版要消灭的东西。
- 返回 `granted bool`，区分「确实删掉了」与「本来就没这条」，UI 不会替没发生的事邀功。
- 同步清空 runner 的内存缓存。这是最容易漏的一步：缓存里还写着"已授权"，下一条命令就会跳过 DACL 复查、以容器令牌直接启动，然后失败在一个毫无指向性的 access denied 上。
- 目录已被删除时直接从记录清理并返回 `revoked=false`。否则用户删了项目，设置页就留下一条永远清不掉、点了就报错的行。

### C · 接口与设置页

- 新增 `GET /api/sandbox/grants`（返回列表 + `revocable`）与 `DELETE /api/sandbox/grants`（body 带 path）。`revocable` 由平台决定，避免在未实现撤销的平台上摆一个只能失败的按钮。
- 设置页新增「已授权的目录」：逐条显示完整路径、撤销需二次确认、撤销失败保留该行并报错（不假装成功）。
- 从 `off` 切回 `auto`/`on` 时弹确认框，明确写出"会向工作区目录写入一条访问控制条目、可在下方撤销"，取消则不发请求。这是唯一会开始产生新 ACE 的转折点，值得先问一次。
- 三语言文案齐备。

### D · 验证

- 新增 13 项 Go 测试。记录层 8 项：往返、幂等（`ensureAccess` 每条命令都跑，记录不能跟着增长）、只删目标条、平台路径比较、损坏文件、缺失文件、空路径拒绝、已删除目录清理。接口层 3 项。真机 ACL 端到端 2 项：撤销后 DACL 里确实没有容器 SID、**下一条命令能重新授权**（这一条专门守着缓存失效）、从未授权的目录返回 false。
- 前端新增 11 项：列表渲染、空态、二次确认不误发请求、撤销后移除行、`revoked=false` 时不谎称撤销、失败保留行、不可撤销平台禁用按钮、确认框出现/取消/确认三条路径、非关闭态切换不打扰。
- `go build` / `go vet` / `gofmt` / `go test ./...` 全绿；`tsc --noEmit` 零错误；前端 **231 项 / 23 文件**全绿。


## v0.3.1 — 操作系统级沙箱（批次 2：后台任务接入 + 设置页状态）(2026-10-17)

> 批次 1 只封住了前台 bash。`run_in_background=true` 的后台任务仍走 `exec.Command`，跑的是完整用户权限，而且它比前台更容易出问题——它脱离了 agent 循环，进程能一直活着。这一版把这条口子关上，并且让用户能在设置页看见边界到底装没装上。

### A · `bgtask` 接入沙箱

- `bgTask` 现在二选一持有进程：`*exec.Cmd`（无沙箱）或 `sandbox.Handle`（有沙箱）。两者不是同一类型——Go 的 `SysProcAttr` 无法表达 `SECURITY_CAPABILITIES`，所以没有能覆盖两者的单一类型。
- 沙箱内后台任务的输出经 `Spec.OnOutput` 写进**同一个 1 MiB 环形缓冲**，`task_output` 看到的分帧与前台一致。
- `launch` 失败**直接返回错误，不回退到无沙箱执行**，错误文案明确说"为避免绕过隔离已取消执行"。后台任务是最容易逃出 agent 视野的地方，恰恰不能在这里"看情况让路"。
- `wait` 把后端的非零退出码还原成 `exit status N`，与 `os/exec` 的错误文本一致，UI 与既有调用方无需区分两条路径。
- `killProcess` 统一终止语义：有句柄走 `Kill()`，否则 `Process.Kill()`。`cancelTask` 显式杀进程**而不是依赖后端监听 ctx**——"停止就是停止"不该取决于某个后端有没有接好 context。
- 抽出 `resolveConfinedDir`，前台与后台共用。空目录以前靠继承父进程 cwd 侥幸可用；沙箱里继承的目录对容器令牌没有权限，`CreateProcess` 会报"目录名无效"，所以必须显式解析。

### B · 沙箱状态进入设置页

- 新增 `GET/PUT /api/sandbox`。GET 同时返回 **runtime（此刻真正生效的）** 与 **config（下次启动生效的）**，因为两者会不一致：`auto` 在无后端的平台上存的是 auto 却按无隔离运行；改了模式但没重启也是不一致。
- PUT 校验 mode 取值、归一化 network（非法值落为 deny，而不是原样回显）、并返回 `restart_required`。沙箱在启动时构建（profile、DACL 授权、私有临时目录都无法热替换），所以不假装改动即时生效。
- 设置页「安全沙箱」区块从纯说明文字换成真实状态：徽标按 runtime 判定（配置了但没装上显示警告色，绝不显示绿色）、后端名、模式切换、重启提示、以及 `Init` 的降级原因列表。
- `sandbox.Status()` 在 `Init` 内以 defer 记录，任何提前返回路径都会写状态——避免报出一个并未安装的边界。

### C · 验证

- 新增 9 项 Go 测试：后台任务确实走沙箱（断言句柄存在且无 `exec.Cmd`）、读工作区外文件被拒、写工作区正常、`kill=true` 真能终止沙箱内进程；接口层 4 项覆盖运行时/配置分离、非法 mode 拒绝、保存后要求重启、network 归一化。
- 新增 5 项前端测试：启用态与后端名、未启用态不谎称、notices 展示、保存后提示重启、保存失败不静默。
- 前端 **220 项 / 23 文件**全绿；`go vet` / `gofmt` / `go test ./...` 全绿。

## v0.3.0 — 操作系统级沙箱（批次 1：Windows AppContainer + bash 工具接入）(2026-10-17)

> 审查报告 P0-1 的落地。此前 bash 的全部防护都在**进程创建之前**：权限门只看命令字符串，23 条正则追不上构造型命令，`exec.Command` 跑的是完整用户权限。这一版把边界挪到内核，命令能不能通过规则变得无关紧要——越界操作由操作系统直接拒绝。

### A · 新增 `internal/sandbox`

- 平台无关核心：`Mode`（auto/on/off）、`NetworkPolicy`（deny/allow）、`Spec`/`Result`/`Handle`/`Runner` 接口、`Init` 统一探测与降级、包级 `Active()`。
- **降级语义**：`auto` 不支持就告警后继续；`on` 不支持则**拒绝执行**；`off` 显式关闭并提示。`Init` 返回 notices 由调用方展示，不做静默降级。
- `ParseMode` 对未知值回退 `auto`——配置写错不能悄悄把沙箱关掉。

### B · Windows AppContainer 后端（`appcontainer_windows.go`）

- 自建 `CreateProcessW` + `STARTUPINFOEX`（Go 的 `os/exec` 不支持 `SECURITY_CAPABILITIES`），一次 profile 创建、全程复用。
- 工作区按需授权：读取现有 DACL → `SetEntriesInAcl` 合并 → `SetNamedSecurityInfo` 写回，**保留原有全部 ACE**（测试用 SID 集合比对守住这条）。
- 子进程环境：注入独立临时目录（`%TEMP%\icode-sandbox`）+ `TMP`/`TEMP` 重定向，**不授权整个 `%TEMP%`**——否则沙箱能读其他进程的临时文件。
- 取消语义在 backend 内实现：`ctx.Done` 直接 `TerminateProcess`，调用方只管 `Wait`。

### C · 三个只有实测才会发现的问题

1. **`SECURITY_CAPABILITIES.Reserved` 是 `DWORD`**。写成 `uint64` 让结构体从 24 字节变 32，`UpdateProcThreadAttribute` 返回 `ERROR_INVALID_PARAMETER`（"参数错误"），完全指不出原因。
2. **传 UTF-16 环境块必须同时置 `CREATE_UNICODE_ENVIRONMENT`**。否则系统按 ANSI 解析，每个条目都解码成空名，`CreateProcess` 报 `ERROR_ENVVAR_NOT_FOUND`（203）。
3. **AppContainer 打不开 `NUL` 设备**。`\Device\Null` 的 DACL 只有 `Everyone / SYSTEM / Administrators / Restricted Code`，没有 AppContainer 组；而 Go 工具链计算 build ID 时会 `open NUL`，导致 `go build` 直接失败。已通过给该设备补一条容器 SID 的 ACE 解决（设备不存数据，不扩大实际暴露面）；DACL 不可写时降级为"沙箱内不能构建"，并在 notices 里说明。

另外两个 Go 陷阱：`envBlock` 的 `*uint16` 转 `uintptr` 后必须 `runtime.KeepAlive`，否则 GC 可能在 `CreateProcess` 期间回收环境块（表现为子进程卡死）；`ConvertSidToStringSidW` 在 advapi32 而非 kernel32。

### D · bash 工具接入

- `BashTool.Execute` 在 `sandbox.Active() != nil` 时走 `runConfined`：同样的超时上限、输出截断、流式进度，只有边界不同。
- **启动失败不回退到无沙箱执行**——静默降级会让沙箱看起来存在、实际可选，比没有更糟。

### E · 配置

```yaml
sandbox:
  mode: auto            # auto | on | off
  writable_paths: []    # 额外可写目录（工作区与 Go 缓存自动处理）
  network: deny         # deny | allow
  grant_workspace: true # 允许为工作区写入 ACE
```

### F · 实测结果（本机 Windows 10 Pro / x64）

| 用例 | 结果 |
|---|---|
| `type %USERPROFILE%\icode-sandbox-probe.txt` | 拒绝 ✅ |
| `dir %USERPROFILE%` | 拒绝 ✅ |
| `echo x > C:\Windows\Temp\...` | 拒绝 ✅ |
| 工作区写文件 / 读授权前已存在的子文件 | 正常 ✅ |
| `echo hi > NUL`、`type NUL` | 正常 ✅（补 ACE 后） |
| `go build` 一个最小 module | 成功产出二进制 ✅ |
| `Kill()` 终止 `ping` 长任务 | 立即退出 ✅ |

测试：新增 **19 项**（沙箱包 16 + bash 工具端到端 3）。

### G · 未完成（明确列出）

- **后台任务（`bgtask`）与用户 `!` 命令尚未接入**。前者在批次 2，后者按设计不沙箱（用户自己敲的命令，威胁模型不同）。
- **Linux / macOS 后端未实现**，`newPlatformRunner` 在非 Windows 返回不支持——本机无法验证，不写未验证的代码充数。
- 设置页尚无沙箱开关与后端状态显示，当前只能改 YAML。
- 首次启用会向项目目录写入一条 AppContainer ACE（可撤销，`sandbox.RevokeWorkspace` 已提供，尚未接到 UI）。

## v0.2.2 — ChatPage 拆分第二批：Composer 独立 + 键盘行为测试 (2026-10-17)

> 承接 v0.2.1 的拆分。上批只搬了「没有 JSX」的传输层，这批把输入区整体搬成受控组件，并补上此前一条都没有的键盘行为回归测试。

### A · `Composer` 独立成组件

- 输入区（附件预览 / ctx 进度条 / 文本框 / 语音 / 发送停止 / 模式行 / token 行 / 计划确认条）从 `ChatPage.tsx` 移入 `components/Composer.tsx`。
- **边界选择**：输入历史、slash 菜单高亮索引、浏览前草稿留在组件内部——它们只描述"打字手感"，页面从不需要读；附件、流式标志、语音、计划确认保持受控——发送路径要读附件，语音还持有 `AudioContext` 与后端调用。
- `filterSlash` / `slashSel` / `handleKeyDown` 从页面移除，键盘规则（Enter、Shift+Enter、Tab 补全、↑↓ 历史、Tab 切模式、@ 唤起选择器）集中在组件内。
- 页面侧净减 325 行（2,345 → 2,020），`Composer` 477 行。

### B · 修掉拆分过程中暴露的一个真实回退

- 原页面注释写着「Allow image-only sends: a pasted screenshot with no caption is valid」，但发送按钮的 `disabled` 只看文本，**只有附件的状态下按钮是灰的**，只能靠 Enter 发送。
- 初版 `Composer` 把 `submit` 写成空白即返回，导致"粘贴截图 + 直接回车"彻底失效——由测试 `sends an image with no caption` 捕获。
- 现以 `canSend = 文本非空 || 有附件` 统一门控 Enter 与按钮，图片无文字也能发。

### C · 测试

| 范围 | 条数 | 覆盖 |
|---|---|---|
| 发送 | 6 | 空白拦截、Enter 提交并清空、Shift+Enter 换行、按钮态、↑ 历史召回 |
| 流式与语音 | 3 | 发送↔停止切换、`aria-pressed` 跟随、转写中禁用麦克风 |
| 附件 | 4 | 图片内联预览、PDF 名称卡片、精确删除、无文字图片可发 |
| slash 与模式 | 3 | `/clear` 补全、Tab 循环模式、`@` 唤起选择器 |
| 计划确认 | 2 | 条件渲染、接受/放弃回调 |

前端测试总数 197 → **215**；`ChatPage.tsx` 与 `Composer.tsx` 合计 2,503 行（拆分前单文件 2,477，净增的是 props 声明与注释，用于换取可测试性）。

## v0.2.1 — ChatPage 拆分第一批：传输层与后端发现独立成模块 (2026-10-17)

> v0.2.0 的「未完成」表里写着「先抽 `useChatStream`/`MessageList`/`Composer`，配 10 条关键测试」。本版完成其中**风险最低、覆盖收益最大**的一块：把流式传输与后端发现从 2,477 行的页面里搬出来，并补上此前完全缺失的测试。

### A · SSE 传输独立为 `lib/chatStream.ts`

- `streamChat` / `sessionStreams` / `encodeWav` / `estimateCost` 原本是 `ChatPage.tsx` 末尾的模块级函数，**无法在不挂载整页的情况下测试**。
- 拆帧规则（一帧被 TCP 切成两半、一帧多条、注释行、空的 `data:`）、空流保护、Abort 语义（用户停止 vs 超时）、120s 超时，现在各有独立用例。
- 新增 `abortSessionStream(sessionKey)`，把「标记 userStopped 再 abort」收敛到一处；页面里的停止按钮改为调用它，不再直接操作 Map 内部结构。

### B · 后端发现独立为 `lib/backendDiscovery.ts`

- 原逻辑内联在发送路径里：先试同源 `/api/health`，再按 `57356/57357/8080/3000/9090` 顺序探端口，失败静默。
- 抽出 `discoverBackend({ explicit, origin, fetchImpl })`，返回 `{ url, source }`。`file://` 与 `"null"` 来源不再发起注定失败的请求；显式配置的 URL 不发任何探测请求。
- `fetchImpl` 可注入，测试不需要起真服务。

### C · 类型下沉

- `ChatEvent` / `ChatPayload` / `UsageInfo` / `PlanInfo` / `PermissionRequest` 从 `ChatPage.shared.tsx` 移到 `lib/chatTypes.ts`，传输层不必为了命名一个 payload 去 import 一个 `.tsx`。
- `ChatPage.shared.tsx` 保留同名 re-export，现有 import 路径全部不变。

### D · 收益

| 指标 | 前 | 后 |
|---|---|---|
| `ChatPage.tsx` | 2,477 行 | 2,345 行 |
| 涉及模块 | 单体 | 4 个（页面 + 类型 + 传输 + 发现） |
| 新增测试 | 0 | **20 条**（流式 15 / 发现 5） |
| 前端测试总数 | 177 | **197** |

未动 `MessageList`（上一轮已拆）与 `Composer`：输入框与附件队列深度耦合着 JSX 与语音状态，拆它需要连同渲染一起改，留待下一批单独做。

## v0.2.0 — 第五轮审查落地：PDF 输入 / 模型主动规划 / FTS5 检索 / 多语言索引 / 资源上限 (2026-10-17)

> 依据 [docs/项目审查_2026-10-17.md](docs/项目审查_2026-10-17.md) 的只读审查结论，优先落地「链路断裂」与「工程健康」两类问题。操作系统级沙箱、可交互浏览器、前端拆分、任务级评测仍待专项批次（理由见报告）。

### A · 多模态输入：PDF 不再是死链路

- **根因**：`types.Attachment` 早就声明了 `"pdf"`，但 `openai_compat` 与 `anthropic` 的编码函数都只有 `type == "image"` 分支——PDF 被**静默丢弃**，模型回答时完全不知道有附件。
- **Anthropic 原生读取**：PDF 附件编码为 `{"type":"document","source":{"type":"base64","media_type":"application/pdf"}}`，模型可直接读正文与页面。
- **OpenAI 兼容端点**：Chat Completions 的 `file` part 支持因厂商而异，贸然发送会让整条请求 400；改为发送**可见文本说明**「附件：xxx.pdf（当前模型端点不支持内联 PDF，内容未能读取）」，让模型与用户都知道发生了什么。
- **桌面入口**：附件按钮此前是**空按钮**（无 onClick）。现接入隐藏文件选择器，支持图片与 PDF 多选；粘贴也接受 PDF；PDF 以文件卡片（图标 + 文件名）展示而非破图。
- **`read_file` 明确拒绝 PDF**：不再把二进制塞进上下文产生乱码，改为提示「作为附件发送或先转文本」。
- 新增 `Attachment.Name` 与 `Attachment.IsPDF()`；服务端契约同步 `name` 字段。

### B · 模型主动进入计划模式（Claude Code EnterPlanMode parity）

- 新增 `enter_plan_mode` 工具：模型判断需求较大/有歧义时，可自主切到只读模式，调研后输出计划，等用户接受再执行——此前只能由用户手动 `/plan`。
- 引擎在 hooks/权限之前拦截该工具（切到**更少**权限不应再走审批），切换 `gate` 并发出新的 `mode_changed` 事件。
- 桌面收到事件后直接更新模式徽章（不再回写 API）；TUI 显示系统提示。已有的「接受计划 / 放弃」确认条、接受后自动继续执行保持不变。
- 子代理调用该工具会得到明确错误，不会偷偷改父会话模式。

### C · 会话检索升级为 FTS5

- 新增 FTS5 外部内容索引 `messages_fts`（`content='messages'`，触发器同步增删改），**trigram 分词**——`unicode61` 会把整句中文当成一个 token，中文搜索等于不可用。
- `SearchMessagesFiltered` 支持 `session_id` / `role` / `from` / `to` 过滤与 `bm25` 相关性排序；短于 3 字的查询自动回退 LIKE（trigram 限制）；FTS 不可用时整体回退 LIKE，搜索永不被索引拖挂。
- 首次启动一次性 `rebuild` 回填历史消息，之后由触发器增量维护；`/api/search` 接受新增过滤参数。

### D · 后台任务资源上限

- 并发上限默认 8：超限返回可操作错误（提示用 `task_output kill=true` 结束一个），避免模型连续起构建/日志任务耗尽小内存机器。
- 保留最近 16 个已完成任务（`task_output` 仍可读），更老的自动回收——此前每个任务最多 1 MiB 缓冲会随进程寿命无限累积。
- 取消测试的等待窗口从 5s 放宽到 15s：`taskkill /T` 要走进程树，满载机器上 5s 会假失败。

### E · 多语言符号索引

- CodeGraph 从 Go + TS/JS 扩展到 **Python / Rust / Java / Kotlin / C#**（`.py/.rs/.java/.kt/.cs`）。
- Java/Kotlin/C# 的函数正则要求明确的修饰符 + 返回类型，并显式排除 `return/if/for/while/switch/catch/...`，避免把普通调用索引成定义。
- 更新了原先「`.py` 不产出符号」的断言，改为正向覆盖。

### F · 文档真源对齐

- `ICODE.md` 版本号从过期的 v0.1.0 修正为 v0.2.0。
- README 路线图与 `ICODE.md` 竞品差距节此前把 CSRF/SSRF/目录沙箱/注册表锁/密钥脱敏列为「下一阶段」，实际 v0.1.3 已全部落地——已改为「已完成」，并把真正剩余的 **bash OS 级沙箱**单列为 P0。
- 新增 `docs/项目审查_2026-10-17.md`（第五轮审查报告）。

### 未在本版完成（明确记录，不虚报）

| 项 | 原因 | 建议 |
|---|---|---|
| bash 操作系统级沙箱 | 三平台内核接口差异大，且本机无法验证 macOS/Linux；半成品沙箱比没有更危险 | 独立分支，Linux Landlock → macOS Seatbelt → Windows AppContainer 逐个验收 |
| 可交互浏览器（CDP） | 需引入 WebSocket 依赖并真机验证点击/填表 | 单独批次，保留 dump 兜底 |
| 前端巨型组件拆分 | ChatPage 2,434 行，拆分回归风险高 | 先抽 `useChatStream`/`MessageList`/`Composer`，配 10 条关键测试 |
| 任务级评测体系 | 需要真实仓库任务与验收脚本，设计成本高于代码 | 先做 5 个种子任务跑通链路，再扩到 20–30 |
| 金额级花费上限 | 需要跨会话日聚合与 UI 入口，否则是死配置 | 与设置页一起做 |
| 结构化日志 / OTel | 改动面大 | 先做本地滚动日志 + 回合 span |

## v0.1.4 — 五批次落地：技能生态市场化 / 多会话协作 / 流式健壮性 / 桌面后台任务视图 (2026-10-06)

> 依据竞品研究（Claude Code v2.1.289 / opencode v2 / Reasonix / WorkBuddy）制定五批次方案并全部落地：A 生态市场化 → B 多会话协作 → C 健壮性 → D 体验 → E 质量债。全项目 57 包测试绿。

### A · 技能生态市场化

- **技能 Category 分组**：SKILL.md frontmatter 新增 `category`（coding / office / general），市场页按分类稳定分组展示。
- **保险技能剥离**：保险话术/异议处理等 5 个垂直技能迁至 `industry-packs/insurance/`，与「开源编码助手」定位解耦（改进清单 #20）；产品决策记录于 A 批报告。
- **远程技能源**：`POST /api/skills/source/list` 探测 GitHub repo / SKILL.md 直链（marketplace.json / skills 目录自动发现），`source/install` 一键安装；`/api/skills/sources` 管理已存源（GET/POST/DELETE）。
- **零成本市场索引**：skills 包新增 GitHub 免鉴权索引（`ghAPIBase`/`ghRawBase` 可测钩子），支持 `GITHUB_TOKEN`/`GH_TOKEN` 提额。
- **`/skill-doctor` 体检**（TUI）：加载路径 / 分类缺失 / 触发词冲突逐项检查。
- TUI i18n en 段补齐历史未翻译项。

### B · 多会话协作

- **后台任务可取消**：`task_output` 的 `kill=true` 现对子代理（agt-N）同样生效（原仅 shell bg-N）；新增 `CancelAgentTask`，取消态与失败态明确区分（不再把用户取消误报为 failed）。
- **TUI `/tasks` 增强**：运行计数摘要（N 个代理 + M 条命令）+ 运行中时提示可用 `task_output kill=true` 取消。
- **团队黑板通信**（B3，默认关闭）：`team:` 多代理编排升级为两阶段——specialist 并行产出写入黑板 → reviewer 注入同侪输出评审 → leader 汇总评审结论。`TeamRunner` 依赖抽象为 `memberRunner` 最小接口，团队测试从零覆盖（team_test.go，4 用例）。默认团队 `team:review` 的 security 角色回归 specialist 以保持既有默认行为，reviewer 黑板为 opt-in。

### C · 流式健壮性

- **断流保留半截输出**：Send / continueAgentLoop 主循环收到 `EventError` 时先 `persistPartialTurn` 落盘，已生成内容非空则追加「已保留，输入『继续』可从断点接着生成」提示——断流不再丢半截回答。
- **流式超时模型修正**（openai_compat 全家 + anthropic 双修）：根因是 `http.Client.Timeout` 属**含流式 body 读取的整请求超时**，长回答 2 分钟被误杀。改为 `Timeout: 0` + `Transport.ResponseHeaderTimeout`（首字节等待保留）+ 非流式 `Chat()` 用 ctx 显式超时 + **120s 流式空闲 watchdog**（`streamIdleTimeout` 包级 var，AfterFunc → 友好错误 → `body.Close()` 解锁 scanner；done chan + atomic 防重复上报）。
- **修复 scanner 吞错**：连接中断时 `scanner.Err()` 一律上报（原 `err != nil && !eventsProduced` 条件会静默吞掉有输出后的错误）。

### D · 桌面后台任务视图（Agents）

- **后端**：`GET /api/bgtasks` 返回结构化快照（`tool.BgTaskInfo`：id/kind/status/elapsed/label/brief/tokens/tail，rune-safe 尾部截取）；`POST /api/bgtasks/{id}/cancel` 收进 Bearer token 门禁（与 /api/shell 同级防护）。shell 侧补 `cancelled` 标志，取消不再误显示为失败。
- **桌面**：新增 `/agents` 页面（Sidebar 🤖 入口）——5 秒轮询、四色状态徽章、耗时格式化、输出尾部点击展开、运行中一键取消、空态引导；三语言 i18n。
- 远程控制（`server.remote_listen` 手机页 + token 门）经侦察确认已有，未重复建设。

### E · 质量债收口

- `desktop/package.json` 增加 `pnpm.onlyBuiltDependencies: ["esbuild"]`（pnpm v10+ 默认拦截依赖构建脚本，vite 构建依赖 esbuild postinstall）；修复 description 等 3 处历史 GBK 转码乱码（`鈥?` → `—`）。
- skills `Load()` 目录内并发读取（`sync.WaitGroup`，结果按序合并，排序保持确定性）。
- 文档对齐（改进清单 #15/#17/#23）：ICODE.md 热键改 `Ctrl+Shift+Space`（代码实况）、vscode/README vsix 版本对齐 `0.22.0`、acp_design.md 状态改「baseline 已落地」。
- 巨型文件拆分（ChatPage 107KB / engine.go 112KB 等，改进清单 #2/#6）**本轮明确不做**：改动面与回归风险大于收益，留待专项批次。

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
