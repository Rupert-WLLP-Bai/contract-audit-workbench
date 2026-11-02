import { Alert, Button, Input, Progress, Select } from "antd";
import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router";
import {
  ApiError,
  assertCommandContextCurrent,
  captureCommandContext,
  bindIdempotencyCommandContext,
  errorMessage,
  isConflict,
  isForbidden,
  isCommandContextVersionCurrent,
  isSessionIdentityCurrent,
  sessionIdentityFor,
  isUnauthenticated,
} from "../api/http";
import { fileSha256, newIdempotencyKey } from "../api/idempotency";
import {
  createIntake,
  getIntake,
  getIntakeByKey,
  getIntakeContinuation,
  INTAKE_MAX_FILE_BYTES,
  type IntakeResult,
} from "../api/intakes";
import type { Actor } from "../api/types";
import {
  caseHref,
  formatBytes,
  formatDateTime,
  validateCaseTitle,
} from "../domain/review";
import {
  clearIntakeContinuation,
  continuationFileProblem,
  pendingReceipt,
  readIntakeContinuation,
  scopeOf,
  shortDigest,
  withReceipt,
  writeIntakeContinuation,
  type IntakeContinuation,
  type IntakeContinuationSelection,
} from "../features/intake/intake-continuation";

/** The server accepts only existing case business lines; this mirrors case creation. */
const BUSINESS_LINE = "ENT";

const SCENARIOS = [
  { value: "telecom_dedicated_line", label: "政企专线" },
  { value: "cloud_services", label: "云服务采购" },
] as const;

const DEFAULT_SCENARIO = SCENARIOS[0].value;

const CONTRACT_TYPES = [
  { value: "procurement_ict", label: "采购 / ICT" },
  { value: "network_engineering", label: "网络工程" },
  { value: "service", label: "服务合同" },
  { value: "unspecified", label: "未分类" },
] as const;

const INTAKE_ERROR_LABELS: Record<string, string> = {
  document_upload_unavailable: "上传服务暂不可用",
  intake_archived: "该办理已归档",
};

type Busy = "hashing" | "uploading" | "recovering" | null;

type Problem =
  | { kind: "missing" }
  | { kind: "unauthenticated" }
  | { kind: "forbidden"; message: string }
  | { kind: "error"; message: string };

interface Conflict {
  message: string;
  existing: IntakeResult | null;
  origin: "client" | "server";
}

function problemFromError(error: unknown): Problem {
  if (isUnauthenticated(error)) return { kind: "unauthenticated" };
  if (isForbidden(error))
    return { kind: "forbidden", message: errorMessage(error) };
  if (error instanceof ApiError && error.status === 404)
    return { kind: "missing" };
  return { kind: "error", message: errorMessage(error) };
}

function isAbortError(error: unknown): boolean {
  return error instanceof Error && error.name === "AbortError";
}

function requestReceipt(
  continuation: IntakeContinuation,
  signal?: AbortSignal,
): Promise<IntakeResult> {
  return continuation.receipt.operation_id === null
    ? getIntakeByKey(continuation.idempotency_key, signal)
    : getIntake(continuation.receipt.operation_id, signal);
}

function continuationFromSelection(
  actor: Actor,
  selection: IntakeContinuationSelection,
): IntakeContinuation | null {
  const { detail } = selection;
  if (detail.request === null || detail.idempotency_key === null) return null;
  return {
    version: 1,
    scope: scopeOf(actor),
    idempotency_key: detail.idempotency_key,
    request: detail.request,
    receipt: {
      operation_id: detail.operation_id,
      case_id: detail.case_id,
      state: detail.state,
      material_id: null,
      material_version: null,
      document_id: null,
      error_code: selection.archived ? "intake_archived" : selection.error_code,
      retryable: detail.state === "upload_pending" && !selection.archived,
    },
    updated_at: new Date().toISOString(),
  };
}

const CONTINUATION_REASON_LABELS: Record<string, string> = {
  legacy_request_unavailable: "这是旧办理，服务器没有可验证的原始请求信息，无法安全续传。",
  request_identity_invalid: "服务器无法验证原始文件身份，已停止续办以避免改写案件。",
  forbidden: "当前身份无权继续该办理。请使用原经办身份登录，或在有权查看时打开原案件。",
  read_only: "当前工作区处于只读状态，不能继续上传。",
  case_not_writable: "原案件当前不可写，不能继续上传。",
  completed: "服务器已确认该办理完成；请打开原案件查看。",
  archived: "该办理已归档。归档只从未完成列表中隐藏办理，不会删除原案件；已开始的上传仍可能完成。",
};

const ARCHIVED_NOTICE =
  CONTINUATION_REASON_LABELS.archived ?? "该办理已归档，继续上传已关闭。";

/**
 * Homepage entry that creates one case from one primary contract. The server
 * fixes the intake intent; this component never sends a replacement or a
 * single-contract review request.
 */
export function IntakePrimaryContract({
  actor,
  selectedContinuation = null,
}: {
  actor: Actor | null;
  selectedContinuation?: IntakeContinuationSelection | null;
}) {
  const navigate = useNavigate();
  const headingId = useId();
  const titleInputId = useId();
  const fileInputId = useId();
  const resumeInputId = useId();
  const hintId = useId();
  const titleErrorId = useId();
  const fileErrorId = useId();

  const [hydrated, setHydrated] = useState(false);
  const [draft, setDraft] = useState<IntakeContinuation | null>(null);

  const [localAccess, setLocalAccess] =
    useState<IntakeContinuationSelection | null>(null);
  const [busy, setBusy] = useState<Busy>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [conflict, setConflict] = useState<Conflict | null>(null);
  const [confirmNew, setConfirmNew] = useState(false);

  const [title, setTitle] = useState("");
  const [scenario, setScenario] = useState<string>(DEFAULT_SCENARIO);
  const [contractType, setContractType] = useState<string>("service");
  const [file, setFile] = useState<File | null>(null);
  const [resumeFile, setResumeFile] = useState<File | null>(null);
  const [titleError, setTitleError] = useState<string | null>(null);
  const [fileError, setFileError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [resumeError, setResumeError] = useState<string | null>(null);
  const submitting = useRef(false);

  const actorKey =
    actor === null
      ? ""
      : [actor.audience, actor.tenant_id, actor.org_id, actor.id, actor.active_context?.context_version ?? ""].join(
          "\u0000",
        );
  const canCreate =
    actor !== null && (actor.role === "admin" || actor.role === "clerk");
  const selectedOperationId = selectedContinuation?.detail.operation_id ?? "";
  const selectedOperationRef = useRef(selectedOperationId);
  selectedOperationRef.current = selectedOperationId;
  const actorKeyRef = useRef(actorKey);
  actorKeyRef.current = actorKey;
  const actorSessionIdentity = actor ? sessionIdentityFor(actor) : "";
  const actorSessionIdentityRef = useRef(actorSessionIdentity);
  actorSessionIdentityRef.current = actorSessionIdentity;
  const activeContinuationKeyRef = useRef<string | null>(null);
  const activeAccess =
    draft?.receipt.operation_id == null
      ? null
      : localAccess?.detail.operation_id === draft.receipt.operation_id
        ? localAccess
        : selectedContinuation?.detail.operation_id === draft.receipt.operation_id
          ? selectedContinuation
          : null;
  const canResumeCurrent =
    activeAccess?.detail.can_resume === true && !activeAccess.archived;

  function isCurrentContinuation(continuation: IntakeContinuation): boolean {
    return (
      actorKeyRef.current ===
        [
          continuation.scope.audience,
          continuation.scope.tenant_id,
          continuation.scope.org_id,
          continuation.scope.actor_id,
          continuation.scope.context_version,
        ].join("\u0000") &&
      (continuation.scope.context_version === "legacy" || isCommandContextVersionCurrent(continuation.scope.context_version)) &&
      isSessionIdentityCurrent(actorSessionIdentityRef.current) &&
      activeContinuationKeyRef.current === continuation.idempotency_key
    );
  }

  function blockLocalContinuation(operationId: string, reasonCode: string) {
    setLocalAccess((current) => {
      const access =
        current?.detail.operation_id === operationId
          ? current
          : selectedContinuation?.detail.operation_id === operationId
            ? selectedContinuation
            : null;
      if (access === null) return current;
      const archived = reasonCode === "archived";
      return {
        ...access,
        archived,
        detail: {
          ...access.detail,
          can_resume: false,
          idempotency_key: null,
          reason_code: reasonCode,
        },
      };
    });
  }

  useEffect(() => {
    const controller = new AbortController();
    const selectionAtStart = selectedOperationRef.current;
    setHydrated(false);
    activeContinuationKeyRef.current = null;
    setDraft(null);
    setLocalAccess(null);
    setBusy(null);
    setNotice(null);
    setProblem(null);
    setConflict(null);
    setConfirmNew(false);
    setTitle("");
    setScenario(DEFAULT_SCENARIO);
    setContractType("service");
    setFile(null);
    setResumeFile(null);
    setTitleError(null);
    setFileError(null);
    setFormError(null);
    setResumeError(null);
    if (actor === null) {
      setHydrated(true);
      return () => controller.abort();
    }
    if (selectionAtStart !== "") {
      setHydrated(true);
      return () => controller.abort();
    }
    void (async () => {
      let stored: IntakeContinuation | null = null;
      try {
        stored = await readIntakeContinuation(actor);
      } catch {
        stored = null;
      }
      if (
        controller.signal.aborted ||
        selectionAtStart !== selectedOperationRef.current
      )
        return;
      activeContinuationKeyRef.current = stored?.idempotency_key ?? null;
      setDraft(stored);
      setHydrated(true);
      if (stored === null) return;
      setBusy("recovering");
      try {
        const result = await requestReceipt(stored, controller.signal);
        if (
          controller.signal.aborted ||
          selectionAtStart !== selectedOperationRef.current
        )
          return;
        let next = withReceipt(stored, result);
        if (result.state === "upload_pending") {
          const detail = await getIntakeContinuation(
            result.operation_id,
            controller.signal,
          );
          if (
            controller.signal.aborted ||
            selectionAtStart !== selectedOperationRef.current
          )
            return;
          setLocalAccess({ detail, error_code: result.error_code, archived: false });
          if (
            detail.can_resume &&
            detail.request !== null &&
            detail.idempotency_key !== null
          ) {
            next = {
              ...next,
              request: detail.request,
              idempotency_key: detail.idempotency_key,
            };
          }
        } else {
          setLocalAccess(null);
        }
        setDraft(next);
        activeContinuationKeyRef.current = next.idempotency_key;
        try {
          await writeIntakeContinuation(next);
        } catch {
          /* The in-memory receipt still drives this session; see help text. */
        }
      } catch (error) {
        if (
          controller.signal.aborted ||
          isAbortError(error) ||
          selectionAtStart !== selectedOperationRef.current
        )
          return;
        if (error instanceof ApiError && error.code === "intake_archived")
          setNotice(ARCHIVED_NOTICE);
        if (
          error instanceof ApiError &&
          error.code === "intake_archived" &&
          stored.receipt.operation_id !== null
        ) {
          try {
            const detail = await getIntakeContinuation(
              stored.receipt.operation_id,
              controller.signal,
            );
            if (controller.signal.aborted) return;
            setLocalAccess({
              detail,
              error_code: "intake_archived",
              archived: false,
            });
          } catch (detailError) {
            if (!controller.signal.aborted)
              setProblem(problemFromError(detailError));
          }
        } else {
          setProblem(problemFromError(error));
        }
      } finally {
        if (!controller.signal.aborted) setBusy(null);
      }
    })();
    return () => controller.abort();
  }, [actorKey]);

  useEffect(() => {
    if (actor === null || selectedContinuation === null) return;
    const continuation = continuationFromSelection(actor, selectedContinuation);
    activeContinuationKeyRef.current = continuation?.idempotency_key ?? null;
    setLocalAccess(selectedContinuation);
    setBusy(null);
    setProblem(null);
    setConflict(null);
    setConfirmNew(false);
    setResumeFile(null);
    setResumeError(null);
    setHydrated(true);
    if (continuation === null) {
      setDraft(null);
      setProblem({
        kind: "error",
        message: "服务器没有提供可验证的原始请求；请从未完成办理列表查看原因。",
      });
      return;
    }
    setDraft(continuation);
    activeContinuationKeyRef.current = continuation.idempotency_key;
    setNotice(
      selectedContinuation.archived
        ? "该办理已归档，继续上传已关闭。"
        : "已读取服务器保存的原始请求。重新选择文件并通过校验后，才会提交同一办理。",
    );
  }, [actorKey, selectedContinuation]);

  async function submitNew(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (actor === null || !canCreate || busy !== null || submitting.current)
      return;
    const actorKeyAtSubmit = actorKey;
    const commandContext = captureCommandContext(actor);
    const titleProblem = validateCaseTitle(title);
    setTitleError(titleProblem);
    if (titleProblem !== null) return;
    if (file === null) {
      setFileError("请选择主合同文件");
      return;
    }
    if (file.size > INTAKE_MAX_FILE_BYTES) {
      setFileError("主合同文件不能超过 32 MiB");
      return;
    }
    submitting.current = true;
    try {
      setFileError(null);
      setFormError(null);
      setProblem(null);
      setConflict(null);
      setNotice(null);
      setBusy("hashing");
      let sha256: string;
      try {
        sha256 = await fileSha256(file);
        assertCommandContextCurrent(commandContext);
        if (actorKeyRef.current !== actorKeyAtSubmit) return;
      } catch {
        setBusy(null);
        setFormError("无法读取文件内容，请重新选择主合同文件");
        return;
      }
      const continuation: IntakeContinuation = {
        version: 1,
        scope: scopeOf(actor),
        idempotency_key: newIdempotencyKey(),
        request: {
          title: title.trim(),
          contract_type: contractType,
          business_line: BUSINESS_LINE,
          scenario,
          file_name: file.name,
          file_size: file.size,
          media_type: file.type,
          file_sha256: sha256,
        },
        receipt: pendingReceipt(),
        updated_at: new Date().toISOString(),
      };
      if (actorKeyRef.current !== actorKeyAtSubmit) return;
      try {
        await writeIntakeContinuation(continuation);
        assertCommandContextCurrent(commandContext);
      } catch (error) {
        setBusy(null);
        setFormError(
          error instanceof Error
            ? error.message
            : "浏览器无法保存续办记录，请允许本站使用本地存储后重试",
        );
        return;
      }
      if (actorKeyRef.current !== actorKeyAtSubmit) return;
      setDraft(continuation);
      activeContinuationKeyRef.current = continuation.idempotency_key;
      await postContinuation(continuation, file, commandContext);
    } finally {
      submitting.current = false;
    }
  }

  async function postContinuation(
    continuation: IntakeContinuation,
    chosen: File,
    commandContext: ReturnType<typeof captureCommandContext>,
  ) {
    if (!isCurrentContinuation(continuation)) return;
    assertCommandContextCurrent(commandContext);
    bindIdempotencyCommandContext(continuation.idempotency_key, commandContext);
    setBusy("uploading");
    setProblem(null);
    setConflict(null);
    setNotice(null);
    setResumeError(null);
    const replayFile =
      chosen.type === continuation.request.media_type &&
      chosen.name === continuation.request.file_name
        ? chosen
        : new File([chosen], continuation.request.file_name, {
            type: continuation.request.media_type,
          });
    try {
      assertCommandContextCurrent(commandContext);
      const result = await createIntake(
        {
          title: continuation.request.title,
          contract_type: continuation.request.contract_type,
          business_line: continuation.request.business_line,
          scenario: continuation.request.scenario,
          file: replayFile,
        },
        continuation.idempotency_key,
      );
      if (!isCurrentContinuation(continuation)) return;
      assertCommandContextCurrent(commandContext);
      let next = withReceipt(continuation, result);
      if (result.state === "upload_pending") {
        try {
          const detail = await getIntakeContinuation(result.operation_id);
          if (!isCurrentContinuation(continuation)) return;
          setLocalAccess({
            detail,
            error_code: result.error_code,
            archived: false,
          });
          if (
            detail.can_resume &&
            detail.request !== null &&
            detail.idempotency_key !== null
          ) {
            next = {
              ...next,
              request: detail.request,
              idempotency_key: detail.idempotency_key,
            };
          }
        } catch (detailError) {
          if (isCurrentContinuation(continuation))
            setProblem(problemFromError(detailError));
        }
      }
      if (!isCurrentContinuation(continuation)) return;
      setDraft(next);
      activeContinuationKeyRef.current = next.idempotency_key;
      setResumeFile(null);
      let stored = true;
      try {
        await writeIntakeContinuation(next);
      } catch {
        stored = false;
      }
      if (!isCurrentContinuation(next)) return;
      setBusy(null);
      if (result.state === "completed") {
        if (stored) {
          navigate(caseHref(result.case_id, null));
          return;
        }
        setProblem({
          kind: "error",
          message: `主合同已上传，但本机无法保存回执；请从案件列表打开案件 ${result.case_id} 并查看准备清单。`,
        });
        return;
      }
      if (!stored) {
        setProblem({
          kind: "error",
          message: `服务器已保留该操作，但本机无法保存续办记录；请记录案件编号 ${result.case_id}，并在本机恢复后继续上传。`,
        });
      }
    } catch (error) {
      if (!isCurrentContinuation(continuation)) return;
      if (isAbortError(error)) {
        setBusy(null);
        setProblem({
          kind: "error",
          message:
            "登录身份已变化，本次请求已取消；服务器未确认前请从未完成办理列表重新选择原办理。",
        });
        return;
      }
      if (
        error instanceof ApiError &&
        error.code === "intake_archived" &&
        continuation.receipt.operation_id !== null
      ) {
        setBusy("recovering");
        blockLocalContinuation(
          continuation.receipt.operation_id,
          "archived",
        );
        setNotice(ARCHIVED_NOTICE);
        setResumeFile(null);
        try {
          const detail = await getIntakeContinuation(
            continuation.receipt.operation_id,
          );
          if (!isCurrentContinuation(continuation)) return;
          setLocalAccess({
            detail,
            error_code: "intake_archived",
            archived: false,
          });
          setNotice(ARCHIVED_NOTICE);
        } catch (detailError) {
          if (isCurrentContinuation(continuation))
            setProblem(problemFromError(detailError));
        } finally {
          if (isCurrentContinuation(continuation)) setBusy(null);
        }
        return;
      }
      if (
        error instanceof ApiError &&
        ["forbidden", "read_only", "case_not_writable"].includes(error.code) &&
        continuation.receipt.operation_id !== null
      ) {
        blockLocalContinuation(
          continuation.receipt.operation_id,
          error.code,
        );
        setResumeFile(null);
        setBusy("recovering");
        try {
          const detail = await getIntakeContinuation(
            continuation.receipt.operation_id,
          );
          if (!isCurrentContinuation(continuation)) return;
          setLocalAccess({
            detail,
            error_code: continuation.receipt.error_code,
            archived: false,
          });
        } catch {
          /* The original write error remains visible below. */
        } finally {
          if (isCurrentContinuation(continuation)) setBusy(null);
        }
        setProblem(problemFromError(error));
        return;
      }
      if (isConflict(error)) {
        setBusy("recovering");
        let existing: IntakeResult | null = null;
        try {
          existing = await requestReceipt(continuation);
        } catch {
          existing = null;
        }
        if (!isCurrentContinuation(continuation)) return;
        setBusy(null);
        setResumeFile(null);
        setConflict({ message: errorMessage(error), existing, origin: "server" });
        return;
      }
      if (
        error instanceof ApiError &&
        (error.status === 0 ||
          error.status >= 500 ||
          error.code === "invalid_response")
      ) {
        try {
          const recovered = await requestReceipt(continuation);
          if (!isCurrentContinuation(continuation)) return;
          let next = withReceipt(continuation, recovered);
          if (recovered.state === "upload_pending") {
            const detail = await getIntakeContinuation(recovered.operation_id);
            if (!isCurrentContinuation(continuation)) return;
            setLocalAccess({
              detail,
              error_code: recovered.error_code,
              archived: false,
            });
            if (
              detail.can_resume &&
              detail.request !== null &&
              detail.idempotency_key !== null
            ) {
              next = {
                ...next,
                request: detail.request,
                idempotency_key: detail.idempotency_key,
              };
            }
          } else {
            setLocalAccess(null);
          }
          setDraft(next);
          activeContinuationKeyRef.current = next.idempotency_key;
          try {
            await writeIntakeContinuation(next);
          } catch {
            /* The recovered record stays usable for this session. */
          }
          if (!isCurrentContinuation(next)) return;
          setNotice("已按同一幂等键从服务器恢复该操作。");
          if (recovered.state === "completed")
            navigate(caseHref(recovered.case_id, null));
          return;
        } catch (recoverError) {
          if (isAbortError(recoverError) || !isCurrentContinuation(continuation))
            return;
          if (
            recoverError instanceof ApiError &&
            recoverError.code === "intake_archived" &&
            continuation.receipt.operation_id !== null
          ) {
            try {
              const detail = await getIntakeContinuation(
                continuation.receipt.operation_id,
              );
              if (!isCurrentContinuation(continuation)) return;
              setLocalAccess({
                detail,
                error_code: "intake_archived",
                archived: false,
              });
              setNotice(ARCHIVED_NOTICE);
            } catch (detailError) {
              if (isCurrentContinuation(continuation))
                setProblem(problemFromError(detailError));
            }
            return;
          }
          setProblem(problemFromError(recoverError));
          return;
        } finally {
          if (isCurrentContinuation(continuation)) setBusy(null);
        }
      }
      setBusy(null);
      setProblem(problemFromError(error));
    }
  }

  async function retryWithFile(
    chosen: File,
    reportError: (message: string | null) => void,
  ) {
    const current = draft;
    if (current === null || actor === null || busy !== null || submitting.current) return;
    const allowed =
      current.receipt.state === "upload_pending"
        ? canResumeCurrent
        : canCreate;
    if (!allowed) {
      reportError("服务端当前未授权继续该办理；请从未完成办理列表刷新权限状态。打开原案件不会更改办理。 ");
      return;
    }
    const actorKeyAtStart = actorKey;
    const commandContext = captureCommandContext(actor);
    if (chosen.size > INTAKE_MAX_FILE_BYTES) {
      reportError("主合同文件不能超过 32 MiB");
      return;
    }
    submitting.current = true;
    try {
      reportError(null);
      setProblem(null);
      setConflict(null);
      setNotice(null);
      setBusy("hashing");
      let sha256: string;
      try {
        sha256 = await fileSha256(chosen);
        assertCommandContextCurrent(commandContext);
        if (
          actorKeyRef.current !== actorKeyAtStart ||
          !isCurrentContinuation(current)
        )
          return;
      } catch {
        if (!isCurrentContinuation(current)) return;
        setBusy(null);
        reportError("无法读取文件内容，请重新选择主合同文件");
        return;
      }
      const mismatch = continuationFileProblem(current, chosen, sha256);
      if (mismatch !== null) {
        setBusy(null);
        setResumeFile(null);
        setConflict({ message: mismatch, existing: null, origin: "client" });
        return;
      }
      await postContinuation(current, chosen, commandContext);
    } finally {
      submitting.current = false;
    }
  }

  function submitResume(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (resumeFile === null) {
      setResumeError("请重新选择最初提交的主合同文件");
      return;
    }
    void retryWithFile(resumeFile, (message) => setResumeError(message));
  }

  async function refreshState() {
    const current = draft;
    if (current === null || busy !== null || !isCurrentContinuation(current))
      return;
    setProblem(null);
    setConflict(null);
    setNotice(null);
    setBusy("recovering");
    try {
      const result = await requestReceipt(current);
      if (!isCurrentContinuation(current)) return;
      let next = withReceipt(current, result);
      if (result.state === "upload_pending") {
        const detail = await getIntakeContinuation(result.operation_id);
        if (!isCurrentContinuation(current)) return;
        setLocalAccess({ detail, error_code: result.error_code, archived: false });
        if (
          detail.can_resume &&
          detail.request !== null &&
          detail.idempotency_key !== null
        ) {
          next = {
            ...next,
            request: detail.request,
            idempotency_key: detail.idempotency_key,
          };
        }
      } else {
        setLocalAccess(null);
      }
      setDraft(next);
      activeContinuationKeyRef.current = next.idempotency_key;
      try {
        await writeIntakeContinuation(next);
      } catch {
        /* Keep the recovered record for this session. */
      }
      if (!isCurrentContinuation(next)) return;
      setNotice(
        result.state === "completed"
          ? "服务器确认主合同已上传完成。"
          : "服务器确认主合同仍在等待上传。",
      );
    } catch (error) {
      if (isAbortError(error) || !isCurrentContinuation(current)) return;
      if (
        error instanceof ApiError &&
        error.code === "intake_archived" &&
        current.receipt.operation_id !== null
      ) {
        blockLocalContinuation(current.receipt.operation_id, "archived");
        setNotice(ARCHIVED_NOTICE);
        setResumeFile(null);
        try {
          const detail = await getIntakeContinuation(
            current.receipt.operation_id,
          );
          if (!isCurrentContinuation(current)) return;
          setLocalAccess({ detail, error_code: "intake_archived", archived: false });
          setNotice(ARCHIVED_NOTICE);
          return;
        } catch (detailError) {
          if (isCurrentContinuation(current))
            setProblem(problemFromError(detailError));
          return;
        }
      }
      setProblem(problemFromError(error));
      if (
        error instanceof ApiError &&
        ["forbidden", "read_only", "case_not_writable"].includes(error.code)
      ) {
        const operationId = current.receipt.operation_id;
        if (operationId !== null)
          blockLocalContinuation(operationId, error.code);
        setResumeFile(null);
      }
    } finally {
      if (isCurrentContinuation(current)) setBusy(null);
    }
  }

  async function discardLocalRecord() {
    if (actor === null || busy !== null) return;
    await clearIntakeContinuation(actor);
    setDraft(null);
    setConfirmNew(false);
    setProblem(null);
    setConflict(null);
    setNotice(null);
    setResumeFile(null);
    setResumeError(null);
    setFormError(null);
    setTitleError(null);
    setFileError(null);
    setTitle("");
    setScenario(DEFAULT_SCENARIO);
    setContractType("service");
    setFile(null);
  }

  function renderProgress() {
    const stage = busy === "hashing" ? 1 : 2;
    const label =
      busy === "hashing"
        ? "正在计算主合同文件的 SHA-256"
        : "正在上传主合同并等待服务器确认";
    return (
      <div className="home-intake-progress" role="status" aria-live="polite">
        <Progress
          percent={Math.round((stage / 3) * 100)}
          status="active"
          aria-label={`主合同上传进度：步骤 ${stage}/3`}
          format={() => `步骤 ${stage}/3`}
        />
        <p>{label}</p>
        <p className="home-intake-hint">
          文件内容只在浏览器内存中读取，用于摘要校验和本次上传；不会保存到本地存储。
        </p>
      </div>
    );
  }

  function renderProblem() {
    if (problem === null) return null;
    const retry =
      draft === null ? undefined : (
        <Button
          size="small"
          htmlType="button"
          disabled={busy !== null}
          onClick={() => void refreshState()}
        >
          再次查询
        </Button>
      );
    if (problem.kind === "missing")
      return (
        <Alert
          type="warning"
          showIcon
          title="服务器没有该幂等键对应的操作"
          description={
            draft !== null && draft.receipt.state === "completed"
              ? "无法从服务器确认这张回执；案件可能已归档，或当前身份无权读取。这不代表回执被改写，服务器不会返回第二个案件。"
              : "请求可能没有到达服务器，也可能已被清理。可以用最初选择的同一文件重新提交；同一幂等键不会建出第二个案件。"
          }
          action={retry}
        />
      );
    if (problem.kind === "unauthenticated")
      return (
        <Alert
          type="warning"
          showIcon
          title="登录已失效"
          description="本机续办记录仍保留。重新登录后会按同一幂等键继续同一个案件，不会重复建案。"
          action={
            <Button href="/login" size="small">
              重新登录
            </Button>
          }
        />
      );
    const description =
      problem.kind === "forbidden"
        ? `${problem.message}。当前身份不能读取或继续这个案件，需要案件成员处理。`
        : problem.message;
    return (
      <Alert
        type={problem.kind === "forbidden" ? "warning" : "error"}
        showIcon
        title={
          problem.kind === "forbidden"
            ? "当前身份没有权限继续该操作"
            : "主合同上传没有完成"
        }
        description={description}
        action={retry}
      />
    );
  }

  function renderDraftIdentity(continuation: IntakeContinuation) {
    const receipt = continuation.receipt;
    const caseId = receipt.case_id;
    return (
      <dl className="home-intake-identity" aria-label="续办记录">
        <div>
          <dt>案件标题</dt>
          <dd>{continuation.request.title}</dd>
        </div>
        <div>
          <dt>主合同文件</dt>
          <dd>
            {continuation.request.file_name} ·{" "}
            {formatBytes(continuation.request.file_size)} · SHA-256{" "}
            <code title={continuation.request.file_sha256}>
              {shortDigest(continuation.request.file_sha256)}…
            </code>
          </dd>
        </div>
        <div>
          <dt>操作编号</dt>
          <dd>
            {receipt.operation_id === null ? (
              "尚未取得"
            ) : (
              <code>{receipt.operation_id}</code>
            )}
          </dd>
        </div>
        <div>
          <dt>案件编号</dt>
          <dd>
            {caseId === null ? (
              "尚未取得"
            ) : (
              <Link to={caseHref(caseId, null)}>{caseId}</Link>
            )}
          </dd>
        </div>
      </dl>
    );
  }

  function renderNewForm() {
    if (!canCreate)
      return (
        <div className="home-intake-permission" role="note">
          <strong>当前身份可以查看案件，不能新建案件或上传主合同</strong>
          <p>
            主合同上传会同时建立案件并登记材料，请由管理员或案件登记人员办理。需要登记多份材料、主体或关联案件时，可在
            <Link to="/cases">案件中心</Link>查看现有案件。
          </p>
        </div>
      );
    return (
      <form className="home-intake-form" onSubmit={submitNew} noValidate>
        <label className="home-intake-field" htmlFor={titleInputId}>
          案件标题
          <Input
            id={titleInputId}
            value={title}
            maxLength={120}
            required
            aria-invalid={titleError !== null}
            aria-describedby={titleError === null ? undefined : titleErrorId}
            onChange={(event) => {
              setTitle(event.target.value);
              setTitleError(null);
            }}
          />
        </label>
        {titleError === null ? null : (
          <p className="home-inline-error" id={titleErrorId} role="alert">
            {titleError}
          </p>
        )}
        <label className="home-intake-field">
          业务场景
          <Select<string>
            aria-label="业务场景"
            value={scenario}
            onChange={(value) => {
              setScenario(value);
              if (value === "cloud_services") setContractType("service");
            }}
            options={[...SCENARIOS]}
          />
        </label>
        <label className="home-intake-field">
          合同类型
          <Select<string>
            aria-label="合同类型"
            value={contractType}
            disabled={scenario === "cloud_services"}
            onChange={(value) => setContractType(value)}
            options={[...CONTRACT_TYPES]}
          />
        </label>
        <div className="home-intake-field home-intake-file">
          <label htmlFor={fileInputId}>主合同文件</label>
          <input
            id={fileInputId}
            type="file"
            aria-describedby={hintId}
            aria-invalid={fileError !== null}
            aria-errormessage={fileError === null ? undefined : fileErrorId}
            onChange={(event) => {
              setFile(event.target.files?.[0] ?? null);
              setFileError(null);
            }}
          />
        </div>
        <p className="home-intake-hint" id={hintId}>
          单个文件不超过 32 MiB。系统按「新增 · 草稿合同 · 主合同」登记这一份文件，不会替换已有主合同。
        </p>
        {file === null ? null : (
          <p className="home-intake-selected">
            已选择 {file.name}（{formatBytes(file.size)}）
          </p>
        )}
        {fileError === null ? null : (
          <p className="home-inline-error" id={fileErrorId} role="alert">
            {fileError}
          </p>
        )}
        {formError === null ? null : (
          <p className="home-inline-error" role="alert">
            {formError}
          </p>
        )}
        <div className="home-intake-actions">
          <Button type="primary" htmlType="submit" disabled={busy !== null}>
            上传主合同并建立案件
          </Button>
          <Button type="text" href="/cases">
            前往案件中心完整建案
          </Button>
        </div>
        <p className="home-intake-note">
          上传成功后进入案件页；准备清单与审查范围以案件页的服务端结果为准，这里不会自动开始单合同审查。
        </p>
      </form>
    );
  }

  function renderCompleted(continuation: IntakeContinuation) {
    const receipt = continuation.receipt;
    const caseId = receipt.case_id;
    return (
      <div className="home-intake-state">
        <Alert
          type="success"
          showIcon
          title="主合同已上传，案件已建立"
          description={`服务器回执已固定；之后替换材料不会改写这张回执。本机最近记录时间 ${formatDateTime(continuation.updated_at)}（北京时间）。`}
        />
        <dl className="home-intake-receipt" aria-label="主合同上传回执">
          <div>
            <dt>操作编号</dt>
            <dd>
              {receipt.operation_id === null ? (
                "未报告"
              ) : (
                <code>{receipt.operation_id}</code>
              )}
            </dd>
          </div>
          <div>
            <dt>案件编号</dt>
            <dd>
              {receipt.case_id === null ? (
                "未报告"
              ) : (
                <code>{receipt.case_id}</code>
              )}
            </dd>
          </div>
          <div>
            <dt>文档编号</dt>
            <dd>
              {receipt.document_id === null ? (
                "未报告"
              ) : (
                <code>{receipt.document_id}</code>
              )}
            </dd>
          </div>
          <div>
            <dt>材料版本</dt>
            <dd>
              {receipt.material_id === null ? (
                "未报告"
              ) : (
                <code>{receipt.material_id}</code>
              )}
              {receipt.material_version === null
                ? ""
                : ` · v${receipt.material_version}`}
            </dd>
          </div>
        </dl>
        <div className="home-intake-actions">
          {caseId === null ? null : (
            <Button type="primary" href={caseHref(caseId, null)}>
              打开案件与准备清单
            </Button>
          )}
          <Button
            htmlType="button"
            disabled={busy !== null}
            onClick={() => void refreshState()}
          >
            重新确认回执
          </Button>
          <Button
            type="text"
            htmlType="button"
            disabled={busy !== null}
            onClick={() => setConfirmNew(true)}
          >
            另建一份主合同
          </Button>
          <Button
            type="text"
            htmlType="button"
            disabled={busy !== null}
            onClick={() => void discardLocalRecord()}
          >
            清除本机回执
          </Button>
        </div>
        <p className="home-intake-note">
          清除本机回执只影响这台浏览器，不影响案件和服务器记录。
        </p>
      </div>
    );
  }

  function renderPending(continuation: IntakeContinuation) {
    const receipt = continuation.receipt;
    const caseId = receipt.case_id;
    return (
      <div className="home-intake-state">
        <Alert
          type="warning"
          showIcon
          title="案件已建立，主合同上传尚未完成"
          description={`${
            receipt.error_code === null
              ? "上传服务暂不可用"
              : INTAKE_ERROR_LABELS[receipt.error_code] ?? "主合同上传没有完成"
          }。案件与操作编号已经保留，继续上传不会重复建案。`}
        />
        <p>
          请重新选择最初提交的主合同文件。系统会先核对 SHA-256，再使用同一幂等键重试；不会替换已有主合同。
        </p>
        <form className="home-intake-form" onSubmit={submitResume} noValidate>
          <div className="home-intake-field">
            <label htmlFor={resumeInputId}>重新选择主合同文件</label>
            <input
              id={resumeInputId}
              type="file"
              onChange={(event) => {
                setResumeFile(event.target.files?.[0] ?? null);
                setResumeError(null);
              }}
            />
          </div>
          {resumeError === null ? null : (
            <p className="home-inline-error" role="alert">
              {resumeError}
            </p>
          )}
          <div className="home-intake-actions">
            <Button
              type="primary"
              htmlType="submit"
              disabled={busy !== null || !canResumeCurrent}
            >
              继续上传同一文件
            </Button>
            <Button
              htmlType="button"
              disabled={busy !== null}
              onClick={() => void refreshState()}
            >
              刷新服务器状态
            </Button>
            {caseId === null ? null : (
              <Button type="text" href={caseHref(caseId, null)}>
                先打开已建案件
              </Button>
            )}
            <Button
              type="text"
              htmlType="button"
              disabled={busy !== null}
              onClick={() => setConfirmNew(true)}
            >
              另建一份主合同
            </Button>
          </div>
        </form>
        <p className="home-intake-note">
          主合同上传完成前，案件不会进入单合同审查；准备清单以案件页的服务端结果为准。
        </p>
      </div>
    );
  }

  function renderBlockedPending(continuation: IntakeContinuation) {
    const detail = activeAccess?.detail;
    const reasonCode = detail?.reason_code ?? "";
    const archived =
      selectedContinuation?.archived === true ||
      reasonCode === "archived" ||
      continuation.receipt.error_code === "intake_archived";
    const completed = detail?.state === "completed" || reasonCode === "completed";
    const caseId = detail?.case_id ?? continuation.receipt.case_id;
    const title = archived
      ? "该办理已归档"
      : completed
        ? "该办理已完成"
        : "当前不能继续此办理";
    const description = archived
      ? CONTINUATION_REASON_LABELS.archived
      : completed
        ? CONTINUATION_REASON_LABELS.completed
        : CONTINUATION_REASON_LABELS[reasonCode] ??
          "尚未取得服务器续办授权。请从“我的未完成办理”重新选择，或查看原案件。";
    return (
      <div className="home-intake-state">
        <Alert
          type={archived ? "info" : completed ? "success" : "warning"}
          showIcon
          title={title}
          description={description}
        />
        <div className="home-intake-actions">
          {caseId === null ? null : (
            <Button type="primary" href={caseHref(caseId, null)}>
              打开原案件
            </Button>
          )}
          {!archived && !completed ? (
            <Button
              htmlType="button"
              disabled={busy !== null}
              onClick={() => void refreshState()}
            >
              重新检查续办权限
            </Button>
          ) : null}
        </div>
      </div>
    );
  }

  function renderLost(continuation: IntakeContinuation) {
    return (
      <div className="home-intake-state">
        <Alert
          type="warning"
          showIcon
          title="还没有收到服务器确认"
          description="第一次响应可能丢失。系统会沿用同一个幂等键重新提交；如果服务器已经建案，会返回原案件，不会重复建案。"
        />
        <p>
          已保存的请求：{continuation.request.title}，文件{" "}
          {continuation.request.file_name}（
          {formatBytes(continuation.request.file_size)}）。
        </p>
        <form className="home-intake-form" onSubmit={submitResume} noValidate>
          <div className="home-intake-field">
            <label htmlFor={resumeInputId}>重新选择同一份主合同文件</label>
            <input
              id={resumeInputId}
              type="file"
              onChange={(event) => {
                setResumeFile(event.target.files?.[0] ?? null);
                setResumeError(null);
              }}
            />
          </div>
          {resumeError === null ? null : (
            <p className="home-inline-error" role="alert">
              {resumeError}
            </p>
          )}
          <div className="home-intake-actions">
            <Button
              type="primary"
              htmlType="submit"
              disabled={busy !== null || !canCreate}
            >
              重新提交同一主合同
            </Button>
            <Button
              htmlType="button"
              disabled={busy !== null}
              onClick={() => void refreshState()}
            >
              查询服务器状态
            </Button>
            <Button
              type="text"
              htmlType="button"
              disabled={busy !== null}
              onClick={() => setConfirmNew(true)}
            >
              另建一份主合同
            </Button>
          </div>
        </form>
      </div>
    );
  }

  function renderContinuation() {
    if (draft === null) return null;
    return (
      <div className="home-intake-body">
        {renderDraftIdentity(draft)}
        {busy === "recovering" ? (
          <p className="home-intake-loading" role="status">
            正在向服务器确认该操作的当前状态…
          </p>
        ) : null}
        {notice === null ? null : (
          <Alert type="info" showIcon title={notice} />
        )}
        {renderProblem()}
        {conflict !== null && !confirmNew ? renderConflict() : null}
        {confirmNew ? renderConfirmNew() : null}
        {busy === "hashing" || busy === "uploading" ? renderProgress() : null}
        {conflict === null &&
        !confirmNew &&
        (busy === null || busy === "recovering") ? (
          draft.receipt.state === "completed"
            ? renderCompleted(draft)
            : draft.receipt.state === "upload_pending"
              ? activeAccess?.archived === true ||
                activeAccess?.detail.reason_code === "archived" ||
                activeAccess?.detail.reason_code === "completed" ||
                activeAccess?.detail.state === "completed" ||
                draft.receipt.error_code === "intake_archived" ||
                !canResumeCurrent
                ? renderBlockedPending(draft)
                : renderPending(draft)
              : renderLost(draft)
        ) : null}
        <p className="home-intake-local">
          续办记录按当前身份、租户和工作区保存在本机，只含幂等键、案件元数据、文件大小与
          SHA-256；不含文件内容、Cookie 或登录凭据。
        </p>
      </div>
    );
  }

  function renderConflict() {
    if (conflict === null) return null;
    const client = conflict.origin === "client";
    return (
      <div className="home-intake-conflict">
        <Alert
          type="error"
          showIcon
          title={
            client
              ? "所选文件与最初提交的主合同不一致"
              : "服务器按幂等键拒绝了这次提交"
          }
          description={
            <>
              <p>{conflict.message}</p>
              <p>
                同一幂等键不能绑定不同的文件或案件信息。系统不会替换已有主合同，也不会再建一个案件。
              </p>
              {conflict.existing === null ? (
                <p>{client ? "返回当前记录并重新选择原文件，即可继续这项办理。" : "暂时无法读取该办理的服务器状态；请重新查询后再继续。"}</p>
              ) : (
                <p>
                  服务器已有该幂等键对应的操作：案件{" "}
                  <code>{conflict.existing.case_id}</code>，状态{" "}
                  {conflict.existing.state === "completed"
                    ? "已完成"
                    : "等待上传"}。
                </p>
              )}
            </>
          }
        />
        <div className="home-intake-actions">
          {conflict.existing === null ? null : (
            <Button href={caseHref(conflict.existing.case_id, null)}>
              打开已有案件
            </Button>
          )}
          <Button
            htmlType="button"
            disabled={busy !== null}
            onClick={() => void refreshState()}
          >
            查询服务器状态
          </Button>
          <Button
            type="text"
            htmlType="button"
            disabled={busy !== null}
            onClick={() => setConfirmNew(true)}
          >
            另建一份主合同
          </Button>
          <Button
            type="text"
            htmlType="button"
            onClick={() => setConflict(null)}
          >
            返回当前记录
          </Button>
        </div>
      </div>
    );
  }

  function renderConfirmNew() {
    const state = draft?.receipt.state ?? null;
    return (
      <div className="home-intake-conflict">
        <Alert
          type="warning"
          showIcon
          title="另建一份主合同会清除本机续办记录"
          description={
            state === "upload_pending"
              ? "该操作仍在等待主合同上传。清除本机记录后，将无法在这台浏览器继续同一个案件；案件和服务器记录仍在，但主合同仍缺失。已建立的案件和服务器回执不会被删除。"
              : state === null
                ? "还没有确认服务器是否已经建案。清除本机记录后，将无法在这台浏览器按同一幂等键恢复该操作。已建立的案件和服务器回执不会被删除。"
                : "已建立的案件和服务器回执不会被删除。系统会生成新的幂等键，新建一个独立案件并登记新的主合同；不会替换任何现有主合同。"
          }
        />
        <div className="home-intake-actions">
          <Button
            type="primary"
            htmlType="button"
            disabled={busy !== null}
            onClick={() => void discardLocalRecord()}
          >
            确认另建主合同
          </Button>
          <Button
            type="text"
            htmlType="button"
            onClick={() => setConfirmNew(false)}
          >
            取消
          </Button>
        </div>
      </div>
    );
  }

  return (
    <section
      className="home-panel home-intake"
      aria-labelledby={headingId}
    >
      <header className="home-panel-head">
        <div className="home-section-title">
          <span aria-hidden="true" />
          <h2 id={headingId}>登记主合同并建案</h2>
        </div>
        <span className="home-panel-meta">一次一份主合同</span>
      </header>
      <p className="home-intake-summary">
        上传一份主合同即可建立案件，服务器固定按「新增 · 草稿合同 ·
        主合同」登记。需要登记多份材料、主体或关联案件时，请前往
        <Link to="/cases">案件中心完整建案</Link>。
      </p>
      {hydrated ? null : (
        <p className="home-intake-loading" role="status">
          正在检查本机的续办记录…
        </p>
      )}
      {hydrated && actor === null ? (
        <p className="home-intake-loading" role="status">
          正在确认当前身份…
        </p>
      ) : null}
      {hydrated && actor !== null && draft === null ? (
        <div className="home-intake-body">
          {renderProblem()}
          {busy === "hashing" || busy === "uploading"
            ? renderProgress()
            : renderNewForm()}
        </div>
      ) : null}
      {hydrated && actor !== null ? renderContinuation() : null}
    </section>
  );
}
