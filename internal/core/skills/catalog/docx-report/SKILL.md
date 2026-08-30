---
name: docx-report
description: 生成规范的中文 Word 报告（.docx）：保单检视报告、财务规划建议书、工作总结等。用 Python(python-docx) 或 pandoc 从结构化内容产出排版规范的文档。
triggers:
  - 生成Word
  - docx报告
  - 保单检视报告
  - 财务规划建议书
  - 导出docx
---

# Word 报告生成

当用户要求生成 .docx 文档（报告/建议书/总结）时，按以下流程产出。

## 流程
0. **环境检测（必做，先于一切生成动作）**：用一次 bash 调用同时探测两条生成链：
   `pandoc --version >/dev/null 2>&1 && echo PANDOC_OK; python -c "import docx" >/dev/null 2>&1 && echo DOCX_OK`
   - `PANDOC_OK` → 走 pandoc（最省事、样式统一）。
   - 仅 `DOCX_OK` → 走 python-docx。
   - 两者皆缺 → 先征询用户：「检测到缺少 pandoc / python-docx，选择：① 我给你安装命令（Windows: `winget install -e --id JohnMacFarlane.Pandoc` 或 `pip install python-docx`；macOS: `brew install pandoc`），装好后继续；② 不装了，退而生成排版好的 Markdown 文件，用户自行粘贴到 Word」——用户选 ② 时把 Markdown 按 Word 可粘贴的层级写清楚，不要硬造 .docx。
1. **确认结构**：先列出文档大纲（标题层级）让用户确认，再生成。
2. **生成方式**（二选一）：
   - 优先 `pandoc`（若已安装）：写 Markdown 源 → `pandoc input.md -o output.docx`，最省事且样式统一。
   - 无 pandoc 用 `python -c` + python-docx：脚本设置标题/正文/表格样式。
3. **规范**：
   - 标题层级：一级标题（文档名）、二级标题（章节）、正文、表格。
   - 中文排版：首行缩进 2 字符、行距 1.5、字体仿宋/宋体（正文）+ 黑体（标题）。
   - 封面：文档名 + 日期 + 落款（如需）。

## 输出要求
- 生成后返回**文件路径**，并说明用什么工具生成的。
- **合规**：保险/财务文档涉及具体产品、费率、收益时，标注"以正式条款为准"，不虚构数字；涉及法税建议提示咨询专业机构。
- 文件名用中文描述 + 日期，如 `保单检视报告_张三_20260830.docx`（客户名脱敏可用代号）。
