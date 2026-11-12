// File metadata (manifest mirror):
// Purpose: Verifies single-request lifecycle rendering, unknown values, access loss, version mismatch and stale-response isolation across account and authorization-context changes.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-03
// Updated: 2026-11-08
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { act, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ContractDetail, ContractLifecycleOverview } from "../api/contracts";
import type { Actor } from "../api/types";
import { renderWithAntd as render } from "../test/antd";
import { ContractLifecycleOverviewPanel } from "./contract-lifecycle-overview-panel";

const contractId = "0f7d5c1e-4a2b-4c3d-8e9f-0123456789ab";
const revisionId = "1f7d5c1e-4a2b-4c3d-8e9f-0123456789ab";

const actor = (id = "owner"): Actor => ({
  id,
  display_name: "审查人",
  role: "admin",
  tenant_id: "tenant",
  org_id: "org",
  audience: "client",
  principal_id: `principal-${id}`,
  active_context: {
    membership_id: "member",
    tenant_id: "tenant",
    tenant_name: "租户",
    org_id: "org",
    org_name: "组织",
    context_version: "1",
  },
  csrf_token: "test-csrf",
  context_switch_enabled: false,
});

const detail = {
  summary: {
    contract_id: contractId,
    authority_mode: "native",
    name: "示例合同",
    number: null,
    amount: null,
    currency: null,
    contract_state: "performing",
    workflow_state: "approved",
    row_version: 2,
    created_at: "2026-11-01T00:00:00Z",
  },
  current_revision_id: revisionId,
  active_round_id: null,
  allowed_commands: [],
  warnings: [],
} as ContractDetail;

function overview(overrides: Partial<ContractLifecycleOverview> = {}): ContractLifecycleOverview {
  return {
    contract: {
      contract_id: contractId,
      current_revision_id: revisionId,
      row_version: 2,
      authority_mode: "native",
    },
    observed_at: "2026-11-03T01:02:03Z",
    completeness: {
      plans: "unknown",
      deliveries: "unknown",
      acceptances: "unknown",
      payments: "unknown",
      assets: "unknown",
      disputes: "unknown",
      warranty: "unknown",
      other_obligations: "unknown",
    },
    source_observed_at: { contract_recorded_at: null, finance: null, erp: null },
    plans: null,
    deliveries: { items: [], row_version: 2, summary: [] },
    acceptances: { items: [], row_version: 2, summary: [] },
    payments: null,
    assets: { contract_id: contractId, fixed_asset_status: "unknown", items: [], row_version: 2 },
    ...overrides,
  };
}

function response(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => vi.unstubAllGlobals());

describe("ContractLifecycleOverviewPanel", () => {
  it("reads one lifecycle endpoint and keeps missing sources unverified", async () => {
    const fetchMock = vi.fn().mockResolvedValue(response(overview()));
    vi.stubGlobal("fetch", fetchMock);
    render(<ContractLifecycleOverviewPanel detail={detail} actor={actor()} />);

    expect(await screen.findByText("计划日期尚未核实。")).toBeInTheDocument();
    expect(screen.getByText(/争议状态：尚未核实/)).toBeInTheDocument();
    expect(screen.getByText(/本次读取时间：2026-11-03T01:02:03Z/)).toBeInTheDocument();
    await waitFor(() =>
      expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
        `/api/contracts/${contractId}/lifecycle-overview`,
      ),
    );
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("labels confirmed empty collections as none while retaining unverified archive facts", async () => {
    const value = overview({
      completeness: {
        plans: "known",
        deliveries: "known",
        acceptances: "known",
        payments: "known",
        assets: "known",
        disputes: "unknown",
        warranty: "unknown",
        other_obligations: "unknown",
      },
      plans: { delivery_plans: [], acceptance_plans: [], payments: [], assets: [] },
      payments: [],
    });
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response(value)));
    render(<ContractLifecycleOverviewPanel detail={detail} actor={actor()} />);

    const deliveries = await screen.findByRole("region", { name: "交付事实" });
    const acceptances = screen.getByRole("region", { name: "验收事实" });
    const payments = screen.getByRole("region", { name: "付款事实" });
    const assets = screen.getByRole("region", { name: "资产事实" });
    expect(deliveries).toHaveTextContent("暂无");
    expect(acceptances).toHaveTextContent("暂无");
    expect(payments).toHaveTextContent("暂无");
    expect(assets).toHaveTextContent("暂无");
    expect(screen.getByText("还没有计划日期。")).toBeInTheDocument();
    expect(screen.getByText(/争议状态：尚未核实/)).toBeInTheDocument();
  });

  it.each([401, 403, 404])(
    "clears the projection and invalidates the parent on HTTP %s",
    async (status) => {
      const fetchMock = vi.fn().mockResolvedValue(response({ error: "denied" }, status));
      vi.stubGlobal("fetch", fetchMock);
      const onAccessDenied = vi.fn();
      render(
        <ContractLifecycleOverviewPanel
          detail={detail}
          actor={actor()}
          onAccessDenied={onAccessDenied}
        />,
      );

      expect(await screen.findByText("当前无法查看这份概览")).toBeInTheDocument();
      await waitFor(() => expect(onAccessDenied).toHaveBeenCalledTimes(1));
      expect(screen.queryByText("还没有计划日期。")).not.toBeInTheDocument();
    },
  );

  it("hides all facts when the response contract revision or row version is stale", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      response(
        overview({
          contract: {
            contract_id: contractId,
            current_revision_id: revisionId,
            row_version: 3,
            authority_mode: "native",
          },
          completeness: {
            plans: "known",
            deliveries: "known",
            acceptances: "known",
            payments: "known",
            assets: "known",
            disputes: "unknown",
            warranty: "unknown",
            other_obligations: "unknown",
          },
          plans: { delivery_plans: [], acceptance_plans: [], payments: [], assets: [] },
          deliveries: {
            items: [
              {
                delivery_id: "delivery-1",
                contract_id: contractId,
                contract_revision_id: revisionId,
                item_name: "受保护事实",
                actual_at: "2026-11-02T00:00:00Z",
                item_id: "item-1",
                batch_reference: "b1",
                quantity: "1",
                unit: "件",
                location: "",
                description: "",
                evidence: [],
                recorded_by: "owner",
                recorded_at: "2026-11-02T00:00:00Z",
              },
            ],
            row_version: 3,
            summary: [],
          },
          acceptances: { items: [], row_version: 3, summary: [] },
          assets: {
            contract_id: contractId,
            fixed_asset_status: "unknown",
            items: [],
            row_version: 3,
          },
        }),
      ),
    );
    vi.stubGlobal("fetch", fetchMock);
    render(<ContractLifecycleOverviewPanel detail={detail} actor={actor()} />);

    expect(await screen.findByText("合同版本已更新，请刷新后再看")).toBeInTheDocument();
    expect(screen.queryByText(/受保护事实/)).not.toBeInTheDocument();
  });

  it.each(["account", "principal", "membership", "version"])(
    "aborts the old read after a %s context change and ignores its late result",
    async (change) => {
      const signals: (AbortSignal | undefined)[] = [];
      const resolvers: ((value: Response) => void)[] = [];
      const fetchMock = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
        signals.push(init?.signal ?? undefined);
        return new Promise<Response>((resolve) => {
          resolvers.push(resolve);
        });
      });
      vi.stubGlobal("fetch", fetchMock);
      const first = actor("first");
      const second: Actor =
        change === "account"
          ? actor("second")
          : {
              ...first,
              principal_id: change === "principal" ? "principal-second" : first.principal_id,
              active_context: {
                ...first.active_context,
                membership_id:
                  change === "membership" ? "member-second" : first.active_context.membership_id,
                context_version: change === "version" ? "2" : first.active_context.context_version,
              },
            };
      const view = render(<ContractLifecycleOverviewPanel detail={detail} actor={first} />);
      await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
      view.rerender(<ContractLifecycleOverviewPanel detail={detail} actor={second} />);
      await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
      await waitFor(() => expect(signals[0]?.aborted).toBe(true));
      await act(async () => {
        resolvers[0]?.(
          response(
            overview({
              observed_at: "2030-01-01T00:00:00Z",
              completeness: {
                plans: "known",
                deliveries: "known",
                acceptances: "known",
                payments: "known",
                assets: "known",
                disputes: "unknown",
                warranty: "unknown",
                other_obligations: "unknown",
              },
            }),
          ),
        );
        await new Promise((resolve) => setTimeout(resolve, 0));
      });
      expect(screen.queryByText(/2030-01-01T00:00:00Z/)).not.toBeInTheDocument();
    },
  );
});
