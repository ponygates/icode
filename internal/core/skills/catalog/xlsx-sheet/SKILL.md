---
name: xlsx-sheet
description: 生成/整理 Excel 表格（.xlsx）：客户信息表、保单清单、产品对比表、统计报表等。用 Python(openpyxl) 或 pandas 产出带格式的表格。
triggers:
  - 生成Excel
  - xlsx表格
  - 客户信息表
  - 保单清单
  - 对比表
  - 导出xlsx
---

# Excel 表格生成

当用户要求生成 .xlsx 表格时，按以下流程产出。

## 流程
0. **环境检测（必做，先于一切生成动作）**：用一次 bash 调用探测：
   `python -c "import openpyxl" >/dev/null 2>&1 && echo XLSX_OK`
   - `XLSX_OK` → 用 openpyxl（含样式时）或纯标准库 csv→xlsx 需求降级。
   - 缺失 → 征询用户：「缺少 openpyxl。选择：① 安装 `pip install openpyxl` 后继续（推荐，可带格式）；② 不装，改为生成 UTF-8 CSV（Excel 可直接打开，但无表头样式/列宽/冻结行）」。

## 流程
1. **确认字段**：先列出表头（列名）让用户确认，再生成。
2. **生成方式**（二选一）：
   - 数据规整用 `pandas`：`df.to_excel('out.xlsx', index=False)`，快速。
   - 需要样式用 `openpyxl`：表头加粗/填充色、列宽自适应、冻结首行。
3. **规范**：
   - 表头：加粗 + 浅色底 + 边框。
   - 列宽：按内容自适应（中文约 2 列宽/字）。
   - 数字列右对齐，文本列左对齐。
   - 多 sheet：一个主题一个 sheet，sheet 名中文。

## 输出要求
- 生成后返回**文件路径**与行数。
- **数据安全**：客户姓名可用代号、手机号脱敏（138****1234），联系方式不上传外部服务；本地生成。
- 不虚构数据；需要统计的数值由用户提供或由现有文件读取。
