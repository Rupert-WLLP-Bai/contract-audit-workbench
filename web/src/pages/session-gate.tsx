import { useLayoutEffect } from "react";
import { Button } from "antd";
import { useQueryClient } from "@tanstack/react-query";
import { Navigate, Outlet, useLocation } from "react-router";
import {
  errorMessage,
  isUnauthenticated,
  setSessionIdentity,
} from "../api/http";
import { clearIdentityQueries, queryKeys, useSession } from "../api/queries";
import type { SessionAudience } from "../api/types";
import { PageStatus } from "../components/page-status";

export function SessionGate({
  audience = "client",
  loginPath = "/login",
}: {
  audience?: SessionAudience;
  loginPath?: string;
}) {
  const location = useLocation();
  const session = useSession(audience);
  const queryClient = useQueryClient();
  const actor = session.data;

  useLayoutEffect(() => {
    if (!actor) return;
    if (setSessionIdentity(actor)) {
      clearIdentityQueries(queryClient);
      queryClient.setQueryData(queryKeys.sessionFor(audience), actor);
    }
  }, [actor, audience, queryClient]);

  if (session.isLoading) {
    return <PageStatus kind="loading" title="正在确认登录状态" />;
  }

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

  if (actor && actor.audience !== audience) {
    return (
      <PageStatus
        kind="denied"
        title="当前会话属于其他工作区"
        detail="请从对应的客户端、管理端或接口门户入口登录。"
      />
    );
  }
  if (!session.isSuccess) {
    return (
      <Navigate to={loginPath} replace state={{ from: location.pathname + location.search + location.hash }} />
    );
  }
  return (
    <Outlet
      key={`${actor?.audience ?? ""}\u0000${actor?.id ?? ""}\u0000${actor?.role ?? ""}`}
    />
  );
}
