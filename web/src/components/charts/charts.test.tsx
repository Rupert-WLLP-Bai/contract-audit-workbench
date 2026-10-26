import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { TrendPoint } from "../../domain/dashboard";
import { ChartLegend, DonutChart } from "./donut-chart";
import { TrendChart } from "./trend-chart";

const items = [
  { key: "high", label: "高风险", value: 2, color: "#ef4444" },
  { key: "low", label: "低风险", value: 1, color: "#10b981" },
  { key: "none", label: "未评级", value: 0, color: "#94a3b8" },
];

function points(overrides: Partial<TrendPoint>[] = []): TrendPoint[] {
  return Array.from({ length: 4 }, (_, index) => ({
    key: `2026-10-${18 + index}`,
    label: `10/${18 + index}`,
    started: index + 1,
    completed: index,
    averageMs: index === 0 ? null : (index + 1) * 60_000,
    ...overrides[index],
  }));
}

describe("DonutChart", () => {
  it("renders labelled slices, a center total and a text legend", () => {
    const { container } = render(
      <>
        <DonutChart
          items={items}
          centerValue={3}
          centerLabel="发现项"
          ariaLabel="发现项分布：高风险 2 项，低风险 1 项"
        />
        <ChartLegend items={items} total={3} />
      </>,
    );
    const chart = screen.getByRole("img", {
      name: "发现项分布：高风险 2 项，低风险 1 项",
    });
    expect(within(chart).getByText("3")).toBeInTheDocument();
    expect(within(chart).getByText("发现项")).toBeInTheDocument();
    const titles = Array.from(container.querySelectorAll("circle title")).map(
      (item) => item.textContent,
    );
    expect(titles).toEqual(["高风险：2", "低风险：1"]);
    expect(screen.getByText("67%")).toBeInTheDocument();
    expect(screen.getByText("33%")).toBeInTheDocument();
    expect(screen.getByText("0%")).toBeInTheDocument();
  });

  it("does not divide by zero or render slices for empty values", () => {
    render(
      <>
        <DonutChart
          items={items.map((item) => ({ ...item, value: 0 }))}
          centerValue={0}
          centerLabel="发现项"
          ariaLabel="没有发现项"
        />
        <ChartLegend
          items={items.map((item) => ({ ...item, value: 0 }))}
          total={0}
          showPercent={false}
        />
      </>,
    );
    expect(screen.getByRole("img", { name: "没有发现项" })).toBeInTheDocument();
    expect(screen.queryByTitle(/高风险：/)).not.toBeInTheDocument();
    expect(screen.queryByText(/%/)).not.toBeInTheDocument();
    expect(screen.getAllByText("0")).toHaveLength(4);
  });
});

describe("TrendChart", () => {
  it("renders bars, a duration line and an accessible data table", () => {
    const { container } = render(<TrendChart points={points()} />);
    expect(
      screen.getByRole("img", {
        name: "近 14 天审查数量、完成数量与平均审查时长趋势",
      }),
    ).toBeInTheDocument();
    expect(screen.getAllByText("审查数量")).toHaveLength(2);
    expect(screen.getAllByText("完成数量")).toHaveLength(2);
    expect(screen.getByText("平均时长（分钟）")).toBeInTheDocument();
    expect(screen.getByText("近 14 天审查趋势数据")).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "日期" })).toBeInTheDocument();
    expect(container.querySelectorAll(".caw-trend__bar-started")).toHaveLength(4);
    expect(container.querySelectorAll(".caw-trend__point")).toHaveLength(3);
  });

  it("omits the duration series when no completed duration exists", () => {
    const { container } = render(
      <TrendChart
        points={points([{ averageMs: null }, { averageMs: null }, { averageMs: null }, { averageMs: null }])}
      />,
    );
    expect(screen.queryByText("平均时长（分钟）")).not.toBeInTheDocument();
    expect(container.querySelector(".caw-trend__line")).not.toBeInTheDocument();
    expect(screen.getAllByText("0").length).toBeGreaterThan(0);
  });
});
