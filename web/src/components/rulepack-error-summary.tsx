import { useEffect, useRef } from "react";
import type { RulePackFormIssue } from "../domain/rulepack-form";

export function RulePackErrorSummary({ issues }: { issues: RulePackFormIssue[] }) {
  const summary = useRef<HTMLElement>(null);
  useEffect(() => {
    if (issues.length > 0) summary.current?.focus();
  }, [issues]);
  if (issues.length === 0) return null;
  return (
    <section className="workflow-error rulepack-error-summary" role="alert" aria-labelledby="rulepack-errors-title" tabIndex={-1} ref={summary}>
      <h2 id="rulepack-errors-title">请修正以下字段</h2>
      <ul>
        {issues.map((issue, index) => (
          <li key={`${issue.fieldId}:${index}`}>
            <a href={`#${issue.fieldId}`} onClick={(event) => {
              event.preventDefault();
              document.getElementById(issue.fieldId)?.focus();
            }}>{issue.message}</a>
          </li>
        ))}
      </ul>
    </section>
  );
}
