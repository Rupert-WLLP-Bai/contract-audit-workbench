import { Collapse } from "antd";
import { Select } from "antd";
import { Button } from "antd";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useParams } from "react-router";
import { ApiError, errorMessage, isConflict, isForbidden } from "../api/http";
import { newIdempotencyKey } from "../api/idempotency";
import {
  useApproveRulePack,
  useDisableRulePack,
  useEditRulePack,
  usePublishRulePack,
  useRollbackRulePack,
  useRulePack,
  useRulePackHistory,
  useSession,
  useValidateRulePack,
} from "../api/queries";
import type {
  RulePackDetail,
  RulePackHistoryEntry,
  RulePackManifest,
  RulePackManifestInput,
  RulePackMutationResult,
} from "../api/rulepacks";
import { PageStatus } from "../components/page-status";
import { DirtyLeaveGuard } from "../components/dirty-leave-guard";
import { RulePackErrorSummary } from "../components/rulepack-error-summary";
import { RulePackManifestEditor } from "../components/rulepack-manifest-editor";
import { RulePackTestPanel } from "../components/rulepack-test-panel";
import { RulePackSuitePanel } from "../components/rulepack-suite-panel";
import { RulePackShadowPanel } from "../components/rulepack-shadow-panel";
import {
  validateRulePackDraft,
  type RulePackFormIssue,
} from "../domain/rulepack-form";

function editableManifest(manifest: RulePackManifest): RulePackManifestInput {
  const { lifecycle: _lifecycle, ...content } = manifest;
  return content;
}

function detailFromMutation(
  result: RulePackMutationResult | undefined,
): result is RulePackDetail {
  return result !== undefined && "manifest" in result;
}

function pointerImage(version: string | null, active: boolean | null): string {
  if (version === null && active === null) return "无前值";
  return `v${version ?? ""}${active ? " 生效" : " 未生效"}`;
}

function pointerTransition(entry: RulePackHistoryEntry): string {
  if (
    entry.previous_version === null &&
    entry.previous_active === null &&
    entry.resulting_version === null &&
    entry.resulting_active === null
  )
    return "";
  const after =
    entry.resulting_version === null && entry.resulting_active === null
      ? "无新值"
      : pointerImage(entry.resulting_version, entry.resulting_active);
  return `，${pointerImage(entry.previous_version, entry.previous_active)} → ${after}`;
}

type PublishRequest = {
  expectedRevision: number;
  expectedPointerRevision: number;
  idempotencyKey: string;
};
type RollbackRequest = {
  version: string;
  expectedPointerRevision: number;
  idempotencyKey: string;
};
type ReplayRequest =
  | { kind: "publish"; identity: string; request: PublishRequest }
  | { kind: "rollback"; identity: string; request: RollbackRequest };

export function RulePackDetailPage() {
  const { packId = "", version = "" } = useParams();
  const detail = useRulePack(packId, version);
  const history = useRulePackHistory(packId);
  const session = useSession("admin");
  const edit = useEditRulePack(packId, version);
  const validate = useValidateRulePack(packId, version);
  const approve = useApproveRulePack(packId, version);
  const publish = usePublishRulePack(packId, version);
  const disable = useDisableRulePack(packId, version);
  const rollback = useRollbackRulePack(packId);
  const serverError = useRef<HTMLParagraphElement>(null);
  const [draft, setDraft] = useState<RulePackManifestInput | null>(null);
  const [dirty, setDirty] = useState(false);
  const [expectedRevision, setExpectedRevision] = useState(0);
  const [issues, setIssues] = useState<RulePackFormIssue[]>([]);
  const [actionError, setActionError] = useState<string | null>(null);
  const [actionMessage, setActionMessage] = useState<string | null>(null);
  const [rollbackVersion, setRollbackVersion] = useState("");
  const [replay, setReplay] = useState<ReplayRequest | null>(null);
  const operationKeys = useRef(new Map<string, string>());
  const tenantId = session.data?.tenant_id ?? "";

  useEffect(() => {
    if (detail.data && !dirty) {
      setDraft(editableManifest(detail.data.manifest));
      setExpectedRevision(detail.data.pack.revision);
      setIssues([]);
    }
  }, [detail.data, dirty]);
  useEffect(() => {
    if (edit.error || actionError) serverError.current?.focus();
  }, [actionError, edit.error]);

  function recordFailure(error: unknown) {
    setActionMessage(null);
    setActionError(errorMessage(error));
    if (isConflict(error)) void detail.refetch();
  }

  function completeAction(message: string, result?: RulePackMutationResult) {
    setActionError(null);
    setActionMessage(message);
    if (result && detailFromMutation(result)) {
      setDraft(editableManifest(result.manifest));
      setExpectedRevision(result.pack.revision);
      setDirty(false);
    } else {
      setDirty(false);
      void detail.refetch();
    }
  }

  function keyFor(identity: string): string {
    const existing = operationKeys.current.get(identity);
    if (existing) return existing;
    const key = newIdempotencyKey();
    operationKeys.current.set(identity, key);
    return key;
  }

  function runPublish(request: PublishRequest, identity: string) {
    publish.mutate(request, {
      onSuccess: (result) => {
        operationKeys.current.delete(identity);
        setReplay(null);
        completeAction(
          "规则包已发布。新受理的案件将按可信租户、业务范围和有效时间选择该版本。",
          result,
        );
      },
      onError: (error) => {
        recordFailure(error);
        if (error instanceof ApiError && error.retryable)
          setReplay({ kind: "publish", identity, request });
      },
    });
  }

  function runRollback(request: RollbackRequest, identity: string) {
    rollback.mutate(request, {
      onSuccess: (result) => {
        operationKeys.current.delete(identity);
        setReplay(null);
        completeAction("活动规则包指针已回滚到所选的精确发布版本。", result);
      },
      onError: (error) => {
        recordFailure(error);
        if (error instanceof ApiError && error.retryable)
          setReplay({ kind: "rollback", identity, request });
      },
    });
  }

  function publishCurrent() {
    if (!detail.data) return;
    const requestBase = {
      expectedRevision,
      expectedPointerRevision: detail.data.pack.active_pointer_revision,
    };
    const identity = JSON.stringify(["publish", packId, version, requestBase]);
    runPublish({ ...requestBase, idempotencyKey: keyFor(identity) }, identity);
  }

  function rollbackToSelected() {
    if (!detail.data || rollbackVersion === "") return;
    const requestBase = {
      version: rollbackVersion,
      expectedPointerRevision: detail.data.pack.active_pointer_revision,
    };
    const identity = JSON.stringify(["rollback", packId, requestBase]);
    runRollback({ ...requestBase, idempotencyKey: keyFor(identity) }, identity);
  }

  function retryExactRequest() {
    if (!replay) return;
    if (replay.kind === "publish") runPublish(replay.request, replay.identity);
    else runRollback(replay.request, replay.identity);
  }

  if (detail.isLoading || session.isLoading)
    return <PageStatus kind="loading" title="正在读取规则包详情" />;
  if (detail.isError) {
    return (
      <PageStatus
        kind={isForbidden(detail.error) ? "denied" : "error"}
        title={
          isForbidden(detail.error)
            ? "当前管理身份无权读取此规则包"
            : "规则包详情没有加载"
        }
        detail={errorMessage(detail.error)}
        action={
          <Button
            aria-label={"返回规则包列表"}
            type="primary"
            href="/admin/rulepacks"
          >
            返回规则包列表
          </Button>
        }
      />
    );
  }
  if (session.isError || !session.data) {
    return (
      <PageStatus
        kind="denied"
        title="无法确认管理会话"
        detail={
          session.error ? errorMessage(session.error) : "请重新登录管理工作台。"
        }
        action={
          <Button aria-label={"重新登录"} type="primary" href="/admin/login">
            重新登录
          </Button>
        }
      />
    );
  }
  if (!detail.data || !draft)
    return <PageStatus kind="empty" title="没有规则包详情" />;

  const { pack, manifest } = detail.data;
  const isTenantOwner =
    pack.tenant_id === tenantId && manifest.tenant_id === tenantId;
  const canEdit = isTenantOwner && pack.status === "draft";
  const hasCurrentRevision = expectedRevision === pack.revision;
  const historyEntries = history.data ?? [];
  const versionsInHistory = [
    ...new Set(
      historyEntries
        .filter((entry) => entry.action === "publish")
        .map((entry) => entry.version),
    ),
  ];

  function submitEdit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!canEdit) return;
    if (!draft) return;
    const nextDraft: RulePackManifestInput = {
      ...draft,
      tenant_id: tenantId,
      rules: draft.rules.map((rule) => ({ ...rule, tenant_id: tenantId })),
    };
    const nextIssues = validateRulePackDraft(nextDraft, tenantId);
    setIssues(nextIssues);
    if (nextIssues.length > 0) return;
    setIssues([]);
    setActionError(null);
    setActionMessage(null);
    edit.mutate(
      { expectedRevision, manifest: nextDraft },
      {
        onSuccess: (result) => {
          setDraft(editableManifest(result.manifest));
          setExpectedRevision(result.pack.revision);
          setDirty(false);
          setActionError(null);
          setActionMessage(
            "草稿已保存；先前的验证和批准状态已由服务器按内容变化重置。",
          );
        },
        onError: recordFailure,
      },
    );
  }

  function applyTransition(kind: "validate" | "approve" | "disable") {
    setActionError(null);
    setActionMessage(null);
    const onSuccess = (result: RulePackMutationResult) =>
      completeAction(
        kind === "validate"
          ? "规则包验证完成。请检查服务端返回的验证摘要。"
          : kind === "approve"
            ? "规则包已由当前可信管理身份批准。"
            : "规则包已停用；已发布字节与生命周期记录仍保留。",
        result,
      );
    if (kind === "validate")
      validate.mutate(expectedRevision, { onSuccess, onError: recordFailure });
    else if (kind === "approve")
      approve.mutate(
        { expectedRevision, expectedContentHash: pack.content_hash },
        { onSuccess, onError: recordFailure },
      );
    else
      disable.mutate(
        {
          expectedRevision,
          expectedPointerRevision: pack.active_pointer_revision,
        },
        { onSuccess, onError: recordFailure },
      );
  }

  const actionPending =
    validate.isPending ||
    approve.isPending ||
    publish.isPending ||
    disable.isPending ||
    rollback.isPending;
  const editPending = edit.isPending;
  const showStaleWarning =
    dirty &&
    (expectedRevision !== pack.revision ||
      (edit.error !== null && isConflict(edit.error)));

  return (
    <section className="page page-rulepack-detail">
      <nav className="page-back" aria-label="规则包导航">
        <Link to="/admin/rulepacks">返回规则包</Link>
      </nav>
      <header className="page-head">
        <div>
          <h1>
            {pack.id}，v{pack.version}
          </h1>
          <p>
            租户 {pack.tenant_id}，状态 {pack.status}，行修订 {pack.revision}{" "}
          ，指针修订 {pack.active_pointer_revision}
            {pack.is_active ? "，当前生效" : "，当前未生效"}
          </p>
        </div>
        <Button
          aria-label={"基于此版本修改"}
          type="primary"
          href={`/admin/rulepacks/new?from_pack=${encodeURIComponent(pack.id)}&from_version=${encodeURIComponent(pack.version)}`}
        >
          基于此版本修改
        </Button>
      </header>
      <nav className="rulepack-detail-nav" aria-label="规则包详情章节">
        <a href="#rulepack-content">规则内容</a>
        <a href="#rulepack-provenance">版本来源</a>
        <a href="#rulepack-lifecycle">验证与发布</a>
        <a href="#rulepack-history">历史与回滚</a>
        <a href="#rulepack-test">规则试算</a>
        <a href="#rulepack-shadow">影子对比</a>
      </nav>
      {!isTenantOwner ? (
        <p className="workflow-error" role="alert">
          当前会话的可信租户与规则包不一致，页面只读。
        </p>
      ) : null}
      <section id="rulepack-provenance">
        <Collapse
          className="workflow-panel rulepack-provenance"
          items={[
            {
              key: "content",
              label: (
                <>
                  <h2 id="rulepack-provenance-title">版本与来源信息</h2>
                </>
              ),
              children: (
                <>
                  <dl className="rulepack-summary-grid">
                    <div>
                      <dt>内容摘要</dt>
                      <dd>
                        <code className="digest">{pack.content_hash}</code>
                      </dd>
                    </div>
                    <div>
                      <dt>保存版本的 SHA-256</dt>
                      <dd>
                        <code className="digest">{pack.snapshot_sha256}</code>
                      </dd>
                    </div>
                    <div>
                      <dt>有效期</dt>
                      <dd>
                        {pack.effective_from} — {pack.effective_until}
                      </dd>
                    </div>
                    <div>
                      <dt>规则依据来源</dt>
                      <dd>{pack.authority_sources.join("；") || "无"}</dd>
                    </div>
                    {manifest.lifecycle.verification ? (
                      <div>
                        <dt>验证</dt>
                        <dd>
                          {manifest.lifecycle.verification.passed
                            ? "通过"
                            : "未通过"}{" "}
                        ，{manifest.lifecycle.verification.verifier_version}，
                          {manifest.lifecycle.verification.manifest_hash}
                        </dd>
                      </div>
                    ) : null}
                    {manifest.lifecycle.approval ? (
                      <div>
                        <dt>批准</dt>
                        <dd>
                          操作者 {manifest.lifecycle.approval.actor_id}，内容{" "}
                          {manifest.lifecycle.approval.manifest_hash}
                        </dd>
                      </div>
                    ) : null}
                    {manifest.lifecycle.release ? (
                      <div>
                        <dt>发布证明</dt>
                        <dd>
                          发布摘要 {manifest.lifecycle.release.release_hash}
                        </dd>
                      </div>
                    ) : null}
                  </dl>
                </>
              ),
            },
          ]}
        />
      </section>
      {canEdit ? (
        <form
          className="workflow-form rulepack-form"
          id="rulepack-content"
          noValidate
          onSubmit={submitEdit}
        >
          <header className="workflow-panel-head">
            <div>
              <h2>编辑草稿内容</h2>
              <p className="meta">
                编辑只允许用于草稿版本。身份和版本固定；内容修改会使服务端重新要求验证与批准。
              </p>
            </div>
            <span
              className={
                dirty
                  ? "status-text status-conflict"
                  : "status-text status-active"
              }
            >
              {dirty ? "有未保存修改" : "与服务端一致"}
            </span>
          </header>
          <RulePackErrorSummary issues={issues} />
          {showStaleWarning ? (
            <div className="workflow-error" role="status">
              <p>
                你的本地修改仍保留。服务器当前为修订 {pack.revision}
               ，本地提交使用修订 {expectedRevision}。
              </p>
              <div className="workflow-links">
                <Button
                  aria-label={"重读服务器版本（保留本地修改）"}
                  htmlType="button"
                  disabled={detail.isFetching}
                  onClick={() => void detail.refetch()}
                >
                  重读服务器版本（保留本地修改）
                </Button>
                {expectedRevision !== pack.revision ? (
                  <Button
                    aria-label={
                      "下一次保存使用服务器修订 " + String(pack.revision)
                    }
                    htmlType="button"
                    onClick={() => setExpectedRevision(pack.revision)}
                  >
                    下一次保存使用服务器修订 {pack.revision}
                  </Button>
                ) : null}
              </div>
            </div>
          ) : null}
          <RulePackManifestEditor
            value={draft}
            tenantId={tenantId}
            lockedIdentity
            disabled={editPending}
            onChange={(next) => {
              setDraft(next);
              setDirty(true);
              setActionMessage(null);
            }}
          />
          <div className="dialog-actions">
            <Button
              aria-label={String(editPending ? "正在保存" : "保存草稿")}
              type="primary"
              htmlType="submit"
              disabled={editPending || !dirty || !hasCurrentRevision}
            >
              {editPending ? "正在保存" : "保存草稿"}
            </Button>
            {dirty && !hasCurrentRevision ? (
              <span className="meta">先读取并明确采用最新 CAS 修订号。</span>
            ) : null}
          </div>
        </form>
      ) : (
        <section
          className="workflow-panel"
          id="rulepack-content"
          aria-labelledby="rulepack-readonly-title"
        >
          <h2 id="rulepack-readonly-title">规则内容（只读）</h2>
          <p>
            只有草稿可以编辑。已验证、批准或发布版本保持内容不可变；如需修改，请创建新版本草稿。
          </p>
          <div className="rulepack-rules-readonly">
            {manifest.rules.map((rule, index) => (
              <Collapse
                className="rulepack-rule"
                key={`${rule.rule_id}:${index}`}
                items={[
                  {
                    key: "content",
                    label: (
                      <>
                        <strong>
                          {rule.domain}，{rule.capability_id}
                        </strong>
                        <span className="meta">
                          {rule.rule_id}，v{rule.version}，
                          {rule.mandatory ? "必检" : "非必检"}
                        </span>
                      </>
                    ),
                    children: (
                      <>
                        <dl className="rulepack-summary-grid">
                          <div>
                            <dt>规则</dt>
                            <dd>
                              {rule.rule_id} v{rule.version}，
                              {rule.mandatory ? "必检" : "非必检"}
                            </dd>
                          </div>
                          <div>
                            <dt>适用范围</dt>
                            <dd>
                              {rule.business_scope.business_line} /{" "}
                              {rule.business_scope.scenario} /{" "}
                              {rule.business_scope.contract_type}
                            </dd>
                          </div>
                          <div>
                            <dt>比较器与阈值</dt>
                            <dd>
                              {rule.comparator}，{rule.threshold.value}{" "}
                              {rule.threshold.unit}
                              {rule.threshold.basis
                                ? `，基数 ${rule.threshold.basis}`
                                : ""}
                            </dd>
                          </div>
                          <div>
                            <dt>所需事实</dt>
                            <dd>
                              {rule.required_fact.dimension}，角色{" "}
                              {rule.required_fact.source_roles.join("、")}，
                              单位族 {rule.required_fact.unit_family}
                            </dd>
                          </div>
                          <div>
                            <dt>规则依据来源</dt>
                            <dd>
                              {rule.authority_sources
                                .map(
                                  (source) =>
                                    `${source.id} v${source.version}，${source.kind}`,
                                )
                                .join("；")}
                            </dd>
                          </div>
                        </dl>
                      </>
                    ),
                  },
                ]}
              />
            ))}
          </div>
        </section>
      )}
      {actionError ? (
        <p
          className="workflow-error"
          role="alert"
          tabIndex={-1}
          ref={serverError}
        >
          {actionError}
          {isConflict(edit.error)
            ? " 本地草稿及表单值已保留；请重读服务器版本，并明确采用新修订号后再保存。"
            : ""}
        </p>
      ) : null}
      {actionMessage ? (
        <p className="callout" role="status">
          {actionMessage}
        </p>
      ) : null}
      <section
        className="workflow-panel"
        id="rulepack-lifecycle"
        aria-labelledby="rulepack-lifecycle-title"
      >
        <header className="workflow-panel-head">
          <div>
            <h2 id="rulepack-lifecycle-title">生命周期操作</h2>
            <p className="meta">
              每次操作都会检查规则版本和启用状态是否已被他人修改。发布和回滚使用请求标识，避免重复执行。
            </p>
          </div>
        </header>
        {dirty ? (
          <p className="callout">
            存在未保存草稿修改；先保存后才能验证、批准或发布。
          </p>
        ) : null}
        <div className="workflow-links">
          {pack.status === "draft" ? (
            <Button
              aria-label={String(validate.isPending ? "正在验证" : "验证草稿")}
              htmlType="button"
              disabled={
                !isTenantOwner || dirty || !hasCurrentRevision || actionPending
              }
              onClick={() => applyTransition("validate")}
            >
              {validate.isPending ? "正在验证" : "验证草稿"}
            </Button>
          ) : null}
          {pack.status === "validated" ? (
            <Button
              aria-label={String(
                approve.isPending ? "正在批准" : "批准此内容摘要",
              )}
              htmlType="button"
              disabled={
                !isTenantOwner || dirty || !hasCurrentRevision || actionPending
              }
              onClick={() => applyTransition("approve")}
            >
              {approve.isPending ? "正在批准" : "批准此内容摘要"}
            </Button>
          ) : null}
          {pack.status === "approved" ? (
            <Button
              aria-label={String(publish.isPending ? "正在发布" : "发布此版本")}
              type="primary"
              htmlType="button"
              disabled={
                !isTenantOwner || dirty || !hasCurrentRevision || actionPending
              }
              onClick={publishCurrent}
            >
              {publish.isPending ? "正在发布" : "发布此版本"}
            </Button>
          ) : null}
          {pack.status === "published" && pack.is_active ? (
            <Button
              aria-label={String(
                disable.isPending ? "正在停用" : "停用活动指针",
              )}
              htmlType="button"
              disabled={!isTenantOwner || !hasCurrentRevision || actionPending}
              onClick={() => applyTransition("disable")}
            >
              {disable.isPending ? "正在停用" : "停用活动指针"}
            </Button>
          ) : null}
          {replay ? (
            <Button
              aria-label={"重试上一次请求"}
              htmlType="button"
              disabled={actionPending}
              onClick={retryExactRequest}
            >
              重试上一次请求
            </Button>
          ) : null}
        </div>
        {pack.status === "approved" ? (
          <p className="meta">
            发布只影响之后新受理案件的规则版本。已有审查、执行记录和报告仍使用当时保存的版本。
          </p>
        ) : null}
      </section>
      <section
        className="workflow-panel"
        id="rulepack-history"
        aria-labelledby="rulepack-rollback-title"
      >
        <header className="workflow-panel-head">
          <div>
            <h2 id="rulepack-rollback-title">回滚活动指针</h2>
            <p className="meta">
              服务器只接受精确的、此前已发布的版本；回滚不会重写该版本内容或历史记录。
            </p>
          </div>
        </header>
        <div className="rulepack-fields">
          <label className="field" htmlFor="rulepack-rollback-version">
            目标历史版本
            <Select<string>
              aria-label={"目标历史版本"}
              id="rulepack-rollback-version"
              value={String(rollbackVersion)}
              onChange={(selectedValue) => setRollbackVersion(selectedValue)}
              disabled={!isTenantOwner || actionPending}
              options={[
                { value: "", label: "选择版本" },
                ...versionsInHistory.map((item) => ({
                  value: String(item),
                  label: (
                    <>
                      {pack.id}，v{item}
                    </>
                  ),
                })),
              ]}
            />
          </label>
        </div>
        <Button
          aria-label={String(
            rollback.isPending ? "正在回滚" : "回滚到所选版本",
          )}
          htmlType="button"
          disabled={
            !isTenantOwner ||
            rollbackVersion === "" ||
            !hasCurrentRevision ||
            actionPending
          }
          onClick={rollbackToSelected}
        >
          {rollback.isPending ? "正在回滚" : "回滚到所选版本"}
        </Button>
        {history.isLoading ? <p>正在读取操作历史。</p> : null}
        {history.isError ? (
          <p className="workflow-error" role="alert">
            历史记录无法读取：{errorMessage(history.error)}
          </p>
        ) : null}
        {historyEntries.length > 0 ? (
          <ol className="rulepack-history">
            {historyEntries.map((entry) => (
              <li key={entry.sequence}>
                <strong>
                  #{entry.sequence}，{entry.action}
                </strong>
                <span>
                  v{entry.version}，操作者 {entry.actor_id}，指针修订{" "}
                  {entry.pointer_revision}
                  {pointerTransition(entry)}
                </span>
                <time dateTime={entry.created_at}>{entry.created_at}</time>
                <code className="digest">{entry.content_hash}</code>
              </li>
            ))}
          </ol>
        ) : !history.isLoading && !history.isError ? (
          <p>还没有生命周期历史。</p>
        ) : null}
      </section>
      <RulePackSuitePanel packId={pack.id} version={pack.version} revision={pack.revision} contentHash={pack.content_hash} available={manifest.fixture_suite !== undefined} />
      <div id="rulepack-test">
        <RulePackTestPanel
          packId={pack.id}
          version={pack.version}
          manifest={manifest}
          revision={pack.revision}
        />
      </div>
      <RulePackShadowPanel
        key={`${pack.id}:${pack.version}:${pack.revision}`}
        packId={pack.id}
        version={pack.version}
        revision={pack.revision}
        contentHash={pack.content_hash}
        enabled={isTenantOwner && !dirty && hasCurrentRevision}
      />
      <DirtyLeaveGuard
        dirty={dirty}
        description="规则包草稿修改尚未保存。离开或刷新页面都会丢失这些修改，先保存草稿再离开。"
      />
    </section>
  );
}
