/** Test-only renderer and client stub for the operator-audit panels. */

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactElement } from "react";
import { renderWithAntd } from "../../test/antd";
import type { OperatorAuditClient } from "./api";

export function renderOperatorAudit(ui: ReactElement) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  const result = renderWithAntd(
    <QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>,
  );
  return { ...result, queryClient };
}

export function operatorAuditClient(
  overrides: Partial<OperatorAuditClient> = {},
): OperatorAuditClient {
  const missing = async (): Promise<never> => {
    throw new Error("测试没有提供该接口的响应");
  };
  return {
    listCapabilityPacks: missing,
    createObjectBinding: missing,
    createExternalCollectionJob: missing,
    listExternalSnapshots: missing,
    createFulfillmentEvaluation: missing,
    getFulfillmentEvaluation: missing,
    getImpactPlan: missing,
    ...overrides,
  };
}

export function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}
