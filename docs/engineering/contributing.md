<!-- File metadata (manifest mirror): -->
<!-- Purpose: Sets the contributor procedure for short-lived fix/feat branches, issue-scoped commits, file ownership during shared-checkout work, evidence labels, and the fast-check cadence. -->
<!-- Author: Bai Junhao <baijunhao@cq.chinamobile.com> -->
<!-- Contact: +86 13617696321 -->
<!-- Created: 2026-11-01 -->
<!-- Updated: 2026-11-02 -->
<!-- Revision: r2 -->
<!-- Copyright: See LICENSE -->
<!-- End file metadata. -->

# 贡献与协作

## 分支与提交

`main` 是唯一长期主线。每项工作从最新 `main` 建立短期 `fix/<issue>-<slug>`、`feat/<issue>-<slug>` 或 `refactor/<issue>-<slug>` 分支，完成后直接向 `main` 提 PR。一个同域小批次可覆盖 2–3 个 Issue；PR 对每项分别列出完成范围，提交按 Issue 拆分。只有完全满足对应验收时才使用 `Closes #N`，关联或部分完成使用 `Refs #N`。

新业务入口使用相应领域模块。`internal/store` 现有接口渐进迁移；新增业务逻辑须先提交 ADR，写清数据归属、事务和授权边界及迁移路径。缺陷修复和迁移适配按原语义处理。结构重构使用 `refactor/` 分支，保持同域 2–3 个 Issue 一批、提交按 Issue 拆分；机器检查按架构守卫的渐进范围执行。

根 Agent 是唯一分支和提交操作人，负责刷新基线、切分支、整理提交并整合最终结果。共享主检出可并行处理，但每个写入者必须先收到明确且互不重叠的文件所有权；写入者不切分支、不提交。共享核心、schema、授权、公共接口、依赖锁、CI/workflow 与 AGENTS 由单一写入者串行维护。

## 检查与证据

按 `AGENTS.md` 的检查节奏和 `make check-fast` 的实际差异计划运行受影响检查。普通文档变更不运行应用套件。PR 的 Evidence 记录检查对应的源码 SHA、命令、范围和实际状态：`PASS`、`FAIL`、`BLOCKED`、`NOT_RUN` 或 `NOT_APPLICABLE`。没运行的项目写 `NOT_RUN`；Phase 4 固定候选的完整回归不由日常 PR 代跑，也不把局部结果拼成完整通过。

`--plan` 中的 `file_metadata` 表示文件说明检查已选中。CI 从实际目标分支加载检查器，候选分支不能通过修改检查器自行宣称初始化完成；旧清单尚未初始化时明确报告 `NOT_RUN`。统一清单仍为 `scripts/file_metadata/manifest.json`，规范见[逐文件元信息](file-metadata.md)。新增、删除或重命名文件时同步维护该清单；已应用迁移、冻结 fixture、生成物和第三方声明按原始字节保护。头部写入仅通过固定对象与计划摘要约束的显式操作执行，不由快速检查自动改文件。

## Issue 与标签

新 Issue 使用表单，写清目标、范围、依赖、文件所有权、验收、最小验证、回退以及文档/概念影响。每项任务选择一个 type 和一个主 area，并保留现有中文 `阶段/…`、`验收/…` 状态标签。不要为同一含义创建同义标签，也不批量重命名历史标签。

截至 2026-11-01 的仓库标签目录核对结果：

| 含义 | 复用的现有标签 | 说明 |
| --- | --- | --- |
| type: bug | `bug` | 已存在 |
| type: feature / refactor | `enhancement` | 已存在；结构调整须保持行为 |
| type: docs | `documentation` | 已存在 |
| area | `area:audit`、`area:docs`、`area:infra`、`area:tenant`、`area:web` | 表单列出当前已存在主 area |
| 阶段 / 验收 | `阶段/待实现`、`验收/NOT_RUN` 等现有中文状态 | 新表单默认待实现与 NOT_RUN |

本次只读取目录，没有创建、删除或改名远端标签；若新工作需要目录之外的 area，应先登记并核对，避免重复同义项。
