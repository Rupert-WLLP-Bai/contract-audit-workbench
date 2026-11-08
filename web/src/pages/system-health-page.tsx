// File metadata (manifest mirror):
// Purpose: Displays the read-only client and admin health workspaces. Top facts are derived only from services present in the health snapshot; missing observations stay 未报告 instead of being promoted to normal.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-08
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

import { useEffect, useState, type CSSProperties, type ReactNode } from "react";
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Drawer,
  Empty,
  Input,
  Select,
  Table,
  Tabs,
  Tag,
  Tree,
  type TableColumnsType,
} from "antd";
import {
  useSystemHealth,
  type HealthGroup,
  type HealthService,
  type HealthStatus,
  type ProductionComponent,
  type SystemHealth,
} from "../api/system-health";
import type { SessionAudience } from "../api/types";
import {
  errorMessage,
  isForbidden,
  isUnauthenticated,
} from "../api/http";
import { PageStatus } from "../components/page-status";
import { CollapsibleSection } from "../components/collapsible-section";
import { formatDateTime } from "../domain/review";
import "./system-health-page.css";

type HealthTone = "success" | "warning" | "error" | "processing" | "default";

const STATUS: Record<HealthStatus, { label: string; tone: HealthTone }> = {
  ready: { label: "正常", tone: "success" },
  degraded: { label: "降级", tone: "warning" },
  unavailable: { label: "不可用", tone: "error" },
  stopped: { label: "已停止", tone: "error" },
  starting: { label: "启动中", tone: "processing" },
  unknown: { label: "待确认", tone: "warning" },
  unconfigured: { label: "未启用", tone: "default" },
  completed: { label: "已完成", tone: "success" },
};
const GROUPS: Record<string, string> = {
  frontend: "前端站点",
  application: "应用服务",
  middleware: "基础中间件",
  identity: "身份服务",
  observability: "可观测服务",
  external: "模型与解析",
};
const CLIENT_TABS = ["服务概览", "依赖与容量", "近期异常"] as const;
const ADMIN_TABS = ["服务健康", "存储与网络", "容量与事件"] as const;
const ATTENTION_STATUSES = new Set<HealthStatus>([
  "degraded",
  "unavailable",
  "stopped",
  "starting",
  "unknown",
]);

type FactDefinition = {
  key: string;
  label: string;
  ids: string[];
  names: RegExp;
};

const CLIENT_FACT_DEFINITIONS: FactDefinition[] = [
  {
    key: "core",
    label: "Core",
    ids: ["core-api", "core"],
    names: /核心业务\s*API|^core(?:\s|$)|core\s*api/i,
  },
  {
    key: "worker",
    label: "Worker",
    ids: ["audit-worker", "worker"],
    names: /审查\s*Worker|^worker(?:\s|$)/i,
  },
  {
    key: "storage",
    label: "对象存储",
    ids: ["rustfs", "minio", "object-storage"],
    names: /对象存储|Object\s*Storage|RustFS|MinIO/i,
  },
  {
    key: "events",
    label: "事件连接",
    ids: ["nats", "event-bus", "event-stream"],
    names: /NATS|事件|Event\s*(?:Bus|Stream)/i,
  },
];
const ADMIN_FACT_DEFINITIONS: FactDefinition[] = [
  {
    key: "core",
    label: "Core",
    ids: ["core-api", "core"],
    names: /核心业务\s*API|^core(?:\s|$)|core\s*api/i,
  },
  {
    key: "database",
    label: "数据库",
    ids: ["db", "postgres", "postgresql"],
    names: /PostgreSQL\s*业务库|业务数据库|^database$|PostgreSQL/i,
  },
  {
    key: "storage",
    label: "对象存储",
    ids: ["rustfs", "minio", "object-storage"],
    names: /对象存储|Object\s*Storage|RustFS|MinIO/i,
  },
  {
    key: "worker",
    label: "Worker",
    ids: ["audit-worker", "worker"],
    names: /审查\s*Worker|^worker(?:\s|$)/i,
  },
];

type HealthFact = {
  key: string;
  label: string;
  value: string;
  tone: HealthTone;
  observedAt?: string;
  missing: boolean;
  detail: string;
};

type HealthIssue = {
  key: string;
  title: string;
  detail: string;
};

type DependencyRow = {
  key: string;
  fromName: string;
  toName: string;
  status: string;
  detail: string;
};

function HealthTag({ status }: { status: HealthStatus }) {
  return <Tag color={STATUS[status].tone}>{STATUS[status].label}</Tag>;
}

function healthStatusSummary(items: HealthService[]): string {
  const counts = new Map<HealthStatus, number>();
  for (const item of items) counts.set(item.status, (counts.get(item.status) ?? 0) + 1);
  return [...counts.entries()]
    .map(([status, count]) => `${STATUS[status].label} ${count}`)
    .join(" · ");
}

function logicalId(service: HealthService): string {
  return service.logical_id ?? service.id;
}

function matchesFact(id: string, name: string, definition: FactDefinition): boolean {
  const normalizedId = id.toLocaleLowerCase();
  return (
    definition.ids.some((item) => item.toLocaleLowerCase() === normalizedId) ||
    definition.names.test(name)
  );
}

function latestObservedAt(items: HealthService[]): string | undefined {
  return items.reduce<string | undefined>(
    (latest, item) =>
      item.observed_at && (!latest || item.observed_at > latest)
        ? item.observed_at
        : latest,
    undefined,
  );
}

function aggregateServiceStatus(items: HealthService[]): HealthStatus {
  const statuses = new Set(items.map((item) => item.status));
  const priority: HealthStatus[] = [
    "unavailable",
    "stopped",
    "degraded",
    "starting",
    "unknown",
    "ready",
    "completed",
    "unconfigured",
  ];
  return priority.find((status) => statuses.has(status)) ?? "unknown";
}

function groupObservationLabel(group: HealthGroup): string {
  if (
    group.observed_instances === 0 &&
    (group.status === "unknown" || group.status === "unconfigured")
  )
    return "未报告";
  return STATUS[group.status].label;
}

function buildHealthFacts(
  data: SystemHealth,
  audience: SessionAudience,
): HealthFact[] {
  const definitions =
    audience === "admin" ? ADMIN_FACT_DEFINITIONS : CLIENT_FACT_DEFINITIONS;
  return definitions.map((definition) => {
    const group = data.groups.find((item) =>
      matchesFact(item.id, item.name, definition),
    );
    const instances = data.services.filter((item) =>
      matchesFact(logicalId(item), item.name, definition),
    );
    const missing = (detail: string): HealthFact => ({
      key: definition.key,
      label: definition.label,
      value: "未报告",
      tone: "default",
      missing: true,
      detail,
    });
    if (!group && instances.length === 0)
      return missing("未在下发的健康快照中找到该服务的观测。");
    if (
      group &&
      group.observed_instances === 0 &&
      (group.status === "unknown" || group.status === "unconfigured")
    )
      return missing(group.detail);
    const status = group?.status ?? aggregateServiceStatus(instances);
    const running = instances.find(
      (item) => item.container?.state.toLowerCase() === "running",
    );
    if ((!group || status === "unknown") && running)
      return {
        key: definition.key,
        label: definition.label,
        value: "运行中",
        tone: "processing",
        observedAt: group?.observed_at ?? latestObservedAt(instances),
        missing: false,
        detail: group?.detail ?? running.detail,
      };
    return {
      key: definition.key,
      label: definition.label,
      value: group ? groupObservationLabel(group) : STATUS[status].label,
      tone: STATUS[status].tone,
      observedAt:
        group?.observed_at ??
        latestObservedAt(instances) ??
        (group ? data.observed_at : undefined),
      missing: false,
      detail: group?.detail ?? instances[0]?.detail ?? "",
    };
  });
}

const DEPENDENCY_NAMES: Record<string, string> = {
  "core-api": "核心业务 API",
  "audit-worker": "审查 Worker",
  "audit-execution": "审查执行服务",
  "document-service": "文档服务",
  "model-gateway": "模型网关",
  "tool-gateway": "工具网关",
  db: "PostgreSQL 业务库",
  rustfs: "RustFS 对象存储",
  nats: "NATS JetStream",
};

function buildDependencyRows(data: SystemHealth): DependencyRow[] {
  const edges: {
    key: string;
    from: HealthService;
    targetId: string;
    target?: HealthService;
  }[] = [];
  const seen = new Set<string>();
  for (const from of data.services) {
    const fromId = logicalId(from);
    for (const dependencyId of from.depends_on) {
      const key = `${fromId}->${dependencyId}`;
      if (seen.has(key)) continue;
      seen.add(key);
      edges.push({
        key,
        from,
        targetId: dependencyId,
        target: data.services.find(
          (item) => logicalId(item) === dependencyId || item.id === dependencyId,
        ),
      });
    }
  }
  const preferred = [
    { source: "core-api", target: "db" },
    { source: "document-service", target: "rustfs" },
    { source: "audit-execution", target: "nats" },
  ];
  const chosen: typeof edges = [];
  for (const preference of preferred) {
    const match =
      edges.find(
        (edge) =>
          logicalId(edge.from) === preference.source &&
          edge.targetId === preference.target,
      ) ?? edges.find((edge) => edge.targetId === preference.target);
    if (match && !chosen.includes(match)) chosen.push(match);
  }
  for (const edge of edges) {
    if (chosen.length >= 3) break;
    if (!chosen.includes(edge)) chosen.push(edge);
  }
  return chosen.slice(0, 3).map((edge) => {
    const group = data.groups.find((item) => item.id === edge.targetId);
    if (!group && !edge.target)
      return {
        key: edge.key,
        fromName: edge.from.name,
        toName: DEPENDENCY_NAMES[edge.targetId] ?? edge.targetId,
        status: "未报告",
        detail: "依赖目标未在下发的健康快照中报告。",
      };
    const target = edge.target;
    return {
      key: edge.key,
      fromName: edge.from.name,
      toName:
        target?.name ?? DEPENDENCY_NAMES[edge.targetId] ?? edge.targetId,
      status: group
        ? groupObservationLabel(group)
        : STATUS[target?.status ?? "unknown"].label,
      detail: group?.detail ?? target?.detail ?? "未报告",
    };
  });
}

function buildRecentIssues(data: SystemHealth, facts: HealthFact[]): HealthIssue[] {
  const issues: HealthIssue[] = [];
  for (const fact of facts) {
    if (!fact.missing) continue;
    issues.push({
      key: `fact-${fact.key}`,
      title: `${fact.label}未报告`,
      detail: "未在下发的健康快照中找到该服务的观测，不能按正常展示。",
    });
  }
  for (const group of data.groups) {
    if (!ATTENTION_STATUSES.has(group.status)) continue;
    if (
      group.observed_instances === 0 &&
      (group.status === "unknown" || group.status === "unconfigured")
    )
      continue;
    issues.push({
      key: `group-${group.id}`,
      title: `${group.name} · ${STATUS[group.status].label}`,
      detail: group.detail,
    });
  }
  return issues;
}

function modeLabel(mode: string): string {
  if (mode === "kubernetes") return "Kubernetes 部署";
  if (mode === "distributed") return "独立服务部署";
  return "单进程部署";
}

function HealthPanel({
  title,
  extra,
  children,
  label,
}: {
  title: string;
  extra?: ReactNode;
  children: ReactNode;
  label?: string;
}) {
  return (
    <section className="health-panel" aria-label={label ?? title}>
      <header className="health-panel-head">
        <h2>{title}</h2>
        {extra}
      </header>
      <div className="health-panel-body">{children}</div>
    </section>
  );
}

function FactCard({ fact }: { fact: HealthFact }) {
  return (
    <li
      className="health-fact"
      aria-label={fact.label}
      data-missing={fact.missing ? "true" : "false"}
      title={fact.detail || undefined}
    >
      <span className="health-fact-label">{fact.label}</span>
      <strong>{fact.value}</strong>
      <Tag color={fact.tone}>{fact.value}</Tag>
      <span className="health-fact-time">
        观测时间：{fact.observedAt ? formatDateTime(fact.observedAt) : "未报告"}
      </span>
    </li>
  );
}

function DependencySummary({ rows }: { rows: DependencyRow[] }) {
  if (rows.length === 0)
    return <p className="health-note">未取得可核对的依赖关系。</p>;
  return (
    <ul className="health-dependency-list">
      {rows.map((row) => (
        <li key={row.key}>
          <span className="health-dependency-title">
            {row.fromName} → {row.toName}
          </span>
          <span className="health-dependency-meta">
            {row.status} · {row.detail}
          </span>
        </li>
      ))}
    </ul>
  );
}

function IssueList({ issues }: { issues: HealthIssue[] }) {
  if (issues.length === 0)
    return <p className="health-issue-empty">当前观测没有需关注的异常。</p>;
  return (
    <ul className="health-issue-list">
      {issues.map((issue) => (
        <li key={issue.key}>
          <strong>{issue.title}</strong>
          <span>{issue.detail}</span>
        </li>
      ))}
    </ul>
  );
}

function HealthSummary({ data }: { data: SystemHealth }) {
  const readyInstances = data.groups.reduce(
    (sum, item) => sum + item.ready_instances,
    0,
  );
  const observedInstances = data.groups.reduce(
    (sum, item) => sum + item.observed_instances,
    0,
  );
  const attentionGroups = data.groups.filter((item) =>
    ATTENTION_STATUSES.has(item.status),
  );
  const unconfiguredGroups = data.groups.filter(
    (item) => item.status === "unconfigured",
  );
  return (
    <ol className="health-summary" aria-label="系统健康统计">
      {[
        { title: "逻辑服务", value: data.groups.length, tone: "progress" },
        {
          title: "实例就绪",
          value: `${readyInstances}/${observedInstances}`,
          tone: readyInstances === observedInstances ? "compatible" : "insufficient",
        },
        {
          title: "实际容器",
          value: data.services.filter((item) => item.container !== null).length,
          tone: "neutral",
        },
        {
          title: "需关注",
          value: attentionGroups.length,
          tone: attentionGroups.length > 0 ? "insufficient" : "compatible",
        },
        { title: "未启用", value: unconfiguredGroups.length, tone: "neutral" },
      ].map((item) => (
        <li key={item.title} data-tone={item.tone}>
          <span className="health-summary-label">{item.title}</span>
          <strong>{item.value}</strong>
        </li>
      ))}
    </ol>
  );
}

function HealthToolbar({
  count,
  mode,
  group,
  search,
  groups,
  onGroup,
  onSearch,
}: {
  count: number;
  mode: string;
  group: string;
  search: string;
  groups: Record<string, string>;
  onGroup: (value: string) => void;
  onSearch: (value: string) => void;
}) {
  return (
    <div className="health-toolbar">
      <span className="health-note">
        {count} 项，{modeLabel(mode)}
      </span>
      <label>
        <span>服务分组</span>
        <Select
          aria-label="服务分组"
          value={group}
          onChange={onGroup}
          style={{ minWidth: 160 }}
          options={[
            { label: "全部服务", value: "all" },
            ...Object.entries(groups).map(([value, label]) => ({
              value,
              label,
            })),
          ]}
        />
      </label>
      <label>
        <span>搜索服务</span>
        <Input
          aria-label="搜索服务"
          allowClear
          value={search}
          onChange={(event) => onSearch(event.target.value)}
          placeholder="服务、容器或镜像"
        />
      </label>
    </div>
  );
}

function GroupTable({
  groups,
  services,
  onSelect,
}: {
  groups: HealthGroup[];
  services: HealthService[];
  onSelect: (id: string) => void;
}) {
  const columns: TableColumnsType<HealthGroup> = [
    {
      title: "逻辑服务",
      dataIndex: "name",
      render: (_: unknown, group) => (
        <span>
          {group.name} <code className="health-note">{group.id}</code>
        </span>
      ),
    },
    {
      title: "状态",
      key: "status",
      width: 110,
      render: (_: unknown, group) => <HealthTag status={group.status} />,
    },
    {
      title: "就绪 / 期望",
      key: "replicas",
      width: 150,
      render: (_: unknown, group) =>
        group.expected_instances > 0
          ? `${group.ready_instances} / ${group.expected_instances}`
          : `${group.ready_instances} / 未采集`,
    },
    {
      title: "已观测实例",
      dataIndex: "observed_instances",
      width: 120,
    },
    {
      title: "期望来源",
      key: "expected_source",
      width: 180,
      render: (_: unknown, group) => EXPECTED_SOURCE[group.expected_source],
    },
    {
      title: "最近观测",
      key: "observed_at",
      width: 180,
      render: (_: unknown, group) =>
        group.observed_at ? formatDateTime(group.observed_at) : "未观测",
    },
    {
      title: "实例",
      key: "instances",
      width: 110,
      render: (_: unknown, group) => {
        const instance = services.find(
          (service) =>
            (service.logical_id ?? service.id) === group.id &&
            service.container !== null,
        );
        return instance ? (
          <Button
            type="link"
            size="small"
            aria-label={`查看 ${group.name} 的实例`}
            onClick={() => onSelect(instance.instance_id ?? instance.id)}
          >
            查看实例
          </Button>
        ) : (
          "无"
        );
      },
    },
  ];
  return (
    <Table
      rowKey="id"
      size="small"
      columns={columns}
      dataSource={groups}
      scroll={{ x: 1100 }}
      pagination={false}
      locale={{ emptyText: "没有服务组观测" }}
    />
  );
}

function Topology({
  services,
  onSelect,
}: {
  services: HealthService[];
  onSelect: (id: string) => void;
}) {
  return (
    <Tree
      showLine
      blockNode
      defaultExpandAll
      treeData={Object.entries(GROUPS).flatMap(([group, title]) => {
        const items = services.filter((item) => item.category === group);
        return items.length
          ? [
              {
                key: `group:${group}`,
                title,
                selectable: false,
                children: items.map((item) => ({
                  key: `service:${item.instance_id ?? item.id}`,
                  title: (
                    <span>
                      {item.name} <HealthTag status={item.status} />
                    </span>
                  ),
                  children: item.depends_on.map((id) => ({
                    key: `dependency:${item.id}:${id}`,
                    title: `依赖：${services.find((service) => service.id === id)?.name ?? id}`,
                  })),
                })),
              },
            ]
          : [];
      })}
      onSelect={(keys) => {
        const key = String(keys[0] ?? "");
        if (key.startsWith("service:")) onSelect(key.slice(8));
        else if (key.startsWith("dependency:"))
          onSelect(key.split(":").slice(2).join(":"));
      }}
    />
  );
}

const ARCHITECTURE_GROUPS: Record<string, string> = {
  platform: "集群与服务治理",
  storage: "数据库与存储",
  messaging: "消息通知",
  identity: "身份认证",
  observability: "可观测",
  application: "应用与 AI 执行",
};
const VERIFICATION: Record<ProductionComponent["verification"], string> = {
  PASS: "已验收",
  FAIL: "验收失败",
  BLOCKED: "验收受阻",
  NOT_RUN: "未验收",
  NOT_APPLICABLE: "不适用",
};

function ProductionArchitecture({
  components,
  services,
  onService,
}: {
  components: ProductionComponent[];
  services: HealthService[];
  onService: (id: string) => void;
}) {
  const [selected, setSelected] = useState<ProductionComponent | null>(null);
  const columns: TableColumnsType<ProductionComponent> = [
    {
      title: "组件",
      dataIndex: "name",
      width: 240,
      render: (_: unknown, item) => (
        <Button
          aria-label={String(item.name)}
          type="link"
          onClick={() => setSelected(item)}
          styles={{ content: { whiteSpace: "normal", textAlign: "left" } }}
        >
          {item.name}
        </Button>
      ),
    },
    { title: "生产目标", dataIndex: "target", width: 330 },
    { title: "当前观测", dataIndex: "observed", width: 400 },
    {
      title: "观测接入",
      key: "state",
      width: 125,
      render: (_: unknown, item) => (
        <Tag color={item.state === "observed" ? "processing" : "default"}>
          {item.state === "observed" ? "已有相关观测" : "未接入观测"}
        </Tag>
      ),
    },
    {
      title: "生产验收",
      key: "verification",
      width: 145,
      render: (_: unknown, item) => (
        <Tag
          color={
            item.verification === "PASS"
              ? "success"
              : item.verification === "FAIL"
                ? "error"
                : "default"
          }
        >
          {item.verification}，{VERIFICATION[item.verification]}
        </Tag>
      ),
    },
  ];
  return (
    <>
      <Alert
        type="info"
        showIcon
        title="生产方案与当前部署对照"
        description="这里显示当前连接状态；生产副本、容灾和专项验证需单独确认。"
      />
      <Table
        rowKey="id"
        columns={columns}
        dataSource={components}
        size="small"
        scroll={{ x: 1240 }}
        pagination={{
          defaultPageSize: 25,
          showSizeChanger: true,
          pageSizeOptions: [10, 25, 50],
          showTotal: (total) => `共 ${total} 项生产架构组件`,
        }}
        locale={{ emptyText: "没有匹配的架构组件" }}
      />
      <Drawer
        open={selected !== null}
        title={selected?.name}
        onClose={() => setSelected(null)}
        size={600}
        mask={false}
        push={false}
        destroyOnHidden
      >
        {selected ? (
          <Descriptions
            column={1}
            bordered
            size="small"
            items={[
              {
                key: "category",
                label: "所属领域",
                children:
                  ARCHITECTURE_GROUPS[selected.category] ?? selected.category,
              },
              { key: "target", label: "生产目标", children: selected.target },
              { key: "actual", label: "当前证据", children: selected.observed },
              {
                key: "verification",
                label: "验收结果",
                children: `${selected.verification}，${VERIFICATION[selected.verification]}`,
              },
              {
                key: "services",
                label: "相关服务",
                children: selected.service_ids.length
                  ? selected.service_ids.map((id) => {
                      const service = services.find((item) => item.id === id);
                      return service ? (
                        <Button
                          aria-label={String(service.name)}
                          key={id}
                          type="link"
                          onClick={() => {
                            setSelected(null);
                            onService(id);
                          }}
                        >
                          {service.name}
                        </Button>
                      ) : (
                        <Tag key={id}>{id}，无观测</Tag>
                      );
                    })
                  : "需要 Kubernetes / Operator 等独立观测源",
              },
              {
                key: "reference",
                label: "技术依据",
                children: (
                  <Button
                    aria-label={"官方文档"}
                    type="link"
                    href={selected.reference}
                    target="_blank"
                    rel="noopener noreferrer"
                  >
                    官方文档
                  </Button>
                ),
              },
            ]}
          />
        ) : null}
      </Drawer>
    </>
  );
}

const EXPECTED_SOURCE: Record<HealthGroup["expected_source"], string> = {
  deployment: "Kubernetes Deployment",
  config: "部署配置",
  catalog: "内置服务清单",
  unavailable: "未采集",
};

function ServiceInventory({
  items,
  onSelect,
}: {
  items: HealthService[];
  onSelect: (id: string) => void;
}) {
  return (
    <>
      {Object.entries(GROUPS).map(([id, name]) => {
        const groupItems = items.filter((item) => item.category === id);
        return groupItems.length ? (
          <section className="health-group" key={id} aria-label={name}>
            <CollapsibleSection
              id={`health-group-${id}`}
              title={name}
              count={groupItems.length}
              meta={healthStatusSummary(groupItems)}
            >
              <div className="health-card-grid">
                {groupItems.map((item) => (
                  <Card
                    role="group"
                    aria-label={String(item.name)}
                    className="health-service-card"
                    size="small"
                    key={item.id}
                    title={item.name}
                    extra={<HealthTag status={item.status} />}
                  >
                    <code className="health-target">
                      {item.target || "未配置连接地址"}
                    </code>
                    <p className="health-service-detail">{item.detail}</p>
                    <div className="health-card-footer">
                      <span>
                        {item.container?.name ??
                          (item.kind === "container"
                            ? "未发现部署容器"
                            : "外部集成")}
                      </span>
                      <Button
                        type="link"
                        size="small"
                        aria-label={`查看 ${item.name}`}
                        onClick={() => onSelect(item.instance_id ?? item.id)}
                      >
                        详情
                      </Button>
                    </div>
                  </Card>
                ))}
              </div>
            </CollapsibleSection>
          </section>
        ) : null;
      })}
    </>
  );
}

function ClusterPanel({ cluster }: { cluster: NonNullable<SystemHealth["cluster"]> }) {
  return (
    <Card
      role="group"
      aria-label={"Kubernetes 节点"}
      title="Kubernetes 节点"
      extra={
        <Tag color={cluster.status === "ready" ? "processing" : "error"}>
          {cluster.namespace}
        </Tag>
      }
    >
      <p>{cluster.detail}</p>
      <Table
        rowKey="name"
        size="small"
        pagination={false}
        dataSource={cluster.nodes}
        columns={[
          { title: "节点", dataIndex: "name" },
          {
            title: "就绪状态",
            key: "status",
            render: (_, node) => <HealthTag status={node.status} />,
          },
          { title: "Kubernetes 版本", dataIndex: "version" },
        ]}
        locale={{ emptyText: "未取得节点观测" }}
      />
    </Card>
  );
}

function ContainerTable({
  items,
  onSelect,
}: {
  items: HealthService[];
  onSelect: (id: string) => void;
}) {
  const columns: TableColumnsType<HealthService> = [
    {
      title: "容器名称",
      key: "name",
      width: 280,
      sorter: (a, b) =>
        (a.container?.name ?? "").localeCompare(b.container?.name ?? ""),
      render: (_: unknown, item) => (
        <Button
          aria-label={String(item.container?.name)}
          type="link"
          onClick={() => onSelect(item.instance_id ?? item.id)}
          styles={{
            content: {
              whiteSpace: "normal",
              textAlign: "left",
              overflowWrap: "anywhere",
            },
          }}
        >
          {item.container?.name}
        </Button>
      ),
    },
    {
      title: "节点",
      key: "node",
      width: 140,
      render: (_: unknown, item) => item.node_name ?? "未采集",
    },
    {
      title: "实例 ID",
      key: "instance",
      width: 180,
      render: (_: unknown, item) => <code>{item.instance_id ?? "未采集"}</code>,
    },
    {
      title: "镜像",
      key: "image",
      width: 250,
      render: (_: unknown, item) => <code>{item.container?.image}</code>,
    },
    {
      title: "进程状态",
      key: "state",
      width: 110,
      render: (_: unknown, item) => item.container?.state,
    },
    {
      title: "健康状态",
      key: "health",
      width: 110,
      render: (_: unknown, item) => <HealthTag status={item.status} />,
    },
    {
      title: "重启次数",
      key: "restarts",
      width: 100,
      sorter: (a, b) =>
        (a.container?.restarts ?? 0) - (b.container?.restarts ?? 0),
      render: (_: unknown, item) => item.container?.restarts,
    },
    {
      title: "启动时间",
      key: "started",
      width: 175,
      render: (_: unknown, item) =>
        item.container?.started_at &&
        !item.container.started_at.startsWith("0001-")
          ? formatDateTime(item.container.started_at)
          : "未启动",
    },
  ];
  return (
    <Table<HealthService>
      rowKey="id"
      columns={columns}
      dataSource={items}
      size="small"
      scroll={{ x: 1340 }}
      pagination={{
        defaultPageSize: 20,
        showSizeChanger: true,
        pageSizeOptions: [10, 20, 50],
        showTotal: (total) => `共 ${total} 个容器`,
      }}
      locale={{ emptyText: "当前筛选范围没有容器" }}
    />
  );
}

export function SystemHealthPage({
  audience = "client",
}: {
  audience?: SessionAudience;
}) {
  const tabs = audience === "admin" ? ADMIN_TABS : CLIENT_TABS;
  const query = useSystemHealth(audience);
  const [tab, setTab] = useState<string>(tabs[0]);
  const [search, setSearch] = useState("");
  const [group, setGroup] = useState("all");
  const [selectedId, setSelectedId] = useState("");
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 10_000);
    return () => clearInterval(timer);
  }, []);
  // A revoked session or a permission change must not leave the previous
  // deployment snapshot on screen, even if React Query still caches it.
  if (isUnauthenticated(query.error) || isForbidden(query.error))
    return (
      <PageStatus
        kind="denied"
        title="没有访问系统健康的权限"
        detail={errorMessage(query.error)}
      />
    );
  if (query.isLoading)
    return <PageStatus kind="loading" title="正在读取系统健康" />;
  if (!query.data)
    return (
      <PageStatus
        kind="error"
        title="系统健康没有加载"
        detail={errorMessage(query.error)}
        action={
          <Button aria-label={"重试"} onClick={() => void query.refetch()}>
            重试
          </Button>
        }
      />
    );
  const data = query.data;
  const observedAt = Date.parse(data.observed_at);
  const ageSeconds = Math.max(0, Math.floor((now - observedAt) / 1000));
  const stale = ageSeconds > 75;
  const partial =
    data.source === "docker_partial" || data.source === "kubernetes_partial";
  const attentionIds = new Set(
    data.services
      .filter((item) => ATTENTION_STATUSES.has(item.status))
      .map((item) => item.id),
  );
  const needle = search.trim().toLocaleLowerCase();
  const visible = data.services.filter(
    (item) =>
      (group === "all" || item.category === group) &&
      `${item.name} ${item.id} ${item.target} ${item.container?.name ?? ""} ${item.container?.image ?? ""}`
        .toLocaleLowerCase()
        .includes(needle),
  );
  const architecture = data.architecture.filter(
    (item) =>
      (group === "all" || item.category === group) &&
      `${item.name} ${item.target} ${item.observed}`
        .toLocaleLowerCase()
        .includes(needle),
  );
  const facts = buildHealthFacts(data, audience);
  const dependencies = buildDependencyRows(data);
  const issues = buildRecentIssues(data, facts);
  const attentionServices = visible.filter((item) => attentionIds.has(item.id));
  const containers = visible.filter((item) => item.container !== null);
  const tabsConfig = tabs;
  const overviewKey = tabsConfig[0];
  const capacityKey = tabsConfig[1];
  const exceptionKey = tabsConfig[2];
  const selected =
    data.services.find(
      (item) => (item.instance_id ?? item.id) === selectedId,
    ) ?? data.services.find((item) => (item.logical_id ?? item.id) === selectedId);
  const pageStyle: CSSProperties & Record<`--health-${string}`, string> = {
    background: "var(--surface-page)",
    color: "var(--text-primary)",
    "--health-bg": "var(--surface-raised)",
    "--health-muted": "var(--text-secondary)",
    "--health-line": "var(--border-default)",
    "--health-primary": "var(--action-primary)",
    "--health-success": "var(--state-compatible)",
    "--health-success-bg": "var(--state-compatible-bg)",
    "--health-error": "var(--state-conflict)",
    "--health-error-bg": "var(--state-conflict-bg)",
    "--health-radius": "var(--panel-radius)",
  };
  const overviewTab = (
    <>
      <div className="health-caveat" role="note">
        <span className="health-caveat-icon" aria-hidden="true">
          i
        </span>
        <p>
          {audience === "admin"
            ? "管理端可见信息按当前管理身份隔离；容量与高可用请以正式验收结果为准。"
            : "组件健康与业务结论无关；缺失观测显示未报告，不补成正常。"}
        </p>
      </div>
      <ul className="health-facts" aria-label="健康概览关键事实">
        {facts.map((fact) => (
          <FactCard key={fact.key} fact={fact} />
        ))}
      </ul>
      <div className="health-overview-grid">
        <HealthPanel title="服务与依赖">
          <DependencySummary rows={dependencies} />
        </HealthPanel>
        <HealthPanel
          title="近期异常"
          extra={issues.length > 0 ? <Tag color="warning">{issues.length} 项</Tag> : null}
        >
          <IssueList issues={issues.slice(0, 3)} />
          {issues.length > 3 ? (
            <p className="health-note">另有 {issues.length - 3} 项，请在{exceptionKey}中查看。</p>
          ) : null}
          <p className="health-note health-issue-note">
            诊断入口只展示当前身份获准的内容。
          </p>
          <div className="health-panel-actions">
            <Button aria-label="查看诊断" onClick={() => setTab(exceptionKey)}>
              查看诊断
            </Button>
          </div>
        </HealthPanel>
      </div>
      <CollapsibleSection
        id="health-service-inventory"
        title="服务明细"
        summaryLabel="展开服务明细"
        count={visible.length}
        meta={healthStatusSummary(visible)}
      >
        <ServiceInventory items={visible} onSelect={setSelectedId} />
      </CollapsibleSection>
    </>
  );
  const capacityTab = (
    <>
      <HealthToolbar
        count={visible.length}
        mode={data.mode}
        group={group}
        search={search}
        groups={GROUPS}
        onGroup={setGroup}
        onSearch={setSearch}
      />
      <HealthSummary data={data} />
      <Card
        role="group"
        aria-label={"服务组与副本"}
        title="服务组与副本"
        extra={
          <Tag color={stale || partial ? "warning" : "processing"}>
            {stale ? "观测过期" : partial ? "部分观测" : "当前观测"}
          </Tag>
        }
      >
        <GroupTable
          groups={data.groups}
          services={data.services}
          onSelect={setSelectedId}
        />
      </Card>
      {data.cluster ? <ClusterPanel cluster={data.cluster} /> : null}
      <CollapsibleSection
        id="health-dependency-topology"
        title="依赖拓扑"
        summaryLabel="展开依赖拓扑"
        count={visible.length}
        meta={healthStatusSummary(visible)}
      >
        <Topology services={visible} onSelect={setSelectedId} />
      </CollapsibleSection>
      <CollapsibleSection
        id="health-containers"
        title="容器清单"
        summaryLabel="展开容器清单"
        count={containers.length}
      >
        <ContainerTable items={containers} onSelect={setSelectedId} />
      </CollapsibleSection>
    </>
  );
  const exceptionTab = (
    <>
      <HealthToolbar
        count={attentionServices.length}
        mode={data.mode}
        group={group}
        search={search}
        groups={GROUPS}
        onGroup={setGroup}
        onSearch={setSearch}
      />
      <HealthPanel title="近期异常">
        <IssueList issues={issues} />
      </HealthPanel>
      {attentionServices.length === 0 ? (
        <Empty description="当前筛选范围没有需关注的服务" />
      ) : (
        <ServiceInventory items={attentionServices} onSelect={setSelectedId} />
      )}
    </>
  );
  const architectureTab = (
    <>
      <HealthToolbar
        count={architecture.length}
        mode={data.mode}
        group={group}
        search={search}
        groups={ARCHITECTURE_GROUPS}
        onGroup={setGroup}
        onSearch={setSearch}
      />
      <ProductionArchitecture
        components={architecture}
        services={data.services}
        onService={setSelectedId}
      />
      <CollapsibleSection
        id="health-dependency-topology"
        title="依赖拓扑"
        summaryLabel="展开依赖拓扑"
        count={data.services.length}
      >
        <Topology services={data.services} onSelect={setSelectedId} />
      </CollapsibleSection>
    </>
  );
  const eventTab = (
    <>
      <HealthToolbar
        count={visible.length}
        mode={data.mode}
        group={group}
        search={search}
        groups={GROUPS}
        onGroup={setGroup}
        onSearch={setSearch}
      />
      <HealthSummary data={data} />
      <Card
        role="group"
        aria-label={"服务组与副本"}
        title="服务组与副本"
        extra={
          <Tag color={stale || partial ? "warning" : "processing"}>
            {stale ? "观测过期" : partial ? "部分观测" : "当前观测"}
          </Tag>
        }
      >
        <GroupTable
          groups={data.groups}
          services={data.services}
          onSelect={setSelectedId}
        />
      </Card>
      {data.cluster ? <ClusterPanel cluster={data.cluster} /> : null}
      <HealthPanel title="事件与异常">
        <IssueList issues={issues} />
      </HealthPanel>
      <CollapsibleSection
        id="health-containers"
        title="容器清单"
        summaryLabel="展开容器清单"
        count={containers.length}
      >
        <ContainerTable items={containers} onSelect={setSelectedId} />
      </CollapsibleSection>
    </>
  );
  return (
    <section className="page health-page" style={pageStyle}>
      <header className="page-head">
        <div>
          <h1>{audience === "admin" ? "基础设施" : "系统健康"}</h1>
          <p>服务、中间件与容器的实际运行状态。每 30 秒自动刷新。</p>
        </div>
        <div className="health-refresh">
          <span className="health-note">
            最近观测 {formatDateTime(data.observed_at)}
          </span>
          <Button
            aria-label={"刷新状态"}
            type="primary"
            loading={query.isFetching}
            onClick={() => void query.refetch()}
          >
            刷新状态
          </Button>
        </div>
      </header>
      {query.isError ? (
        <Alert
          type="warning"
          showIcon
          title="刷新失败，保留上次观测"
          description={`${errorMessage(query.error)}；上次观测距现在 ${ageSeconds} 秒。`}
        />
      ) : null}
      {data.source === "docker_unavailable" ||
      data.source === "monitor_unavailable" ||
      data.source === "kubernetes_unavailable" ? (
        <Alert
          type="error"
          showIcon
          title="健康采集不可用"
          description="无法确认当前部署状态，请检查健康采集器。"
        />
      ) : null}
      {partial ? (
        <Alert
          type="warning"
          showIcon
          title="部分观测不可用"
          description="健康采集器只返回了部分节点、Pod 或容器结果；未返回的依赖保持待确认，不会按正常计算。"
        />
      ) : null}
      {stale && !query.isError ? (
        <Alert
          type="warning"
          showIcon
          title="观测已过期"
          description={`上次观测距现在 ${ageSeconds} 秒，已超过刷新周期的健康窗口；当前状态不能视为实时。`}
        />
      ) : null}
      <section className="health-workspace" aria-label="系统健康视图">
        <Tabs
          activeKey={tab}
          onChange={(key) => {
            setTab(key);
            setGroup("all");
          }}
          items={tabsConfig.map((key) => ({
            key,
            label: key,
            children:
              key === overviewKey
                ? overviewTab
                : key === capacityKey
                  ? audience === "admin"
                    ? architectureTab
                    : capacityTab
                  : audience === "admin"
                    ? eventTab
                    : exceptionTab,
          }))}
        />
      </section>
      <Drawer
        title={selected?.name ?? "服务详情"}
        open={selected !== undefined}
        onClose={() => setSelectedId("")}
        size={520}
        mask={false}
        push={false}
        destroyOnHidden
      >
        {selected ? (
          <>
            <HealthTag status={selected.status} />
            <Descriptions
              column={1}
              size="small"
              items={[
                { key: "id", label: "服务标识", children: selected.id },
                {
                  key: "logical",
                  label: "逻辑服务",
                  children: selected.logical_id ?? selected.id,
                },
                {
                  key: "instance",
                  label: "实例 ID",
                  children: selected.instance_id ?? "仅逻辑服务",
                },
                {
                  key: "node",
                  label: "节点",
                  children: selected.node_name ?? "未采集",
                },
                {
                  key: "namespace",
                  label: "命名空间",
                  children: selected.namespace ?? "不适用",
                },
                {
                  key: "workload",
                  label: "工作负载",
                  children: selected.workload ?? "不适用",
                },
                {
                  key: "target",
                  label: "连接目标",
                  children: <code>{selected.target || "未配置"}</code>,
                },
                {
                  key: "observed",
                  label: "本次观测",
                  children: formatDateTime(
                    selected.observed_at ?? data.observed_at,
                  ),
                },
                { key: "detail", label: "状态说明", children: selected.detail },
                {
                  key: "container",
                  label: "容器",
                  children: selected.container?.name ?? "未发现容器实例",
                },
                {
                  key: "image",
                  label: "镜像",
                  children: selected.container?.image ?? "不适用",
                },
                {
                  key: "process",
                  label: "进程状态",
                  children: selected.container?.state ?? "不适用",
                },
                {
                  key: "health",
                  label:
                    data.mode === "kubernetes"
                      ? "Pod 就绪状态"
                      : "Docker 健康检查",
                  children: selected.container?.health || "未配置",
                },
                {
                  key: "probe",
                  label: "探测耗时",
                  children: selected.probe
                    ? `${selected.probe.duration_ms} ms`
                    : "未执行额外探测",
                },
                {
                  key: "dependencies",
                  label: "依赖服务",
                  children: selected.depends_on.length
                    ? selected.depends_on.map((id) => (
                        <Button
                          aria-label={String(
                            data.services.find((item) => item.id === id)
                              ?.name ?? id,
                          )}
                          key={id}
                          type="link"
                          size="small"
                          onClick={() => setSelectedId(id)}
                        >
                          {data.services.find((item) => item.id === id)?.name ??
                            id}
                        </Button>
                      ))
                    : "无声明依赖",
                },
              ]}
            />
          </>
        ) : null}
      </Drawer>
      <p className="health-note">
        容器观测与端点探测分别呈现；未探测的外部模型保持待确认。
      </p>
    </section>
  );
}
