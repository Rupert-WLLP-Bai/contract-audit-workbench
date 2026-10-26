import { Card } from "antd";
import type { ReactNode } from "react";

export function SectionCard({
  title,
  extra,
  children,
  className,
  ariaLabel,
}: {
  title?: ReactNode;
  extra?: ReactNode;
  children: ReactNode;
  className?: string;
  ariaLabel?: string;
}) {
  return (
    <Card
      className={["caw-section-card", className].filter(Boolean).join(" ")}
      title={title}
      extra={extra}
      variant="outlined"
      role={ariaLabel ? "region" : undefined}
      aria-label={ariaLabel}
    >
      {children}
    </Card>
  );
}
