<!-- File metadata (manifest mirror): -->
<!-- Purpose: 说明“文档”的操作或技术边界：这里是产品、工程、运维和架构手册的唯一长期来源。`README.md` 提供项目入口和最短启动方式；此处页面从当前源码与受支持操作流程维护。历史检查结果、真实合同和运行产物保存在仓外交付目录。 -->
<!-- Author: Bai Junhao <baijunhao@cq.chinamobile.com> -->
<!-- Contact: +86 13617696321 -->
<!-- Created: 2026-11-01 -->
<!-- Updated: 2026-11-12 -->
<!-- Revision: r8 -->
<!-- Copyright: See LICENSE -->
<!-- End file metadata. -->

# 文档

## 快速开始

1. [从工作台完成案件审查](product/workflow.md#从工作台开始)：从“案件”进入案件管理，按准备清单补材料、选审查范围并开始审查。
2. [核对原文、人工办理与整改](product/workflow.md#原文人工决定与后续办理)：在审查工作区核对冻结原文和关键条款，再记录人工判断、办理整改。
3. [查看合同台账和会签](product/workflow.md#合同台账来源与会签)：合同草稿、版本及会签属于合同台账，和案件审查决定分开保存。

也可以先看[能力与边界](product/capabilities.md)，了解工作台能做什么。

## 手册目录

## 产品

- [能力与边界](product/capabilities.md)
- [案件审查工作流](product/workflow.md)

## 工程

- [本地开发](engineering/local-development.md)
- [业务界面工程约定](engineering/interface-conventions.md)
- [UI2 M1 基础盘点](engineering/ui-foundation-inventory.md)
- [UI2 覆盖矩阵（M5）](engineering/ui-coverage-matrix.md)
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
