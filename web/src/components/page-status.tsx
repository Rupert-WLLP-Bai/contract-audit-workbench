import type { ReactNode } from "react";

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
  return (
    <section className={`page-status page-status-${kind}`} role={kind === "error" || kind === "denied" ? "alert" : "status"}>
      <h1>{title}</h1>
      {detail ? <p>{detail}</p> : null}
      {action}
    </section>
  );
}
