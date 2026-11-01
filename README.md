# 合同智能审计工作台

基于 Go 和 React 的合同审计工作台，用于组织合同材料、执行规则核对、查看带原文引用的模型建议，并记录人工复核、整改与复审。业务工作台当前展示“第一届黄桷树 AI 智能体开发大赛”标题及赛事 Logo。

## 能力与边界

系统支持案件材料准备、冻结审查尝试、版本化规则和知识、人工决定、整改复核与报告。模型输出只是建议；引用必须与本次尝试冻结原文相匹配。租户、组织、角色和案件范围由服务端校验。工具网关只读，外部系统写回须经人工授权。

合同、不可变版本、案件关联、草稿、存量导入来源及部分顺序会签记录已有实现。PR [#140](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/pull/140) 已合入 main `595fe2f`，当前源码接受 schema `[28,28]`：0027 记录签署登记与补充协议修订关系，0028 记录交付、验收、缺陷和整改事实。代码合入不代表数据库已升级或完整交付已验收；部署与完整候选验收仍为 `NOT_RUN`。签署登记保存人工核对事实，不等于电子签名验证；履约记录不等于已付款，也不证明所有签署、履约和付款流程已交付。

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

## 文档

- [文档入口](docs/index.md)：产品、工程、运维、架构与术语
- [能力与边界](docs/product/capabilities.md)
- [案件审查工作流](docs/product/workflow.md)
- [架构概览](docs/architecture/overview.md)
- [部署模式](docs/operations/deployment.md)
- [检查与验收](docs/engineering/verification.md)
- [贡献与协作](docs/engineering/contributing.md)
- [许可与贡献](docs/engineering/licensing.md)

根目录 `LICENSE` 文件标识为 `LicenseRef-CAW-NC-1.0.0`，版权标记为 Bai Junhao © 2026，文件注明日期 2026-11-01。文件所述许可仅覆盖约定的非商业用途；组织运营、商业部署、托管服务、独立发布或分发衍生版本须另行取得书面许可。文件允许公开 GitHub Fork 用于修改、测试并向上游提交 PR，该例外不授予商业使用、生产部署或站外分发权。第三方材料保留各自许可；本项目不宣称为 OSI 开源。许可及贡献条款以根目录 [LICENSE](LICENSE) 为准。详见[许可与贡献说明](docs/engineering/licensing.md)。

## 源码边界

仓库保存运行源码、配置、锁文件、接口契约、测试、小型 fixture 和精选长期手册。真实合同、批量样本、模型权重、密钥、运行日志与完整验收报告存放在仓库外的 `contract-audit-delivery/` 或 `CAW_DELIVERY_DIR`。`docs/` 是长期手册唯一事实源；`docs/glossary/concepts.json` 是中英文术语的机器可读概念源。
