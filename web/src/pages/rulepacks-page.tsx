import { useState } from "react";
import { Link } from "react-router";
import { errorMessage, isForbidden } from "../api/http";
import { useRulePacks } from "../api/queries";
import { PageStatus } from "../components/page-status";
import { StatusText } from "../components/status-text";

const STATE_LABELS = {
  draft: "草稿",
  validated: "已验证",
  approved: "已批准",
  published: "已发布",
} as const;

export function RulePacksPage() {
  const packs = useRulePacks();
  const [query, setQuery] = useState("");
  const [state, setState] = useState("all");
  const [selectedVersion, setSelectedVersion] = useState<string | null>(null);
  if (packs.isLoading) return <PageStatus kind="loading" title="正在读取规则包" />;
  if (packs.isError) {
    const forbidden = isForbidden(packs.error);
    return (
      <PageStatus
        kind={forbidden ? "denied" : "error"}
        title={forbidden ? "当前管理身份无权查看规则包" : "规则包列表没有加载"}
        detail={errorMessage(packs.error)}
        action={forbidden
          ? <Link className="button button-primary" to="/admin/settings">返回管理首页</Link>
          : <button className="button button-primary" type="button" onClick={() => void packs.refetch()}>重试</button>}
      />
    );
  }

  const rows = [...(packs.data ?? [])].sort((left, right) => left.id.localeCompare(right.id) || right.version.localeCompare(left.version));
  const needle = query.trim().toLocaleLowerCase();
  const filtered = rows.filter((pack) => (state === "all" || (state === "active" ? pack.is_active : pack.status === state)) &&
    `${pack.id} ${pack.version} ${pack.authority_sources.join(" ")}`.toLocaleLowerCase().includes(needle));
  const selected = filtered.find((pack) => `${pack.id}:${pack.version}` === selectedVersion) ?? filtered[0];
  const filters = [
    { id: "all", label: "全部版本", count: rows.length },
    { id: "active", label: "当前生效", count: rows.filter((pack) => pack.is_active).length },
    ...Object.entries(STATE_LABELS).map(([id, label]) => ({ id, label, count: rows.filter((pack) => pack.status === id).length })),
  ];
  return (
    <section className="page page-rulepacks">
      <header className="page-head">
        <div>
          <h1>规则库 / RulePack 治理</h1>
          <p>管理规则版本、验证与发布。已发布内容保持不可变，活动版本用于后续新审查。</p>
        </div>
        <Link className="button button-primary" to="/admin/rulepacks/new">创建草稿</Link>
      </header>
      <div className="governance-workspace">
        <nav className="governance-state-nav" aria-label="规则包版本分类">
          <h2>版本状态</h2>
          {filters.map((item) => <button key={item.id} type="button" aria-pressed={state === item.id} onClick={() => setState(item.id)}><span>{item.label}</span><span>{item.count}</span></button>)}
        </nav>
        <div className="governance-list">
      <div className="governance-filters">
        <label className="field">查找规则包<input type="search" placeholder="名称、版本或权威来源" value={query} onChange={(event) => setQuery(event.target.value)} /></label>
        <label className="field">版本状态<select value={state} onChange={(event) => setState(event.target.value)}>
          <option value="all">全部状态</option><option value="active">当前生效</option>
          {Object.entries(STATE_LABELS).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
        </select></label>
        <p className="meta" role="status">{filtered.length} / {rows.length} 个版本</p>
      </div>
      {rows.length === 0 ? (
        <PageStatus kind="empty" title="还没有规则包" detail="创建一个新草稿，再填写适用范围、有效期、比较参数和权威来源。" />
      ) : (
        filtered.length === 0 ? <PageStatus kind="empty" title="没有匹配的规则包" detail="调整关键词或状态筛选后重试。" /> :
        <div className="data-table-scroll">
          <table className="data-table rulepack-table">
            <thead><tr><th scope="col">规则包 / 版本</th><th scope="col">状态</th><th scope="col">有效期（含开始，不含结束）</th><th scope="col">权威来源</th><th scope="col">操作</th></tr></thead>
            <tbody>{filtered.map((pack) => <tr key={`${pack.id}:${pack.version}`} data-selected={selected?.id === pack.id && selected.version === pack.version}>
              <th scope="row"><Link to={`/admin/rulepacks/${encodeURIComponent(pack.id)}/versions/${encodeURIComponent(pack.version)}`}>{pack.id} · v{pack.version}</Link><span className="meta">租户 {pack.tenant_id}</span></th>
              <td><div className="rulepack-badges">
                <StatusText tone={pack.status === "published" ? "good" : pack.status === "draft" ? "neutral" : "attention"}>{STATE_LABELS[pack.status]}</StatusText>
                <StatusText tone={pack.is_active ? "good" : "neutral"}>{pack.is_active ? "当前生效" : "未生效"}</StatusText>
              </div></td>
              <td><div>{pack.effective_from}</div><div className="meta">至 {pack.effective_until}</div></td>
              <td>{pack.authority_sources.join("；") || "无"}</td>
              <td><div className="rulepack-row-actions"><button className="button button-quiet" type="button" aria-label={`预览 ${pack.id} v${pack.version}`} aria-pressed={selected?.id === pack.id && selected.version === pack.version} onClick={() => setSelectedVersion(`${pack.id}:${pack.version}`)}>预览</button><Link to={`/admin/rulepacks/${encodeURIComponent(pack.id)}/versions/${encodeURIComponent(pack.version)}`}>查看与管理</Link></div></td>
            </tr>)}</tbody>
          </table>
        </div>
      )}
        </div>
        {selected ? <aside className="rulepack-preview" aria-label="选中规则包概览">
          <header><span className="rulepack-preview-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M5 3h9l5 5v13H5zM14 3v6h5M8 13h8M8 17h6" /></svg></span><div><h2>{selected.id}</h2><p className="meta">版本 v{selected.version}</p></div></header>
          <h3>基本信息</h3>
          <dl>
            <div><dt>状态</dt><dd><StatusText tone={selected.status === "published" ? "good" : "neutral"}>{STATE_LABELS[selected.status]}</StatusText></dd></div>
            <div><dt>生效指针</dt><dd>{selected.is_active ? "当前生效" : "未生效"}</dd></div>
            <div><dt>租户</dt><dd>{selected.tenant_id}</dd></div>
            <div><dt>生效开始</dt><dd>{selected.effective_from}</dd></div>
            <div><dt>生效结束</dt><dd>{selected.effective_until}</dd></div>
          </dl>
          <h3>来源依据</h3>
          {selected.authority_sources.length ? <ul>{selected.authority_sources.map((source) => <li key={source}>{source}</li>)}</ul> : <p className="meta">尚未填写权威来源。</p>}
          <Link className="button button-primary" to={`/admin/rulepacks/${encodeURIComponent(selected.id)}/versions/${encodeURIComponent(selected.version)}`}>管理此版本</Link>
        </aside> : null}
      </div>
    </section>
  );
}
