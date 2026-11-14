<!-- File metadata (manifest mirror): -->
<!-- Purpose: Documents frozen document parsing, model budgets, original-source reading and optional same-host MinerU deployment boundaries. -->
<!-- Author: Bai Junhao <baijunhao@cq.chinamobile.com> -->
<!-- Contact: +86 13617696321 -->
<!-- Created: 2026-11-01 -->
<!-- Updated: 2026-11-14 -->
<!-- Revision: r6 -->
<!-- Copyright: See LICENSE -->
<!-- End file metadata. -->

# 文档解析与模型预算边界

本页整理扫描件 OCR、请求限制和 DOCX 可见文本处理约束。具体部署前按真实模型服务能力配置并验收；不要从少量样本推算 token 上界。

## 原件阅读与引用标注

审查工作区和已核验模型引用支持 PDF、PNG、JPEG 原件与冻结文字切换。原件从现有案件授权入口读取，并核对对应不可变文档的 SHA-256；切换身份、案件或执行记录时释放旧阅读器。标注只辅助人工对照，不写回解析结果、引用状态、人工判断或历史审查。

原生 PDF 使用文件中实际的文字区域；扫描件在浏览器内按当前页识别文字位置。只有冻结文字区间正确、原件匹配、页面文字与引用一致且位置唯一时才标注。位置匹配允许排版空白、全角冒号和非数字相邻中文句号的字形差异，不改变冻结引用，不模糊替换数值、比例或主体名称；数字相邻的句号不作等价替换。独立的非数字相邻冒号仅作上下文，不绘制标注区域；正文词语、数值和单位仍须满足识别质量要求。同页重复文字、正文识别质量不足或找不到引用时，保留原件供人工核对；没有冻结页码时须手动选择页面查找。高亮为引用所在文字区域，不表示该风险已被人工确认。

阅读器限制为单文件 32 MiB、PDF 128 页、页面最长边 2400 像素、输入扫描图 4000 万像素；单次原件读取或当前页位置识别超时为 90 秒。翻页和离开阅读器会取消旧任务。缩放只改变显示比例，不改变定位坐标。

PDF 阅读器、字体、解码器及中文/英文位置识别资源由 `web/bun.lock` 固定，Vite 从依赖生成同源 `document-reader/` 资源，随各前端构建产物部署，不依赖公共 CDN。浏览器位置识别不调用模型网关，不改变既有审查 OCR 的预算和冻结身份。局部合成文件检查与固定候选的真实合同、浏览器、真实审查模型/OCR 验收分别记录。

## 文件与请求预算

单文件上限为 32 MiB。模型网关对声明超过 33 MiB 的请求在读取正文前拒绝；未声明长度的流在读取过程中仍按相同字节上限限制。默认输入预留按请求体字节作保守预算。若需完整覆盖最大 33 MiB 请求，单次输入预留至少为 `34603008`；租户与全局每分钟预算都必须大于单次输入预留与输出额度之和。

逐页扫描 OCR 的输出上限为 8192 tokens，文本审查输出上限为 2048 tokens；额度进入冻结模型配置身份。模型返回截断或过滤状态时，该页不会被当作完整正文。旧尝试的解析正文和模型配置身份不被重写。长文档须同时核对 `DOCUMENT_PARSE_TIMEOUT`、`GATEWAY_REQUEST_TIMEOUT` 与 `GATEWAY_UPLOAD_TIMEOUT`。解析配置上限为 30 分钟，但显式重新解析仍受当前会话和最长 5 分钟的授权票据约束；后台审查准备另有自己的执行时限。提高单个超时不能延长授权，也不代表长文档能够完成或断点恢复。

合同事实提取最多请求 32000 个输出 token，使用高推理强度；这只是上限，实际用量由供应商响应记录。模型网关默认单次输入、输出预留上限分别为 131072、32000，默认请求超时为 600 秒。每次预留仍受租户和全局分钟预算、调用次数、并发、租约及金额限额约束；用量未知时按保守预留结算。

持久化的单合同事实审查在冻结配置有效时，执行总时限从启动时起最多 720 秒；启动和授权阶段仍限 90 秒。其他审查保留 90 秒限制。任务受理时冻结的队列截止时间、用户取消和工作租约仍可提前终止执行。

部署前应确认所选供应商和审查模型支持高推理强度及 32000 输出 token，并让各服务使用一致的模型身份。一次本机实测使用 `panda-local/cqmoma/cqcm/deepseek-v4-flash`、`high` 和 32000 输出上限；网关超时为 600 秒，调用客户端为 630 秒。要复现单次物理调用的设置，可在网关配置 `MODEL_RETRIES=0`；默认值为 1，仅对符合现有重试与结算条件的调用重试。实际结果仍取决于供应商、输入和当前预算。

默认输入预留按 JSON 请求体字节计算，并非图片 token 数：一次实测合同内图片的 OCR 请求为 317987 字节，会被默认 131072 上限拒绝。本机隔离测试将单次输入预留设为 360000，并保持租户每分钟 400000、全局每分钟 2000000 和图片专用上界关闭；这一配置只证明所测请求可进入预算流程，不代表所有 PDF 或图片均可通过。调整时须同时核对单次输入与输出预留之和是否能放入租户和全局剩余额度，不应仅为提高单次上限而盲目增加分钟预算。

常用预算变量：`MODEL_MAX_INPUT_TOKEN_RESERVATION`、`MODEL_MAX_OUTPUT_TOKEN_RESERVATION`、`MODEL_REQUEST_TIMEOUT`、`MODEL_TENANT_TOKENS_PER_MINUTE`、`MODEL_GLOBAL_TOKENS_PER_MINUTE`。降低单次上限会拒绝超出该上限的请求。

## 可选本机 MinerU OCR

同机原生进程部署可选择 MinerU 4.2.2 的 `flash`、`ocr`、ONNX CPU 组合：MinerU 负责图片转文字，现有 DeepSeek 负责文字事实建议。默认 `DOCUMENT_OCR_ENGINE=model` 继续使用配置的视觉模型；选择本地引擎后，失败会明确返回，不自动改用另一种引擎。

在源码仓外的 Python 3.12 虚拟环境安装固定版本 `mineru==4.2.2`，将权重、缓存、服务状态和输出保存在私有目录。官方 `mineru-kit api-server` 和 `mineru-kit file-parse-server` 都只监听 `127.0.0.1`；后者连接前者并使用独立的 `--state-dir`。设置 `MINERU_TELEMETRY_DISABLED=1`、`MINERU_MODEL_SMALL_BACKEND=onnx` 和 `MINERU_MODEL_ONNX_DEVICE=cpu`。本配置不适用于让应用容器通过宿主机回环地址访问服务。

所有参与解析及审查受理的应用进程使用相同配置：

```dotenv
DOCUMENT_OCR_ENGINE=mineru
DOCUMENT_MINERU_URL=http://127.0.0.1:8003
DOCUMENT_MINERU_PYTHON=/absolute/private/ocr/.venv/bin/python
DOCUMENT_MINERU_MANIFEST=/absolute/private/ocr/assets-manifest.json
DOCUMENT_MINERU_ASSET_ROOT=/absolute/private/ocr/models
```

资产清单使用 `repository`、`revision` 和 `files`；仓库名固定为 `opendatalab/MinerU-4_models_onnx`，每个文件记录相对 `path` 和 `sha256`，覆盖该版本 flash 所需的版面配置及三个 ONNX 权重。启动时核验清单与本机文件摘要。这是同机受信部署的配置核验，不是 HTTP 服务对实际加载权重的密码学证明；服务必须使用同一份资产。

解析采用官方 Middle JSON 和 SDK 的 `RenderMode.FULL`，保留默认 Markdown 导出会省略的页眉、页脚等内容。转写文字与新解析身份一起冻结；它包含官方排版标记，不代表原图逐字符副本。升级引擎、权重或导出方式会改变解析身份，旧审查保留原结果。文字识别成功、引用匹配和事实覆盖分别验收，手写日期、金额、缺页及模糊内容仍须对照原件。

本地 OCR 调用前后都核验原操作授权；大模型调用仍经过原模型预算账本。本地服务没有访问鉴权，状态目录应仅服务账号可读写（权限 `0700`），不得公开端口。官方任务在客户端取消后可能继续计算；应用不接收取消后的结果。状态目录可能保留上传图片和结果，停用服务后按部署方保留策略清理，不能把它当作业务权威存储或提交到源码仓。

## 可选结构化输出

结构化输出默认关闭。只有管理员确认供应商支持系统使用的固定 JSON Schema 后，才设置 `CAW_STRUCTURED_OUTPUT_ENABLED=true`，并同时填写 `CAW_STRUCTURED_OUTPUT_PROVIDER_URL` 与 `CAW_STRUCTURED_OUTPUT_REVIEW_MODEL`。两项必须分别与最终生效的 provider 地址和审查模型完全匹配；未确认支持或不匹配时保持关闭。此配置不表示某个本地供应商已具备该能力。

Compose 部署时，`CAW_STRUCTURED_OUTPUT_PROVIDER_URL` 必须绑定容器用于冻结身份并访问供应商的最终 provider 地址。若部署把宿主机上的 `localhost` 地址改为容器可达的 `host.docker.internal`，绑定值也必须使用容器最终看到的地址。审查模型必须与最终生效的模型一致；采用平台保存的端点时，以该端点选定的审查模型为准。

所有参与该审查路径的 Core、Document、Execution、Worker 和 Model Gateway 进程，都必须使用相同的开关、provider 地址和审查模型配置。启用或调整配置时，按受控部署流程更新所有参与方，再接收新审查。不支持此能力的服务保持关闭，不能接收该启用配置的请求。fixture 与手动 Compose 配置必须显式关闭结构化输出，并将 provider 地址和审查模型绑定值留空。

启用或更改这些值会生成新的冻结模型身份和价格键。开始使用新身份前，按现有价格配置流程为新身份配置价格；不能用旧身份的价格代替。已冻结尝试保留受理时的身份和历史记录；当前配置与冻结身份不一致时，不能按新配置继续调用，应重新发起审查。旧身份和历史价格记录不得覆盖。

该 schema 只用于审查模型请求，不用于 OCR 或路由请求。供应商拒绝请求时保留上游状态类别；拒答、截断和格式错误沿用现有诊断。HTTP 400 或 422 只能说明请求被拒，不能据此认定供应商不支持 schema；系统不会自动降级为无约束输出。模型建议仍须通过现有冻结原文引用核验；结构化输出本身不构成审查结论或人工决定。

## 单图 token 上界

只有模型服务对单图输入明确保证 token 上界时，才可同时设置 `MODEL_OCR_IMAGE_PROVIDER_URL`、`MODEL_OCR_IMAGE_MODEL` 和 `MODEL_OCR_IMAGE_TOKEN_UPPER_BOUND`。仅当运行时 provider 地址与规范化后的 OCR 模型身份完全匹配、请求是服务生成的单张内嵌 PNG/JPEG OCR 请求且通过图像校验时，输入预留才按“非图像 JSON 字节数 + 配置的单图上界”计算。PNG 不得有动画帧或尾随拼接内容。其他请求仍按完整请求体字节保守预留。配置值应来自对应服务的明确保证。

新版逐页额度要求 Core、Document、Worker 与 Model Gateway 运行同一版本。

## 上传与解析受理

材料上传、重新解析和草稿生成在进入解析队列前检查账号角色与案件权限，任务提交时再检查一次；队列满时，只有通过权限检查的请求才收到容量不足响应。模型不可用时审查按执行结果记录失败或部分完成，不自动生成模型结论。

## DOCX 可见内容

DOCX 解析保留 Adobe Symbol 字体字符的 Unicode 映射；不支持的符号字体、无可靠文本映射的字形或无效编码会使解析失败，不会静默丢弃。

解析按渲染后的可见内容处理 `mc:AlternateContent`，只取一个分支：`Requires` 前缀按该元素作用域中的 `xmlns` 解析为命名空间，全部受支持时取对应 `mc:Choice`，否则取 `mc:Fallback`；缺失、空或未声明的 `Requires` 视为无效标记并失败，不会重复计入等价表示。`wordprocessingShape` 文本框中的段落计入正文；页眉页脚按首次引用只读一次。

`PAGE`、`NUMPAGES`、`SECTIONPAGES`、`SECTION` 字段结果随版式变化，作为页码排除在合同正文外；其他字段读取缓存结果。图表、组合形状、VML 回退内容、文本框内表格等无法完整定位的内容会使解析失败，不会静默丢弃或猜测。
