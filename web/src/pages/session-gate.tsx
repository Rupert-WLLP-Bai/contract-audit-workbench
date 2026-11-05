// File metadata (manifest mirror):
// Purpose: Waits for the authenticated audience/session query before rendering protected routes, redirects unauthenticated users to login and clears private route state when identity expires.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-02
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { useLayoutEffect, useRef } from "react";
import { Button } from "antd";
import { useQueryClient } from "@tanstack/react-query";
import { Navigate, Outlet, useLocation } from "react-router";
import {
  errorMessage,
  isUnauthenticated,
  isForbidden,
  setSessionIdentity,
} from "../api/http";
import { clearIdentityQueries, queryKeys, useSession } from "../api/queries";
import type { SessionAudience } from "../api/types";
import { PageStatus } from "../components/page-status";
import { clearAllDecisionDrafts } from "../hooks/decision-drafts";
import { clearAllEvidenceDrafts } from "../features/review/evidence-request-drafts";
import { actorReadContextKey } from "../domain/actor-read-context";

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
  const previousReadScope = useRef<string | null>(null);

  useLayoutEffect(() => {
    if (!actor) return;
    const readScope = actorReadContextKey(actor);
    if (previousReadScope.current && previousReadScope.current !== readScope) clearAllEvidenceDrafts();
    previousReadScope.current = readScope;
    if (setSessionIdentity(actor)) {
      clearAllDecisionDrafts();
      clearAllEvidenceDrafts();
      clearIdentityQueries(queryClient);
      queryClient.setQueryData(queryKeys.sessionFor(audience), actor);
    }
  }, [actor, audience, queryClient]);

  useLayoutEffect(() => {
    if ((session.isError && (isUnauthenticated(session.error) || isForbidden(session.error))) ||
        (actor && actor.audience !== audience)) clearAllEvidenceDrafts();
  }, [session.isError, session.error, actor, audience]);

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
      key={`${actor?.audience ?? ""}\u0000${actor?.id ?? ""}\u0000${actor?.role ?? ""}\u0000${actor?.tenant_id ?? ""}\u0000${actor?.org_id ?? ""}\u0000${actor?.active_context?.context_version ?? ""}\u0000${actor?.csrf_token ?? ""}`}
    />
  );
}
