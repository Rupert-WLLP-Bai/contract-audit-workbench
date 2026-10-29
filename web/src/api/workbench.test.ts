import { describe, expect, it } from "vitest";
import {
  parseCaseProfile,
  parseCaseRelation,
  parseKnowledge,
  parseWorkbenchSnapshot,
  parseWorkbenchTask,
} from "./workbench";

const dashboard = {
  visible_cases: 1,
  my_pending_tasks: 2,
  overdue_tasks: 3,
  pending_review_cases: 4,
  remediation_cases: 5,
  completed_reviews: 6,
  average_duration_ms: 700,
  progress: [],
  risks: [],
};

const task = {
  id: "task-1",
  case_id: "case-1",
  title: "复核条款",
  kind: "review",
  assignee_id: "user-1",
  due_at: null,
  status: "open",
  priority: "high",
  finding_id: null,
  version: 2,
  created_by: null,
  created_at: "2026-10-01T00:00:00Z",
  updated_at: "2026-10-02T00:00:00Z",
};

const caseSummary = {
  id: "case-1",
  title: "专线合同",
  contract_type: "service",
  document_count: 2,
  pending_decisions: 1,
  latest_run: null,
  updated_at: "2026-10-03T00:00:00Z",
  business_line: "ENT",
  scenario: "telecom_dedicated_line",
  business_status: "active",
  archived_at: null,
};

const workbenchCase = {
  ...caseSummary,
  profile: null,
  severity: null,
  progress: null,
};

function snapshot(overrides: Record<string, unknown> = {}) {
  return {
    schema_version: "workbench/v1",
    generated_at: "2026-10-04T00:00:00Z",
    period: "week",
    dashboard,
    cases: [],
    profiles: [],
    tasks: [],
    relations: [],
    knowledge: [],
    members: [],
    remediations: [],
    reports: [],
    documents: [],
    runs: [],
    audit: [],
    templates: [],
    model: {
      name: "model",
      ocr_name: "ocr",
      status: "configured",
      label: "真实模型",
    },
    tools: [],
    ...overrides,
  };
}

describe("workbench task parsing", () => {
  it("reads every documented task field and keeps nulls distinct", () => {
    expect(parseWorkbenchTask(task)).toEqual(task);
    expect(
      parseWorkbenchTask({
        ...task,
        due_at: "2026-11-01T00:00:00+08:00",
        finding_id: "finding-1",
        created_by: "user-2",
        kind: "decision",
        status: "in_progress",
        priority: "low",
      }),
    ).toMatchObject({
      due_at: "2026-11-01T00:00:00+08:00",
      finding_id: "finding-1",
      created_by: "user-2",
      kind: "decision",
      status: "in_progress",
      priority: "low",
    });
  });

  it.each([
    ["kind", { kind: "unknown" }, "任务 kind 无效"],
    ["status", { status: "done" }, "任务 status 无效"],
    ["priority", { priority: "urgent" }, "任务 priority 无效"],
    ["id", { id: 1 }, "id不是字符串"],
    ["due_at", { due_at: 1 }, "due_at不是字符串或null"],
    ["version", { version: -1 }, "version不是非负整数"],
    ["version fraction", { version: 1.5 }, "version不是非负整数"],
    ["created_at", { created_at: "2026-10-01" }, "created_at不是RFC3339时间"],
    ["updated_at", { updated_at: "not-a-date" }, "updated_at不是RFC3339时间"],
  ])("rejects %s", (_label, patch, message) => {
    expect(() => parseWorkbenchTask({ ...task, ...patch })).toThrow(
      `工作台数据无效：${message}`,
    );
  });

  it("rejects a non-object task", () => {
    expect(() => parseWorkbenchTask(null)).toThrow("工作台数据无效：任务不是对象");
  });
});

describe("workbench case parsing", () => {
  it("reads profile, severity and progress when present", () => {
    const snapshotValue = snapshot({
      cases: [
        {
          ...workbenchCase,
          profile: {
            case_id: "case-1",
            number: "HT-1",
            customer: "客户",
            supplier: "供应商",
            contract_category: "服务",
            owner_id: "user-1",
            due_at: null,
            version: 3,
            updated_at: "2026-10-04T00:00:00Z",
          },
          severity: "high",
          progress: 100,
        },
      ],
    });
    expect(parseWorkbenchSnapshot(snapshotValue, "week").cases[0]).toMatchObject({
      id: "case-1",
      profile: { number: "HT-1", version: 3 },
      severity: "high",
      progress: 100,
    });
  });

  it("accepts zero progress and a null profile", () => {
    expect(
      parseWorkbenchSnapshot(
        snapshot({ cases: [{ ...workbenchCase, progress: 0 }] }),
        "week",
      ).cases[0]?.progress,
    ).toBe(0);
    expect(
      parseWorkbenchSnapshot(snapshot({ cases: [workbenchCase] }), "week")
        .cases[0]?.profile,
    ).toBeNull();
  });

  it("requires the case envelope keys", () => {
    for (const key of ["profile", "severity", "progress", "latest_run"]) {
      const copy = { ...workbenchCase } as Record<string, unknown>;
      delete copy[key];
      expect(() =>
        parseWorkbenchSnapshot(snapshot({ cases: [copy] }), "week"),
      ).toThrow(
        "工作台数据无效：案件缺少profile、severity、progress或latest_run",
      );
    }
  });

  it.each([
    [{ severity: 1 }, "severity不是字符串或null"],
    [{ progress: -1 }, "案件 progress 无效"],
    [{ progress: 101 }, "案件 progress 无效"],
    [{ progress: Number.NaN }, "案件 progress 无效"],
    [{ progress: "50" }, "案件 progress 无效"],
    [{ profile: { case_id: "c" } }, "number不是字符串"],
  ])("rejects case patch %j", (patch, message) => {
    expect(() =>
      parseWorkbenchSnapshot(
        snapshot({ cases: [{ ...workbenchCase, ...patch }] }),
        "week",
      ),
    ).toThrow(`工作台数据无效：${message}`);
  });

  it("rejects a non-object case and a missing case summary count", () => {
    expect(() =>
      parseWorkbenchSnapshot(snapshot({ cases: [null] }), "week"),
    ).toThrow("工作台数据无效：案件不是对象");
    expect(() =>
      parseWorkbenchSnapshot(
        snapshot({ cases: [{ ...workbenchCase, document_count: "2" }] }),
        "week",
      ),
    ).toThrow("案件列表缺少数量");
  });
});

describe("workbench snapshot parsing", () => {
  it("parses an empty snapshot and the documented aliases", () => {
    const parsed = parseWorkbenchSnapshot(snapshot(), "week");
    expect(parsed).toMatchObject({
      schema_version: "workbench/v1",
      generated_at: "2026-10-04T00:00:00Z",
      period: "week",
      dashboard,
      model: {
        name: "model",
        ocr_name: "ocr",
        status: "configured",
        label: "真实模型",
      },
    });
    expect(parsed.cases).toEqual([]);
    expect(parsed.tasks).toEqual([]);
  });

  it("reads tasks, relations and knowledge through their public parsers", () => {
    const relation = {
      id: "rel-1",
      case_id: "case-1",
      related_case_id: "case-2",
      reason: "同一客户",
      created_by: "user-1",
      created_at: "2026-10-04T00:00:00Z",
    };
    const knowledge = {
      id: "kb-1",
      version_id: "v1",
      title: "制度",
      version: "1",
      body: "正文",
      source_type: "reference",
      effective_at: null,
      created_by: "user-1",
      created_at: "2026-10-04T00:00:00Z",
    };
    expect(parseCaseRelation(relation)).toEqual(relation);
    expect(parseKnowledge(knowledge)).toEqual(knowledge);
    expect(
      parseWorkbenchSnapshot(
        snapshot({ tasks: [task], relations: [relation], knowledge: [knowledge] }),
        "week",
      ),
    ).toMatchObject({
      tasks: [task],
      relations: [relation],
      knowledge: [knowledge],
    });
    expect(
      parseCaseProfile({
        case_id: "case-1",
        number: "HT-1",
        customer: "客户",
        supplier: "供应商",
        contract_category: "服务",
        owner_id: "user-1",
        due_at: null,
        version: 0,
        updated_at: "2026-10-04T00:00:00Z",
      }),
    ).toMatchObject({ version: 0, due_at: null });
  });

  it("rejects unsupported schema versions, periods and mismatched requests", () => {
    expect(() =>
      parseWorkbenchSnapshot(snapshot({ schema_version: "workbench/v2" }), "week"),
    ).toThrow("工作台数据无效：schema_version 不受支持");
    expect(() =>
      parseWorkbenchSnapshot(snapshot({ period: "year" }), "week"),
    ).toThrow("工作台数据无效：period 无效");
    expect(() => parseWorkbenchSnapshot(snapshot(), "day")).toThrow(
      "工作台数据无效：响应 period 与请求不一致",
    );
  });

  it("rejects malformed timestamps, model status and tools", () => {
    expect(() =>
      parseWorkbenchSnapshot(snapshot({ generated_at: "yesterday" }), "week"),
    ).toThrow("工作台数据无效：generated_at不是RFC3339时间");
    expect(() =>
      parseWorkbenchSnapshot(
        snapshot({ model: { name: "m", ocr_name: "o", status: "unknown", label: "l" } }),
        "week",
      ),
    ).toThrow("工作台数据无效：model.status 无效");
    expect(
      parseWorkbenchSnapshot(
        snapshot({ tools: [{ id: "tool", name: "n", description: "d", capabilities: ["read"], calls: 1, last_status: null }] }),
        "week",
      ),
    ).toMatchObject({ tools: [{ id: "tool", calls: 1 }] });
    expect(() =>
      parseWorkbenchSnapshot(
        snapshot({ tools: [{ id: "tool", name: "n", description: "d", capabilities: [1], calls: 1, last_status: null }] }),
        "week",
      ),
    ).toThrow("工作台数据无效：capabilities不是字符串数组");
  });
});

const documentBase = {
  id: "d1",
  role: "draft_contract",
  filename: "合同.txt",
  media_type: "text/plain",
  digest: "original-hash",
  size: 1,
  parse_status: "ready",
  parse_error: null,
  created_at: "2026-10-04T00:00:00Z",
  parsed_document: null,
};

const parsedDocumentBase = {
  id: "pd1",
  document_id: "d1",
  original_digest: "original-hash",
  parser_id: "text",
  parser_version: "1",
  config_hash: "config-hash",
  normalization_version: "unicode-v1",
  normalized_digest: "text-hash",
  ir_digest: "ir-hash",
  artifact_digest: "artifact-hash",
  created_at: "2026-10-04T00:00:01Z",
  status: "ready",
};

const workbenchDocument = {
  ...documentBase,
  case_id: "case-1",
  case_title: "专线合同",
};

describe("workbench boundary values", () => {
  it("accepts zero and rejects invalid dashboard durations", () => {
    const parsed = parseWorkbenchSnapshot(
      snapshot({ dashboard: { ...dashboard, average_duration_ms: 0 } }),
      "week",
    );
    expect(parsed.dashboard.average_duration_ms).toBe(0);
    for (const value of [-1, 1.5, "x"]) {
      expect(() =>
        parseWorkbenchSnapshot(
          snapshot({ dashboard: { ...dashboard, average_duration_ms: value } }),
          "week",
        ),
      ).toThrow("工作台数据无效：average_duration_ms不是非负整数或null");
    }
  });

  it("accepts every documented risk severity and rejects unknown ones", () => {
    const severities = ["high", "medium", "low", "info", "unrated"];
    const parsed = parseWorkbenchSnapshot(
      snapshot({
        dashboard: {
          ...dashboard,
          risks: severities.map((severity) => ({ severity, count: 1 })),
        },
      }),
      "week",
    );
    expect(parsed.dashboard.risks.map((risk) => risk.severity)).toEqual(
      severities,
    );
    expect(() =>
      parseWorkbenchSnapshot(
        snapshot({
          dashboard: {
            ...dashboard,
            risks: [{ severity: "bogus", count: 1 }],
          },
        }),
        "week",
      ),
    ).toThrow("工作台数据无效：风险统计 severity 无效");
  });

  it("accepts both knowledge source types", () => {
    const knowledge = {
      id: "kb-1",
      version_id: "v1",
      title: "制度",
      version: "1",
      body: "正文",
      source_type: "synthetic_policy",
      effective_at: null,
      created_by: "user-1",
      created_at: "2026-10-04T00:00:00Z",
    };
    expect(parseKnowledge(knowledge).source_type).toBe("synthetic_policy");
    expect(
      parseKnowledge({ ...knowledge, source_type: "reference" }).source_type,
    ).toBe("reference");
    expect(() =>
      parseKnowledge({ ...knowledge, source_type: "other" }),
    ).toThrow("工作台数据无效：知识来源类型无效");
    expect(() => parseKnowledge({ ...knowledge, version_id: "" })).toThrow(
      "工作台数据无效：知识版本标识为空",
    );
  });

  it("accepts zero document sizes and rejects invalid ones", () => {
    const zero = parseWorkbenchSnapshot(
      snapshot({ documents: [{ ...workbenchDocument, size: 0 }] }),
      "week",
    );
    expect(zero.documents[0]?.size).toBe(0);
    for (const value of [-1, 1.5, "x"]) {
      expect(() =>
        parseWorkbenchSnapshot(
          snapshot({ documents: [{ ...workbenchDocument, size: value }] }),
          "week",
        ),
      ).toThrow("工作台数据无效：材料 size 无效");
    }
  });

  it("keeps the superseded flag absent unless the server sends it", () => {
    const parsed = parseWorkbenchSnapshot(
      snapshot({ documents: [workbenchDocument] }),
      "week",
    );
    expect(parsed.documents[0]).not.toHaveProperty("superseded");
    const flagged = parseWorkbenchSnapshot(
      snapshot({
        documents: [{ ...workbenchDocument, superseded: true }],
      }),
      "week",
    );
    expect(flagged.documents[0]).toMatchObject({ superseded: true });
    expect(() =>
      parseWorkbenchSnapshot(
        snapshot({
          documents: [{ ...workbenchDocument, superseded: "yes" }],
        }),
        "week",
      ),
    ).toThrow("工作台数据无效：材料 superseded 无效");
  });

  it("parses document metadata and validates its status", () => {
    const parsed = parseWorkbenchSnapshot(
      snapshot({
        documents: [
          {
            ...workbenchDocument,
            parsed_document: parsedDocumentBase,
          },
          {
            ...workbenchDocument,
            id: "d2",
            parsed_document: { ...parsedDocumentBase, status: "failed" },
          },
        ],
      }),
      "week",
    );
    expect(parsed.documents[0]?.parsed_document).toMatchObject({
      id: "pd1",
      status: "ready",
    });
    expect(parsed.documents[1]?.parsed_document).toMatchObject({
      status: "failed",
    });
    expect(() =>
      parseWorkbenchSnapshot(
        snapshot({
          documents: [
            {
              ...workbenchDocument,
              parsed_document: { ...parsedDocumentBase, status: "pending" },
            },
          ],
        }),
        "week",
      ),
    ).toThrow("工作台数据无效：解析产物 status 无效");
  });

  it("parses template fields with a boolean required flag", () => {
    const parsed = parseWorkbenchSnapshot(
      snapshot({
        templates: [
          {
            id: "t1",
            name: "模板",
            version: "1",
            digest: "digest-1",
            fields: [
              { key: "title", label: "标题", required: true, type: "text" },
            ],
          },
        ],
      }),
      "week",
    );
    expect(parsed.templates[0]?.fields[0]).toEqual({
      key: "title",
      label: "标题",
      required: true,
      type: "text",
    });
    expect(() =>
      parseWorkbenchSnapshot(
        snapshot({
          templates: [
            {
              id: "t1",
              name: "模板",
              version: "1",
              digest: "digest-1",
              fields: [{ key: "title", label: "标题", type: "text" }],
            },
          ],
        }),
        "week",
      ),
    ).toThrow("工作台数据无效：模板字段 required 无效");
  });
});
