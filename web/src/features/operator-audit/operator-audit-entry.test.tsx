import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { OperatorAuditEntry } from "./operator-audit-entry";

const mocks = vi.hoisted(() => ({
  useSettings: vi.fn(),
}));

vi.mock("../../api/queries", () => ({
  useSettings: mocks.useSettings,
}));

describe("OperatorAuditEntry", () => {
  beforeEach(() => {
    mocks.useSettings.mockReturnValue({
      data: { operator_audit: { enabled: false, connectors: [] } },
      isLoading: false,
    });
  });

  it("is absent when the server has not enabled the extension", () => {
    const { container } = render(
      <MemoryRouter>
        <OperatorAuditEntry
          caseId="case-1"
          runId="run-1"
          attemptId="attempt-1"
        />
      </MemoryRouter>,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("links to the versioned run and attempt when enabled", () => {
    mocks.useSettings.mockReturnValue({
      data: { operator_audit: { enabled: true, connectors: [] } },
      isLoading: false,
    });
    render(
      <MemoryRouter>
        <OperatorAuditEntry
          caseId="case-1"
          runId="run-1"
          attemptId="attempt-1"
        />
      </MemoryRouter>,
    );
    expect(
      screen.getByRole("link", { name: "进入跨系统核验" }),
    ).toHaveAttribute(
      "href",
      "/cases/case-1/operator-audit?run_id=run-1&attempt_id=attempt-1",
    );
  });
});
