// File metadata (manifest mirror):
// Purpose: Installs Testing Library DOM matchers, raises async UI wait time to five seconds for coverage runs, and cleans rendered React trees after every Vitest case.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-10-28
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import * as matchers from "@testing-library/jest-dom/matchers";
import { configure, cleanup } from "@testing-library/react";
import { afterEach, expect } from "vitest";

expect.extend(matchers);
// Coverage instrumentation makes the largest route journeys render slower.
// Wait for observable UI instead of failing on the 1s default; assertions and
// coverage gates are unchanged.
configure({ asyncUtilTimeout: 5_000 });
// Always clean up explicitly: relying on runner globals left old DOM trees
// behind and made the original component suite order-dependent.
afterEach(() => cleanup());
