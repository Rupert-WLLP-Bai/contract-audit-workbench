<!-- File metadata (manifest mirror): -->
<!-- Purpose: Explains the product’s service boundaries, local setup, operating profiles, API/runtime behavior, and phase-based verification contract; the tables and commands are the operator-facing index into the current source tree. -->
<!-- Author: Bai Junhao <baijunhao@cq.chinamobile.com> -->
<!-- Contact: +86 13617696321 -->
<!-- Created: 2026-10-25 -->
<!-- Updated: 2026-11-02 -->
<!-- Revision: r2 -->
<!-- Copyright: See LICENSE -->
<!-- End file metadata. -->

# 合同智能审计工作台

基于 Go 和 React 的合同审计工作台，用于组织合同材料、执行规则核对、查看带原文引用的模型建议，并记录人工复核、整改与复审。业务工作台当前展示“第一届黄桷树 AI 智能体开发大赛”标题及赛事 Logo。

## 能力与边界

系统支持案件材料准备、冻结审查尝试、版本化规则和知识、人工决定、整改复核与报告。模型输出只是建议；引用必须与本次尝试冻结原文相匹配。租户、组织、角色和案件范围由服务端校验。工具网关只读，外部系统写回须经人工授权。

合同、冻结审查、人工决定、规则治理和履约事实已有相应源码实现。功能状态以对应源码为准；迁移目录与兼容范围由 [`internal/store/migrations.go`](internal/store/migrations.go) 管理。应用连接会校验迁移账本，启动不自动迁移。Schema 是存储兼容边界，不是产品版本。目标环境部署、真实身份/外部系统接入、生产恢复和完整候选验收按实际证据记为 `NOT_RUN` 或 `BLOCKED`；源码能力不代表已部署或验收。

## 最短本地启动

需要 Go 1.26.7+、Bun 1.4.3、Node.js 22.12+、Python 3.10+、Docker Engine/Desktop 与 Compose v2。PDF 解析还需要 Poppler 和 CJK mappings。

```bash
make setup
make app-bootstrap
make app-config
make infra-up
make infra-smoke
```

终端一：

```bash
set -a
source .env
set +a
make dev
```

终端二：

```bash
make web
```

API 默认在 `http://127.0.0.1:8080`，前端在 `http://127.0.0.1:5173`。配置和演示账号见[本地开发手册](docs/engineering/local-development.md)。

单进程离线启动不要求模型网关。真实模型审查需要现有预算网关、独立的模型数据库角色 `MODEL_DATABASE_URL`，以及 `MODEL_GATEWAY_URL`、`WORKER_MODEL_TOKEN`、`DOCUMENT_MODEL_TOKEN` 和 `CORE_MODEL_AUTHORITY_TOKEN`；凭据必须彼此不同。模型网关以 `CORE_AUTHORITY_URL` 回连单进程 API 的私有授权接口；开发环境使用回环 HTTP 时须显式设置 `CORE_AUTHORITY_ALLOW_INSECURE_DEV=true`。缺少网关配置或只设置直连模型端点时，启动会给出错误。

## 文档

- [文档入口](docs/index.md)：产品、工程、运维、架构与术语
- [能力与边界](docs/product/capabilities.md)
- [案件审查工作流](docs/product/workflow.md)
- [架构概览](docs/architecture/overview.md)
- [部署模式](docs/operations/deployment.md)
- [检查与验收](docs/engineering/verification.md)
- [贡献与协作](docs/engineering/contributing.md)
- [许可与贡献](docs/engineering/licensing.md)

本项目自有材料采用根目录 `LICENSE` 中的 Contract Audit Workbench Noncommercial Source-Available License 1.0.0（`LicenseRef-CAW-NC-1.0.0`），生效日期为 2026-11-01；这是限制性源码许可，不是 OSI 开源许可。许可范围、公开 GitHub Fork 例外、贡献授权和第三方材料边界详见根目录 [LICENSE](LICENSE) 与[许可与贡献说明](docs/engineering/licensing.md)。

## 源码边界

仓库保存运行源码、配置、锁文件、接口契约、测试、小型 fixture 和精选长期手册。真实合同、批量样本、模型权重、密钥、运行日志与完整验收报告存放在仓库外的 `contract-audit-delivery/` 或 `CAW_DELIVERY_DIR`。`docs/` 是长期手册唯一事实源；`docs/glossary/concepts.json` 是中英文术语的机器可读概念源。
