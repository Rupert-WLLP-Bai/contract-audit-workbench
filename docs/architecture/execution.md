# 独立服务与异步执行

本地默认可用 `cmd/server` 单进程运行。独立模式将外部流量交给 Gateway，再按职责转发到 Core、Document 和 Execution。Worker 领取 PostgreSQL 任务并调用 Document、Model Gateway 与只读 Tool Gateway。

PostgreSQL 保存任务状态、租约/代次和 Outbox 记录，是恢复与业务状态的权威；NATS JetStream 只发短唤醒通知。收到通知不构成授权，Worker 仍需根据受控数据库状态领取任务。未知模型用量不能被当作已成功结算。

Core、Document、Execution、Model 和 Tool 使用各自数据库角色。新增事务须在同一事务中设置可信的租户/组织/角色范围。跨服务凭据由私有部署配置生成，不能共享本机开发密钥或放入仓库。

单机 k3d 演练可以覆盖部署、调度、副本伸缩、滚动更新与 Pod 故障恢复，但容器仍共享物理主机；不证明物理机高可用、跨故障域恢复或生产容量。详见[k3d 演练](../operations/k3d.md)和[部署模式](../operations/deployment.md)。
