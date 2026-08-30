# D1 · 桌面左右分屏 设计文档

> 目标：两会话并排显示（Reasonix 桌面「sessions side by side」拍照级），左/右各自独立滚动、独立流式、可拖拽分隔条调宽。
> 状态：SplitPane 地基组件已交付（v0.50.0，`desktop/src/components/SplitPane.tsx`）；**SessionPane 参数化待实施**（唯一未完成项）。

---

## 零、精确实施清单（已勘明，供专项轮直接落地）

**当前障碍**：`ChatPage.tsx`（2488 行）36 处 `activeSessionId` 全局引用，需改为入参驱动。

### 引用点清单（v0.50.2 实测行号）
| 行号 | 用途 | 参数化改法 |
|---|---|---|
| 396 | `const activeSessionId = useAppStore(...)` | 改为 `const sid = props.sessionId ?? storeActiveId` |
| 432/438 | isStreaming / activeSession 派生 | 用 `sid` |
| 455/465/477/487 | 输入框恢复闭包 | 依赖数组换 `sid` |
| 595/621 | sendMessage 闭包 | `sid` |
| 686/689/707/710 | 首条会话自动创建 | 仅左侧生效 |
| 818/825/826/837/840 | addMessage / apiAppendMessage | `sid` |
| 960/1001 | 流式 tick 会话校验 | `sid` |
| 1153/1204/1205/1206 | send/stop 闭包 | `sid` |
| 1314/1318/1323/1398/1400 | 标题/会话 API | `sid` |
| 1456/1500/1505/1506/1508 | 工具条/清空 | `sid` |
| 1527/1664/2045 | TabBar/欢迎/plan 弹窗 | `sid` |

### 落地步骤
1. `ChatPage` 加可选 prop `sessionId?: string`；组件内 `const sid = sessionId ?? activeSessionId`，上述 36 处 `activeSessionId` 全部替换为 `sid`（`useAppStore.getState().activeSessionId` 两处同理用 `sid`）。
2. 抽 `SessionPane({ sessionId })`：把 ChatPage 主体包一层，`sessionId` 透传。
3. 新增 `SplitView`：`<SplitPane left={<SessionPane sessionId={leftId}/>} right={<SessionPane sessionId={rightId}/>} />`；右侧会话用 TabBar 下拉选择。
4. **回归风险最高点在 455-487（输入框恢复）与 595-621（sendMessage）**——改完必须回归测试：单会话消息收发、切换会话、清空、标题重命名、权限弹窗、流式中断。

### 已完成（无需重复）
- `SplitPane.tsx`（拖拽分隔条 + 比例持久化 + 双击恢复）— v0.50.0
- 多会话并行流式 `streamingSessions` + TabBar 拖拽 — v0.48.0

---

## 一、现状与改造核心

- 现状：`ChatPage.tsx`（2488 行）深度绑定 `appStore.activeSessionId` 单例——消息列表、输入框、SSE 流、权限弹窗、滚动状态全部挂一个全局会话。
- **核心改造**：把 ChatPage 的消息/输入/流逻辑**下沉为可参数化的子组件** `SessionPane({ sessionId })`，再包一层 `SplitPane` 容器放两个实例（SplitPane 已就绪）。

## 二、实施拆解

### P0 · SessionPane 参数化（1 天，风险最高）
1. 抽 `SessionPane`：接收 `sessionId`，内部不再读 `activeSessionId`，改读入参；SSE 订阅、输入框、滚动各自独立（store 已有 `streamingSessions` 多流状态，D1 前半段已铺路）。
2. 权限弹窗 `pendingPermission` 已带 `sid`（v0.48.0 改过结构），天然支持按会话路由。
3. **验收**：单会话行为与重构前完全一致（回归测试 + 手动）。

### P1 · SplitPane 容器 + 拖拽分隔条（0.5 天）
1. `SplitPane`：`flex` 两栏 + 中间 4px 分隔条（`mousedown` 拖拽改 `splitRatio`，存 localStorage）。
2. 右侧面板会话选择：复用 TabBar 或下拉，选中后右侧 `SessionPane` 独立渲染。
3. **验收**：拖分隔条流畅；两栏各自滚动/流式互不干扰。

### P2 · 并排增强（0.5 天，可二期）
1. 双击分隔条恢复 50/50；右键 tab「在右侧打开」。
2. 左右同步滚动（可选，读两侧 scrollTop 互相跟随）。

## 三、关键风险
- **SSE 按会话隔离**：确认 `streamingSessions` 的 key 是 sessionId（v0.48.0 已引入）；若现仍是「单活跃流」，先补后端/前端的多流路由（D1 前半段已做并行流式）。
- **巨型组件拆分**：ChatPage 2000 行，抽 SessionPane 时按「消息列表 / 输入框 / 工具调用卡片」粒度切，避免一次性大重构引入回归。

## 四、验收准则
1. 左右两会话同时流式，互不串流、互不卡顿。
2. 拖拽分隔条调宽并持久化；重启恢复比例。
3. 权限请求弹窗出现在正确的会话栏。
4. 单会话模式下零回归。
