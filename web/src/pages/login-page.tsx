import { useState, type FormEvent } from "react";
import { Navigate, useNavigate } from "react-router";
import { errorMessage, isUnauthenticated } from "../api/http";
import { paths } from "../api/paths";
import { useLogin, useSession } from "../api/queries";
import type { SessionAudience } from "../api/types";
import { PageStatus } from "../components/page-status";

export function LoginPage({ audience = "client" }: { audience?: SessionAudience }) {
  const login = useLogin(audience);
  const session = useSession(audience);
  const navigate = useNavigate();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");

  function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    login.mutate({ username: username.trim(), password }, {
      onSuccess: () => navigate(audience === "admin" ? "/admin/settings" : "/cases", { replace: true }),
    });
  }

  if (session.isLoading) return <PageStatus kind="loading" title="正在确认登录状态" />;
  if (session.isError && !isUnauthenticated(session.error)) {
    return <PageStatus kind="error" title="无法连接审查服务" detail={errorMessage(session.error)} action={<button className="button button-primary" type="button" onClick={() => void session.refetch()}>重试</button>} />;
  }
  if (session.isSuccess) return <Navigate to={audience === "admin" ? "/admin/settings" : "/cases"} replace />;

  return (
    <main className="login-screen" id="main">
      <form className="login-card" onSubmit={onSubmit}>
        <h1>{audience === "admin" ? "登录管理工作台" : "登录合同审查"}</h1>
        <p>使用组织分配的账号。会话按当前工作区隔离；系统不在这里创建用户，也不会显示模型密钥。</p>
        <label className="field">
          账号
          <input
            name="username"
            autoComplete="username"
            value={username}
            onChange={(event) => setUsername(event.target.value)}
            required
          />
        </label>
        <label className="field">
          密码
          <input
            name="password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            required
          />
        </label>
        {login.isError ? <p role="alert">{errorMessage(login.error)}</p> : null}
        <button className="button button-primary" type="submit" disabled={login.isPending}>
          {login.isPending ? "正在登录" : "登录"}
        </button>
        <a className="button button-quiet" href={paths.oidcLogin(audience)}>通过组织身份提供方登录</a>
      </form>
    </main>
  );
}
