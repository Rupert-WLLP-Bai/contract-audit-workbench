import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { MemoryRouter } from "react-router";
import { queryKeys } from "../api/queries";
import type { WorkbenchCase, WorkbenchSnapshot } from "../api/types";
import { renderWithAntd as render } from "../test/antd";
import { ReviewEntryPage } from "./review-entry-page";

const at = "2026-10-21T06:00:00Z";
const clients: QueryClient[] = [];

function snapshot(): WorkbenchSnapshot {
  const reviewCase: WorkbenchCase = {
    id: "case/alpha",
    title: "专线合同审查",
    contract_type: "service",
    document_count: 2,
    pending_decisions: 1,
    latest_run: null,
    updated_at: at,
    profile: null,
    severity: null,
    progress: 50,
  };
  return {
    schema_version: "workbench/v1",
    generated_at: at,
    period: "week",
    dashboard: {
      visible_cases: 1,
      my_pending_tasks: 0,
      overdue_tasks: 0,
      pending_review_cases: 1,
      remediation_cases: 0,
      completed_reviews: 2,
      average_duration_ms: null,
      progress: [],
      risks: [],
    },
    cases: [reviewCase],
    profiles: [],
    tasks: [],
    relations: [],
    knowledge: [],
    members: [],
    remediations: [],
    reports: [],
    documents: [],
    runs: [
      {
        id: "run-old",
        case_id: reviewCase.id,
        case_title: reviewCase.title,
        state: "completed",
        created_at: at,
        updated_at: at,
        attempt_id: "attempt-old",
        started_at: at,
        finished_at: at,
        usage: null,
      },
      {
        id: "run-new",
        case_id: reviewCase.id,
        case_title: reviewCase.title,
        state: "partial",
        created_at: "2026-10-22T06:00:00Z",
        updated_at: "2026-10-22T06:00:00Z",
        attempt_id: "attempt/new",
        started_at: "2026-10-22T06:00:00Z",
        finished_at: "2026-10-22T06:01:00Z",
        usage: null,
      },
    ],
    audit: [],
    templates: [],
    model: {
      name: "",
      ocr_name: "",
      status: "unconfigured",
      label: "规则模式",
    },
    tools: [],
  };
}

afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
});

describe("审查入口", () => {
  it("opens the newest run with its exact frozen attempt id", () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    clients.push(client);
    client.setQueryData(queryKeys.workbench("week"), snapshot());

    render(
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={["/reviews"]}>
          <ReviewEntryPage />
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(screen.getByRole("link", { name: "打开审查" })).toHaveAttribute(
      "href",
      "/cases/case%2Falpha/reviews/run-new?attempt_id=attempt%2Fnew",
    );
  });
});
