# 建议层与确定性结论

智能体和模型输出只参与建议。引用是否存在、是否定位到冻结原文、规则结果、证据完整性、任务状态和人工决定仍由确定性代码与持久化记录负责。建议模型不直接决定风险严重度或最终结论。

## TypeSafe Jev

Jev 的审查路径只对受治理知识的 optional 候选重新排序；required 候选、确定性分数、候选全集、预算、规则接受结果、引用、关系、缺口状态和人工决定不由它改变。审查路径配置在受理时冻结。系统以 0.70/0.30 阈值将结果归入三档，中间值为 `uncertain`。判断写入不可变 `advisory_judgments`；同一 run 的同一冻结输入复用已有判断。失败、超时、429/5xx、预算不足、模型版本不一致或非法响应时回退到确定性顺序并记录 unavailable 状态；Jev 故障不应影响 chat/OCR readiness。

相关配置：`TYPESAFE_JEV_MODE=off|fixture|advisory`、`TYPESAFE_JEV_EGRESS=minimized`、`TYPESAFE_JEV_FEATURES=knowledge_rerank`、固定的 `TYPESAFE_JEV_MODEL`、`TYPESAFE_JEV_PROVIDER=model-gateway|direct`、`TYPESAFE_JEV_TIMEOUT`、`TYPESAFE_JEV_MAX_CANDIDATES` 和 `TYPESAFE_API_KEY`。生产基础清单默认为 `off`；`advisory` 必须使用固定模型版本，拒绝 `jev-latest` 等 alias。分布式时 key 只在 Model Gateway。

`minimized` 仅发送业务线/场景/合同类型、gap 的 domain/capability/reason 标签，以及治理知识的 kind/summary/check_action/minimum_evidence 等元数据。发送前对 URL、邮箱、电话、日期、金额和长标识做确定性脱敏；治理知识本身也不得写入当事方名称等案件信息。尚未实现的 `approved_text` 模式由客户端和网关拒绝。显式启用需由目标部署环境授权，并满足数据处理审批。生产基础清单默认 `off`；健康页显示启用模式、模型、egress、feature 和 key 是否存在，不泄露密钥或模型答案。

影子规则实验与在线审查是分开的。`cmd/ruleshadow -backend jev` 仅用于 D0 合成 `review_focus` 对照，不进入在线审查。历史单次结果（40/40 强制规则输出不变、39/40 标签一致、p50 300ms / p95 427ms）只表示该次合成实验表现，不代表生产准确率；应按对应固定 SHA 查看仓外实验记录。

相关源码：`internal/jev/`、`cmd/ruleshadow/`、`internal/store/model_budget.go` 与 Model Gateway。证据与命令不在这里重述，按本仓[验证手册](../engineering/verification.md)记录实际执行状态。
