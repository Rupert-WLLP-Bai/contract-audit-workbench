import { Card } from "antd";
import type { ReactNode } from "react";

export function SectionCard({
  title,
  extra,
  children,
  className,
}: {
  title?: ReactNode;
  extra?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <Card
      className={["caw-section-card", className].filter(Boolean).join(" ")}
      title={title}
      extra={extra}
      variant="outlined"
    >
      {children}
    </Card>
  );
}
