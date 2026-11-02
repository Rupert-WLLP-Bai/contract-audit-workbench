// File metadata (manifest mirror):
// Purpose: Checks the web behavior for “CDP request body capture”; representative assertions cover uses the event body when present without another CDP call, retrieves a base64 body using the captured request and target session, fails closed when the request body cannot be retrieved. It protects the rendered/API state expected by this component without starting the full browser journey.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-10-31
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { describe, expect, test } from "bun:test";
import { readCDPRequestPostData } from "./cdp-request-post-data";

describe("CDP request body capture", () => {
  test("uses the event body when present without another CDP call", async () => {
    let calls = 0;
    const body = await readCDPRequestPostData({
      requestId: "request-1",
      sessionId: "target-1",
      postData: "multipart body",
      getRequestPostData: async () => {
        calls += 1;
        return {};
      },
    });

    expect(body).toBe("multipart body");
    expect(calls).toBe(0);
  });

  test("retrieves a base64 body using the captured request and target session", async () => {
    let captured: string[] = [];
    const body = await readCDPRequestPostData({
      requestId: "request-2",
      sessionId: "target-2",
      getRequestPostData: async (requestId, sessionId) => {
        captured = [requestId, sessionId];
        return { postData: Buffer.from("multipart body", "utf8").toString("base64"), base64Encoded: true };
      },
    });

    expect(body).toBe("multipart body");
    expect(captured).toEqual(["request-2", "target-2"]);
  });

  test("fails closed when the request body cannot be retrieved", async () => {
    await expect(readCDPRequestPostData({
      requestId: "request-3",
      sessionId: "target-3",
      getRequestPostData: async () => ({ }),
    })).rejects.toThrow("CDP returned no request body");
  });
});
