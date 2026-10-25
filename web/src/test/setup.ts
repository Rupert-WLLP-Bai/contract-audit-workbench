import * as matchers from "@testing-library/jest-dom/matchers";
import { cleanup } from "@testing-library/react";
import { afterEach, expect } from "vitest";

expect.extend(matchers);
// Always clean up explicitly: relying on runner globals left old DOM trees
// behind and made the original component suite order-dependent.
afterEach(() => cleanup());
