import { Button } from "antd";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import {
  errorMessage,
  isConflict,
  isForbidden,
  isUnauthenticated,
} from "../api/http";
import { useCreateRulePack, useRulePack, useSession } from "../api/queries";
import type { RulePackManifestInput } from "../api/rulepacks";
import { PageStatus } from "../components/page-status";
import { RulePackErrorSummary } from "../components/rulepack-error-summary";
import {
  createEmptyRulePackDraft,
  RulePackManifestEditor,
} from "../components/rulepack-manifest-editor";
import {
  validateRulePackDraft,
  type RulePackFormIssue,
} from "../domain/rulepack-form";

export function RulePackCreatePage() {
  const [params] = useSearchParams();
  const sourcePack = params.get("from_pack") ?? "";
  const sourceVersion = params.get("from_version") ?? "";
  const source = useRulePack(sourcePack, sourceVersion);
  const [sourceInitialized, setSourceInitialized] = useState(false);
  const session = useSession("admin");
  const create = useCreateRulePack();
  const navigate = useNavigate();
  const serverError = useRef<HTMLParagraphElement>(null);
  const [draft, setDraft] = useState<RulePackManifestInput>(() =>
    createEmptyRulePackDraft(""),
  );
  const [issues, setIssues] = useState<RulePackFormIssue[]>([]);
  const tenantId = session.data?.tenant_id ?? "";

  useEffect(() => {
    if (!source.data || tenantId === "" || sourceInitialized) return;
    if (source.data.manifest.tenant_id !== tenantId) return;
    const { lifecycle: _lifecycle, ...content } = source.data.manifest;
    setDraft({
      ...content,
      version: "",
      rules: content.rules.map((rule) => ({ ...rule, tenant_id: tenantId })),
    });
    setSourceInitialized(true);
  }, [source.data, tenantId, sourceInitialized]);

  useEffect(() => {
    if (tenantId === "") return;
    setDraft((current) => ({
      ...current,
      tenant_id: tenantId,
      rules: current.rules.map((rule) => ({ ...rule, tenant_id: tenantId })),
    }));
  }, [tenantId]);
  useEffect(() => {
    if (create.error) serverError.current?.focus();
  }, [create.error]);

  if (session.isLoading)
    return <PageStatus kind="loading" title="正在确认管理身份" />;
  if (session.isError || !session.data) {
    const denied = isForbidden(session.error);
    const unauthenticated = isUnauthenticated(session.error);
    return (
      <PageStatus
        kind={denied ? "denied" : "error"}
        title={denied ? "当前管理身份无权创建规则包" : "无法确认管理身份"}
        detail={
          session.error ? errorMessage(session.error) : "请重新登录管理工作台。"
        }
        action={
          denied ? (
            <Button
              aria-label={"返回管理设置"}
              type="primary"
              href="/admin/settings"
            >
              返回管理设置
            </Button>
          ) : (
            <Button
              aria-label={String(
                unauthenticated ? "重新登录" : "登录管理工作台",
              )}
              type="primary"
              href="/admin/login"
            >
              {unauthenticated ? "重新登录" : "登录管理工作台"}
            </Button>
          )
        }
      />
    );
  }

  if (sourcePack !== "" && sourceVersion !== "") {
    if (source.isError)
      return (
        <PageStatus
          kind="error"
          title="来源规则版本没有加载"
          detail={errorMessage(source.error)}
          action={<Link to="/admin/rulepacks">返回规则库</Link>}
        />
      );
    if (source.data && source.data.manifest.tenant_id !== tenantId)
      return <PageStatus kind="denied" title="来源规则版本不属于当前租户" />;
    if (!sourceInitialized)
      return <PageStatus kind="loading" title="正在读取来源规则版本" />;
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const manifest: RulePackManifestInput = {
      ...draft,
      tenant_id: tenantId,
      rules: draft.rules.map((rule) => ({ ...rule, tenant_id: tenantId })),
    };
    const nextIssues = validateRulePackDraft(manifest, tenantId);
    setIssues(nextIssues);
    if (nextIssues.length > 0) return;
    setIssues([]);
    create.mutate(manifest, {
      onSuccess: () =>
        navigate(
          `/admin/rulepacks/${encodeURIComponent(manifest.id)}/versions/${encodeURIComponent(manifest.version)}`,
        ),
    });
  }

  return (
    <section className="page page-rulepack-form">
      <p>
        <Link to="/admin/rulepacks">返回规则包</Link>
      </p>
      <header className="page-head">
        <div>
          <h1>创建规则包草稿</h1>
          <p>
            {sourceInitialized
              ? `已复制 ${sourcePack} v${sourceVersion} 的规则内容。填写一个新的版本号后编辑并保存；原版本继续保留。`
              : "填写规则的适用范围、有限比较器参数、事实来源角色与权威来源。保存只创建草稿，不会批准或发布。"}
          </p>
        </div>
      </header>
      <form
        className="workflow-form rulepack-form"
        noValidate
        onSubmit={submit}
      >
        <RulePackErrorSummary issues={issues} />
        {create.error ? (
          <p
            className="workflow-error"
            role="alert"
            tabIndex={-1}
            ref={serverError}
          >
            {errorMessage(create.error)}
            {isConflict(create.error)
              ? " 草稿未创建；请调整规则包 ID 或版本后重试。"
              : ""}
          </p>
        ) : null}
        <RulePackManifestEditor
          value={draft}
          tenantId={tenantId}
          lockedPackId={sourceInitialized}
          disabled={create.isPending}
          onChange={(next) => setDraft(next)}
        />
        <div className="dialog-actions">
          <Button aria-label={"取消"} type="text" href="/admin/rulepacks">
            取消
          </Button>
          <Button
            aria-label={String(create.isPending ? "正在创建草稿" : "创建草稿")}
            type="primary"
            htmlType="submit"
            disabled={create.isPending || tenantId === ""}
          >
            {create.isPending ? "正在创建草稿" : "创建草稿"}
          </Button>
        </div>
      </form>
    </section>
  );
}
