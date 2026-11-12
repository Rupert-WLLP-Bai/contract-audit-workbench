<!-- File metadata (manifest mirror): -->
<!-- Purpose: Registers the UI2-M5 in-repo coverage matrix for 54 design views and representative page-pattern migrations, points to out-of-repo screenshots, and records honest PARTIAL/BLOCKED/NOT_RUN acceptance without claiming fixed-candidate PASS. -->
<!-- Author: Bai Junhao <baijunhao@cq.chinamobile.com> -->
<!-- Contact: +86 13617696321 -->
<!-- Created: 2026-11-08 -->
<!-- Updated: 2026-11-12 -->
<!-- Revision: r2 -->
<!-- Copyright: See LICENSE -->
<!-- End file metadata. -->

# UI2 覆盖矩阵（M5）

对应 [#260](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/issues/260)。机器可读登记在 [`ui-coverage-matrix.json`](./ui-coverage-matrix.json)。截图、设计包与扫描原始产物留在仓外 `CAW_DELIVERY_DIR/ui2/`（默认与源码仓同级的 `contract-audit-delivery/ui2/`）。

## 口径

- 设计基线：仓外 `prototype-pages.json`，共 **54** 个设计视图；对应约 **44** 个正常路由入口，其余为条件/子视图/异常入口。
- 路由事实来源：`web/src/app.tsx`、`web/src/admin-app.tsx`、`WORKBENCH_MODULES`。新增入口必须同时更新本矩阵。
- `PARTIAL`：已有真实页面截图或定点几何记录，**不是**产品 PASS。
- `BLOCKED`：缺真实业务数据，无法截到目标视图（当前：支援详情、主体更正复核）。
- `NOT_RUN`：尚未执行的验收项，尤其是四宽度 + 原生 200%、角色/状态/键盘全矩阵，以及 [#97](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/issues/97) 固定候选完整五阶段。

## 当前统计（基线 `2a1a942`）

| 项                                | 结果                                           |
| --------------------------------- | ---------------------------------------------- |
| 设计视图登记                      | 54/54 **PASS**                                 |
| 源码路径存在                      | 54/54 **PASS**                                 |
| 页面截图（仓外）                  | 52 有 / 2 `BLOCKED`                            |
| 只读样式线索扫描                  | `scripts/ui_style_scan.py`（线索，不自动批改） |
| 浏览器矩阵 `web/e2e/ui-matrix.ts` | 工具已有；绑定固定 SHA 的全站跑次 **NOT_RUN**  |
| 1440/1280/1024/390 与原生 200%    | **NOT_RUN** → #97                              |
| 角色/状态/键盘全矩阵              | **NOT_RUN** → #97                              |
| 完整五阶段固定候选                | **NOT_RUN** → #97                              |

## 视图摘要

完整字段见 JSON。实现批次：

| Issue       | 范围                   | 视图数                            |
| ----------- | ---------------------- | --------------------------------- |
| #257 UI2-M2 | 业务办理与支援         | 见 JSON `implementationIssue=257` |
| #258 UI2-M3 | 审查、证据、对照、报告 | 见 JSON `implementationIssue=258` |
| #259 UI2-M4 | 治理、平台、运营、身份 | 见 JSON `implementationIssue=259` |

当前 `BLOCKED`：

| 设计ID | 视图           | 原因                   |
| ------ | -------------- | ---------------------- |
| UI-008 | support-detail | 无真实支援授权数据     |
| UI-014 | subject-change | 无真实主体更正请求数据 |

## 首批页面模板与入口

本批实现四种薄组合模板，并迁移五个真实页面：列表用于 `/cases` 与 `/contracts`；对象详情用于 `/cases/:caseId`；版本编辑用于 `/admin/rulepacks/new`；运行检查用于现有 Agent Studio 详情 `/cases/:caseId/reviews/:runId/agent?attempt=:attemptId`。Agent Studio 主入口指向真实执行清单 `/workbench/executions`；审查工作区 `/cases/:caseId/reviews/:runId` 继续使用现有 `ReviewWorkspaceView` 与 `DocumentStage`。

五个主入口和旧次级路径登记在 JSON 的 `mainNavigation`、`views[].sources` 与 `representativePagePatterns` 中。登记只标出本批接入点；54 个设计视图、原有路由去向、`PARTIAL` / `BLOCKED` 状态与验收结论都没有因此改变，也不代表剩余视图已迁移。

Agent Studio 详情是已有应用路由，但不是这 54 个设计视图中的独立设计项。JSON 将它单列在 `representativePagePatterns`，没有新增或重编号设计 ID。

此批改造的固定候选、角色状态、键盘及窄屏验收仍为 `NOT_RUN`。

## 样式扫描

```bash
python3 scripts/ui_style_scan.py --root .
```

扫描只输出线索（原生控件、硬编码颜色/字号、`!important`、疑似根级 `overflow` 裁切等），不能当作确定缺陷，也不自动改文件。

## 验收边界

- 截图生成 ≠ 视觉通过；静态原型通过 ≠ 真实产品通过。
- 常规 PR 只跑受影响范围；全站截图/状态/角色与完整五阶段集中到 #97。
- 本文件与 JSON 登记计划与局部证据状态；不把 `PARTIAL` 写成 PASS。
