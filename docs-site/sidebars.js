const doc = (id, label) => ({ type: 'doc', id, label });
const category = (label, items) => ({
  type: 'category',
  label,
  collapsible: true,
  collapsed: true,
  items,
});

module.exports = {
  handbook: [
    doc('index', '手册首页'),
    category('产品', [
      doc('product/capabilities', '能力与边界'),
      doc('product/workflow', '案件审查工作流'),
    ]),
    category('工程', [
      doc('engineering/community-guide', '用户上手与扩展指南'),
      doc('engineering/contributing', '贡献与协作'),
      doc('engineering/file-metadata', '文件元信息规范'),
      doc('engineering/licensing', '许可与贡献'),
      doc('engineering/local-development', '本地开发'),
      doc('engineering/verification', '检查与验收'),
    ]),
    category('架构', [
      doc('architecture/advisory', '建议层与确定性结论'),
      doc('architecture/domain-boundaries', '领域边界与持久化边界'),
      doc('architecture/execution', '独立服务与异步执行'),
      doc('architecture/overview', '架构概览'),
    ]),
    category('运维', [
      doc('operations/deployment', '部署模式'),
      doc('operations/document-processing', '文档解析与模型预算边界'),
      doc('operations/external-evidence', '外部证据与核验'),
      doc('operations/integrations', '真实语料联调'),
      doc('operations/k3d', '本地 k3d 演练'),
      doc('operations/multi-company-operations', '多租户身份与运营支持'),
      doc('operations/ocr-mineru', '本地 OCR（MinerU）'),
      doc('operations/tenant-restore', '备份、恢复与租户生命周期边界'),
    ]),
    category('术语', [
      doc('glossary/user-terms', '用户术语表'),
      doc('glossary/system-terms', '系统术语表'),
    ]),
  ],
};
