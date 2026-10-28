import { act, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "../../api/http";
import { CapabilityPackPanel } from "./capability-pack-panel";
import { DEMO_OPERATOR_AUDIT_FLAGS } from "./feature-flags";
import { capabilityPacks } from "./fixture";
import {
  deferred,
  operatorAuditClient,
  renderOperatorAudit,
} from "./test-utils";

describe("capability pack panel", () => {
  it("is off by default and does not query the server", () => {
    const listCapabilityPacks = vi.fn(async () => capabilityPacks);
    renderOperatorAudit(
      <CapabilityPackPanel
        client={operatorAuditClient({ listCapabilityPacks })}
      />,
    );
    expect(screen.queryByText("能力包目录")).not.toBeInTheDocument();
    expect(listCapabilityPacks).not.toHaveBeenCalled();
  });

  it("lists pack versions, scenarios and status without hiding synthetic data", async () => {
    renderOperatorAudit(
      <CapabilityPackPanel
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
        client={operatorAuditClient({
          listCapabilityPacks: async () => capabilityPacks,
        })}
      />,
    );
    expect(
      await screen.findByText("专线配置核验（合成示例）"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("云服务规则复用（合成示例）"),
    ).toBeInTheDocument();
    expect(screen.getAllByText("1.0.0")).toHaveLength(2);
    expect(screen.getByText("政企专线")).toBeInTheDocument();
    expect(screen.getByText("云服务")).toBeInTheDocument();
    expect(screen.getAllByText("已发布")).toHaveLength(2);
    expect(screen.getAllByText("合成数据（fixture）")).toHaveLength(2);
    expect(
      screen.getByText("operator.dedicated_line"),
    ).toBeInTheDocument();
  });

  it("shows loading first and an explicit empty catalogue afterwards", async () => {
    const gate = deferred<typeof capabilityPacks>();
    renderOperatorAudit(
      <CapabilityPackPanel
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
        client={operatorAuditClient({
          listCapabilityPacks: () => gate.promise,
        })}
      />,
    );
    expect(screen.getByText("正在读取能力包目录")).toBeInTheDocument();
    await act(async () => {
      gate.resolve([]);
    });
    expect(
      await screen.findByText("当前范围没有已发布的能力包"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("空目录不代表审查通过，也不改变已有案件。"),
    ).toBeInTheDocument();
  });

  it("treats a forbidden response as denied, not as an empty list", async () => {
    renderOperatorAudit(
      <CapabilityPackPanel
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
        client={operatorAuditClient({
          listCapabilityPacks: async () => {
            throw new ApiError("没有访问该内容的权限", 403, "forbidden", false, null);
          },
        })}
      />,
    );
    expect(
      await screen.findByText("没有查看能力包目录的权限"),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("当前范围没有已发布的能力包"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "重试读取能力包" }),
    ).not.toBeInTheDocument();
  });

  it("surfaces a load failure and allows an explicit retry", async () => {
    const user = userEvent.setup();
    let attempt = 0;
    const listCapabilityPacks = vi.fn(async () => {
      attempt += 1;
      if (attempt === 1) {
        throw new ApiError(
          "能力包服务暂不可用",
          503,
          "unavailable",
          true,
          null,
        );
      }
      return capabilityPacks;
    });
    renderOperatorAudit(
      <CapabilityPackPanel
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
        client={operatorAuditClient({ listCapabilityPacks })}
      />,
    );
    expect(
      await screen.findByText("能力包目录读取失败"),
    ).toBeInTheDocument();
    expect(screen.getByText("能力包服务暂不可用")).toBeInTheDocument();
    await user.click(
      screen.getByRole("button", { name: "重试读取能力包" }),
    );
    expect(
      await screen.findByText("专线配置核验（合成示例）"),
    ).toBeInTheDocument();
    expect(listCapabilityPacks).toHaveBeenCalledTimes(2);
  });
});
