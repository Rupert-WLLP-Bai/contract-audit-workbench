import type { DocumentVersion, ParsedDocumentMetadata } from "../api/types";
import { formatDateTime, shortDigest } from "../domain/review";

function hash(value: string | null, missing: string): string {
  return value === null ? missing : shortDigest(value);
}

function attribute(value: string | null | undefined): string {
  return value === null || value === undefined ? "" : value;
}

function titleOf(value: string | null): string | undefined {
  return value === null ? undefined : value;
}

function parsedSummaryAttributes(
  parsed: ParsedDocumentMetadata,
): Record<string, string> {
  return {
    "data-parsed-original-digest": attribute(parsed.original_digest),
    "data-parsed-document-id": attribute(parsed.id),
    "data-parser-id": attribute(parsed.parser_id),
    "data-parser-version": attribute(parsed.parser_version),
    "data-config-hash": attribute(parsed.config_hash),
    "data-normalized-digest": attribute(parsed.normalized_digest),
    "data-normalization-version": attribute(parsed.normalization_version),
    "data-ir-digest": attribute(parsed.ir_digest),
    "data-artifact-digest": attribute(parsed.artifact_digest),
    "data-parsed-created-at": attribute(parsed.created_at),
    "data-status": attribute(parsed.status),
  };
}

const EMPTY_PARSED_ATTRIBUTES: Record<string, string> = {
  "data-parsed-original-digest": "",
  "data-parsed-document-id": "",
  "data-parser-id": "",
  "data-parser-version": "",
  "data-config-hash": "",
  "data-normalized-digest": "",
  "data-normalization-version": "",
  "data-ir-digest": "",
  "data-artifact-digest": "",
  "data-parsed-created-at": "",
  "data-status": "",
};

function ParsedMetadata({ parsed }: { parsed: ParsedDocumentMetadata }) {
  return (
    <>
      {"，"}解析 <span title={parsed.id}>{shortDigest(parsed.id)}</span>
      {"，"}解析器 {parsed.parser_id}@{parsed.parser_version}
      {"，"}配置 SHA-256{" "}
      <span title={parsed.config_hash}>{shortDigest(parsed.config_hash)}</span>
      {"，"}规范化 {parsed.normalization_version}
      {"，"}正文 SHA-256{" "}
      <span title={titleOf(parsed.normalized_digest)}>
        {hash(parsed.normalized_digest, "不可用")}
      </span>
      {"，"}IR SHA-256{" "}
      <span title={titleOf(parsed.ir_digest)}>
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
  );
}

function LegacyMetadata({ parseStatus }: { parseStatus: string }) {
  const legacy = parseStatus === "ready" || parseStatus === "failed";
  return (
    <>
      {"，"}解析来源 {legacy ? "未记录（历史记录）" : "尚无解析结果"}
      {"，"}正文摘要 未记录
    </>
  );
}

export function ParsedDocumentSummary({
  document,
}: {
  document: DocumentVersion;
}) {
  const parsed = document.parsed_document;
  return (
    <div
      className="parsed-document-summary"
      aria-label="解析来源与摘要"
      data-document-version-id={document.id}
      data-original-digest={document.digest}
      {...(parsed
        ? parsedSummaryAttributes(parsed)
        : EMPTY_PARSED_ATTRIBUTES)}
    >
      <p className="meta">
        原件 SHA-256{" "}
        <span title={document.digest === "" ? undefined : document.digest}>
          {hash(document.digest, "未记录")}
        </span>
        {parsed ? (
          <ParsedMetadata parsed={parsed} />
        ) : (
          <LegacyMetadata parseStatus={document.parse_status} />
        )}
      </p>
    </div>
  );
}
