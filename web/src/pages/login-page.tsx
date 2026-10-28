import { useState, type FormEvent } from "react";
import { Button, Input } from "antd";
import { Navigate, useNavigate } from "react-router";
import { errorMessage, isUnauthenticated } from "../api/http";
import { paths } from "../api/paths";
import { useLogin, useSession } from "../api/queries";
import type { SessionAudience } from "../api/types";
import { PageStatus } from "../components/page-status";
import { CircuitMark } from "../components/circuit-mark";
import { CompetitionIdentity } from "../components/competition-identity";

export function LoginPage({
  audience = "client",
}: {
  audience?: SessionAudience;
}) {
  const login = useLogin(audience);
  const session = useSession(audience);
  const navigate = useNavigate();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");

  function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    login.mutate(
      { username: username.trim(), password },
      {
        onSuccess: () =>
          navigate(audience === "admin" ? "/admin/settings" : "/cases", {
            replace: true,
          }),
      },
    );
  }

  if (session.isLoading)
    return <PageStatus kind="loading" title="正在确认登录状态" />;
  if (session.isError && !isUnauthenticated(session.error)) {
    return (
      <PageStatus
        kind="error"
        title="无法连接审查服务"
        detail={errorMessage(session.error)}
        action={
          <Button
            aria-label={"重试"}
            type="primary"
            onClick={() => void session.refetch()}
          >
            重试
          </Button>
        }
      />
    );
  }
  if (session.isSuccess)
    return (
      <Navigate
        to={audience === "admin" ? "/admin/settings" : "/cases"}
        replace
      />
    );

  return (
    <main className="login-screen" id="main">
      <aside className="login-rail">
        <div className="login-rail-brand competition-brand">
          {audience === "client" ? (
            <CompetitionIdentity />
          ) : (
            <>
              <CircuitMark size={34} />
              <strong>合同智能审计工作台</strong>
            </>
          )}
        </div>
        <p className="login-rail-copy">
          每次审查都会保存当时使用的材料、解析结果和规则。每项结论都需要有原文依据；审查人员修改判断时，之前的记录也会保留。
        </p>
        <ol className="login-rail-circuit" aria-label="审查链路示意">
          <li>材料</li>
          <li>解析</li>
          <li>引用</li>
          <li>决定</li>
        </ol>
      </aside>
      <form className="login-card" onSubmit={onSubmit}>
        {audience === "client" ? (
          <div className="login-mobile-brand competition-brand">
            <CompetitionIdentity />
          </div>
        ) : null}
        <h1>{audience === "admin" ? "登录管理工作台" : "登录合同审查"}</h1>
        <p>
          使用组织分配的账号。会话按当前工作区隔离；系统不在这里创建用户，也不会显示模型密钥。
        </p>
        <label className="field">
          账号
          <Input
            size="large"
            name="username"
            autoComplete="username"
            value={username}
            onChange={(event) => setUsername(event.target.value)}
            required
          />
        </label>
        <label className="field">
          密码
          <Input.Password
            size="large"
            name="password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            required
          />
        </label>
        {login.isError ? <p role="alert">{errorMessage(login.error)}</p> : null}
        <Button
          aria-label={String(login.isPending ? "正在登录" : "登录")}
          type="primary"
          size="large"
          htmlType="submit"
          disabled={login.isPending}
        >
          {login.isPending ? "正在登录" : "登录"}
        </Button>
        <Button
          aria-label={"通过组织身份提供方登录"}
          type="text"
          size="large"
          href={paths.oidcLogin(audience)}
        >
          通过组织身份提供方登录
        </Button>
      </form>
    </main>
  );
}
