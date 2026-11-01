import { Alert, Button, Empty, Input, InputNumber, Modal, Select, Table, Tag } from "antd";
import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, errorMessage } from "../api/http";
import {
  CONTRACT_LEDGER_COLUMNS,
  CONTRACT_LEDGER_DEFAULT_COLUMNS,
  createContractDraft,
  createContractExport,
  downloadContractExport,
  emptyDraft,
  getContract,
  importLegacyContract,
  isMaskedRestricted,
  listMyContractDrafts,
  replaceContractDraft,
  type ContractDetail,
  type ContractDraftSummary,
  type ContractLedgerColumn,
  type ContractLedgerFilter,
  type ContractLedgerItem,
  type DraftContent,
  type DraftFunding,
  type DraftItem,
  type MaterialRef,
} from "../api/contracts";
import {
  queryKeys,
  useCaseDetail,
  useCases,
  useContractLedger,
  useContractSavedViews,
  useCreateContractSavedView,
  useDeleteContractSavedView,
  useReplaceContractSavedView,
  useSession,
} from "../api/queries";
import {
  clearPendingMutationKey,
  idempotencyActorScope,
  pendingMutationKey,
} from "../api/idempotency";
import type { Actor } from "../api/types";
import { PageStatus } from "../components/page-status";
import { ContractReferencesPanel } from "../components/contract-references-panel";
import { ContractSigningPanel } from "../components/contract-signing-panel";
import { ContractFulfillmentPanel } from "../components/contract-fulfillment-panel";
import { ContractPaymentsPanel } from "../components/contract-payments-panel";
import { formatDateTime } from "../domain/review";
import "./contracts-page.css";

const DRAFTING_PARTIES = [
  { value: "buyer", label: "采购方拟定" },
  { value: "supplier", label: "供应方拟定" },
  { value: "joint", label: "双方共同拟定" },
  { value: "other", label: "其他（备注说明）" },
];

const SETTLEMENT_MODES = [
  { value: "fixed_total_price", label: "固定总价" },
  { value: "framework", label: "框架协议" },
  { value: "by_quantity", label: "按量结算" },
];

function newId(): string {
  return crypto.randomUUID();
}

function optionalText(value: string): string | undefined {
  const trimmed = value.trim();
  return trimmed === "" ? undefined : trimmed;
}

function actorScope(actor: Actor | null): string {
  return actor === null
    ? ""
    : [actor.audience, actor.tenant_id, actor.org_id, actor.id].join("\u0000");
}

function fullActorScope(actor: Actor | null): string {
  return actor === null
    ? ""
    : [actor.audience, actor.tenant_id, actor.org_id, actor.id, actor.role].join("\u0000");
}

function emptyItem(): DraftItem {
  return { item_id: newId() };
}

function emptyFunding(): DraftFunding {
  return {};
}

function amountLabel(amount: string | null, currency: string | null): string {
  if (amount === null || currency === null) return "金额未知";
  return `${currency} ${amount}`;
}

const CONTRACT_STATE_OPTIONS = [
  { value: "draft", label: "草稿" },
  { value: "ready_to_sign", label: "待签署" },
  { value: "registered", label: "已登记" },
  { value: "performing", label: "履约中" },
  { value: "closed", label: "已关闭" },
  { value: "terminated", label: "已终止" },
];

const WORKFLOW_STATE_OPTIONS = [
  { value: "not_started", label: "未开始会签" },
  { value: "in_review", label: "会签中" },
];

const AUTHORITY_MODE_OPTIONS = [
  { value: "native", label: "本地登记" },
  { value: "external", label: "外部来源" },
];

const VALIDITY_STATE_OPTIONS = [
  { value: "active", label: "有效" },
  { value: "voided", label: "已作废" },
];

const LEDGER_COLUMN_OPTIONS = CONTRACT_LEDGER_COLUMNS.map((column) => ({
  value: column.key,
  label: column.label,
}));

function stateLabel(state: string): string {
  return CONTRACT_STATE_OPTIONS.find((option) => option.value === state)?.label ?? state;
}

function workflowStateLabel(state: string): string {
  return WORKFLOW_STATE_OPTIONS.find((option) => option.value === state)?.label ?? state;
}

function ledgerCellValue(item: ContractLedgerItem, column: ContractLedgerColumn): string {
  switch (column) {
    case "contract_number":
      return item.number ?? "—";
    case "contract_name":
      return item.name || "未命名合同";
    case "authority_mode":
      return item.authority_mode === "native" ? "本地登记" : "外部来源";
    case "supplier_name":
      return item.supplier_name ?? "—";
    case "amount":
      return item.amount ?? "未知";
    case "currency":
      return item.currency ?? "—";
    case "department_id":
      return item.department_id ?? "—";
    case "project_type":
      return item.project_type ?? "—";
    case "procurement_method":
      return item.procurement_method ?? "—";
    case "contract_state":
      return stateLabel(item.contract_state);
    case "workflow_state":
      return workflowStateLabel(item.workflow_state);
    case "signing_date":
      return item.signing_date ?? "—";
    case "source_system":
      return item.source_system ?? "—";
    case "created_at":
      return formatDateTime(item.created_at);
    case "updated_at":
      return formatDateTime(item.updated_at);
  }
}

function ledgerDateInput(value: string | undefined): string {
  return value === undefined ? "" : value.slice(0, 10);
}

function ledgerDayToIso(day: string): string | undefined {
  return day === "" ? undefined : new Date(`${day}T00:00:00.000Z`).toISOString();
}

function saveDownload(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}

export function ContractsPage() {
  const queryClient = useQueryClient();
  const session = useSession();
  const actor = session.data ?? null;
  const scope = actorScope(actor);
  const drafts = useQuery({
    queryKey: queryKeys.contractDrafts(scope),
    queryFn: ({ signal }) => listMyContractDrafts(signal),
    enabled: actor !== null,
  });
  const [ledgerForm, setLedgerForm] = useState<ContractLedgerFilter>({});
  const [ledgerFilter, setLedgerFilter] = useState<ContractLedgerFilter>({});
  const [ledgerColumns, setLedgerColumns] = useState<ContractLedgerColumn[]>([
    ...CONTRACT_LEDGER_DEFAULT_COLUMNS,
  ]);
  const [ledgerAdvanced, setLedgerAdvanced] = useState(false);
  const [selectedViewId, setSelectedViewId] = useState<string | null>(null);
  const [selectedViewVersion, setSelectedViewVersion] = useState<number | null>(null);
  const [viewName, setViewName] = useState("");
  const [viewMessage, setViewMessage] = useState<string | null>(null);
  const [viewSaving, setViewSaving] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [exportMessage, setExportMessage] = useState<string | null>(null);
  const ledger = useContractLedger(ledgerFilter, scope, actor !== null);
  const savedViews = useContractSavedViews(scope, actor !== null);
  const createSavedView = useCreateContractSavedView();
  const replaceSavedView = useReplaceContractSavedView();
  const deleteSavedView = useDeleteContractSavedView();

  const [contractID, setContractID] = useState<string | null>(null);
  const editorIdentityRef = useRef({ contractID: null as string | null, actorScope: "" });
  const currentActorScope = fullActorScope(actor);
  editorIdentityRef.current = { contractID, actorScope: currentActorScope };
  const [contractDetail, setContractDetail] = useState<ContractDetail | null>(null);
  const [rowVersion, setRowVersion] = useState<number | null>(null);
  const [draft, setDraft] = useState<DraftContent>(emptyDraft);
  const [loaded, setLoaded] = useState<DraftContent>(emptyDraft);
  const [allowed, setAllowed] = useState<string[]>([]);
  const [warnings, setWarnings] = useState<string[]>([]);
  const [sourceLabel, setSourceLabel] = useState<string | null>(null);
  const [message, setMessage] = useState<{ kind: "saved" | "error" | "conflict"; text: string } | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [importOpen, setImportOpen] = useState(false);

  const dirty = useMemo(
    () => JSON.stringify(draft) !== JSON.stringify(loaded),
    [draft, loaded],
  );
  const isEditableDraft = contractID === null || contractDetail?.summary.contract_state === "draft";
  const canSave = contractID === null || (isEditableDraft && allowed.includes("replace_draft"));
  const readOnly = contractID !== null && (!isEditableDraft || !allowed.includes("replace_draft"));

  useEffect(() => {
    if (!dirty) return;
    function warn(event: BeforeUnloadEvent) {
      event.preventDefault();
      event.returnValue = "";
    }
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  function resetEditor(detail: ContractDetail | null) {
    if (detail === null) {
      setContractDetail(null);
      setContractID(null);
      setRowVersion(null);
      setDraft(emptyDraft());
      setLoaded(emptyDraft());
      setAllowed([]);
      setWarnings([]);
      setSourceLabel(null);
      return;
    }
    const next = detail.draft ?? emptyDraft();
    setContractDetail(detail);
    setContractID(detail.summary.contract_id);
    setRowVersion(detail.summary.row_version);
    setDraft(next);
    setLoaded(next);
    setAllowed(detail.allowed_commands);
    setWarnings(detail.warnings);
    setSourceLabel(
      detail.source_ref === undefined
        ? null
        : `${detail.source_ref.system} · ${detail.source_ref.id} · 版本 ${detail.source_ref.source_version}`,
    );
  }

  async function refreshPaymentContract(expectedContractID: string, expectedActorScope: string) {
    let latest: ContractDetail;
    try {
      latest = await getContract(expectedContractID);
    } catch (caught) {
      const current = editorIdentityRef.current;
      if (current.contractID === expectedContractID && current.actorScope === expectedActorScope)
        resetEditor(null);
      throw caught;
    }
    const current = editorIdentityRef.current;
    if (current.contractID === expectedContractID && current.actorScope === expectedActorScope)
      resetEditor(latest);
  }

  async function openDraft(id: string) {
    await openContractDetail(id);
  }

  async function openContractDetail(id: string) {
    if (dirty) {
      const discard = await confirmDiscard();
      if (!discard) return;
    }
    setLoading(true);
    setMessage(null);
    try {
      resetEditor(await getContract(id));
    } catch (error) {
      setMessage({ kind: "error", text: errorMessage(error) });
    } finally {
      setLoading(false);
    }
  }

  function confirmDiscard(): Promise<boolean> {
    // The project's TS lib target predates Promise.withResolvers, so the
    // executor form is the only available construction here.
    return new Promise((resolve) => {
      Modal.confirm({
        title: "当前草稿有未保存的修改",
        content: "切换后会丢失本页尚未保存的内容。",
        okText: "放弃并切换",
        cancelText: "留在本页",
        onOk: () => resolve(true),
        onCancel: () => resolve(false),
      });
    });
  }

  async function startNew() {
    if (dirty) {
      const discard = await confirmDiscard();
      if (!discard) return;
    }
    resetEditor(null);
    setMessage(null);
  }

  function update(patch: Partial<DraftContent["contract"]>) {
    setDraft((current) => ({ ...current, contract: { ...current.contract, ...patch } }));
  }

  function updateSupplier(patch: NonNullable<DraftContent["supplier"]>) {
    setDraft((current) => ({ ...current, supplier: { ...current.supplier, ...patch } }));
  }

  async function save() {
    if (actor === null || saving || !canSave) return;
    setSaving(true);
    setMessage(null);
    const operation = contractID === null ? "clm-create-draft" : "clm-replace-draft";
    const target = contractID ?? "new";
    try {
      const pending = await pendingMutationKey(
        operation,
        idempotencyActorScope(actor),
        target,
        { rowVersion, draft },
      );
      try {
        const detail =
          contractID === null
            ? await createContractDraft(draft, pending.key)
            : await replaceContractDraft(contractID, rowVersion ?? 1, draft, pending.key);
        clearPendingMutationKey(pending.storageKey);
        resetEditor(detail);
        setMessage({ kind: "saved", text: `已保存于 ${new Date().toLocaleTimeString()}` });
        void queryClient.invalidateQueries({ queryKey: queryKeys.contractDraftsPrefix });
      } catch (error) {
        if (error instanceof ApiError && error.status === 409) {
          setMessage({
            kind: "conflict",
            text: "服务端已有更新的内容。请先刷新查看最新版本；本页输入仍保留，未覆盖他人修改。",
          });
        } else {
          setMessage({ kind: "error", text: errorMessage(error) });
        }
      }
    } finally {
      setSaving(false);
    }
  }

  function updateLedgerForm(key: keyof ContractLedgerFilter, value: string) {
    setLedgerForm((current) => ({
      ...current,
      [key]: value.trim() === "" ? undefined : value,
    }));
  }

  function applyLedgerForm() {
    setLedgerFilter({ ...ledgerForm });
    setSelectedViewId(null);
    setSelectedViewVersion(null);
    setViewMessage(null);
  }

  function resetLedgerForm() {
    setLedgerForm({});
    setLedgerFilter({});
    setLedgerColumns([...CONTRACT_LEDGER_DEFAULT_COLUMNS]);
    setSelectedViewId(null);
    setSelectedViewVersion(null);
    setViewName("");
    setViewMessage(null);
    setExportMessage(null);
  }

  function applySavedView(viewId: string) {
    const view = (savedViews.data ?? []).find((item) => item.view_id === viewId);
    if (view === undefined) return;
    setSelectedViewId(view.view_id);
    setSelectedViewVersion(view.row_version);
    setViewName(view.name);
    setLedgerForm(view.filter);
    setLedgerFilter(view.filter);
    setLedgerColumns(
      view.columns.length === 0 ? [...CONTRACT_LEDGER_DEFAULT_COLUMNS] : view.columns,
    );
    setViewMessage(null);
  }

  async function saveLedgerView() {
    const name = viewName.trim();
    if (name === "") {
      setViewMessage("请先填写视图名称");
      return;
    }
    setViewSaving(true);
    setViewMessage(null);
    try {
      if (selectedViewId !== null && selectedViewVersion !== null) {
        const updated = await replaceSavedView.mutateAsync({
          viewId: selectedViewId,
          expectedRowVersion: selectedViewVersion,
          name,
          filter: ledgerFilter,
          columns: ledgerColumns,
        });
        setSelectedViewVersion(updated.row_version);
        setLedgerColumns(
          updated.columns.length === 0 ? [...CONTRACT_LEDGER_DEFAULT_COLUMNS] : updated.columns,
        );
        setViewMessage("视图已更新");
      } else {
        const created = await createSavedView.mutateAsync({
          name,
          filter: ledgerFilter,
          columns: ledgerColumns,
        });
        setSelectedViewId(created.view_id);
        setSelectedViewVersion(created.row_version);
        setLedgerColumns(
          created.columns.length === 0 ? [...CONTRACT_LEDGER_DEFAULT_COLUMNS] : created.columns,
        );
        setViewMessage("视图已保存");
      }
    } catch (error) {
      setViewMessage(errorMessage(error));
    } finally {
      setViewSaving(false);
    }
  }

  function confirmDeleteLedgerView(): Promise<boolean> {
    return new Promise((resolve) => {
      Modal.confirm({
        title: "删除这个台账视图？",
        content: "只删除个人筛选与列设置，不会删除任何合同。",
        okText: "删除视图",
        cancelText: "取消",
        onOk: () => resolve(true),
        onCancel: () => resolve(false),
      });
    });
  }

  async function deleteLedgerView() {
    if (selectedViewId === null || selectedViewVersion === null) return;
    if (!(await confirmDeleteLedgerView())) return;
    try {
      await deleteSavedView.mutateAsync({
        viewId: selectedViewId,
        expectedRowVersion: selectedViewVersion,
      });
      setSelectedViewId(null);
      setSelectedViewVersion(null);
      setViewName("");
      setViewMessage("视图已删除");
    } catch (error) {
      setViewMessage(errorMessage(error));
    }
  }

  async function exportLedger() {
    if (actor === null || exporting) return;
    setExporting(true);
    setExportMessage(null);
    try {
      const payload = { filter: ledgerFilter, columns: ledgerColumns };
      const pending = await pendingMutationKey(
        "clm-contract-export",
        idempotencyActorScope(actor),
        "ledger",
        payload,
      );
      const created = await createContractExport(
        ledgerFilter,
        ledgerColumns,
        pending.key,
      );
      const blob = await downloadContractExport(created.export_id);
      clearPendingMutationKey(pending.storageKey);
      saveDownload(blob, `contracts-${created.export_id}.csv`);
      setExportMessage(
        created.replayed
          ? `已重放同一导出，共 ${created.row_count} 行`
          : `已导出 ${created.row_count} 行`,
      );
    } catch (error) {
      setExportMessage(errorMessage(error));
    } finally {
      setExporting(false);
    }
  }

  const draftsData = drafts.data ?? [];
  const ledgerItems = useMemo(
    () => ledger.data?.pages.flatMap((page) => page.items) ?? [],
    [ledger.data],
  );
  const ledgerTableColumns = useMemo(
    () =>
      [
        ...ledgerColumns.map((column) => ({
          title: LEDGER_COLUMN_OPTIONS.find((option) => option.value === column)?.label ?? column,
          dataIndex: column,
          key: column,
          render: (_: unknown, item: ContractLedgerItem) => ledgerCellValue(item, column),
        })),
        {
          title: "操作",
          key: "contract_detail_action",
          fixed: "right" as const,
          render: (_: unknown, item: ContractLedgerItem) => (
            <Button type="link" disabled={loading} onClick={() => void openContractDetail(item.contract_id)}>
              查看详情
            </Button>
          ),
        },
      ],
    [ledgerColumns, loading, dirty],
  );
  const activeLedgerFilters = Object.values(ledgerFilter).filter(
    (value) => value !== undefined && value !== "",
  ).length;

  return (
    <section className="page contracts-page">
      <header className="page-head">
        <div>
          <h1>合同登记</h1>
          <p>
            草稿可以暂不完整；保存后可随时恢复继续填写。受限账户字段加密保存，只有具备管理授权者可见明文。
          </p>
        </div>
        <div className="contracts-page-actions">
          <Button type="text" onClick={() => setImportOpen(true)} disabled={actor === null}>
            导入已签存量合同
          </Button>
          <Button type="primary" onClick={() => void startNew()} disabled={actor === null}>
            新建合同草稿
          </Button>
        </div>
      </header>

      <div className="contracts-ledger-summary" aria-label="合同台账摘要">
        <span>生效筛选</span>
        <strong>{activeLedgerFilters}</strong>
        <span>已加载</span>
        <strong>{ledgerItems.length}</strong>
        <span>还有更多</span>
        <strong>{ledger.hasNextPage ? "是" : "否"}</strong>
      </div>

      <section className="contracts-ledger" aria-label="合同台账">
        <div className="contracts-ledger-head">
          <div>
            <h2>合同台账</h2>
            <p>
              默认条件覆盖常用查询，高级条件只收窄结果；每行仍按当前案件授权实时判断。
            </p>
          </div>
          <div className="contracts-ledger-actions">
            <Button onClick={() => setLedgerAdvanced((current) => !current)}>
              {ledgerAdvanced ? "收起高级条件" : "高级条件"}
            </Button>
            <Button
              type="primary"
              onClick={() => void exportLedger()}
              loading={exporting}
              disabled={actor === null || ledgerColumns.length === 0}
            >
              导出 CSV
            </Button>
          </div>
        </div>

        <div className="contracts-ledger-filters">
          <label className="field">
            合同名称或编号
            <Input
              value={ledgerForm.text ?? ""}
              maxLength={200}
              placeholder="名称、服务端编号或内容编号"
              onChange={(event) => updateLedgerForm("text", event.target.value)}
              onPressEnter={applyLedgerForm}
            />
          </label>
          <label className="field">
            供应商
            <Input
              value={ledgerForm.supplier_name ?? ""}
              maxLength={200}
              placeholder="供应商名称子串"
              onChange={(event) => updateLedgerForm("supplier_name", event.target.value)}
              onPressEnter={applyLedgerForm}
            />
          </label>
          <label className="field">
            合同状态
            <Select
              value={ledgerForm.contract_state ?? undefined}
              allowClear
              placeholder="全部状态"
              options={CONTRACT_STATE_OPTIONS}
              onChange={(value) => updateLedgerForm("contract_state", value ?? "")}
            />
          </label>
          <label className="field">
            申请部门
            <Input
              value={ledgerForm.department_id ?? ""}
              maxLength={100}
              onChange={(event) => updateLedgerForm("department_id", event.target.value)}
              onPressEnter={applyLedgerForm}
            />
          </label>
          <label className="field">
            会签状态
            <Select
              value={ledgerForm.workflow_state ?? undefined}
              allowClear
              placeholder="全部会签状态"
              options={WORKFLOW_STATE_OPTIONS}
              onChange={(value) => updateLedgerForm("workflow_state", value ?? "")}
            />
          </label>
          <label className="field">
            权威模式
            <Select
              value={ledgerForm.authority_mode ?? undefined}
              allowClear
              placeholder="全部模式"
              options={AUTHORITY_MODE_OPTIONS}
              onChange={(value) => updateLedgerForm("authority_mode", value ?? "")}
            />
          </label>
        </div>

        {ledgerAdvanced ? (
          <div className="contracts-ledger-advanced">
            <label className="field">
              有效性
              <Select
                value={ledgerForm.validity_state ?? undefined}
                allowClear
                placeholder="全部"
                options={VALIDITY_STATE_OPTIONS}
                onChange={(value) => updateLedgerForm("validity_state", value ?? "")}
              />
            </label>
            <label className="field">
              项目类型
              <Input
                value={ledgerForm.project_type ?? ""}
                maxLength={100}
                onChange={(event) => updateLedgerForm("project_type", event.target.value)}
              />
            </label>
            <label className="field">
              采购方式
              <Input
                value={ledgerForm.procurement_method ?? ""}
                maxLength={100}
                onChange={(event) => updateLedgerForm("procurement_method", event.target.value)}
              />
            </label>
            <label className="field">
              金额下限
              <Input
                value={ledgerForm.amount_min ?? ""}
                placeholder="0.00"
                inputMode="decimal"
                onChange={(event) => updateLedgerForm("amount_min", event.target.value)}
              />
            </label>
            <label className="field">
              金额上限
              <Input
                value={ledgerForm.amount_max ?? ""}
                placeholder="0.00"
                inputMode="decimal"
                onChange={(event) => updateLedgerForm("amount_max", event.target.value)}
              />
            </label>
            <label className="field">
              签订日期从
              <Input
                type="date"
                value={ledgerDateInput(ledgerForm.signing_date_from)}
                onChange={(event) => updateLedgerForm("signing_date_from", event.target.value)}
              />
            </label>
            <label className="field">
              签订日期至
              <Input
                type="date"
                value={ledgerDateInput(ledgerForm.signing_date_to)}
                onChange={(event) => updateLedgerForm("signing_date_to", event.target.value)}
              />
            </label>
            <label className="field">
              创建日期从
              <Input
                type="date"
                value={ledgerDateInput(ledgerForm.created_from)}
                onChange={(event) =>
                  updateLedgerForm("created_from", ledgerDayToIso(event.target.value) ?? "")
                }
              />
            </label>
            <label className="field">
              创建日期至
              <Input
                type="date"
                value={ledgerDateInput(ledgerForm.created_to)}
                onChange={(event) =>
                  updateLedgerForm("created_to", ledgerDayToIso(event.target.value) ?? "")
                }
              />
            </label>
          </div>
        ) : null}

        <div className="contracts-ledger-filter-actions">
          <Button type="primary" onClick={applyLedgerForm} disabled={actor === null}>
            查询
          </Button>
          <Button onClick={resetLedgerForm} disabled={actor === null}>
            重置
          </Button>
        </div>

        <div className="contracts-ledger-viewbar">
          <Select
            value={selectedViewId ?? undefined}
            allowClear
            placeholder="应用已保存视图"
            options={(savedViews.data ?? []).map((view) => ({
              value: view.view_id,
              label: view.name,
            }))}
            onChange={(value) => {
              if (typeof value === "string") applySavedView(value);
              else {
                setSelectedViewId(null);
                setSelectedViewVersion(null);
                setViewName("");
              }
            }}
            className="contracts-ledger-view-select"
          />
          <Input
            value={viewName}
            maxLength={60}
            placeholder="视图名称（最多 60 字）"
            onChange={(event) => setViewName(event.target.value)}
            className="contracts-ledger-view-name"
          />
          <Button
            onClick={() => void saveLedgerView()}
            loading={viewSaving}
            disabled={actor === null}
          >
            {selectedViewId === null ? "保存视图" : "更新视图"}
          </Button>
          <Button
            danger
            onClick={() => void deleteLedgerView()}
            disabled={selectedViewId === null || selectedViewVersion === null}
          >
            删除视图
          </Button>
          <Select
            mode="multiple"
            value={ledgerColumns}
            maxTagCount="responsive"
            options={LEDGER_COLUMN_OPTIONS}
            onChange={(values: ContractLedgerColumn[]) => setLedgerColumns(values)}
            className="contracts-ledger-columns"
            placeholder="选择导出与表格列"
          />
        </div>

        {viewMessage !== null ? (
          <Alert
            type="info"
            showIcon
            message={viewMessage}
            closable
            onClose={() => setViewMessage(null)}
          />
        ) : null}
        {exportMessage !== null ? (
          <Alert
            type="info"
            showIcon
            message={exportMessage}
            closable
            onClose={() => setExportMessage(null)}
          />
        ) : null}

        {ledger.isError ? (
          <Alert
            type="error"
            showIcon
            message="合同台账没有加载"
            description={errorMessage(ledger.error)}
          />
        ) : null}

        <Table<ContractLedgerItem>
          rowKey="contract_id"
          columns={ledgerTableColumns}
          dataSource={ledgerItems}
          loading={ledger.isLoading}
          pagination={false}
          size="small"
          scroll={{ x: "max-content" }}
          locale={{ emptyText: <Empty description="当前条件下没有可见合同" /> }}
        />

        <div className="contracts-ledger-more">
          {ledger.hasNextPage ? (
            <Button
              onClick={() => void ledger.fetchNextPage()}
              loading={ledger.isFetchingNextPage}
            >
              加载更多
            </Button>
          ) : (
            <span>已加载全部可见合同</span>
          )}
        </div>
      </section>

      <div className="contracts-page-summary" aria-label="合同草稿摘要">
        <span>我的草稿</span>
        <strong>{draftsData.length}</strong>
        <span>本页状态</span>
        <strong>{contractID === null ? "新建" : `版本 ${rowVersion ?? "?"}`}</strong>
        <span>未保存修改</span>
        <strong>{dirty ? "有" : "无"}</strong>
      </div>

      <div className="contracts-page-layout">
        <aside className="contracts-page-list" aria-label="我的合同草稿">
          <h2>我的草稿</h2>
          {drafts.isLoading ? (
            <p role="status">正在读取草稿</p>
          ) : drafts.isError ? (
            <Alert type="error" showIcon message="草稿列表没有加载" description={errorMessage(drafts.error)} />
          ) : draftsData.length === 0 ? (
            <Empty description="还没有草稿" />
          ) : (
            <ul>
              {draftsData.map((item: ContractDraftSummary) => (
                <li key={item.contract_id}>
                  <button
                    type="button"
                    className={item.contract_id === contractID ? "is-current" : undefined}
                    onClick={() => void openDraft(item.contract_id)}
                    disabled={loading}
                  >
                    <span className="contracts-page-list-name">{item.name || "未命名合同"}</span>
                    <span className="contracts-page-list-meta">
                      {item.number ?? "尚无编号"} · {amountLabel(item.amount, item.currency)} ·{" "}
                      {stateLabel(item.contract_state)}
                    </span>
                    <span className="contracts-page-list-meta">
                      更新 {formatDateTime(item.updated_at ?? item.created_at)}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </aside>

        <div className="contracts-page-editor">
          {loading ? (
            <PageStatus kind="loading" title="正在读取合同详情" />
          ) : (
            <>
              {sourceLabel !== null ? (
                <Alert
                  type="info"
                  showIcon
                  message="已签存量合同"
                  description={`来源：${sourceLabel}。登记保留历史缺口，不补造会签或签名。`}
                />
              ) : null}
              {warnings.includes("approval_history_unknown") ? (
                <Alert
                  type="warning"
                  showIcon
                  message="未取得原会签记录"
                  description="该合同按存量事实登记；历史审批状态未知，不会被当作已通过会签。"
                />
              ) : null}
              {message !== null ? (
                <Alert
                  type={message.kind === "saved" ? "success" : message.kind === "conflict" ? "warning" : "error"}
                  showIcon
                  message={message.text}
                  action={
                    message.kind === "conflict" && contractID !== null ? (
                      <Button size="small" onClick={() => void openDraft(contractID)}>
                        刷新最新版本
                      </Button>
                    ) : undefined
                  }
                />
              ) : null}
              {readOnly ? (
                <Alert type="info" showIcon message="当前身份只能查看该合同" description="没有合同管理授权，不能保存修改；受限字段显示为掩码。" />
              ) : null}

              <fieldset className="contracts-page-section" disabled={readOnly}>
                <legend>一、合同基本信息</legend>
                <div className="contracts-page-fields">
                  <label className="field">
                    合同名称
                    <Input
                      value={draft.contract.name ?? ""}
                      maxLength={200}
                      placeholder="例如：2026 年传输设备采购合同"
                      onChange={(event) => update({ name: event.target.value })}
                    />
                  </label>
                  <label className="field">
                    合同编号
                    <Input
                      value={draft.contract.number ?? ""}
                      maxLength={100}
                      placeholder="会签阶段可以留空"
                      onChange={(event) => update({ number: optionalText(event.target.value) })}
                    />
                  </label>
                  <label className="field">
                    项目号
                    <Input
                      value={draft.contract.project_number ?? ""}
                      maxLength={100}
                      onChange={(event) => update({ project_number: optionalText(event.target.value) })}
                    />
                  </label>
                  <label className="field">
                    合同分类
                    <Input
                      value={draft.contract.classification ?? ""}
                      maxLength={100}
                      onChange={(event) => update({ classification: optionalText(event.target.value) })}
                    />
                  </label>
                  <label className="field">
                    合同类别
                    <Input
                      value={draft.contract.category ?? ""}
                      maxLength={100}
                      onChange={(event) => update({ category: optionalText(event.target.value) })}
                    />
                  </label>
                  <label className="field">
                    合同等级
                    <Input
                      value={draft.contract.grade ?? ""}
                      maxLength={100}
                      onChange={(event) => update({ grade: optionalText(event.target.value) })}
                    />
                  </label>
                  <label className="field">
                    合同拟定方
                    <Select
                      allowClear
                      value={draft.contract.drafting_party}
                      options={DRAFTING_PARTIES}
                      onChange={(value) => update({ drafting_party: value })}
                      placeholder="选择拟定方"
                    />
                  </label>
                  <label className="field">
                    申请部门
                    <Input
                      value={draft.contract.department_id ?? ""}
                      maxLength={100}
                      onChange={(event) => update({ department_id: optionalText(event.target.value) })}
                    />
                  </label>
                  <label className="field">
                    部门经办人
                    <Input
                      value={draft.contract.handler_user_id ?? ""}
                      maxLength={100}
                      onChange={(event) => update({ handler_user_id: optionalText(event.target.value) })}
                    />
                  </label>
                  <label className="field">
                    经办人联系方式
                    <Input
                      value={draft.contract.handler_contact ?? ""}
                      maxLength={100}
                      onChange={(event) => update({ handler_contact: optionalText(event.target.value) })}
                    />
                  </label>
                  <label className="field field-wide">
                    备注
                    <Input.TextArea
                      value={draft.contract.remark ?? ""}
                      maxLength={4000}
                      autoSize={{ minRows: 2, maxRows: 5 }}
                      onChange={(event) => update({ remark: optionalText(event.target.value) })}
                    />
                  </label>
                </div>
              </fieldset>

              <fieldset className="contracts-page-section" disabled={readOnly}>
                <legend>二、相关方与标的</legend>
                <div className="contracts-page-fields">
                  <label className="field">
                    供应商全称
                    <Input
                      value={draft.supplier?.name ?? ""}
                      maxLength={200}
                      onChange={(event) => updateSupplier({ name: event.target.value })}
                    />
                  </label>
                  <label className="field">
                    社会信用代码
                    <Input
                      value={draft.supplier?.credit_code ?? ""}
                      maxLength={100}
                      onChange={(event) => updateSupplier({ credit_code: event.target.value })}
                    />
                  </label>
                  <label className="field">
                    供应方联系人
                    <Input
                      value={draft.supplier?.contact_name ?? ""}
                      maxLength={100}
                      onChange={(event) => updateSupplier({ contact_name: event.target.value })}
                    />
                  </label>
                  <label className="field">
                    供应方联系电话
                    <Input
                      value={draft.supplier?.contact_phone ?? ""}
                      maxLength={100}
                      onChange={(event) => updateSupplier({ contact_phone: event.target.value })}
                    />
                  </label>
                  <label className="field">
                    收款方
                    <Input
                      value={draft.supplier?.payee_name ?? ""}
                      maxLength={200}
                      readOnly={isMaskedRestricted(draft.supplier?.payee_name)}
                      onChange={(event) => updateSupplier({ payee_name: event.target.value })}
                    />
                  </label>
                  <label className="field">
                    开户行
                    <Input
                      value={draft.supplier?.bank_name ?? ""}
                      maxLength={200}
                      readOnly={isMaskedRestricted(draft.supplier?.bank_name)}
                      onChange={(event) => updateSupplier({ bank_name: event.target.value })}
                    />
                  </label>
                  <label className="field">
                    账号
                    <Input
                      value={draft.supplier?.bank_account ?? ""}
                      maxLength={64}
                      readOnly={isMaskedRestricted(draft.supplier?.bank_account)}
                      onChange={(event) => updateSupplier({ bank_account: event.target.value })}
                    />
                  </label>
                  <label className="field">
                    纳税人识别号
                    <Input
                      value={draft.supplier?.tax_identifier ?? ""}
                      maxLength={100}
                      readOnly={isMaskedRestricted(draft.supplier?.tax_identifier)}
                      onChange={(event) => updateSupplier({ tax_identifier: event.target.value })}
                    />
                  </label>
                  <label className="field">
                    合同金额
                    <Input
                      value={draft.contract.amount ?? ""}
                      placeholder="例如 100000.00"
                      onChange={(event) => update({ amount: optionalText(event.target.value) })}
                    />
                  </label>
                  <label className="field">
                    币种
                    <Input
                      value={draft.contract.currency ?? ""}
                      maxLength={3}
                      placeholder="CNY"
                      onChange={(event) => update({ currency: optionalText(event.target.value.toUpperCase()) })}
                    />
                  </label>
                  <label className="field">
                    结算模式
                    <Select
                      allowClear
                      value={draft.contract.settlement_mode}
                      options={SETTLEMENT_MODES}
                      onChange={(value) => update({ settlement_mode: value })}
                      placeholder="固定总价、框架或按量"
                    />
                  </label>
                  <label className="field">
                    单价是否含税
                    <Select
                      allowClear
                      value={draft.contract.prices_include_tax}
                      options={[
                        { value: true, label: "含税" },
                        { value: false, label: "不含税" },
                      ]}
                      onChange={(value) => update({ prices_include_tax: value })}
                      placeholder="未知时留空"
                    />
                  </label>
                </div>

                <h3>商品或服务明细</h3>
                <Table<DraftItem>
                  size="small"
                  rowKey="item_id"
                  dataSource={draft.items ?? []}
                  pagination={false}
                  locale={{ emptyText: "还没有明细行" }}
                  columns={[
                    {
                      title: "名称",
                      key: "name",
                      render: (_, row, index) => (
                        <Input
                          value={row.name ?? ""}
                          maxLength={200}
                          aria-label={`第 ${index + 1} 行名称`}
                          onChange={(event) => patchItem(index, { name: event.target.value })}
                        />
                      ),
                    },
                    {
                      title: "数量",
                      key: "quantity",
                      width: 130,
                      render: (_, row, index) => (
                        <Input
                          value={row.quantity ?? ""}
                          placeholder="10.000000"
                          aria-label={`第 ${index + 1} 行数量`}
                          onChange={(event) => patchItem(index, { quantity: optionalText(event.target.value) })}
                        />
                      ),
                    },
                    {
                      title: "单位",
                      key: "unit",
                      width: 90,
                      render: (_, row, index) => (
                        <Input
                          value={row.unit ?? ""}
                          maxLength={40}
                          aria-label={`第 ${index + 1} 行单位`}
                          onChange={(event) => patchItem(index, { unit: optionalText(event.target.value) })}
                        />
                      ),
                    },
                    {
                      title: "单价",
                      key: "unit_price",
                      width: 150,
                      render: (_, row, index) => (
                        <Input
                          value={row.unit_price ?? ""}
                          placeholder="10000.0000"
                          aria-label={`第 ${index + 1} 行单价`}
                          onChange={(event) => patchItem(index, { unit_price: optionalText(event.target.value) })}
                        />
                      ),
                    },
                    {
                      title: "明细金额",
                      key: "line_amount",
                      width: 150,
                      render: (_, row, index) => (
                        <Input
                          value={row.line_amount ?? ""}
                          placeholder="100000.00"
                          aria-label={`第 ${index + 1} 行明细金额`}
                          onChange={(event) => patchItem(index, { line_amount: optionalText(event.target.value) })}
                        />
                      ),
                    },
                    {
                      title: "",
                      key: "remove",
                      width: 70,
                      render: (_, row) => (
                        <Button
                          type="link"
                          size="small"
                          aria-label={`删除第 ${(draft.items ?? []).indexOf(row) + 1} 行`}
                          onClick={() => removeItem(row.item_id)}
                        >
                          删除
                        </Button>
                      ),
                    },
                  ]}
                />
                <Button
                  type="dashed"
                  size="small"
                  onClick={() => setDraft((current) => ({ ...current, items: [...(current.items ?? []), emptyItem()] }))}
                >
                  添加明细行
                </Button>
              </fieldset>

              <fieldset className="contracts-page-section" disabled={readOnly}>
                <legend>三、资金与付款计划</legend>
                <h3>资金来源</h3>
                {(draft.funding ?? []).length === 0 ? (
                  <p className="meta">还没有资金来源。固定总价合同在提交时必须与合同金额一致。</p>
                ) : null}
                <div className="contracts-page-rows">
                  {(draft.funding ?? []).map((row, index) => (
                    <div className="contracts-page-row" key={`funding-${index}`}>
                      <Input
                        value={row.nature ?? ""}
                        placeholder="资金性质"
                        aria-label={`第 ${index + 1} 项资金性质`}
                        onChange={(event) => patchFunding(index, { nature: optionalText(event.target.value) })}
                      />
                      <Input
                        value={row.channel ?? ""}
                        placeholder="支付渠道"
                        aria-label={`第 ${index + 1} 项支付渠道`}
                        onChange={(event) => patchFunding(index, { channel: optionalText(event.target.value) })}
                      />
                      <Input
                        value={row.amount ?? ""}
                        placeholder="金额 50000.00"
                        aria-label={`第 ${index + 1} 项资金金额`}
                        onChange={(event) => patchFunding(index, { amount: optionalText(event.target.value) })}
                      />
                      <InputNumber
                        value={row.funding_year ?? null}
                        min={1900}
                        max={2999}
                        placeholder="年度"
                        aria-label={`第 ${index + 1} 项资金年度`}
                        onChange={(value) => patchFunding(index, { funding_year: value ?? undefined })}
                      />
                      <Input
                        value={row.cost_center ?? ""}
                        placeholder="成本中心"
                        aria-label={`第 ${index + 1} 项成本中心`}
                        onChange={(event) => patchFunding(index, { cost_center: optionalText(event.target.value) })}
                      />
                      <Button type="link" size="small" onClick={() => removeFunding(index)}>
                        删除
                      </Button>
                    </div>
                  ))}
                </div>
                <Button
                  type="dashed"
                  size="small"
                  onClick={() => setDraft((current) => ({ ...current, funding: [...(current.funding ?? []), emptyFunding()] }))}
                >
                  添加资金来源
                </Button>

                <h3>付款期次</h3>
                <div className="contracts-page-rows">
                  {(draft.payments ?? []).map((row, index) => (
                    <div className="contracts-page-row" key={`payment-${index}`}>
                      <InputNumber
                        value={row.sequence}
                        min={1}
                        max={1000}
                        aria-label={`第 ${index + 1} 期期次`}
                        onChange={(value) => patchPayment(index, { sequence: value ?? index + 1 })}
                      />
                      <Select
                        value={row.is_advance ?? false}
                        aria-label={`第 ${index + 1} 期是否预付`}
                        options={[
                          { value: false, label: "非预付" },
                          { value: true, label: "预付款" },
                        ]}
                        onChange={(value) => patchPayment(index, { is_advance: value })}
                      />
                      <Input
                        value={row.amount ?? ""}
                        placeholder="本期金额 80000.00"
                        aria-label={`第 ${index + 1} 期金额`}
                        onChange={(event) => patchPayment(index, { amount: optionalText(event.target.value) })}
                      />
                      <Input
                        value={row.advance_amount ?? ""}
                        placeholder="预付款金额 0.00"
                        aria-label={`第 ${index + 1} 期预付款金额`}
                        onChange={(event) => patchPayment(index, { advance_amount: optionalText(event.target.value) })}
                      />
                      <Input
                        value={row.unit_amount ?? ""}
                        placeholder="单位支付 80000.00"
                        aria-label={`第 ${index + 1} 期单位支付金额`}
                        onChange={(event) => patchPayment(index, { unit_amount: optionalText(event.target.value) })}
                      />
                      <Input
                        value={row.fiscal_amount ?? ""}
                        placeholder="财政支付 0.00"
                        aria-label={`第 ${index + 1} 期财政支付金额`}
                        onChange={(event) => patchPayment(index, { fiscal_amount: optionalText(event.target.value) })}
                      />
                      <Input
                        value={row.ratio ?? ""}
                        placeholder="比例 0.200000"
                        aria-label={`第 ${index + 1} 期比例`}
                        onChange={(event) => patchPayment(index, { ratio: optionalText(event.target.value) })}
                      />
                      <Input
                        value={row.ratio_base ?? ""}
                        placeholder="比例基数说明"
                        aria-label={`第 ${index + 1} 期比例基数`}
                        onChange={(event) => patchPayment(index, { ratio_base: optionalText(event.target.value) })}
                      />
                      <Button type="link" size="small" onClick={() => removePayment(index)}>
                        删除
                      </Button>
                    </div>
                  ))}
                </div>
                <Button
                  type="dashed"
                  size="small"
                  onClick={() =>
                    setDraft((current) => ({
                      ...current,
                      payments: [
                        ...(current.payments ?? []),
                        { sequence: (current.payments?.length ?? 0) + 1, is_advance: false },
                      ],
                    }))
                  }
                >
                  添加付款期次
                </Button>
                <p className="meta">
                  预付款是本期的分类值，不重复计入单位支付与财政支付合计；金额恒等式在提交时由服务端校验。
                </p>
              </fieldset>

              <fieldset className="contracts-page-section" disabled={readOnly}>
                <legend>四、材料与核对</legend>
                <p className="meta">
                  当前切片保存结构化登记内容。真实材料版本在“导入已签存量合同”时按来源案件关联并核对摘要；上传、解析与提取结果随合同材料链后续切片接入。
                </p>
                {(draft.materials ?? []).length === 0 ? (
                  <p className="meta">尚未关联材料。</p>
                ) : (
                  <ul className="contracts-page-materials">
                    {(draft.materials ?? []).map((ref: MaterialRef) => (
                      <li key={`${ref.material_id}-${ref.material_version_id}`}>
                        <Tag>{ref.purpose}</Tag>
                        <span>{ref.material_id}</span>
                        <span className="contracts-page-hash">{ref.sha256}</span>
                      </li>
                    ))}
                  </ul>
                )}
                <ContractReferencesPanel contractID={contractID} draft={draft} />
              </fieldset>

              {contractID !== null && contractDetail !== null ? (
                <ContractSigningPanel
                  key={contractID}
                  detail={contractDetail}
                  actor={actor}
                  onRefresh={async () => resetEditor(await getContract(contractID))}
                  onOpenDraft={(id) => void openDraft(id)}
                />
              ) : null}
              {contractID !== null && contractDetail !== null ? (
                <ContractFulfillmentPanel
                  key={`${contractID}-fulfillment-${actor?.audience ?? "anonymous"}-${actor?.tenant_id ?? ""}-${actor?.org_id ?? ""}-${actor?.id ?? ""}-${actor?.role ?? ""}`}
                  detail={contractDetail}
                  actor={actor}
                  onRefresh={async () => resetEditor(await getContract(contractID))}
                />
              ) : null}
              {contractID !== null && contractDetail !== null ? (
                <ContractPaymentsPanel
                  key={`${contractID}-payments-${currentActorScope}`}
                  detail={contractDetail}
                  actor={actor}
                  canCheck={!dirty && !saving && !loading}
                  onRefresh={() => refreshPaymentContract(contractID, currentActorScope)}
                />
              ) : null}

              <div className="contracts-page-savebar">
                <span role="status">
                  {dirty ? "有未保存的修改" : contractID === null ? "尚未创建草稿" : "与服务端一致"}
                </span>
                <Button
                  type="primary"
                  loading={saving}
                  disabled={!canSave || !dirty}
                  onClick={() => void save()}
                >
                  保存草稿
                </Button>
              </div>
            </>
          )}
        </div>
      </div>

      <LegacyImportDialog
        open={importOpen}
        onClose={() => setImportOpen(false)}
        snapshot={draft}
        actor={actor}
        onImported={(detail) => {
          resetEditor(detail);
          setImportOpen(false);
          setMessage({ kind: "saved", text: "已登记存量合同并保留历史未知" });
          void queryClient.invalidateQueries({ queryKey: queryKeys.contractDraftsPrefix });
        }}
      />
    </section>
  );

  function patchItem(index: number, patch: Partial<DraftItem>) {
    setDraft((current) => {
      const items = [...(current.items ?? [])];
      items[index] = { ...items[index]!, ...patch };
      return { ...current, items };
    });
  }

  function removeItem(itemID: string) {
    setDraft((current) => ({
      ...current,
      items: (current.items ?? []).filter((item) => item.item_id !== itemID),
    }));
  }

  function patchFunding(index: number, patch: Partial<DraftFunding>) {
    setDraft((current) => {
      const funding = [...(current.funding ?? [])];
      funding[index] = { ...funding[index]!, ...patch };
      return { ...current, funding };
    });
  }

  function removeFunding(index: number) {
    setDraft((current) => ({
      ...current,
      funding: (current.funding ?? []).filter((_, position) => position !== index),
    }));
  }

  function patchPayment(index: number, patch: Partial<NonNullable<DraftContent["payments"]>[number]>) {
    setDraft((current) => {
      const payments = [...(current.payments ?? [])];
      payments[index] = { ...payments[index]!, ...patch };
      return { ...current, payments };
    });
  }

  function removePayment(index: number) {
    setDraft((current) => ({
      ...current,
      payments: (current.payments ?? []).filter((_, position) => position !== index),
    }));
  }
}

function LegacyImportDialog({
  open,
  onClose,
  snapshot,
  actor,
  onImported,
}: {
  open: boolean;
  onClose: () => void;
  snapshot: DraftContent;
  actor: Actor | null;
  onImported: (detail: ContractDetail) => void;
}) {
  const cases = useCases();
  const [caseID, setCaseID] = useState<string | null>(null);
  const detail = useCaseDetail(caseID ?? "");
  const [materialVersionID, setMaterialVersionID] = useState<string | null>(null);
  const [reason, setReason] = useState("");
  const [sourceSystem, setSourceSystem] = useState("");
  const [sourceID, setSourceID] = useState("");
  const [sourceVersion, setSourceVersion] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const materials = useMemo(() => {
    if (detail.data === undefined) return [];
    const documents = new Map(detail.data.documents.map((document) => [document.id, document]));
    return (detail.data.material_versions ?? []).flatMap((version) => {
      const document = documents.get(version.document_id);
      if (document === undefined) return [];
      return [
        {
          value: version.document_id,
          label: `${document.filename} · v${version.version} · ${document.digest.slice(0, 12)}…`,
          ref: {
            material_id: version.material_id,
            material_version_id: version.document_id,
            sha256: document.digest,
            purpose: "signed_contract",
          } satisfies MaterialRef,
        },
      ];
    });
  }, [detail.data]);

  const selected = materials.find((item) => item.value === materialVersionID) ?? null;
  const ready =
    actor !== null &&
    caseID !== null &&
    selected !== null &&
    reason.trim() !== "" &&
    sourceSystem.trim() !== "" &&
    sourceID.trim() !== "" &&
    sourceVersion.trim() !== "";

  async function submit() {
    if (!ready || actor === null || selected === null || caseID === null) return;
    setBusy(true);
    setError(null);
    try {
      const input = {
        source_ref: { system: sourceSystem.trim(), id: sourceID.trim(), source_version: sourceVersion.trim() },
        source_evidence: selected.ref,
        snapshot,
        approval_history_status: "unknown" as const,
        historical_approval_materials: [] as MaterialRef[],
        reason: reason.trim(),
      };
      const pending = await pendingMutationKey(
        "clm-legacy-import",
        idempotencyActorScope(actor),
        `${sourceSystem}:${sourceID}`,
        input,
      );
      const result = await importLegacyContract(input, pending.key);
      clearPendingMutationKey(pending.storageKey);
      onImported(result);
      setReason("");
    } catch (caught) {
      setError(errorMessage(caught));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open={open}
      onCancel={onClose}
      title="导入已签存量合同"
      width={720}
      footer={[
        <Button key="cancel" onClick={onClose}>
          取消
        </Button>,
        <Button key="submit" type="primary" loading={busy} disabled={!ready} onClick={() => void submit()}>
          登记存量合同
        </Button>,
      ]}
    >
      <p className="meta">
        登记会冻结第 1 版并保留来源信息。历史会签无法取得时保持“未知”，不会生成会签轮次或签名；使用当前编辑区内容作为合同快照。
      </p>
      {error !== null ? <Alert type="error" showIcon message={error} /> : null}
      <div className="contracts-page-fields">
        <label className="field">
          来源系统
          <Input value={sourceSystem} maxLength={100} placeholder="例如 oa-contract" onChange={(event) => setSourceSystem(event.target.value)} />
        </label>
        <label className="field">
          来源合同 ID
          <Input value={sourceID} maxLength={200} onChange={(event) => setSourceID(event.target.value)} />
        </label>
        <label className="field">
          来源版本
          <Input value={sourceVersion} maxLength={100} onChange={(event) => setSourceVersion(event.target.value)} />
        </label>
        <label className="field">
          来源案件
          <Select
            showSearch
            optionFilterProp="label"
            value={caseID}
            placeholder="选择有权访问的案件"
            loading={cases.isLoading}
            options={(cases.data ?? []).map((item) => ({ value: item.id, label: item.title }))}
            onChange={(value) => {
              setCaseID(value);
              setMaterialVersionID(null);
            }}
          />
        </label>
        <label className="field field-wide">
          签署件材料版本
          <Select
            showSearch
            optionFilterProp="label"
            value={materialVersionID}
            placeholder={caseID === null ? "先选择来源案件" : "选择真实材料版本"}
            loading={detail.isLoading}
            disabled={caseID === null}
            options={materials}
            onChange={(value) => setMaterialVersionID(value)}
          />
        </label>
        <label className="field field-wide">
          导入原因
          <Input.TextArea
            value={reason}
            maxLength={2000}
            autoSize={{ minRows: 2, maxRows: 4 }}
            placeholder="说明来源、历史缺口以及为何按存量登记"
            onChange={(event) => setReason(event.target.value)}
          />
        </label>
      </div>
      <p className="meta">
        合同快照名称：{snapshot.contract.name ?? "未填写"}；金额：
        {snapshot.contract.amount ?? "未知"} {snapshot.contract.currency ?? ""}。金额或币种缺失时按未知成对保存。
      </p>
    </Modal>
  );
}
