<!-- File metadata (manifest mirror): -->
<!-- Purpose: Describes supported backup tools, tenant lifecycle fences, evidence requirements and production recovery prerequisites without equating local fixtures with completed tenant restoration. -->
<!-- Author: Bai Junhao <baijunhao@cq.chinamobile.com> -->
<!-- Contact: +86 13617696321 -->
<!-- Created: 2026-11-02 -->
<!-- Updated: 2026-11-10 -->
<!-- Revision: r4 -->
<!-- Copyright: See LICENSE -->
<!-- End file metadata. -->

# 备份、恢复与租户生命周期边界

本文说明当前源码中的本地/测试恢复工具、租户状态控制和租户数据恢复前置条件。它不是生产恢复 SOP，也不证明目标环境已部署或演练通过。生产升级、回退、租户数据恢复和跨故障域恢复的现场验收均为 **NOT_RUN**；缺少获批隔离环境或权威备份材料时为 **BLOCKED**。

## 当前源码范围

历史基线 `faeca3938293169f9d7d190b9e4c140feb85d8ae` 使用 schema 39；当前源码要求 schema 44。[0040](../../internal/store/migrations/0040_case_context_shared_fence.sql) 为普通案件写入增加独立授权围栏；[0041](../../internal/store/migrations/0041_intake_session_recovery.sql) 增加原经办人续办所需的受限回执恢复和完成审计；[0042](../../internal/store/migrations/0042_review_receipt_session_recovery.sql) 对审查受理增加当前授权下的精确跨会话回执恢复；[0043](../../internal/store/migrations/0043_review_receipt_origin_org.sql) 将审查受理的发起会话组织与案件组织分别冻结和校验；[0044](../../internal/store/migrations/0044_report_receipt_session_recovery.sql) 对报告生成回执冻结来源组织，允许原用户在当前 Export 授权下精确重放。升级前旧回执缺少可信来源时不会回填猜测，原会话及原上下文仍可重放；新会话对来源不明的报告键收到受控冲突，须按当前权限查看既有报告。原报告字节、摘要和回执印记不因恢复而改写。升级后仍需配置服务角色的函数权限。应用启动只核对迁移，不会自动执行迁移；要求 schema 39、40、41、42、43 和 44 的二进制不可混用，升级或回退前须固定镜像、备份和维护窗口。迁移名称、校验和及兼容范围以 [`internal/store/migrations.go`](../../internal/store/migrations.go)、[`internal/store/table_catalog.json`](../../internal/store/table_catalog.json) 和迁移账本为准。租户生命周期步骤见 [0038](../../internal/store/migrations/0038_tenant_provision_operations.sql)，辖区投影见 [0039](../../internal/store/migrations/0039_jurisdiction_operations_projection.sql)。schema 版本是数据库兼容边界，不证明任何目标数据库已升级。

PostgreSQL 保存任务和业务状态；NATS 只承载短通知。恢复时必须分别核对数据库快照、原件对象版本与摘要、外部密钥管理中保存的密钥版本引用。当前受限字段使用版本化 `CLM_ENVELOPE_KEYS`；旧 `clmv1:` 数据还依赖显式 legacy key ref。恢复所需密钥材料必须由受控密钥系统按授权恢复，不能从数据库、`CLM_DATA_KEY` 或空值推导。保留回滚窗口仍需的旧版本 key。

当前仓库提供两类受限工具：

- `deployments/scripts/restore-local.py` 将本地环境快照恢复到同一 PostgreSQL 集群中的新数据库和新的对象 bucket 或目录。它不覆盖源、不启动 Worker、relay 或模型调用，并拒绝 `APP_ENV=production`。这只证明脚本覆盖的本地 `BACKUP_RESTORE` 路径，不证明生产 RPO/RTO、多节点隔离或灾难恢复。
- `deployments/scripts/backup-test-db.sh`、`restore-test-db.sh` 与 `deployments/scripts/test-recovery.py` 面向标记为 `environment=test` 的 CNPG 测试集群和新的隔离目标。流程核对 PITR 与远端原件对象版本、大小和摘要；需要写入静止、S3 versioning 和预先准备的空目标。它不抽取单一租户，也不证明生产恢复。

## 租户开停与恢复不是同一操作

当前源码提供有审计记录的租户开通、停用、重新激活操作。停用开始时，服务端将租户标为 suspended 并递增 `live_epoch`，之后必须完成 usage reconciliation；未对账的未知费用 hold 会阻止操作完成。重新激活还要求租户处于 suspended、已有有效配置，并核对辖区、组织单元、启用的租户管理员 membership 与指定配置版本。状态和 epoch 由生命周期操作推进；再次导入 identity crosswalk 只初始化新 tenant 状态，不能覆盖已有状态或 epoch。

平台生命周期操作要求当前身份角色为 admin，并同时通过 `CAW_PLATFORM_OPERATOR_ACTORS` 精确 actor ID 白名单及 `CAW_PLATFORM_OPERATOR_DATABASE_PRINCIPALS` Core 实际 PostgreSQL login 白名单。两项默认空；普通租户管理员不会自动成为平台运营者。每个操作使用幂等键、操作 revision 和逐步回执。步骤返回冲突、外部结果未知或费用无法对账时，停止重试，核对 operation ID、服务端状态和外部事实后再续办。没有现场授权和逐步记录时，不要直接修改状态表或 epoch。

停用或租户运行围栏可以阻止旧 epoch 的请求继续通过服务端检查，但不等于完成数据库/对象恢复、全部任务排空或外部副作用对账。设置页中的“停止后续模型发送”是另一项租户管理员控制，只停止后续模型发送，不代表停掉所有租户任务；已发出调用仍按服务端预留结算。

## 单租户数据恢复的前置条件

当前源码没有从共享库中抽取 tenant 数据、恢复到专属池或切换恢复 placement 的通用命令。`live_epoch` 和状态生命周期提供权限/运行围栏，不是数据迁移或恢复程序。恢复之前至少需要：

1. 获得数据所有者与授权方对租户、源、目标、时间点、恢复范围和操作者的书面确认。精确识别 tenant 与 org，不以展示名称或导出文件名推断归属。
2. 固定与目标 schema 兼容的源码、镜像 digest、迁移账本、数据库快照和对象版本。准备新隔离目标；不得覆盖共享源库。
3. 逐租户核对数据依赖闭包、RLS/授权、冻结证据引用、案件/人工决定历史、幂等记录、模型费用预留/结算/未知 hold、Outbox 和其它外部副作用。不得从较早备份覆盖较新的决定或把未知费用当零。
4. 从受控密钥管理系统准备对应租户与用途的 CLM key ID/version 引用，并确认旧值需要的 legacy key 仍可读。不得写入或记录密钥明文。
5. 通过获批生命周期操作停止新准入并按要求完成 usage reconciliation；同时由交付方验证在途 Worker、租约、relay、模型调用和外部写入已静止或逐项对账。递增 epoch 和 DNS/Ingress 切换都不能代替单写者证明。
6. 在隔离目标恢复后，对数据库记录、对象版本/摘要、密钥引用、权限边界与外部副作用做双租户对照，确认目标恢复了获准数据且其它租户未改变。恢复 queued work 前逐项审阅，不自动重放旧 provider 调用。
7. 由授权方确认旧源不能继续写、目标接受当前 lifecycle epoch，并批准重新开放。失败时隔离目标并保留证据；不回写共享源、不删除迁移记录、不以旧快照复活权限。

在通用租户提取/导入工具、端到端单写者围栏和真实隔离演练完成前，租户数据恢复为 **BLOCKED** 或 **NOT_RUN**，按实际原因记录。不得把停用/重新激活 API、本地 restore、迁移成功、`/ready` 或单机 k3d smoke 描述为租户恢复 PASS。

## 发布、回退与支持路线

部署前固定源码 SHA、镜像 digest、迁移清单、密钥版本引用和目标兼容范围。迁移 ledger 是已应用迁移的权威记录；应用镜像回退不会撤销数据库迁移。Kubernetes 发布按 [`deployments/scripts/release-k8s.py`](../../deployments/scripts/release-k8s.py) 的 release lock 流程执行。`rollback-k8s.sh` 只回退应用，不回退数据库。

只有旧 release lock 与迁移状态兼容，且已有混合服务窗口证据时才回退应用。缺少兼容证据时停止写入，按批准的恢复点恢复数据库与对象。不得删除迁移记录伪造降级，也不得让旧二进制写入不兼容 schema。

分批支持时，先让管理员核对租户状态、epoch、配置版本、预算快照和费用对账；需要案件内容再申请到期的 `case.read` 只读支援；跨租户开停由双白名单平台运营者按生命周期步骤处理；只有满足上节前置条件后，才进入专属目标恢复演练。目标环境部署、生产升级/回退、tenant restore 和跨故障域演练目前均 **NOT_RUN**；外部权限或环境材料缺失时 **BLOCKED**。

## 恢复证据

实际演练应在仓外受限目录记录固定源码和镜像身份、迁移 ledger、备份及对象版本、密钥版本引用、操作者审批、命令退出码、核验摘要和恢复后的业务检查。不得记录密钥明文、数据库密码、服务 token 或合同正文。

结论至少说明执行范围、源与目标身份、时间点、对象与数据库核验、权限检查、未覆盖数据类型，以及是否启动后台任务或外部调用。只报告实际执行步骤；未执行项写 **NOT_RUN**，外部条件阻断写 **BLOCKED**。
