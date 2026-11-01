# 部署模式

## 主线版本与升级

历史固定基线 `0421831c5ca86fdf56ea6d32939da4f825f08c3d` 的应用兼容 schema 为 `[26,26]`；PR [#140](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/pull/140) 合入后的固定源码 `595fe2f` 接受 `[28,28]`。PR [#144](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/pull/144) 已合入 main `1a9eae0`，当前源码接受 schema `[30,30]`：0029 增加本地付款资格核验和可信付款/冲正事实，0030 增加冻结资产计划、人工移交及可信 ERP 入账/冲正事实。完整迁移目录和兼容范围以 [`internal/store/migrations.go`](../../internal/store/migrations.go)、[0029 付款迁移](../../internal/store/migrations/0029_contract_payments.sql) 和 [0030 资产迁移](../../internal/store/migrations/0030_contract_assets.sql) 为准；应用连接通过 [`internal/store/pg.go`](../../internal/store/pg.go) 校验迁移名称、校验和与兼容范围，不会在应用启动时自动迁移。独立迁移作业由部署流程运行（本地编排见 [`deployments/local.py`](../../deployments/local.py)）。Schema 是数据库兼容边界，不是产品发布版本。PR #144 的 `pr-fast-gate` 为 `PASS`；目标环境数据库升级、部署、回退演练和完整候选验收仍为 `NOT_RUN`。真实 finance/ERP 来源接入仍为 `BLOCKED`，本地可信回执处理不代表生产来源已连接。升级前停写并保留数据库与对象存储恢复点，迁移后使用兼容同一 schema 的服务。旧二进制不可直接对已升级数据库继续写入；回退只能使用已验证兼容版本或恢复同批快照，不支持删除迁移记录伪造降级。服务 `/ready` 仅确认服务数据库连接可用（[`internal/httpapi/health.go`](../../internal/httpapi/health.go)），不代表完整业务验收。

## CLM maintenance owner 前置配置

包含 CLM outbox/metrics 迁移的候选版本要求一个数据库级、schema 绑定的 `NOLOGIN` maintenance owner。可信数据库管理员先准备 schema 和独立离线迁移登录；离线迁移登录必须与 Core、Document、Execution、Model、Tool 运行身份不同，且不能是超级用户、`BYPASSRLS`、`CREATEDB`、复制角色或其它角色成员。它需要 `CREATEROLE` 以供独立迁移作业管理服务角色。Schema 必须已存在，并允许该迁移登录使用和创建对象。

维护 owner 名称由数据库和 schema 确定。不要把凭据放入 SQL、命令行参数、日志或本手册示例中。由可信超级用户使用受控的管理会话创建离线迁移登录和 schema，例如：

```sql
CREATE ROLE <offline_migrator> LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB CREATEROLE NOREPLICATION NOINHERIT;
CREATE SCHEMA <application_schema> AUTHORIZATION <offline_migrator>;
```

如果 schema 已存在，应确认它授予离线迁移身份 `USAGE` 和 `CREATE`。然后只在一次性受控管理会话中运行 maintenance owner provisioning 命令。该命令验证两个数据库 URL 指向 APP_ENV 选定数据库，按 `caw_clm_maint_<md5(database:schema)>` 创建或验证 owner，写入 `contractaudit:clm-maintenance:v1:<database>:<schema>` 绑定，并只授予离线迁移身份 `ADMIN FALSE, INHERIT FALSE, SET TRUE`。额外或提升的角色成员、错误绑定、运行身份复用和非超级用户管理连接都会被拒绝。

示例通过部署 Secret 注入 URL，不在命令文本中填写密码：

```sh
APP_ENV=<target> DATABASE_SCHEMA=<application_schema> \
  CLM_ADMIN_DATABASE_URL="$CLM_ADMIN_DATABASE_URL" \
  MIGRATOR_DATABASE_URL="$MIGRATOR_DATABASE_URL" \
  go run ./cmd/migrate provision-clm-maintenance

# 后续 schema 迁移只使用离线迁移身份；不再传入 CLM_ADMIN_DATABASE_URL。
APP_ENV=<target> DATABASE_SCHEMA=<application_schema> \
  MIGRATOR_DATABASE_URL="$MIGRATOR_DATABASE_URL" \
  SERVICE_DATABASE_ROLES=true go run ./cmd/migrate
```

若日志报告 owner 缺失，停止迁移并重新运行前置 provisioning 命令；不要临时把超级用户身份授给服务、把 owner 授给运行角色，或放宽数据库成员权限。owner provisioning 是部署前置步骤，不会自动执行生产写入或迁移。当前文档不表示包含 schema 32 的候选已部署。

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
