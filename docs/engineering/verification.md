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
python3 scripts/fetch_legal_test_assets.py --destination ../contract-audit-delivery/legal-test-assets-20261101-v2
export CAW_LEGAL_TEST_ROOT="$(realpath ../contract-audit-delivery/legal-test-assets-20261101-v2)"
export LEGALNORM_ASSET_DIR="$CAW_LEGAL_TEST_ROOT/legal-sources"
export LEGALNORM_SUPPLEMENTAL_ASSET_DIR="$CAW_LEGAL_TEST_ROOT/legal-sources"
export LEGALNORM_CANONICAL_MANIFEST="$CAW_LEGAL_TEST_ROOT/p4-canonical-articles-v2.json"
```

脚本校验归档与文件 SHA-256；固定原始快照只支持可重复测试，不证明法律当前持续有效。CI 会按需准备测试资产，它们不是应用发布内容。

官方登记效力可离线或在线只读核对：

```bash
go run ./cmd/legalstatuscheck -asset-dir "$LEGALNORM_ASSET_DIR"
go run ./cmd/legalstatuscheck -capture-dir /tmp/legal-status-<ts>
```

在线模式只请求 `internal/legalnorm` 登记的 HTTPS 端点，拒绝跨主机重定向；响应限制 8 MiB，失败重试一次。原始响应和观察元数据输出到仓外目录。它不会自动导入新法律来源版本。核对命令的 `decision`（例如 `UNCHANGED`、`CHANGED_NEW_CAPTURE_REQUIRED`、`CAPTURE_FAILED`）不是验收状态。

## 固定候选完整检查

完整 `static`、`go`、`web`、`coverage`、`browser` 五阶段由 GitHub Actions 的 `workflow_dispatch` 执行。入口固定目标 SHA；五个阶段全部 `success` 才是完整 PASS。`skipped`、`cancelled`、失败和未运行阶段都不能算完整通过。普通 `main` 推送不自动执行完整五阶段。

Phase 0–3 只做受影响局部检查。完整 race、coverage、浏览器、真实模型/OCR、容量、故障和恢复矩阵集中在固定候选窗口。检查失败后只重跑相关范围，避免重复已取得的同范围证据。
