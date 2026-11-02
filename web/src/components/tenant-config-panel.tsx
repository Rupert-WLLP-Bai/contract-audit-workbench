import { useEffect, useMemo, useState } from "react";
import { Alert, Button, Select, Table, Tag, type TableProps } from "antd";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  captureCommandContext,
  errorMessage,
  type CommandContext,
} from "../api/http";
import {
  adoptTenantConfig,
  previewTenantConfig,
  setTenantConfigLiveControl,
  useTenantConfig,
  type TenantConfigPreview,
  type TenantConfigRef,
  type TenantConfigSelection,
} from "../api/tenant-config";
import type { Actor } from "../api/types";
import "./tenant-config-panel.css";

type DiffRow = TenantConfigPreview["diff"][number] & { key: string };
const diffColumns: TableProps<DiffRow>["columns"] = [
  { title: "配置项", dataIndex: "path", rowScope: "row" },
  { title: "变化", dataIndex: "change", render: (value: DiffRow["change"]) => ({ added: "新增", removed: "移除", changed: "变更" })[value] },
  { title: "原值", dataIndex: "before", render: (value: string | null) => value ?? "未设置" },
  { title: "新值", dataIndex: "after", render: (value: string | null) => value ?? "未设置" },
];

function selectionFromCurrent(current: {
  public_template: TenantConfigRef;
  private_policy_refs: TenantConfigRef[];
  model_catalog_ref: string;
  credential_ref: string | null;
  egress_mode: "approved_only";
}): TenantConfigSelection {
  return {
    public_template_ref: {
      id: current.public_template.id,
      version: current.public_template.version,
      digest: current.public_template.digest,
    },
    private_policy_refs: current.private_policy_refs.map(({ id, version, digest }) => ({ id, version, digest })),
    model_catalog_ref: current.model_catalog_ref,
    credential_ref: current.credential_ref,
    egress_mode: current.egress_mode,
  };
}

function initialSelection(data: NonNullable<ReturnType<typeof useTenantConfig>["data"]>): TenantConfigSelection | null {
  if (data.current) return selectionFromCurrent(data.current);
  const template = data.public_template_catalog[0];
  const model = data.model_catalog[0];
  if (!template || !model) return null;
  return {
    public_template_ref: { id: template.id, version: template.version, digest: template.digest },
    private_policy_refs: [],
    model_catalog_ref: model.id,
    credential_ref: null,
    egress_mode: "approved_only",
  };
}

function refKey(ref: Pick<TenantConfigRef, "id" | "version" | "digest">): string {
  return JSON.stringify([ref.id, ref.version, ref.digest]);
}

function shortDigest(value: string): string {
  return `${value.slice(0, 12)}…`;
}

export function TenantConfigPanel({ actor }: { actor: Actor }) {
  const queryClient = useQueryClient();
  const query = useTenantConfig(actor.tenant_id, actor.role === "admin" && actor.audience === "admin");
  const [selection, setSelection] = useState<TenantConfigSelection | null>(null);
  const [preview, setPreview] = useState<{ revision: string; selectionKey: string; value: TenantConfigPreview } | null>(null);
  const [ackLiveControl, setAckLiveControl] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const data = query.data;

  useEffect(() => {
    setSelection(data ? initialSelection(data) : null);
    setPreview(null);
    setAckLiveControl(false);
    setNotice(null);
  }, [data?.tenant_id, data?.revision]);

  const templateOptions = useMemo(() => {
    if (!data) return [];
    const refs = [...(data.current ? [data.current.public_template] : []), ...data.public_template_catalog];
    const byKey = new Map(refs.map((ref) => [refKey(ref), ref]));
    return [...byKey.entries()].map(([value, ref]) => ({ value, label: `${ref.title} · ${ref.version} · ${ref.kind}` }));
  }, [data]);
  const privatePolicyOptions = useMemo(() => {
    if (!data) return [];
    const refs = [...(data.current?.private_policy_refs ?? []), ...data.private_policy_catalog];
    const byKey = new Map(refs.map((ref) => [refKey(ref), ref]));
    return [...byKey.entries()].map(([value, ref]) => ({ value, label: `${ref.title} · ${ref.version}` }));
  }, [data]);
  const currentSelectionKey = selection ? JSON.stringify(selection) : "";
  const previewMatches = Boolean(
    preview && data && selection && preview.revision === data.revision && preview.selectionKey === currentSelectionKey,
  );

  const previewMutation = useMutation({
    mutationFn: (input: { revision: string; selection: TenantConfigSelection; context: CommandContext }) =>
      previewTenantConfig(input.revision, input.selection, input.context),
    onSuccess: (value, input) => setPreview({ revision: input.revision, selectionKey: JSON.stringify(input.selection), value }),
    onError: (cause) => {
      setPreview(null);
      if (cause instanceof Error && "code" in cause && cause.code === "config_revision_conflict") void query.refetch();
    },
  });
  const adoptionMutation = useMutation({
    mutationFn: (input: { revision: string; selection: TenantConfigSelection; context: CommandContext }) =>
      adoptTenantConfig(input.revision, input.selection, input.context),
    onSuccess: async (result) => {
      setPreview(null);
      setNotice(`已采用配置版本 ${result.adopted_config.config_version}；只影响之后启动的运行。`);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["tenant-config", actor.tenant_id] }),
        queryClient.invalidateQueries({ queryKey: ["tenant-operations", "admin"] }),
      ]);
    },
    onError: (cause) => {
      setPreview(null);
      if (cause instanceof Error && "code" in cause && cause.code === "config_revision_conflict") void query.refetch();
    },
  });
  const liveMutation = useMutation({
    mutationFn: (input: { revision: string; enabled: boolean; context: CommandContext }) =>
      setTenantConfigLiveControl(input.revision, input.enabled, input.context),
    onSuccess: async (result) => {
      setAckLiveControl(false);
      setNotice(result.enabled ? "已恢复实时模型发送控制。" : "已停止后续模型发送；服务端将保留已发出调用的结算。" );
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["tenant-config", actor.tenant_id] }),
        queryClient.invalidateQueries({ queryKey: ["tenant-operations", "admin"] }),
      ]);
    },
    onError: (cause) => {
      if (cause instanceof Error && "code" in cause && cause.code === "live_control_revision_conflict") void query.refetch();
    },
  });

  function updateSelection(next: TenantConfigSelection) {
    setSelection(next);
    setPreview(null);
    setNotice(null);
  }

  function requestPreview() {
    if (!data || !selection || query.isError || query.isFetching) return;
    const context = captureCommandContext(actor);
    setNotice(null);
    setPreview(null);
    previewMutation.mutate({ revision: data.revision, selection, context });
  }

  function requestAdoption() {
    if (!data || !selection || !previewMatches || !preview?.value.valid || query.isError || query.isFetching) return;
    const context = captureCommandContext(actor);
    adoptionMutation.mutate({ revision: data.revision, selection, context });
  }

  function requestLiveControl() {
    if (!data || !ackLiveControl || query.isError || query.isFetching) return;
    const context = captureCommandContext(actor);
    liveMutation.mutate({ revision: data.live_control.revision, enabled: !data.live_control.enabled, context });
  }

  const commandPending = previewMutation.isPending || adoptionMutation.isPending || liveMutation.isPending;
  const modelOptions = data?.model_catalog.map((model) => ({
    value: model.id,
    label: `${model.provider_label} · 聊天 ${model.chat_model} · OCR ${model.ocr_model} · r${model.revision}`,
  })) ?? [];
  if (data?.current && !modelOptions.some((option) => option.value === data.current?.model_catalog_ref)) {
    modelOptions.push({ value: data.current.model_catalog_ref, label: `当前采用模型 · ${data.current.model_catalog_ref}` });
  }
  const credentialOptions = [
    { value: "__none__", label: "不使用平台凭据" },
    ...(data?.credential_refs ?? []).map((credential) => ({ value: credential.id, label: credential.label })),
  ];
  if (data?.current?.credential_ref && !credentialOptions.some((option) => option.value === data.current?.credential_ref)) {
    credentialOptions.push({ value: data.current.credential_ref, label: `当前采用凭据 · ${data.current.credential_ref}` });
  }

  return (
    <section className="settings-panel tenant-config-panel" aria-labelledby="tenant-config-title">
      <header className="settings-panel-head">
        <div>
          <h2 id="tenant-config-title">租户配置采用与实时停发</h2>
          <p className="settings-panel-note">只使用服务端列出的候选版本和平台凭据别名；不会读取或提交供应商 URL、密钥明文。</p>
        </div>
        <Button loading={query.isFetching} onClick={() => void query.refetch()}>刷新租户配置</Button>
      </header>
      {query.isLoading ? <p role="status">正在读取当前采用版本和允许候选项</p> : null}
      {query.isError ? <Alert type="error" showIcon title={`租户配置没有加载：${errorMessage(query.error)}`} /> : null}
      {data ? <>
        <dl className="settings-facts tenant-config-current">
          <div><dt>当前租户</dt><dd>{data.tenant_id}</dd></div>
          {data.current ? <>
            <div><dt>配置采用版本</dt><dd className="is-code">{data.current.config_version}</dd></div>
            <div><dt>版本 ID / 摘要</dt><dd className="is-code">{data.current.version_id} · {shortDigest(data.current.digest)}</dd></div>
            <div><dt>公共模板</dt><dd>{data.current.public_template.title} · {data.current.public_template.version}</dd></div>
            <div><dt>租户私有制度</dt><dd>{data.current.private_policy_refs.length === 0 ? "未采用" : data.current.private_policy_refs.map((ref) => `${ref.title} · ${ref.version}`).join("、")}</dd></div>
            <div><dt>模型目录</dt><dd className="is-code">{data.current.model_catalog_ref}</dd></div>
            <div><dt>凭据引用</dt><dd>{data.current.credential_ref ?? "未使用平台凭据"}</dd></div>
            <div><dt>数据发送模式</dt><dd>仅限已批准端点</dd></div>
            <div><dt>采用时间</dt><dd>{new Date(data.current.adopted_at).toLocaleString()}</dd></div>
            <div><dt>采用人</dt><dd>{data.current.adopted_by}</dd></div>
          </> : <div data-tone="attention"><dt>当前配置</dt><dd>尚未采用</dd></div>}
          <div data-tone={data.live_control.enabled ? "good" : "danger"}>
            <dt>实时模型发送</dt><dd>{data.live_control.enabled ? "允许（仍受服务端实时校验）" : "已停止后续发送"}</dd>
          </div>
          <div><dt>停发控制修订</dt><dd className="is-code">{data.live_control.revision}</dd></div>
          <div><dt>停发状态更新时间</dt><dd>{new Date(data.live_control.updated_at).toLocaleString()}</dd></div>
        </dl>
        <p>新配置只用于之后创建的运行；既有运行继续使用各自冻结的配置版本。实时停发是独立控制，不能被旧配置覆盖；已发出的调用仍按原预留结算。</p>

        <section className="tenant-config-editor" aria-labelledby="tenant-config-editor-title">
          <h3 id="tenant-config-editor-title">预览并采用候选配置</h3>
          {!selection ? <p role="status">没有可用于预览的公共模板或模型候选；请由平台管理员提供允许的配置后再采用。</p> : null}
          <label>
            公共模板
            <Select
              aria-label="公共模板"
              value={selection ? refKey(selection.public_template_ref) : undefined}
              options={templateOptions}
              disabled={query.isError || query.isFetching || commandPending}
              onChange={(value: string) => {
                const ref = [...(data?.public_template_catalog ?? []), ...(data?.current ? [data.current.public_template] : [])]
                  .find((candidate) => refKey(candidate) === value);
                if (ref && selection) updateSelection({ ...selection, public_template_ref: { id: ref.id, version: ref.version, digest: ref.digest } });
              }}
            />
          </label>
          <label>
            租户私有制度
            <Select
              mode="multiple"
              aria-label="租户私有制度"
              value={selection?.private_policy_refs.map(refKey) ?? []}
              options={privatePolicyOptions}
              disabled={query.isError || query.isFetching || commandPending}
              onChange={(values: string[]) => {
                if (!selection) return;
                const refs = [...(data?.private_policy_catalog ?? []), ...(data?.current?.private_policy_refs ?? [])];
                updateSelection({ ...selection, private_policy_refs: values.flatMap((value) => {
                  const ref = refs.find((candidate) => refKey(candidate) === value);
                  return ref ? [{ id: ref.id, version: ref.version, digest: ref.digest }] : [];
                }) });
              }}
            />
          </label>
          <label>
            模型目录
            <Select
              aria-label="模型目录"
              value={selection?.model_catalog_ref}
              options={modelOptions}
              disabled={query.isError || query.isFetching || commandPending}
              onChange={(value: string) => { if (selection) updateSelection({ ...selection, model_catalog_ref: value }); }}
            />
          </label>
          <label>
            凭据引用
            <Select
              aria-label="凭据引用"
              value={selection?.credential_ref ?? "__none__"}
              options={credentialOptions}
              disabled={query.isError || query.isFetching || commandPending}
              onChange={(value: string) => { if (selection) updateSelection({ ...selection, credential_ref: value === "__none__" ? null : value }); }}
            />
          </label>
          <p>数据发送模式固定为“仅限已批准端点”。配置差异由服务端验证，页面不接受任意 URL 或密钥引用。</p>
          <div className="tenant-config-actions">
            <Button
              loading={previewMutation.isPending}
              disabled={!selection || query.isError || query.isFetching || commandPending}
              onClick={requestPreview}
            >预览差异</Button>
            <Button
              type="primary"
              loading={adoptionMutation.isPending}
              disabled={!previewMatches || !preview?.value.valid || query.isError || query.isFetching || commandPending}
              onClick={requestAdoption}
            >采用到后续新运行</Button>
          </div>
        </section>

        {previewMutation.isError ? <Alert type="error" showIcon title={`配置预览失败：${errorMessage(previewMutation.error)}`} /> : null}
        {adoptionMutation.isError ? <Alert type="error" showIcon title={`配置没有采用：${errorMessage(adoptionMutation.error)}`} /> : null}
        {previewMatches && preview ? <>
          {preview.value.valid ? <Tag color="success">服务端校验通过</Tag> : <Tag color="warning">服务端拒绝该组合</Tag>}
          {preview.value.errors.length ? <ul className="tenant-config-errors">{preview.value.errors.map((error, index) => <li key={`${error.path}-${index}`}>{error.path} · {error.code}</li>)}</ul> : null}
          <Table<DiffRow>
            rowKey="key"
            columns={diffColumns}
            pagination={false}
            size="small"
            locale={{ emptyText: "配置没有差异" }}
            dataSource={preview.value.diff.map((diff, index) => ({ ...diff, key: `${diff.path}-${index}` }))}
          />
        </> : null}

        <section className="tenant-config-live-control" aria-label="实时模型停发控制">
          <h3>独立实时停发控制</h3>
          <p>{data.live_control.enabled ? "关闭后，服务端会阻止后续模型请求；既有运行的已发出调用不会被伪报为未发生。" : "当前已停止后续模型请求；恢复后新调用仍须满足租户配置、授权和预算条件。"}</p>
          <label className="tenant-config-confirm">
            <input type="checkbox" checked={ackLiveControl} disabled={query.isError || query.isFetching || commandPending} onChange={(event) => setAckLiveControl(event.target.checked)} />
            我确认通过服务器端租户控制改变后续模型发送状态
          </label>
          <Button
            danger={data.live_control.enabled}
            type={data.live_control.enabled ? "primary" : "default"}
            loading={liveMutation.isPending}
            disabled={!ackLiveControl || query.isError || query.isFetching || commandPending}
            onClick={requestLiveControl}
          >{data.live_control.enabled ? "停止后续模型发送" : "恢复后续模型发送"}</Button>
          {liveMutation.isError ? <Alert type="error" showIcon title={`实时控制没有更新：${errorMessage(liveMutation.error)}`} /> : null}
        </section>
      </> : null}
      {notice ? <Alert type="success" showIcon title={notice} /> : null}
    </section>
  );
}
