import React from 'react';
import Layout from '@theme/Layout';
import Link from '@docusaurus/Link';

const cards = [
  {
    kicker: '审查人员',
    title: '案件审查工作流',
    text: '从准备清单开始一次审查。',
    to: '/handbook/product/workflow',
  },
  {
    kicker: '首次使用',
    title: '能力与边界',
    text: '先看看工作台覆盖哪些能力。',
    to: '/handbook/product/capabilities',
  },
];

export default function Home() {
  return (
    <Layout
      title="合同智能审计工作台 · 使用手册"
      description="合同智能审计工作台的产品、工程与运维手册">
      <main id="top" className="caw-home">
        <section className="caw-hero">
          <span className="caw-eyebrow">使用手册</span>
          <h1>合同智能审计工作台</h1>
          <div className="caw-actions">
            <a className="caw-btn caw-btn-primary" href="#quick">快速开始</a>
            <Link className="caw-btn caw-btn-ghost" to="/handbook/">阅读用户手册</Link>
          </div>
        </section>

        <section className="caw-quick" id="quick">
          <div className="caw-wrap">
            <h2>快速开始</h2>
            <div className="caw-grid">
              {cards.map((card) => (
                <Link className="caw-card" to={card.to} key={card.title}>
                  <div className="caw-kicker">{card.kicker}</div>
                  <h3>{card.title}</h3>
                  <p>{card.text}</p>
                  <span className="caw-arrow">阅读 →</span>
                </Link>
              ))}
              <a className="caw-card" href="/api-docs">
                <div className="caw-kicker">开发者</div>
                <h3>API 开放平台</h3>
                <p>接口参考与在线试运行。</p>
                <span className="caw-arrow">打开 →</span>
              </a>
            </div>
          </div>
        </section>
      </main>
    </Layout>
  );
}
