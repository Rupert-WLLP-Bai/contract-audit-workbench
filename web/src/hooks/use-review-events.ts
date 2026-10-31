import { useEffect, useState } from "react";
import {
  isActiveAttemptState,
  isTerminalAttemptState,
  parseRunEvent,
  type RunEvent,
} from "../domain/review";

export type StreamStatus =
  | "connecting"
  | "live"
  | "reconnecting"
  | "closed"
  | "inactive";

/** The latest delivered event and any terminal transition in its batch. */
export interface ReviewEventBatch {
  latest: RunEvent;
  terminalState: string | null;
}

/** HTML Standard EventSource readyState values. */
const STREAM_READY_CLOSED = 2;

// Progress updates replace pending data without postponing the next refresh.
const EVENT_REFRESH_COALESCE_MS = 100;

function isProgressEvent(event: RunEvent): boolean {
  return (
    (event.type === "state" || event.type === "run_state") &&
    event.state !== null &&
    isActiveAttemptState(event.state)
  );
}

function isTerminalStateEvent(event: RunEvent): boolean {
  return (
    (event.type === "state" || event.type === "run_state") &&
    event.state !== null &&
    isTerminalAttemptState(event.state)
  );
}

/**
 * Subscribes to a review run's presentation stream.
 * EventSource owns cursor replay; persisted workspace reads remain authoritative.
 */
export function useReviewEvents(
  url: string,
  enabled: boolean,
  onEvent: (batch: ReviewEventBatch) => void | Promise<void>,
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
    let active = true;
    let refreshing = false;
    let pendingEvent: RunEvent | null = null;
    let pendingTerminalState: string | null = null;
    let refreshTimer: number | null = null;

    const cancelScheduledRefresh = () => {
      if (refreshTimer === null) return;
      window.clearTimeout(refreshTimer);
      refreshTimer = null;
    };

    function scheduleRefresh() {
      if (
        !active ||
        refreshing ||
        pendingEvent === null ||
        refreshTimer !== null
      )
        return;
      refreshTimer = window.setTimeout(() => {
        refreshTimer = null;
        flushRefresh();
      }, EVENT_REFRESH_COALESCE_MS);
    }

    function finishRefresh() {
      refreshing = false;
      if (!active || pendingEvent === null) return;
      if (pendingTerminalState !== null || !isProgressEvent(pendingEvent))
        flushRefresh();
      else scheduleRefresh();
    }

    function flushRefresh() {
      cancelScheduledRefresh();
      if (!active || refreshing || pendingEvent === null) return;
      const batch = {
        latest: pendingEvent,
        terminalState: pendingTerminalState,
      };
      pendingEvent = null;
      pendingTerminalState = null;
      refreshing = true;
      let result: void | Promise<void>;
      try {
        result = onEvent(batch);
      } catch {
        finishRefresh();
        return;
      }
      if (result === undefined) finishRefresh();
      else void result.then(finishRefresh, finishRefresh);
    }

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
      if (!active) return;
      const event = parseRunEvent(message.data);
      if (!event) return;
      pendingEvent = event;
      if (isTerminalStateEvent(event)) pendingTerminalState = event.state;
      if (pendingTerminalState !== null || !isProgressEvent(event))
        flushRefresh();
      else scheduleRefresh();
    };
    return () => {
      active = false;
      cancelScheduledRefresh();
      pendingEvent = null;
      pendingTerminalState = null;
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
