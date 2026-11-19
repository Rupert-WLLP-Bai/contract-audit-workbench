import React from 'react';
import Layout from '@theme/Layout';
import Link from '@docusaurus/Link';

const handbookSections = [
  {
    title: '审查合同',
    links: [
      {label: '案件审查工作流', to: '/handbook/product/workflow'},
      {label: '能力与边界', to: '/handbook/product/capabilities'},
      {label: '用户术语表', to: '/handbook/glossary/user-terms'},
    ],
  },
  {
    title: '维护工作台',
    links: [
      {label: '部署模式', to: '/handbook/operations/deployment'},
      {label: '多租户身份与运营支持', to: '/handbook/operations/multi-company-operations'},
      {label: '本地开发', to: '/handbook/engineering/local-development'},
    ],
  },
];

export default function Home() {
  return (
    <Layout title="使用手册" description="合同智能审计工作台的产品、工程与运维手册">
      <main className="caw-home">
        <header className="caw-manual-heading">
          <h1>合同智能审计工作台使用手册</h1>
          <Link className="button button--primary" to="/handbook/">浏览完整手册</Link>
        </header>
        <div className="caw-manual-sections">
          {handbookSections.map(({title, links}) => (
            <section key={title}>
              <h2>{title}</h2>
              <ul className="caw-manual-links">
                {links.map(({label, to}) => (
                  <li key={to}><Link to={to}>{label}</Link></li>
                ))}
              </ul>
            </section>
          ))}
        </div>
      </main>
    </Layout>
  );
}
