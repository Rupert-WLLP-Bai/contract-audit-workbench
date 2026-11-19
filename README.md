<!-- File metadata (manifest mirror): -->
<!-- Purpose: Introduces the contract audit workbench and its single-machine experience, then guides readers through a synthetic 36- versus 24-month warranty review, frozen text and a human decision. -->
<!-- Author: Bai Junhao <baijunhao@cq.chinamobile.com> -->
<!-- Contact: +86 13617696321 -->
<!-- Created: 2026-10-25 -->
<!-- Updated: 2026-11-19 -->
<!-- Revision: r6 -->
<!-- Copyright: See LICENSE -->
<!-- End file metadata. -->

# 合同智能审计工作台

这是一个基于 Go 和 React 的合同审计工作台。经办和审查人员可以整理材料、按冻结原文核对条款、记录判断，再跟进整改、复审与报告。合同台账还可管理草稿、版本、会签与履约记录。

## 单机体验

安装本机工具后，在仓库根目录执行以下命令。 [本地开发手册](docs/engineering/local-development.md#环境)

```bash
make setup
make app-bootstrap
make app-config
make infra-up
make apps-up-fixture
```

打开默认业务工作台 `http://127.0.0.1:18080`，用开发账号 `demo-admin` 和密码 `admin-demo` 登录。本例用合成材料和本机容器提供的确定性模型。真实模型、OCR 和目标环境有单独的验收方法。 [检查与验收](docs/engineering/verification.md)

### 审查一份合成合同

1. 在「案件」中新建「采购 / ICT」案件，把表中的两句话分别存为 UTF-8 TXT。在「材料与版本」按对应角色上传，等待解析完成。

   | 材料角色 | TXT 内容                 |
   | -------- | ------------------------ |
   | 采购文件 | 采购文件要求质保36个月。 |
   | 拟签合同 | 拟签合同约定质保24个月。 |

2. 在「审查记录」打开「审查准备清单」，选择「完整审查」。按提示补齐必填项，清单就绪后开始审查。
3. 在「原文核对」阅读正文。黄色笔迹标出付款、质保、争议解决等常查位置，可用「查看重点」和上、下一个重点跳转。切换材料时打开「案件与材料」。PDF、PNG 和 JPEG 可切到「原件标注」核对版面，本例的 TXT 直接显示文字。
4. 对照两份冻结原文中的 36 个月与 24 个月，在「人工办理」记录「确认风险」及理由。引用无法核对时，保留为待核对或记录「证据不足」。

## 文档

- [案件审查工作流](docs/product/workflow.md)
- [能力与边界](docs/product/capabilities.md)
- [架构概览](docs/architecture/overview.md)
- [部署模式](docs/operations/deployment.md)
- [检查与验收](docs/engineering/verification.md)
- [贡献与协作](docs/engineering/contributing.md) · [仓库约定](AGENTS.md)
- [完整文档索引](docs/index.md)

## 许可

根目录许可文件 `LicenseRef-CAW-NC-1.0.0` 适用于约定的非商业用途。 [LICENSE](LICENSE) · [许可与贡献说明](docs/engineering/licensing.md)
