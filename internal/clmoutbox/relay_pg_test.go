package clmoutbox

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/clmoutbox/consumer"
	"contractaudit/internal/config"
	"contractaudit/internal/outboxrelay"
	"contractaudit/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestCLMOutboxRelayRetryConsumerRefreshDoesNotChangeBusinessFacts crosses
// the real Core Store, PostgreSQL outbox, relay, and metrics consumer boundary.
// It requires the isolated PG18 database provisioned by pr-fast-gate.
func TestCLMOutboxRelayRetryConsumerRefreshDoesNotChangeBusinessFacts(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	migratorDSN := strings.TrimSpace(os.Getenv("MIGRATOR_DATABASE_URL"))
	if baseDSN == "" || migratorDSN == "" {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for CLM outbox bridge PG check")
	}
	// OpenTest applies the repository's development/production refusal to the
	// supplied test DSN before this fixture creates its separately named schema.
	probe, err := store.OpenTest(t.TempDir(), baseDSN)
	if err != nil {
		t.Fatalf("open guarded isolated PG test harness: %v", err)
	}
	if err := probe.Close(); err != nil {
		t.Fatalf("close guarded PG test harness: %v", err)
	}
	ctx := context.Background()
	schema := "it_clmbridge_" + randomSuffix(t)
	roleNames := map[string]string{}
	roleDSNs := map[string]string{}
	t.Cleanup(func() {
		pool := openAdminPool(t, baseDSN, "public")
		defer pool.Close()
		if _, err := pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Errorf("drop isolated bridge schema: %v", err)
		}
		for _, name := range roleNames {
			if _, err := pool.Exec(context.Background(), "DROP ROLE IF EXISTS "+pgx.Identifier{name}.Sanitize()); err != nil {
				t.Errorf("drop isolated service role: %v", err)
			}
		}
	})

	t.Setenv("APP_ENV", config.ModeTest)
	t.Setenv("TEST_DATABASE_URL", baseDSN)
	t.Setenv("MIGRATOR_DATABASE_URL", migratorDSN)
	t.Setenv("DATABASE_SCHEMA", schema)
	for _, service := range []string{"core", "document", "execution", "model", "tool"} {
		role, password := "it_"+service+"_"+randomSuffix(t), randomSuffix(t)+randomSuffix(t)
		dsn, err := dsnWithUserPassword(baseDSN, role, password)
		if err != nil {
			t.Fatalf("build %s role DSN: %v", service, err)
		}
		roleNames[service] = role
		roleDSNs[service] = dsn
		t.Setenv(strings.ToUpper(service)+"_DATABASE_URL", dsn)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate isolated bridge schema: %v", err)
	}
	if err := store.MigrateServiceRoles(ctx); err != nil {
		t.Fatalf("provision isolated service roles: %v", err)
	}

	selection, err := config.Select(config.Environ{AppEnv: config.ModeTest, TestDatabaseURL: roleDSNs["core"]})
	if err != nil {
		t.Fatalf("select Core role database: %v", err)
	}
	core, err := store.OpenApplicationSelection(t.TempDir(), selection)
	if err != nil {
		t.Fatalf("open Core Store: %v", err)
	}
	t.Cleanup(func() {
		if err := core.Close(); err != nil {
			t.Errorf("close Core Store: %v", err)
		}
	})
	if err := core.UseServiceOwner("core"); err != nil {
		t.Fatal(err)
	}
	if err := core.ValidateServiceDatabaseRole(ctx, "core"); err != nil {
		t.Fatalf("validate Core database role: %v", err)
	}
	assertOrdinaryRole(t, roleDSNs["core"], schema)

	rawKey := make([]byte, 32)
	for i := range rawKey {
		rawKey[i] = byte(i + 11)
	}
	t.Setenv("CLM_DATA_KEY", base64.StdEncoding.EncodeToString(rawKey))
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo-admin fixture identity is unavailable")
	}
	caseID, err := core.CreateCase(admin, "CLM outbox relay fixture", "procurement_ict")
	if err != nil {
		t.Fatalf("create fixture case through Core: %v", err)
	}
	draftContent := store.DraftContent{
		Contract: &store.ContractBasics{Name: "CLM relay bridge fixture", Currency: "CNY", Amount: "100000.00", SettlementMode: "fixed_total", PricesIncludeTax: boolPointer(true)},
		Supplier: &store.Supplier{Name: "示例供应商有限公司", BankAccount: "6222020200001234567", PayeeName: "示例供应商有限公司"},
		Items:    []store.DraftItem{{ItemID: "c6052c81-8b05-49ce-89e9-f6e9c6403c10", Name: "交换机", Quantity: "10.000000", Unit: "台", UnitPrice: "10000.0000", LineAmount: "100000.00"}},
		Funding:  []store.DraftFunding{{Nature: "self", Channel: "unit", Amount: "100000.00"}},
		Payments: []store.DraftPaymentPlan{{Sequence: 1, Amount: "100000.00", AdvanceAmount: "0.00", UnitAmount: "100000.00", FiscalAmount: "0.00"}},
	}
	draft, err := core.CreateContractDraft(ctx, admin, store.CreateContractDraftInput{
		ExpectedRowVersion: 0, AuthorityMode: "native",
		Draft:          draftContent,
		IdempotencyKey: "clm-relay-bridge-create-0001",
	})
	if err != nil {
		t.Fatalf("create fixture contract through Core: %v", err)
	}
	frozen, err := core.FreezeContractVersion(ctx, admin, store.FreezeContractVersionInput{
		ContractID: draft.Summary.ContractID, CaseID: caseID,
		ExpectedRowVersion: draft.Summary.RowVersion, IdempotencyKey: "clm-relay-bridge-freeze-0001",
	})
	if err != nil {
		t.Fatalf("freeze fixture contract through Core: %v", err)
	}
	if frozen.Version.ContractRevisionID == "" {
		t.Fatal("Core freeze did not produce a revision")
	}

	adminPool := openAdminPool(t, baseDSN, schema)
	defer adminPool.Close()
	before := businessCounts(t, ctx, adminPool, admin.TenantID, draft.Summary.ContractID)
	if before.events != 1 || before.outbox != 1 {
		t.Fatalf("expected one committed Core event and delivery before relay: %+v", before)
	}

	var refreshes atomic.Int64
	refresher := countingRefresher{store: core, calls: &refreshes}
	wake := make(chan struct{}, 1)
	wakeBroker := &bridgeBroker{wake: wake}
	stopWakeConsumer := startBridgeConsumer(t, refresher, time.Hour, wake)
	waitForCount(t, &refreshes, 1) // immediate initial refresh
	assertRefreshCount(t, &refreshes, 1)

	failingAck := &ackLossStore{Store: core}
	relay, err := New(failingAck, wakeBroker, outboxrelay.Config{
		Stream: DefaultStream, Subject: DefaultSubject, Replicas: 1,
		Batch: 4, Lease: time.Second, Poll: 10 * time.Millisecond,
		PublishTimeout: 200 * time.Millisecond, MaxBackoff: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create relay: %v", err)
	}
	if delivered, err := relay.Step(ctx); err == nil || delivered != 0 {
		t.Fatalf("first publish must expose simulated lost ACK: delivered=%d err=%v", delivered, err)
	}
	waitForCount(t, &refreshes, 2) // this increment can only come from publish wake
	assertRefreshCount(t, &refreshes, 2)
	if afterLostAck := businessCounts(t, ctx, adminPool, admin.TenantID, draft.Summary.ContractID); afterLostAck != before {
		t.Fatalf("first publish or lost ACK changed business facts: before=%+v after=%+v", before, afterLostAck)
	}
	time.Sleep(1100 * time.Millisecond) // allow the committed lease to expire before redelivery.
	if delivered, err := relay.Step(ctx); err != nil || delivered != 1 {
		t.Fatalf("retry must publish and acknowledge exactly one row: delivered=%d err=%v", delivered, err)
	}
	waitForCount(t, &refreshes, 3) // no periodic tick can occur during the 1.1s lease wait
	assertRefreshCount(t, &refreshes, 3)
	if ids := wakeBroker.messageIDs(); len(ids) != 2 || ids[0] == "" || ids[0] != ids[1] {
		t.Fatalf("redelivery did not retain stable broker MessageID: %q", ids)
	}
	if afterAck := businessCounts(t, ctx, adminPool, admin.TenantID, draft.Summary.ContractID); afterAck != before {
		t.Fatalf("duplicate consumer refresh changed business facts: before=%+v after=%+v", before, afterAck)
	}
	acked := outboxAcked(t, ctx, adminPool, admin.TenantID, draft.Summary.ContractID)
	if acked != 1 {
		t.Fatalf("normal retry ACK did not mark delivery: acked=%d", acked)
	}

	stopWakeConsumer()
	// A separate consumer has no wake channel. Its first refresh is immediate;
	// the next must come from the periodic PostgreSQL recovery timer alone.
	periodicStart := refreshes.Load()
	startBridgeConsumer(t, refresher, 3*time.Second, nil)
	waitForCount(t, &refreshes, periodicStart+1)
	assertRefreshCount(t, &refreshes, periodicStart+1)
	firstPeriodicSnapshot := core.CLMMetricsSnapshot()
	waitForCountWithin(t, &refreshes, periodicStart+2, 8*time.Second)
	assertRefreshCount(t, &refreshes, periodicStart+2)
	secondPeriodicSnapshot := core.CLMMetricsSnapshot()
	if !secondPeriodicSnapshot.CapturedAt.After(firstPeriodicSnapshot.CapturedAt) {
		t.Fatalf("periodic consumer did not replace the PG metrics snapshot: first=%s second=%s", firstPeriodicSnapshot.CapturedAt, secondPeriodicSnapshot.CapturedAt)
	}
	if afterPeriodic := businessCounts(t, ctx, adminPool, admin.TenantID, draft.Summary.ContractID); afterPeriodic != before {
		t.Fatalf("periodic refresh changed business facts: before=%+v after=%+v", before, afterPeriodic)
	}
}

func startBridgeConsumer(t *testing.T, refresher MetricsRefresher, interval time.Duration, wakeups <-chan struct{}) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- consumer.Run(ctx, refresher, nil, interval, wakeups) }()
	var once sync.Once
	var result error
	stop := func() {
		once.Do(func() {
			cancel()
			result = <-done
			if !errors.Is(result, context.Canceled) {
				t.Errorf("consumer shutdown: %v", result)
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

type ackLossStore struct {
	Store
	failed atomic.Bool
}

func (s *ackLossStore) AckCLMOutbox(ctx context.Context, claim store.CLMOutboxClaim) error {
	if s.failed.CompareAndSwap(false, true) {
		return errors.New("simulated lost ACK response")
	}
	return s.Store.AckCLMOutbox(ctx, claim)
}

type bridgeBroker struct {
	mu   sync.Mutex
	ids  []string
	wake chan<- struct{}
}

func (b *bridgeBroker) Publish(_ context.Context, _ string, messageID string, _ []byte) error {
	b.mu.Lock()
	b.ids = append(b.ids, messageID)
	b.mu.Unlock()
	select {
	case b.wake <- struct{}{}:
	default:
	}
	return nil
}

func (*bridgeBroker) Close() error { return nil }

func (b *bridgeBroker) messageIDs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.ids...)
}

type countingRefresher struct {
	store *store.Store
	calls *atomic.Int64
}

func (r countingRefresher) RefreshCLMMetrics(ctx context.Context, threshold *time.Duration) error {
	err := r.store.RefreshCLMMetrics(ctx, threshold)
	if err == nil {
		r.calls.Add(1)
	}
	return err
}

func waitForCount(t *testing.T, count *atomic.Int64, target int64) {
	waitForCountWithin(t, count, target, 3*time.Second)
}

func waitForCountWithin(t *testing.T, count *atomic.Int64, target int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if count.Load() >= target {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("metrics refresher count=%d, want at least %d", count.Load(), target)
}

func assertRefreshCount(t *testing.T, count *atomic.Int64, want int64) {
	t.Helper()
	if got := count.Load(); got != want {
		t.Fatalf("metrics refresher count=%d, want exactly %d", got, want)
	}
}

type clmBusinessCounts struct{ contracts, revisions, events, outbox, rounds, decisions int64 }

func businessCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, contractID string) clmBusinessCounts {
	t.Helper()
	var got clmBusinessCounts
	err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM clm_contracts WHERE tenant_id=$1 AND contract_id=$2),
		(SELECT count(*) FROM clm_contract_revisions WHERE tenant_id=$1 AND contract_id=$2),
		(SELECT count(*) FROM clm_business_events WHERE tenant_id=$1 AND contract_id=$2),
		(SELECT count(*) FROM clm_outbox_delivery d JOIN clm_business_events e ON e.tenant_id=d.tenant_id AND e.event_id=d.event_id WHERE e.tenant_id=$1 AND e.contract_id=$2),
		(SELECT count(*) FROM clm_approval_rounds WHERE tenant_id=$1 AND contract_id=$2),
		(SELECT count(*) FROM clm_approval_decisions WHERE tenant_id=$1 AND contract_id=$2)`, tenantID, contractID).
		Scan(&got.contracts, &got.revisions, &got.events, &got.outbox, &got.rounds, &got.decisions)
	if err != nil {
		t.Fatalf("read admin business-fact counts: %v", err)
	}
	return got
}

func outboxAcked(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, contractID string) int64 {
	t.Helper()
	var count int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM clm_outbox_delivery d JOIN clm_business_events e
		ON e.tenant_id=d.tenant_id AND e.event_id=d.event_id
		WHERE e.tenant_id=$1 AND e.contract_id=$2 AND d.broker_acked_at IS NOT NULL`, tenantID, contractID).Scan(&count); err != nil {
		t.Fatalf("read outbox ACK state: %v", err)
	}
	return count
}

func assertOrdinaryRole(t *testing.T, dsn, schema string) {
	t.Helper()
	pool := openAdminPool(t, dsn, schema)
	defer pool.Close()
	var user string
	var superuser, bypass bool
	if err := pool.QueryRow(context.Background(), `SELECT current_user,rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&user, &superuser, &bypass); err != nil {
		t.Fatalf("inspect Core login role: %v", err)
	}
	if user == "" || superuser || bypass {
		t.Fatalf("Core login is not an ordinary restricted role: user=%q superuser=%v bypassrls=%v", user, superuser, bypass)
	}
}

func openAdminPool(t *testing.T, dsn, schema string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse PG test DSN: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open PG test connection: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("ping PG test connection: %v", err)
	}
	return pool
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", raw[:])
}

func dsnWithUserPassword(raw, username, password string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	parsed.User = url.UserPassword(username, password)
	return parsed.String(), nil
}

func boolPointer(value bool) *bool { return &value }
