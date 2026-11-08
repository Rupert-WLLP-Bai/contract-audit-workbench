<!-- File metadata (manifest mirror): -->
<!-- Purpose: 说明“文档”的操作或技术边界：这里是产品、工程、运维和架构手册的唯一长期来源。`README.md` 提供项目入口和最短启动方式；此处页面从当前源码与受支持操作流程维护。历史检查结果、真实合同和运行产物保存在仓外交付目录。 -->
<!-- Author: Bai Junhao <baijunhao@cq.chinamobile.com> -->
<!-- Contact: +86 13617696321 -->
<!-- Created: 2026-11-01 -->
<!-- Updated: 2026-11-08 -->
<!-- Revision: r6 -->
<!-- Copyright: See LICENSE -->
<!-- End file metadata. -->

# 文档

## 快速开始

1. [了解案件审查工作流](product/workflow.md)：在案件页看准备清单，把材料和规则需要的内容补齐，确认后开始审查。
2. [开始审查与复核](product/workflow.md#审查与复核)：核对本次审查使用的材料、规则和原文引用。模型结果是建议，请对照原文再决定。
3. [登记合同与处理会签](product/workflow.md#合同登记与会签)：在合同登记维护草稿，在合同会签处理自己的待办。审查意见和会签决定分开保存。

也可以先看[能力与边界](product/capabilities.md)，了解工作台能做什么。

## 手册目录

## 产品

- [能力与边界](product/capabilities.md)
- [案件审查工作流](product/workflow.md)

## 工程

- [本地开发](engineering/local-development.md)
- [业务界面工程约定](engineering/interface-conventions.md)
- [UI2 M1 基础盘点](engineering/ui-foundation-inventory.md)
- [检查与验收](engineering/verification.md)
- [贡献与协作](engineering/contributing.md)
- [用户上手与扩展指南](engineering/community-guide.md)

## 架构

- [架构概览](architecture/overview.md)
- [建议层与确定性结论](architecture/advisory.md)
- [独立服务与异步执行](architecture/execution.md)

## 运维

- [部署模式](operations/deployment.md)
- [多租户身份与运营支持](operations/multi-company-operations.md)
- [备份、恢复与租户生命周期边界](operations/tenant-restore.md)
- [本地 k3d 演练](operations/k3d.md)
- [MinerU 本地 OCR](operations/ocr-mineru.md)
- [真实语料联调](operations/integrations.md)
- [文档解析与模型预算边界](operations/document-processing.md)
- [外部证据与核验](operations/external-evidence.md)

## 术语

- [用户术语](glossary/user-terms.md)
- [系统术语](glossary/system-terms.md)

机器可读概念定义仅以 [`glossary/concepts.json`](glossary/concepts.json) 为准，用户术语和系统术语页面均由它生成。请勿在其他手册中另建术语映射。

## 关于本手册

这里是产品、工程、架构、运维和术语手册的入口。正文以当前源码和已支持的操作为准。最短启动方式见仓库根目录 `README.md`。
