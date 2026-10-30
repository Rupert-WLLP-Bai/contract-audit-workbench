import { useState } from "react";
import { errorMessage } from "../api/http";
import { Button, Statistic, Table, type TableProps } from "antd";
import { useLLMEndpoint, useSession, useSettings } from "../api/queries";
import { PageStatus } from "../components/page-status";
import {
  LLMEndpointPanel,
  LLMModelSelection,
} from "../components/llm-endpoint";
import { actorRoleLabel, formatWait, modelStatusLabel } from "../domain/review";

type ModelFact = {
  term: string;
  detail: string;
  tone?: "attention" | "danger";
  mono?: boolean;
};

// The service reports the model summary as one readable sentence. Split it into
// labelled rows so the panel does not present a comma-joined paragraph. Unknown
// shapes stay on one line instead of hiding information.
function modelFacts(label: string): ModelFact[] {
  const parts = label
    .split(/[，。]/)
    .map((part) => part.trim())
    .filter(Boolean);
  if (parts.length === 0) return [{ term: "", detail: "未报告" }];
  return parts.map((part) => {
    const cut = part.indexOf(" ");
    const head = cut > 0 ? part.slice(0, cut) : "";
    const pair = head !== "" && head.length <= 8 && !/[：:]/.test(head);
    const detail = pair ? part.slice(cut + 1).trim() : part;
    return {
      term: pair ? head : "",
      detail,
      tone: /不可达|没有配置的模型/.test(detail)
        ? "danger"
        : /未配置/.test(detail)
          ? "attention"
          : undefined,
      mono: /^https?:\/\//.test(detail),
    };
  });
}

export function SettingsPage({
  audience = "admin",
}: {
  audience?: "client" | "admin";
}) {
  const settings = useSettings(audience);
  const session = useSession(audience);
  const [endpointOpen, setEndpointOpen] = useState(false);
  const isAdmin = audience === "admin" && session.data?.role === "admin";
  const endpoint = useLLMEndpoint(isAdmin);
  if (settings.isLoading)
    return <PageStatus kind="loading" title="正在加载设置" />;
  if (settings.isError) {
    return (
      <PageStatus
        kind="error"
        title="设置没有加载"
        detail={errorMessage(settings.error)}
        action={
          <Button
            aria-label={"重试"}
            type="primary"
            onClick={() => void settings.refetch()}
          >
            重试
          </Button>
        }
      />
    );
  }
  if (!settings.data) return <PageStatus kind="empty" title="没有设置数据" />;
  const data = settings.data;
  const queueDepth =
    data.queue.depth === null ? "未报告" : String(data.queue.depth);
  const oldestWait = formatWait(data.queue.oldest_wait_ms);
  const modelConnected =
    data.model.status === "connected" || data.model.status === "configured";
  const executionFigures = data.execution
    ? [
        { label: "全部执行记录", value: data.execution.total_attempts, tone: "neutral" },
        { label: "运行中", value: data.execution.running_attempts, tone: "progress" },
        { label: "已完成", value: data.execution.completed_attempts, tone: "compatible" },
        { label: "部分完成", value: data.execution.partial_attempts, tone: "insufficient" },
        { label: "失败", value: data.execution.failed_attempts, tone: "conflict" },
        { label: "已取消", value: data.execution.cancelled_attempts, tone: "neutral" },
        { label: "模型调用", value: data.execution.model_calls, tone: "progress" },
        {
          label: "用量未知的调用",
          value: data.execution.unknown_usage_calls,
          tone: "insufficient",
        },
        {
          label: "队列深度",
          value:
            data.execution.queue_depth === null
              ? "未报告"
              : data.execution.queue_depth,
          tone:
            data.execution.queue_depth !== null &&
            data.execution.queue_depth > 0
              ? "insufficient"
              : "neutral",
        },
      ]
    : [];
  const userColumns: TableProps<(typeof data.users)[number]>["columns"] = [
    { title: "姓名", dataIndex: "display_name", key: "display_name" },
    {
      title: "角色",
      key: "role",
      width: 160,
      render: (_, user) => actorRoleLabel(user.role),
    },
  ];
  return (
    <section className="page settings-page">
      <header className="page-head">
        <div>
          <h1>设置</h1>
          <p>
            查看连通、队列、脱敏政策和角色。模型端点由管理员配置，密钥只在保存时提交并留在服务端。
          </p>
        </div>
      </header>
      <section className="settings-overview" aria-label="设置概览">
        <Statistic
          title="模型连通"
          value={modelStatusLabel(data.model.status)}
        />
        <Statistic title="等待任务" value={queueDepth} />
        <Statistic title="最长等待" value={oldestWait} />
        <Statistic title="用户" value={data.users.length} />
      </section>
      <div className="settings-grid">
        <section
          className="settings-panel"
          aria-labelledby="settings-model-title"
        >
          <header className="settings-panel-head">
            <h2 id="settings-model-title">模型连通</h2>
            <span
              className="settings-badge"
              data-tone={modelConnected ? "good" : "attention"}
            >
              {modelStatusLabel(data.model.status)}
            </span>
          </header>
          <dl className="settings-facts">
            {modelFacts(data.model.label).map((fact, index) => (
              <div key={`${fact.detail}-${index}`} data-tone={fact.tone}>
                {fact.term ? <dt>{fact.term}</dt> : null}
                <dd className={fact.mono ? "is-code" : undefined}>
                  {fact.detail}
                </dd>
              </div>
            ))}
          </dl>
          {isAdmin ? (
            <LLMModelSelection
              endpoint={endpoint.data}
              loading={endpoint.isLoading}
              onConfigure={() => setEndpointOpen(true)}
            />
          ) : null}
        </section>
        <section
          className="settings-panel"
          aria-labelledby="settings-queue-title"
        >
          <h2 id="settings-queue-title">队列</h2>
          <dl className="settings-pairs">
            <div>
              <dt>等待任务</dt>
              <dd>{queueDepth}</dd>
            </div>
            <div>
              <dt>最长等待</dt>
              <dd>{oldestWait}</dd>
            </div>
          </dl>
        </section>
      </div>
      {isAdmin ? (
        <LLMEndpointPanel
          endpoint={endpoint.data}
          loading={endpoint.isLoading}
          error={endpoint.isError ? endpoint.error : null}
          onRetry={() => void endpoint.refetch()}
          open={endpointOpen}
          onOpenChange={setEndpointOpen}
        />
      ) : null}
      <section
        className="settings-panel"
        aria-labelledby="settings-redaction-title"
      >
        <h2 id="settings-redaction-title">脱敏政策</h2>
        <p className="settings-policy">
          {data.redaction_policy === ""
            ? "未提供脱敏政策说明。"
            : data.redaction_policy}
        </p>
      </section>
      {session.data?.role === "admin" ? (
        <section
          className="settings-panel"
          aria-labelledby="execution-operations-title"
        >
          <h2 id="execution-operations-title">执行运维统计</h2>
          <p className="meta">
            仅展示授权的汇总元数据，不包含案件正文或执行事件。
          </p>
          {executionFigures.length > 0 ? (
            <dl className="settings-figures">
              {executionFigures.map((item) => (
                <div key={item.label} data-tone={item.tone}>
                  <dt>{item.label}</dt>
                  <dd>{item.value}</dd>
                </div>
              ))}
            </dl>
          ) : (
            <p>没有获取到执行运维统计。</p>
          )}
        </section>
      ) : null}
      <section className="settings-panel" aria-labelledby="settings-users-title">
        <h2 id="settings-users-title">用户与角色</h2>
        <p className="meta">
          首版只读展示。账号的创建和停用不在这个页面里完成。
        </p>
        {data.users.length === 0 ? <p>没有返回用户。</p> : null}
        <Table
          className="data-table"
          columns={userColumns}
          dataSource={data.users}
          rowKey="id"
          size="small"
          tableLayout="fixed"
          pagination={false}
        />
      </section>
    </section>
  );
}
