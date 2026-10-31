import { clearPendingMutationKey, pendingMutationKey } from "./idempotency";

describe("pending mutation idempotency", () => {
  beforeEach(() => sessionStorage.clear());

  it("reuses only the pending random key for the same actor, target and payload", async () => {
    const actor = { actorId: "user-1", tenantId: "tenant-1", orgId: "org-1", audience: "client" };
    const payload = { identifiers: [{ value: "9132000000000001X3" }] };
    const first = await pendingMutationKey("subject.create", actor, "new", payload);
    const retry = await pendingMutationKey("subject.create", actor, "new", payload);
    expect(retry).toEqual(first);
    expect(sessionStorage.length).toBe(1);
    expect(sessionStorage.getItem(first.storageKey)).toBe(first.key);
    expect(first.storageKey).not.toContain(payload.identifiers[0]!.value);
    expect([...Array(sessionStorage.length)].map((_, index) => sessionStorage.key(index))).toEqual([first.storageKey]);

    const changedActor = await pendingMutationKey("subject.create", { ...actor, actorId: "user-2" }, "new", payload);
    const changedPayload = await pendingMutationKey("subject.create", actor, "new", { identifiers: [] });
    expect(changedActor.key).not.toBe(first.key);
    expect(changedPayload.key).not.toBe(first.key);

    clearPendingMutationKey(first.storageKey);
    expect((await pendingMutationKey("subject.create", actor, "new", payload)).key).not.toBe(first.key);
  });
});
