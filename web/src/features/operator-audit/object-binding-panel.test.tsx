import { screen } from "@testing-library/react";
import userEvent, { type UserEvent } from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "../../api/http";
import { chooseOption } from "../../test/antd";
import { DEMO_OPERATOR_AUDIT_FLAGS } from "./feature-flags";
import { ambiguousBinding, confirmedBinding } from "./fixture";
import {
  ObjectBindingPanel,
  type ContractObjectOption,
  type ExternalObjectOption,
} from "./object-binding-panel";
import {
  operatorAuditClient,
  renderOperatorAudit,
} from "./test-utils";
import type {
  CreateObjectBindingInput,
  ObjectBinding,
} from "./types";

const contractObjects: ContractObjectOption[] = [
  {
    objectType: "contract_line",
    objectId: "line-1",
    label: "政企专线合同",
  },
  {
    objectType: "contract_line",
    objectId: "line-2",
    label: "政企专线合同（扩容）",
  },
];

const externalObjects: ExternalObjectOption[] = [
  {
    connectorId: "fixture.oss",
    externalObjectId: "fixture-line-001",
    label: "OSS 专线 001",
  },
  {
    connectorId: "fixture.oss",
    externalObjectId: "fixture-line-002",
    label: "OSS 专线 001 备用",
  },
];

async function selectPair(user: UserEvent) {
  await chooseOption(
    user,
    screen.getByRole("combobox", { name: "合同对象" }),
    /政企专线合同（contract_line · line-1）/,
  );
  await chooseOption(
    user,
    screen.getByRole("combobox", { name: "外部对象" }),
    /OSS 专线 001（fixture.oss · fixture-line-001）/,
  );
}

describe("object binding panel", () => {
  it("is off by default and never submits without an explicit human confirmation", async () => {
    const user = userEvent.setup();
    const createObjectBinding = vi.fn(
      async (
        _caseId: string,
        _input: CreateObjectBindingInput,
        _key: string,
      ): Promise<ObjectBinding> => confirmedBinding,
    );
    const client = operatorAuditClient({ createObjectBinding });
    renderOperatorAudit(<ObjectBindingPanel caseId="case-1" client={client} />);
    expect(
      screen.queryByText("合同对象与外部对象映射"),
    ).not.toBeInTheDocument();

    renderOperatorAudit(
      <ObjectBindingPanel
        caseId="case-1"
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
        client={client}
        contractObjects={contractObjects}
        externalObjects={externalObjects}
      />,
    );
    expect(
      screen.getByText(/系统不会按名称相似度自动匹配/),
    ).toBeInTheDocument();
    const submit = screen.getByRole("button", { name: "确认对象映射" });
    expect(submit).toBeDisabled();
    await selectPair(user);
    expect(submit).toBeDisabled();
    expect(createObjectBinding).not.toHaveBeenCalled();

    await user.click(
      screen.getByRole("checkbox", {
        name: "我已人工核对上述合同对象与外部对象的对应关系",
      }),
    );
    expect(submit).toBeEnabled();
    await user.click(submit);
    expect(
      await screen.findByText(/映射已确认，版本 2/),
    ).toBeInTheDocument();
    expect(screen.getByText(/确认人 reviewer-1/)).toBeInTheDocument();
    expect(createObjectBinding).toHaveBeenCalledTimes(1);
    const call = createObjectBinding.mock.calls[0];
    expect(call?.[0]).toBe("case-1");
    expect(call?.[1]).toMatchObject({
      contract_object: {
        object_type: "contract_line",
        object_id: "line-1",
        label: "政企专线合同",
      },
      external_object: {
        connector_id: "fixture.oss",
        external_object_id: "fixture-line-001",
      },
      effective_from: null,
      effective_until: null,
      confirmed: true,
    });
    expect(call?.[2]).toMatch(/^[\w-]{20,}$/);
  });

  it("shows missing and ambiguous mappings as evidence-insufficient", () => {
    renderOperatorAudit(
      <ObjectBindingPanel
        caseId="case-1"
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
        client={operatorAuditClient()}
        contractObjects={contractObjects}
        externalObjects={externalObjects}
        existingBindings={[ambiguousBinding]}
      />,
    );
    expect(screen.getByText("多义（无法唯一确定）")).toBeInTheDocument();
    expect(screen.getByText("确认人未报告")).toBeInTheDocument();
    expect(
      screen.getByText(/这些映射不产生确定性结论/),
    ).toBeInTheDocument();
    expect(screen.getByText("外部对象未报告")).toBeInTheDocument();
    expect(screen.getByText("时间未报告")).toBeInTheDocument();
  });

  it("states when no candidate objects exist instead of guessing a pair", () => {
    renderOperatorAudit(
      <ObjectBindingPanel
        caseId="case-1"
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
        client={operatorAuditClient()}
        contractObjects={[]}
        externalObjects={[]}
      />,
    );
    expect(
      screen.getByText(/没有可确认的候选对象/),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "确认对象映射" }),
    ).toBeDisabled();
  });

  it("reuses the same idempotency key when retrying the same confirmation", async () => {
    const user = userEvent.setup();
    const keys: string[] = [];
    let attempt = 0;
    const createObjectBinding = vi.fn(
      async (
        _caseId: string,
        _input: CreateObjectBindingInput,
        key: string,
      ): Promise<ObjectBinding> => {
        keys.push(key);
        attempt += 1;
        if (attempt === 1) {
          throw new ApiError(
            "该事项已被他人更新，请按最新内容重新确认",
            409,
            "conflict",
            true,
            null,
          );
        }
        return confirmedBinding;
      },
    );
    renderOperatorAudit(
      <ObjectBindingPanel
        caseId="case-1"
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
        client={operatorAuditClient({ createObjectBinding })}
        contractObjects={contractObjects}
        externalObjects={externalObjects}
      />,
    );
    await selectPair(user);
    await user.click(
      screen.getByRole("checkbox", {
        name: "我已人工核对上述合同对象与外部对象的对应关系",
      }),
    );
    await user.click(screen.getByRole("button", { name: "确认对象映射" }));
    expect(
      await screen.findByText("该事项已被他人更新，请按最新内容重新确认"),
    ).toBeInTheDocument();
    await user.click(
      await screen.findByRole("button", { name: /确认对象映射/ }),
    );
    expect(
      await screen.findByText(/映射已确认，版本 2/),
    ).toBeInTheDocument();
    expect(keys).toHaveLength(2);
    expect(keys[0]).toBe(keys[1]);
  });
});
