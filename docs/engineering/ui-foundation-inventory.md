<!-- File metadata (manifest mirror): -->
<!-- Purpose: Records the UI2 M1 route/component/style inventory, the single design-value source, button semantics and the verification boundary without claiming unrun browser or release acceptance. -->
<!-- Author: Bai Junhao <baijunhao@cq.chinamobile.com> -->
<!-- Contact: +86 13617696321 -->
<!-- Created: 2026-11-05 -->
<!-- Updated: 2026-11-05 -->
<!-- Revision: r1 -->
<!-- Copyright: See LICENSE -->
<!-- End file metadata. -->

# UI2 M1 基础盘点

本文对应 [#255](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/issues/255)。盘点基线为 `682bccd3a9e248595a708ce0e23dcdf7ccbb79d4`；只记录当前源码事实，不把设计稿、离线原型或局部检查当作产品验收。完整五阶段与固定候选验收仍由 [#97](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/issues/97) 执行。

## 入口与源码

| 范围 | 入口 | 路由/页面来源 | 状态 |
|---|---|---|---|
| 业务端 | `web/index.html` → `web/src/main.tsx` → `app.tsx` | `web/src/pages/*.tsx` | 已统一页面头与主布局；逐页细节见 #257/#258 |
| 管理端 | `web/admin.html` → `admin-app.tsx` | `settings-page`、`rulepacks-page`、`rulepack-*`、`governed-knowledge-page`、`tenant-lifecycle-page`、`jurisdiction-operations-page`、`system-health-page` | 已统一页面头与主题；治理详情细节见 #259 |
| 独立门户 | `web/portal.html` → `portal-main.tsx` | `api-docs-page` | 保留 Scalar API 文档自身结构与主题，只统一外层壳；不重建 API 契约 |
| 登录/异常 | `login-page`、`pending-membership-page`、`not-found-page` | 同上 | 登录不使用已登录业务壳；未找到入口独立 |

`web/src/app.tsx` 和 `web/src/admin-app.tsx` 是路由事实来源；`WORKBENCH_MODULES` 是 `/workbench/:module` 的模块清单。新增入口必须同时更新覆盖矩阵，不能只改设计稿。

## 共享组件与布局契约

| 契约 | 现有实现 | 使用范围 | 状态 |
|---|---|---|---|
| 应用壳、导航、身份与上下文 | `components/app-shell.tsx`、`components/admin-shell.tsx`、`components/context-switcher.tsx` | 业务/管理端 | 已统一；门户和登录不继承 |
| 页面头与汇总 | `.page`、`.page-head`、`.home-metrics`、`.module-stats` | 业务/管理端 | 汇总透明；列表和工作面白底 |
| 分区导航与阅读位置 | `components/section-navigation.tsx`、`components/collapsible-section.tsx` | 案件、审查、长详情 | 已实现；逐页覆盖由 #233 验收 |
| 技术身份 | `components/technical-detail.tsx` 的 `TechnicalDetail`、`TechnicalFieldGroup` | 全站详情 | 首屏只留业务信息；稳定标识、摘要和枚举编码集中到一次“技术详情”展开 |
| 状态与颜色 | `components/status-text.tsx`、`components/page-status.tsx`、`domain/business-labels.ts` | 全站 | 未知状态不使用成功色；颜色只表达语义 |
| 脏表单离开保护 | `components/dirty-leave-guard.tsx` | 合同、规则包、知识编辑 | 已有；不因换布局重写 |
| 审查工作面 | `components/review-workspace.tsx`、`review-workspace-layout.css` | 审查详情 | 双栏与独立滚动已有；分区分层由 #258 继续 |
| 列表内详情 | `pages/workbench-module-page.tsx` | 任务、整改、报告、证据、执行、知识、组织、模板、工具、审计 | 业务字段在前，技术字段聚合；逐模块验收由 #257/#258/#259 承担 |

## 设计值唯一来源

- 颜色、字号、间距、圆角、阴影、状态语义 token 统一在 `web/src/styles.css` 的 `@layer tokens`。
- `web/src/bootstrap.tsx` 从同一组 CSS 变量读取 Ant Design `ThemeConfig`；控件圆角读取 `--radius-control`，面板圆角读取 `--panel-radius`/`--radius-panel`。
- 页面级 CSS 使用 token，不新增同义色板。当前明确例外是独立 API 文档的 `web/src/pages/api-docs.css`（保留 Scalar 的阅读主题）和登录/门户的独立壳。
- 生产页面不使用原型中的 `prototype/styles.css` reset、合成数据或 `app.js`。

## 按钮语义

| 场景 | 主操作 | 次操作 | 说明 |
|---|---|---|---|
| 首页 | “打开案件中心” | 统计周期、新建任务 | 主操作用蓝色；查看类使用文本/默认按钮 |
| 案件/审查列表 | “新建案件”“发起审查” | 筛选、刷新、返回 | 一个区域最多一个主操作 |
| 审查工作区 | 提交人工判断/重连运行状态 | 返回案件、查看技术详情 | 禁用时保留原因；危险操作不使用主色 |
| 合同登记 | “新建合同草稿”“保存草稿” | 导入、导出、查询 | 导出不冒充保存；受限身份保留禁用原因 |
| 治理详情 | 批准/发布/保存新版本 | 校验、查看来源、回滚 | 已发布版本只读；危险动作显示对象与影响范围 |
| 登录/门户 | 登录/进入 | 统一身份登录 | 不使用业务导航外壳 |

## 已执行检查

| 检查 | 命令/范围 | 结果 |
|---|---|---|
| Web 类型检查 | `cd web && bun run typecheck` | PASS |
| 技术详情聚合组件 | `bunx vitest run src/components/technical-detail.test.tsx` | PASS |
| 工作台列表内详情 | `bunx vitest run src/pages/workbench-module-page.test.tsx` | PASS |
| 首页 | `bunx vitest run src/pages/home-page.test.tsx` | PASS |
| 快速门禁 | `make check-fast`（PR 实际目标基线） | 见 PR 检查结果；未运行前记 `NOT_RUN` |
| 浏览器 computed style / 键盘 / 四宽度 | 固定候选或本批定点浏览器检查 | `NOT_RUN`；由 #97 固定候选执行完整验收 |

## 已知边界

- 本盘点不声称 54 个设计视图已全部完成；逐页状态以 #261 覆盖矩阵和各里程碑 Issue 为准。
- 颜色、字体和汇总透明度可以在组件层验证；真实浏览器滚动、原生 200% 缩放、权限和异步返回仍需要浏览器与固定候选证据。
- 工作台技术字段聚合只改变呈现，不改变服务端字段、授权、冻结记录或人工历史。
