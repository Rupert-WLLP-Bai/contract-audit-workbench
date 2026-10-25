import { useEffect, useState } from "react";
import { parseRunEvent, type RunEvent } from "../domain/review";

export type StreamStatus =
  | "connecting"
  | "live"
  | "reconnecting"
  | "closed"
  | "inactive";

/** HTML Standard EventSource readyState values. */
const STREAM_READY_CLOSED = 2;

/**
 * Subscribes to a review run's presentation stream.
 * EventSource owns cursor replay; persisted workspace reads remain authoritative.
 */
export function useReviewEvents(
  url: string,
  enabled: boolean,
  onEvent: (event: RunEvent) => void,
  scopeKey?: string | null,
): StreamStatus {
  const [status, setStatus] = useState<StreamStatus>("connecting");

  useEffect(() => {
    if (!enabled) {
      setStatus("inactive");
      return undefined;
    }
    setStatus("connecting");
    const source = new EventSource(url, { withCredentials: true });
    source.onopen = () => setStatus("live");
    source.onerror = () => {
      // A refused connect (revoked session, missing case grant) fails the stream
      // terminally: the browser stops retrying because readyState is CLOSED. Only
      // a dropped connection stays CONNECTING and retries with Last-Event-ID.
      setStatus(
        source.readyState === STREAM_READY_CLOSED ? "closed" : "reconnecting",
      );
    };
    source.onmessage = (message) => {
      const event = parseRunEvent(message.data);
      if (event) onEvent(event);
    };
    return () => {
      // Detach before closing: a superseded stream must not be able to feed the
      // current identity's cache, even if a queued delivery is dispatched late.
      source.onopen = null;
      source.onerror = null;
      source.onmessage = null;
      source.close();
    };
  }, [url, enabled, onEvent, scopeKey]);

  return enabled ? status : "inactive";
}
