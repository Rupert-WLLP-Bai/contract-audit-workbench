import { useQuery } from "@tanstack/react-query";
import { paths } from "./paths";
import { jsonInit, requestJson, type CommandContext } from "./http";
import { enumValue, responseReader } from "./respond";

export interface TenantConfigRef {
  id: string;
  version: string;
  digest: string;
  title: string;
  kind: "public_template" | "private_policy";
}

export interface TenantConfigAdopted {
  config_version: string;
  version_id: string;
  digest: string;
  public_template: TenantConfigRef;
  private_policy_refs: TenantConfigRef[];
  model_catalog_ref: string;
  credential_ref: string | null;
  egress_mode: "approved_only";
  adopted_at: string;
  adopted_by: string;
}

export interface TenantConfigSelection {
  public_template_ref: Omit<TenantConfigRef, "title" | "kind">;
  private_policy_refs: Array<Omit<TenantConfigRef, "title" | "kind">>;
  model_catalog_ref: string;
  credential_ref: string | null;
  egress_mode: "approved_only";
}

export interface TenantConfigView {
  schema_version: "tenant-config/v1";
  tenant_id: string;
  revision: string;
  current: TenantConfigAdopted | null;
  public_template_catalog: TenantConfigRef[];
  private_policy_catalog: TenantConfigRef[];
  model_catalog: Array<{
    id: string;
    revision: string;
    provider_label: string;
    chat_model: string;
    ocr_model: string;
  }>;
  credential_refs: Array<{ id: "platform/llm-endpoint/active"; label: string }>;
  live_control: { enabled: boolean; revision: string; updated_at: string };
}

export interface TenantConfigPreview {
  valid: boolean;
  diff: Array<{
    path: string;
    change: "added" | "removed" | "changed";
    before: string | null;
    after: string | null;
  }>;
  errors: Array<{ path: string; code: string }>;
}

export interface TenantConfigCommandResult {
  revision: string;
  adopted_config: TenantConfigAdopted;
}

export interface TenantConfigLiveControlResult {
  revision: string;
  enabled: boolean;
}

const { object, text, nullableText, bool, array, invalid } = responseReader((path) => `租户配置响应中的 ${path} 无法识别`);

function timestamp(value: unknown, path: string): string {
  const result = text(value, path);
  return result !== "" && Number.isFinite(Date.parse(result)) ? result : invalid(path);
}

function digest(value: unknown, path: string): string {
  const result = text(value, path);
  return /^[0-9a-f]{64}$/.test(result) ? result : invalid(path);
}

function decimalRevision(value: unknown, path: string, allowZero: boolean): string {
  const result = text(value, path);
  return (allowZero ? /^(0|[1-9][0-9]*)$/ : /^[1-9][0-9]*$/).test(result) ? result : invalid(path);
}

function parseConfigRef(value: unknown, path: string): TenantConfigRef {
  const item = object(value, path);
  return {
    id: text(item.id, `${path}.id`),
    version: text(item.version, `${path}.version`),
    digest: digest(item.digest, `${path}.digest`),
    title: text(item.title, `${path}.title`),
    kind: enumValue(item.kind, ["public_template", "private_policy"] as const) ?? invalid(`${path}.kind`),
  };
}

function parseAdoptedConfig(value: unknown, path: string): TenantConfigAdopted {
  const item = object(value, path);
  const privatePolicyRefs = array(item.private_policy_refs, `${path}.private_policy_refs`, parseConfigRef);
  const result: TenantConfigAdopted = {
    config_version: decimalRevision(item.config_version, `${path}.config_version`, false),
    version_id: /^tcv_[a-zA-Z0-9_-]{8,120}$/.test(text(item.version_id, `${path}.version_id`))
      ? text(item.version_id, `${path}.version_id`)
      : invalid(`${path}.version_id`),
    digest: digest(item.digest, `${path}.digest`),
    public_template: parseConfigRef(item.public_template, `${path}.public_template`),
    private_policy_refs: privatePolicyRefs,
    model_catalog_ref: digest(item.model_catalog_ref, `${path}.model_catalog_ref`),
    credential_ref: nullableText(item.credential_ref, `${path}.credential_ref`),
    egress_mode: enumValue(item.egress_mode, ["approved_only"] as const) ?? invalid(`${path}.egress_mode`),
    adopted_at: timestamp(item.adopted_at, `${path}.adopted_at`),
    adopted_by: text(item.adopted_by, `${path}.adopted_by`),
  };
  if (result.credential_ref !== null && result.credential_ref !== "platform/llm-endpoint/active")
    return invalid(`${path}.credential_ref`);
  return result;
}

export function parseTenantConfigView(value: unknown): TenantConfigView {
  const item = object(value, "tenant_config");
  if (item.schema_version !== "tenant-config/v1") return invalid("tenant_config.schema_version");
  const current = item.current === null ? null : parseAdoptedConfig(item.current, "current");
  const publicTemplates = array(item.public_template_catalog, "public_template_catalog", parseConfigRef);
  const privatePolicies = array(item.private_policy_catalog, "private_policy_catalog", parseConfigRef);
  if (publicTemplates.some((ref) => ref.kind !== "public_template") ||
      privatePolicies.some((ref) => ref.kind !== "private_policy") ||
      (current && (current.public_template.kind !== "public_template" || current.private_policy_refs.some((ref) => ref.kind !== "private_policy")))) {
    return invalid("tenant_config.catalog_kinds");
  }
  const modelCatalog = array(item.model_catalog, "model_catalog", (entry, path) => {
    const model = object(entry, path);
    return {
      id: digest(model.id, `${path}.id`),
      revision: decimalRevision(model.revision, `${path}.revision`, true),
      provider_label: text(model.provider_label, `${path}.provider_label`),
      chat_model: text(model.chat_model, `${path}.chat_model`),
      ocr_model: text(model.ocr_model, `${path}.ocr_model`),
    };
  });
  const credentialRefs = array(item.credential_refs, "credential_refs", (entry, path) => {
    const credential = object(entry, path);
    const id = text(credential.id, `${path}.id`);
    if (id !== "platform/llm-endpoint/active") return invalid(`${path}.id`);
    return { id: "platform/llm-endpoint/active" as const, label: text(credential.label, `${path}.label`) };
  });
  const liveControl = object(item.live_control, "live_control");
  const result: TenantConfigView = {
    schema_version: "tenant-config/v1",
    tenant_id: text(item.tenant_id, "tenant_id"),
    revision: decimalRevision(item.revision, "revision", true),
    current,
    public_template_catalog: publicTemplates,
    private_policy_catalog: privatePolicies,
    model_catalog: modelCatalog,
    credential_refs: credentialRefs,
    live_control: {
      enabled: bool(liveControl.enabled, "live_control.enabled"),
      revision: decimalRevision(liveControl.revision, "live_control.revision", false),
      updated_at: timestamp(liveControl.updated_at, "live_control.updated_at"),
    },
  };
  if (!result.tenant_id)
    return invalid("tenant_config.references");
  return result;
}

export function parseTenantConfigPreview(value: unknown): TenantConfigPreview {
  const item = object(value, "tenant_config_preview");
  return {
    valid: bool(item.valid, "valid"),
    diff: array(item.diff, "diff", (entry, path) => {
      const change = object(entry, path);
      return {
        path: text(change.path, `${path}.path`),
        change: enumValue(change.change, ["added", "removed", "changed"] as const) ?? invalid(`${path}.change`),
        before: nullableText(change.before, `${path}.before`),
        after: nullableText(change.after, `${path}.after`),
      };
    }),
    errors: array(item.errors, "errors", (entry, path) => {
      const error = object(entry, path);
      return { path: text(error.path, `${path}.path`), code: text(error.code, `${path}.code`) };
    }),
  };
}

export function parseTenantConfigCommandResult(value: unknown): TenantConfigCommandResult {
  const item = object(value, "tenant_config_adoption");
  return {
    revision: decimalRevision(item.revision, "revision", false),
    adopted_config: parseAdoptedConfig(item.adopted_config, "adopted_config"),
  };
}

export function parseTenantConfigLiveControlResult(value: unknown): TenantConfigLiveControlResult {
  const item = object(value, "tenant_config_live_control");
  return { revision: decimalRevision(item.revision, "revision", false), enabled: bool(item.enabled, "enabled") };
}

export function getTenantConfig(signal?: AbortSignal): Promise<TenantConfigView> {
  return requestJson(paths.tenantConfig, { method: "GET", signal }, parseTenantConfigView);
}

export function previewTenantConfig(expectedRevision: string, selection: TenantConfigSelection, commandContext: CommandContext): Promise<TenantConfigPreview> {
  return requestJson(paths.tenantConfigPreview, {
    ...jsonInit("POST", { expected_revision: expectedRevision, selection }),
    commandContext,
  }, parseTenantConfigPreview);
}

export function adoptTenantConfig(expectedRevision: string, selection: TenantConfigSelection, commandContext: CommandContext): Promise<TenantConfigCommandResult> {
  return requestJson(paths.tenantConfigAdoption, {
    ...jsonInit("PUT", { expected_revision: expectedRevision, selection }),
    commandContext,
  }, parseTenantConfigCommandResult);
}

export function setTenantConfigLiveControl(expectedRevision: string, enabled: boolean, commandContext: CommandContext): Promise<TenantConfigLiveControlResult> {
  return requestJson(paths.tenantConfigLiveControl, {
    ...jsonInit("PUT", { expected_revision: expectedRevision, enabled }),
    commandContext,
  }, parseTenantConfigLiveControlResult);
}

export function useTenantConfig(tenantId: string | undefined, enabled: boolean) {
  return useQuery({
    queryKey: ["tenant-config", tenantId ?? ""],
    queryFn: ({ signal }) => getTenantConfig(signal),
    enabled: enabled && tenantId !== undefined,
    refetchInterval: 30_000,
    retry: false,
  });
}
