# 文档

这里是产品、工程、运维和架构手册的唯一长期来源。`README.md` 提供项目入口和最短启动方式；此处页面从当前源码与受支持操作流程维护。历史检查结果、真实合同和运行产物保存在仓外交付目录。

## 产品

- [能力与边界](product/capabilities.md)
- [案件审查工作流](product/workflow.md)

## 工程

- [本地开发](engineering/local-development.md)
- [检查与验收](engineering/verification.md)
- [贡献与协作](engineering/contributing.md)

## 架构

- [架构概览](architecture/overview.md)
- [建议层与确定性结论](architecture/advisory.md)
- [独立服务与异步执行](architecture/execution.md)

## 运维

- [部署模式](operations/deployment.md)
- [本地 k3d 演练](operations/k3d.md)
- [MinerU 本地 OCR](operations/ocr-mineru.md)
- [真实语料联调](operations/integrations.md)
- [文档解析与模型预算边界](operations/document-processing.md)
- [外部证据与核验](operations/external-evidence.md)

## 术语

- [用户术语](glossary/user-terms.md)
- [系统术语](glossary/system-terms.md)

机器可读概念定义的唯一来源是 [`glossary/concepts.json`](glossary/concepts.json)，两类术语视图均由它维护。不要在其他手册建立第二套术语映射。
