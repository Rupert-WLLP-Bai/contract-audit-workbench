<!-- File metadata (manifest mirror): -->
<!-- Purpose: 说明“Changelog”的操作或技术边界：行为变更仅在有实际 PR 或发布依据时记录。数据库 schema 号不是产品版本；未发布改动不记为已发布版本。 -->
<!-- Author: Bai Junhao <baijunhao@cq.chinamobile.com> -->
<!-- Contact: +86 13617696321 -->
<!-- Created: 2026-11-01 -->
<!-- Updated: 2026-11-08 -->
<!-- Revision: r2 -->
<!-- Copyright: See LICENSE -->
<!-- End file metadata. -->

# Changelog

行为变更仅在有实际 PR 或发布依据时记录。数据库 schema 号不是产品版本；未发布改动不记为已发布版本。

## Unreleased

## 版本号

产品版本与数据库 schema 分开。当前发布线是 `v0.1.N`。第一个版本是 `v0.1.1`，之后每次发布把最后一位加 1，例如 `v0.1.2`、`v0.1.3`。出现不兼容或范围明显扩大的大版本时，另起 `v0.2.0`，之后同样按最后一位递增。Git 标签与产品版本号相同。

## v0.1.1 - 2026-11-08

第一个产品版本，覆盖当时 `main` 已合入的提交，不只下列原先未编号的记录。全站界面重设计 #257、#258、#259、#260、#261 保持开放，不由本版本关闭。在途 [PR #282](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/pull/282) 未计入本版本。固定候选、真实模型/OCR 与目标环境部署验收仍为 `NOT_RUN`。

- [PR #140](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/pull/140) 已合入 main，增加合同签署登记/补充协议关系，以及按冻结合同修订记录交付、验收、缺陷与整改事实。代码合入不表示数据库已部署或完整交付验收通过；部署与完整候选验收：`NOT_RUN`。
- [PR #144](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/pull/144) 已合入 main `1a9eae0`，增加本地付款条件核验与可信付款/冲正回执，以及冻结资产计划、人工移交和独立可信 ERP 入账/冲正事实；不发起转账或写回 ERP。该 PR 的 [`pr-fast-gate`](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/actions/runs/41339271202) 为 `PASS`。真实 finance/ERP 来源接入仍 `BLOCKED`；目标部署和完整候选验收仍 `NOT_RUN`。
