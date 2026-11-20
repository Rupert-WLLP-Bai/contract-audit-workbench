<!-- File metadata (manifest mirror): -->
<!-- Purpose: Defines short-lived issue branches, refactor conventions, Store entry restrictions, shared-checkout ownership, evidence requirements, and the iteration, Issue, backlog Project and release close-out operations for the scoped contribution workflow. -->
<!-- Author: Bai Junhao <baijunhao@cq.chinamobile.com> -->
<!-- Contact: +86 13617696321 -->
<!-- Created: 2026-11-01 -->
<!-- Updated: 2026-11-20 -->
<!-- Revision: r7 -->
<!-- Copyright: See LICENSE -->
<!-- End file metadata. -->

# 贡献与协作

## 分支与提交

`main` 是唯一长期主线。每项工作从最新 `main` 建立短期 `fix/<issue>-<slug>`、`feat/<issue>-<slug>` 或 `refactor/<issue>-<slug>` 分支，完成后直接向 `main` 提 PR。按共同目标、依赖和可回退范围组织批次，不设 Issue 数量硬上限；PR 对每项分别列出完成范围，独立工作按 Issue 拆分提交。只有完成本项范围、必要局部验证及快速门槛时才使用 `Closes #N`，仅关联时使用 `Refs #N`。本轮做不完的 Issue 按下文「做不完」处理，不保持开放等待。

新业务入口使用相应领域模块。`internal/store` 现有接口渐进迁移；新增业务逻辑须先提交 ADR，写清数据归属、事务和授权边界及迁移路径。缺陷修复和迁移适配按原语义处理。结构重构使用 `refactor/` 分支，批次与流程例外遵循 `AGENTS.md`；机器检查按架构守卫的渐进范围执行。

根 Agent 是唯一分支和提交操作人，负责刷新基线、切分支、整理提交并整合最终结果。共享主检出可并行处理，但每个写入者必须先收到明确且互不重叠的文件所有权；写入者不切分支、不提交。共享核心、schema、授权、公共接口、依赖锁、CI/workflow 与 AGENTS 由单一写入者串行维护。

## 检查与证据

按 `AGENTS.md` 的检查节奏和 `make check-fast` 的实际差异计划运行受影响检查。普通文档变更不运行应用套件。PR 的 Evidence 记录检查对应的源码 SHA、命令、范围和实际状态：`PASS`、`FAIL`、`BLOCKED`、`NOT_RUN` 或 `NOT_APPLICABLE`。没运行的项目写 `NOT_RUN`；完整五阶段在每轮收口时于最终提交上派发，不由日常 PR 代跑，也不把局部结果拼成完整通过。PR 说明同时列出需要写进本轮发布说明的限制。

`--plan` 中的 `file_metadata` 表示文件说明检查已选中。CI 从实际目标分支加载检查器，候选分支不能通过修改检查器自行宣称初始化完成；旧清单尚未初始化时明确报告 `NOT_RUN`。统一清单仍为 `scripts/file_metadata/manifest.json`，规范见[逐文件元信息](file-metadata.md)。新增、删除或重命名文件时同步维护该清单；已应用迁移、冻结 fixture、生成物和第三方声明按原始字节保护。头部写入仅通过固定对象与计划摘要约束的显式操作执行，不由快速检查自动改文件。

## 工程工具

Web 代码使用 ESLint 9、typescript-eslint、React Hooks 规则和 Prettier；ESLint 查代码问题，Prettier 管格式。TypeScript 的类型和未使用声明由现有 `tsc` 选项检查，ESLint 不重复启用这两类检查。Python 使用 Ruff，目标版本为 Python 3.10，规则只选基础语法、常见错误和未使用项。工具版本固定在 `web/package.json`、`web/bun.lock` 与 `requirements-dev.txt`。

### 实现风格

- Go 1.26 优先使用标准库惯用法（如 `slices`、`maps`、`strings.Cut`、`reflect.TypeFor` 和内置 `new`），让业务条件与错误链易读、易调试；保留 request、task 和 attempt 追踪信息，避免代码高尔夫。
- React 远程读取使用 TanStack Query；查询键包含完整有效 actor 上下文，查询函数传递取消信号。授权错误（尤其 403）后不继续展示缓存数据。
- 只为可复用的领域行为建立接口或 Hook；避免单用途万能层和压缩业务判断的嵌套表达式。

本地安装及完整静态检查：

```bash
python3 -m pip install --requirement requirements-dev.txt
cd web && bun install --frozen-lockfile
bun run lint
bun run format:check
cd ..
ruff check .
ruff format --check .
```

OpenAPI 契约由 Go 源生成，前端 wire 类型由锁定的 `openapi-typescript` Node API 生成：

```bash
make generate-openapi
make check-openapi-generated
```

旧的 `python3 scripts/generate_openapi_types.py [--check]` 入口保留为兼容启动器，会转发到 `web/scripts/generate-openapi-types.ts`。递归引用、组合 schema、required/null 和类型级 enum 由 SDK 根据同一份 OpenAPI 文档生成；`WireSchemas` 与 `wireEnums` 保持现有名字，后者只按已登记字段路径从该文档提取值。wire 类型不是运行时验证，现有领域 guard 与解析逻辑继续负责请求和响应检查。执行生成或漂移检查前需在 `web/` 安装锁定依赖。

PR 快速门禁只把本次变更且仍存在的源文件交给对应 lint/format CLI；删除文件不传入命令，重命名只检查新路径。配置或依赖变化不会扩展成全仓扫描；配置单独变化时只执行对应的配置解析探针。生成的 `web/src/api/wire.generated.ts`、Bun 锁文件、fixture、构建产物与缓存不交给格式化器。快速检查只读，不自动改写文件；全量质量入口在 static 阶段执行仓库范围的锁定工具。已有文件的格式债务应由对应目录维护者按单独 Issue 逐步清理，不通过关闭整类规则绕过。

## 迭代、Issue 与待办池

规则以 `AGENTS.md` 的「迭代与 Issue」为准，本节写具体操作。仓库为 `Rupert-WLLP-Bai/contract-audit-workbench`。

| 内容 | 位置 |
| --- | --- |
| 本轮工作 | 开放 Issue，挂当前 Milestone（以下一个产品版本命名，如 `v0.1.2`）；正常情况下只有一个开放 Milestone |
| 以后可能做的事 | GitHub Project「合同审计待办池」：[https://github.com/users/Rupert-WLLP-Bai/projects/2](https://github.com/users/Rupert-WLLP-Bai/projects/2)（编号 `2`），字段「状态」（`待办池` / `下一轮候选` / `等外部条件`）和「来源」 |
| 质量状态 | 每轮 GitHub Release 与 `CHANGELOG.md` |

`gh` 读写 Project 需要 `project` 权限。缺少时报告负责人执行 `gh auth refresh -h github.com -s project`，Agent 不自行发起授权。

### 日常操作

1. 开工先看本轮剩余工作，只做本轮 Issue：

   ```bash
   gh api repos/Rupert-WLLP-Bai/contract-audit-workbench/milestones --jq '.[] | select(.state=="open") | .title'
   gh issue list --milestone <当前 Milestone> --state open
   ```

2. 实现中发现的新问题、想法和优化记入待办池，不开 Issue，也不往已开放 Issue 追加范围。阻塞本轮收口的缺陷例外，直接建 Issue 并挂本轮 Milestone。

   ```bash
   gh project item-create 2 --owner Rupert-WLLP-Bai --title "<一句话标题>" --body "<背景、发现位置、建议范围>" --format json --jq '.id'
   gh project field-list 2 --owner Rupert-WLLP-Bai --format json
   gh project item-edit --project-id <Project ID> --id <草稿项 ID> --field-id <状态字段 ID> --single-select-option-id <选项 ID>
   gh project item-edit --project-id <Project ID> --id <草稿项 ID> --field-id <来源字段 ID> --text "<来源 PR、Issue 或决定>"
   ```

   Project ID 用 `gh project view 2 --owner Rupert-WLLP-Bai --format json --jq '.id'` 查询。

3. PR 用 `Closes #N` 关闭完全完成的本轮 Issue。Evidence 写实际检查结论，并列出需要写进本轮发布说明的限制。
4. 做不完：本轮无法完成的 Issue，经负责人确认后以 `not planned` 关闭（`gh issue close <N> --reason "not planned" --comment "<原因、已完成部分、待办池草稿项>"`），先建好对应草稿项；收口时列入「本轮移出」。
5. Issue 不用于记录长期状态、跟踪汇总或验收台账。新 Issue 只用 type 和 area 标签；`阶段/…`、`验收/…` 标签只保留在历史 Issue 上，不再新增。

### 规划下一轮

负责人批准后执行：

1. 确认上一轮已收口：Milestone 已关闭，版本已发布。
2. 从待办池挑选「下一轮候选」，逐项做准入检查：一到两个 PR 能完成；验收本轮可执行，所需环境、数据、模型和账号已经具备；不依赖外部人员或暂缓事项；写明不做什么。不满足的留在待办池或先拆小。
3. 建 Milestone：

   ```bash
   gh api repos/Rupert-WLLP-Bai/contract-audit-workbench/milestones -f title=v0.1.N -f description="<本轮目标>；收口条件：开放 Issue 为 0，quality.yml 在最终提交上五阶段全部 success"
   ```

4. 把选中的草稿项转为 Issue：按 Issue 表单写全正文并注明来源草稿项，`gh issue create --milestone v0.1.N --label <type> --label <area> --title "<标题>" --body-file <正文文件>`；创建后删除草稿项：`gh project item-delete 2 --owner Rupert-WLLP-Bai --id <草稿项 ID>`。

### 收口

1. 本轮 Issue 全部关闭后，在最终提交派发完整五阶段：`gh workflow run quality.yml -f ref=<完整 SHA>`。任一阶段不是 `success` 就在本轮修复后重新派发，不开下一轮。
2. 全部通过后，经负责人确认，在该提交打版本 tag 并发布：

   ```bash
   git tag v0.1.N <完整 SHA> && git push origin v0.1.N
   gh release create v0.1.N --verify-tag --title v0.1.N --notes-file <发布说明文件>
   ```

3. 以文档 PR 把同一份发布说明写入 `CHANGELOG.md` 的 `## v0.1.N - YYYY-MM-DD`，内容包括：完成的 Issue、本轮移出、验证表（快速门槛、完整五阶段、真实模型/OCR、安装恢复、目标环境部署）和已知限制。
4. 关闭 Milestone：`gh api -X PATCH repos/Rupert-WLLP-Bai/contract-audit-workbench/milestones/<编号> -f state=closed`。

### 标签

每个 Issue 选择一个 type 和一个主 area，不为同一含义创建同义标签，也不批量重命名历史标签。

| 含义 | 标签 |
| --- | --- |
| type: bug | `bug` |
| type: feature / refactor | `enhancement`（结构调整须保持行为） |
| type: docs | `documentation` |
| area | `area:audit`、`area:docs`、`area:infra`、`area:tenant`、`area:web` |
| 历史状态（不再新增） | `阶段/…`、`验收/…` |

若新工作需要目录之外的 area，应先登记并核对，避免重复同义项。
