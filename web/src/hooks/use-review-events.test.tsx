import { act, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useReviewEvents, type ReviewEventBatch } from "./use-review-events";

class FakeEventSource {
  static latest: FakeEventSource | null = null;
  static instances: FakeEventSource[] = [];
  url: string;
  withCredentials: boolean;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  closed = false;
  readyState = 0;

  constructor(url: string, init?: { withCredentials?: boolean }) {
    this.url = url;
    this.withCredentials = Boolean(init?.withCredentials);
    FakeEventSource.latest = this;
    FakeEventSource.instances.push(this);
  }

  close() {
    this.closed = true;
  }
}

function Probe({
  scope,
  enabled = true,
  onEvent,
}: {
  scope?: string;
  enabled?: boolean;
  onEvent: (batch: ReviewEventBatch) => void | Promise<void>;
}) {
  const status = useReviewEvents(
    "/api/cases/c1/reviews/r1/events",
    enabled,
    onEvent,
    scope,
  );
  return <p>{status}</p>;
}

const originalEventSource = globalThis.EventSource;

describe("review event stream", () => {
  afterEach(() => {
    globalThis.EventSource = originalEventSource;
    FakeEventSource.latest = null;
    FakeEventSource.instances = [];
  });

  it("connects with credentials and forwards sequenced events", () => {
    globalThis.EventSource = FakeEventSource as unknown as typeof EventSource;
    const onEvent = vi.fn();
    render(<Probe onEvent={onEvent} />);
    const source = FakeEventSource.latest;
    expect(source?.url).toBe("/api/cases/c1/reviews/r1/events");
    expect(source?.withCredentials).toBe(true);
    act(() => source?.onopen?.());
    expect(screen.getByText("live")).toBeInTheDocument();
    act(() => {
      source?.onmessage?.({
        data: JSON.stringify({
          seq: 7,
          type: "run_state",
          state: "awaiting_human",
        }),
      });
      source?.onmessage?.({ data: "{" });
    });
    expect(onEvent).toHaveBeenCalledWith({
      latest: {
        seq: 7,
        type: "run_state",
        state: "awaiting_human",
      },
      terminalState: "awaiting_human",
    });
    expect(onEvent).toHaveBeenCalledTimes(1);
    act(() => source?.onerror?.());
    expect(screen.getByText("reconnecting")).toBeInTheDocument();
    // The browser owns reconnection and its Last-Event-ID resume: a transient
    // error must not replace the stream (server resumes strictly after the seq).
    expect(FakeEventSource.latest).toBe(source);
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it("closes the superseded stream and subscribes again when the session scope changes", () => {
    globalThis.EventSource = FakeEventSource as unknown as typeof EventSource;
    const onEvent = vi.fn();
    const { rerender } = render(
      <Probe scope={"tenant-a\u0000u1"} onEvent={onEvent} />,
    );
    const first = FakeEventSource.latest!;
    act(() => first.onopen?.());
    expect(screen.getByText("live")).toBeInTheDocument();

    rerender(<Probe scope={"tenant-b\u0000u1"} onEvent={onEvent} />);
    expect(first.closed).toBe(true);
    expect(first.onmessage).toBeNull();
    const second = FakeEventSource.latest!;
    expect(second).not.toBe(first);
    expect(FakeEventSource.instances).toHaveLength(2);

    // A late delivery on the superseded stream must not reach the consumer.
    act(() => {
      first.onmessage?.({
        data: JSON.stringify({ seq: 9, type: "run_state", state: "failed" }),
      });
    });
    expect(onEvent).not.toHaveBeenCalled();
    act(() => {
      second.onmessage?.({
        data: JSON.stringify({
          seq: 10,
          type: "run_state",
          state: "awaiting_human",
        }),
      });
    });
    expect(onEvent).toHaveBeenCalledTimes(1);
    expect(onEvent).toHaveBeenCalledWith({
      latest: {
        seq: 10,
        type: "run_state",
        state: "awaiting_human",
      },
      terminalState: "awaiting_human",
    });
  });

  it("reports a terminal refused connect instead of a reconnecting stream", () => {
    globalThis.EventSource = FakeEventSource as unknown as typeof EventSource;
    const onEvent = vi.fn();
    render(<Probe scope={"tenant-a\u0000u1"} onEvent={onEvent} />);
    const source = FakeEventSource.latest!;
    // A non-200 connect fails the stream: the browser sets readyState CLOSED and stops retrying.
    source.readyState = 2;
    act(() => source.onerror?.());
    expect(screen.getByText("closed")).toBeInTheDocument();
    expect(screen.queryByText("reconnecting")).not.toBeInTheDocument();
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it("opens no stream and stays inactive when the run has no active attempt", () => {
    globalThis.EventSource = FakeEventSource as unknown as typeof EventSource;
    render(<Probe enabled={false} onEvent={vi.fn()} />);
    expect(screen.getByText("inactive")).toBeInTheDocument();
    expect(FakeEventSource.instances).toHaveLength(0);
  });

  it("coalesces progress while preserving terminal and latest events across scopes", async () => {
    globalThis.EventSource = FakeEventSource as unknown as typeof EventSource;
    vi.useFakeTimers();
    try {
      const completion = Promise.withResolvers<void>();
      const onEvent = vi.fn((batch: ReviewEventBatch) =>
        batch.latest.seq === 2 ? completion.promise : undefined,
      );
      const { rerender, unmount } = render(
        <Probe scope="identity-a" onEvent={onEvent} />,
      );
      const first = FakeEventSource.latest!;
      act(() => {
        first.onmessage?.({
          data: JSON.stringify({ seq: 1, type: "state", state: "running" }),
        });
        first.onmessage?.({
          data: JSON.stringify({ seq: 2, type: "state", state: "running" }),
        });
      });
      expect(onEvent).not.toHaveBeenCalled();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(100);
      });
      expect(onEvent).toHaveBeenCalledTimes(1);
      expect(onEvent.mock.calls[0]?.[0]).toEqual({
        latest: { seq: 2, type: "state", state: "running" },
        terminalState: null,
      });

      act(() => {
        first.onmessage?.({
          data: JSON.stringify({ seq: 3, type: "state", state: "running" }),
        });
        first.onmessage?.({
          data: JSON.stringify({
            seq: 4,
            type: "state",
            state: "awaiting_human",
          }),
        });
        first.onmessage?.({
          data: JSON.stringify({
            seq: 5,
            type: "decision",
            state: "awaiting_human",
          }),
        });
      });
      expect(onEvent).toHaveBeenCalledTimes(1);
      await act(async () => {
        completion.resolve();
        await completion.promise;
      });
      expect(onEvent).toHaveBeenCalledTimes(2);
      expect(onEvent.mock.calls[1]?.[0]).toEqual({
        latest: { seq: 5, type: "decision", state: "awaiting_human" },
        terminalState: "awaiting_human",
      });

      act(() => {
        first.onmessage?.({
          data: JSON.stringify({ seq: 6, type: "state", state: "running" }),
        });
      });
      rerender(<Probe scope="identity-b" onEvent={onEvent} />);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(100);
      });
      expect(onEvent).toHaveBeenCalledTimes(2);

      const second = FakeEventSource.latest!;
      act(() => {
        second.onmessage?.({
          data: JSON.stringify({ seq: 7, type: "state", state: "running" }),
        });
      });
      unmount();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(100);
      });
      expect(onEvent).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });
});
