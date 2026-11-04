import { StyleContext } from "@ant-design/cssinjs";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Button, ConfigProvider, Select } from "antd";
import { createContext, useContext, useEffect, useState, type PropsWithChildren } from "react";
import { expect, it, vi } from "vitest";
import { renderWithAntd } from "./antd";

it("retains the same stateful child through prop changes", async () => {
  function Counter({ label }: { label: string }) {
    const [count, setCount] = useState(0);
    return <button onClick={() => setCount((value) => value + 1)}>{label}: {count}</button>;
  }

  const view = renderWithAntd(<Counter label="A" />);
  const button = screen.getByRole("button", { name: "A: 0" });
  await userEvent.setup().click(button);
  view.rerender(<Counter label="B" />);
  expect(screen.getByRole("button", { name: "B: 1" })).toBe(button);
  view.rerender(<Counter label="A" />);
  expect(screen.getByRole("button", { name: "A: 1" })).toBe(button);
});

it("only cleans up the child and caller wrapper when the tree unmounts", () => {
  const childMount = vi.fn();
  const childCleanup = vi.fn();
  const wrapperMount = vi.fn();
  const wrapperCleanup = vi.fn();

  function CallerWrapper({ children }: PropsWithChildren) {
    useEffect(() => {
      wrapperMount();
      return wrapperCleanup;
    }, []);
    return <div data-testid="caller-wrapper">{children}</div>;
  }
  function Child({ label }: { label: string }) {
    useEffect(() => {
      childMount();
      return childCleanup;
    }, []);
    return <span>{label}</span>;
  }

  const view = renderWithAntd(<Child label="A" />, { wrapper: CallerWrapper });
  const wrapper = screen.getByTestId("caller-wrapper");
  expect(childMount).toHaveBeenCalledTimes(1);
  expect(wrapperMount).toHaveBeenCalledTimes(1);
  view.rerender(<Child label="B" />);
  expect(screen.getByTestId("caller-wrapper")).toBe(wrapper);
  expect.soft(childMount).toHaveBeenCalledTimes(1);
  expect.soft(childCleanup).not.toHaveBeenCalled();
  expect.soft(wrapperMount).toHaveBeenCalledTimes(1);
  expect.soft(wrapperCleanup).not.toHaveBeenCalled();
  view.unmount();
  expect.soft(childCleanup).toHaveBeenCalledTimes(1);
  expect.soft(wrapperCleanup).toHaveBeenCalledTimes(1);
});

it("keeps caller options and Ant Design configuration before and after rerender", () => {
  const CallerContext = createContext("missing caller wrapper");
  const options = Array.from({ length: 30 }, (_, index) => ({
    label: `选项 ${index}`,
    value: String(index),
  }));

  function CallerWrapper({ children }: PropsWithChildren) {
    return (
      <CallerContext.Provider value="caller wrapper active">
        <ConfigProvider button={{ autoInsertSpace: true }}>{children}</ConfigProvider>
      </CallerContext.Provider>
    );
  }
  function ConfiguredChild({ label }: { label: string }) {
    const caller = useContext(CallerContext);
    const layer = useContext(StyleContext).layer;
    return (
      <>
        <span>{label}</span>
        <output data-testid="caller-context">{caller}</output>
        <output data-testid="style-layer">{String(layer)}</output>
        <Button data-testid="two-character-button">编辑</Button>
        <Select aria-label="空选择" open options={[]} />
        <Select aria-label="全部选项" open options={options} />
      </>
    );
  }

  const container = document.body.appendChild(document.createElement("div"));
  const view = renderWithAntd(<ConfiguredChild label="A" />, {
    wrapper: CallerWrapper,
    container,
    baseElement: container,
  });
  expect(view.container).toBe(container);
  expect(view.baseElement).toBe(container);

  function expectConfiguration() {
    expect.soft(view.getByTestId("caller-context")).toHaveTextContent("caller wrapper active");
    expect.soft(view.getByTestId("style-layer")).toHaveTextContent("true");
    expect.soft(view.getByTestId("two-character-button").textContent).toBe("编辑");
    expect.soft(screen.queryByText("暂无数据", { selector: "div" })).toBeInTheDocument();
    expect.soft(screen.queryByRole("option", { name: "选项 29" })).toBeInTheDocument();
  }

  expect(view.getByText("A")).toBeInTheDocument();
  expectConfiguration();
  view.rerender(<ConfiguredChild label="B" />);
  expect(view.getByText("B")).toBeInTheDocument();
  expectConfiguration();
});
