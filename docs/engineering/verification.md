<!-- File metadata (manifest mirror): -->
<!-- Purpose: 说明“检查与验收”的操作或技术边界：所有结果应记录实际源码 SHA、命令、范围和状态。只使用 `PASS`、`FAIL`、`BLOCKED`、`NOT_RUN`、`NOT_APPLICABLE`；未运行项目标为 `NOT_RUN`。一次局部结果不能拼成完整回归 PASS。 -->
<!-- Author: Bai Junhao <baijunhao@cq.chinamobile.com> -->
<!-- Contact: +86 13617696321 -->
<!-- Created: 2026-11-01 -->
<!-- Updated: 2026-11-14 -->
<!-- Revision: r4 -->
<!-- Copyright: See LICENSE -->
<!-- End file metadata. -->

# 检查与验收

所有结果应记录实际源码 SHA、命令、范围和状态。只使用 `PASS`、`FAIL`、`BLOCKED`、`NOT_RUN`、`NOT_APPLICABLE`；未运行项目标为 `NOT_RUN`。一次局部结果不能拼成完整回归 PASS。

## PR 快速门禁

```bash
make check-fast
python3 scripts/check_fast.py --base origin/main
BASE_REF=<base-sha> make check-fast
python3 scripts/check_fast.py --base <base-sha> --plan --github-output "$GITHUB_OUTPUT"
```

`make check-fast` 委托 `scripts/check_fast.py`，从 PR 实际目标基线的 merge-base 计算包含已修改、新增、删除、重命名路径的差异，只运行受影响范围。`--plan` 报告 Go、web、Python、database、Poppler 和 legal-assets 准备需求。快速 Go 检查做格式、编译和受影响无数据库包测试，不跑 `go vet`；快速路径不跑浏览器。只有计划需要数据库时才准备 PostgreSQL 18 和隔离测试角色。

Web 与 Python 的快速 lint/format 只接收本次变更且仍存在的源文件；删除文件不会传入 CLI，重命名只检查新路径。配置或依赖变化不扩大为全仓文件扫描。`--plan` 中的 `web_eslint_files`、`web_prettier_files` 和 `python_ruff_files` 展示实际 lint/format 文件，`web_prettier_ignore_config` 与 `python_ruff_config` 标记配置解析探针。单独修改 Ruff 配置或版本时，快速路径调用 `ruff check --show-settings scripts/check_fast.py` 解析配置，不对无关 Python 文件 lint/format；`.prettierignore` 单独变化时调用 Prettier `--file-info` 解析忽略配置。ESLint 与 Prettier 可解析的配置文件本身仍按变更文件进入对应 CLI。全仓 lint/format 只由完整 quality 的 static 阶段执行。CI 只在计划需要 Python 工具时安装 Ruff；OpenAPI 漂移检查需要 Go、Bun、Node 与冻结的 Web 依赖，因此 Go-only 计划也会先安装这些工具后再执行生成检查。

当前未启用 React Compiler。ESLint 保留官方 recommended 配置，`rules-of-hooks` 等规则维持原强度；`refs` 与 `set-state-in-effect` 两类 Compiler 兼容诊断先以 warning 完整报告，按 [React 官方的渐进处理建议](https://react.dev/reference/eslint-plugin-react-hooks)逐步改进已有页面。warning 不表示问题已修复，也不替代角色切换、异步结果隔离或浏览器验收；现有 UI 验收继续跟踪这些诊断。

OpenAPI 更新先改 Go 契约，再运行 `make generate-openapi` 生成前端类型。`make check-openapi-generated` 是只读漂移检查，运行前需要已安装 `web/bun.lock` 对应依赖；PR 中 Go-only 变更也会准备 Bun、Node 与 Web 锁定依赖后再执行它。Python 兼容入口只转发，不再实现 schema 到 TypeScript 的转换。

## 定点/本地测试

```bash
make test-unit
```

只有需要完整本地确定性验收时使用 `make verify` 或 `make acceptance`；`acceptance` 会增加真实浏览器旅程，不能仅凭命令配置或依赖可用就记为通过。真实模型测试是 opt-in：`make test-live`。

前端本地检查可在 `web/` 执行：

```bash
bun run typecheck
bun run test
bun run build
```

需要数据库的集成检查使用隔离 `TEST_DATABASE_URL`；`MIGRATOR_DATABASE_URL` 指向同一测试库的迁移角色，`MODEL_DATABASE_URL` 指向独立非特权角色。专用角色不得是超级用户、表所有者或拥有 `BYPASSRLS`。快速检查仅在计划要求 `database=true` 时准备隔离 PostgreSQL 18。


需要固定法律来源测试资产时，先在仓外准备：

```bash
CAW_LEGAL_ARCHIVE=/path/to/legal-test-assets-20261101-v2.tar.xz
python3 scripts/fetch_legal_test_assets.py --archive "$CAW_LEGAL_ARCHIVE" --destination ../contract-audit-delivery/legal-test-assets-20261101-v2
export CAW_LEGAL_TEST_ROOT="$(realpath ../contract-audit-delivery/legal-test-assets-20261101-v2)"
export LEGALNORM_ASSET_DIR="$CAW_LEGAL_TEST_ROOT/legal-sources"
export LEGALNORM_SUPPLEMENTAL_ASSET_DIR="$CAW_LEGAL_TEST_ROOT/legal-sources"
export LEGALNORM_CANONICAL_MANIFEST="$CAW_LEGAL_TEST_ROOT/p4-canonical-articles-v2.json"
```

脚本校验归档与每个文件的 SHA-256；`--archive`（或 `CAW_LEGAL_TEST_ARCHIVE`）用于本地指定受大小限制的普通归档文件。CI 从仓库变量 `CAW_LEGAL_TEST_ARCHIVE_XZ_01` 至 `_05` 重组同一固定归档；这些变量只在准备素材的步骤传入，不写入日志，也不用于传递秘密。源码仓只保留文件大小与摘要清单，不保存归档或解出的法律素材。固定原始快照只支持可重复测试，不证明法律当前持续有效。CI 会按需准备测试资产，它们不是应用发布内容。

官方登记效力可离线或在线只读核对：

```bash
go run ./cmd/legalstatuscheck -asset-dir "$LEGALNORM_ASSET_DIR"
go run ./cmd/legalstatuscheck -capture-dir /tmp/legal-status-<ts>
```

在线模式只请求 `internal/legalnorm` 登记的 HTTPS 端点，拒绝跨主机重定向；响应限制 8 MiB，失败重试一次。原始响应和观察元数据输出到仓外目录。它不会自动导入新法律来源版本。核对命令的 `decision`（例如 `UNCHANGED`、`CHANGED_NEW_CAPTURE_REQUIRED`、`CAPTURE_FAILED`）不是验收状态。

## 固定候选完整检查

完整 `static`、`go`、`web`、`coverage`、`browser` 五阶段由 GitHub Actions 的 `workflow_dispatch` 执行。入口固定目标 SHA；五个阶段全部 `success` 才是完整 PASS。`skipped`、`cancelled`、失败和未运行阶段都不能算完整通过。普通 `main` 推送不自动执行完整五阶段。

完整 `static` 阶段对仓库 Python 源运行 Ruff `check` 与 `format --check`，并对 Web 工具范围运行 ESLint 和 Prettier `--check`。两者都只报告，不写回源码。Ruff 配置目标 Python 3.10；ESLint 与 Prettier 依赖通过 `bun install --frozen-lockfile` 准备，Ruff 由 `requirements-dev.txt` 固定并安装。完整质量入口与 PR 快速入口使用同一工具版本和配置。

Phase 0–3 只做受影响局部检查。完整 race、coverage、浏览器、真实模型/OCR、容量、故障和恢复矩阵集中在固定候选窗口。检查失败后只重跑相关范围，避免重复已取得的同范围证据。

## 工作流启动与完整验收

修改工作流后先用固定版本 `actionlint` 检查表达式所在位置；例如 `runner.temp` 可以用于 step 的 `env`，不能用于 job 级 `env`。PR 快检查的 `workflow` 计划标志会准备并执行语法检查，避免其它门禁通过但完整工作流根本无法创建任务。

完整入口的 `validation_only=true` 只运行固定 SHA 解析和工作流语法任务；五阶段与最终完整质量门禁保持跳过，结果是完整验收 `NOT_RUN`。默认值为 false，完整质量仍要求准备、语法与 static/go/web/coverage/browser 全部成功。启动验证和完整运行使用不同并发组，互不取消。

完整 static 阶段按文件独立进程递归发现 Python 测试，包含架构和元信息子目录。空范围、跳过、预期失败或失败都不能成为 PASS；需要 TypeScript 的 Python 用例会准备真实 web 工具链。实际架构守卫读取已固定的目标 BASE 配置与完整 Git 历史。

确定性五阶段使用受控模型夹具；显式真实模型检查单独记录实际请求、模型身份和退出码。不同文本/OCR模型通过私密测试环境的 `LLM_MODEL`、`OCR_MODEL`、`LLM_BASE_URL` 指定，不能用模型目录 GET 成功代替真实调用，也不能把两类结果拼成未执行的整体验收。

PR 的计划和执行控制器取自固定目标 BASE 的 Git blob，实际被检查的源码仍绑定 PR HEAD；完整质量的五阶段共用 prepare 固定的 subject/base SHA。候选版本把控制器改为空操作，不能据此取消 BASE 规定的检查。控制器升级先以原 required gate 审阅基础版本，再切换 workflow；这不替代对工作流定义本身的代码审查。

测试请求同样遵守当前命令协议：登录返回的上下文版本和 CSRF 证明按受众保存，测试助手不伪造租户/角色、不自动刷新或重试已拒绝命令。真实模型测试经生产模型网关代码、持久任务和 PG 预算账本验证，目录连通不等于模型执行成功。
