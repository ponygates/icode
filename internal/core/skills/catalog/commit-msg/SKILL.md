---
name: commit-msg
description: 根据当前 git 改动生成规范、可读的 Conventional Commits 提交信息，可附带中英文版本与正文。
triggers:
  - 提交信息
  - commit message
  - 写 commit
  - 生成提交说明
---

# 生成提交信息

当用户要提交代码或要求生成提交信息时：

## 步骤
1. 运行 `git diff --staged` 与 `git status` 获取已暂存改动；若暂存区为空，用 `git diff` 看未暂存改动。
2. 归纳改动意图（修复 bug / 新功能 / 重构 / 文档 / 测试 / 配置）。
3. 选择 Conventional Commits 类型：`feat` / `fix` / `refactor` / `docs` / `test` / `chore` / `perf` / `style`。

## 输出格式
```
<type>(<scope>): <一句话摘要，中文，不超过 50 字>

<可选正文：为什么改、怎么改的，分点>
```
- 摘要用祈使句、中文、不写句号。
- scope 用受影响模块名，可选。
- 仅当用户要求才附带英文版本。

## 约束
- 不要自动执行 `git commit`，除非用户明确要求。
- 一条提交聚焦一个意图；若改动混杂多个意图，建议拆分提交。
