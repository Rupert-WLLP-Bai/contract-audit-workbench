import { Collapse } from "antd";
import type { AuditEvent, BusinessAuditEvent } from "../api/types";

const EVENT_LABELS: Record<string, string> = {
  review_started: "审查已发起",
  review_retried: "已重新执行审查",
  review_cancelled: "审查已取消",
  attempt_input_frozen: "已保存本次审查材料",
  human_decision: "已记录审查判断",
};

interface AuditProjectionProps {
  runId: string;
  events: (AuditEvent | BusinessAuditEvent)[];
  showDiagnostics?: boolean;
}

export function AuditProjection({ runId, events, showDiagnostics = false }: AuditProjectionProps) {
  return (
    <section className="pane" aria-label="业务审计记录">
      <h2>业务审计</h2>
      <p>这里记录本次审查的各次执行和人工操作，按系统保存的顺序显示。</p>
      <Collapse
        items={[
          {
            key: "content",
            label: "查看审计记录（" + String(events.length ?? "") + "）",
            children: (
              <>
                {showDiagnostics ? <p className="digest">运行：{runId}</p> : null}
                {events.length === 0 ? (
                  <p>尚未获取本次审查的审计记录。</p>
                ) : (
                  <ol className="workflow-record-list">
                    {events.map((event) => (
                      <li key={event.event_id}>
                        {showDiagnostics ? (
                        <Collapse
                          items={[
                            {
                              key: "content",
                              label: (
                                <>
                                  序号 {event.seq}，{event.event_type}，{" "}
                                  {event.actor}
                                </>
                              ),
                              children: (
                                <>
                                  <dl className="execution-dl">
                                    <div>
                                      <dt>事件编号</dt>
                                      <dd>{event.event_id}</dd>
                                    </div>
                                    <div>
                                      <dt>审查编号</dt>
                                      <dd>{event.run_id}</dd>
                                    </div>
                                    {"hash" in event ? (
                                      <>
                                        <div>
                                          <dt>记录摘要</dt>
                                          <dd><code>{event.hash}</code></dd>
                                        </div>
                                        <div>
                                          <dt>前序摘要</dt>
                                          <dd><code>{event.prev_hash || "起始记录"}</code></dd>
                                        </div>
                                      </>
                                    ) : null}
                                    <div>
                                      <dt>关联编号</dt>
                                      <dd>
                                        {event.correlation_ids.length === 0
                                          ? "未提供"
                                          : event.correlation_ids.join("、")}
                                      </dd>
                                    </div>
                                  </dl>
                                </>
                              ),
                            },
                          ]}
                        />
                        ) : (
                          <p>记录 {event.seq}，{EVENT_LABELS[event.event_type] ?? "其他审查记录"}</p>
                        )}
                      </li>
                    ))}
                  </ol>
                )}
              </>
            ),
          },
        ]}
      />
    </section>
  );
}
