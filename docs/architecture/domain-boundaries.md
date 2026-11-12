<!-- File metadata (manifest mirror): -->
<!-- Purpose: Defines accepted domain data ownership, shared transaction requirements and dependency directions while distinguishing existing Store placement from future incremental migration. -->
<!-- Author: Bai Junhao <baijunhao@cq.chinamobile.com> -->
<!-- Contact: +86 13617696321 -->
<!-- Created: 2026-11-02 -->
<!-- Updated: 2026-11-03 -->
<!-- Revision: r3 -->
<!-- Copyright: See LICENSE -->
<!-- End file metadata. -->

# ADR：领域边界与现有持久化边界

状态：采纳。所有权清单固定于源码 `9040a61f8410fef46c06106f5874d2b9a6fe1c21`（迁移 0001–0039）；集成基线 f3c3272 的 Go 输入与该快照逐文件一致。目标约束用于后续渐进迁移，不表示运行结构已迁移。采用 14 个领域作为职责边界，而不是将蓝图目录逐一新建或把现有 `Store` 按文件名搬迁。Core、Document、Execution、Worker 是部署服务，不能直接等同领域。DTO 字段数量本身不是数据库宽表错误的证据。

下表“数据”列是采纳后的目标所有权及已核对的代表性表，不是完整表清单；“接口”是现有源码实际内部入口示例，不表示现有代码已经迁入目标模块。当前多数持久化及跨域组合仍在 `internal/store`；迁移例外持续有效，直到逐个替换并验证，不能把此目标当作当前运行结构。新领域标签不触发目录、运行代码或 schema 修改。

## 共同约束

- PostgreSQL 是业务和任务状态的权威；同一个跨域命令涉及多领域的原子写入时共享同一数据库事务，不拆成多次提交。会签决定及相应任务、轮次、合同状态、审计和 Outbox 写入按现有命令的条件和事务边界一起处理。RLS scope 在事务开始后设置并随事务结束清除；租户、组织、角色、对象和案件范围由服务端解析，默认拒绝。各服务继续使用各自数据库角色。
- 目标依赖方向：transport → application；application → 本域 ports 和其他领域公开 contract；PostgreSQL 适配器只实现本域 ports，不导入其他领域 PostgreSQL 适配器，也不直接访问其他领域任意表。跨域命令通过携带同一事务及已授权 scope 的 ports 协作，不拆提交；跨域读取通过公开 contract。现有 `internal/store` 的跨域 SQL 和现有包导入是迁移例外，不声称当前已满足目标方向。
- 审查输入、解析与规则快照按尝试冻结，历史人工决定追加；不得为目录迁移重写身份、冻结证据或历史决定。模型结果是建议，无可核验冻结原文引用不得成为确认结论。
- 预算账本、任务租约和 Outbox 继续作为权威边界；NATS 仅承载短通知。工具网关只读，外部写回须人工授权。跨域访问使用现有授权入口和明确契约，不绕过 scope 直接写另一个领域的表；共享事务通过事务感知的调用协作，并非允许任意跨域写入。

## 领域清单

| 领域 | 数据所有权范围（代表性数据） | 事务边界 | 现存对外入口示例 | 禁止依赖或操作 |
| --- | --- | --- | --- | --- |
| Contract | 合同聚合及版本：`clm_contracts`、`clm_contract_revisions`、`clm_contract_cases`；外部来源身份与合同来源观察归 Contract；`clm_business_events` 为合同聚合的追加业务事件。 | 版本、状态及相关事件在现有命令事务内一致；不改已冻结版本。 | `Store.CreateContractDraft`、`Store.LoadContractDetail`、`clmsource.CompareReceipt` | 不依赖绕过授权的其他域表写入；不以外部观察覆盖历史版本。 |
| Approval | 审批轮次、任务分配和决定：`clm_approval_rounds`、`clm_approval_decisions`；任务及转办见 #149。 | 会签命令保持决定、相关任务/轮次/合同状态及适用审计、Outbox 同事务。 | `Store.DecideApprovalTask`、`Store.TransferApprovalTask`、`approval.Keyring.Verify` | 不以新决定覆盖历史决定，不信任客户端自报身份或权限。 |
| Fulfillment | 交付、验收、缺陷和整改：`clm_contract_deliveries`、`clm_contract_acceptances`、`clm_contract_rectifications`。 | 单个命令及其关联状态和事件按现有事务提交；跨域状态不另开独立提交。 | `Store.CreateContractDelivery`、`Store.CreateContractAcceptance`、`fulfillment.Evaluator.Evaluate` | 不把外部观察或机器检查直接当人工确认；不改合同冻结版本。 |
| Payment | 付款资格检查与可信回执：`clm_contract_payment_eligibility_checks`、`clm_contract_payment_receipts`。 | 资格、可信回执与相应状态/事件按现有命令事务；金额规则不以浮点替代。 | `Store.CheckContractPaymentEligibility`、`Store.RecordTrustedContractPaymentReceipt` | 不把普通客户端输入当可信付款事实，不绕过授权更改合同或验收。 |
| Asset | 资产移交和可信回执：`clm_contract_asset_handovers`、`clm_contract_asset_receipts`；版本计划与合同版本共同约束。 | 移交或回执与关联状态/事件遵循现有事务。 | `Store.CreateContractAssetHandover`、`Store.RecordTrustedContractAssetReceipt` | 不凭外部未验证事实改变合同状态，不改冻结版本计划。 |
| Audit | 审查运行、尝试和审计链：`review_runs`、`review_attempts`、`audit_events`、`business_audit_events`；各事件生产者通过对应命令追加。 | 审查尝试输入及适用审计事件随对应业务写入原子提交；保留既有审计链约束。 | `audit.Append`、`audit.Verify`、`Store.RewriteAudit`（仅用于验证拒绝重写，不是修改接口） | 不用目录重组理由重写冻结快照或历史决定；不把建议自动确认为结论。 |
| Knowledge | 治理知识的版本、状态、指针和历史：`governed_knowledge_state`、`governed_knowledge_pointers`、`governed_knowledge_history`。 | 治理命令和对应状态/历史在现有事务内一致；审查引用既有冻结快照。 | `Store.GovernedKnowledgeCommand`、`Store.ResolveGovernedKnowledge` | 不以当前知识替换旧尝试快照；不以检索排名充当风险可信度。 |
| Legal | 法律来源捕获与适用性事实：`legal_source_captures`；来源捕获事实归 Legal，治理知识版本、状态和指针归 Knowledge。 | 来源导入及关联记录依现有事务；引用依赖冻结审查上下文。 | `Store.ImportLegalSource` | 不把未核实的来源或模型建议变成专业结论；不跨案件绕过 scope。 |
| Subject | 主体及历史、变更与来源快照：`subjects`、`subject_versions`、`subject_change_requests`、`subject_source_snapshots`。 | 主体命令、历史及幂等记录遵循现有事务；不得覆盖既有历史。 | `Store.CreateCaseSubject`、`Store.CreateSubjectSourceSnapshot` | 不把主体身份等同登录身份；不绕过案件授权或重写历史。 |
| Identity | 身份与会话：`sessions`；租户成员关系、角色和登录身份归 Identity；运行控制归 Platform。 | 会话及成员授权操作保留原有事务和会话隔离；scope 在事务内设置。 | `identity.Manager.Begin`、`identity.Manager.CompleteWithMembership`、`Store.EstablishSession` | 不接受客户端声称的租户、角色或 scope 作为权威；不与业务主体混同。 |
| Document | 材料版本、解析及原件引用：`material_entities`、`material_versions`、`parsed_documents`。 | 材料选择、解析记录与尝试绑定按现有事务及对象存储边界；旧尝试不被重解析改写。 | `Store.PrepareAttemptDocument`、`Store.ConfirmMaterialSelection` | 不跨案件读取原件；不让新解析改写旧冻结输入。 |
| Evidence | 证据请求、回执及历史：`evidence_requests`、`evidence_receipts`、`evidence_request_events`；原文引用核验与审查尝试相连。 | 请求、回执和状态历史按现有命令事务；跨域更新需同一事务或明确异步边界。 | `Store.CreateEvidenceRequest`、`Store.SubmitEvidenceReceipt`、`Store.ListEvidenceRequestHistory` | 不接受无法对上冻结原文的引用作为已确认结论；工具网关不写外部系统。 |
| Operator | 服务对象及运营商对象映射、观察、评估与计划：`service_objects`、`operator_object_bindings`、`operator_external_snapshot_sets`、`operator_fulfillment_evaluations`、`operator_impact_plans`。 | 绑定、采集租约、观察及评估各按现有命令边界提交；外部读取与数据库事务边界不得伪装成跨系统原子提交。 | `Store.CreateOperatorObjectBinding`、`Store.CollectOperatorExternalSnapshot`、`Store.CreateOperatorFulfillmentEvaluation` | 不把外部观察、影响计划或模型输出视作人工授权写回；不跨 scope 读取。 |
| Platform | 通用事务和 RLS、任务/租约、预算、Outbox 与运行控制：`outbox_events`、`model_call_leases`、`model_usage`；`clm_outbox_delivery` 的投递状态与中继属 Platform 能力；`clm_business_events` 的业务事实归 Contract。 | 租约、预算账本和状态由 PG 保障；业务变更与其 Outbox 记录同事务，投递可重试且不等于业务提交。 | `Store.ClaimNextJob`、`Store.HeartbeatJob`、`clmoutbox.Relay.Step`、`Store.ReadTenantRuntimeControl` | 不以 NATS 替代状态或租约权威；不让共享基础设施绕过 RLS 和业务授权。 |

## 既有包与蓝图区别

以下均为**扩展现有包**，不是按 14 个领域重建；包与领域不是一一映射。当前实现未必直接拥有表，持久化仍主要经 `internal/store`，以下边界不决定未审方法的归属。

| 已有包 | 现存边界与蓝图差异 |
| --- | --- |
| `internal/approval` | 审批凭据签发、校验；审批持久化及事务仍在 Store，不等同完整 Approval 存储模块。 |
| `internal/fulfillment` | 冻结输入和观察的履约评估；交付、验收写入仍在 Store。 |
| `internal/audit` | 审计事件链与快照不可改规则；不等同所有审查和审计表的持久化包。 |
| `internal/auditext` | 外部观察、证据及评估契约；横跨 Operator、Fulfillment、Evidence，非单一领域存储。 |
| `internal/decision` | 决策建议输入、结果和校验契约；不替代人工决定历史。 |
| `internal/clmoutbox` | CLM 事件中继及消费接口；Outbox 仍依赖 PG，非独立业务真相源。 |
| `internal/clmsource` | 外部来源身份、能力和回执顺序契约；不等于新建 Contract 域存储。 |
| `internal/coreauthority` | Core authority 传输配置；不替代服务端身份或对象授权。 |
| `internal/execution` | 执行阶段和事件记录接口；是横切执行能力，非部署服务或 14 领域之一。 |
| `internal/identity` | 身份流程及会话接口；不拥有主体领域全部数据，也不替代 Store scope。 |
| `internal/contractmoney` | 合同金额十进制及校验；供合同、付款等复用，不等于新建 Payment 存储。 |

## 四处交界的依据和决定

1. **Audit**：`internal/store/pg.go:99` 的 `RewriteAudit` 尝试更新 `audit_events.actor`，数据库拒绝后才视为预期：含 `append-only` 的错误映射为 `audit.ErrBrokenChain`，其他数据库错误原样返回，若更新被接受则返回错误。`internal/store/migrations/0001_legacy_core.sql:377` 的触发器禁止 UPDATE/DELETE；`internal/store/pg_test.go:238` 验证拒绝。目标保留其拒绝验证语义，不将它作为正常写接口，也不据方法名推断允许重写。
2. **Legal / Knowledge**：`internal/store/legal_knowledge.go:571` 的 `GetLegalSourceCaptureForCase` 先校验案件，再查询已发布或撤回的 legal 类型知识版本是否引用捕获，之后通过同事务读取 Legal 捕获；`internal/httpapi/legal_routes.go:280` 调用此组合读取。目标上 Legal 拥有 `legal_source_captures`，Knowledge 拥有治理版本及引用，案件读取属于跨域应用组合，通过双方公开读取 contract 和同一授权 scope 实现。#149 将此方法暂归 Knowledge，反映其直接 SQL 命中知识表，不改变 Legal 对捕获事实的所有权。
3. **Contract / Platform**：`internal/store/migrations/0018_contract_aggregate.sql:97` 将 `clm_business_events` 绑定合同聚合版本并设为追加，`clm_outbox_delivery` 是关联事件的投递状态；`internal/store/contract_payments.go:438` 在同一事务插入事件和投递记录。`internal/clmoutbox/relay.go:11` 明确中继 Core 所有的合同事件，`Step` 只发布短事件身份并以 claim token 确认。因此 Contract 拥有合同事件事实，Platform 提供投递能力与状态；broker 确认不等于业务完成或用户送达。
4. **Operator / Fulfillment**：`internal/store/operator_audit.go:908` 的运营商命令校验案件 scope、确认映射、快照、冻结尝试和能力包，并持久化运营商评估及检查；它通过 `auditext.Evaluator` 契约调用核验。`internal/fulfillment/fulfillment.go:79` 的实现是无状态专线配置带宽比较，结果要求人工复核，不读取外部服务。目标中 Operator 拥有运营商映射、观察、评估记录与编排；Fulfillment 提供可复用的履约评估能力及其 contract，不能接管运营商评估记录，也不自动确认风险。

## #149 清单核对与迁移范围

依据 #149 清单 `docs/architecture/store-ownership.csv` 固定源码 `9040a61f8410fef46c06106f5874d2b9a6fe1c21`，覆盖 39 个迁移和 556 个 Store receiver 方法：526 行 `UNKNOWN`，原因为直接 SQL 证据不足或存在歧义；30 行是 SQL 字面量支持的**暂定**领域，不是已验证最终方法所有权。已逐组核对 30 行：Approval 2、Audit 2、Identity 3、Document 3、Evidence 3、Knowledge 4、Legal 1、Operator 12。它们指向的表均与上表的数据归属一致；两个组合读取不能把 SQL 命中当成整个方法的唯一归属：

- `GetLegalSourceCaptureForCase` 暂标 Knowledge；其捕获数据归 Legal，跨域应用组合如上。这是方法标注与目标单域位置之间的差异，不是表所有权冲突。
- `tenantConfigCatalogs` 暂标 Knowledge（知识表 SQL 命中），但 `internal/store/tenant_config.go:162` 同时组合平台模型与凭据目录；目标上 Platform 应用组合调用 Knowledge 公开目录，不把整个配置组合永久放入 Knowledge。
- `GetServiceObject` 暂标 Operator。`internal/store/service_objects_store.go` 组合读取服务对象、`service_object_contract_associations` 和运营商映射；该关联固定 material/document/parsed_document 及 review run/attempt，未引用 CLM 合同 ID（迁移 0003）。Operator 拥有此服务对象关联及版本，依赖 Document 的材料版本和 Audit 的冻结尝试，通过相应公开契约及同一授权 scope 读取；只有实际 CLM 聚合数据才调用 Contract。此处 Operator 标签与目标一致，不能把材料关联误拆为 CLM 合同归属。

`UNKNOWN` 仍按 #149 原样保留，不能从本 ADR 自动赋值；后续移动具体方法时以调用链、权限、事务和表证据逐项核实。静态清单不覆盖接口动态分派、反射或其他 build tags。后续变更应核对固定源码与映射；本 ADR 确定目标所有权和依赖方向，未迁移任何业务实现。工具构建及文档由局部检查验证；运行重构、数据库行为和产品交付验收仍为 NOT_RUN。


## 清单与重放

统一方法清单为 [store-ownership.csv](store-ownership.csv)。每行记录完整 source SHA、相对源码符号/行号和调用者线索；只有 STATIC_PATH 行附已观察到的 legacy writer 路径。NOT_OBSERVED_STATIC 表示默认构建未观察到路径，不证明动态路径不存在。UNKNOWN 有原因，测试线索不等于覆盖；默认 Go 构建图不证明接口动态分派、反射或其它 build tags 的穷尽性。完整 catalog 表/迁移、characterization 线索、类型调用边和 #146 触碰路径可在仓外重放：

```sh
python3 scripts/architecture/generate_store_ownership.py \
  --repo . \
  --source-sha 9040a61f8410fef46c06106f5874d2b9a6fe1c21 \
  --out /absolute/path/outside-source/store-ownership
```

工具从固定提交创建临时 Git archive，拒绝仓内或非空输出目录；仅分析默认 Go 构建，解析失败会退出非零。生成脚本身份须随源码一并审阅，只有经审查的 CSV 回填到本目录，其余原始数据保留仓外。


## 架构守卫与渐进登记

活动配置 `scripts/architecture/architecture.json` 使用 schema v3，检查器、schema 和配置需一起升级。v2 只用于读取旧目标基线，不能将活动配置降回 v2 关闭新规则。原有三个命名导入策略保持不变。

新增 `internal/` 业务包立即禁止直接依赖 Store；现有十个直接依赖包按精确名称列为存量豁免。豁免只能缩减，目标 BASE 已移除的项不能重新加入。领域登记的 app、transport、postgres 路径覆盖其子包，登记路径不得重叠；transport 走本域 app，跨域 PostgreSQL 适配器依赖被拒绝。

领域登记初始为空，在首个实际切片迁移时登记真实 Go package。已登记领域和角色路径对照实际目标 BASE 只能扩展，不能静默删除或改派；目标分支前进时读取该固定 SHA 的配置比较，并记录双方 merge-base。缺少可靠基线会阻断检查。检查器读取默认 Go 构建的生产直接导入；测试导入与运行时授权仍分别由测试和服务端边界负责。

## #179 生命周期只读编排的过渡边界

生命周期概览采用一个只读 Repeatable Read 事务，复用现有交付、验收、付款和资产读取及材料授权。响应契约归 `internal/contract/lifecycle`，数据所有权仍分别归原领域。为保留私有授权与既有 DTO 的兼容性，暂允许 Store 的专用概览入口组织这些事务内加载器；该例外仅覆盖读取和投影组合，不新增业务事实、关闭判断、通知或通用事务回调。将四类 DTO 和加载器一次搬出会扩大当前切片，后续随 #148 对应领域迁移逐项替换，旧四个读取接口继续保留兼容包装。

本次读取时间采用数据库事务开始时间；外部事实时间、记录时间与独立来源观察时间分别保留，未知来源时间为 null。`known` 只表示本次授权范围内读取成功，空集合不证明履约完成。租户、合同、外部关联案件和材料授权沿用原服务端入口，失权或版本不匹配时清除视图；不声称跨外部系统的一致快照或快照开始后的即时撤权。

## #287 Contract 草稿创建与台账查询纵切片

此切片仅迁移 `CreateContractDraft` 命令和 `ListContractLedger` 查询的领域模型、不变量、应用编排、端口及 PostgreSQL adapter。`internal/contract/model` 承接草稿模型、格式与金额校验、合同聚合及摘要；`internal/contract/app` 承接草稿创建的校验、请求摘要、回执重放和台账分页；`internal/contract/ledger` 承接台账筛选、游标和投影；`internal/contract/postgres` 只查询或写入合同自有表。

Store 仍是认证、角色门禁、服务端身份解析、RLS scope、事务生命周期、当前合同授权及受限字段加解密的边界。写命令在 Store 已授权事务中创建 adapter；同一个非拥有 `platform.Executor` 仅增加 `Query`、`QueryRow` 能力，不暴露连接、提交或回滚，并继续委托给原事务封装，使每条语句和提交前 proof 复核保持生效。合同写入及幂等回执仍原子提交。

台账 SQL 仅按已校验筛选条件缩小候选行；Store 对每行执行现有授权后，应用层继续扫描直到填满一页可见行，再生成绑定租户、操作者、排序和筛选摘要的游标。投影不含受限供应商字段；未知状态值继续作为原始值显示。已迁移的草稿模型、摘要、创建输入和台账 DTO 通过 Store 类型别名保留兼容入口；包含签署 DTO 的 `ContractDetail` 仍是 Store 兼容 envelope。

当前 `CreateContractDraft` 本身不追加 `clm_business_events` 或 Outbox，因此该切片不新增事件；`ImportLegacySignedContract` 已有的业务事件与 Outbox 写入仍留在原 Store 事务及原位置，未纳入迁移。保存视图、导出、替换草稿和历史导入也不属于本切片。此迁移不改变 HTTP/OpenAPI wire 或数据库 schema。
