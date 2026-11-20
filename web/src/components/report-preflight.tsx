import { Button, Modal } from "antd";
import { useEffect, useRef, useState, type ReactNode } from "react";
import {
  assertCommandContextCurrent,
  captureCommandContext,
  type CommandContext,
} from "../api/http";
import type { partialReportScope } from "../features/review/review-outcome";

type Confirmation = { scope: string; context: CommandContext };

export function useReportPreflight(scope: string) {
  const [ticket, setTicket] = useState<Confirmation | null>(null);
  const pending = useRef<Confirmation | null>(null);
  useEffect(() => {
    if (pending.current && pending.current.scope !== scope) {
      pending.current = null;
      setTicket(null);
    }
  }, [scope]);

  function dismiss() {
    pending.current = null;
    setTicket(null);
  }

  return {
    open: ticket !== null && ticket.scope === scope,
    show() {
      try {
        const confirmation = { scope, context: captureCommandContext() };
        pending.current = confirmation;
        setTicket(confirmation);
      } catch {
        dismiss();
      }
    },
    dismiss,
    confirm(submit: () => void) {
      const confirmation = pending.current;
      dismiss();
      if (confirmation?.scope !== scope) return;
      try {
        assertCommandContextCurrent(confirmation.context);
      } catch {
        return;
      }
      submit();
    },
  };
}

export function ReportPreflight({
  summary,
  open,
  onConfirm,
  onCancel,
  recovery,
}: {
  summary: NonNullable<ReturnType<typeof partialReportScope>>;
  open: boolean;
  onConfirm: () => void;
  onCancel: () => void;
  recovery?: ReactNode;
}) {
  return (
    <Modal
      title={summary.precheck ? "单合同预审报告预检" : "部分报告预检"}
      open={open}
      destroyOnHidden
      onCancel={onCancel}
      footer={
        <div
          style={{
            display: "flex",
            flexWrap: "wrap",
            justifyContent: "flex-end",
            alignItems: "center",
            gap: "var(--space-2)",
          }}
        >
          <Button onClick={onCancel}>取消</Button>
          <Button onClick={onConfirm}>仍出部分报告</Button>
          {recovery}
        </div>
      }
    >
      <p>{summary.precheck ? "本次仅做单合同预审" : summary.executionLabel}</p>
      <ul>
        <li>{summary.ruleCoverage}</li>
        <li>
          已判断 {summary.decidedCount} / {summary.findingCount} 条线索
        </li>
        <li>未覆盖：{summary.uncovered.join("、")}</li>
      </ul>
      <p>报告将标注审查范围和完成状态。</p>
    </Modal>
  );
}
