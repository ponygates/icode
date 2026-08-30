# C3 · ACP（Agent Client Protocol）编辑器接入 设计文档

> 目标：`icode acp` 子命令启动一个 stdio JSON-RPC 服务端，让 Zed / Neovim 等任意 ACP 兼容编辑器接入 iCode——一份协议覆盖 N 个编辑器（对标 Reasonix `reasonix acp`）。
> 状态：设计定稿，待实施。工程量 3-5 天。

---

## 一、协议要点（已核实 agentclientprotocol.com）

- **传输**：本地 = JSON-RPC 2.0 over stdio（Agent 作为编辑器的子进程）；远程 HTTP/WS 仍在草案。
- **角色**：Agent = iCode（服务端，实现方法）；Client = 编辑器（调用 Agent 方法 + 实现权限/文件/终端方法）。
- **约定**：属性 camelCase、判别值 snake_case、JSON-RPC 2.0 信封（`jsonrpc/id/method/params/result/error`）、文件路径**必须绝对**、行号 1-based、用户文本 Markdown。
- **扩展**：`_meta` 加自定义数据；下划线前缀自定义方法；initialize 时声明能力。

### Agent 必须实现（baseline）
| 方法 | 作用 | iCode 映射 |
|---|---|---|
| `initialize` | 版本+能力协商 | 返回 `protocolVersion`、`agentCapabilities`（loadSession/modes/fs/terminal 按需） |
| `session/new` | 新建会话 | `sessionum.Create` → 返回 session id |
| `session/prompt` | 发提示，响应带 stop reason | `engine.Send` → 流式转 `session/update` 通知，结束回 stop reason |
| `session/load`（可选） | 加载会话 | `sessionum.Get`（需 `loadSession` 能力） |
| `session/set_mode`（可选） | 切换模式 | `gate.SetMode`（plan/agent/auto/yolo） |
| 通知 `session/cancel` | 取消进行中 | `engine.Stop(sessionID)` |

### Agent 发通知
| 通知 | 作用 | iCode 映射 |
|---|---|---|
| `session/update` | 消息块/工具调用/计划/命令/模式 | `StreamEvent`：`EventText`→message chunk；`EventToolUse`→tool call；`EventSystem/Error`→message；模式变化→mode update |

### Agent 可调 Client 方法
| 方法 | 作用 | iCode 映射 |
|---|---|---|
| `session/request_permission` | 请求工具授权 | `engine` 权限流：`EventPermission` → 调 Client 方法 → 回填 `permRespChans` |
| `fs/read_text_file` / `fs/write_text_file` | 文件读写（编辑器托管） | 可选能力，iCode 用内置工具也可 |
| `elicitation/create` | 结构化提问 | 映射 `ask_user_question` / `ask_user_form` |

---

## 二、实施拆解（P0 → P1 → P2）

### P0 · stdio 服务端 + 握手（1 天）
1. 新包 `internal/acp/`：
   - `transport.go`：stdio 读写 JSON-RPC（`jsonrpc=2.0`、id 关联、method 分发）。
   - `server.go`：`initialize` 握手 + 能力声明。
2. `cmd/acp.go` + Cobra 子命令 `icode acp`（不进 TUI，直接跑服务端循环）。
3. **验收**：`echo '{"jsonrpc":"2.0","id":1,"method":"initialize",...}' | icode acp` 返回能力对象；Zed 能列出 iCode 为可用 agent。

### P1 · 会话 + 流式（2 天）
1. `session/new` → 建会话；`session/prompt` → `engine.Send`。
2. `streamAdapter`：把 `<-chan StreamEvent` 转成 `session/update` 通知序列（text chunk / tool call / error）。
3. `session/cancel` → `engine.Stop`。
4. **验收**：Zed 里发提示，回复实时流式出现；取消可中断。

### P2 · 权限 + 模式 + 生态（2 天）
1. 权限：`EventPermission` → `session/request_permission`（同步等待 Client 返回 allow/deny → 回填）。
2. 模式：`session/set_mode` ↔ `gate`；`session/update` 播报模式变化。
3. VS Code 扩展打包发布 Open VSX（`vsce package` + `vsce publish`，独立于 ACP，可与 ACP 并行）。

---

## 三、关键风险与决策
- **阻塞式权限**：`session/request_permission` 需阻塞等待编辑器响应 → 复用 engine 现有 `permRespChans` 机制（已是 channel 同步）。
- **流式背压**：stdio 写阻塞时暂停读；用 `context` 传播取消。
- **能力协商**：第一版只声明 baseline（不声明 fs/terminal 能力，iCode 用内置工具），降低首版复杂度。
- **不引入重依赖**：JSON-RPC 手写（信封简单），避免拖入完整 MCP SDK。

---

## 四、验收准则（量化）
1. `initialize`/`session/new`/`session/prompt`/`session/cancel` 四个方法在 stdio 下往返正确（单测 mock 编辑器）。
2. Zed 能连接并完成一轮「提问 → 流式回复」。
3. 权限请求在编辑器侧弹窗，allow/deny 正确传导。
4. `_meta` 透传自定义字段（如 token 用量）不丢。
