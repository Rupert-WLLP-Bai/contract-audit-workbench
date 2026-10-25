import type { ReactNode } from "react";
import type { StatusTone } from "../domain/review";

export function StatusText({ tone, children }: { tone: StatusTone; children: ReactNode }) {
  return <span className={`status status-${tone}`}>{children}</span>;
}
