<!-- File metadata (manifest mirror): -->
<!-- Purpose: 介绍合同智能审计工作台的核心功能、技术架构、内置示例合同快速体验方式与文档入口。 -->
<!-- Author: Bai Junhao <baijunhao@cq.chinamobile.com> -->
<!-- Contact: +86 13617696321 -->
<!-- Created: 2026-10-25 -->
<!-- Updated: 2026-11-20 -->
<!-- Revision: r7 -->
<!-- Copyright: See LICENSE -->
<!-- End file metadata. -->

# 合同智能审计工作台

面向政企采购、法务和合规团队的合同审查与台账系统，基于 Go 和 React 构建。

上传采购文件、中标文件和待审合同后，系统结合内置规则与大模型交叉比对关键条款，把每条风险线索定位到具体原文；审查人员对照原文确认或排除风险，再跟进补件、在线改稿、整改复核与报告归档，并在合同台账中管理会签和履约记录。

```mermaid
flowchart LR
    A[上传合同与采购材料] --> B[解析原文与前置检查]
    B --> C[规则核对 + 大模型审查]
    C --> D[对照原文人工判断]
    D --> E[在线改稿 / 补件 / 整改复核]
    E --> F[审查报告与合同台账履约]
```

## 为什么这样设计

- **每条风险都对得上原文**：每次审查都会保存当时使用的文件、解析文本和规则版本，后续换稿或改规则不覆盖旧记录。大模型输出只作为建议，必须定位到当次送审原文才能确认为风险。
- **所有人工判断按历史留痕**：确认风险、排除误报、补充问题和整改复核都以追加方式保存，随时可以回看历次意见和当时的审查依据。
- **改稿与对照在同一套系统里完成**：可以在浏览器里直接编辑 DOCX 合同并另存为新版本，也能从单条风险线索生成修改稿，并排对照不同版本的条款差异。
- **严格的权限与数据隔离**：服务端按租户、组织和角色校验访问权限，数据库启用行级安全（RLS），各核心服务使用独立数据库账号。

## 主要功能

| 模块 | 说明 |
| --- | --- |
| 案件与材料 | 按案件归集采购文件、中标文件、合同正文和技术附件；支持 PDF / DOCX / TXT 解析、多版本保留和浏览器内编辑 DOCX |
| 智能审查 | 基于 Eino 编排规则核对与大模型审查，支持单合同预审和跨文件完整审查；点击风险线索直接高亮原文位置 |
| 人工复核与整改 | 对照原文记录判断与理由、补充人工问题、发起补件和整改复核，导出审查报告及可独立验签的证据包 |
| 合同台账与履约 | 管理合同草稿、定稿版本、历史来源记录、部门会签、交付验收、付款核对与站内履约提醒 |
| 知识与规则管理 | 维护审查规则、审查手册、合同范本和经验条目，支持审批发布与版本追溯 |

## 快速体验

### 1. 启动本地环境

先安装本机依赖（Go 1.26.7+、Bun 1.4.3、Node.js 22.12+、Python 3.10+、Docker Compose v2 和 Poppler，详见 [本地开发手册](docs/engineering/local-development.md#环境)），在仓库根目录执行：

```bash
make setup
make app-bootstrap
make app-config
make infra-up
make apps-up-fixture
```

启动后访问 `http://127.0.0.1:18080`，使用演示账号 `demo-admin` / `admin-demo` 登录。

> `make apps-up-fixture` 使用内置的本地模拟模型，无需外部 API 密钥即可体验规则核对与主流程。接入真实大模型或本机 OCR 请改用 `make apps-up`，配置方式见 [本机文本与 OCR 模型](docs/engineering/local-development.md#本机文本与-ocr-模型)。

### 2. 用内置示例合同走通一次审查

仓库自带一组贴近真实场景的示例文件（`testdata/demo-contract/`），模拟“政务云核心网络扩容及三年运维服务”项目的采购文件与乙方合同初稿：

- 采购文件：[`testdata/demo-contract/procurement-spec.txt`](testdata/demo-contract/procurement-spec.txt)
- 合同初稿：[`testdata/demo-contract/draft-contract.txt`](testdata/demo-contract/draft-contract.txt)

合同初稿刻意埋入了两类常见风险：一类是金额、付款比例和期限上的数值偏差，另一类是“总价包干变另行计费”“除……外掏空质保”“默认验收合格”“变相转包”“自动续约”等文字游戏。

操作步骤：

1. 在左侧栏点 **案件**，新建一个“采购 / ICT”案件。
2. 进入案件的 **材料与版本**，分别上传上面两份文件（对应选择“采购文件”和“拟签合同”），等待解析完成。
3. 切到 **审查记录**，在准备清单里选择 **完整审查**，补齐提示的必填项后开始审查。
4. 审查完成后打开审查工作区，可按“金额与付款”“履约与期限”“权利与责任”快速跳到对应条款，逐条对照原文并记录人工判断。

**这套示例能测出什么：**

| 风险类型 | 采购文件要求 | 合同初稿写法 | 识别方式 |
| --- | --- | --- | --- |
| 金额上浮 | 采购金额 180 万元 | 合同总价 192 万元 | 内置规则直接标出数值冲突 |
| 付款节点偷换 | 预付 20% + 验收后 70% + 质保满 10% | 预付 40% + 到货后 50% + 质保满 10%（合计仍是 100%） | 内置规则识别节点变化并标冲突 |
| 单位一字之差 | 交付后 15 个工作日验收 | 交付后 15 个自然日验收 | 内置规则识别“工作日 / 自然日”差异 |
| 表面同数、例外掏空 | 免费质保期 36 个月 | 除第三方软件、易损备件及现场人工外，质保期 36 个月 | 内置规则识别“除……外”限定，拒绝误判通过并交由人工核对 |
| 纯条款文字游戏 | 总价包干；终验起算质保；7×24 现场响应；禁止转包；赔付上限 20%；不自动续约；永久保密 | 差旅加急另算；逾期未异议默认验收合格；发货起算质保且仅 5×8 远程；自行安排协作方视同认可；赔付封顶 5% 且免责；自动续期一年；保密仅两年 | 接入真实大模型（`make apps-up`）后进一步检出；本地模式也可在双栏视图中对照原文人工补充 |

## 技术栈与架构

| 层次 | 技术选型 |
| --- | --- |
| 后端 | Go 1.26、Eino（智能体编排）、pgx v5、OIDC / PKCE、OpenTelemetry |
| 前端 | React 19、TypeScript、Ant Design 6、TanStack Query、Vite、DOCX 浏览器编辑器 |
| 数据与存储 | PostgreSQL 18（行级安全、业务与任务权威存储）、RustFS（S3 兼容对象存储）、NATS JetStream（短通知） |
| 架构形态 | 支持单进程本地开发（`cmd/server`），也支持通过 Docker Compose 运行多服务（Gateway、Core、Document、Execution、Worker、Model Gateway、Tool Gateway 及业务/管理/门户三端） |

详细服务划分与目录索引见 [架构概览](docs/architecture/overview.md)。

## 文档导航

| 方向 | 入口 |
| --- | --- |
| 产品与使用 | [案件审查工作流](docs/product/workflow.md) · [能力与边界](docs/product/capabilities.md) · [用户术语](docs/glossary/user-terms.md) |
| 开发与架构 | [本地开发](docs/engineering/local-development.md) · [架构概览](docs/architecture/overview.md) · [贡献与协作](docs/engineering/contributing.md) · [Agent 约束](AGENTS.md) |
| 运维与验收 | [部署模式](docs/operations/deployment.md) · [MinerU 本地 OCR](docs/operations/ocr-mineru.md) · [检查与验收](docs/engineering/verification.md) |
| 全部文档 | [完整文档索引](docs/index.md) |

## 许可

本项目自有源码采用非商业源码可用许可 `LicenseRef-CAW-NC-1.0.0`（非 OSI 开源许可）。允许个人学习、非商业评估和公共知识研究，也允许通过公开 GitHub Fork 向上游提交补丁；商业使用、组织内部业务部署或再分发须事先取得书面授权。详见 [LICENSE](LICENSE) 与 [许可与贡献说明](docs/engineering/licensing.md)。
