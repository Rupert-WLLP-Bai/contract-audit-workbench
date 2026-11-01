# 本地开发

## 环境

需要 Go 1.26.7+、Bun 1.4.3、Node.js 22.12+、Python 3.10+、Docker Engine/Desktop 与 Compose v2。PDF 解析需要 Poppler 的 `pdfinfo`、`pdftotext`、`pdfimages`、`pdftoppm` 和 CJK mappings。

## 单进程开发

在仓库根目录执行：

```bash
make setup
make app-bootstrap
make app-config
make infra-up
make infra-smoke
```

`make app-bootstrap` 创建本机私有 `.env` 和 `.env.distributed`，不会覆盖已有文件；配置项来源是 [.env.example](../../.env.example)。凭据不提交到仓库。Go 不会自动加载 `.env`，启动 API：

```bash
set -a
source .env
set +a
make dev
```

另开终端启动前端：

```bash
make web
```

默认 API 为 `http://127.0.0.1:8080`，Vite 为 `http://127.0.0.1:5173`，`/api` 和 `/openapi.json` 代理到 API。目标地址可由 `VITE_DEV_API_PROXY` 调整。

`make infra-up` 使用本地 PostgreSQL、RustFS 和 NATS。`make infra-down` 停止基础设施但保留数据卷。业务数据库使用 PostgreSQL 18；Keycloak 身份库仍独立使用 PostgreSQL 17。不要把 PostgreSQL 17 数据卷直接挂给 PostgreSQL 18。版本迁移须备份、使用 `pg_dump`/`pg_restore`、验证并保留回退窗口。

## Demo 身份

开发和测试模式提供以下固定账号。生产环境禁止使用这些账号；配置企业身份目录后本地密码登录会停用。授权仍由服务端按租户、组织、角色和案件范围校验。

| 用户 | 密码 | 角色 | 租户 | 组织 |
| --- | --- | --- | --- | --- |
| `demo-admin` | `admin-demo` | 管理员 | `local-demo` | `demo-org` |
| `demo-clerk` | `clerk-demo` | 经办 | `local-demo` | `demo-org` |
| `demo-reviewer` | `reviewer-demo` | 审查员 | `local-demo` | `demo-org` |
| `demo-viewer` | `viewer-demo` | 只读 | `local-demo` | `demo-org` |
| `demo-otherorg-clerk` | `otherorg-clerk-demo` | 经办 | `local-demo` | `demo-other-org` |
| `demo-tenant2-admin` | `tenant2-admin-demo` | 管理员 | `local-demo-2` | `demo-org-2` |
| `demo-tenant2-clerk` | `tenant2-clerk-demo` | 经办 | `local-demo-2` | `demo-org-2` |
| `demo-tenant2-reviewer` | `tenant2-reviewer-demo` | 审查员 | `local-demo-2` | `demo-org-2` |
| `demo-tenant2-viewer` | `tenant2-viewer-demo` | 只读 | `local-demo-2` | `demo-org-2` |

模型与 OCR 服务由部署方提供。在 `.env` 配置 `LLM_BASE_URL`、`LLM_MODEL`、`OCR_MODEL` 与 `LLM_API_KEY`。模型不可用时，相关审查会记录失败或部分完成，不自动生成模型结论。模型 ID 应与端点 `/v1/models` 返回的目录一致，不能把可接受别名当作正式模型 ID。

## 会话与运营快照

浏览器身份范围包含工作区、账号、角色、租户和组织；身份变化时前端清理旧身份数据并重建页面与审查事件连接，服务端仍逐次检查权限。Admin 的「身份目录与租户模型预算」页读取 `GET /api/admin/tenant-operations` 的当前租户快照。配置 `IDENTITY_DIRECTORY_FILE` 后，版本按可信文件原始字节的 SHA-256 计算；文件替换会重新核验，同 inode/大小且时间戳保留的修改在身份查询时最多每 1000ms 核验一次。读取或解析失败立即清空有效用户和版本，最后成功版本只保留作诊断，不继续授权。这个频率不保证 Kubernetes 挂载变更到达所有副本的时间。

配额页从 `MODEL_GATEWAY_URL` 读取无凭据配置元数据，只读聚合 PostgreSQL 的当前租户 60 秒滚动窗口。预算包含预留和保守计费；明确未发出的请求才能释放预留。上限或余量未知时显示未知，全局约束仍适用，页面余量不保证请求一定受理。登录失败限流是进程本地，不是多副本共享限制。真实 IdP、配置传播和集群网络策略仍需在目标交付环境单独验收。

更完整的本地检查步骤见[检查与验收](verification.md)；多服务启动见[部署模式](../operations/deployment.md)。
