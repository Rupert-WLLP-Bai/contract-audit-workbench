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

以下均为**扩展现有包**，不是按 14 个领域重建；包与领域不是一一映射。部分领域已有本域 app/postgres 实现，其余持久化仍主要经 `internal/store`；Store 的兼容入口、当前授权和事务管理不等于它继续拥有领域规则。以下边界不决定未审方法的归属。

| 已有包 | 现存边界与蓝图差异 |
| --- | --- |
| `internal/approval` | 根包保留凭据签发、校验；model/app/postgres 已承接模板、送审、决定、转办及冻结历史读取，Store 保留当前授权、事务和公开兼容入口。源码候选的运行验收仍为 NOT_RUN。 |
| `internal/fulfillment` | 根包保留冻结输入和观察的履约评估；CLM 交付、验收、缺陷、整改规则和自有 SQL 已迁入 app/postgres，Store 保留授权事务及旧调用方的窄兼容入口。源码候选的运行验收仍为 NOT_RUN。 |
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

## Agent 步骤恢复与 Store 过渡适配

状态：采纳。现有恢复从持久事件和终态投影续算预算及失败调用，没有保存可重用的成功 planner/tool 输出。增加 Execution 所有的步骤记录及领域端口，复用现有 PostgreSQL、服务角色和租约围栏；Eino 继续负责执行组合，不建立第二个任务或状态权威。

每项记录绑定租户、组织、案件、run、attempt、冻结输入摘要、planner/model/runtime/tool 版本及步骤身份，保存输入与输出摘要、实际观察时间、状态和已耗预算。写入须使用当前执行服务身份，在现有案件→attempt锁顺序下复核活动租约、generation、当前授权和冻结依据；旧 generation 不得继续写。成功输出在步骤成功对后续执行可见前持久化，写失败终止当前执行。未完整保存的步骤不能凭事件名称重建成功。

恢复先覆盖同一 attempt 的 Worker 接管。只有身份、版本和输入完全匹配的记录才可复用；引用与事实仍对冻结原文重新核验，外部观察时间仍是原时间。模型建议不能因来自检查点成为确认结论，预算账本仍决定真实用量；失败、取消、未知调用及未确认用量不会按零成本重放。新 attempt 默认使用既有冻结与重试规则，跨 attempt 的成功输出复用须另有明确兼容与新鲜度证明。

Store 暂保留针对现有执行事务的窄适配入口，以调用其私有授权、任务租约和冻结依据复核能力；新增步骤模型、校验和恢复逻辑进入 Agent/Execution 领域，持久化适配器使用非拥有事务能力。该例外不开放可自报 scope 的通用事务或 SQL 入口，后续按 #148 将其迁入 Execution 持久化端口。已有终态投影、人工决定、模型账本和历史原件不被步骤恢复改写。

不在旧事件 JSON 中塞入完整合同输出，也不使用框架内存检查点替代数据库围栏。若需要新增表，采用追加迁移、强制 RLS 和明确的角色 ACL，声明应用/schema 兼容范围与停写、恢复边界；不改已应用迁移。当前实现及运行验收均为 NOT_RUN，集中实现后执行受影响的重入、崩溃、撤权、旧租约、摘要篡改和预算用例。

## #292 Contract 修订冻结、读取和历史导入的迁移边界

状态：采纳。接续 #287/#291，在现有 Contract 模块内迁移版本冻结、版本查询及历史导入的模型、规则、应用编排和自有持久化；Store 的公开签名保持兼容。此决定不增加业务能力、数据库版本或新的任务平台，现阶段运行验收为 NOT_RUN。

`clm_contract_revision_items` 是 Contract 冻结修订的不可变计划项索引：0028 的复合外键直接引用 `clm_contract_revisions`，插入沿用 `guard_contract_version_child_insert`，UPDATE/DELETE 由追加约束拒绝。交付与验收事实通过外键引用这些计划项，不取得修订计划的所有权。因此该表随 Contract 版本适配器迁移，现有 `insertFrozenContractRevisionItems` 可保留窄兼容转发供尚未迁移的来源观察调用；不能仅按其当前文件名把它归给 Fulfillment。实际交付、验收、付款及资产事实仍归原领域。

Store 暂时承接当前身份、RLS scope、授权事务生命周期、锁及提交前 fence，并通过有明确输入输出的窄端口复用唯一的案件/主体/材料引用 resolver、字段绑定加解密与受限指纹。Contract application 承接校验、请求摘要、回执重放、状态/CAS、快照和结果组装的真实流程；不能把完整原 Store 业务体放进回调。引用 pins 只进入新冻结快照，StageFinal 金额校验、原字节封存、材料当前可见性和历史未知状态均保留。

Contract PostgreSQL adapter 只读写本域聚合、修订、材料快照、封印、计划项索引和导入来源。业务事件复用 Contract 事件端口，投递记录复用 Platform Outbox 适配器，均使用同一个非拥有事务句柄。修订、索引、封印、聚合 CAS、业务事件、Outbox 与幂等回执共同提交或回滚；adapter 不开放任意 SQL、scope 或提交能力。历史导入不伪造会签、签署或专业审核，版本读取也不以当前解析、模板或政策重算旧快照。

## #293 单原件冻结事实建议与执行投影

状态：采纳，实施前决策。现有非治理单合同预检只运行付款比例规则，规划模型也不接收合同正文；一次 planner 调用不能证明主体、金额税口径、期限或例外已经提取。为补足真实材料验收的产品路径，新增有界、带引用的单合同事实建议，仍以单材料受限范围结束，不增加已确认风险或跨材料审查结论。

事实建议归 Execution 的尝试输出，校验和应用规则进入既有 Agent/Execution 模块；模型客户端只承担严格类型的请求与响应，网关继续独占凭据、预算和用量账本。输入只取已冻结且校验通过的唯一主合同及其解析，正文作为不可信数据发送，输入截断和未知值必须显式保留。服务端用 Unicode codepoint 位置、精确引用及原件/解析摘要核对输出；引用定位只证明来源文本存在，不证明模型归纳、签署、工程适配或法律判断正确。

新接纳的尝试须冻结事实能力及 prompt/schema/configuration 身份。历史尝试缺少该配置时保留原执行行为，不能从当前环境补开新调用。物理事实调用复用现有委派 scope、冻结模型配置、预算预约/结算、执行 observer 和活动租约；调用用途明确区分 planner、contract_facts 和 OCR。复用 0045 的 tool 步骤记录保存有界结果及身份摘要；同 attempt 接管须重新验证冻结输入、当前授权和引用，未确定完成的物理调用不得当零成本成功重放。此决定不新增数据库表、服务角色或第二个任务权威。

终态复用 Execution 所有的 `execution_agent_reviews` 哈希投影存储及既有终态输出摘要链，但纯事实输出使用独立、带版本的 discriminator，不能伪装 `five-domain-review/v1` 或制造 Planner/五域字段。当前切片先覆盖非治理单合同路径，与既有治理审查投影互斥；每一代写入一次完整投影，保留原有同代字节幂等约束。旧五域消费者继续只接收五域投影，事实结果经独立 Workspace 字段显示；报告、补证与恢复读取也须按类型分派。日后需要同一次尝试组合两种输出时，先设计一次性组合及兼容规则，不通过覆写同代投影拼接。

Store 暂保留已授权尝试的冻结材料解析、scope/租约复核、步骤 journal 和终态投影的窄适配；新增事实选择、归纳校验、引用绑定和失败语义不进入 Store 业务体。该例外不开放通用 SQL、scope 或跨案件读取，后续随 #148 迁入 Execution 持久化端口。HTTP/Web/真实语料 runner 分别展示事实建议、规划与 OCR 的实际调用，原12份与新项目材料仍分别验收。所有本批运行检查和真实模型/OCR验收均为 NOT_RUN。

## #295 Contract 来源观察与历史持久化迁移

状态：采纳，实施前决策。接续 #292/#294，把既有来源观察的模型、规则、应用编排及 Contract 自有持久化迁入现有 `internal/contract`。本决定迁移现有能力与历史/fixture 支持，不增加真实外部系统接入；用户取消的 #183 保持 NOT_APPLICABLE。HTTP 签名认证、服务端来源配置和公开 wire 不变，运行验收为 NOT_RUN。

Contract 拥有来源到合同的身份、追加式 `clm_contract_source_observations`、合同与案件关联、聚合、修订和冻结计划项。案件的存在、归档状态及授权仍由既有案件边界负责，不新建领域分类。来源凭据、租户和允许案件集合由服务端配置解析，签名请求正文不能提供这些权限。`internal/clmsource` 继续提供来源能力与 `CompareVersion` 契约；不另建接入平台或删除已有历史。

Contract model/app 承接输入校验、克隆规范化、语义及快照摘要、事件幂等、版本/序号不可变判断、归档或推进当前版本的决定与结果组装。Contract PostgreSQL adapter 仅访问本域表，使用调用者同一事务绑定的非拥有 executor；外部合同只能有一个且匹配的案件关联，由 Contract 自有持久化检查。业务事件仍由 Contract 事件端口追加，投递状态由 Platform Outbox 端口处理，禁止 adapter 查询案件表或设置身份、scope、提交与回滚。

Store 仅保留服务身份、写入门禁、RLS scope、事务生命周期与提交围栏，以及两类明确窄桥：其一在同一事务内复核当前服务端允许案件集合，读取指定 tenant/org/case 的存在及非归档状态并取得 `FOR KEY SHARE`；其二复用现有字段绑定 codec 与原始来源载荷 codec。后者只封存准确原始请求字节，绑定 tenant/source/event/observation、字段 `source.raw_body`、用途 `contract.source.observation` 和现有 data epoch，不暴露密钥或任意加密上下文。既有 `clmsrc1:` 历史解码保留；冻结修订继续复用已批准的 DraftContent 字段封存桥。桥不执行来源版本决定、循环、摘要或旧 Store 完整业务体。

一条观察仍为单一 PostgreSQL 事务，保留顺序：案件 `FOR KEY SHARE` → 来源事件 advisory lock → 来源合同 advisory lock → 合同聚合 `FOR UPDATE`。来源观察、可选身份/案件关联、修订/计划项、聚合与当前版本、业务事件和 Outbox 共同提交或回滚。保持事件ID与规范载荷摘要幂等、原始字节独立摘要、来源版本与序号不可变、opaque/monotonic 差异、未知金额不推进当前版本、旧序号仅归档及历史无可信 baseline 不替换当前版本。已应用迁移、append-only、RLS 与服务角色 ACL 不改。

后续随 #148 迁入相应授权/持久化端口并删除过渡桥；不得把旧方法作为 app 的通用 callback，也不扩大成任意 SQL、身份或跨案件入口。源代码完成后再按固定候选执行来源幂等/序号冲突、未知金额、绑定、撤权、密文绑定和事务回滚的定点验收；当前全部 NOT_RUN。

## #303 标准审查阶段与 Store 窄适配

状态：采纳，实施前决策。基于 #302 源码候选 `949d78a4f00ce1f7ac38716c754f2721b6b9549d`。标准审查仍由 Store 四个阶段闭包组织规则、模型资料、引用合并及终态分类；仅把这些闭包传给 `execution.Run` 不构成迁移。Execution app 承接真实编排和纯计划，复用现有 engine、model 与 execution instrumentation。此次先迁标准路径，保留 #289 步骤恢复、#293 单原件事实 discriminator 和 telecom 冻结路由；不重建平台或变更公有 Store/HTTP 契约。

冻结材料、解析及 normalized digest 由既有 Document 路径读取；run/attempt/snapshot、审计及执行输出保持当前持久化责任，不因目录改变 RLS 或数据库服务 owner。服务端构造冻结尝试身份；generation、epoch、claim 和 scope 不接受客户端声明。准备前可能没有最终 input hash，由准备结果明确返回实际摘要、document/parsed 身份及冻结文本，不从当前配置补旧尝试。引用 verified 仅表示对上冻结原文；Finding 仍是建议，人工决定历史独立追加。

本次准许 Store 的专用过渡能力如下，最终方法名可随实现调整，但不能扩成任意 state、SQL 或事务 callback：

- PrepareFrozenInput：复用原材料准备、验证和冻结，以及对应审计事务。
- AppendRuleResults：仅应用 app 生成的 typed findings/evaluations，不在 Store 执行规则或分类。
- MergeCitationDrafts：在原受锁真实前态内调用 app 纯计划并应用明确 typed delta，匹配、去重与 materialize 规则归 app。
- ReadAttemptProgress、FinishAttempt：读取原尝试进度，并沿原 durable/inline 终态提交路径执行 typed 结果。
- 原 attempt recorder 与受限 ID/时钟 codec：保留事件和 ID/时间语义，不暴露原 Store 业务方法或任意函数入口。

引用合并保留 `scopedAttemptMutationInternal` 的 case→attempt→claim 锁序、当前授权、generation/epoch 与提交 proof。Store 私有适配闭包只把真实 fresh loaded state 的有序候选映射为 typed 输入，调用 Execution app 纯 `PlanCitationMerge`，再将显式新增 finding、字段替换及 anchor 追加 delta 应用到同一个 state。按 draft 顺序维护临时视图，保留 first-match、原 anchor 去重键、全部历史引用字段及 ID/时间生成顺序；app 不取得 Store.state，也不执行 I/O。原合并不递增 Finding.Version，不能据此新增事务外读取、版本 CAS 或重算协议。原先仅新建 finding 参与首次 verified 事件判断，仍在该次事务成功后记录。

所有状态写继续从真实 `loadScopedAttemptState` 基线 clone，经原 append-only/service delta/stateDelta/persist；不构造空 state 或覆盖历史 snapshot/Decision。规则发布、引用写入、首次 verified 事件和终态各保持原提交点。适用 audit、job、Outbox 随原事务提交，并非每一步都有 Outbox。deadline 清理、父取消、recorder 失败优先和部分解析/模型错误分类保持，durable 与 inline 终态不能被新流程合并成不同提交语义。

模型配置沿现有 nil 判断和 Warranty 窄协议，复用当前入口已选客户端、冻结 delegated scope 和 observer，不新增探测或 provider client。调用仍经网关预算 Reserve→重授权/dispatch→settle；trace/checkpoint 不替代账本，未知用量不记零。PostgreSQL 继续是任务/租约与业务权威，NATS 仅作短通知。本次不新增 schema、服务角色、默认外部连接器或专业结论。

这些 Store 桥只覆盖本次 typed 标准流程；随 #148 相应 Document/Audit/Execution 持久化和授权 ports 逐项迁出，AR020 清理无消费者适配。SOURCE 覆盖真实规则/引用顺序、错误分类、持久失败、失权/旧 generation 以及终态兼容；并不据此宣称运行通过。整轮重构完成后统一执行检查，当前测试、编译、门禁、PG/app/browser 与真实模型/OCR 全部 NOT_RUN。

## #304 Knowledge 治理的精确依赖与 Store 过渡桥

状态：采纳，实施前决策。基于 #303 源码候选 `45a36d766b1e57b5788bb5ec50167dde99c4f290`。治理创建、命令、版本/目录、历史和政策预览的真实规则迁入 Knowledge model/app，自有 SQL 迁入 Knowledge PG。跨域依赖仍有私有身份和授权入口；保留原单事务内的专用精确事实桥，避免拆提交或让新 adapter 越权读表。此选择是受限过渡，不以旧 Store 业务函数 callback 代替迁移。

Knowledge 自有持久化限于治理 versions、state、pointers、history、idempotency、policy_rule_bindings 六表；PG adapter 只接同一 caller-owned transaction 的非拥有 Executor，不持有 pool、scope 或提交能力，不访问 RulePack、Legal capture、Case/candidate/feedback/lineage、review snapshot、job 或 Outbox 表。Legal 捕获事实仍归 Legal；RulePack 不新增领域，候选等所有权不凭旧 UNKNOWN 清单或服务授权表推定。

本次准许 Store 保留以下命名明确、同事务的私有能力，最终方法名按实际契约调整：

- 精确 RulePack 版本及 pointer 事实：原 manifest bytes、存储 hash/digest、状态与 pointer 身份。复用现有 rulepack 解析契约，Knowledge 自己的 binding、期限、范围、authority 与阈值判定进入 app/model，不回调旧 validateKnowledgeBinding 整体业务。
- 精确 Legal capture 或 batch 来源事实：现有不可变来源、状态、canonical text 和文章身份及来源层完整性验证。Knowledge 判断自身内容的精确绑定，不将来源可定位解释为专业审核通过。
- scope/source 案件及祖先的当前授权、服务端 Org 推导与原 blocking/try case locks：只返回指定身份的必要事实，不开放 scope 枚举或任意 SQL。
- 精确 experience candidate/version/feedback/lineage 事实及当前来源授权：来源层完整性和现有 candidate 自身 completeness 契约保持；Knowledge 对所需条件、内容一致性、依赖、跨案审阅及可发布性的决定进入 app，不把整个 knowledgeCandidate 业务函数隐藏在桥内。

Store 继续 trustedAdmin、Core service owner、写门禁、updateAdminAs 或原读事务、schema advisory lock、transaction-local RLS、当前 source/ancestor grant 和提交 proof。各命令分支保留原依赖读取与错误优先顺序，幂等 replay 也重验当前授权。state revision + contentSHA 与 pointer revision 双 CAS、tombstone/无 ABA、追加 history 和幂等响应在原单事务内共同提交或回滚；案件与祖先锁、case_busy 竞争语义不变。不新增 schema、角色、ACL、Outbox 或事务框架。

公开 Store DTO/方法保留直接字段、keyed literal、JSON tags/顺序、canonical bytes、摘要及 wire。精确同形 alias 可减少重复 DTO 和 mapper；内部形状不同才做有界转换。LegalMetadata 在此表示 Knowledge 不可变文章内容中的 metadata 值，不将 Legal 捕获表改归 Knowledge，也不改变法律审核 NOT_RUN。旧 business knowledge API、Legal 管理分派/案件读取和 Platform 配置聚合保持；若抽取 published-policy catalog，只迁 Knowledge 行读取，不迁模型/凭据聚合。

本次不迁 #305 Resolve、接纳、retry 和 worker frozen read，既有消费者通过兼容入口继续使用精确值和规则。T14/T15/T23/T08 的规则包、知识版本、业务时间与执行尝试含义沿唯一概念源，不新增同义概念源；旧尝试不补当前知识或改写冻结身份。原跨域 state writer 的真实 load→clone→append-only/delta→persist 及适用 snapshot/run/attempt/job/Outbox 原子性不变，不能构造空 state。

本过渡桥随对应领域公开 contract 就绪逐项退场，AR020 检查消费者后删除无用适配。实际包落地后登记架构路径；必要 SOURCE 补真实 CAS/history/幂等失败回滚及当前授权，不复制旧完整矩阵。全部运行检查、PG/app/browser 和真实模型/OCR 仍 NOT_RUN，整轮重构结束后统一验收。

### #304 旧知识版本命名空间兼容读取

状态：采纳，创建接线前的补充决策。现有 `validateKnowledgeVersionFamily` 先在同一条固定 SQL 中检查 `business_knowledge` 的 tenant/id/version 或治理 versions 的 tenant/knowledge_id/display_version 是否占用，再检查治理族是否出现其他 kind。两条 API 的版本命名空间共享冲突边界，但旧 business knowledge 不据此归入新 Knowledge PG 六表。

准许一个专用 `VersionSlotOccupied` 同事务只读事实桥，输入限于服务端绑定 tenant 与精确 knowledge ID/display version，返回 bool/error；保留原两表 EXISTS OR EXISTS 的单条固定查询。app 根据该事实给出原重复版本冲突，随后才调用 Knowledge 自有 PG 的 family-kind 事实并判断。桥不执行内容、生命周期或 family-kind 决定，也不开放 SQL、scope、pool、事务或泛化 callback。此处保留旧单条查询，避免拆成 LegacyExists 与 GovernedExists 后增加查询及改变错误时序；Knowledge PG 不读取 business_knowledge，服务角色/RLS/schema 锁与原事务不变。

本例外只覆盖创建/导入现有命名空间兼容的精确读取；不迁旧 API、不推定其数据 owner。相应公开所有者契约可承接同事务兼容检查后删除该 Store 桥，AR020 继续检查消费者。所有运行检查仍 NOT_RUN。

### #304 来源授权失败的精确读取返回值

状态：采纳，兼容性例外。旧 `GetGovernedKnowledgeVersion` 在来源/祖先授权失败时仍以 `(已加载版本, error)` 返回，错误值之外可能携带受限正文；本次迁移保留新版 app 的零值返回，明确以 `(零版本, 原错误)` 收紧该路径，符合默认拒绝授权的边界。HTTP 原错误码、错误体、公开方法签名与成功响应不变，来源权限仍在原事务和原校验点复核，不改变角色、查询或冻结内容。此决定仅确认来源授权失败的精确内容读取，不据此更改其它读取/写入的错误返回值；必要 SOURCE 须证明已通过内容完整性校验后发生授权拒绝，且返回值不携带正文。运行验证仍 NOT_RUN。

## #305 审查知识选择与冻结校验的过渡边界

状态：采纳，实施前决策。基于 #304 源码候选 `6d3d1014e0a988d48650a0df078f7e2d774a9087`，接续其精确版本读取、纯模型与非拥有事务能力。Knowledge app 承接当前知识候选选择、mandatory/optional 覆盖、适用条件、排序与预算、精确依赖校验、快照封存、Retry 比较和冻结引用读取；不把旧 resolver 或 frozen validator 整体包装成回调。

复用既有 RawVersion、LoadedVersion、VersionReader、ValidateRawVersion、LegalFactsReader 及 model 值，保留 `review-knowledge-snapshot/v1` 的字段、tag、顺序、nil/空数组、canonical bytes/SHA 和历史 Legal cutoff/fingerprint 特例。Knowledge PG 仍只访问自有六表；active-candidate 用固定 typed 查询保持原 knowledge_id/scope_key 排序和 4096 上限，不把管理分页或 Platform summary 当作执行选择接口。

Review/Execution actor 来自原受信 case/run/attempt/claim 与数据库身份解析，沿原 proof、租户 epoch、generation 和当前权限复核。不能复用治理 RecheckAdmin 要求或提权；case/org/业务属性、businessAt/cutoff、依赖身份均服务端构造。准许 Store 在同 caller-owned transaction 下提供以下专用窄能力：

- 当前目标、来源、祖先的 Read/Review grant 与原 blocking/try case locks。
- 精确 RulePack identity、manifest、compiler、active/pointer 事实；不新建 RulePack 领域。
- 精确 Legal capture、时间/身份与已有冻结依据事实；Legal 捕获和 Legal 自身适用性规则仍归 Legal，优先复用已有纯 legalnorm 契约。若实际需要跨域决策公共契约，先列明最小具体接缝，不把整段 Store 业务藏在接口内。
- 冻结 run/attempt/snapshot 的原读取与接纳挂接：app 只返回 sealed snapshot、精确身份和 typed 决定，Store 沿真实 state writer 应用，不开放 Store.state、pgx.Tx/pool、任意 SQL/scope 或通用 mutation callback。

Start 在原 receipt/config/case/assessment hook 取得 app 结果，仍经真实 loadState→hook→clone→append-only/serviceDelta/stateDelta→persist→commit，已有 run/attempt/input/snapshot/audit/job/Outbox/receipt 的原子性不拆。不构造空 state 或覆盖旧快照；独立 readiness 事务不能替代接纳同事务复核。

Retry 按原业务时间解析当前依赖，变化返回冲突要求重新发起；Retry 不自动创建新 run，随后 Start 才按当前 signature 决定复用或新建，旧 snapshot 不改。worker/adoption 从数据库装载原 frozen digest/refs 并重验当前 actor/source/ancestor 权限，不按当前 pointer 重算旧引用。required 保留原 exact selection 与 raw pointer active，不机械改成派生的 Version.Active；optional adoption 不新增 active 要求，而 ReadFrozen 对所读 exact ref 另有 active 要求。原 case→attempt→live claim/generation/tenant epoch/inputHash 围栏与 #289 成功步骤的重读、比对保持。

经验人工使用账本继续原 Decide/source 授权和命令事务；知识行读取接新 contract 只替换精确 facts，不迁模型预算或人工决定。Core 写治理、Execution 只读等服务角色/RLS/schema 不变；预算、租约和 Outbox 仍由 PG 决定，NATS 仍只作短通知。过渡桥随对应公开领域 contract 逐项迁出，AR020 消费审计后删无用适配。必要 SOURCE 固定 canonical 历史、Retry/新 Start 差异、optional adoption/read 差别、接纳及后续 persist 失败回滚和撤权/旧 generation 边界。整轮重构结束后统一运行检查，当前均 NOT_RUN。

### #305 Legal 纯契约与 Subject pin 精确事实

状态：采纳，选择器接线前的具体补充。当前冻结法律依据同时读取 Knowledge 精确版本、Legal 事实历史和 Subject pin，不能把原 `freezeLegalAuthoritiesTx` 整体作为新 Knowledge 回调。先提取实际依赖的最小 Legal-owned 纯值与规则到 `internal/legal/model`：事实版本/条件/缺口/冻结依据、时序选择、party stale/ambiguous 与 all_of 判定、规范化及指纹。Store 保留当前事务的来源事实读取与权限；Knowledge 组合封存知识快照。此处是已有规则的真实依赖抽取，不提前建设 Legal CRUD、平台或空包，#306/#307 仍承接剩余捕获与管理/依据路径。

准许仅为既有 Legal 依据链读取服务端绑定 tenant/org/case 与 pin 的精确 Subject 事实：case_subject_roles 当前绑定头、case_subject_role_versions 当前版本字段、指定 subjects.current_version，以及同案件/角色的 active binding 数。业务主体归 Subject，不能与登录 Identity 混同，不按此给它改物理 owner。桥返回 found/版本/subject/role/state/count 原始值，Legal 纯规则决定 stale/ambiguous；保留原 binding→subject 当前版本→仅在 pin 新鲜时请求同角色 count 的查询/错误先后、原 case scope/授权/锁、service role/RLS，不扩大成通用主体枚举或新授权入口。

Legal 公共纯值可消费 Knowledge 公共值引用及 rulepack/legalnorm 契约，不导入 Knowledge app/PG 或 Store；Knowledge app 可依赖该 Legal 纯契约。A 段泛型 v1 snapshot 只承载 Legal-owned authority 值，避免把 Legal 类型永久归 Knowledge或形成 model 循环；Store 以实际具体类型实例化，Wire/tag/字节与 SHA 不变。后续最小 Legal 值抽取后仍保留这种数据所有权边界，不通过任意函数 callback 绕过验证。实际 Legal Go package 落地后登记真实角色路径；数据库 schema、服务角色及外部连接器不变，所有运行检查仍 NOT_RUN。

## #306 官方来源捕获与已核验知识草稿组合

状态：采纳，实施前决策。基于 #305 已审查源码候选 `c0cd498fa727110a0a1739213572c561f3b0d4fe`，以下窄能力先于 #306 实施明确。

接续 #304/#305，迁移已有来源导入和精确捕获读取，不新增真实外部系统接入。Legal 拥有 `legal_source_captures` 的原始响应、提取文本、状态证据、条文 manifest、请求摘要与有序 imported refs；Knowledge 拥有自己的版本、状态与历史。Legal model/app 复用 `legalnorm.VerifyBaseline` 和现有值类型，PG adapter 只访问自有捕获表，绑定原调用者事务且不设置身份、scope 或 commit。Legal app 声明窄 Knowledge 端口，通过 Store 同事务适配调用 Knowledge app 的批量草稿准备与持久化；跨域草稿值归 Knowledge model。既有 Knowledge app 已组合 Legal app，因此 Legal app 不反向导入 Knowledge app/PG 或查询其表，避免循环；Knowledge PG 不读 Legal 表。

Store 保留原 `commandContext`、trusted admin、Core/只读门禁、mutex、schema advisory lock、tenant governance scope、原 RR 读取事务/写事务及提交前 admin 复验。导入在第一次只读 replay 未命中后才验证数量、快照断言与 official baseline；第二次同事务 replay 在 verifier 和 article-version 覆盖核对之后、服务端 capture ID 和逐篇内容/namespace 校验之前。两个 replay 保留相同 tenant/actor/key、原摘要冲突和已存结果完整性规则，不为重放追加新权限或业务校验。

同一事务内，Legal app 生成服务端 capture 身份、根据已核验全文构建有序条文内容；Knowledge app 先完整批量预检（重复编号、内容、已批准的单 SQL legacy-or-governed 版本槽事实、自己的 family kind），产生精确 bytes/SHA/version ID/refs，全部成功后才插入 Legal 捕获。随后按原顺序逐篇运行精确来源核验并由 Knowledge 自己插入 version、draft revision 1 和 create history。三类 Knowledge 写入与 Legal 捕获共同提交或回滚；不调用普通单篇 Create 的 pointer/read/recheck/receipt 路径，不增加逐篇幂等回执，不自动批准或发布。

准许的过渡桥仅为：绑定同一事务的 Legal replay/insert/exact-capture facts；既有 Knowledge 精确 replay ref/content/capture-identity 验证；同一 tenant/id/displayVersion 的单条 legacy-or-governed namespace facts；现有服务端 ID 生成和身份/提交围栏。复用 #304 的窄 Legal capture facts 校验，保留 digest→metadata→manifest→article 的错误顺序。桥不接收任意 SQL、事务/state/scope 或旧完整业务回调。业务循环、摘要、版本计划、source内容决定和结果组装均在领域 app/model，Store 仅负责事务组合。

重放结果保持原窄验证：先读取 Legal summary，再解析非空有序 refs，逐 ref 由 Knowledge 自有查询只读取精确 tenant/id/version/kind=legal/captureID 的 content SHA。不能替换成完整 LoadExactVersion 而增加正文、pointer、批准、来源或其他早期错误。完整 capture 读取与 Knowledge metadata/reference facts 读取保留各自验证顺序，不能以一种更重的读取改变另一种首错。

保留现有 DTO 直接字段、JSON tags/顺序、scrubbed capture ID 后的 request digest、有序文章 manifest 原字节及摘要、raw/status bytes、DB created_at、历史字段和错误。获取捕获的案件路径仍先当前案件 Read，再查精确 published/withdrawn Knowledge 引用，最后读取指定 immutable capture；该查询经 Knowledge 自己的接口实现，不放入 Legal PG。后续 #307 迁移适用事实管理，#308 迁移 Subject，不扩大本次事务或专业结论。TextCheckStatus PASS 仅代表来源文字核验；LegalReviewStatus 继续 NOT_RUN。

schema45、五个服务数据库角色、RLS、append-only 和 PostgreSQL 权威不改。whole-refactor freeze 前所有测试、编译、门禁、app/browser 与真实模型/OCR 为 NOT_RUN；草稿本身不是实施或运行验收证据。
