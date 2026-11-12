// File metadata (manifest mirror):
// Purpose: Exercises the application bootstrap and browser-history error recovery without depending on the failed component library to render the fallback.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: unknown
// Updated: unknown
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import * as client from "react-dom/client";
import { Link, Route, Routes, useNavigate } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { mountApplication } from "./bootstrap";

// Keep real React, the renderer, router and bootstrap; theme setup is outside
// the navigation/recovery contract and CSS variables are absent in this DOM.
vi.mock("antd", () => ({
  ConfigProvider: ({ children }: { children: ReactNode }) => children,
  App: ({ children }: { children: ReactNode }) => children,
}));
vi.mock("@ant-design/cssinjs", () => ({
  StyleProvider: ({ children }: { children: ReactNode }) => children,
}));
vi.mock("react-dom/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-dom/client")>();
  return { ...actual, createRoot: vi.fn(actual.createRoot) };
});

let root: client.Root | undefined;

afterEach(async () => {
  if (root) await act(async () => root?.unmount());
  root = undefined;
  document.getElementById("root")?.remove();
  window.history.replaceState(null, "", "/");
  vi.unstubAllGlobals();
});

it("recovers a render failure after browser Back with a reload of the same evidence address", async () => {
  vi.spyOn(console, "error").mockImplementation(() => undefined);
  vi.stubGlobal("matchMedia", vi.fn(() => ({ matches: false })));
  const createRoot = vi.mocked(client.createRoot);
  const reload = vi.spyOn(window.location, "reload").mockImplementation(() => undefined);
  const evidenceAddress = "/workbench/evidence?case_id=case_fixture&tab=citations#source";
  window.history.replaceState(null, "", evidenceAddress);
  document.body.insertAdjacentHTML("beforeend", '<div id="root"></div>');
  let failOnReturn = false;

  function Evidence() {
    if (failOnReturn) throw new TypeError("Cannot read properties of null (reading 'useContext')");
    return <Link to="/subjects">查看主体台账</Link>;
  }
  function Subjects() {
    const navigate = useNavigate();
    return <button onClick={() => navigate(-1)}>返回上一页</button>;
  }

  await act(async () => {
    mountApplication(
      <Routes>
        <Route path="/workbench/evidence" element={<Evidence />} />
        <Route path="/subjects" element={<Subjects />} />
      </Routes>,
    );
  });
  root = createRoot.mock.results[0]?.value;
  const user = userEvent.setup();
  await user.click(await screen.findByRole("link", { name: "查看主体台账" }));
  failOnReturn = true;
  await user.click(await screen.findByRole("button", { name: "返回上一页" }));

  expect(await screen.findByRole("heading", { name: "页面暂时无法显示" })).toBeVisible();
  expect(screen.getByRole("alert")).not.toHaveTextContent("useContext");
  expect(screen.queryByText("Unexpected Application Error!")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "重新加载当前页面" }));
  await waitFor(() => expect(reload).toHaveBeenCalledOnce());
  expect(window.location.pathname + window.location.search + window.location.hash).toBe(evidenceAddress);
});
