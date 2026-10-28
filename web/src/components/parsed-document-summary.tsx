import type { DocumentVersion } from "../api/types";
import { formatDateTime, shortDigest } from "../domain/review";

function hash(value: string | null, missing: string): string {
  return value === null ? missing : shortDigest(value);
}

export function ParsedDocumentSummary({
  document,
}: {
  document: DocumentVersion;
}) {
  const parsed = document.parsed_document;
  const legacy =
    document.parse_status === "ready" || document.parse_status === "failed";
  return (
    <div
      className="parsed-document-summary"
      aria-label="解析来源与摘要"
      data-document-version-id={document.id}
      data-original-digest={document.digest}
      data-parsed-original-digest={parsed?.original_digest ?? ""}
      data-parsed-document-id={parsed?.id ?? ""}
      data-parser-id={parsed?.parser_id ?? ""}
      data-parser-version={parsed?.parser_version ?? ""}
      data-config-hash={parsed?.config_hash ?? ""}
      data-normalized-digest={parsed?.normalized_digest ?? ""}
      data-normalization-version={parsed?.normalization_version ?? ""}
      data-ir-digest={parsed?.ir_digest ?? ""}
      data-artifact-digest={parsed?.artifact_digest ?? ""}
      data-parsed-created-at={parsed?.created_at ?? ""}
      data-status={parsed?.status ?? ""}
    >
      <p className="meta">
        原件 SHA-256{" "}
        <span title={document.digest || undefined}>
          {hash(document.digest, "未记录")}
        </span>
        {parsed ? (
          <>
            {"，"}解析 <span title={parsed.id}>{shortDigest(parsed.id)}</span>
            {"，"}解析器 {parsed.parser_id}@{parsed.parser_version}
            {"，"}配置 SHA-256{" "}
            <span title={parsed.config_hash}>
              {shortDigest(parsed.config_hash)}
            </span>
            {"，"}规范化 {parsed.normalization_version}
            {"，"}正文 SHA-256{" "}
            <span title={parsed.normalized_digest ?? undefined}>
              {hash(parsed.normalized_digest, "不可用")}
            </span>
            {"，"}IR SHA-256{" "}
            <span title={parsed.ir_digest ?? undefined}>
              {hash(parsed.ir_digest, "不可用")}
            </span>
            {"，"}产物 SHA-256{" "}
            <span title={parsed.artifact_digest}>
              {shortDigest(parsed.artifact_digest)}
            </span>
            {"，"}
            {parsed.status === "ready" ? "解析就绪" : "解析失败"}
            {parsed.created_at ? `，${formatDateTime(parsed.created_at)}` : ""}
          </>
        ) : (
          <>
            {"，"}解析来源 {legacy ? "未记录（历史记录）" : "尚无解析结果"}
            {"，"}正文摘要 未记录
          </>
        )}
      </p>
    </div>
  );
}
