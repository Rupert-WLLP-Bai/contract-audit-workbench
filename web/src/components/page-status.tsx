import type { ReactNode } from "react";
import { Empty, Result, Spin } from "antd";

export function PageStatus({
  kind,
  title,
  detail,
  action,
}: {
  kind: "loading" | "empty" | "error" | "denied";
  title: string;
  detail?: string;
  action?: ReactNode;
}) {
  if (kind === "loading")
    return (
      <section className="page-status page-status-loading" role="status">
        <Spin />
        <h1>{title}</h1>
      </section>
    );
  if (kind === "empty")
    return (
      <section className="page-status page-status-empty" role="status">
        <Empty
          description={
            <>
              <h2>{title}</h2>
              {detail ? <div>{detail}</div> : null}
            </>
          }
        >
          {action}
        </Empty>
      </section>
    );
  return (
    <section role="alert">
      <Result
        className={`page-status page-status-${kind}`}
        status={kind === "denied" ? "403" : "error"}
        title={<h1>{title}</h1>}
        subTitle={detail}
        extra={action}
      />
    </section>
  );
}
