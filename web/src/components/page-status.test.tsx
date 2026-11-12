// File metadata (manifest mirror):
// Purpose: Checks that unknown page state is presented as unverified and remains distinct from empty, failed and forbidden states.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { renderWithAntd as render } from "../test/antd";
import { PageStatus } from "./page-status";

it("presents unknown state as unverified without using the empty or alert role", () => {
  render(<PageStatus kind="unknown" title="尚未核实" detail="当前没有可确认的来源记录。" />);

  expect(screen.getByRole("status")).toHaveTextContent("尚未核实");
  expect(screen.getByRole("status")).toHaveTextContent("当前没有可确认的来源记录。");
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  expect(screen.queryByRole("img", { name: /empty/i })).not.toBeInTheDocument();
});
