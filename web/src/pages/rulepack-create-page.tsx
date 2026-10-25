import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router";
import { errorMessage, isConflict, isForbidden, isUnauthenticated } from "../api/http";
import { useCreateRulePack, useSession } from "../api/queries";
import type { RulePackManifestInput } from "../api/rulepacks";
import { PageStatus } from "../components/page-status";
import { RulePackErrorSummary } from "../components/rulepack-error-summary";
import { createEmptyRulePackDraft, RulePackManifestEditor } from "../components/rulepack-manifest-editor";
import { validateRulePackDraft, type RulePackFormIssue } from "../domain/rulepack-form";

export function RulePackCreatePage() {
  const session = useSession("admin");
  const create = useCreateRulePack();
  const navigate = useNavigate();
  const serverError = useRef<HTMLParagraphElement>(null);
  const [draft, setDraft] = useState<RulePackManifestInput>(() => createEmptyRulePackDraft(""));
  const [issues, setIssues] = useState<RulePackFormIssue[]>([]);
  const tenantId = session.data?.tenant_id ?? "";

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

  if (session.isLoading) return <PageStatus kind="loading" title="正在确认管理身份" />;
  if (session.isError || !session.data) {
    const denied = isForbidden(session.error);
    const unauthenticated = isUnauthenticated(session.error);
    return <PageStatus kind={denied ? "denied" : "error"} title={denied ? "当前管理身份无权创建规则包" : "无法确认管理身份"} detail={session.error ? errorMessage(session.error) : "请重新登录管理工作台。"} action={denied ? <Link className="button button-primary" to="/admin/settings">返回管理设置</Link> : <Link className="button button-primary" to="/admin/login">{unauthenticated ? "重新登录" : "登录管理工作台"}</Link>} />;
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
      onSuccess: () => navigate(`/admin/rulepacks/${encodeURIComponent(manifest.id)}/versions/${encodeURIComponent(manifest.version)}`),
    });
  }

  return (
    <section className="page page-rulepack-form">
      <p><Link to="/admin/rulepacks">返回规则包</Link></p>
      <header className="page-head">
        <div><h1>创建规则包草稿</h1><p>填写规则的适用范围、有限比较器参数、事实来源角色与权威来源。保存只创建草稿，不会批准或发布。</p></div>
      </header>
      <form className="workflow-form rulepack-form" noValidate onSubmit={submit}>
        <RulePackErrorSummary issues={issues} />
        {create.error ? <p className="workflow-error" role="alert" tabIndex={-1} ref={serverError}>{errorMessage(create.error)}{isConflict(create.error) ? " 草稿未创建；请调整规则包 ID 或版本后重试。" : ""}</p> : null}
        <RulePackManifestEditor value={draft} tenantId={tenantId} disabled={create.isPending} onChange={(next) => setDraft(next)} />
        <div className="dialog-actions">
          <Link className="button button-quiet" to="/admin/rulepacks">取消</Link>
          <button className="button button-primary" type="submit" disabled={create.isPending || tenantId === ""}>{create.isPending ? "正在创建草稿" : "创建草稿"}</button>
        </div>
      </form>
    </section>
  );
}
