import { screen, within } from "@testing-library/react";
import userEvent, { type UserEvent } from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { describe, expect, it } from "vitest";
import type { ReviewWorkspace } from "../api/types";
import { renderWithAntd as render } from "../test/antd";
import { RunProvenance } from "./run-provenance";

const at = "2026-10-21T06:00:00Z";

function workspace(overrides: Partial<ReviewWorkspace> = {}): ReviewWorkspace {
  return {
    run: {
      id: "run-1",
      case_id: "case-1",
      state: "completed",
      updated_at: at,
      started_at: at,
      model_ref: null,
      partial_reason: null,
      failure_message: null,
    },
    documents: [],
    findings: [],
    review_attempts: null,
    execution: null,
    audit_events: [],
    ...overrides,
  } as ReviewWorkspace;
}

const evaluation = {
  id: "evaluation-1",
  domain: "履约",
  relation: "conflict",
  rule_version: "v2",
  reason: "期限不一致",
  rule_id: "rule-1",
  capability_id: "capability-1",
  object_refs: ["object-1", "object-2"],
  finding_id: "finding-1",
  reason_code: "date_mismatch",
  fact_ids: ["fact-1"],
};

async function open(user: UserEvent, name: string) {
  await user.click(screen.getByRole("button", { name }));
}

describe("run provenance", () => {
  it("says when neither the snapshot nor evaluations were returned", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <RunProvenance workspace={workspace()} />
      </MemoryRouter>,
    );
    await open(user, "运行依据与规则评估");
    expect(
      screen.getByText("未获取到本次审查的材料和规则记录。"),
    ).toBeInTheDocument();
    expect(screen.getByText("没有返回规则评估记录。")).toBeInTheDocument();
    expect(
      screen.getByText("材料与引用以本次审查记录为准。"),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "修改稿对照" })).not.toBeInTheDocument();
  });

  it("asks for a check without exposing identities when diagnostics are off", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <RunProvenance
          workspace={workspace({
            snapshot: {
              document_ids: ["document-1"],
              rule_set_hash: "rule-set-hash",
              playbook_hash: "playbook-hash",
              model_ref: "model-v1",
              comparator_ref: "comparator-v1",
            },
            evaluations: [evaluation],
          })}
        />
      </MemoryRouter>,
    );
    await open(user, "运行依据与规则评估");
    expect(
      screen.getByText("请核对本次审查的材料与引用。"),
    ).toBeInTheDocument();
    expect(screen.getByText("履约，冲突")).toBeInTheDocument();
    expect(screen.getByText("期限不一致")).toBeInTheDocument();
    expect(screen.queryByText("rule-1")).not.toBeInTheDocument();
    expect(screen.queryByText(/业务对象/)).not.toBeInTheDocument();
  });

  it("shows the frozen snapshot identities with diagnostics", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <RunProvenance
          workspace={workspace({
            snapshot: {
              document_ids: ["document-1", "document-2"],
              rule_set_hash: "rule-set-hash",
              playbook_hash: "playbook-hash",
              model_ref: "model-v1",
              comparator_ref: "comparator-v1",
            },
            evaluations: [evaluation],
          })}
          showDiagnostics
        />
      </MemoryRouter>,
    );
    await open(user, "运行依据与规则评估");
    const region = screen.getByRole("region", { name: "运行依据与复审" });
    expect(within(region).getByText("document-1、document-2")).toBeInTheDocument();
    expect(within(region).getByText("rule-set-hash")).toBeInTheDocument();
    expect(within(region).getByText("playbook-hash")).toBeInTheDocument();
    expect(within(region).getByText("comparator-v1")).toBeInTheDocument();
    expect(within(region).getByText("model-v1")).toBeInTheDocument();
    expect(
      within(region).getByText(/rule-1，v2，capability-1/),
    ).toBeInTheDocument();
    expect(
      within(region).getByText(/业务对象：object-1、object-2，规则依据：date_mismatch，事实：fact-1/),
    ).toBeInTheDocument();
  });

  it("marks unreturned snapshot fields without inventing values", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <RunProvenance
          workspace={workspace({
            snapshot: {
              document_ids: [],
              rule_set_hash: "",
              playbook_hash: "",
              model_ref: null,
              comparator_ref: null,
            },
            evaluations: [
              {
                ...evaluation,
                rule_id: "",
                rule_version: "",
                capability_id: "",
                object_refs: [],
                reason_code: "",
                fact_ids: [],
              },
            ],
          })}
          showDiagnostics
        />
      </MemoryRouter>,
    );
    await open(user, "运行依据与规则评估");
    expect(screen.getAllByText("未返回")).toHaveLength(4);
    expect(screen.getByText("未记录")).toBeInTheDocument();
    expect(
      screen.getByText(/规则身份未返回，版本未返回，能力身份未返回/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/业务对象：未返回，规则依据：未返回，事实：未返回/),
    ).toBeInTheDocument();
  });

  it("links back to the previous run and compares revisions", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <RunProvenance
          workspace={workspace({
            previous_run_id: "run-0",
            revision_comparison_reason: "规则版本变化",
            revisions: [
              {
                mark: "新增",
                finding_id: "finding-1",
                previous_finding_id: null,
                domain: "履约",
                relation: "conflict",
                claim: "新增期限差异",
                prior_decision: {
                  id: "decision-1",
                  action: "confirm_risk",
                  reason: "已核实",
                  actor: "审查员甲",
                  decided_at: at,
                },
              },
              {
                mark: "保留",
                finding_id: null,
                previous_finding_id: "previous-1",
                domain: "商务",
                relation: "insufficient",
                claim: "保留的线索",
                prior_decision: null,
              },
            ],
          })}
        />
      </MemoryRouter>,
    );
    await open(user, "修改稿对照");
    expect(
      screen.getByRole("link", { name: "查看上一轮审查" }),
    ).toHaveAttribute("href", "/cases/case-1/reviews/run-0");
    expect(screen.getByText("规则版本变化")).toBeInTheDocument();
    expect(screen.getByText("新增，履约")).toBeInTheDocument();
    expect(screen.getByText("新增期限差异")).toBeInTheDocument();
    expect(
      screen.getByText("先前决定：确认风险，审查员甲，已核实"),
    ).toBeInTheDocument();
    expect(screen.getByText("保留，商务")).toBeInTheDocument();
    expect(screen.getByText("保留的线索")).toBeInTheDocument();
  });

  it("says when a previous run has no comparable clues", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <RunProvenance
          workspace={workspace({ previous_run_id: "run-0", revisions: [] })}
        />
      </MemoryRouter>,
    );
    await open(user, "修改稿对照");
    expect(screen.getByText("没有返回可比较的线索。")).toBeInTheDocument();
    expect(screen.queryByText("规则版本变化")).not.toBeInTheDocument();
  });
});
