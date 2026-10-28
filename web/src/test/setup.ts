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
