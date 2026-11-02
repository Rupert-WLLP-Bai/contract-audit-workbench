import { clearPendingMutationKey, idempotencyActorScope, pendingMutationKey } from "./idempotency";
import { captureCommandContext, setSessionIdentity } from "./http";

describe("pending mutation idempotency", () => {
  beforeEach(() => {
    sessionStorage.clear();
    setSessionIdentity(null);
  });

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

  it("does not send a hash-delayed command after a new session reuses context version 1", async () => {
    const actorA = {
      id: "actor-a", role: "clerk", audience: "client", tenant_id: "tenant-a", org_id: "org-a",
      principal_id: "principal-a", active_context: { context_version: "1" }, csrf_token: "csrf-a",
    };
    const actorB = {
      ...actorA, id: "actor-b", tenant_id: "tenant-b", org_id: "org-b", principal_id: "principal-b", csrf_token: "csrf-b",
    };
    setSessionIdentity(actorA);
    const snapshot = captureCommandContext(actorA);
    const digest = crypto.subtle.digest.bind(crypto.subtle);
    let releaseDigest!: () => void;
    const digestSpy = vi.spyOn(crypto.subtle, "digest").mockImplementation((algorithm, data) =>
      new Promise<ArrayBuffer>((resolve, reject) => {
        releaseDigest = () => { void digest(algorithm, data).then(resolve, reject); };
      }),
    );
    const send = vi.fn();
    const command = pendingMutationKey(
      "evidence.upload",
      idempotencyActorScope(actorA),
      "case-a",
      { digest: "file" },
      snapshot,
    ).then(({ key }) => send(key));
    setSessionIdentity(actorB);
    releaseDigest();
    await expect(command).rejects.toMatchObject({ name: "AbortError" });
    expect(send).not.toHaveBeenCalled();
    digestSpy.mockRestore();
  });
});
