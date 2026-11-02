# 部署模式

## 主线版本与升级

历史固定基线 `0421831c5ca86fdf56ea6d32939da4f825f08c3d` 的应用兼容 schema 为 `[26,26]`；PR [#140](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/pull/140) 合入后的固定源码 `595fe2f` 接受 `[28,28]`。PR [#144](https://github.com/Rupert-WLLP-Bai/contract-audit-workbench/pull/144) 已合入 main `1a9eae0`，当前源码接受 schema `[30,30]`：0029 增加本地付款资格核验和可信付款/冲正事实，0030 增加冻结资产计划、人工移交及可信 ERP 入账/冲正事实。完整迁移目录和兼容范围以 [`internal/store/migrations.go`](../../internal/store/migrations.go)、[0029 付款迁移](../../internal/store/migrations/0029_contract_payments.sql) 和 [0030 资产迁移](../../internal/store/migrations/0030_contract_assets.sql) 为准；应用连接通过 [`internal/store/pg.go`](../../internal/store/pg.go) 校验迁移名称、校验和与兼容范围，不会在应用启动时自动迁移。独立迁移作业由部署流程运行（本地编排见 [`deployments/local.py`](../../deployments/local.py)）。Schema 是数据库兼容边界，不是产品发布版本。PR #144 的 `pr-fast-gate` 为 `PASS`；目标环境数据库升级、部署、回退演练和完整候选验收仍为 `NOT_RUN`。真实 finance/ERP 来源接入仍为 `BLOCKED`，本地可信回执处理不代表生产来源已连接。升级前停写并保留数据库与对象存储恢复点，迁移后使用兼容同一 schema 的服务。旧二进制不可直接对已升级数据库继续写入；回退只能使用已验证兼容版本或恢复同批快照，不支持删除迁移记录伪造降级。服务 `/ready` 仅确认服务数据库连接可用（[`internal/httpapi/health.go`](../../internal/httpapi/health.go)），不代表完整业务验收。

## CLM maintenance owner 前置配置

包含 CLM outbox/metrics 与 tenant membership 的候选版本要求独立离线迁移登录和两个数据库级、schema 绑定的 `NOLOGIN` owner。离线迁移登录必须与管理员及 Core、Document、Execution、Model、Tool 运行身份不同，并为 `LOGIN CREATEROLE NOINHERIT NOSUPERUSER NOBYPASSRLS NOCREATEDB NOREPLICATION`。

部署按顺序运行 `provision-offline-migrator`、`provision-clm-maintenance`、`provision-tenant-identity`，再以仅有离线迁移 DSN 的普通迁移容器执行 schema 与 service-role 迁移。三个预置步骤只在短命管理容器接收 admin DSN。所有步骤按 `APP_ENV` 选择实际数据库槽，五个 `CORE_DATABASE_URL`、`DOCUMENT_DATABASE_URL`、`EXECUTION_DATABASE_URL`、`MODEL_DATABASE_URL`、`TOOL_DATABASE_URL` 必须指向同一数据库且为不同运行身份。

`provision-offline-migrator` 在显式 development/test 安装中可新建带数据库/schema 绑定的独立登录和 schema；production 必须由管理员预先审阅角色及其绑定，缺失时停止，不自动提权。已存在登录必须真实用给定密码认证，命令不重置它或运行角色的密码，不修改旧超级账号。对旧库，只接受完整校验通过的迁移记录、已知应用对象、受控管理员或绑定 owner 的所有权、受审 ACL 与五个精确 service-role 绑定；外来对象、额外成员或其它数据库/schema 权限均拒绝。只转移当前 schema 中经审计的旧 admin 应用对象，不执行全库 `REASSIGN OWNED`。迁移者获得 schema `USAGE/CREATE` 及转授权；两个 owner 成员关系仅为 `ADMIN FALSE, INHERIT FALSE, SET TRUE`。已有五个服务角色的必要离线管理授权仅为 `ADMIN TRUE, INHERIT FALSE, SET FALSE`，运行角色不能反向获得 owner 权限。生产手工预置角色需使用相同的 `contractaudit offline migrator:<database>:<schema>` 注释绑定；不应为绕过拒绝而修改未知角色绑定。

维护 owner 名称由数据库和 schema 确定；owner provisioning 会校验绑定和完整权限，未知权限不会被静默清除。不要把密码放入 SQL、命令行参数或日志。

示例通过部署 Secret 注入 URL，不在命令文本中填写密码：

```sh
# 五个运行 DSN 与 APP_ENV 的目标槽由私密部署环境注入。
APP_ENV=<target> DATABASE_SCHEMA=<application_schema> \
  AUTH_ADMIN_DATABASE_URL="$AUTH_ADMIN_DATABASE_URL" \
  MIGRATOR_DATABASE_URL="$MIGRATOR_DATABASE_URL" \
  go run ./cmd/migrate provision-offline-migrator

APP_ENV=<target> DATABASE_SCHEMA=<application_schema> \
  CLM_ADMIN_DATABASE_URL="$CLM_ADMIN_DATABASE_URL" \
  MIGRATOR_DATABASE_URL="$MIGRATOR_DATABASE_URL" \
  go run ./cmd/migrate provision-clm-maintenance

APP_ENV=<target> DATABASE_SCHEMA=<application_schema> \
  AUTH_ADMIN_DATABASE_URL="$AUTH_ADMIN_DATABASE_URL" \
  MIGRATOR_DATABASE_URL="$MIGRATOR_DATABASE_URL" \
  go run ./cmd/migrate provision-tenant-identity

# 后续 schema 迁移只使用离线迁移身份；不再传入任何 admin DSN。
APP_ENV=<target> DATABASE_SCHEMA=<application_schema> \
  MIGRATOR_DATABASE_URL="$MIGRATOR_DATABASE_URL" \
  SERVICE_DATABASE_ROLES=true go run ./cmd/migrate
```

若日志报告 owner 缺失，停止迁移并重新运行前置 provisioning 命令；不要临时把超级用户身份授给服务、把 owner 授给运行角色，或放宽数据库成员权限。owner provisioning 是部署前置步骤，不会自动执行生产写入或迁移。当前文档不表示包含 schema 32 的候选已部署。

## 租户身份 owner 前置配置

包含 tenant membership 的版本还要求一个独立于 CLM maintenance owner 的 `AUTH_FENCE` NOLOGIN/NOBYPASSRLS 角色。可信管理员须先准备应用 schema，并让离线迁移身份拥有 schema（或明确授予它可转授的 CREATE 权限）及 USAGE；由 `cmd/migrate provision-tenant-identity` 校验目标数据库、schema 和迁移身份后，创建或验证按数据库和 schema 唯一绑定的 `caw_auth_fence_<md5(database:schema)>`，并只授予离线迁移身份 `ADMIN FALSE, INHERIT FALSE, SET TRUE`。schema CREATE 的临时授权用于迁移中转移四个受限函数所有权，迁移后立即撤销。运行期服务角色不加入该角色。迁移失败或 owner 缺失时应停止；不得把 AUTH_FENCE 授给 Core 或其它运行角色，也不得用目录文件权限回退。

先用可信管理连接预置身份 owner，再由独立迁移登录执行迁移。两个 URL 通过部署 Secret 注入，不把密码放进命令文本：

```sh
APP_ENV=<target> DATABASE_SCHEMA=<application_schema> \
  AUTH_ADMIN_DATABASE_URL="$AUTH_ADMIN_DATABASE_URL" \
  MIGRATOR_DATABASE_URL="$MIGRATOR_DATABASE_URL" \
  go run ./cmd/migrate provision-tenant-identity

# schema migration remains an independent offline job.
APP_ENV=<target> DATABASE_SCHEMA=<application_schema> \
  MIGRATOR_DATABASE_URL="$MIGRATOR_DATABASE_URL" \
  SERVICE_DATABASE_ROLES=true go run ./cmd/migrate
```

显式 identity crosswalk 的真实组织映射须由授权方提供并单独审阅；本命令仅准备数据库 owner，不导入或推断任何人员、租户、组织、角色或案件授权。当前实现不表示生产 crosswalk 已导入或多租户切换已启用；#128 的事务围栏完成前，生产上下文切换保持关闭。当前源码迁移为 0033；实际数据库升级与部署状态由目标环境执行证据确认。

Crosswalk 导入使用单独的离线管理命令，不从用户目录中的显示名、邮箱、角色或租户字段推导授权。JSON 必须显式列出 jurisdiction、tenant、org unit、principal（稳定 actor ID + 精确 issuer/subject）和 membership（tenant、组织、角色及 grants）；principal 的 issuer/subject 必须与 `IDENTITY_DIRECTORY_FILE` 中启用的同一 actor 完全一致。先对经过授权方审阅的文件执行默认 dry-run，再由受控管理员显式 `--apply`：

```sh
APP_ENV=<target> DATABASE_SCHEMA=<application_schema> \
  AUTH_ADMIN_DATABASE_URL="$AUTH_ADMIN_DATABASE_URL" \
  IDENTITY_DIRECTORY_FILE=<trusted-directory.json> \
  go run ./cmd/identity-import --file <reviewed-crosswalk.json>

APP_ENV=<target> DATABASE_SCHEMA=<application_schema> \
  AUTH_ADMIN_DATABASE_URL="$AUTH_ADMIN_DATABASE_URL" \
  IDENTITY_DIRECTORY_FILE=<trusted-directory.json> \
  go run ./cmd/identity-import --file <reviewed-crosswalk.json> --apply
```

命令输出 source SHA-256、模式（dry-run/apply）与变更计数，须随变更审批记录保存；数据库冲突或身份不一致会拒绝整批事务。不能使用 Core 运行凭据调用导入，不能把配置 fixture 或 dry-run 结果当作生产组织授权已落库的证据。真实 crosswalk 仍需目标授权方提供并审阅。

## 本地 Compose

`make apps-up` 通过 `deployments/local.py` 和 `compose.app.yaml` 迁移数据库并启动 Gateway、Core、Document、Execution、Worker、Model/Tool Gateway 以及三个前端站点。先完成[本地开发](../engineering/local-development.md)中的基础设施和私有配置步骤，并确认容器能够访问模型端点。容器内 `127.0.0.1` 指向容器自身；模型服务若在宿主机运行，应配置容器可达地址。

- `make apps-up` 使用已配置的 Model Gateway。
- `make apps-up-fixture` 使用 Compose fixture 模型端点和 fixture 模型名，便于本地联调；fixture 结果不代表真实模型质量。
- `make apps-down` 停止独立应用服务。
- 分站部署时可设置 `VITE_ADMIN_URL`；留空使用同源 `/admin/rulepacks`。
- 前端可分别构建：`make web-client`、`make web-admin`、`make web-portal`。

Document 通过短期 opaque ticket 向 Core 回查一次建案上传授权。Document、Worker、Tool Gateway 和 Execution 使用同一组 authority-only transport 配置：`CORE_AUTHORITY_URL`、可选 `CORE_AUTHORITY_CA_FILE` 和 `CORE_AUTHORITY_ALLOW_INSECURE_DEV`。分布式开发配置使用 `CORE_AUTHORITY_URL=http://core-api:8081` 与 `CORE_AUTHORITY_ALLOW_INSECURE_DEV=true`；该精确 Compose/k3d service origin 只用于 APP_ENV=development/test 的本地受控网络，不提供对 hostile 本地网络的 TLS 保证。进程内 Document 模式沿用 direct Store 路径，不需要回调地址或 insecure 开关。

生产 Document、Worker、Tool Gateway 和 Execution 必须设置 `CORE_AUTHORITY_URL` 为指向现有 Core API 的 HTTPS origin。可通过 `CORE_AUTHORITY_CA_FILE` 指向容器内可信 PEM CA bundle；空值使用系统证书库。证书链和 URL hostname 必须匹配。生产必须省略或设置 `CORE_AUTHORITY_ALLOW_INSECURE_DEV=false`；设置 true、未知 APP_ENV、非法布尔值、无可信证书或非 HTTPS URL 都会拒绝启动。开发例外只允许上述精确 `http://core-api:8081` 或带显式端口的 loopback origin；不会接受任意内部网段地址。客户端不使用环境代理，也不跟随重定向。生产 endpoint、DNS/TLS 终止和 CA 配置由交付平台提供；未配置时不得称为生产可用。Document 的 `CORE_DOCUMENT_TOKEN` 只认证配对请求，不能代替 TLS 对 Core 响应进行服务器认证；Tool Gateway 使用用户当前 session bearer 请求 Core，Execution 使用浏览器当前 session bearer 请求 Core，响应均依靠 TLS 验证且不新增 service token。

`TOOL_CORE_AUTHORITY_TOKEN` 是 Tool Gateway 调用 Core job-claim authority endpoint 的独立高熵凭据，由 `make app-bootstrap` 或受控部署秘密管理生成；只注入 Core 和 Tool Gateway，并且必须不同于 `WORKER_CORE_TOKEN`、`WORKER_TOOL_TOKEN`、`GATEWAY_TOOL_TOKEN`、`CORE_DOCUMENT_TOKEN` 等服务凭据。它不会授予 Tool 数据库身份表权限。Worker 使用独立 `WORKER_CORE_TOKEN` 解析其确切 job claim；Worker 和 Tool 的 authority 回查都要求上述 Core TLS 配置。密钥缺失、复用或 Core endpoint 不可达时，相关调用拒绝继续，不降级到目录租户或 actor 字段。

私有配置设置 `OBSERVE=1` 与 `GRAFANA_ADMIN_PASSWORD` 后，`apps-up` / `apps-up-fixture` 会一并启动 Collector、Tempo、Prometheus 和 Grafana；`apps-down` 同时停止。Collector 地址为 `http://otel-collector:4318`。Grafana、Tempo、Prometheus 默认仅绑定主机回环端口 13000、13200、19090。导出的执行指标带有有限标签组合，不包含合同原文。

## Kubernetes

Kubernetes 基础清单与本地 k3d 演练不同。先准备目标集群、密钥和存储，再按实际部署清单验收网络策略、数据库角色、对象存储、证据导出凭据与外部依赖。`deployments/k8s/optional/evidence-export.yaml` 需要部署方提供指定 Secret 和字体 PVC。

单机 k3d 仅用于部署和故障行为演练，不能作为生产 HA、跨故障域容灾或容量证明。操作步骤见[k3d 手册](k3d.md)。本手册不宣称线上发布、生产集群接入或密钥配置已执行。
