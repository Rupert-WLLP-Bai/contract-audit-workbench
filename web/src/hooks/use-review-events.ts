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
  | "exhausted"
  | "closed"
  | "inactive";

/** The latest delivered event and any terminal transition in its batch. */
export interface ReviewEventBatch {
  latest: RunEvent;
  terminalState: string | null;
}

/** HTML Standard EventSource readyState values. */
const STREAM_READY_CLOSED = 2;

const MAX_AUTOMATIC_RECONNECT_ATTEMPTS = 3;
const STREAM_STABLE_CONNECTION_MS = 30_000;

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
 * Native retries preserve Last-Event-ID; callers re-read persisted workspace
 * state after a failed or manually restarted connection recovers.
 */
export function useReviewEvents(
  url: string,
  enabled: boolean,
  onEvent: (batch: ReviewEventBatch) => void | Promise<void>,
  scopeKey?: string | null,
  reconnectKey = 0,
  onReconnect?: () => void,
): StreamStatus {
  const [status, setStatus] = useState<StreamStatus>("connecting");

  useEffect(() => {
    if (!enabled) {
      setStatus("inactive");
      return undefined;
    }
    setStatus("connecting");
    let active = true;
    let stopped = false;
    let reconnectAttempts = 0;
    let needsRefreshOnOpen = reconnectKey > 0;
    const source = new EventSource(url, { withCredentials: true });
    let refreshing = false;
    let pendingEvent: RunEvent | null = null;
    let pendingTerminalState: string | null = null;
    let refreshTimer: number | null = null;
    let stableConnectionTimer: number | null = null;

    const cancelStabilityTimer = () => {
      if (stableConnectionTimer === null) return;
      window.clearTimeout(stableConnectionTimer);
      stableConnectionTimer = null;
    };

    const scheduleStabilityReset = () => {
      cancelStabilityTimer();
      stableConnectionTimer = window.setTimeout(() => {
        stableConnectionTimer = null;
        reconnectAttempts = 0;
      }, STREAM_STABLE_CONNECTION_MS);
    };

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

    source.onopen = () => {
      if (!active || stopped) return;
      const recovered = needsRefreshOnOpen;
      needsRefreshOnOpen = false;
      scheduleStabilityReset();
      setStatus("live");
      if (recovered) onReconnect?.();
    };
    source.onerror = () => {
      if (!active || stopped) return;
      cancelStabilityTimer();
      if (source.readyState === STREAM_READY_CLOSED) {
        stopped = true;
        source.close();
        setStatus("closed");
        return;
      }
      needsRefreshOnOpen = true;
      reconnectAttempts += 1;
      // The first error schedules retry one; exhaust after three retries fail.
      if (reconnectAttempts > MAX_AUTOMATIC_RECONNECT_ATTEMPTS) {
        stopped = true;
        source.close();
        setStatus("exhausted");
        return;
      }
      setStatus("reconnecting");
    };
    source.onmessage = (message) => {
      if (!active || stopped) return;
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
      cancelStabilityTimer();
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
  }, [url, enabled, onEvent, scopeKey, reconnectKey, onReconnect]);

  return enabled ? status : "inactive";
}
