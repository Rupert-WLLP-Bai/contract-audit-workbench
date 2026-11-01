# 部署模式

## 主线版本与升级

历史固定基线 `0421831c5ca86fdf56ea6d32939da4f825f08c3d` 的应用兼容 schema 为 `[26,26]`。PR [#140](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/pull/140) 已合入 main `595fe2f`，当前应用源码接受 schema `[28,28]`：既有迁移 0020–0026 建立合同台账、冻结版本、顺序会签及审计关联；0027 增加签署登记与补充协议关系；0028 增加履约交付、验收、缺陷和整改事实。代码合入不代表任一数据库已迁移；数据库升级、部署、回退演练和完整交付验收均为 `NOT_RUN`。实际升级前停写并保留数据库与对象存储恢复点，迁移后使用兼容该 schema 的同版服务。旧二进制不可直接对已升级数据库继续写入；回退只能使用已验证兼容版本或恢复同批快照，不支持删除迁移记录伪造降级。

## 本地 Compose

`make apps-up` 通过 `deployments/local.py` 和 `compose.app.yaml` 迁移数据库并启动 Gateway、Core、Document、Execution、Worker、Model/Tool Gateway 以及三个前端站点。先完成[本地开发](../engineering/local-development.md)中的基础设施和私有配置步骤，并确认容器能够访问模型端点。容器内 `127.0.0.1` 指向容器自身；模型服务若在宿主机运行，应配置容器可达地址。

- `make apps-up` 使用已配置的 Model Gateway。
- `make apps-up-fixture` 使用 Compose fixture 模型端点和 fixture 模型名，便于本地联调；fixture 结果不代表真实模型质量。
- `make apps-down` 停止独立应用服务。
- 分站部署时可设置 `VITE_ADMIN_URL`；留空使用同源 `/admin/rulepacks`。
- 前端可分别构建：`make web-client`、`make web-admin`、`make web-portal`。

私有配置设置 `OBSERVE=1` 与 `GRAFANA_ADMIN_PASSWORD` 后，`apps-up` / `apps-up-fixture` 会一并启动 Collector、Tempo、Prometheus 和 Grafana；`apps-down` 同时停止。Collector 地址为 `http://otel-collector:4318`。Grafana、Tempo、Prometheus 默认仅绑定主机回环端口 13000、13200、19090。导出的执行指标带有有限标签组合，不包含合同原文。

## Kubernetes

Kubernetes 基础清单与本地 k3d 演练不同。先准备目标集群、密钥和存储，再按实际部署清单验收网络策略、数据库角色、对象存储、证据导出凭据与外部依赖。`deployments/k8s/optional/evidence-export.yaml` 需要部署方提供指定 Secret 和字体 PVC。

单机 k3d 仅用于部署和故障行为演练，不能作为生产 HA、跨故障域容灾或容量证明。操作步骤见[k3d 手册](k3d.md)。本手册不宣称线上发布、生产集群接入或密钥配置已执行。
