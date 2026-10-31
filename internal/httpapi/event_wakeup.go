package httpapi

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"contractaudit/internal/outboxrelay"
	"contractaudit/internal/store"

	"github.com/nats-io/nats.go"
)

type eventWakeupKey struct {
	tenantID string
	caseID   string
	runID    string
}

// eventWakeups shares a broadcast subscription between this handler's streams.
// Notifications are hints only; event contents and authorization come from PG.
type eventWakeups struct {
	url       string
	mu        sync.Mutex
	listeners map[eventWakeupKey]map[chan struct{}]struct{}
	running   bool
	cancel    context.CancelFunc
}

func newEventWakeups() *eventWakeups {
	return &eventWakeups{
		url:       strings.TrimSpace(os.Getenv("NATS_URL")),
		listeners: make(map[eventWakeupKey]map[chan struct{}]struct{}),
	}
}

func (h *eventWakeups) subscribe(key eventWakeupKey) (<-chan struct{}, func()) {
	if h.url == "" {
		return nil, func() {}
	}
	wake := make(chan struct{}, 1)
	h.mu.Lock()
	if h.listeners[key] == nil {
		h.listeners[key] = make(map[chan struct{}]struct{})
	}
	h.listeners[key][wake] = struct{}{}
	h.startLocked()
	h.mu.Unlock()
	return wake, func() {
		h.mu.Lock()
		delete(h.listeners[key], wake)
		if len(h.listeners[key]) == 0 {
			delete(h.listeners, key)
		}
		if len(h.listeners) == 0 && h.cancel != nil {
			h.cancel()
		}
		h.mu.Unlock()
	}
}

// At most one connection attempt or subscriber runs per handler, even if the
// last stream disconnects while a new one is opening.
func (h *eventWakeups) startLocked() {
	if h.running || len(h.listeners) == 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.running = true
	go h.run(ctx)
}

func (h *eventWakeups) run(ctx context.Context) {
	defer func() {
		h.mu.Lock()
		h.running = false
		h.cancel = nil
		h.startLocked()
		h.mu.Unlock()
	}()
	for ctx.Err() == nil {
		closed := make(chan struct{})
		conn, err := nats.Connect(h.url,
			nats.Name("contract-audit-event-wakeup"),
			nats.Timeout(2*time.Second),
			nats.MaxReconnects(-1),
			nats.ReconnectWait(2*time.Second),
			nats.ClosedHandler(func(*nats.Conn) { close(closed) }),
		)
		if err == nil {
			// This is a broadcast subscriber, not the workers' queue group: every
			// API replica must see the hint without consuming a worker's wake-up.
			_, err = conn.Subscribe(outboxrelay.Subject, h.deliver)
			if err == nil {
				select {
				case <-ctx.Done():
				case <-closed:
				}
			}
			conn.Close()
		}
		if ctx.Err() != nil {
			return
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (h *eventWakeups) deliver(message *nats.Msg) {
	var notice store.OutboxRecord
	if json.Unmarshal(message.Data, &notice) != nil || notice.EventType != "review.accepted" {
		return
	}
	var acceptance store.JobAcceptance
	if json.Unmarshal([]byte(notice.Payload), &acceptance) != nil || acceptance.RunID == "" {
		return
	}
	key := eventWakeupKey{tenantID: notice.TenantID, caseID: notice.CaseID, runID: acceptance.RunID}
	h.mu.Lock()
	defer h.mu.Unlock()
	for wake := range h.listeners[key] {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}
