// File metadata (manifest mirror):
// Purpose: 声明 `CHANNEL_NAME`, `STORAGE_SIGNAL_KEY`, `installContextInvalidation` 等 API 类型、解析器或请求函数，校验服务端响应并封装受控调用。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

const CHANNEL_NAME = "caw-session-context";
const STORAGE_SIGNAL_KEY = "caw:session-context-invalidated";

export function installContextInvalidation(onInvalidate: () => void): void {
  if (typeof window === "undefined") return;
  let channel: BroadcastChannel | null = null;
  if (typeof BroadcastChannel !== "undefined") {
    channel = new BroadcastChannel(CHANNEL_NAME);
    channel.addEventListener("message", (event: MessageEvent<unknown>) => {
      if (event.data === "invalidate") {
        clearAllDecisionDrafts();
        onInvalidate();
      }
    });
  }
  window.addEventListener("storage", (event) => {
    if (event.key === STORAGE_SIGNAL_KEY) {
      clearAllDecisionDrafts();
      onInvalidate();
    }
  });
  window.addEventListener("caw:context-broadcast", () => {
    channel?.postMessage("invalidate");
    if (channel) return;
    try {
      localStorage.setItem(STORAGE_SIGNAL_KEY, crypto.randomUUID());
      localStorage.removeItem(STORAGE_SIGNAL_KEY);
    } catch {
      // BroadcastChannel remains available when storage is blocked.
    }
  });
}
import { clearAllDecisionDrafts } from "../hooks/decision-drafts";

