// File metadata (manifest mirror):
// Purpose: Loads and verifies authorized DOCX bytes, supports editing and downloading the current copy, and submits edits only as a new permitted material version.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-19
// Updated: 2026-11-19
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { useEffect, useRef, useState } from "react";
import { Button, Modal } from "antd";
import { DocxEditor, type DocxEditorRef } from "@docx-editor.dev/react";
import "@docx-editor.dev/core/styles/editor.css";
import zhCN from "@docx-editor.dev/i18n/zh-CN";
import {
  assertCommandContextCurrent,
  bindIdempotencyCommandContext,
  captureCommandContext,
  errorMessage,
  requestBytes,
} from "../api/http";
import { newIdempotencyKey } from "../api/idempotency";
import { paths } from "../api/paths";
import { useUploadDocument } from "../api/queries";
import type { MaterialProjection } from "../api/types";
import { verifyOriginalDigest } from "../features/review/original-source";
import "./docx-workspace.css";

export default function DocxWorkspace({
  caseId,
  documentId,
  filename,
  digest,
  material,
  canSave,
  onDirty,
  onClose,
}: {
  caseId: string;
  documentId: string;
  filename: string;
  digest: string;
  material: MaterialProjection;
  canSave: boolean;
  onDirty: (dirty: boolean) => void;
  onClose: () => void;
}) {
  const editor = useRef<DocxEditorRef>(null);
  const [bytes, setBytes] = useState<Uint8Array<ArrayBuffer> | null>(null);
  const [error, setError] = useState("");
  const [dirty, setDirty] = useState(false);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const context = useRef<ReturnType<typeof captureCommandContext> | null>(null);
  const retry = useRef<{ bytes: ArrayBuffer; key: string } | null>(null);
  const upload = useUploadDocument(caseId);

  useEffect(() => {
    const controller = new AbortController();
    void (async () => {
      const captured = captureCommandContext();
      const source = await requestBytes(
        paths.documentOriginal(caseId, documentId),
        controller.signal,
      );
      await verifyOriginalDigest(source, digest);
      assertCommandContextCurrent(captured);
      if (!controller.signal.aborted) {
        context.current = captured;
        setBytes(source);
      }
    })().catch((cause: unknown) => {
      if (!controller.signal.aborted) setError(errorMessage(cause));
    });
    return () => {
      controller.abort();
      onDirty(false);
    };
  }, [caseId, documentId, digest, onDirty]);

  async function output(): Promise<ArrayBuffer> {
    if (!context.current) throw new Error("登录上下文不可用，请重新打开文件。");
    assertCommandContextCurrent(context.current);
    const result = await editor.current?.save();
    if (!result || !result.byteLength) throw new Error("当前文档尚未就绪，无法生成文件。");
    assertCommandContextCurrent(context.current);
    return result;
  }
  async function download() {
    setBusy(true);
    setError("");
    try {
      const data = await output();
      const url = URL.createObjectURL(
        new Blob([data], {
          type: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
        }),
      );
      const a = document.createElement("a");
      a.href = url;
      a.download = filename.replace(/\.docx$/i, "") + "-修改稿.docx";
      a.click();
      window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }
  async function save() {
    if (!canSave) {
      setError("材料版本或权限已变化，请返回案件核对。");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const data = retry.current?.bytes ?? (await output());
      const key = retry.current?.key ?? newIdempotencyKey();
      retry.current = { bytes: data, key };
      assertCommandContextCurrent(context.current!);
      bindIdempotencyCommandContext(key, context.current!);
      const file = new File([data], filename, {
        type: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
      });
      await upload.mutateAsync({
        file,
        role: material.role,
        purpose: material.purpose,
        intent: "replace",
        material_id: material.material_id,
        replaces_document_id: documentId,
        idempotency_key: key,
      });
      retry.current = null;
      setDirty(false);
      onDirty(false);
      setSaved(true);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }
  function close() {
    if (busy || (dirty && !window.confirm("修改尚未保存，确定关闭编辑器吗？"))) return;
    onDirty(false);
    onClose();
  }
  return (
    <Modal
      open
      title="编辑合同修改稿"
      width="min(1200px, 96vw)"
      footer={null}
      onCancel={close}
      mask={{ closable: false }}
      keyboard={!busy}
      closable={!busy}
      destroyOnHidden
    >
      <div className="docx-workspace">
        <p>保存为新版本。页面可能重排，目录、页码和复杂对象请在 Word 中核对更新。</p>
        {error && <p role="alert">{error}</p>}
        {saved ? (
          <p role="status">新版本已保存。请回到案件材料打开新版本。</p>
        ) : (
          <>
            <div className="docx-workspace-actions">
              <Button
                disabled={!bytes || busy}
                onClick={() => {
                  if (!bytes) return;
                  const url = URL.createObjectURL(
                    new Blob([bytes], {
                      type: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
                    }),
                  );
                  const link = document.createElement("a");
                  link.href = url;
                  link.download = filename;
                  link.click();
                  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
                }}
              >
                下载原件
              </Button>
              <Button onClick={download} disabled={!bytes || busy}>
                下载修改稿
              </Button>
              <Button
                type="primary"
                onClick={save}
                disabled={!bytes || busy || !dirty || !canSave}
                loading={busy}
              >
                保存为新版本
              </Button>
            </div>
            {!bytes && !error && <p role="status">正在读取原件…</p>}
            {bytes && (
              <div className="docx-workspace-editor">
                <DocxEditor
                  ref={editor}
                  document={bytes}
                  mode="edit"
                  title={filename}
                  locale="zh-CN"
                  i18n={zhCN}
                  menu={false}
                  onChange={(change) => {
                    if (change.source) return;
                    retry.current = null;
                    setDirty(true);
                    onDirty(true);
                  }}
                />
              </div>
            )}
          </>
        )}
      </div>
    </Modal>
  );
}
