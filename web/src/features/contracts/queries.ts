// File metadata (manifest mirror):
// Purpose: Provides contract-specific React Query reads with authorization-scoped cache keys and abort propagation.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { useQuery } from "@tanstack/react-query";
import {
  listContractAcceptances,
  listContractDeliveries,
  listContractReminders,
  listMyContractReminderInbox,
} from "../../api/contracts";
import type { Actor } from "../../api/types";
import { actorReadContextKey } from "../../domain/actor-read-context";

export function useContractRemindersQuery(
  contractId: string,
  revisionId: string | null,
  rowVersion: number,
  actor: Actor | null,
  contractActive: boolean,
  enabled = true,
) {
  const actorScope = actorReadContextKey(actor);
  return useQuery({
    queryKey: [
      "contracts",
      "reminders",
      actorScope,
      contractId,
      revisionId,
      rowVersion,
      contractActive,
    ] as const,
    queryFn: ({ signal }) => listContractReminders(contractId, signal),
    enabled: enabled && actor !== null && contractId !== "",
    retry: false,
  });
}

export function useContractReminderInboxQuery(actor: Actor | null) {
  const actorScope = actorReadContextKey(actor);
  return useQuery({
    queryKey: ["contracts", "reminder-inbox", actorScope] as const,
    queryFn: ({ signal }) => listMyContractReminderInbox(signal),
    enabled: actor !== null,
    retry: false,
  });
}

export function useContractFulfillmentQuery(
  contractId: string,
  revisionId: string | null,
  rowVersion: number,
  actor: Actor | null,
  enabled = true,
) {
  const actorScope = actorReadContextKey(actor);
  return useQuery({
    queryKey: ["contracts", "fulfillment", actorScope, contractId, revisionId, rowVersion] as const,
    queryFn: async ({ signal }) => {
      const [deliveries, acceptances] = await Promise.all([
        listContractDeliveries(contractId, signal),
        listContractAcceptances(contractId, signal),
      ]);
      return { deliveries, acceptances };
    },
    enabled: enabled && actor !== null && contractId !== "",
    retry: false,
  });
}
