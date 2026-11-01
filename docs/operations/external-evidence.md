# 外部证据与核验

## 签名证据包

签名证据包需要 Ed25519 PKCS#8 私钥 `EVIDENCE_SIGNING_KEY_FILE` 和包含合同文字的 CJK TrueType 字体 `EVIDENCE_PDF_FONT_FILE`。由部署方管理密钥和字体；Compose 以只读方式挂载到 Document 服务。Kubernetes 可使用 `deployments/k8s/optional/evidence-export.yaml`，先配置其 Secret 和字体 PVC。

报告创建后可下载：

```text
/api/cases/{caseID}/reports/{reportID}/evidence-bundle
```

离线验证时，使用独立可信渠道获取的公钥或指纹；证据包内部自带的公钥不能单独作为信任依据：

```bash
go run ./cmd/verify-evidence -public-key <trusted-public-key.pem> <evidence-bundle.zip>
```

离线验签检查包完整性与签名。在线下载仍须当时的案件授权与对象访问控制；拥有文件不等于获得在线访问权限。

## 外部业务记录核验

跨系统核验默认关闭。`OPERATOR_AUDIT_ENABLED=false` 时 API 返回未启用状态且前端不显示入口。开发/测试可显式配置合成连接器：

```text
OPERATOR_AUDIT_ENABLED=true
EXTERNAL_EVIDENCE_MODE=fixture
EXTERNAL_EVIDENCE_CONNECTOR_ID=fixture.oss
EXTERNAL_EVIDENCE_SOURCE_SYSTEM=fixture-oss-east
```

`fixture` 仅用于 development/test，读取合成 OSS 记录。`live` 接入须先完成数据出境/处理审批，并配置获授权的真实来源；本手册不代表已启用真实连接器。

能力包声明场景、材料角色、精确 RulePack 版本、只读能力和比较器，不加载任意脚本或 SQL。对象映射需要人工确认；采集与核验受理分开。比较使用精确十进制与单位归一，不能无损转换的数值会拒绝受理，不舍入。核验结果不是自动人工结论或赔偿计算。带宽配置值不等于实际吞吐。能以有限小数无损转换的有理数（例如 `999/10` → `99.9`）才可用于精确比较；`1/3` 等不能有限精确表达的值会被拒绝，不舍入。原始事实、摘要和引用按尝试冻结。合同对象映射更正通过提案与独立复核切换版本，审批摘要绑定合同、冻结尝试、对象事实和依据；依据内容改变会使审批失效。审查影响计划只提供 explain/shadow；依赖不完整、集合变化、旧证据不可重绑定或候选/全量结果不一致时必须完整执行，复用保持关闭。

## 真实访问权限核对

本地演示环境可对运行中的 API 执行授权检查：

```bash
python3 scripts/check_real_access.py \
  --base http://127.0.0.1:8080 \
  --manifest ../contract-audit-delivery/real-corpus/manifest.json
```

脚本会创建独立验证案件，使用不同演示账号检查列表、原件、成员和写操作的状态码，结果写到仓外 `test-runs/`。这是实际运行检查，未执行时不能用代码存在或清单文件存在声称隔离 PASS。不要在真实数据上运行演示账号验证。
