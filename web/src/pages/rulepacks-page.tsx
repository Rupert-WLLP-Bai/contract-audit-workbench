// File metadata (manifest mirror):
// Purpose: Lists rule-pack versions and lifecycle states for the admin audience, linking to drafts, published detail and creation flows without treating shadow results as active rules.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-12
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { FileProtectOutlined } from "@ant-design/icons";
import { Button, Input, Table, Tabs, type TableProps } from "antd";
import { useState } from "react";
import { Link } from "react-router";
import { errorMessage, isForbidden } from "../api/http";
import { useRulePacks } from "../api/queries";
import { PageStatus } from "../components/page-status";
import { ListPage } from "../components/page-patterns";
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
        action={
          forbidden ? (
            <Button aria-label={"返回管理首页"} type="primary" href="/admin/settings">
              返回管理首页
            </Button>
          ) : (
            <Button
              aria-label={"重试"}
              type="primary"
              htmlType="button"
              onClick={() => void packs.refetch()}
            >
              重试
            </Button>
          )
        }
      />
    );
  }

  const rows = [...(packs.data ?? [])].sort(
    (left, right) => left.id.localeCompare(right.id) || right.version.localeCompare(left.version),
  );
  const needle = query.trim().toLocaleLowerCase();
  const filtered = rows.filter(
    (pack) =>
      (state === "all" || (state === "active" ? pack.is_active : pack.status === state)) &&
      `${pack.id} ${pack.version} ${pack.authority_sources.join(" ")}`
        .toLocaleLowerCase()
        .includes(needle),
  );
  const selected =
    filtered.find((pack) => `${pack.id}:${pack.version}` === selectedVersion) ?? filtered[0];
  const filters = [
    { id: "all", label: "全部版本", count: rows.length },
    {
      id: "active",
      label: "当前生效",
      count: rows.filter((pack) => pack.is_active).length,
    },
    ...Object.entries(STATE_LABELS).map(([id, label]) => ({
      id,
      label,
      count: rows.filter((pack) => pack.status === id).length,
    })),
  ];
  const columns: TableProps<(typeof filtered)[number]>["columns"] = [
    {
      title: "规则包 / 版本",
      key: "rulepack",
      render: (_, pack) => (
        <>
          <Link
            to={`/admin/rulepacks/${encodeURIComponent(pack.id)}/versions/${encodeURIComponent(pack.version)}`}
          >
            {pack.id}，v{pack.version}
          </Link>
          <span className="meta">租户 {pack.tenant_id}</span>
        </>
      ),
    },
    {
      title: "状态",
      key: "status",
      render: (_, pack) => (
        <div className="rulepack-badges">
          <StatusText
            tone={
              pack.status === "published"
                ? "good"
                : pack.status === "draft"
                  ? "neutral"
                  : "attention"
            }
          >
            {STATE_LABELS[pack.status]}
          </StatusText>
          <StatusText tone={pack.is_active ? "good" : "neutral"}>
            {pack.is_active ? "当前生效" : "未生效"}
          </StatusText>
        </div>
      ),
    },
    {
      title: "有效期（含开始，不含结束）",
      key: "effective",
      render: (_, pack) => (
        <>
          <div>{pack.effective_from}</div>
          <div className="meta">至 {pack.effective_until}</div>
        </>
      ),
    },
    {
      title: "规则依据来源",
      key: "authority",
      render: (_, pack) => pack.authority_sources.join("；") || "无",
    },
    {
      title: "操作",
      key: "actions",
      render: (_, pack) => (
        <div className="rulepack-row-actions">
          <Button
            type="text"
            htmlType="button"
            aria-label={`预览 ${pack.id} v${pack.version}`}
            aria-pressed={selected?.id === pack.id && selected.version === pack.version}
            onClick={() => setSelectedVersion(`${pack.id}:${pack.version}`)}
          >
            预览
          </Button>
          <Link
            to={`/admin/rulepacks/${encodeURIComponent(pack.id)}/versions/${encodeURIComponent(pack.version)}`}
          >
            查看与管理
          </Link>
        </div>
      ),
    },
  ];
  return (
    <ListPage
      className="page-rulepacks"
      title="规则库 / RulePack 治理"
      description="当前生效版本用于后续新审查。"
      actions={
        <Button aria-label={"创建草稿"} type="primary" href="/admin/rulepacks/new">
          创建草稿
        </Button>
      }
      summary={
        <p className="meta" role="status">
          {filtered.length} / {rows.length} 个版本
        </p>
      }
      filters={
        <>
          <Tabs
            activeKey={state}
            aria-label="规则包版本分类"
            items={filters.map(({ id, label, count }) => ({ key: id, label: `${label} ${count}` }))}
            onChange={setState}
          />
          <label className="field governance-version-search">
            查找规则包
            <Input
              type="search"
              placeholder="名称、版本或规则依据来源"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
          </label>
        </>
      }
    >
      <div className="governance-workspace">
        <div className="governance-list">
          {rows.length === 0 ? (
            <PageStatus
              kind="empty"
              title="还没有规则包"
              detail="创建一个新草稿，再填写适用范围、有效期、比较参数和规则依据来源。"
            />
          ) : filtered.length === 0 ? (
            <PageStatus
              kind="empty"
              title="没有匹配的规则包"
              detail="调整关键词或状态筛选后重试。"
            />
          ) : (
            <div className="data-table-scroll">
              <Table
                className="data-table rulepack-table"
                columns={columns}
                dataSource={filtered}
                rowKey={(pack) => `${pack.id}:${pack.version}`}
                pagination={false}
                scroll={{ x: 740 }}
                onRow={(pack) => ({
                  "aria-current": selected?.id === pack.id && selected.version === pack.version,
                })}
              />
            </div>
          )}
        </div>
        {selected ? (
          <aside className="rulepack-preview" aria-label="选中规则包概览">
            <header>
              <span className="rulepack-preview-icon" aria-hidden="true">
                <FileProtectOutlined />
              </span>
              <div>
                <h2>{selected.id}</h2>
                <p className="meta">版本 v{selected.version}</p>
              </div>
            </header>
            <h3>基本信息</h3>
            <dl>
              <div>
                <dt>状态</dt>
                <dd>
                  <StatusText tone={selected.status === "published" ? "good" : "neutral"}>
                    {STATE_LABELS[selected.status]}
                  </StatusText>
                </dd>
              </div>
              <div>
                <dt>生效指针</dt>
                <dd>{selected.is_active ? "当前生效" : "未生效"}</dd>
              </div>
              <div>
                <dt>租户</dt>
                <dd>{selected.tenant_id}</dd>
              </div>
              <div>
                <dt>生效开始</dt>
                <dd>{selected.effective_from}</dd>
              </div>
              <div>
                <dt>生效结束</dt>
                <dd>{selected.effective_until}</dd>
              </div>
            </dl>
            <h3>来源依据</h3>
            {selected.authority_sources.length ? (
              <ul>
                {selected.authority_sources.map((source) => (
                  <li key={source}>{source}</li>
                ))}
              </ul>
            ) : (
              <p className="meta">尚未填写规则依据来源。</p>
            )}
            <Button
              aria-label={"管理此版本"}
              type="primary"
              href={`/admin/rulepacks/${encodeURIComponent(selected.id)}/versions/${encodeURIComponent(selected.version)}`}
            >
              管理此版本
            </Button>
          </aside>
        ) : null}
      </div>
    </ListPage>
  );
}
