# 本地 MinerU OCR

MinerU 4.x 可作为本机 OCR 服务选项。以下命令来自当前 README 与实际工具入口；模型权重放在仓外。示例适用于已配置 NVIDIA CUDA 的 Linux/WSL 主机，不是通用硬件性能承诺。

```bash
uv tool install --python 3.12 'mineru[full]==4.0.12'
mineru-kit models download --tier standard --small-backend torch --vlm-engine vllm --source modelscope
mineru-kit models verify --tier standard --small-backend torch --vlm-engine vllm
mineru-openai-server --engine vllm --host 127.0.0.1 --port 30000 --gpu-memory-utilization 0.45 --max-model-len 8192 --max-num-seqs 1 --limit-mm-per-prompt '{"image":1}' --enforce-eager
```

另开终端启动解析 API：

```bash
MINERU_MODEL_SMALL_BACKEND=torch MINERU_MODEL_VLM_ENGINE=vllm mineru-kit api-server --host 127.0.0.1 --port 18000 --tier standard --concurrency 1 --vlm-server-url http://127.0.0.1:30000 --vlm-max-concurrency 1
```

文档解析适配边界是 `store.Models.Transcribe(context.Context, media, body)`。当前系统的模型配置与 Model Gateway 预算授权边界仍须遵守；不要把 MinerU `/v1/file_parse` 当作 `/chat/completions`。原生 DOCX 使用工作台现有解析器；复杂 DOCX 中可见内容与 OCR 引擎抽取结果可能不同，不能直接替换。

曾在 RTX 4070 SUPER 12GB 单机配置下运行标准扫描 PDF 和图片解析的记录，不能代表其他设备或吞吐。实际内存/显存取决于页面、上下文和并发；以 CUDA 推理日志确认 GPU 生效，不根据少量样本提高并发。真实 OCR 和模型调用验收见[检查与验收](../engineering/verification.md)，运行记录需放在仓外。
