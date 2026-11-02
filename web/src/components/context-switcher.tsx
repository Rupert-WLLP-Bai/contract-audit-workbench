import { useState } from "react";
import { Alert, Button, Select, Space, Typography } from "antd";
import { useQueryClient } from "@tanstack/react-query";
import { ApiError, captureCommandContext, captureCommandContextIfAvailable, invalidateSessionIdentity } from "../api/http";
import { clearIdentityQueries, queryKeys, revalidateSessionContext, useContextChoices, useSwitchContext } from "../api/queries";
import type { Actor, SessionAudience } from "../api/types";

export function ContextSwitcher({
  actor,
  audience,
  onTransition,
}: {
  actor: Actor | undefined;
  audience: SessionAudience;
  onTransition: (pending: boolean) => void;
}) {
  const choices = useContextChoices(audience);
  const switching = useSwitchContext(audience);
  const queryClient = useQueryClient();
  const [error, setError] = useState<string | null>(null);
  const [revalidationRequired, setRevalidationRequired] = useState(false);

  async function retryRevalidation() {
    setError(null);
    try {
      await revalidateSessionContext(queryClient, audience);
      setRevalidationRequired(false);
      onTransition(false);
    } catch (cause) {
      invalidateSessionIdentity();
      clearIdentityQueries(queryClient);
      await queryClient.resetQueries({ queryKey: queryKeys.sessionFor(audience) });
      setError(cause instanceof ApiError ? cause.message : "无法确认当前登录上下文，请稍后重试");
      setRevalidationRequired(true);
    }
  }

  if (!actor?.active_context) {
    return revalidationRequired ? (
      <Alert
        type="error"
        showIcon
        title={error ?? "上下文尚未重新确认"}
        action={<Button htmlType="button" onClick={() => void retryRevalidation()}>重新确认</Button>}
      />
    ) : null;
  }
  if (revalidationRequired) {
    return (
      <Alert
        type="error"
        showIcon
        title={error ?? "上下文尚未重新确认"}
        action={<Button htmlType="button" onClick={() => void retryRevalidation()}>重新确认</Button>}
      />
    );
  }

  const enabled = actor.context_switch_enabled && choices.data?.context_switch_enabled !== false;
  if (!enabled) {
    return (
      <span className="organization-badge" title={actor.active_context.org_name}>
        <span className="organization-badge-label">租户：</span>
        <span className="organization-badge-value">{actor.active_context.tenant_name}</span>
      </span>
    );
  }

  const options = (choices.data?.memberships ?? []).map((membership) => ({
    value: membership.id,
    label: `${membership.tenant_name} · ${membership.org_name}`,
  }));
  const currentId = actor.active_context.membership_id ?? undefined;
  return (
      <Space orientation="vertical" size={2}>
      <Select
        aria-label="切换工作上下文"
        value={currentId}
        placeholder={`${actor.active_context.tenant_name} · ${actor.active_context.org_name}`}
        options={options}
        loading={choices.isLoading || switching.isPending}
        disabled={switching.isPending || choices.isError}
        style={{ minWidth: 220 }}
        onChange={(membershipId: string) => {
          if (membershipId === currentId) return;
          const context = captureCommandContext(actor);
          setError(null);
          setRevalidationRequired(false);
          onTransition(true);
          void switching.mutateAsync({ membershipId, context })
            .then(() => {
              setRevalidationRequired(false);
              onTransition(false);
            })
            .catch((cause: unknown) => {
              setError(cause instanceof ApiError ? cause.message : "切换未完成，请重新确认当前上下文");
              if (captureCommandContextIfAvailable()) onTransition(false);
              else setRevalidationRequired(true);
            })
        }}
      />
      {revalidationRequired ? (
        <Alert type="error" showIcon title={error ?? "上下文尚未重新确认"} action={<Button htmlType="button" onClick={() => void retryRevalidation()}>重新确认</Button>} />
      ) : error ? <Alert type="error" showIcon title={error} /> : null}
      <Typography.Text className="sr-only">
        当前组织：{actor.active_context.org_name}
      </Typography.Text>
    </Space>
  );
}
