# 行业技能包 · 保险行业（Insurance Pack）

本目录存放从 iCode 内置技能市场剥离的**垂直行业技能包**。它们不再随 `icode` 二进制内嵌分发，以保持 iCode "开源通用编码助手" 的定位纯净；需要时按下述方式一键装回。

## 包含技能（5 个）

| 技能 | 用途 |
|------|------|
| `insurance-pitch` | 保险产品讲解与需求匹配话术 |
| `client-needs` | 客户需求分析（KYC） |
| `policy-review` | 保单检视与保障缺口分析 |
| `claims-assist` | 理赔协助流程 |
| `objection-handling` | 客户异议处理 |

## 安装方式

**方式一：iCode 桌面端 / API 导入**（推荐）

在桌面端「设置 → 技能 → 已安装 → 本地导入」填入本目录的绝对路径（一次导入一个技能目录），或直接调 API：

```bash
curl -X POST http://127.0.0.1:57356/api/skills/import \
  -H "Content-Type: application/json" \
  -d '{"path": "E:/icode/industry-packs/insurance/client-needs"}'
```

**方式二：直接复制**

```powershell
Copy-Item -Recurse E:\icode\industry-packs\insurance\* $HOME\.icode\skills\
```

安装后重启会话（或等注册表下次加载），技能即出现在可用列表中。

## 说明

- 技能格式与内置市场完全一致（SKILL.md，YAML frontmatter），`category` 建议填 `general` 或留空。
- 本包同时是未来社区市场 `icode-skills` 仓库的行业包样例——远程技能源（`POST /api/skills/install-from-source`）上线后，可直接从 GitHub 安装。
