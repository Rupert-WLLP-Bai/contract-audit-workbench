import { useState, type FormEvent } from "react";
import { Button, Input } from "antd";
import { Navigate, useLocation, useNavigate } from "react-router";
import { errorMessage, isUnauthenticated } from "../api/http";
import { paths } from "../api/paths";
import { useLogin, useSession } from "../api/queries";
import type { SessionAudience } from "../api/types";
import { PageStatus } from "../components/page-status";

function returnPath(
  state: unknown,
  audience: SessionAudience,
): string {
  const fallback = audience === "admin" ? "/admin/settings" : "/dashboard";
  if (typeof state !== "object" || state === null) return fallback;
  const requested = (state as { from?: unknown }).from;
  if (typeof requested !== "string" || !requested.startsWith("/")) return fallback;
  if (requested.startsWith("//")) return fallback;
  const loginPath = audience === "admin" ? "/admin/login" : "/login";
  if (requested === loginPath) return fallback;
  if (audience === "admin" && !requested.startsWith("/admin")) return fallback;
  if (audience === "client" && requested.startsWith("/admin")) return fallback;
  return requested;
}

export function LoginPage({
  audience = "client",
}: {
  audience?: SessionAudience;
}) {
  const login = useLogin(audience);
  const session = useSession(audience);
  const navigate = useNavigate();
  const location = useLocation();
  const from = returnPath(location.state, audience);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");

  function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    login.mutate(
      { username: username.trim(), password },
      {
        onSuccess: () => navigate(from, { replace: true }),
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
  if (session.isSuccess) return <Navigate to={from} replace />;

  return (
    <main className="login-screen" id="main">
      <form className="login-card" onSubmit={onSubmit}>
        <h1>{audience === "admin" ? "登录管理工作台" : "登录合同审查"}</h1>
        <p>
          使用组织分配的账号。会话按当前工作区隔离；系统不在这里创建用户，也不会显示模型密钥。
        </p>
        <label className="field">
          账号
          <Input
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
          htmlType="submit"
          disabled={login.isPending}
        >
          {login.isPending ? "正在登录" : "登录"}
        </Button>
        <Button
          aria-label={"通过组织身份提供方登录"}
          type="text"
          href={paths.oidcLogin(audience)}
        >
          通过组织身份提供方登录
        </Button>
      </form>
    </main>
  );
}
