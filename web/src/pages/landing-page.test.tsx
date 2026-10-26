import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router";
import { describe, expect, it } from "vitest";
import { renderWithAntd as render } from "../test/antd";
import { LandingPage } from "./landing-page";

function renderLanding() {
  return render(
    <MemoryRouter initialEntries={["/"]}>
      <Routes>
        <Route path="/" element={<LandingPage />} />
        <Route path="/login" element={<h1>登录合同审查</h1>} />
        <Route path="/dashboard" element={<h1>工作台</h1>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("LandingPage", () => {
  it("presents the public product story without unverified metrics", () => {
    const { container } = renderLanding();
    expect(
      screen.getByRole("heading", { level: 1, name: /让合同审查/ }),
    ).toBeInTheDocument();
    expect(screen.getByText("核心产品能力")).toBeInTheDocument();
    expect(screen.getByText("三步完成合同审查")).toBeInTheDocument();
    expect(screen.getByText("适用多种业务场景")).toBeInTheDocument();
    expect(screen.getByText("企业级安全保障")).toBeInTheDocument();
    expect(screen.getByText("界面示意 · 数据以实际系统为准")).toBeInTheDocument();
    for (const name of [
      "智能风险识别",
      "专业规则包",
      "审查建议生成",
      "多格式文档支持",
    ]) {
      expect(
        screen.getByRole("heading", { name, level: 3 }),
      ).toBeInTheDocument();
    }
    expect(container.textContent).not.toMatch(/80%|1000\+|准确率|效率提升/);
  });

  it("links product navigation to real in-page sections", () => {
    renderLanding();
    const nav = screen.getByRole("navigation", { name: "产品导航" });
    expect(nav).toBeInTheDocument();
    for (const [label, href] of [
      ["产品能力", "#capabilities"],
      ["适用场景", "#scenarios"],
      ["审查流程", "#workflow"],
      ["安全合规", "#security"],
      ["联系我们", "#contact"],
    ]) {
      expect(within(nav).getByRole("link", { name: label })).toHaveAttribute(
        "href",
        href,
      );
    }
  });

  it("sends guests to login or the protected workspace", async () => {
    const user = userEvent.setup();
    renderLanding();
    await user.click(screen.getByRole("button", { name: "登录" }));
    expect(
      await screen.findByRole("heading", { name: "登录合同审查" }),
    ).toBeInTheDocument();
  });

  it("sends the primary action to the dashboard gate", async () => {
    const user = userEvent.setup();
    renderLanding();
    await user.click(screen.getByRole("button", { name: /立即体验/ }));
    expect(
      await screen.findByRole("heading", { name: "工作台" }),
    ).toBeInTheDocument();
  });
});
