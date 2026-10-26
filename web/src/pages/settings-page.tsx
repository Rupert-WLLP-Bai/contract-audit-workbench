import {
  ApiOutlined,
  ClockCircleOutlined,
  SafetyCertificateOutlined,
  TeamOutlined,
} from "@ant-design/icons";
import { Button, Table, Tag, type TableProps } from "antd";
import { errorMessage } from "../api/http";
import { useSession, useSettings } from "../api/queries";
import { PageStatus } from "../components/page-status";
import { KpiStat, PageHeader, SectionCard } from "../components/ui";
import {
  actorRoleLabel,
  formatWait,
  modelStatusLabel,
} from "../domain/review";
import "./settings-page.css";

function cardTitle(text: string) {
  return <h2 className="settings-card-title">{text}</h2>;
}

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
  const modelConnected = data.model.status === "connected";
  const userColumns: TableProps<(typeof data.users)[number]>["columns"] = [
    { title: "姓名", dataIndex: "display_name", key: "display_name" },
    {
      title: "角色",
      key: "role",
      render: (_, user) => (
        <Tag color={user.role === "admin" ? "blue" : "default"}>
          {actorRoleLabel(user.role)}
        </Tag>
      ),
    },
  ];

  return (
    <section className="settings-page">
      <PageHeader
        title="设置"
        description="只显示连通、队列、脱敏政策和角色。密钥留在服务端，这个页面不提供输入框。"
      />

      <div className="settings-kpis" aria-label="设置概览">
        <KpiStat
          label="模型连通"
          value={modelStatusLabel(data.model.status)}
          hint="来自服务端当前配置"
          icon={<ApiOutlined />}
          tone={modelConnected ? "low" : "medium"}
        />
        <KpiStat
          label="等待任务"
          value={data.queue.depth === null ? "未报告" : data.queue.depth}
          hint="服务端返回的当前队列深度"
          icon={<ClockCircleOutlined />}
          tone="primary"
        />
        <KpiStat
          label="最老任务等待"
          value={formatWait(data.queue.oldest_wait_ms)}
          hint="从入队到当前的等待时长"
          icon={<SafetyCertificateOutlined />}
          tone="primary"
        />
      </div>

      <div className="settings-grid">
        <div className="settings-column">
          <SectionCard title={cardTitle("模型与脱敏")}>
            <dl className="settings-fields">
              <div>
                <dt>模型连通</dt>
                <dd>
                  <Tag color={modelConnected ? "success" : "warning"}>
                    {modelStatusLabel(data.model.status)}
                  </Tag>
                  <span className="settings-field-note">{data.model.label}</span>
                </dd>
              </div>
              <div>
                <dt>脱敏政策</dt>
                <dd>
                  {data.redaction_policy === ""
                    ? "未提供脱敏政策说明。"
                    : data.redaction_policy}
                </dd>
              </div>
            </dl>
            <p className="settings-note">
              模型密钥和连接凭据只保留在服务端，页面不会读取或回显。
            </p>
          </SectionCard>

          {session.data?.role === "admin" ? (
            <SectionCard
              title={cardTitle("执行运维统计")}
              ariaLabel="执行运维统计"
            >
              <p className="settings-note">
                仅展示授权的汇总元数据，不包含案件正文或执行事件。
              </p>
              {data.execution ? (
                <dl className="settings-stats">
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
                <p className="settings-note">执行运维统计未报告。</p>
              )}
            </SectionCard>
          ) : null}

          <SectionCard
            title={cardTitle("用户与角色")}
            extra={<span className="settings-count">{data.users.length} 个账号</span>}
          >
            <p className="settings-note">
              首版只读展示。账号的创建和停用不在这个页面里完成。
            </p>
            {data.users.length === 0 ? (
              <p className="settings-note">没有返回用户。</p>
            ) : (
              <Table
                className="settings-users"
                columns={userColumns}
                dataSource={data.users}
                rowKey="id"
                pagination={false}
                size="small"
              />
            )}
          </SectionCard>
        </div>

        <aside className="settings-column">
          <SectionCard title={cardTitle("队列与等待")}>
            <dl className="settings-fields">
              <div>
                <dt>等待任务</dt>
                <dd>
                  {data.queue.depth === null ? "未报告" : data.queue.depth}
                </dd>
              </div>
              <div>
                <dt>最老任务等待</dt>
                <dd>{formatWait(data.queue.oldest_wait_ms)}</dd>
              </div>
            </dl>
            <p className="settings-note">
              队列深度与等待时长由服务端按当前授权范围返回；未返回时不做推断。
            </p>
          </SectionCard>

          <SectionCard title={cardTitle("只读边界")}>
            <ul className="settings-boundaries">
              <li>
                <TeamOutlined aria-hidden="true" />
                <span>账号与角色由组织统一分配，本页只读展示。</span>
              </li>
              <li>
                <SafetyCertificateOutlined aria-hidden="true" />
                <span>模型输出只作审查建议，不在这里形成人工结论。</span>
              </li>
              <li>
                <ClockCircleOutlined aria-hidden="true" />
                <span>队列、执行用量和连通状态均来自服务端当前快照。</span>
              </li>
            </ul>
            {session.data?.role ? (
              <p className="settings-session-role">
                当前工作区角色：{actorRoleLabel(session.data.role)}
              </p>
            ) : null}
          </SectionCard>
        </aside>
      </div>
    </section>
  );
}
