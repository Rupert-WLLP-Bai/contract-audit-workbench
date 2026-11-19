// File metadata (manifest mirror):
// Purpose: Checks authorized DOCX loading and append-only version saves, and denies saving after the source or authorization changes.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-19
// Updated: 2026-11-19
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { forwardRef, useImperativeHandle } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import DocxWorkspace from "./docx-workspace";

const mocks = vi.hoisted(() => ({
  upload: vi.fn(),
  read: vi.fn(),
  verify: vi.fn(),
  bind: vi.fn(),
}));
vi.mock("@docx-editor.dev/react", () => ({
  DocxEditor: forwardRef(function Editor(
    props: { onChange: (change: { revision: number }) => void },
    ref,
  ) {
    useImperativeHandle(ref, () => ({ save: async () => new Uint8Array([1, 2, 3]).buffer }));
    return <button onClick={() => props.onChange({ revision: 1 })}>修改正文</button>;
  }),
}));
vi.mock("../api/http", () => ({
  captureCommandContext: () => ({ identity: "actor" }),
  assertCommandContextCurrent: vi.fn(),
  bindIdempotencyCommandContext: mocks.bind,
  errorMessage: (e: unknown) => (e instanceof Error ? e.message : "失败"),
  requestBytes: mocks.read,
}));
vi.mock("../api/idempotency", () => ({ newIdempotencyKey: () => "retry-key" }));
vi.mock("../api/queries", () => ({ useUploadDocument: () => ({ mutateAsync: mocks.upload }) }));
vi.mock("../features/review/original-source", () => ({ verifyOriginalDigest: mocks.verify }));
const material = {
  current_document_id: "doc-1",
  current_version: 1,
  legacy_selected: false,
  legacy_status: "native" as const,
  material_id: "mat-1",
  purpose: "primary_contract" as const,
  role: "draft_contract" as const,
  selected: true,
};
const noop = () => {};
function open(canSave = true) {
  return render(
    <DocxWorkspace
      caseId="case-1"
      documentId="doc-1"
      filename="合成.docx"
      digest="a"
      material={material}
      canSave={canSave}
      onDirty={noop}
      onClose={noop}
    />,
  );
}
beforeEach(() => {
  vi.clearAllMocks();
  mocks.read.mockResolvedValue(new Uint8Array([1]));
  mocks.verify.mockResolvedValue(undefined);
  mocks.upload.mockResolvedValue({ id: "doc-2" });
});
describe("DOCX workspace", () => {
  it("reads the authorized original and saves edits as a new version of the same material", async () => {
    const user = userEvent.setup();
    open();
    await user.click(await screen.findByRole("button", { name: "修改正文" }));
    await user.click(screen.getByRole("button", { name: "保存为新版本" }));
    await waitFor(() => expect(mocks.upload).toHaveBeenCalledTimes(1));
    expect(mocks.read.mock.calls[0]?.[0]).toBe("/api/cases/case-1/documents/doc-1/original");
    expect(mocks.verify).toHaveBeenCalled();
    expect(mocks.upload.mock.calls[0]?.[0]).toMatchObject({
      role: "draft_contract",
      intent: "replace",
      material_id: "mat-1",
      replaces_document_id: "doc-1",
      idempotency_key: "retry-key",
    });
    expect(await screen.findByText(/新版本已保存/)).toBeInTheDocument();
  });
  it("does not allow saving when authorization or the current version is no longer valid", async () => {
    const user = userEvent.setup();
    open(false);
    await user.click(await screen.findByRole("button", { name: "修改正文" }));
    expect(screen.getByRole("button", { name: "保存为新版本" })).toBeDisabled();
    expect(mocks.upload).not.toHaveBeenCalled();
  });
});
