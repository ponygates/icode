# iCode 项目记忆

## 项目概况
- 名称: iCode — 多模型 AI Coding Agent
- 仓库: github.com/ponygates/icode
- 技术栈: Go (CLI) + Electron/React/TypeScript (桌面版)
- 协议: Apache-2.0
- 开发者: 独立开发者 (ponygates)

## 核心差异化
1. 多模型聚合平台 — 国内(DeepSeek/智谱/Kimi/华为/火山方舟/腾讯/SCNET) + 国外(OpenRouter/Anthropic/OpenAI) 50+ 提供商一键更新
2. Token 节省机制 — 借鉴 Reasonix 的 Cache-First Loop (immutable prefix + append-only log + volatile scratch)，扩展为跨 Provider 通用方案
3. 中文原生体验 — zh-CN/zh-TW/en 简繁双语界面完整支持
4. 跨平台双端 — CLI TUI + Electron 桌面版同步开发

## 参考项目
- Reasonix (shengcanxu/Reasonix): Prefix-cache 机制核心参考，94%缓存命中率
- OpenCode/Crush (charmbracelet): Go + Bubble Tea 架构参考，已归档
- CodeWhale (Hmbown/CodeWhale): Rust 实现，多模型路由 + Fleet 控制平面

## 开发路线
- Phase 1 (已完成): 项目骨架 + 核心接口 + 配置 + i18n + 工具系统 + Provider 基础 + Electron 前端骨架
- Phase 2 (已完成): LLM 流式集成 + 对话引擎真实调用 + Provider 一键更新服务 + SQLite 持久化 + 权限系统 + 国内 9 Provider + Anthropic 原生 API
- Phase 3 (已完成): Token 优化器智能压缩 + TUI 终端界面 (ANSI) + MCP 协议客户端 (JSON-RPC stdio)
- Phase 4 (已完成): Electron 桌面版后端集成 + HTTP API Server + CI/CD
- Phase 5 (下一步): 文档完善 + 分发渠道 + v0.1.0 发布

## 技术约束
- Go 1.26.4+
- Node.js >= 22
- 依赖: cobra, yaml.v3, uuid, modernc.org/sqlite
- 桌面: React 18, Zustand 4, i18next, Vite 5
