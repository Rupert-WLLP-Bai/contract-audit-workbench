import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { parseFrozenLegalReviewProjection } from "../api/legal-citations";
import { renderWithAntd as render } from "../test/antd";
import { FrozenLegalAuthorities } from "./frozen-legal-authorities";

function fixture(): unknown {
  return JSON.parse(readFileSync(resolve(process.cwd(), "../testdata/contracts/legal/frozen-legal-authorities.json"), "utf8")) as unknown;
}

function rawAuthority(): Record<string, unknown> {
  return (fixture() as { authorities: Record<string, unknown>[] }).authorities[0]!;
}

function mount(projection: ReturnType<typeof parseFrozenLegalReviewProjection> | undefined) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  return render(<QueryClientProvider client={client}>
    <FrozenLegalAuthorities caseId="case-wire" runId="run_wire_fixture" attemptId="attempt_wire_fixture" projection={projection} />
  </QueryClientProvider>);
}

afterEach(() => vi.unstubAllGlobals());

describe("frozen legal authorities review panel", () => {
  it("shows frozen applicability and missing dependencies, then reads the exact frozen citation", async () => {
    const projection = parseFrozenLegalReviewProjection(fixture());
    const authority = projection.authorities[0];
    expect(authority).toBeDefined();
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(rawAuthority()), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    mount(projection);

    expect(screen.getByText("适用状态：适用")).toBeInTheDocument();
    expect(screen.getByText("法务审核：未运行")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "读取本次冻结条文" }));
    expect(await screen.findByText("fixture-only body; not a legal quotation")).toBeInTheDocument();
    await waitFor(() => expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      `/api/cases/case-wire/runs/run_wire_fixture/attempts/attempt_wire_fixture/legal-citations/${authority!.citation_id}`,
    ));
  });

  it("does not backfill legacy workspaces without a frozen projection", () => {
    mount(undefined);
    expect(screen.getByText("本次审查未返回独立法律依据快照；不会用当前规则或案件资料补填。")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "读取本次冻结条文" })).not.toBeInTheDocument();
  });

  it("does not expose a citation action when the selected attempt differs", () => {
    const projection = parseFrozenLegalReviewProjection(fixture());
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    render(<QueryClientProvider client={client}>
      <FrozenLegalAuthorities caseId="case-wire" runId="another-run" attemptId="another-attempt" projection={projection} />
    </QueryClientProvider>);
    expect(screen.getByRole("alert")).toHaveTextContent("与当前选择的审查或执行记录不匹配");
    expect(screen.queryByRole("button", { name: "读取本次冻结条文" })).not.toBeInTheDocument();
  });
});
