import { ApiError, errorMessage, statusLabel } from "../api/http";

/** Mutation error surface shared by the workbench panels. */
export function ErrorNotice({ error }: { error: unknown }) {
  if (!error) return null;
  return (
    <div className="workflow-error" role="alert">
      <p>{errorMessage(error)}</p>
      {error instanceof ApiError ? (
        <p>
          服务响应：{statusLabel(error.status)} · {error.code}
          {error.retryable ? " · 可重试" : ""}
        </p>
      ) : null}
      {error instanceof ApiError && error.details !== null ? (
        <details>
          <summary>服务错误详情</summary>
          <pre>{JSON.stringify(error.details, null, 2)}</pre>
        </details>
      ) : null}
    </div>
  );
}
