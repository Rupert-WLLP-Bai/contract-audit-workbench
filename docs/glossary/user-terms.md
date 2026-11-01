# 用户术语表

> 本页由 `concepts.json` 生成；概念定义、实现状态和 ID 只在机器源中维护。

| 概念 ID | 用户词 | 系统词 |
| --- | --- | --- |
| T01 | 审查案件 | `store.Case` |
| T02 | 合同 | `Contract / clm_contracts` |
| T03 | 合同草稿 | `DraftContent / ContractDraftSummary` |
| T04 | 合同版本 | `ContractRevision / ContractVersionSummary` |
| T05 | 材料 | `MaterialEntity` |
| T06 | 材料版本 | `MaterialVersion` |
| T07 | 审查执行 | `store.Run / web.ReviewRun` |
| T08 | 执行尝试 | `store.Attempt / web.ExecutionAttempt` |
| T09 | 会签轮次 | `ApprovalRound / clm_approval_rounds` |
| T10 | 审批待办 | `ApprovalTask / clm_approval_tasks` |
| T11 | 审查发现 | `store.Finding / web.Finding` |
| T12 | 发现处理意见 | `store.Decision / web.HumanDecision` |
| T13 | 会签决定 | `ApprovalDecision / approval decision history` |
| T14 | 规则包版本 | `RulePackVersion` |
| T15 | 知识版本 | `GovernedKnowledgeVersion` |
| T16 | 合同相关方 | `Subject / SubjectVersion` |
| T17 | 服务对象 | `ServiceObjectHead / ServiceObjectVersion` |
| T18 | 补充材料请求 | `EvidenceRequest` |
| T19 | 材料接收回执 | `EvidenceReceipt` |
| T20 | 整改事项 | `Remediation` |
| T21 | 审查报告 | `ReportRecord` |
| T22 | 原文引用 | `Anchor / contractCitation` |
| T23 | 业务时间 | `BusinessAt / business_at` |
| T24 | 租户 | `Tenant / accounts.User.TenantID` |
| T25 | 组织/部门 | `Organization / accounts.User.OrgID` |
| T26 | 重复操作识别 | `IdempotencyKey / writeReceipt` |
| T27 | 执行租约与接管代次 | `JobClaim / Worker Lease / Generation` |
| T28 | 业务事件与通知投递 | `Core Domain Event / Outbox Delivery` |
| T29 | 验收记录 | `AcceptanceRecord（计划概念）` |
| T30 | 实际支付回执 | `PaymentReceipt（计划概念）` |
| T31 | 身份主体 | `Principal / (issuer, subject)` |
| T32 | 租户成员资格 | `TenantMembership / membership (proposed)` |
| T33 | 当前租户上下文 | `ActiveTenantContext / active context (proposed)` |
| T34 | 授权范围 | `Grant.Scope / dbScope` |
| T35 | 执行尝试冻结快照 | `AttemptInput / RunSnapshot` |
| T36 | 检索相关度 | `KnowledgeCandidate.Score / governedRelevance` |
| T37 | 人工发现 | `store.ManualFinding` |

## T01 · 审查案件

**系统映射：** `store.Case`

组织某项合同审查的材料、访问范围和办理活动的业务容器。

边界：
- 案件 ID 与合同 ID 分属不同对象。
- 创建案件不等于合同已经签订或会签通过。

**误用：** 把 case_id 显示为正式合同编号。

**原因：** 案件办理身份不能替代合同登记编号。

**建议：** 分别展示案件编号和已知合同编号；未知合同编号保留未知。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

Case 类型可定位；本轮未运行案件流程。

源码锚点：
- `store.Case` — [`internal/store/state.go`](../../internal/store/state.go)

相关概念：`T02`, `T07`


## T02 · 合同

**系统映射：** `Contract / clm_contracts`

具有独立稳定身份的合同登记聚合，组织草稿、冻结版本和与案件的关系。

边界：
- 合同可以关联审查案件，但两种身份不能互相覆盖。
- 合同聚合状态不证明外部实际签署、付款或资产入账。

**误用：** 已有合同表，所以会签和履约已经全部可用。

**原因：** 基础登记与完整流程的交付状态需要分别证明。

**建议：** 在能力说明中分别标明登记、会签、履约的实际状态。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

合同表、ContractSummary 和登记读取代码可定位；不据此推定全部流程可用。

源码锚点：
- `clm_contracts` — [`internal/store/migrations/0018_contract_aggregate.sql`](../../internal/store/migrations/0018_contract_aggregate.sql)
- `store.ContractSummary` — [`internal/store/contract_draft.go`](../../internal/store/contract_draft.go)

相关概念：`T01`, `T03`, `T04`


## T03 · 合同草稿

**系统映射：** `DraftContent / ContractDraftSummary`

尚可编辑的结构化合同内容，允许在暂存阶段缺少后续提交必需信息。

边界：
- 保存草稿不等于提交会签。
- 草稿更新不能改写已冻结合同版本。

**误用：** 将‘保存草稿成功’提示为‘合同审核通过’。

**原因：** 保存仅确认草稿写入，不产生审批结论。

**建议：** 草稿已保存；需要时继续补充资料并执行明确的下一步。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

草稿类型及创建/替换方法可定位；本轮未执行保存或冲突测试。

源码锚点：
- `store.DraftContent` — [`internal/store/contract_draft_types.go`](../../internal/store/contract_draft_types.go)
- `store.ContractDraftSummary` — [`internal/store/contract_draft.go`](../../internal/store/contract_draft.go)

相关概念：`T02`, `T04`


## T04 · 合同版本

**系统映射：** `ContractRevision / ContractVersionSummary`

冻结后的合同结构化内容及其选定材料关联，用于后续精确引用和版本差异比较。

边界：
- 合同版本与材料版本分别有身份。
- 冻结表示内容定版，不能自动推导已签订、已批准或真实合同合法有效。

**误用：** 后续更换附件后直接覆盖旧冻结版本。

**原因：** 旧版本的内容和材料依据必须仍可追溯。

**建议：** 追加新合同版本，并保留旧版本及各自材料身份。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

冻结方法、版本读取类型和修订表可定位；产品运行验证未执行。

源码锚点：
- `store.ContractVersionSummary` — [`internal/store/contract_version.go`](../../internal/store/contract_version.go)
- `store.FreezeContractVersion` — [`internal/store/contract_version.go`](../../internal/store/contract_version.go)
- `clm_contract_revisions` — [`internal/store/migrations/0018_contract_aggregate.sql`](../../internal/store/migrations/0018_contract_aggregate.sql)

相关概念：`T03`, `T06`, `T09`


## T05 · 材料

**系统映射：** `MaterialEntity`

一份材料的逻辑身份，用来关联其不同文件版本和业务用途。

边界：
- 同一材料可以有多个版本；当前版本与历史引用版本不能混用。
- 文件名相同不证明材料内容或版本相同。

**误用：** 按 filename 覆盖原文件并沿用所有旧引用。

**原因：** 文件名不足以证明内容身份。

**建议：** 显示材料名称，同时按材料 ID、版本与摘要定位实际内容。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

材料实体及版本关联类型可定位；本轮未运行上传。

源码锚点：
- `store.MaterialEntity` — [`internal/store/materials.go`](../../internal/store/materials.go)

相关概念：`T06`, `T22`


## T06 · 材料版本

**系统映射：** `MaterialVersion`

某材料一次具体文件内容的版本身份，关联实际 document_id 及前一版本。

边界：
- 文件版本、解析结果版本和合同结构化修订不是同一序号。
- 旧审查引用必须指向当时冻结的材料和解析内容。

**误用：** 打开旧审查引用时直接显示该材料最新上传文件。

**原因：** 最新文件可能已改变被引用原文。

**建议：** 打开所选执行尝试使用的冻结材料版本。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

MaterialVersion 类型和材料身份函数可定位；本轮未运行引用定位。

源码锚点：
- `store.MaterialVersion` — [`internal/store/materials.go`](../../internal/store/materials.go)
- `store.MaterialIdentity` — [`internal/store/materials.go`](../../internal/store/materials.go)

相关概念：`T04`, `T05`, `T08`, `T22`


## T07 · 审查执行

**系统映射：** `store.Run / web.ReviewRun`

用户发起审查后可追踪的一项执行身份；它与其下属具体执行尝试分别保存 ID。

边界：
- 一次 Run 可关联执行尝试和重试关系，不能用 attempt_id 替代 run_id。
- AuditRun 在设计讨论中可指此概念，但本轮实际锚点是 Run/ReviewRun，不把它虚构成已验证代码类型。

**误用：** 把每次 Worker 接管计为新的审查执行或新的会签轮次。

**原因：** Worker 接管代次属于后台租约；Run、Attempt、ApprovalRound 身份不同。

**建议：** 分别展示审查执行、执行尝试；后台接管代次只在诊断中展示。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

后端 Run 和前端 ReviewRun 类型已读取；本轮未证明名为 AuditRun 的现有声明。

源码锚点：
- `store.Run` — [`internal/store/state.go`](../../internal/store/state.go)
- `web.ReviewRun` — [`web/src/api/types.ts`](../../web/src/api/types.ts)

相关概念：`T08`, `T09`, `T27`


## T08 · 执行尝试

**系统映射：** `store.Attempt / web.ExecutionAttempt`

某一 Run 之下的一次执行或执行尝试，具有独立 attempt_id、顺序号和重试来源；执行身份及冻结上下文固定，运行状态按既有生命周期变化。

边界：
- Attempt 不是 AuditRun/Run 本身。
- Attempt 绝不是 ApprovalRound 会签轮次，也不应改叫‘审查轮次’。
- 重试关系、Worker 接管 generation、Agent 内部计划轮次是不同层级。

**误用：** 将 ReviewAttempt 文案统一替换成‘审查轮次’，并用于显示会签第几轮。

**原因：** 这样把执行尝试与业务审批混为一体，导致身份、历史和退回规则含义错误。

**建议：** 用户界面用‘执行尝试’或带上下文的‘审查执行尝试’；会签仅用‘会签轮次’。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

state.go 明确注释 Attempt 是 Run 内的不可变执行身份；前端 ExecutionAttempt 同时保留 id 和 run_id。

源码锚点：
- `store.Attempt` — [`internal/store/state.go`](../../internal/store/state.go)
- `web.ExecutionAttempt` — [`web/src/api/types.ts`](../../web/src/api/types.ts)

相关概念：`T07`, `T09`, `T27`


## T09 · 会签轮次

**系统映射：** `ApprovalRound / clm_approval_rounds`

某合同版本从一次送审开始直到该次审批周期结束的业务轮次。

边界：
- 关联合同修订，而不是模型执行 attempt。
- 退回后重新提交应开启新轮；旧批准不能自动算作新轮批准。

**误用：** 把审查任务重试后的 attempt ordinal 显示为合同第二轮会签。

**原因：** 机器执行重试不等于重新提交业务审批。

**建议：** 仅使用真实 approval_round_id 和 round_number 展示会签轮次。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

当前仓库有固定审批流程、版本化路由模板、会签轮次读写与历史决定结构；本次未运行会签流程。

源码锚点：
- `store.ApprovalRoundSummary` — [`internal/store/contract_approval_types.go`](../../internal/store/contract_approval_types.go)
- `store.ApprovalRoundReadSummary` — [`internal/store/approval_read.go`](../../internal/store/approval_read.go)

相关概念：`T04`, `T08`, `T10`, `T13`


## T10 · 审批待办

**系统映射：** `ApprovalTask / clm_approval_tasks`

某一会签轮次中某节点分配给当前合格办理人的审批工作项。

边界：
- 不是后台 Worker Job，也不是既有通用 WorkbenchTask 的无条件改名。
- 任务是否可办由当前服务端权限和状态决定，不只看界面按钮。

**误用：** 把后台 OCR Job 标成‘律师审批待办’。

**原因：** 机器作业与人工审批责任不同。

**建议：** 分别显示执行任务和审批待办，并保留各自身份。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

当前仓库已有会签轮次、审批待办和追加决定历史的类型、读写代码及迁移；本次未运行会签流程。

源码锚点：
- `store.ApprovalTaskSummary` — [`internal/store/contract_approval_types.go`](../../internal/store/contract_approval_types.go)
- `store.ApprovalInboxItem` — [`internal/store/approval_read.go`](../../internal/store/approval_read.go)

相关概念：`T09`, `T13`, `T27`


## T11 · 审查发现

**系统映射：** `store.Finding / web.Finding`

规则或模型审查产生的具体发现，保留来源、引用和核验信息供人工处理。

边界：
- 发现不天然等于已确认风险。
- 发现的来源证据与后来人工处理意见分别保存。

**误用：** 模型返回疑点后直接标注‘风险已确认’。

**原因：** 模型建议不能替代人工决定或确定性核验。

**建议：** 显示发现与待核实状态，由具备权限的人作出处理。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

Finding 类型及前端映射可定位；本轮未运行规则或模型。

源码锚点：
- `store.Finding` — [`internal/store/state.go`](../../internal/store/state.go)
- `web.Finding` — [`web/src/api/types.ts`](../../web/src/api/types.ts)

相关概念：`T12`, `T22`


## T12 · 发现处理意见

**系统映射：** `store.Decision / web.HumanDecision`

人工对特定审查发现作出的确认、误报、证据不足或接受残余风险等处置记录。

边界：
- 旧 Decision 字段和接口保持原义。
- 发现处理意见不是部门会签批准，不产生签订合同的授权。

**误用：** 用户点击 confirm_risk 后自动推进部门审批节点。

**原因：** 对审查发现的处置与会签决定是两个业务行为。

**建议：** 仅更新发现处理轨迹；会签需独立审批命令。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

Decision 与 Decide 方法可定位，动作映射可读；未执行本轮产品验证。

源码锚点：
- `store.Decision` — [`internal/store/state.go`](../../internal/store/state.go)
- `store.Decide` — [`internal/store/reviews.go`](../../internal/store/reviews.go)
- `web.HumanDecision` — [`web/src/api/types.ts`](../../web/src/api/types.ts)

相关概念：`T11`, `T13`


## T13 · 会签决定

**系统映射：** `ApprovalDecision / approval decision history`

当前授权审批人对特定审批待办作出的同意、退回等业务决定。

边界：
- 与 store.Decision 的发现处置语义分开。
- 签名图片或模型建议不能替代真实审批身份、内容版本和决定记录。

**误用：** 复用 finding 的 Decision 表记录各部门通过，并把原意见覆盖掉。

**原因：** 会破坏既有发现处置历史和业务边界。

**建议：** 定义独立的审批决定契约，保存任务、轮次和审阅版本身份。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

当前仓库已有会签轮次、审批待办和追加决定历史的类型、读写代码及迁移；本次未运行会签流程。

源码锚点：
- `store.ApprovalReadDecision` — [`internal/store/approval_read.go`](../../internal/store/approval_read.go)
- `store.ApprovalDecisionResult` — [`internal/store/contract_approval_decision.go`](../../internal/store/contract_approval_decision.go)

相关概念：`T09`, `T10`, `T12`


## T14 · 规则包版本

**系统映射：** `RulePackVersion`

一组受治理规则及其精确版本身份，用于确定审查采用哪些规则和参数。

边界：
- 规则说明网页不是可执行规则包。
- 后续发布规则不能改写旧执行冻结的规则依据。

**误用：** 用当前最新规则结果解释历史审查，而不显示原规则版本。

**原因：** 审查依据发生变化后历史结论不可直接替换。

**建议：** 显示本次尝试使用的规则包及版本。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

RulePackVersion 类型及尝试中的冻结引用可定位；未运行规则发布。

源码锚点：
- `store.RulePackVersion` — [`internal/store/governance_types.go`](../../internal/store/governance_types.go)
- `store.Attempt.RulePackPointerRevision` — [`internal/store/state.go`](../../internal/store/state.go)

相关概念：`T08`, `T15`


## T15 · 知识版本

**系统映射：** `GovernedKnowledgeVersion`

制度、指引、例外或经验的受治理内容版本，附带当前治理状态和适用范围。

边界：
- 草稿或待审知识不能统称‘已发布知识’。
- 检索命中不等于该案件有权使用，也不证明条文当前有效。

**误用：** 把所有 GovernedKnowledgeVersion 记录显示为‘已发布制度’。

**原因：** 类型同时承载状态，不能忽略发布和适用性。

**建议：** 显示‘知识版本’并单列实际状态；仅已发布且适用时显示可用。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

治理知识版本及选择快照类型可定位；未运行审批或发布。

源码锚点：
- `store.GovernedKnowledgeVersion` — [`internal/store/knowledge_governance.go`](../../internal/store/knowledge_governance.go)
- `store.ReviewKnowledgeSnapshot` — [`internal/store/knowledge_governance.go`](../../internal/store/knowledge_governance.go)

相关概念：`T14`, `T23`


## T16 · 合同相关方

**系统映射：** `Subject / SubjectVersion`

采购方、供应方等业务主体的身份及其版本，可与案件中的具体角色关联。

边界：
- 主体不是登录账号，也不是服务对象。
- 更新主体主数据不应静默改写旧合同付款或审查依据。

**误用：** 把 actor_id 当供应商的主体 ID。

**原因：** 操作者身份与合同当事方身份不同。

**建议：** 分别使用当前操作者和所选合同相关方。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

主体及案件角色版本类型可定位；未运行主体核验。

源码锚点：
- `store.Subject` — [`internal/store/subjects.go`](../../internal/store/subjects.go)
- `store.SubjectVersion` — [`internal/store/subjects.go`](../../internal/store/subjects.go)

相关概念：`T17`, `T24`, `T25`


## T17 · 服务对象

**系统映射：** `ServiceObjectHead / ServiceObjectVersion`

合同约定或跨系统核验所指的专线、站点等具体对象及其版本和关联。

边界：
- 服务对象与供应商主体不同。
- 对象映射必须有可信来源和确认，不能仅按名称相似自动认定。

**误用：** 把供应商名称当作专线对象的唯一标识。

**原因：** 供应商可对应多个服务对象。

**建议：** 单独选择服务对象，并查看确认后的合同关联。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

服务对象及关联类型可定位；未运行真实外部映射。

源码锚点：
- `store.ServiceObjectHead` — [`internal/store/service_objects.go`](../../internal/store/service_objects.go)
- `store.ServiceObjectVersion` — [`internal/store/service_objects.go`](../../internal/store/service_objects.go)

相关概念：`T16`, `T23`


## T18 · 补充材料请求

**系统映射：** `EvidenceRequest`

针对已知待补充事项向办理人提出的有来源、状态和历史的补件要求。

边界：
- 提出请求与接收材料分开。
- 补件请求完成应有后续核验依据，不由文件上传成功直接推导。

**误用：** 上传一个附件后把所有补件请求自动标成已核验。

**原因：** 接收事实不能证明各项缺证已经解决。

**建议：** 按请求关联接收回执，并按后续审查结果完成核验。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

请求类型与创建、核验方法可定位；本轮未运行补件闭环。

源码锚点：
- `store.EvidenceRequest` — [`internal/store/evidence_requests.go`](../../internal/store/evidence_requests.go)
- `store.CreateEvidenceRequest` — [`internal/store/evidence_requests.go`](../../internal/store/evidence_requests.go)

相关概念：`T19`, `T08`


## T19 · 材料接收回执

**系统映射：** `EvidenceReceipt`

确认某个补件请求收到哪些材料及相关来源的记录。

边界：
- 收到不等于内容合格或补件核验通过。
- 与银行支付回执不是同一种记录。

**误用：** 将 evidence receipt 翻译为‘付款成功回执’。

**原因：** 两者所属业务、来源和金额含义不同。

**建议：** 使用‘材料接收回执’，实际支付另用 PaymentReceipt 概念。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

接收回执类型和提交方法可定位；本轮未运行接收。

源码锚点：
- `store.EvidenceReceipt` — [`internal/store/evidence_requests.go`](../../internal/store/evidence_requests.go)
- `store.SubmitEvidenceReceipt` — [`internal/store/evidence_requests.go`](../../internal/store/evidence_requests.go)

相关概念：`T18`, `T30`


## T20 · 整改事项

**系统映射：** `Remediation`

对问题的整改安排、提交与复核过程，保留原问题和后续处理关系。

边界：
- 整改完成与原发现消失不是一回事。
- 后续复核应核对明确来源，不能仅凭最近一次执行无问题就关闭全部整改。

**误用：** 重新审查后删除原风险和整改历史。

**原因：** 会丢失发现、措施和复核之间的证据链。

**建议：** 保留原记录，用后续复核结果说明是否完成。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

整改及复核输入类型可定位；未运行本轮整改复审。

源码锚点：
- `store.Remediation` — [`internal/store/business_types.go`](../../internal/store/business_types.go)
- `store.RemediationVerifyInput` — [`internal/store/business_types.go`](../../internal/store/business_types.go)

相关概念：`T11`, `T12`, `T21`


## T21 · 审查报告

**系统映射：** `ReportRecord`

根据指定审查及相关人工处理形成、可以追溯来源的报告记录。

边界：
- 报告不是当前页面所有最新数据的无身份拼接。
- 有报告文件不表示所有真实外部依赖已完成验收。

**误用：** 材料更新后原报告字节随查询自动变化。

**原因：** 历史报告需要保持当时输出和来源关系。

**建议：** 生成新的报告记录并保留原报告。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

ReportRecord 及冻结报告类型可定位；本轮未生成或下载报告。

源码锚点：
- `store.ReportRecord` — [`internal/store/business_types.go`](../../internal/store/business_types.go)
- `store.frozenReport` — [`internal/store/business_types.go`](../../internal/store/business_types.go)

相关概念：`T08`, `T12`, `T22`


## T22 · 原文引用

**系统映射：** `Anchor / contractCitation`

指向指定执行尝试所用冻结原文的引用，包含材料、解析、文本位置和核验所需身份。

边界：
- 任意搜索摘录不等于已核验引用。
- 引用定位成功不等于法律结论或付款条件已成立。

**误用：** 用后来重解析的文本偏移定位旧审查引用。

**原因：** 旧偏移可能落在不同句子或遗漏否定词。

**建议：** 按所选尝试的原文、解析摘要和位置读取并核对引用。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

冻结引用 HTTP 类型与 Anchor 可定位；本轮未执行原文核验。

源码锚点：
- `httpapi.contractCitation` — [`internal/httpapi/contract_citations.go`](../../internal/httpapi/contract_citations.go)
- `store.Anchor` — [`internal/store/state.go`](../../internal/store/state.go)

相关概念：`T06`, `T08`, `T11`


## T23 · 业务时间

**系统映射：** `BusinessAt / business_at`

用于确定本次业务与规则适用性的时点，在审查知识选择上下文中保存。

边界：
- 不是页面加载时间、数据库创建时间或任意浏览器本地时区。
- 更改业务时间需要重新核对适用依据，不修改旧执行的冻结时点。

**误用：** 用户换时区后把原业务日期自动当成另一时点提交。

**原因：** 显示时区与业务判断时点不能悄然变化。

**建议：** 按明确时区录入和解释，并保留冻结 business_at。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

BusinessAt 字段和 StartReviewAt 方法可定位；本轮未运行时区边界验证。

源码锚点：
- `store.ReviewKnowledgeSnapshot.BusinessAt` — [`internal/store/knowledge_governance.go`](../../internal/store/knowledge_governance.go)
- `store.StartReviewAt` — [`internal/store/execution.go`](../../internal/store/execution.go)

相关概念：`T08`, `T14`, `T15`


## T24 · 租户

**系统映射：** `Tenant / accounts.User.TenantID`

服务端可信身份绑定的数据隔离范围，当前租户标识为不透明字符串。

边界：
- 租户不等于部门，也不保证对应一个法人或一个物理部署。
- 不能把 tenant_id 强制改为 UUID 或接受普通请求任意自报租户。
- ‘租户’在管理员、工程和多租户选择场景是准确用词，不作全局禁词。

**误用：** 为让界面更易懂，把所有 tenant_id 和租户权限改成部门 ID。

**原因：** 这会合并不同隔离维度并破坏兼容。

**建议：** 业务页面按需要说明当前租户；仅在关系已经明确时使用工作区展示名，后端标识不变。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

accounts.User.TenantID 和合同表 text tenant_id 可定位；本轮未运行隔离测试。

源码锚点：
- `accounts.User.TenantID` — [`internal/accounts/accounts.go`](../../internal/accounts/accounts.go)
- `clm_contracts.tenant_id` — [`internal/store/migrations/0018_contract_aggregate.sql`](../../internal/store/migrations/0018_contract_aggregate.sql)

相关概念：`T25`, `T26`


## T25 · 组织/部门

**系统映射：** `Organization / accounts.User.OrgID`

租户内用于身份和业务数据范围的组织标识；显示名称可以采用机构的部门名称。

边界：
- 组织与租户是两个维度。
- 界面筛选不构成服务端授权。

**误用：** 把列表按部门过滤后就认为后端无需重验 org_id。

**原因：** 其他路径仍可能访问不该读取的数据。

**建议：** 界面展示所属组织，服务端每次执行真实数据范围校验。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

OrgID 和 Case.OwnerOrgID 字段可定位；本轮未运行跨组织请求。

源码锚点：
- `accounts.User.OrgID` — [`internal/accounts/accounts.go`](../../internal/accounts/accounts.go)
- `store.Case.OwnerOrgID` — [`internal/store/state.go`](../../internal/store/state.go)

相关概念：`T01`, `T24`


## T26 · 重复操作识别

**系统映射：** `IdempotencyKey / writeReceipt`

按具体命令契约识别同一主体的重复请求，并核对请求内容与已有回执。

边界：
- 同键同内容可以复用原回执；同键不同内容不能当正常重试。
- 各现有端点契约保持原样，不因为词表统一而全站强加同一字段或长度。

**误用：** 重试时重新生成请求键，然后把重复写入称为幂等成功。

**原因：** 新键可能表示另一次业务命令，无法靠旧回执去重。

**建议：** 保留原命令身份与请求内容，按服务端回执恢复结果。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

既有写回执、CLM 键校验与内容摘要字段可定位；本轮未做重放验证。

源码锚点：
- `store.writeReceipt` — [`internal/store/write_idempotency.go`](../../internal/store/write_idempotency.go)
- `store.clmWriteReceipt` — [`internal/store/contract_draft.go`](../../internal/store/contract_draft.go)

相关概念：`T24`, `T27`


## T27 · 执行租约与接管代次

**系统映射：** `JobClaim / Worker Lease / Generation`

PostgreSQL 任务领取和接管时用于确认当前 Worker 有效身份的机制。

边界：
- generation 不是审查次数、会签轮次或合同版本号。
- 接管同一任务可改变代次，但不自动建立新的 Run 或业务会签。

**误用：** Worker generation 从 1 到 2 后显示‘合同第二轮审批’。

**原因：** 后台失效隔离状态与业务审批无关。

**建议：** 工程诊断显示‘接管代次’，业务界面只显示可理解的处理或恢复状态。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

Job、JobClaim 的 run_id、attempt_id 和 generation 同时可定位；本轮未运行故障接管。

源码锚点：
- `store.JobClaim` — [`internal/store/queue_types.go`](../../internal/store/queue_types.go)
- `store.Job` — [`internal/store/queue_types.go`](../../internal/store/queue_types.go)

相关概念：`T07`, `T08`, `T10`


## T28 · 业务事件与通知投递

**系统映射：** `Core Domain Event / Outbox Delivery`

业务事务保存领域事件，再由投递记录跟踪通知的异步发送与重试。

边界：
- Core 的合同领域事件与 Execution 的 job Outbox 具有不同所有权和用途。
- 通知发出或消息 ACK 不等于审批、支付或最终用户送达已成功。

**误用：** 为复用执行 Outbox，给合同审批事件伪造一个 worker job。

**原因：** 会混淆服务角色、外键和业务事实。

**建议：** 保留 Core 领域事件和投递记录的独立语义。

**实现状态：** `FOUNDATION_ONLY_NOT_FULL_WORKFLOW` — 当前源码包含相关类型、策略或数据结构，但不足以证明完整用户流程已交付。

Core 领域事件和投递表已有锚点；本轮不证明全部扫描、通知与多副本投递行为已实现。

源码锚点：
- `clm_business_events` — [`internal/store/migrations/0018_contract_aggregate.sql`](../../internal/store/migrations/0018_contract_aggregate.sql)
- `clm_outbox_delivery` — [`internal/store/migrations/0018_contract_aggregate.sql`](../../internal/store/migrations/0018_contract_aggregate.sql)
- `store.OutboxRecord` — [`internal/store/queue_types.go`](../../internal/store/queue_types.go)

相关概念：`T02`, `T27`


## T29 · 验收记录

**系统映射：** `AcceptanceRecord（计划概念）`

记录实际交付范围、验收结论、合格或不合格数量和依据的业务事实。

边界：
- 当前 DraftAcceptancePlan 是验收计划，不是实际验收记录。
- 预计交货时间不等于实际验收时间，部分合格不能代表全部验收。

**误用：** 把验收计划的 planned_date 显示成‘已验收日期’并放行全部尾款。

**原因：** 计划未证明实际交付和合格结果。

**建议：** 分别显示验收计划与实际验收；只按已确认范围判断后续条件。

**实现状态：** `PLANNED_NOT_CODE_VERIFIED` — 提案或需求概念；当前源码没有完整实现。若保留源码锚点，只用于说明现状边界或计划上下文。

当前仅有验收/付款计划结构；计划不是实际验收或支付回执。本概念未定位到实际记录实现。

源码锚点：
- `store.DraftAcceptancePlan` — [`internal/store/contract_draft_types.go`](../../internal/store/contract_draft_types.go)

相关概念：`T04`, `T30`


## T30 · 实际支付回执

**系统映射：** `PaymentReceipt（计划概念）`

来自可信来源、用于证明实际支付事实及其金额和关联的回执记录。

边界：
- 付款计划、付款条件满足和付款申请都不是实际支付。
- 预付款与尾款各有约定条件，不能用一种条件概括全部付款。

**误用：** DraftPaymentPlan 的 amount 有值，就显示银行已支付成功。

**原因：** 计划金额没有支付事实或可信回执。

**建议：** 计划与实际回执分栏；未取得可信事实时保持未确认。

**实现状态：** `PLANNED_NOT_CODE_VERIFIED` — 提案或需求概念；当前源码没有完整实现。若保留源码锚点，只用于说明现状边界或计划上下文。

当前仅有验收/付款计划结构；计划不是实际验收或支付回执。本概念未定位到实际记录实现。

源码锚点：
- `store.DraftPaymentPlan` — [`internal/store/contract_draft_types.go`](../../internal/store/contract_draft_types.go)
- `store.PaymentCondition` — [`internal/store/contract_draft_types.go`](../../internal/store/contract_draft_types.go)

相关概念：`T19`, `T29`


## T31 · 身份主体

**系统映射：** `Principal / (issuer, subject)`

能被可信身份来源稳定识别的主体；显示名变化不应被误当成主体变化。

边界：
- Principal 是授权策略输入中的主体概念；它不等于浏览器自报的角色或租户。当前账户目录的 User 与 access.Principal 是不同类型。

**误用：** 把用户显示名当成永久身份键。

**原因：** 显示名可变且不证明身份来源相同。

**建议：** 以可信 issuer/subject 或系统既有 actor ID 表示身份，并按当前接口契约使用。

**实现状态：** `FOUNDATION_ONLY_NOT_FULL_WORKFLOW` — 当前源码包含相关类型、策略或数据结构，但不足以证明完整用户流程已交付。

accounts.User 与 access.Principal 类型可定位；access 包注释说明其尚未接入 legacy HTTP 应用。

源码锚点：
- `accounts.User` — [`internal/accounts/accounts.go`](../../internal/accounts/accounts.go)
- `access.Principal` — [`internal/access/policy.go`](../../internal/access/policy.go)

相关概念：`T24`


## T32 · 租户成员资格

**系统映射：** `TenantMembership / membership (proposed)`

某身份在特定租户中的有效成员关系及其状态、角色和授权范围。

边界：
- 成员资格不是账号副本；同一主体可以在设计上关联多个租户，但当前 User 只有一个 TenantID，不能据此声称多租户成员关系已实现。

**误用：** 仅在客户端改 tenant_id 就视为加入另一个租户。

**原因：** 租户选择与成员授权必须由可信服务端来源校验。

**建议：** 只有具备当前有效成员关系且通过具体对象授权，才进入对应租户办理。

**实现状态：** `PLANNED_NOT_CODE_VERIFIED` — 提案或需求概念；当前源码没有完整实现。若保留源码锚点，只用于说明现状边界或计划上下文。

当前源码没有多租户 membership 实体；User.TenantID 是一个静态租户绑定字段。

源码锚点：
- `accounts.User.TenantID` — [`internal/accounts/accounts.go`](../../internal/accounts/accounts.go)

相关概念：`T24`, `T31`, `T33`


## T33 · 当前租户上下文

**系统映射：** `ActiveTenantContext / active context (proposed)`

用户在已验证身份与授权范围内，当前办理请求所绑定的租户上下文。

边界：
- 当前上下文是服务器解析的请求范围；不能由浏览器 query、Host 或表单字段自行选择。当前 session 含 tenant_id，但没有多成员切换的 active-context 版本模型。

**误用：** 把页面下拉框选中的 tenant_id 当成授权凭证。

**原因：** 界面选择只表达意图，服务端必须校验身份、成员资格与对象权限。

**建议：** 显示当前租户可帮助用户辨认办理位置；所有读写仍按服务端有效上下文授权。

**实现状态：** `PLANNED_NOT_CODE_VERIFIED` — 提案或需求概念；当前源码没有完整实现。若保留源码锚点，只用于说明现状边界或计划上下文。

当前 session 只保存一个 tenant_id；具有成员选择、切换版本和失效围栏的 active context 尚未在当前代码中定位。

源码锚点：
- `store.session` — [`internal/store/state.go`](../../internal/store/state.go)
- `store.dbScope` — [`internal/store/transaction.go`](../../internal/store/transaction.go)

相关概念：`T24`, `T31`, `T32`, `T34`


## T34 · 授权范围

**系统映射：** `Grant.Scope / dbScope`

授权范围限定主体在特定资源上可执行的动作，包括 self、case、org、org_subtree 或 tenant 等明确范围。

边界：
- 角色说明做什么，范围说明对哪些数据对象做；租户、组织、案件范围不能彼此替代。服务端负责解析范围，未知范围默认不匹配。

**误用：** 同租户就代表可以读取租户内所有合同。

**原因：** 租户归属只是一层边界，具体动作仍需有效角色和对象范围。

**建议：** 同时核验动作、主体、租户、组织/案件范围及资源身份。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

access.Check 明确分开角色与范围并默认拒绝；本次未运行授权场景。

源码锚点：
- `access.Grant` — [`internal/access/policy.go`](../../internal/access/policy.go)
- `access.Check` — [`internal/access/policy.go`](../../internal/access/policy.go)

相关概念：`T24`, `T25`, `T31`, `T33`


## T35 · 执行尝试冻结快照

**系统映射：** `AttemptInput / RunSnapshot`

与一次执行尝试关联、用于解释该次输入与解析身份的不可变材料、解析选择及规则等快照。

边界：
- Run 是审查执行聚合；Attempt 是其中一次执行身份；ApprovalRound 是另一条会签业务历史。重试可以复用冻结输入但仍产生新的尝试身份。

**误用：** 把重新解析后的最新材料覆盖旧 attempt 的输入。

**原因：** 旧结论必须按当次冻结证据解释，后续材料变化应发起新执行。

**建议：** 按 attempt_id 读取对应冻结输入和规则身份，并把新尝试另行记录。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

AttemptInput、RunSnapshot 与冻结解析选择可定位；本次未运行冻结或重试流程。

源码锚点：
- `store.AttemptInput` — [`internal/store/state.go`](../../internal/store/state.go)
- `store.RunSnapshot` — [`internal/store/state.go`](../../internal/store/state.go)

相关概念：`T07`, `T08`, `T09`, `T14`, `T22`


## T36 · 检索相关度

**系统映射：** `KnowledgeCandidate.Score / governedRelevance`

用于候选知识选择或排序的相关性信号，帮助决定哪些内容进入本次上下文。

边界：
- 相关度、向量距离或排序分数不是风险可信度、法律效力、来源真实性或审查结论；被检索到也不代表证据已核实。

**误用：** 检索分数高，所以条款风险已经确认。

**原因：** 召回与排序不证明内容适用于案件，也不代替原文引用和规则核验。

**建议：** 以可核验来源和冻结原文证据支持结论；相关度仅解释选择过程。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

知识规划器存在确定性候选 score；不据此推断存在向量检索或风险置信度。

源码锚点：
- `governedRelevance` — [`internal/store/knowledge_planner.go`](../../internal/store/knowledge_planner.go)
- `KnowledgeCandidate.Score` — [`internal/agentflow/types.go`](../../internal/agentflow/types.go)

相关概念：`T11`, `T14`, `T15`, `T22`


## T37 · 人工发现

**系统映射：** `store.ManualFinding`

审查人员基于一次执行尝试中的冻结原文，追加记录的人工观察。

边界：
- 人工发现保留记录者与原文依据，不覆盖自动生成的 store.Finding，也不修改已有审查结论或报告。
- 人工发现不是对自动发现的处理决定（store.Decision），也不是合同会签或批准决定（ApprovalDecision）。
- 每条记录关联具体 case、run 和 attempt；执行尝试（T08）与会签轮次（T09）是不同业务身份。

**误用：** 将新增人工发现显示成合同已批准或审查结论已确认。

**原因：** ManualFinding 是附带冻结证据的人工观察，不表达审批通过，也不替代审查决定。

**建议：** 将人工发现作为独立记录展示，并单独呈现审查决定或会签决定。

**实现状态：** `CODE_ANCHORED_NOT_RUNTIME_VERIFIED` — 当前源码可定位对应概念或行为；本次未运行应用验证。

store.ManualFinding 是追加式人工观察，写入时核对对应执行尝试和冻结原文；本次仅核对源码与 UI 文案，没有运行产品流程。

源码锚点：
- `store.ManualFinding` — [`internal/store/manual_findings.go`](../../internal/store/manual_findings.go)

相关概念：`T08`, `T09`, `T11`, `T12`, `T13`
