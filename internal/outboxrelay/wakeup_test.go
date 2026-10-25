package outboxrelay

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

const wakeupFixtureTimeout = 10 * time.Second

type wakeupProtocolBroker struct {
	listener net.Listener

	mu            sync.Mutex
	online        bool
	closed        bool
	clients       map[*wakeupProtocolClient]struct{}
	accepted      int
	rejected      int
	subscriptions int
	changed       chan struct{}

	closeOnce sync.Once
	closeDone chan struct{}
	workers   sync.WaitGroup
}

type wakeupProtocolClient struct {
	broker *wakeupProtocolBroker
	conn   net.Conn

	writeMu sync.Mutex
	subject string
	sid     string
	pongs   chan struct{}
}

type wakeupProtocolSnapshot struct {
	accepted      int
	active        int
	rejected      int
	subscriptions int
}

func newWakeupProtocolBroker(t *testing.T) *wakeupProtocolBroker {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for local NATS fixture: %v", err)
	}
	broker := &wakeupProtocolBroker{
		listener:  listener,
		online:    true,
		clients:   make(map[*wakeupProtocolClient]struct{}),
		changed:   make(chan struct{}),
		closeDone: make(chan struct{}),
	}
	broker.workers.Add(1)
	go broker.serve()
	t.Cleanup(func() {
		if !broker.close() {
			t.Errorf("local NATS fixture did not stop within the cleanup deadline")
		}
	})
	return broker
}

func (b *wakeupProtocolBroker) url() string {
	return "nats://" + b.listener.Addr().String()
}

func (b *wakeupProtocolBroker) serve() {
	defer b.workers.Done()
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			return
		}

		b.mu.Lock()
		b.accepted++
		client := &wakeupProtocolClient{
			broker: b,
			conn:   conn,
			pongs:  make(chan struct{}, 256),
		}
		serveClient := b.online && !b.closed
		if serveClient {
			b.clients[client] = struct{}{}
		} else {
			b.rejected++
		}
		b.notifyChangeLocked()
		b.mu.Unlock()

		if !serveClient {
			_ = conn.Close()
			continue
		}
		b.workers.Add(1)
		go b.serveClient(client)
	}
}

func (b *wakeupProtocolBroker) serveClient(client *wakeupProtocolClient) {
	defer b.workers.Done()
	defer func() {
		_ = client.conn.Close()
		b.mu.Lock()
		delete(b.clients, client)
		b.notifyChangeLocked()
		b.mu.Unlock()
	}()

	const greeting = "INFO {\"server_id\":\"local-wakeup-fixture\",\"version\":\"2.11.8\",\"proto\":1,\"host\":\"127.0.0.1\",\"port\":4222,\"max_payload\":1048576,\"headers\":true}\r\n"
	if err := client.write([]byte(greeting)); err != nil {
		return
	}
	reader := bufio.NewReader(client.conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "PING":
			if err := client.write([]byte("PONG\r\n")); err != nil {
				return
			}
		case "PONG":
			select {
			case client.pongs <- struct{}{}:
			default:
			}
		case "SUB":
			if len(fields) < 3 {
				continue
			}
			client.broker.mu.Lock()
			client.subject = fields[1]
			client.sid = fields[len(fields)-1]
			client.broker.subscriptions++
			client.broker.notifyChangeLocked()
			client.broker.mu.Unlock()
		}
	}
}

func (c *wakeupProtocolClient) write(payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	for len(payload) > 0 {
		written, err := c.conn.Write(payload)
		if err != nil {
			return err
		}
		payload = payload[written:]
	}
	return nil
}

func (b *wakeupProtocolBroker) sendNotification(payload string) (*wakeupProtocolClient, error) {
	b.mu.Lock()
	var target *wakeupProtocolClient
	for client := range b.clients {
		if client.subject == Subject && client.sid != "" {
			target = client
			break
		}
	}
	b.mu.Unlock()
	if target == nil {
		return nil, fmt.Errorf("no active subscription for %q", Subject)
	}

	var frame strings.Builder
	fmt.Fprintf(&frame, "MSG %s %s %d\r\n%s\r\nPING\r\n", Subject, target.sid, len(payload), payload)
	if err := target.write([]byte(frame.String())); err != nil {
		return nil, err
	}
	return target, nil
}

func (b *wakeupProtocolBroker) setOnline(online bool) {
	b.mu.Lock()
	b.online = online
	var clients []*wakeupProtocolClient
	if !online {
		for client := range b.clients {
			clients = append(clients, client)
		}
	}
	b.notifyChangeLocked()
	b.mu.Unlock()

	for _, client := range clients {
		_ = client.conn.Close()
	}
}

func (b *wakeupProtocolBroker) snapshot() wakeupProtocolSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshotLocked()
}

func (b *wakeupProtocolBroker) snapshotLocked() wakeupProtocolSnapshot {
	return wakeupProtocolSnapshot{
		accepted:      b.accepted,
		active:        len(b.clients),
		rejected:      b.rejected,
		subscriptions: b.subscriptions,
	}
}

func (b *wakeupProtocolBroker) waitFor(t *testing.T, description string, predicate func(wakeupProtocolSnapshot) bool) wakeupProtocolSnapshot {
	t.Helper()
	timer := time.NewTimer(wakeupFixtureTimeout)
	defer timer.Stop()
	for {
		b.mu.Lock()
		snapshot := b.snapshotLocked()
		changed := b.changed
		b.mu.Unlock()
		if predicate(snapshot) {
			return snapshot
		}
		select {
		case <-changed:
		case <-timer.C:
			t.Fatalf("timed out waiting for %s; last broker state: %+v", description, snapshot)
		}
	}
}

func (b *wakeupProtocolBroker) notifyChangeLocked() {
	close(b.changed)
	b.changed = make(chan struct{})
}

func (b *wakeupProtocolBroker) close() bool {
	b.closeOnce.Do(func() {
		b.mu.Lock()
		b.closed = true
		clients := make([]*wakeupProtocolClient, 0, len(b.clients))
		for client := range b.clients {
			clients = append(clients, client)
		}
		b.notifyChangeLocked()
		b.mu.Unlock()

		_ = b.listener.Close()
		for _, client := range clients {
			_ = client.conn.Close()
		}
		go func() {
			b.workers.Wait()
			close(b.closeDone)
		}()
	})

	select {
	case <-b.closeDone:
		return true
	case <-time.After(3 * time.Second):
		return false
	}
}

func wakeupTestConfig(broker *wakeupProtocolBroker) Config {
	return Config{URL: broker.url(), Subject: Subject}
}

func awaitWakeupSubscription(t *testing.T, broker *wakeupProtocolBroker) wakeupProtocolSnapshot {
	t.Helper()
	return broker.waitFor(t, "one active wake-up subscription", func(state wakeupProtocolSnapshot) bool {
		return state.active == 1 && state.subscriptions >= 1
	})
}

func awaitWakeSignal(t *testing.T, wake <-chan struct{}) {
	t.Helper()
	select {
	case <-wake:
	case <-time.After(wakeupFixtureTimeout):
		t.Fatal("timed out waiting for a wake-up notification")
	}
}

func awaitBrokerPong(t *testing.T, client *wakeupProtocolClient) {
	t.Helper()
	select {
	case <-client.pongs:
	case <-time.After(wakeupFixtureTimeout):
		t.Fatal("timed out waiting for the NATS client to process the notification frame")
	}
}

func sendWakeupNotification(t *testing.T, broker *wakeupProtocolBroker, payload string) *wakeupProtocolClient {
	t.Helper()
	client, err := broker.sendNotification(payload)
	if err != nil {
		t.Fatalf("send NATS notification: %v", err)
	}
	awaitBrokerPong(t, client)
	return client
}

func TestSubscribeLoopKeepsOneConnectionForNotificationsAndClosesOnCancellation(t *testing.T) {
	broker := newWakeupProtocolBroker(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	wake := SubscribeLoop(ctx, wakeupTestConfig(broker))
	awaitWakeupSubscription(t, broker)

	for i := range 5 {
		sendWakeupNotification(t, broker, fmt.Sprintf("outbox-%d", i))
		awaitWakeSignal(t, wake)
		state := broker.snapshot()
		if state.accepted != 1 || state.active != 1 || state.subscriptions != 1 {
			t.Fatalf("notification changed the active NATS connection or subscription count: %+v", state)
		}
	}

	cancel()
	state := broker.waitFor(t, "connection cleanup after cancellation", func(state wakeupProtocolSnapshot) bool {
		return state.active == 0
	})
	if state.accepted != 1 {
		t.Fatalf("unexpected additional NATS connections before cancellation: %+v", state)
	}
}

func TestSubscribeLoopCoalescesNotificationsWhileCallerIsBusy(t *testing.T) {
	broker := newWakeupProtocolBroker(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	wake := SubscribeLoop(ctx, wakeupTestConfig(broker))
	awaitWakeupSubscription(t, broker)

	sendWakeupNotification(t, broker, "outbox-first")
	deadline := time.NewTimer(wakeupFixtureTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for len(wake) == 0 {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("the first broker notification did not reach the busy caller")
		}
	}

	for i := range 32 {
		sendWakeupNotification(t, broker, fmt.Sprintf("outbox-burst-%d", i))
	}
	cancel()
	broker.waitFor(t, "connection cleanup after coalesced notifications", func(state wakeupProtocolSnapshot) bool {
		return state.active == 0
	})

	select {
	case <-wake:
	default:
		t.Fatal("the pending wake-up signal was lost while the caller was busy")
	}
	select {
	case <-wake:
		t.Fatal("notifications accumulated as more than one pending wake-up signal")
	default:
	}
}

func TestSubscribeLoopRecoversAfterBrokerOutage(t *testing.T) {
	broker := newWakeupProtocolBroker(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	wake := SubscribeLoop(ctx, wakeupTestConfig(broker))
	awaitWakeupSubscription(t, broker)

	broker.setOnline(false)
	broker.waitFor(t, "broker connection loss", func(state wakeupProtocolSnapshot) bool {
		return state.active == 0
	})
	broker.waitFor(t, "a reconnect attempt during the outage", func(state wakeupProtocolSnapshot) bool {
		return state.rejected > 0
	})

	broker.setOnline(true)
	state := broker.waitFor(t, "subscription recovery after the broker returns", func(state wakeupProtocolSnapshot) bool {
		return state.active == 1 && state.subscriptions >= 2
	})
	if state.accepted < 3 {
		t.Fatalf("expected an outage retry and a recovered NATS connection, got %+v", state)
	}

	sendWakeupNotification(t, broker, "outbox-after-recovery")
	awaitWakeSignal(t, wake)
	cancel()
	broker.waitFor(t, "connection cleanup after recovery cancellation", func(state wakeupProtocolSnapshot) bool {
		return state.active == 0
	})
}

func TestWakeupCloseIsSafeDuringConcurrentConnectionLoss(t *testing.T) {
	broker := newWakeupProtocolBroker(t)
	subscriber, err := Subscribe(wakeupTestConfig(broker))
	if err != nil {
		t.Fatalf("subscribe to local NATS fixture: %v", err)
	}
	subscriberOpen := true
	t.Cleanup(func() {
		if subscriberOpen {
			broker.setOnline(false)
			_ = subscriber.Close()
		}
	})
	awaitWakeupSubscription(t, broker)

	const closeCallers = 32
	start := make(chan struct{})
	ready := make(chan struct{}, closeCallers+1)
	failures := make(chan string, closeCallers)
	var callers sync.WaitGroup
	callers.Add(closeCallers)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(start) }) }
	disconnectDone := make(chan struct{})
	for range closeCallers {
		go func() {
			defer callers.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					failures <- fmt.Sprintf("concurrent Close panicked: %v", recovered)
				}
			}()
			ready <- struct{}{}
			<-start
			if err := subscriber.Close(); err != nil {
				failures <- fmt.Sprintf("concurrent Close returned an error: %v", err)
			}
		}()
	}
	go func() {
		defer close(disconnectDone)
		ready <- struct{}{}
		<-start
		broker.setOnline(false)
	}()

	finished := make(chan struct{})
	go func() {
		callers.Wait()
		<-disconnectDone
		close(finished)
	}()
	t.Cleanup(func() {
		release()
		if !broker.close() {
			t.Errorf("local NATS fixture did not stop during concurrent-close cleanup")
		}
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Errorf("concurrent close goroutines did not stop during cleanup")
		}
	})

	for range closeCallers + 1 {
		select {
		case <-ready:
		case <-time.After(wakeupFixtureTimeout):
			t.Fatal("concurrent close callers did not reach the start barrier")
		}
	}
	release()
	select {
	case <-finished:
	case <-time.After(wakeupFixtureTimeout):
		t.Fatal("concurrent close callers did not finish")
	}
	subscriberOpen = false
	select {
	case <-subscriber.Done():
	case <-time.After(wakeupFixtureTimeout):
		t.Fatal("closed wake-up subscription did not signal completion")
	}
	select {
	case failure := <-failures:
		t.Fatal(failure)
	default:
	}
	broker.waitFor(t, "connection cleanup after concurrent close", func(state wakeupProtocolSnapshot) bool {
		return state.active == 0
	})
}
