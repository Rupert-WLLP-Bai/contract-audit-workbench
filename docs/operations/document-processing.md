# 文档解析与模型预算边界

本页整理扫描件 OCR、请求限制和 DOCX 可见文本处理约束。具体部署前按真实模型服务能力配置并验收；不要从少量样本推算 token 上界。

## 文件与请求预算

单文件上限为 32 MiB。模型网关对声明超过 33 MiB 的请求在读取正文前拒绝；未声明长度的流在读取过程中仍按相同字节上限限制。默认输入预留按请求体字节作保守预算。若需完整覆盖最大 33 MiB 请求，单次输入预留至少为 `34603008`；租户与全局每分钟预算都必须大于单次输入预留与输出额度之和。

逐页扫描 OCR 的输出上限为 8192 tokens，文本审查输出上限为 2048 tokens；额度进入冻结模型配置身份。模型返回截断或过滤状态时，该页不会被当作完整正文。旧尝试的解析正文和模型配置身份不被重写。长文档须按实际耗时配置 `DOCUMENT_PARSE_TIMEOUT`、`GATEWAY_REQUEST_TIMEOUT` 与 `GATEWAY_UPLOAD_TIMEOUT`；解析超时上限为 30 分钟。

常用预算变量：`MODEL_MAX_INPUT_TOKEN_RESERVATION`、`MODEL_TENANT_TOKENS_PER_MINUTE`、`MODEL_GLOBAL_TOKENS_PER_MINUTE`。预算还受模型调用次数、并发、租约及未知用量结算规则约束。

## 单图 token 上界

只有模型服务对单图输入明确保证 token 上界时，才可同时设置 `MODEL_OCR_IMAGE_PROVIDER_URL`、`MODEL_OCR_IMAGE_MODEL` 和 `MODEL_OCR_IMAGE_TOKEN_UPPER_BOUND`。仅当运行时 provider 地址与规范化后的 OCR 模型身份完全匹配、请求是服务生成的单张内嵌 PNG/JPEG OCR 请求且通过图像校验时，输入预留才按“非图像 JSON 字节数 + 配置的单图上界”计算。PNG 不得有动画帧或尾随拼接内容。其他请求仍按完整请求体字节保守预留。配置值应来自对应服务的明确保证。

新版逐页额度要求 Core、Document、Worker 与 Model Gateway 运行同一版本。

## 上传与解析受理

材料上传、重新解析和草稿生成在进入解析队列前检查账号角色与案件权限，任务提交时再检查一次；队列满时，只有通过权限检查的请求才收到容量不足响应。模型不可用时审查按执行结果记录失败或部分完成，不自动生成模型结论。

## DOCX 可见内容

DOCX 解析保留 Adobe Symbol 字体字符的 Unicode 映射；不支持的符号字体、无可靠文本映射的字形或无效编码会使解析失败，不会静默丢弃。

解析按渲染后的可见内容处理 `mc:AlternateContent`，只取一个分支：`Requires` 前缀按该元素作用域中的 `xmlns` 解析为命名空间，全部受支持时取对应 `mc:Choice`，否则取 `mc:Fallback`；缺失、空或未声明的 `Requires` 视为无效标记并失败，不会重复计入等价表示。`wordprocessingShape` 文本框中的段落计入正文；页眉页脚按首次引用只读一次。

`PAGE`、`NUMPAGES`、`SECTIONPAGES`、`SECTION` 字段结果随版式变化，作为页码排除在合同正文外；其他字段读取缓存结果。图表、组合形状、VML 回退内容、文本框内表格等无法完整定位的内容会使解析失败，不会静默丢弃或猜测。
