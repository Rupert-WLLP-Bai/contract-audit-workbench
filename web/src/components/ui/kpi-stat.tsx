import { Skeleton } from "antd";
import type { ReactNode } from "react";

export type KpiTone = "primary" | "high" | "medium" | "low";

export function KpiStat({
  label,
  value,
  hint,
  icon,
  tone = "primary",
  loading = false,
}: {
  label: ReactNode;
  value: ReactNode;
  hint?: ReactNode;
  icon?: ReactNode;
  tone?: KpiTone;
  loading?: boolean;
}) {
  return (
    <article className="caw-kpi" aria-label={typeof label === "string" ? label : undefined}>
      {icon ? (
        <span
          className={`caw-kpi__icon caw-kpi__icon--${tone}`}
          aria-hidden="true"
        >
          {icon}
        </span>
      ) : null}
      <div className="caw-kpi__body">
        <span className="caw-kpi__label">{label}</span>
        {loading ? (
          <Skeleton.Input active size="small" style={{ width: 96 }} />
        ) : (
          <span className="caw-kpi__value">{value}</span>
        )}
        {hint ? <span className="caw-kpi__hint">{hint}</span> : null}
      </div>
    </article>
  );
}
