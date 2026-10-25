import { errorMessage } from "../api/http";
import { Button, Table, type TableProps } from "antd";
import { useSession, useSettings } from "../api/queries";
import { PageStatus } from "../components/page-status";
import { actorRoleLabel, formatWait, modelStatusLabel } from "../domain/review";

export function SettingsPage({
  audience = "admin",
}: {
  audience?: "client" | "admin";
}) {
  const settings = useSettings(audience);
  const session = useSession(audience);
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
  const userColumns: TableProps<(typeof data.users)[number]>["columns"] = [
    { title: "姓名", dataIndex: "display_name", key: "display_name" },
    {
      title: "角色",
      key: "role",
      render: (_, user) => actorRoleLabel(user.role),
    },
  ];
  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>设置</h1>
          <p>
            只显示连通、队列、脱敏政策和角色。密钥留在服务端，这个页面不提供输入框。
          </p>
        </div>
      </header>
      <section>
        <h2>模型连通</h2>
        <p>
          {modelStatusLabel(data.model.status)} · {data.model.label}
        </p>
      </section>
      <section>
        <h2>队列</h2>
        <p>
          等待任务 {data.queue.depth === null ? "未报告" : data.queue.depth}
        </p>
        <p>最老任务等待 {formatWait(data.queue.oldest_wait_ms)}</p>
      </section>
      <section>
        <h2>脱敏政策</h2>
        <p>
          {data.redaction_policy === ""
            ? "未提供脱敏政策说明。"
            : data.redaction_policy}
        </p>
      </section>
      {session.data?.role === "admin" ? (
        <section aria-labelledby="execution-operations-title">
          <h2 id="execution-operations-title">执行运维统计</h2>
          <p className="meta">
            仅展示授权的汇总元数据，不包含案件正文或执行事件。
          </p>
          {data.execution ? (
            <dl className="execution-dl">
              <div>
                <dt>全部尝试</dt>
                <dd>{data.execution.total_attempts}</dd>
              </div>
              <div>
                <dt>运行中</dt>
                <dd>{data.execution.running_attempts}</dd>
              </div>
              <div>
                <dt>已完成</dt>
                <dd>{data.execution.completed_attempts}</dd>
              </div>
              <div>
                <dt>部分完成</dt>
                <dd>{data.execution.partial_attempts}</dd>
              </div>
              <div>
                <dt>失败</dt>
                <dd>{data.execution.failed_attempts}</dd>
              </div>
              <div>
                <dt>已取消</dt>
                <dd>{data.execution.cancelled_attempts}</dd>
              </div>
              <div>
                <dt>模型调用</dt>
                <dd>{data.execution.model_calls}</dd>
              </div>
              <div>
                <dt>用量未知的调用</dt>
                <dd>{data.execution.unknown_usage_calls}</dd>
              </div>
              <div>
                <dt>队列深度</dt>
                <dd>
                  {data.execution.queue_depth === null
                    ? "未报告"
                    : data.execution.queue_depth}
                </dd>
              </div>
            </dl>
          ) : (
            <p>执行运维统计未报告。</p>
          )}
        </section>
      ) : null}
      <section>
        <h2>用户与角色</h2>
        <p className="meta">
          首版只读展示。账号的创建和停用不在这个页面里完成。
        </p>
        {data.users.length === 0 ? <p>没有返回用户。</p> : null}
        <Table
          className="data-table"
          columns={userColumns}
          dataSource={data.users}
          rowKey="id"
          pagination={false}
        />
      </section>
    </section>
  );
}
