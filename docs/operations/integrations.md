# 真实语料联调

真实合同、记录器输出和结果清单都放在仓外交付目录。不要将合同原件、模型正文或可识别个人/机构的信息提交到源码仓。

## 准备清单

在仓库外建立版本 1 JSON 清单，根对象包含 `version`、`dataset_id`、`cases`。每个案例包含 `id`、`title`、`contract_type`、`materials`；材料包含 `role`、相对清单的 `path`、`sha256`、`source` 和 `data_class: "original"`。角色可使用 `draft_contract`、`procurement`、`award` 或 `attachment`。开始前核对材料关系；审查结论仍须人工核验。

模型调用可经仓外记录器转发。记录器只绑定本机回环地址；请求/响应正文可能含合同原文，应保存在私有目录：

```bash
python3 scripts/record_model_proxy.py \
  --upstream http://127.0.0.1:3425/v1 \
  --listen 127.0.0.1:3426 \
  --out ../contract-audit-delivery/model-records/real-run
```

独立服务的私有配置将 `LLM_BASE_URL` 指向记录器，并使用真实模型目录中的 `LLM_MODEL` 与 `OCR_MODEL`。容器访问宿主机记录器需要受限的网络转发；不要把记录器监听地址改成对外网卡。冻结模型身份所用服务必须配置相同地址和模型名称。记录器不保存凭据头，但请求/响应正文可包含敏感数据；是否转发到外部取决于真实上游。

## 运行语料

服务启动后执行：

```bash
python3 scripts/run_real_corpus.py \
  --manifest ../contract-audit-delivery/real-corpus/manifest.json \
  --base http://127.0.0.1:8080 \
  --request-timeout 1800 --review-timeout 900 \
  --model-record-dir ../contract-audit-delivery/model-records/real-run
```

`--case-id` 可重复指定清单中的案例；省略时处理全部。脚本为案例创建新案件，并把输入哈希、解析状态、标识、冻结快照、执行事件和发现写到仓外 `test-runs/`。失败时保留已经生成的记录。`--model-record-dir` 必须指向现存记录器目录。每次运行使用独立记录器或顺序执行，避免按时间归档时混入并发调用。报告和人工决定不会自动创建。

真实原件处理、模型结果和判断属于受控试点，不是 CI 测试。运行前先确认授权和数据处理范围；运行后的 PASS 只适用于对应清单、源码 SHA、环境与检查项目。
