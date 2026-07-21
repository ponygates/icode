# iCode — 项目上下文

## 项目描述

iCode 是一个多模型 AI 编码助手，可运行于终端和桌面。开箱支持 9 个 LLM 提供商、60+ 模型，缓存优先架构可实现最高 94% token 节省。Go 语言编写，使用 cobra CLI 框架，附带 Electron 桌面应用。

## 构建命令

- `go build -o icode .` — 构建 CLI 二进制（Linux/macOS）
- Windows 桌面版（双击无 CMD 黑窗）：`go build -ldflags="-s -w -H windowsgui" -o icode.exe .`，或直接用 `build.bat` / `install.bat`（已默认带该标志）
- `go build ./...` — 编译所有包
- `go test ./...` — 运行测试
- `go run . doctor` — 系统诊断
- `go run . chat` — 启动交互式会话
- `go run . exec -p "<prompt>"` — 非交互式单次执行

桌面端：
- `cd desktop && npm install` — 安装依赖
- `cd desktop && npm run dev` — 开发模式
- 或直接运行 `desktop.exe`

## 架构总览

```
main.go                  入口
cmd/                     Cobra CLI 命令（root, chat, exec, auth, doctor, server）
internal/
  app/                   App 启动和生命周期
  config/                配置加载 + 国际化
  core/
    agent/               子代理注册和运行
    checkpoint/          基于 git 的检查点/回退
    context/             项目上下文加载（ICODE.md）
    conversation/        智能体对话循环（引擎）
    permission/          权限门（Plan/Agent/YOLO/Auto 模式）
    privacy/             隐私脱敏（6级安全等级）
    session/             会话持久化
    slashcmd/            用户自定义斜杠命令
    todo/                待办事项存储
    tool/                内置工具注册表（read/write/bash/grep/glob/edit/task）
  db/                    SQLite 存储
  llm/
    provider/            提供商实现（anthropic, deepseek, zhipu, kimi,
                         volcengine, tencent, huawei, scnet, nvidia,
                         openrouter, openai_compat）+ 注册表
    tokenopt/            缓存优先 token 优化（不可变前缀、仅追加日志、智能压缩）
  mcp/                   MCP 客户端（JSON-RPC stdio 传输）
  server/                HTTP API 服务器（41KB，供桌面端调用）
  tui/                   终端 UI（71KB，全屏 raw 模式 + 后备行模式）
  types/                 共享类型定义
pkg/
  modelupdate/           模型列表更新
desktop/                 Electron 桌面应用 + web UI
configs/                 默认配置文件
```

## 设计原则

- **缓存优先**: 系统提示+工具定义组成不可变前缀，跨轮保持稳定，利用提供商 KV 缓存
- **多提供商**: 所有提供商实现 `types.Provider` 接口，统一注册
- **智能体循环**: `conversation.Engine` 流式推送事件（text/tool_use/done/error），内联执行工具，最多 10 轮递归
- **隐私优先**: 6 级安全等级（本地处理→脱敏→本地大模型→代理模式），从不发送遥测
- **双端统一**: CLI（TUI）+ 桌面（Electron）共享同一后端，会话数据存储在 SQLite

## 当前状态

### 已有功能
- 9 个 LLM 提供商（DeepSeek/Zhipu/Kimi/火山引擎/腾讯/华为/SCNet/OpenRouter/Anthropic/NVIDIA）
- 流式事件推送（goroutine + channel）
- 内置工具系统（read/write/bash/grep/glob/edit/task/子代理）
- 权限控制 4 模式（Plan/Agent/YOLO/Auto）
- SQLite 会话持久化
- 基于 git 的检查点/回退
- 缓存优先 token 优化
- 全屏 TUI（思考动画、上下文条、待办计数、安全徽章）
- HTTP API 服务器（REST + SSE 流式）
- MCP 客户端（stdio 传输，工具发现和执行）
- 子代理系统（Task 工具）
- 隐私脱敏 6 级
- 国际化（zh-CN/zh-TW/en）
- 20+ 斜杠命令
- 成本计算和仪表盘
- 桌面端 Electron 应用

### 与竞品差距
1. **SEARCH/REPLACE 编辑块**: 当前 edit 工具直接修改，没有 review+apply 流程
2. **智能模型路由**: 需要手动指定模型
3. **技能系统（SKILL.md）**: 未实现
4. **CodeGraph 源码索引**: 未实现
5. **多智能体编排**: 有基本子代理，无 Agent Teams

## 约定

- Go 1.26+，module path: `github.com/ponygates/icode`
- 包名匹配目录名（如 `conversation`, `tokenopt`）
- 公开类型/函数 PascalCase，私有 camelCase
- 所有文件读写和命令执行经过 tool 注册表，权限门审批
- 优先编辑已有文件，不创建文档文件除非明确要求
- 引用代码时使用 `file_path:line_number` 格式
- 保持回答简洁直接

## 文件位置

- `E:\icode\main.go` — 入口
- `E:\icode\internal\tui\tui.go` — 终端 UI
- `E:\icode\internal\server\server.go` — HTTP 服务器
- `E:\icode\internal\core\conversation\engine.go` — 对话引擎
- `E:\icode\internal\mcp\client.go` — MCP 客户端
- `E:\icode\internal\llm\provider\` — 9 个提供商实现
- `E:\icode\internal\types\types.go` — 核心类型定义
- `E:\icode\internal\core\tool\tools.go` — 工具系统
- `E:\icode\internal\core\tokenopt\optimizer.go` — token 优化
- `E:\icode\cmd\commands.go` — CLI 命令
- `E:\icode\desktop\` — 桌面端
