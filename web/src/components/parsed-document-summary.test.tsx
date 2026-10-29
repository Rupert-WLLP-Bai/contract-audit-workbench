import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { DocumentVersion, ParsedDocumentMetadata } from "../api/types";
import { formatDateTime, shortDigest } from "../domain/review";
import { renderWithAntd as render } from "../test/antd";
import { ParsedDocumentSummary } from "./parsed-document-summary";

const at = "2026-10-21T06:00:00Z";

const parsed: ParsedDocumentMetadata = {
  id: "parsed-document-1",
  document_id: "document-1",
  original_digest: "original-digest",
  parser_id: "parser-a",
  parser_version: "v2",
  config_hash: "config-hash-1",
  normalization_version: "norm-v1",
  normalized_digest: "normalized-digest-1",
  ir_digest: "ir-digest-1",
  artifact_digest: "artifact-digest-1",
  created_at: at,
  status: "ready",
};

function document(overrides: Partial<DocumentVersion> = {}): DocumentVersion {
  return {
    id: "document-version-1",
    role: "draft_contract",
    filename: "合同.txt",
    media_type: "text/plain",
    digest: "document-digest-1",
    size: 10,
    parse_status: "ready",
    parse_error: null,
    created_at: at,
    parsed_document: parsed,
    ...overrides,
  };
}

describe("parsed document summary", () => {
  it("shows the frozen parse identities and digests for a ready parse", () => {
    render(<ParsedDocumentSummary document={document()} />);
    const summary = screen.getByLabelText("解析来源与摘要");
    expect(summary).toHaveTextContent("原件 SHA-256 document");
    expect(summary).toHaveTextContent(
      `解析 ${shortDigest(parsed.id)}，解析器 parser-a@v2`,
    );
    expect(summary).toHaveTextContent(`配置 SHA-256 ${shortDigest(parsed.config_hash)}`);
    expect(summary).toHaveTextContent("规范化 norm-v1");
    expect(summary).toHaveTextContent(
      `正文 SHA-256 ${shortDigest(parsed.normalized_digest!)}`,
    );
    expect(summary).toHaveTextContent(
      `IR SHA-256 ${shortDigest(parsed.ir_digest!)}`,
    );
    expect(summary).toHaveTextContent(
      `产物 SHA-256 ${shortDigest(parsed.artifact_digest)}`,
    );
    expect(summary).toHaveTextContent(`解析就绪，${formatDateTime(at)}`);
  });

  it("exports the parse identity as data attributes for audit checks", () => {
    render(<ParsedDocumentSummary document={document()} />);
    const summary = screen.getByLabelText("解析来源与摘要");
    expect(summary).toHaveAttribute("data-document-version-id", "document-version-1");
    expect(summary).toHaveAttribute("data-original-digest", "document-digest-1");
    expect(summary).toHaveAttribute("data-parsed-document-id", parsed.id);
    expect(summary).toHaveAttribute("data-parser-id", "parser-a");
    expect(summary).toHaveAttribute("data-parser-version", "v2");
    expect(summary).toHaveAttribute("data-config-hash", parsed.config_hash);
    expect(summary).toHaveAttribute("data-normalized-digest", parsed.normalized_digest);
    expect(summary).toHaveAttribute("data-normalization-version", "norm-v1");
    expect(summary).toHaveAttribute("data-ir-digest", parsed.ir_digest);
    expect(summary).toHaveAttribute("data-artifact-digest", parsed.artifact_digest);
    expect(summary).toHaveAttribute("data-parsed-created-at", at);
    expect(summary).toHaveAttribute("data-status", "ready");
  });

  it("marks a failed parse and missing optional digests", () => {
    render(
      <ParsedDocumentSummary
        document={document({
          parse_status: "failed",
          parsed_document: {
            ...parsed,
            status: "failed",
            normalized_digest: null,
            ir_digest: null,
            created_at: "",
          },
        })}
      />,
    );
    const summary = screen.getByLabelText("解析来源与摘要");
    expect(summary).toHaveTextContent("正文 SHA-256 不可用");
    expect(summary).toHaveTextContent("IR SHA-256 不可用");
    expect(summary).toHaveTextContent("解析失败");
    expect(summary).not.toHaveTextContent("时间未记录");
    expect(summary).toHaveAttribute("data-normalized-digest", "");
    expect(summary).toHaveAttribute("data-ir-digest", "");
    expect(summary).toHaveAttribute("data-status", "failed");
  });

  it("labels a short missing original digest instead of inventing one", () => {
    render(
      <ParsedDocumentSummary
        document={document({ digest: "", parsed_document: { ...parsed, normalized_digest: "", ir_digest: "" } })}
      />,
    );
    const summary = screen.getByLabelText("解析来源与摘要");
    expect(summary).toHaveTextContent("原件 SHA-256 摘要缺失");
    expect(summary).toHaveTextContent("正文 SHA-256 摘要缺失");
    expect(summary).toHaveTextContent("IR SHA-256 摘要缺失");
  });

  it("marks an unavailable original digest as unrecorded", () => {
    render(
      <ParsedDocumentSummary
        document={document({ digest: null as unknown as string })}
      />,
    );
    expect(screen.getByLabelText("解析来源与摘要")).toHaveTextContent(
      "原件 SHA-256 未记录",
    );
  });

  it("calls a legacy parse without metadata a historical record", () => {
    render(
      <ParsedDocumentSummary
        document={document({ parse_status: "ready", parsed_document: null })}
      />,
    );
    const summary = screen.getByLabelText("解析来源与摘要");
    expect(summary).toHaveTextContent(
      "解析来源 未记录（历史记录），正文摘要 未记录",
    );
    expect(summary).toHaveAttribute("data-parsed-document-id", "");
    expect(summary).toHaveAttribute("data-status", "");
  });

  it("distinguishes a legacy failed parse from a missing parse", () => {
    render(
      <ParsedDocumentSummary
        document={document({ parse_status: "failed", parsed_document: null })}
      />,
    );
    expect(screen.getByLabelText("解析来源与摘要")).toHaveTextContent(
      "解析来源 未记录（历史记录）",
    );
  });

  it("says a parse result is still absent when parsing is pending", () => {
    render(
      <ParsedDocumentSummary
        document={document({ parse_status: "pending", parsed_document: null })}
      />,
    );
    expect(screen.getByLabelText("解析来源与摘要")).toHaveTextContent(
      "解析来源 尚无解析结果",
    );
  });
});
