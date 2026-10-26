import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithAntd as render } from "../../test/antd";
import { palette, uiTheme } from "../../theme";
import {
  EmptyState,
  ErrorState,
  KpiStat,
  LoadingBlock,
  PageHeader,
  RiskTag,
  SectionCard,
  severityLabel,
} from ".";

describe("shared UI primitives", () => {
  it("keeps the design tokens in one theme", () => {
    expect(uiTheme.token?.colorPrimary).toBe(palette.primary);
    expect(uiTheme.token?.colorBgLayout).toBe(palette.page);
    expect(uiTheme.components?.Menu?.itemSelectedBg).toBe(palette.primarySoft);
  });

  it("renders a page header with description and actions", () => {
    render(
      <PageHeader
        title="案件中心"
        description="统一管理审查任务"
        eyebrow="工作区"
        extra={<button type="button">新建案件</button>}
      />,
    );
    expect(
      screen.getByRole("heading", { name: "案件中心", level: 1 }),
    ).toBeInTheDocument();
    expect(screen.getByText("统一管理审查任务")).toBeInTheDocument();
    expect(screen.getByText("工作区")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "新建案件" })).toBeVisible();
  });

  it("wraps section content in a card", () => {
    render(
      <SectionCard title="最近案件" extra={<span>查看全部</span>}>
        <p>案件列表</p>
      </SectionCard>,
    );
    expect(screen.getByText("最近案件")).toBeInTheDocument();
    expect(screen.getByText("查看全部")).toBeInTheDocument();
    expect(screen.getByText("案件列表")).toBeInTheDocument();
  });

  it("shows a KPI value, hint and loading skeleton", () => {
    const { rerender } = render(
      <KpiStat
        label="待处理审查"
        value={12}
        hint="当前状态"
        tone="high"
        icon={<span>!</span>}
      />,
    );
    expect(screen.getByLabelText("待处理审查")).toBeInTheDocument();
    expect(screen.getByText("12")).toBeInTheDocument();
    expect(screen.getByText("当前状态")).toBeInTheDocument();
    rerender(
      <KpiStat label="待处理审查" value={12} loading />,
    );
    expect(screen.queryByText("12")).not.toBeInTheDocument();
  });

  it("labels every risk severity with text, not colour alone", () => {
    expect(severityLabel("high")).toBe("高风险");
    expect(severityLabel("medium")).toBe("中风险");
    expect(severityLabel("low")).toBe("低风险");
    expect(severityLabel(null)).toBe("未评级");
    expect(severityLabel("other")).toBe("未识别（other）");
    render(<RiskTag severity="high" />);
    expect(screen.getByText("高风险")).toBeInTheDocument();
  });

  it("renders empty, error and loading states", async () => {
    const retry = vi.fn();
    const user = userEvent.setup();
    const { rerender } = render(
      <EmptyState
        title="暂无数据"
        detail="当前筛选没有结果"
        action={<button type="button">清除筛选</button>}
      />,
    );
    expect(screen.getByRole("heading", { name: "暂无数据" })).toBeInTheDocument();
    expect(screen.getByText("当前筛选没有结果")).toBeInTheDocument();

    rerender(
      <ErrorState
        title="加载失败"
        detail="网络错误"
        action={<button type="button" onClick={retry}>重试</button>}
      />,
    );
    expect(screen.getByText("加载失败")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "重试" }));
    expect(retry).toHaveBeenCalledOnce();
  });

  it("announces loading to assistive technology", () => {
    render(<LoadingBlock label="正在加载案件" />);
    expect(screen.getByRole("status")).toHaveAttribute("aria-busy", "true");
    expect(screen.getByText("正在加载案件")).toBeInTheDocument();
  });
});
