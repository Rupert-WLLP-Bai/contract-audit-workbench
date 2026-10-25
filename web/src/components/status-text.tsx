import type { ReactNode } from "react";
import type { StatusTone } from "../domain/review";
import { Tag } from "antd";

export function StatusText({
  tone,
  children,
}: {
  tone: StatusTone;
  children: ReactNode;
}) {
  const color =
    tone === "good"
      ? "success"
      : tone === "bad"
        ? "error"
        : tone === "attention"
          ? "warning"
          : tone === "progress"
            ? "processing"
            : "default";
  return <Tag color={color}>{children}</Tag>;
}
