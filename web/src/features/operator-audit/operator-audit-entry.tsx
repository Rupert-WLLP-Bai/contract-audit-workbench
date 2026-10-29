import { Link } from "react-router";
import { useSettings } from "../../api/queries";

/**
 * The entry is rendered only when the server reports the extension enabled.
 * A disabled deployment therefore has no clickable write surface.
 */
export function OperatorAuditEntry(props: {
  caseId: string;
  runId: string;
  attemptId: string | null;
}) {
  const settings = useSettings("client");
  if (settings.data?.operator_audit?.enabled !== true) return null;
  const params = new URLSearchParams({ run_id: props.runId });
  if (props.attemptId) params.set("attempt_id", props.attemptId);
  return (
    <p className="meta">
      <Link to={`/cases/${props.caseId}/operator-audit?${params.toString()}`}>
        进入跨系统核验
      </Link>{" "}
      · 合同约定与授权业务记录的只读对照
    </p>
  );
}
