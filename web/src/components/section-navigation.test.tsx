// File metadata (manifest mirror):
// Purpose: Verifies scoped section navigation, ancestor expansion, pending-target cancellation and focus fallback behavior.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-05
// Updated: 2026-11-08
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";
import type { Actor } from "../api/types";
import { actorReadContextKey } from "../domain/actor-read-context";
import { CollapsibleSection, SectionAnchorNav } from "./collapsible-section";
import { readCaseListReturn, readReviewReadingPosition, SectionNavigationProvider } from "./section-navigation";

function Sections({ scope = "actor-a:case-a", actorScope, ready = true, waiting = false, showTarget = true }: {
  scope?: string;
  actorScope?: string;
  ready?: boolean;
  waiting?: boolean;
  showTarget?: boolean;
}) {
  return (
    <SectionNavigationProvider scope={scope} actorScope={actorScope} ready={ready} targetPending={() => waiting}>
      <SectionAnchorNav items={[{ id: "inside-title", label: "目标" }, { id: "outer", label: "外层" }]} />
      <button type="button">继续其他工作</button>
      <CollapsibleSection id="outer" title="外层">
        <CollapsibleSection id="inner" title="内层">
          {showTarget ? <h2 id="inside-title">内部标题</h2> : null}
        </CollapsibleSection>
      </CollapsibleSection>
    </SectionNavigationProvider>
  );
}

describe("page section navigation", () => {
  it("opens all controlled ancestors for an initial hash and a repeat click", async () => {
    const user = userEvent.setup();
    render(<MemoryRouter initialEntries={["/cases/c#inside-title"]}><Sections /></MemoryRouter>);
    await waitFor(() => expect(screen.getByRole("heading", { name: "内部标题" })).toHaveFocus());
    const outer = document.getElementById("outer") as HTMLDetailsElement;
    const inner = document.getElementById("inner") as HTMLDetailsElement;
    expect(outer.open && inner.open).toBe(true);
    await user.click(screen.getByText("内层"));
    expect(inner.open).toBe(false);
    await user.click(screen.getByRole("link", { name: "跳转到目标" }));
    await waitFor(() => expect(screen.getByRole("heading", { name: "内部标题" })).toHaveFocus());
    expect(outer.open && inner.open).toBe(true);
  });

  it("waits for an asynchronous target and stops waiting when the user moves on", async () => {
    const view = render(<MemoryRouter initialEntries={["/cases/c#inside-title"]}><Sections waiting showTarget={false} /></MemoryRouter>);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    view.rerender(<MemoryRouter initialEntries={["/cases/c#inside-title"]}><Sections waiting={false} /></MemoryRouter>);
    await waitFor(() => expect(screen.getByRole("heading", { name: "内部标题" })).toHaveFocus());
    view.unmount();

    const next = render(<MemoryRouter initialEntries={["/cases/c#inside-title"]}><Sections waiting showTarget={false} /></MemoryRouter>);
    const other = screen.getByRole("button", { name: "继续其他工作" });
    fireEvent.pointerDown(other);
    other.focus();
    next.rerender(<MemoryRouter initialEntries={["/cases/c#inside-title"]}><Sections waiting={false} /></MemoryRouter>);
    await waitFor(() => expect(document.getElementById("inside-title")).toBeInTheDocument());
    expect(other).toHaveFocus();
    expect((document.getElementById("outer") as HTMLDetailsElement).open).toBe(false);
  });

  it("reports a missing target and does not apply an old request after an identity change", async () => {
    const view = render(<MemoryRouter initialEntries={["/cases/c#missing"]}><Sections showTarget={false} /></MemoryRouter>);
    expect(await screen.findByRole("alert")).toHaveTextContent("无法找到或读取所选分区");
    view.unmount();

    const next = render(<MemoryRouter initialEntries={["/cases/c#inside-title"]}><Sections waiting showTarget={false} /></MemoryRouter>);
    next.rerender(<MemoryRouter initialEntries={["/cases/c#inside-title"]}><Sections scope="actor-b:case-a" /></MemoryRouter>);
    await vi.waitFor(() => expect(document.getElementById("inside-title")).toBeInTheDocument());
    expect(document.getElementById("inside-title")).not.toHaveFocus();
    expect((document.getElementById("outer") as HTMLDetailsElement).open).toBe(false);
  });

  it("does not steal focus after the user continues while authorization is being checked", async () => {
    const view = render(<MemoryRouter initialEntries={["/cases/c#inside-title"]}><Sections ready={false} /></MemoryRouter>);
    const other = screen.getByRole("button", { name: "继续其他工作" });
    fireEvent.pointerDown(other);
    other.focus();
    view.rerender(<MemoryRouter initialEntries={["/cases/c#inside-title"]}><Sections ready /></MemoryRouter>);
    await waitFor(() => expect(other).toHaveFocus());
    expect((document.getElementById("outer") as HTMLDetailsElement).open).toBe(false);
  });

  it("does not replay a pending hash when only the explicit actor context changes", async () => {
    const view = render(<MemoryRouter initialEntries={["/cases/c#inside-title"]}>
      <Sections scope="case-c:actor-a" actorScope="actor-a" waiting showTarget={false} />
    </MemoryRouter>);
    const previousFocus = document.activeElement;
    await act(async () => {
      view.rerender(<MemoryRouter initialEntries={["/cases/c#inside-title"]}>
        <Sections scope="case-c:actor-b" actorScope="actor-b" />
      </MemoryRouter>);
      await new Promise((resolve) => window.setTimeout(resolve, 120));
    });
    expect((document.getElementById("outer") as HTMLDetailsElement).open).toBe(false);
    expect(document.activeElement).toBe(previousFocus);
  });

  it("preserves cancellation while the initial actor context is being confirmed", async () => {
    const view = render(<MemoryRouter initialEntries={["/cases/c#inside-title"]}>
      <Sections scope="case-c:" actorScope="" ready={false} />
    </MemoryRouter>);
    fireEvent.wheel(document, { deltaY: 400 });
    const previousFocus = document.activeElement;
    await act(async () => {
      view.rerender(<MemoryRouter initialEntries={["/cases/c#inside-title"]}>
        <Sections scope="case-c:actor-a" actorScope="actor-a" />
      </MemoryRouter>);
      await new Promise((resolve) => window.setTimeout(resolve, 120));
    });
    expect((document.getElementById("outer") as HTMLDetailsElement).open).toBe(false);
    expect(document.activeElement).toBe(previousFocus);
    view.unmount();

    const uninterrupted = render(<MemoryRouter initialEntries={["/cases/c#inside-title"]}>
      <Sections scope="case-c:" actorScope="" ready={false} />
    </MemoryRouter>);
    uninterrupted.rerender(<MemoryRouter initialEntries={["/cases/c#inside-title"]}>
      <Sections scope="case-c:actor-a" actorScope="actor-a" />
    </MemoryRouter>);
    await waitFor(() => expect(screen.getByRole("heading", { name: "内部标题" })).toHaveFocus());
  });

  it("does not navigate to an asynchronous target after the user wheels to continue reading", async () => {
    const view = render(<MemoryRouter initialEntries={["/cases/c#inside-title"]}><Sections waiting showTarget={false} /></MemoryRouter>);
    const previousFocus = document.activeElement;
    fireEvent.wheel(document, { deltaY: 400 });
    await act(async () => {
      view.rerender(<MemoryRouter initialEntries={["/cases/c#inside-title"]}><Sections waiting={false} /></MemoryRouter>);
      await new Promise((resolve) => window.setTimeout(resolve, 120));
    });
    expect((document.getElementById("outer") as HTMLDetailsElement).open).toBe(false);
    expect(document.activeElement).toBe(previousFocus);
  });

  it.each(["hidden", "aria-hidden", "inert"])("waits for a committed %s panel before opening and focusing", async (attribute) => {
    const reveal = vi.fn(() => true);
    render(<MemoryRouter initialEntries={["/cases/c#panel-target"]}>
      <SectionNavigationProvider scope="case" ready revealTarget={reveal}>
        <div data-testid="panel" {...{ [attribute]: attribute === "aria-hidden" ? "true" : true }}>
          <CollapsibleSection id="panel-details" title="详情"><h2 id="panel-target">目标标题</h2></CollapsibleSection>
        </div>
      </SectionNavigationProvider>
    </MemoryRouter>);
    const target = document.getElementById("panel-target")!;
    expect(reveal).toHaveBeenCalledWith("panel-target", target);
    expect((document.getElementById("panel-details") as HTMLDetailsElement).open).toBe(false);
    expect(target).not.toHaveFocus();
    act(() => screen.getByTestId("panel").removeAttribute(attribute));
    await waitFor(() => expect(target).toHaveFocus());
    expect((document.getElementById("panel-details") as HTMLDetailsElement).open).toBe(true);
  });

  it("resolves a dynamic target after the page reports readiness", async () => {
    const reveal = vi.fn((_id: string, target: HTMLElement | null) => target !== null);
    const view = render(<MemoryRouter initialEntries={["/cases/c#dynamic"]}>
      <SectionNavigationProvider scope="case" ready revealTarget={reveal}><div /></SectionNavigationProvider>
    </MemoryRouter>);
    expect(reveal).toHaveBeenCalledWith("dynamic", null);
    view.rerender(<MemoryRouter initialEntries={["/cases/c#dynamic"]}>
      <SectionNavigationProvider scope="case" ready revealTarget={reveal}><h2 id="dynamic">动态目标</h2></SectionNavigationProvider>
    </MemoryRouter>);
    await waitFor(() => expect(screen.getByRole("heading", { name: "动态目标" })).toHaveFocus());
    expect(reveal).toHaveBeenCalledWith("dynamic", document.getElementById("dynamic"));
  });

  it("requests reveal for a missing dynamic target and cancels when the user moves on", async () => {
    const reveal = vi.fn(() => false);
    const view = render(<MemoryRouter initialEntries={["/cases/c#dynamic"]}>
      <SectionNavigationProvider scope="case" ready revealTarget={reveal}>
        <button>其他工作</button>
      </SectionNavigationProvider>
    </MemoryRouter>);
    expect(reveal).toHaveBeenCalledWith("dynamic", null);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    fireEvent.pointerDown(screen.getByRole("button", { name: "其他工作" }));
    const calls = reveal.mock.calls.length;
    view.rerender(<MemoryRouter initialEntries={["/cases/c#dynamic"]}>
      <SectionNavigationProvider scope="case" ready revealTarget={reveal}>
        <button>其他工作</button><h2 id="dynamic">动态目标</h2>
      </SectionNavigationProvider>
    </MemoryRouter>);
    await act(async () => { await new Promise((resolve) => window.setTimeout(resolve, 50)); });
    expect(reveal).toHaveBeenCalledTimes(calls);
    expect(screen.getByRole("heading", { name: "动态目标" })).not.toHaveFocus();
  });

  it("reports a malformed fragment instead of silently dropping it", async () => {
    render(<MemoryRouter initialEntries={["/cases/c#%ZZ"]}><Sections /></MemoryRouter>);
    expect(await screen.findByRole("alert")).toHaveTextContent("无法找到或读取所选分区");
    expect((document.getElementById("outer") as HTMLDetailsElement).open).toBe(false);
  });
});

it("accepts return positions only for the same actor, case and attempt", () => {
  const list = { returnTo: "/cases?q=示例&page=2&selected=c1", scrollY: 220, selectedId: "c1", actorScope: "actor-a" };
  const restored = readCaseListReturn(list, "c1", "actor-a");
  expect(restored).toMatchObject({ scrollY: 220, selectedId: "c1", actorScope: "actor-a" });
  expect(new URL(restored!.returnTo, "https://navigation.invalid").searchParams.get("q")).toBe("示例");
  expect(readCaseListReturn(list, "c1", "actor-b")).toBeNull();
  expect(readCaseListReturn({ ...list, returnTo: "//outside.invalid/cases?selected=c1" }, "c1", "actor-a")).toBeNull();
  expect(readCaseListReturn({ ...list, returnTo: `${list.returnTo}&unrelated=secret` }, "c1", "actor-a")?.returnTo).not.toContain("secret");
  const state = { reviewReadingPosition: { caseId: "c1", runId: "r1", attemptId: "a1", actorScope: "actor-a", scrollY: 50, documentScroll: 120, findingScroll: 75 } };
  expect(readReviewReadingPosition(state, "c1", "r1", "a1", "actor-a")).toEqual(state.reviewReadingPosition);
  expect(readReviewReadingPosition(state, "c2", "r1", "a1", "actor-a")).toBeNull();
  expect(readReviewReadingPosition(state, "c1", "r2", "a1", "actor-a")).toBeNull();
  expect(readReviewReadingPosition(state, "c1", "r1", "a2", "actor-a")).toBeNull();
  expect(readReviewReadingPosition(state, "c1", "r1", "a1", "actor-b")).toBeNull();
});

it("rejects list state after account, tenant, role or authorization context changes", () => {
  const actor: Actor = { id: "user-a", display_name: "测试", role: "reviewer", tenant_id: "tenant-a", org_id: "org-a", audience: "client",
    principal_id: null, active_context: { membership_id: null, tenant_id: "tenant-a", tenant_name: "租户", org_id: "org-a", org_name: "组织", context_version: "1" },
    csrf_token: "synthetic", context_switch_enabled: false };
  const scope = actorReadContextKey(actor);
  const state = { returnTo: "/cases?selected=c1", scrollY: 50, selectedId: "c1", actorScope: scope };
  const changed: Actor[] = [
    { ...actor, id: "user-b" },
    { ...actor, tenant_id: "tenant-b" },
    { ...actor, org_id: "org-b" },
    { ...actor, role: "viewer" },
    { ...actor, principal_id: "principal-b" },
    { ...actor, active_context: { ...actor.active_context, membership_id: "membership-b" } },
    { ...actor, active_context: { ...actor.active_context, context_version: "2" } },
  ];
  for (const next of changed) expect(readCaseListReturn(state, "c1", actorReadContextKey(next))).toBeNull();
});
