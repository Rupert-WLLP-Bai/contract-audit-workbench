import * as matchers from "@testing-library/jest-dom/matchers";
import { cleanup, configure } from "@testing-library/react";
import { afterEach, expect } from "vitest";

expect.extend(matchers);
configure({ asyncUtilTimeout: 3000 });
// Always clean up explicitly: relying on runner globals left old DOM trees
// behind and made the original component suite order-dependent.
afterEach(() => cleanup());
