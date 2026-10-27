import { Collapse } from "antd";
import type { AuditEvent } from "../api/types";

interface AuditProjectionProps {
  runId: string;
  events: AuditEvent[];
}

export function AuditProjection({ runId, events }: AuditProjectionProps) {
  return (
    <section className="pane" aria-label="业务审计记录">
      <h2>业务审计</h2>
      <p>当前运行的追加记录，包含历史尝试与人工操作；顺序以服务端序号为准。</p>
      <Collapse
        items={[
          {
            key: "content",
            label: "查看审计记录（" + String(events.length ?? "") + "）",
            children: (
              <>
                <p className="digest">运行：{runId}</p>
                {events.length === 0 ? (
                  <p>当前运行尚未返回审计记录。</p>
                ) : (
                  <ol className="workflow-record-list">
                    {events.map((event) => (
                      <li key={event.event_id}>
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
                                      <dt>事件身份</dt>
                                      <dd>{event.event_id}</dd>
                                    </div>
                                    <div>
                                      <dt>运行身份</dt>
                                      <dd>{event.run_id}</dd>
                                    </div>
                                    <div>
                                      <dt>记录摘要</dt>
                                      <dd>
                                        <code>{event.hash}</code>
                                      </dd>
                                    </div>
                                    <div>
                                      <dt>前序摘要</dt>
                                      <dd>
                                        <code>
                                          {event.prev_hash || "起始记录"}
                                        </code>
                                      </dd>
                                    </div>
                                    <div>
                                      <dt>关联身份</dt>
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
