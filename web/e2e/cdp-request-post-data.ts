export interface CDPRequestPostDataInput {
  requestId?: string;
  sessionId?: string;
  postData?: string;
  getRequestPostData: (requestId: string, sessionId: string) => Promise<unknown>;
}

export async function readCDPRequestPostData(input: CDPRequestPostDataInput): Promise<string> {
  if (typeof input.postData === "string") return input.postData;
  if (!input.requestId || !input.sessionId)
    throw new Error("CDP request body lookup requires requestId and target sessionId");

  let timeout: ReturnType<typeof setTimeout> | undefined;
  let response: unknown;
  try {
    response = await Promise.race([
      input.getRequestPostData(input.requestId, input.sessionId),
      new Promise<never>((_, reject) => {
        timeout = setTimeout(() => reject(new Error("timed out reading CDP request body after 10000ms")), 10000);
      }),
    ]);
  } finally {
    if (timeout) clearTimeout(timeout);
  }
  if (response === null || typeof response !== "object" || Array.isArray(response))
    throw new Error("CDP returned an invalid request-body response");
  const record = response as Record<string, unknown>;
  if (typeof record.postData !== "string")
    throw new Error("CDP returned no request body");
  if (record.base64Encoded === true)
    return Buffer.from(record.postData, "base64").toString("utf8");
  if (record.base64Encoded !== undefined && typeof record.base64Encoded !== "boolean")
    throw new Error("CDP returned an invalid base64Encoded flag");
  return record.postData;
}
