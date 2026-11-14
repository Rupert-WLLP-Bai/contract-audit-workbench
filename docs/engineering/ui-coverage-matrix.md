<!-- File metadata (manifest mirror): -->
<!-- Purpose: Registers the UI2-M5 in-repo coverage matrix for 54 design views, points to out-of-repo screenshots, and records honest PARTIAL/BLOCKED/NOT_RUN acceptance without claiming fixed-candidate PASS. -->
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

## 页面模板迁移与逐行归属

四种薄模板分别负责列表、对象详情、版本编辑和运行检查的重复页面结构；已实际接入适用的业务页。审查工作区继续由现有 `ReviewWorkspaceView` 与 `DocumentStage` 管理。下表覆盖原有 **54** 个设计视图；JSON 的每行 `template` 与 `sources` 是逐视图的实现归属，路由、设计 ID 与原有 `PARTIAL` / `BLOCKED` 状态保持不变。源码列列出主要页面和实际内容组件；JSON 同时登记关联样式与共享模板源码。

| 设计 ID | 路由                                                         | 实际模式 / 模板                               | 主要源码                                                                                                                                             | 状态                          |
| ------- | ------------------------------------------------------------ | --------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------- |
| UI-001  | `/`                                                          | specialized dashboard                         | `web/src/pages/home-page.tsx`<br>`web/src/components/app-shell.tsx`                                                                                  | PARTIAL                       |
| UI-002  | `/review`                                                    | ListPage                                      | `web/src/pages/review-entry-page.tsx`                                                                                                                | PARTIAL                       |
| UI-003  | `/cases`                                                     | ListPage                                      | `web/src/pages/cases-page.tsx`<br>`web/src/components/cases-table.tsx`                                                                               | PARTIAL                       |
| UI-004  | `/contracts`                                                 | ListPage with embedded contract editor        | `web/src/pages/contracts-page.tsx`                                                                                                                   | PARTIAL                       |
| UI-005  | `/workbench/approvals`                                       | ListPage                                      | `web/src/pages/approval-tasks-page.tsx`                                                                                                              | PARTIAL                       |
| UI-006  | `/cases/:caseId`                                             | ObjectDetailPage                              | `web/src/pages/case-detail-page.tsx`                                                                                                                 | PARTIAL                       |
| UI-007  | `/support`                                                   | ListPage                                      | `web/src/pages/support-grant-pages.tsx`                                                                                                              | PARTIAL                       |
| UI-008  | `/support-grants/:grantId`                                   | ObjectDetailPage                              | `web/src/pages/support-grant-pages.tsx`                                                                                                              | BLOCKED（无真实支援授权数据） |
| UI-009  | `/cases/:caseId/legal-knowledge`                             | specialized legal reader with admin list      | `web/src/pages/legal-knowledge-page.tsx`                                                                                                             | PARTIAL                       |
| UI-010  | `/subjects`                                                  | ListPage                                      | `web/src/pages/subjects-page.tsx`                                                                                                                    | PARTIAL                       |
| UI-011  | `/subjects/:subjectId`                                       | ObjectDetailPage                              | `web/src/pages/subjects-page.tsx`                                                                                                                    | PARTIAL                       |
| UI-012  | `/cases/:caseId/operator-audit`                              | specialized operator audit workspace          | `web/src/pages/operator-audit-page.tsx`                                                                                                              | PARTIAL                       |
| UI-013  | `/cases/:caseId/reviews/:runId`                              | ReviewWorkspaceView                           | `web/src/pages/review-page.tsx`<br>`web/src/components/review-workspace.tsx`<br>`web/src/components/document-stage.tsx`                              | PARTIAL                       |
| UI-014  | `/cases/:caseId/subject-changes/:requestId`                  | specialized subject change review             | `web/src/pages/subject-change-review-page.tsx`                                                                                                       | BLOCKED（无真实更正请求数据） |
| UI-015  | `/compare`                                                   | ListPage                                      | `web/src/pages/compare-entry-page.tsx`                                                                                                               | PARTIAL                       |
| UI-016  | `/workbench/health`                                          | specialized health dashboard                  | `web/src/pages/system-health-page.tsx`                                                                                                               | PARTIAL                       |
| UI-017  | `/workbench/evidence-sla`                                    | specialized evidence SLA dashboard            | `web/src/pages/evidence-sla-page.tsx`                                                                                                                | PARTIAL                       |
| UI-018  | `/api-docs`                                                  | specialized API reference explorer            | `web/src/pages/api-docs-page.tsx`                                                                                                                    | PARTIAL                       |
| UI-019  | `/workbench/tasks`                                           | ListPage                                      | `web/src/pages/workbench-module-page.tsx`                                                                                                            | PARTIAL                       |
| UI-020  | `/workbench/remediation`                                     | ListPage                                      | `web/src/pages/workbench-module-page.tsx`                                                                                                            | PARTIAL                       |
| UI-021  | `/workbench/reports`                                         | ListPage                                      | `web/src/pages/workbench-module-page.tsx`                                                                                                            | PARTIAL                       |
| UI-022  | `/workbench/evidence`                                        | ListPage                                      | `web/src/pages/workbench-module-page.tsx`                                                                                                            | PARTIAL                       |
| UI-023  | `/workbench/executions`                                      | ListPage                                      | `web/src/pages/workbench-module-page.tsx`<br>`web/src/components/app-shell.tsx`                                                                      | PARTIAL                       |
| UI-024  | `/workbench/knowledge`                                       | ListPage                                      | `web/src/pages/workbench-module-page.tsx`                                                                                                            | PARTIAL                       |
| UI-025  | `/workbench/organization`                                    | ListPage                                      | `web/src/pages/workbench-module-page.tsx`                                                                                                            | PARTIAL                       |
| UI-026  | `/workbench/templates`                                       | ListPage                                      | `web/src/pages/workbench-module-page.tsx`                                                                                                            | PARTIAL                       |
| UI-027  | `/workbench/tools`                                           | ListPage                                      | `web/src/pages/workbench-module-page.tsx`                                                                                                            | PARTIAL                       |
| UI-028  | `/workbench/audit`                                           | ListPage                                      | `web/src/pages/workbench-module-page.tsx`                                                                                                            | PARTIAL                       |
| UI-029  | `/workbench/settings`                                        | specialized settings forms                    | `web/src/pages/settings-page.tsx`                                                                                                                    | PARTIAL                       |
| UI-030  | `/login`                                                     | specialized authentication form               | `web/src/pages/login-page.tsx`                                                                                                                       | PARTIAL                       |
| UI-031  | `/login/membership`                                          | specialized membership status                 | `web/src/pages/pending-membership-page.tsx`                                                                                                          | PARTIAL                       |
| UI-032  | `/admin/login`                                               | specialized authentication form               | `web/src/pages/login-page.tsx`                                                                                                                       | PARTIAL                       |
| UI-033  | `/admin/login/membership`                                    | specialized membership status                 | `web/src/pages/pending-membership-page.tsx`                                                                                                          | PARTIAL                       |
| UI-034  | `/admin/infrastructure`                                      | specialized infrastructure dashboard          | `web/src/pages/system-health-page.tsx`                                                                                                               | PARTIAL                       |
| UI-035  | `/admin/settings`                                            | specialized settings forms                    | `web/src/pages/settings-page.tsx`                                                                                                                    | PARTIAL                       |
| UI-036  | `/admin/tenant-lifecycle`                                    | ListPage and ObjectDetailPage                 | `web/src/pages/tenant-lifecycle-page.tsx`                                                                                                            | PARTIAL                       |
| UI-037  | `/admin/jurisdiction-operations`                             | specialized jurisdiction operations dashboard | `web/src/pages/jurisdiction-operations-page.tsx`                                                                                                     | PARTIAL                       |
| UI-038  | `/admin/rulepacks`                                           | ListPage                                      | `web/src/pages/rulepacks-page.tsx`                                                                                                                   | PARTIAL                       |
| UI-039  | `/admin/rulepacks/new`                                       | VersionEditorPage                             | `web/src/pages/rulepack-create-page.tsx`                                                                                                             | PARTIAL                       |
| UI-040  | `/admin/rulepacks/:packId/versions/:version`                 | ObjectDetailPage                              | `web/src/pages/rulepack-detail-page.tsx`                                                                                                             | PARTIAL                       |
| UI-041  | `/admin/governed-knowledge`                                  | ListPage                                      | `web/src/pages/governed-knowledge-page.tsx`                                                                                                          | PARTIAL                       |
| UI-042  | `/admin/governed-knowledge/new`                              | VersionEditorPage                             | `web/src/pages/governed-knowledge-page.tsx`                                                                                                          | PARTIAL                       |
| UI-043  | `/admin/governed-knowledge/:knowledgeId/versions/:versionId` | ObjectDetailPage                              | `web/src/pages/governed-knowledge-page.tsx`                                                                                                          | PARTIAL                       |
| UI-044  | `/`                                                          | portal-mounted API reference explorer         | `web/src/portal-main.tsx`<br>`web/src/pages/api-docs-page.tsx`                                                                                       | PARTIAL                       |
| UI-045  | `/contracts`                                                 | embedded contract editor                      | `web/src/pages/contracts-page.tsx`                                                                                                                   | PARTIAL                       |
| UI-046  | `/compare?case_id=:caseId`                                   | specialized comparison diff                   | `web/src/pages/compare-page.tsx`                                                                                                                     | PARTIAL                       |
| UI-047  | `*`                                                          | specialized not-found result                  | `web/src/pages/not-found-page.tsx`                                                                                                                   | PARTIAL                       |
| UI-048  | `/admin/*`                                                   | specialized admin not-found result            | `web/src/admin-app.tsx`<br>`web/src/pages/not-found-page.tsx`                                                                                        | PARTIAL                       |
| UI-049  | `/workbench/:unknown`                                        | specialized module not-found status           | `web/src/pages/workbench-module-page.tsx`                                                                                                            | PARTIAL                       |
| UI-050  | `/cases/:caseId/reviews/:runId`                              | ReviewWorkspaceView                           | `web/src/pages/review-page.tsx`<br>`web/src/components/review-workspace.tsx`<br>`web/src/components/document-stage.tsx`                              | PARTIAL                       |
| UI-051  | `/cases/:caseId/reviews/:runId`                              | ReviewWorkspaceView                           | `web/src/pages/review-page.tsx`<br>`web/src/components/review-workspace.tsx`<br>`web/src/components/document-stage.tsx`                              | PARTIAL                       |
| UI-052  | `/cases/:caseId/reviews/:runId`                              | ReviewWorkspaceView                           | `web/src/pages/review-page.tsx`<br>`web/src/components/review-workspace.tsx`<br>`web/src/components/document-stage.tsx`                              | PARTIAL                       |
| UI-053  | `/cases/:caseId`                                             | embedded case material collection             | `web/src/pages/case-detail-page.tsx`<br>`web/src/components/material-collection-panel.tsx`                                                           | PARTIAL                       |
| UI-054  | `/contracts`                                                 | embedded contract payment panel               | `web/src/pages/contracts-page.tsx`<br>`web/src/components/contract-payments-panel.tsx`<br>`web/src/components/contract-lifecycle-overview-panel.tsx` | PARTIAL                       |

Agent Studio 的运行详情路由 `/cases/:caseId/reviews/:runId/agent?attempt=:attemptId` 不是这 54 个设计视图中的独立设计项。它继续作为 `representativePagePatterns` 的运行检查示例；列表入口是 `/workbench/executions`。未新增或重编号设计 ID。

客户端侧栏根据当前 actor 的 `role === "admin"` 展示规则包治理、系统设置、系统健康和审计日志入口。这是导航可见性控制，旧深链仍由原有服务端和页面授权逻辑处理。组织目录显示当前 actor 可见的公开成员信息，工具网关显示已登记工具与调用状态投影，模板页显示服务端模板、字段和生成记录；这些页面没有对应写操作，因此保留给其他客户端角色。使用手册、API 文档与知识库阅读入口继续可见；知识创建仍由原功能和服务端检查处理。

UI-011 路由实际加载 `subjects-page.tsx` 导出的 `SubjectDetailPage`；该页已接入对象详情模板。UI-054 付款事实由合同登记页内的 `ContractPaymentsPanel` 和 `ContractLifecycleOverviewPanel` 呈现，矩阵同时列出父页面与实际组件。

页面源码迁移没有执行产品验收。本轮没有运行测试、lint、typecheck、build、`check-fast`、Ant Design lint、浏览器或截图；固定候选、角色状态、键盘及窄屏验收保持 `NOT_RUN`。

## 样式扫描

```bash
python3 scripts/ui_style_scan.py --root .
```

扫描只输出线索（原生控件、硬编码颜色/字号、`!important`、疑似根级 `overflow` 裁切等），不能当作确定缺陷，也不自动改文件。

## 验收边界

- 截图生成 ≠ 视觉通过；静态原型通过 ≠ 真实产品通过。
- 常规 PR 只跑受影响范围；全站截图/状态/角色与完整五阶段集中到 #97。
- 本文件与 JSON 登记计划与局部证据状态；不把 `PARTIAL` 写成 PASS。
