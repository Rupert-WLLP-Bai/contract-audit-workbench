import { useState, type CSSProperties } from "react";
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
  type HealthService,
  type HealthStatus,
  type ProductionComponent,
} from "../api/system-health";
import type { SessionAudience } from "../api/types";
import { errorMessage } from "../api/http";
import { PageStatus } from "../components/page-status";
import { formatDateTime } from "../domain/review";
import "./system-health-page.css";

const STATUS: Record<HealthStatus, { label: string; color: string }> = {
  ready: { label: "正常", color: "success" },
  unavailable: { label: "不可用", color: "error" },
  stopped: { label: "已停止", color: "error" },
  starting: { label: "启动中", color: "processing" },
  unknown: { label: "待确认", color: "warning" },
  unconfigured: { label: "未启用", color: "default" },
  completed: { label: "已完成", color: "success" },
};
const GROUPS: Record<string, string> = {
  frontend: "前端站点",
  application: "应用服务",
  middleware: "基础中间件",
  identity: "身份服务",
  observability: "可观测服务",
  external: "模型与解析",
};
const TABS = ["服务概览", "容器清单", "依赖拓扑", "当前异常"] as const;

function HealthTag({ status }: { status: HealthStatus }) {
  return <Tag color={STATUS[status].color}>{STATUS[status].label}</Tag>;
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
                  key: `service:${item.id}`,
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
        description="单实例容器健康不代表满足生产副本、纠删码、mTLS、伸缩或容灾要求。没有集群观测和专项验证的能力保持未接入或未验收。"
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

export function SystemHealthPage({
  audience = "client",
}: {
  audience?: SessionAudience;
}) {
  const query = useSystemHealth(audience);
  const [tab, setTab] = useState<string>(
    audience === "admin" ? "生产架构" : TABS[0],
  );
  const [search, setSearch] = useState("");
  const [group, setGroup] = useState("all");
  const [selectedId, setSelectedId] = useState("");
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
  const attentionIds = new Set(
    data.services
      .filter(
        (item) =>
          item.status === "unavailable" ||
          item.status === "stopped" ||
          item.status === "unknown" ||
          item.status === "starting",
      )
      .map((item) => item.id),
  );
  const needle = search.trim().toLocaleLowerCase();
  const visible = data.services.filter(
    (item) =>
      (group === "all" || item.category === group) &&
      `${item.name} ${item.id} ${item.target} ${item.container?.name ?? ""} ${item.container?.image ?? ""}`
        .toLocaleLowerCase()
        .includes(needle) &&
      (tab !== "当前异常" || attentionIds.has(item.id)) &&
      (tab !== "容器清单" || item.container !== null),
  );
  const architecture = data.architecture.filter(
    (item) =>
      (group === "all" || item.category === group) &&
      `${item.name} ${item.target} ${item.observed}`
        .toLocaleLowerCase()
        .includes(needle),
  );
  const tabs = audience === "admin" ? ["生产架构", ...TABS] : TABS;
  const groups = tab === "生产架构" ? ARCHITECTURE_GROUPS : GROUPS;
  const selected = data.services.find((item) => item.id === selectedId);
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
          onClick={() => setSelectedId(item.id)}
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
    <section className="page health-page" style={pageStyle}>
      <header className="page-head">
        <div>
          <h1>{audience === "admin" ? "基础设施与中间件" : "系统健康"}</h1>
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
          description={errorMessage(query.error)}
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
      <ol className="health-summary" aria-label="系统健康统计">
        {[
          {
            title: "服务与集成",
            value: data.services.length,
            state: data.services.length > 0 ? "filled" : "empty",
          },
          {
            title: "实际容器",
            value: data.services.filter((item) => item.container !== null)
              .length,
            state: data.services.some((item) => item.container !== null)
              ? "filled"
              : "empty",
          },
          {
            title: "已确认正常",
            value: data.services.filter((item) => item.status === "ready")
              .length,
            state: data.services.some((item) => item.status === "ready")
              ? "filled"
              : "open",
          },
          {
            title: "需关注",
            value: attentionIds.size,
            state: attentionIds.size > 0 ? "open" : "filled",
          },
          {
            title: "未启用",
            value: data.services.filter(
              (item) => item.status === "unconfigured",
            ).length,
            state: "empty",
          },
        ].map((item) => (
          <li key={item.title} data-state={item.state}>
            <span className="health-summary-label">{item.title}</span>
            <strong>{item.value}</strong>
          </li>
        ))}
      </ol>
      {data.cluster ? (
        <Card
          role="group"
          aria-label={"Kubernetes 节点"}
          title="Kubernetes 节点"
          extra={
            <Tag
              color={data.cluster.status === "ready" ? "processing" : "error"}
            >
              {data.cluster.namespace}
            </Tag>
          }
        >
          <p>{data.cluster.detail}</p>
          <Table
            rowKey="name"
            size="small"
            pagination={false}
            dataSource={data.cluster.nodes}
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
      ) : null}
      <section className="health-workspace">
        <Tabs
          activeKey={tab}
          onChange={(key) => {
            setTab(key);
            setGroup("all");
          }}
          items={tabs.map((key) => ({ key, label: key }))}
        />
        <div className="health-toolbar">
          <span className="health-note">
            {tab === "生产架构" ? architecture.length : visible.length} 项，{" "}
            {data.mode === "kubernetes"
              ? "Kubernetes 部署"
              : data.mode === "distributed"
                ? "独立服务部署"
                : "单进程部署"}
          </span>
          <label>
            <span>服务分组</span>
            <Select
              aria-label="服务分组"
              value={group}
              onChange={setGroup}
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
              onChange={(event) => setSearch(event.target.value)}
              placeholder="服务、容器或镜像"
            />
          </label>
        </div>
        {tab === "生产架构" ? (
          <ProductionArchitecture
            components={architecture}
            services={data.services}
            onService={setSelectedId}
          />
        ) : tab === "依赖拓扑" ? (
          <Topology services={visible} onSelect={setSelectedId} />
        ) : tab === "容器清单" ? (
          <Table<HealthService>
            rowKey="id"
            columns={columns}
            dataSource={visible}
            size="small"
            scroll={{ x: 1020 }}
            pagination={{
              defaultPageSize: 20,
              showSizeChanger: true,
              pageSizeOptions: [10, 20, 50],
              showTotal: (total) => `共 ${total} 个容器`,
            }}
            locale={{ emptyText: "当前筛选范围没有容器" }}
          />
        ) : visible.length === 0 ? (
          <Empty
            description={
              tab === "当前异常"
                ? "当前筛选范围没有需关注的服务"
                : "没有匹配的服务"
            }
          />
        ) : (
          Object.entries(GROUPS).map(([id, name]) => {
            const items = visible.filter((item) => item.category === id);
            return items.length ? (
              <section className="health-group" key={id} aria-label={name}>
                <h2>
                  {name}
                  <span>{items.length}</span>
                </h2>
                <div className="health-card-grid">
                  {items.map((item) => (
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
                          onClick={() => setSelectedId(item.id)}
                        >
                          详情
                        </Button>
                      </div>
                    </Card>
                  ))}
                </div>
              </section>
            ) : null;
          })
        )}
      </section>
      <Drawer
        title={selected?.name ?? "服务详情"}
        open={selected !== undefined}
        onClose={() => setSelectedId("")}
        size={520}
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
                  key: "target",
                  label: "连接目标",
                  children: <code>{selected.target || "未配置"}</code>,
                },
                {
                  key: "observed",
                  label: "本次观测",
                  children: formatDateTime(data.observed_at),
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
