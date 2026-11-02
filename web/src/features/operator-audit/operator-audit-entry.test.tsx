// File metadata (manifest mirror):
// Purpose: Checks the web behavior for “OperatorAuditEntry”; representative assertions cover is absent when the server has not enabled the extension, links to the versioned run and attempt when enabled. It protects the rendered/API state expected by this component without starting the full browser journey.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-29
// Updated: 2026-10-29
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

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
