import { Link } from "react-router";

export function NotFoundPage() {
  return (
    <section className="page">
      <h1>没有这个页面</h1>
      <p>可从工作台进入案件、审查工作区、设置或 API 文档。</p>
      <Link to="/cases">返回案件</Link>
    </section>
  );
}
