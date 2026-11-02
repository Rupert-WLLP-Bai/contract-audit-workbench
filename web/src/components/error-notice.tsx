// File metadata (manifest mirror):
// Purpose: Renders ErrorNotice, the error notice workbench UI, from the selected case/workspace projection and the module’s query/state inputs; it displays server-derived state and does not establish business authorization or evidence validity.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-30
// Updated: 2026-10-30
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

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
