# iCode for VS Code

在 VS Code 侧栏里直接使用 [iCode](https://github.com/ponygates/icode) 多模型 AI 编程助手。本扩展**不重新实现模型对话**，而是作为本机已运行的 iCode 后端（`icode server` / `icode desktop`）的一个轻量前端：聊天请求经扩展进程代理到 `http://127.0.0.1:<port>/api/*`，因此复用 iCode 的全部能力（多模型路由、超省 token 的 Cache-First、技能、工作区等）。

## 功能

- 活动栏 **iCode** 图标 → 侧栏聊天视图（原生 UI，零框架）。
- 会话列表 / 新建会话 / 切换会话（复用本机 iCode 会话存储）。
- 模型下拉（从后端 `/api/models` 实时拉取）。
- 流式回复（SSE 直传），支持思考过程、工具调用卡片、交互式权限批准。
- 自动发现后端：优先用设置 `icode.serverPort`（配合后端 v0.20 固定端口），其次读 `%TEMP%/icode/port`（mac/Linux 为 `$TMPDIR/icode/port`），否则探测 `57356 / 8080 / 3000`，仍不可用则尝试自动启动 `icode server`（可用 `icode.autoStartBackend=false` 禁用）。
- **编辑器集成（v0.22 新增）**：选中代码后右键 →
  - `iCode: 询问选中代码` —— 预填带 `文件:行号` 与语言围栏的提示词，可编辑后发送；
  - `iCode: 解释选中代码` / `iCode: 优化选中代码` —— 自动发送。
- **状态栏（v0.22 新增）**：右侧常驻 `⚡ iCode :端口`（已连接）/ `⊘ iCode`（未连接，黄色警示），每 15s 健康检查，点击打开侧栏。
- **设置项（v0.22 新增）**：`icode.binPath`（自定义可执行文件路径）、`icode.serverPort`（固定后端端口，0=自动发现）、`icode.autoStartBackend`（默认 true）。
- 命令面板：
  - `iCode: 打开聊天侧栏`
  - `iCode: 启动后端服务`
  - `iCode: 在终端启动 CLI（完整 TUI）` —— 打开集成终端运行 `icode`，获得完整增强 TUI 体验。
  - `iCode: 打开设置` —— 跳转到 VS Code 设置中的 iCode 配置项。
  - `iCode: 询问/解释/优化选中代码`

## 前置条件

- 已安装 iCode CLI（`icode` 在 PATH 中，或位于 `~/go/bin`、`~/.local/bin`、`/usr/local/bin` 等）。
- 后端未运行时，扩展会尝试自动 `icode server`；也可手动 `icode server` 后刷新。

## 从源码构建 / 安装

```bash
cd vscode
npm install
npm run compile        # 输出到 dist/extension.js
```

用 VS Code 打开本仓库根目录，在扩展开发宿主（F5）中运行，或在 `.vsix` 打包后安装：

```bash
npx @vscode/vsce package   # 需安装 vsce
code --install-extension icode-vscode-0.22.0.vsix
```

## 架构

```
VS Code WebView (sidebar.html, 原生 JS)
   │  acquireVsCodeApi() postMessage
   ▼
extension.ts (Node 进程)
   │  http -> 127.0.0.1:<port>
   ▼
iCode 后端 (/api/chat SSE, /api/sessions, /api/models, /api/permission/respond ...)
```

WebView 与扩展通过 `postMessage` 双向通信；扩展用 Node `http` 模块代理 REST 与 SSE，绕开浏览器跨域 / X-Frame 限制，无需在后端额外开放 CORS。

## 已知限制

- 本扩展在**无头构建环境仅做了 TypeScript 编译验证**，尚未在真实 VS Code 中加载点测；首次在真机使用时请确认 `icode` 后端可被发现。
- 文件选择等依赖 Electron 原生桥（`window.icode`）的能力在 VS Code WebView 中不可用；需要完整桌面能力时请使用 `iCode: 在终端启动 CLI` 或独立桌面版。
