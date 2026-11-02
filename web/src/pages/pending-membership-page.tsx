import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Button, Radio } from "antd";
import { ApiError, isForbidden, isUnauthenticated } from "../api/http";
import {
  listPendingMemberships,
  selectPendingMembership,
  type MembershipChoice,
  type PendingMembershipAudience,
} from "../api/pending-memberships";
import { paths } from "../api/paths";
import { CircuitMark } from "../components/circuit-mark";
import { CompetitionIdentity } from "../components/competition-identity";

function isExpired(error: unknown): boolean {
  return (
    isUnauthenticated(error) ||
    isForbidden(error) ||
    (error instanceof ApiError && error.status === 404)
  );
}

function safeRequestMessage(error: unknown): string {
  if (error instanceof ApiError && error.status === 0)
    return "暂时无法连接身份服务，请检查网络后重试。";
  return "身份服务暂时无法完成请求，请稍后重试。";
}

export function PendingMembershipPage({
  audience,
}: {
  audience: PendingMembershipAudience;
}) {
  const [memberships, setMemberships] = useState<MembershipChoice[] | null>(null);
  const [selectedId, setSelectedId] = useState("");
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [expired, setExpired] = useState(false);
  const [loadError, setLoadError] = useState("");
  const [submitError, setSubmitError] = useState("");
  const generation = useRef(0);
  const submittingRef = useRef(false);
  const controllerRef = useRef<AbortController | null>(null);

  const load = useCallback(async () => {
    const requestGeneration = ++generation.current;
    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;
    setLoading(true);
    setMemberships(null);
    setSelectedId("");
    setExpired(false);
    setLoadError("");
    setSubmitError("");
    try {
      const result = await listPendingMemberships(audience, controller.signal);
      if (generation.current !== requestGeneration) return;
      setMemberships(result.memberships);
      if (result.memberships.length === 0) setExpired(true);
    } catch (error) {
      if (generation.current !== requestGeneration) return;
      setMemberships([]);
      if (isExpired(error)) setExpired(true);
      else if (!(error instanceof Error && error.name === "AbortError"))
        setLoadError(safeRequestMessage(error));
    } finally {
      if (generation.current === requestGeneration) setLoading(false);
    }
  }, [audience]);

  useEffect(() => {
    void load();
    return () => {
      generation.current += 1;
      controllerRef.current?.abort();
    };
  }, [load]);

  async function submitSelection() {
    if (submittingRef.current || !selectedId || !memberships?.some((item) => item.id === selectedId)) return;
    const requestGeneration = generation.current;
    submittingRef.current = true;
    setSubmitting(true);
    setSubmitError("");
    try {
      await selectPendingMembership(audience, selectedId);
      if (generation.current !== requestGeneration) return;
      // The server has atomically consumed the pending challenge and set the
      // audience session cookie. Reload the fixed application entry point.
      window.location.replace(audience === "admin" ? "/admin" : "/");
    } catch (error) {
      if (generation.current !== requestGeneration) return;
      setMemberships([]);
      setSelectedId("");
      if (isExpired(error)) {
        setExpired(true);
      } else {
        setSubmitError(safeRequestMessage(error));
      }
    } finally {
      submittingRef.current = false;
      if (generation.current === requestGeneration) setSubmitting(false);
    }
  }

  const loginPath = audience === "admin" ? "/admin/login" : "/login";
  const audienceLabel = audience === "admin" ? "管理工作台" : "合同审查工作台";

  return (
    <main className="login-screen" id="main">
      <section className="login-card" aria-labelledby="membership-heading">
        <div className="login-mobile-brand competition-brand">
          {audience === "client" ? <CompetitionIdentity /> : <><CircuitMark size={34} /><strong>合同智能审计工作台</strong></>}
        </div>
        <h1 id="membership-heading">选择组织身份</h1>
        <p>请选择进入{audienceLabel}时使用的组织身份。选项由服务端根据本次登录提供。</p>
        {expired ? (
          <Alert
            type="warning"
            showIcon
            title="本次组织登录已过期或没有可用身份，请重新登录。"
            action={<Button href={paths.oidcLogin(audience)}>重新登录</Button>}
          />
        ) : null}
        {loading ? <p role="status">正在读取可用组织身份…</p> : null}
        {loadError ? (
          <Alert
            type="error"
            showIcon
            title="暂时无法读取组织身份"
            description={loadError}
            action={<Button onClick={() => void load()}>重试</Button>}
          />
        ) : null}
        {submitError ? (
          <Alert
            type="error"
            showIcon
            title="没有完成身份选择"
            description={submitError}
            action={<Button onClick={() => void load()}>重新读取身份</Button>}
          />
        ) : null}
        {!loading && !expired && !loadError && memberships && memberships.length > 0 ? (
          <>
            <Radio.Group
              aria-label="可用组织身份"
              value={selectedId}
              onChange={(event) => setSelectedId(event.target.value as string)}
              disabled={submitting}
              style={{ display: "grid", gap: "var(--space-2)" }}
            >
              {memberships.map((membership) => (
                <Radio
                  key={membership.id}
                  value={membership.id}
                  style={{
                    display: "flex",
                    alignItems: "flex-start",
                    gap: "var(--space-2)",
                    padding: "var(--space-3)",
                    border: "1px solid var(--border-default)",
                    borderRadius: 6,
                    background: "var(--surface-raised)",
                  }}
                >
                  <span>
                    <strong>{membership.tenant_name}</strong>
                    <span style={{ display: "block" }}>{membership.org_name} · {membership.role}</span>
                  </span>
                </Radio>
              ))}
            </Radio.Group>
            <Button
              type="primary"
              size="large"
              loading={submitting}
              disabled={!selectedId || submitting}
              onClick={() => void submitSelection()}
            >
              进入工作台
            </Button>
          </>
        ) : null}
        <Button type="link" href={loginPath}>返回登录页面</Button>
      </section>
    </main>
  );
}
