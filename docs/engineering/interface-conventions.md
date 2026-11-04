# 业务界面工程约定

本文面向新增或修改业务界面的贡献者，说明应沿用的边界和增量采用的要求。下文的“已有”指仓库中的相应实现，并不表示所有页面、部署环境或用户旅程均已完成；“增量”适用于后续变更，功能和验收进度以各自 Issue 为准。本文不定义新的接口字段或术语词典；概念以 [concepts.json](../glossary/concepts.json) 为唯一机器可读来源，业务和系统含义参见[术语页面](../index.md#术语)。

## 业务展示与稳定身份

**已有：** [`case-identity.ts`](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/blob/main/web/src/domain/case-identity.ts) 复用 `CaseProfile.number` 及案件列表投影展示和查找案件业务编号，并保留完整案件标识以区分选项；[`workflow.ts`](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/blob/main/web/src/domain/workflow.ts) 用审查和执行标识组合选择历史执行。

**增量：** 案件业务编号可编辑、可缺失，也不同于合同编号；不得把它当作唯一键。链接、查询、缓存、诊断和证据选择使用相应的案件、审查、执行、材料版本、解析和规则快照等稳定身份。标题、名称、时间、数组位置或截短摘要都不能替代这些身份。同名或缺编号的选项应显示授权范围内足够的客户、供应商、负责人或时间等上下文，并保留完整机器标识供区分和复制。显式链接指向的案件或执行不可用时，应说明不可用，不静默切换到其他记录。

## 名称、状态与颜色

**已有：** [`business-labels.ts`](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/blob/main/web/src/domain/business-labels.ts) 等领域代码维护已有展示标签，并对部分缺失和未知枚举分别回退；不同模块的存量处理并不完全一致。[工具台账](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/blob/main/web/src/pages/workbench-module-page.tsx) 对其特定来源将能力重复作为名称的情况有兼容处理。

**增量：** 字段要说明由谁提供显示名、是否为枚举。已知枚举复用其领域已有标签；来源契约明确提供的合法业务名称、原文和人工理由原样保留，不按 ASCII 或非 ASCII 推测含义。对未知枚举使用中性的中文“待核对”等提示，并在有权访问的技术详情中保留准确原值；缺失应与未知区分。兼容历史生产者时限定来源和范围，新生产者应明确显示名契约，不累积通用字符串猜测。新增字段、枚举或事件时同步检查业务视图、技术详情、选择器、筛选和现有用户导出，覆盖已知、未知和缺失值。

声明的审查范围、实际检查覆盖、执行状态和人工办理进度应分别表达；文件格式、来源分类、摘要匹配、执行完成或检索排名不代表内容真实、法律有效、审批通过或风险可信度。合同、案件和记录列表使用白底，顶部汇总透明；状态和分类复用现有语义颜色 token，数字保持中性，未知状态不用成功色。合同引用结果的业务显示仍由 [#244](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/issues/244) 单独验收。

## 时间含义与诊断

**已有：** [`time.ts`](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/blob/main/web/src/domain/time.ts) 区分未记录与格式异常，提供北京时间分钟及带秒和实际偏移的详细展示；[`InvalidTimeDetail`](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/blob/main/web/src/components/invalid-time-detail.tsx) 可保留异常原值。现有使用范围需逐页面核对。

**增量：** 对审查执行，`accepted_at` 是服务端受理时间，`started_at` 是执行开始，`finished_at` 是执行结束，`updated_at` 是对应记录更新；不得互相冒充。列表可显示到分钟，详情或需要区分同一分钟执行的选项显示到秒并标明实际展示时区和偏移。未提供显示“未记录”，非法值显示“时间格式异常”并提供可复制原值；不改写原始时间或响应。时间不能充当稳定身份、请求代次或授权证明。

## 查找、聚合和幂等

**已有：** [案件列表](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/blob/main/web/src/pages/cases-page.tsx) 对已加载案件先筛选、排序再分页；[待处理命令的幂等键](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/blob/main/web/src/api/idempotency.ts) 绑定操作、当前身份上下文、目标和请求内容。

**增量：** 查找需说明范围：本地筛选只覆盖当前授权且已加载的数据；只有确实完整加载相应授权集合，或由服务端对相应授权集合执行查询，才能称覆盖该范围的全部结果。服务端分页时，不能把对当前页的筛选称为全量查找；数量应与实际查找范围和当前筛选一致。展示聚合只合并同一底层事项的重复投影并保留来源及入口，不按同标题、条款或相近时间吞掉不同来源、执行和独立决定。命令幂等按现有服务端契约和幂等键处理重试与新意图，不能用显示聚合代替。待判断、补件和整改导航的聚合与状态刷新由 [#225](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/issues/225) 验收；执行泳道搜索、数量和图/列表反馈的一致性由 [#234](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/issues/234) 验收。

## 对照、原文与人工决定

**已有：** [版本对照页](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/blob/main/web/src/pages/compare-page.tsx) 按左右执行的冻结材料与解析身份选择内容，并检查修订对照来源；[原件阅读器](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/blob/main/web/src/components/original-document-reader.tsx) 在支持的 PDF、PNG、JPEG 原件和冻结文字间切换，读取时核对原件摘要。逐条定位、对照和其他格式仍有单独的后续范围。

**增量：** 对照一旦解析左右两侧，就固定各侧的案件、审查、执行、材料版本和解析身份；仅缺省的旧链接可从授权响应解析一次，显式无效选择不得回退。每侧只能读取该侧冻结来源；旧记录未返回身份时说明未知，不把缺字段当作明确没有执行。修订、前序决定等派生结论需有能同时对应所选两次执行的实际来源；来源不足时保留各侧可读证据，停用无法证明的派生结论。规则、范围、解析或材料身份变化应明确提示；发现消失不等于风险消除。

每条引用分别绑定冻结原件、解析和执行，文字引用核验与原件页上的几何定位分别报告。文字核验通过不代表已定位；一个框或一条成功引用不代表全部引用成功。无法唯一匹配、缺页、质量不足或原件不可用时说明原因，不猜框、不升级证据状态。PDF/扫描图片标注是覆盖显示，不修改原件；DOCX 可阅读的解析文字不等于可标注版式预览。模型输出只是建议；缺引用或不能对应冻结原文时，不得成为已确认结论，人工决定只追加历史。逐条几何结果与有界 OCR 缓存由 [#224](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/issues/224) 验收，两侧原件与报告回链由 [#226](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/issues/226) 验收，DOCX 派生版式及其来源绑定由 [#227](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/issues/227) 验收。

## 长页面、异步结果与权限

**已有：** [折叠区与部分页内导航](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/blob/main/web/src/components/collapsible-section.tsx) 以及案件列表的 URL 筛选和位置恢复可供复用；[合同引用面板](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/blob/main/web/src/components/contract-references-panel.tsx) 已按资源、读取身份和查询代次隔离部分异步结果。

**增量：** 长页面围绕当前任务组织，常用阻塞原因、待办和操作可发现，次要历史和诊断按需展开；相关原文和风险应能同时核对。直达、刷新及前进后退应展开目标的所有必要祖先，等待合法的异步目标，缺失时给出回退；键盘焦点落在实际标题或操作，保持可见且不被固定区域遮挡。返回时恢复仍有权访问的筛选、页码、选择和阅读位置；案件、审查、执行、身份或租户变更及撤权后重新校验或清除失效状态。完整导航链由 [#233](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/issues/233) 验收。

加载、成功、失败、复制反馈和缓存都必须属于当前资源、读取身份和请求代次；授权重取可能复用旧数组甚至相同时间戳，不能仅凭数组引用或时钟认定请求未变。切换 A→B→A、撤权或取消后，不发布旧请求或识别任务的结果；缓存按当前授权身份、资源及冻结版本隔离并有界，敏感原文和表单不进入跨身份恢复缓存。扫描页识别缓存还须区分案件、原件摘要、解析或执行和页码，并在身份、租户、版本变化或撤权时失效。服务端仍是授权权威，隐藏控件不代替每次访问和写入的权限检查。

## 导出与冻结事实

**已有：** [审查报告](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/blob/main/internal/store/business_reports.go) 为指定终态执行创建并保存 JSON 和 HTML 字节；[合同台账](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/blob/main/internal/store/contract_ledger.go) 创建时保存导出列和行，下载时生成 CSV 表头和内容，重新检查授权。两类出口的冻结方式不同。

**增量：** 新的用户可读投影在创建时确定，保留准确机器字段及其兼容约定；不能因标签、模板或代码更新重写历史报告、冻结行和原有下载字节。CSV 表头也参与下载字节，仅保留旧行不足以保证旧结果不变；修改时保留旧表头和列顺序的渲染契约或明确版本化，并覆盖旧导出回读。来源类型不等于真实性核验。新的中文 CSV 与旧格式兼容由 [#240](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/issues/240) 验收。

## 贡献检查

在受影响范围内核对已知、未知和缺失值；同名/缺编号对象；不同来源的相同标题；时间异常；明确无效链接；两侧冻结身份和引用失败；异步重取、撤权、A→B→A、返回与焦点；用户导出和旧字节。需要验证同一组件状态延续时，测试应在保留 Provider 的同一实例上重渲染，主动以 key 重建是另一种情形。

按[检查与验收](verification.md)记录实际源码 SHA、命令、范围及状态。局部检查和文档完成不能替代 [#95](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/issues/95) 的业务上手、[#97](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/issues/97) 的固定候选回归、[#98](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/issues/98) 的真实模型/OCR及容量恢复、[#99](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/issues/99) 的试点与交接验收。
