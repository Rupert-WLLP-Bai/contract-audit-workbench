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

