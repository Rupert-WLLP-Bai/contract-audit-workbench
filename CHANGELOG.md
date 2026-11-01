# Changelog

行为变更仅在有实际 PR 或发布依据时记录。数据库 schema 号不是产品版本；未发布改动不记为已发布版本。

## Unreleased

- [PR #140](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/pull/140) 已合入 main，增加合同签署登记/补充协议关系，以及按冻结合同修订记录交付、验收、缺陷与整改事实。代码合入不表示数据库已部署或完整交付验收通过；部署与完整候选验收：`NOT_RUN`。
- [PR #144](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/pull/144) 已合入 main `1a9eae0`，增加本地付款条件核验与可信付款/冲正回执，以及冻结资产计划、人工移交和独立可信 ERP 入账/冲正事实；不发起转账或写回 ERP。该 PR 的 [`pr-fast-gate`](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/actions/runs/41339271202) 为 `PASS`。真实 finance/ERP 来源接入仍 `BLOCKED`；目标部署和完整候选验收仍 `NOT_RUN`。
