// File metadata (manifest mirror):
// Purpose: Presents client and admin sign-in, validates return routes, and sends an ordinary client login to the personal workbench.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-20
// Revision: r4
// Copyright: See LICENSE
// End file metadata.

import { useState, type FormEvent } from "react";
import { Button, Input } from "antd";
import { Navigate, useLocation, useNavigate } from "react-router";
import { errorMessage, isUnauthenticated } from "../api/http";
import { paths } from "../api/paths";
import { useLogin, useSession } from "../api/queries";
import type { SessionAudience } from "../api/types";
import { PageStatus } from "../components/page-status";
import { CircuitMark } from "../components/circuit-mark";
import "./login-page.css";

export function LoginPage({ audience = "client" }: { audience?: SessionAudience }) {
  const login = useLogin(audience);
  const session = useSession(audience);
  const navigate = useNavigate();
  const location = useLocation();
  const navigationState: unknown = location.state;
  const from =
    navigationState !== null && typeof navigationState === "object" && "from" in navigationState
      ? navigationState.from
      : undefined;
  const returnTo =
    typeof from === "string" &&
    from.startsWith("/") &&
    !from.startsWith("//") &&
    !from.includes("\\") &&
    (audience === "admin"
      ? from.startsWith("/admin/") && !from.startsWith("/admin/login")
      : !from.startsWith("/admin") && !from.startsWith("/login"))
      ? from
      : audience === "admin"
        ? "/admin/settings"
        : "/";
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");

  function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    login.mutate(
      { username: username.trim(), password },
      {
        onSuccess: () => navigate(returnTo, { replace: true }),
      },
    );
  }

  if (session.isLoading) return <PageStatus kind="loading" title="正在确认登录状态" />;
  if (session.isError && !isUnauthenticated(session.error)) {
    return (
      <PageStatus
        kind="error"
        title="无法连接审查服务"
        detail={errorMessage(session.error)}
        action={
          <Button aria-label={"重试"} type="primary" onClick={() => void session.refetch()}>
            重试
          </Button>
        }
      />
    );
  }
  if (session.isSuccess) return <Navigate to={returnTo} replace />;

  return (
    <main className="login-screen" id="main">
      <div className="login-layout">
        <form className="login-card" onSubmit={onSubmit}>
          <div className="login-brand">
            <CircuitMark size={34} />
            <strong>{audience === "admin" ? "合同智能审计工作台" : "合同审计工作台"}</strong>
          </div>
          <header className="login-heading">
            <h1>{audience === "admin" ? "登录管理后台" : "登录"}</h1>
          </header>
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
          {login.isError ? (
            <p className="login-error" role="alert">
              {errorMessage(login.error)}
            </p>
          ) : null}
          <Button
            aria-label={String(login.isPending ? "正在登录" : "登录")}
            type="primary"
            htmlType="submit"
            disabled={login.isPending}
          >
            {login.isPending ? "正在登录" : "登录"}
          </Button>
          <Button aria-label={"企业单点登录"} type="text" href={paths.oidcLogin(audience)}>
            企业单点登录
          </Button>
        </form>
        {audience === "client" ? (
          <p className="login-competition-name">第一届黄桷树AI智能体开发大赛</p>
        ) : null}
      </div>
    </main>
  );
}
