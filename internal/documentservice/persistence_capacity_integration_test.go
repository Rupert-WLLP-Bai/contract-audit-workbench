package documentservice_test

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/model"
	"contractaudit/internal/store"
	"github.com/jackc/pgx/v5"
)

const lockedGenerationRequest = `{"template_id":"ent_telecom_dedicated_line_v1","fields":{"circuit_id":"LINE-LOCK","bandwidth_mbps":"100","sla_percent":"99.9","sla_period":"calendar_month","sla_exclusions":"none","amount_cny":"100000","tax_basis":"inclusive","prepayment_percent":"20","customer":"甲方","supplier":"乙方"}}`

func TestUploadDeadlineReleasesParserCapacityBeforeCaseUnlock(t *testing.T) {
	testPersistenceDeadlineAtCaseLock(t, "upload")
}

func TestGenerationDeadlineReleasesParserCapacityBeforeCaseUnlock(t *testing.T) {
	testPersistenceDeadlineAtCaseLock(t, "generation")
}

func TestReparseDeadlineReleasesParserCapacityBeforeCaseUnlock(t *testing.T) {
	testPersistenceDeadlineAtCaseLock(t, "reparse")
}

func testPersistenceDeadlineAtCaseLock(t *testing.T, operation string) {
	t.Helper()
	f := newServiceFixture(t, 1, 2*time.Second)
	t.Cleanup(f.model.releaseAll)
	caseID, imageID := f.newCaseWithDocuments(t)
	followupCaseID, err := f.core.CreateCaseWithBusiness(f.admin, "持锁期间容量回归", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatalf("create follow-up case: %v", err)
	}
	followupImageID, imageResult := f.upload(t, followupCaseID, "procurement", "follow-up.png", "text/plain", model.SampleWarrantyPNG)
	if imageResult.err != nil || imageResult.status != http.StatusCreated || followupImageID == "" {
		t.Fatalf("upload follow-up image: status=%d body=%s err=%v", imageResult.status, imageResult.body, imageResult.err)
	}
	before, err := f.core.GetCase(f.admin, caseID)
	if err != nil {
		t.Fatalf("read case before locked mutation: %v", err)
	}
	lock := holdCaseAdvisoryLock(t, f, caseID)
	t.Cleanup(func() { lock.close(t) })

	result := startLockedMutation(t, f, operation, caseID, imageID)
	if operation == "reparse" {
		if call := waitParseCall(t, f.model.entered); call != 1 {
			t.Fatalf("reparse OCR call number=%d, want 1", call)
		}
		f.model.release <- struct{}{}
	}
	lock.waitForWaiter(t)
	response := waitHTTP(t, result)
	if response.status != http.StatusGatewayTimeout {
		t.Fatalf("locked %s status=%d body=%s, want 504", operation, response.status, response.body)
	}

	if operation == "reparse" {
		if active := f.model.active.Load(); active != 0 {
			t.Fatalf("timed-out parser remained active while the case lock was held: %d", active)
		}
		followup := f.reparseAsync(followupCaseID, followupImageID)
		select {
		case call := <-f.model.entered:
			if call != 2 {
				t.Fatalf("follow-up OCR call number=%d, want 2", call)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("follow-up OCR did not acquire the released parser capacity")
		}
		f.model.release <- struct{}{}
		if response := waitHTTP(t, followup); response.status != http.StatusOK {
			t.Fatalf("follow-up reparse failed while the original case lock remained held: status=%d body=%s", response.status, response.body)
		}
	} else {
		followupID, followup := f.upload(t, followupCaseID, "attachment", "follow-up.txt", "text/plain", []byte("后续上传应正常完成。"))
		if followup.err != nil || followup.status != http.StatusCreated || followupID == "" {
			t.Fatalf("parser capacity was not released while the original case lock remained held: status=%d body=%s id=%q err=%v", followup.status, followup.body, followupID, followup.err)
		}
	}
	lock.release(t)

	after, err := f.core.GetCase(f.admin, caseID)
	if err != nil {
		t.Fatalf("read case after locked mutation: %v", err)
	}
	if len(after.Documents) != len(before.Documents) {
		t.Fatalf("timed-out %s changed the case document count after unlocking: before=%d after=%d", operation, len(before.Documents), len(after.Documents))
	}
	switch operation {
	case "generation":
		if len(after.Generations) != len(before.Generations) {
			t.Fatalf("timed-out generation was persisted after unlocking: before=%d after=%d", len(before.Generations), len(after.Generations))
		}
	case "reparse":
		if parsedDocumentID(after, imageID) != parsedDocumentID(before, imageID) {
			t.Fatalf("timed-out reparse added a parsed artifact after unlocking: before=%q after=%q", parsedDocumentID(before, imageID), parsedDocumentID(after, imageID))
		}
	}
}

func startLockedMutation(t *testing.T, f *serviceFixture, operation, caseID, imageID string) <-chan httpResult {
	t.Helper()
	result := make(chan httpResult, 1)
	var method, path, contentType string
	var body []byte
	switch operation {
	case "upload":
		contentType, body = multipartUploadPayload(t, "attachment", "locked-upload.txt", "text/plain", []byte("此上传必须在超时后保持未登记。"))
		method, path = http.MethodPost, "/api/cases/"+caseID+"/documents"
	case "generation":
		method, path = http.MethodPost, "/api/cases/"+caseID+"/generations"
		contentType, body = "application/json", []byte(lockedGenerationRequest)
	case "reparse":
		return f.reparseAsync(caseID, imageID)
	default:
		panic("unknown locked mutation: " + operation)
	}
	go func() { result <- f.do(method, path, contentType, body) }()
	return result
}

func parsedDocumentID(detail store.CaseDetail, documentID string) string {
	for _, interpreted := range detail.Documents {
		if interpreted.Document.ID == documentID && interpreted.ParsedDocument != nil {
			return interpreted.ParsedDocument.ID
		}
	}
	return ""
}

type caseAdvisoryLock struct {
	conn     *pgx.Conn
	key      int64
	released bool
}

func holdCaseAdvisoryLock(t *testing.T, f *serviceFixture, caseID string) *caseAdvisoryLock {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, strings.TrimSpace(os.Getenv("TEST_DATABASE_URL")))
	if err != nil {
		t.Fatalf("connect to test PostgreSQL: %v", err)
	}
	_, err = conn.Exec(ctx, `SELECT
		set_config('app.actor_id',$1,false), set_config('app.tenant_id',$2,false),
		set_config('app.org_id',$3,false), set_config('app.actor_role','api',false)`,
		f.admin.ID, f.admin.TenantID, f.admin.OrgID)
	if err != nil {
		_ = conn.Close(context.Background())
		t.Fatalf("set case lookup scope: %v", err)
	}
	schemas, err := conn.Query(ctx, `SELECT n.nspname FROM pg_namespace n JOIN pg_class c ON c.relnamespace=n.oid
		WHERE left(n.nspname,3)='it_' AND c.relname='cases' AND c.relkind IN ('r','p') ORDER BY n.nspname`)
	if err != nil {
		_ = conn.Close(context.Background())
		t.Fatalf("find test schemas: %v", err)
	}
	var candidates []string
	for schemas.Next() {
		var schema string
		if err := schemas.Scan(&schema); err != nil {
			schemas.Close()
			_ = conn.Close(context.Background())
			t.Fatalf("read test schema: %v", err)
		}
		candidates = append(candidates, schema)
	}
	if err := schemas.Err(); err != nil {
		schemas.Close()
		_ = conn.Close(context.Background())
		t.Fatalf("iterate test schemas: %v", err)
	}
	schemas.Close()
	var selected string
	for _, schema := range candidates {
		table := pgx.Identifier{schema, "cases"}.Sanitize()
		var exists bool
		if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM "+table+" WHERE id=$1)", caseID).Scan(&exists); err == nil && exists {
			selected = schema
			break
		}
	}
	if selected == "" {
		_ = conn.Close(context.Background())
		t.Fatal("could not locate the service fixture schema")
	}
	if _, err := conn.Exec(ctx, "SET search_path TO "+pgx.Identifier{selected}.Sanitize()); err != nil {
		_ = conn.Close(context.Background())
		t.Fatalf("select fixture schema: %v", err)
	}
	lock := &caseAdvisoryLock{conn: conn}
	if err := conn.QueryRow(ctx, `SELECT hashtextextended(current_schema() || ':case:' || $1, 0)`, caseID).Scan(&lock.key); err != nil {
		_ = conn.Close(context.Background())
		t.Fatalf("compute case lock key: %v", err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lock.key); err != nil {
		_ = conn.Close(context.Background())
		t.Fatalf("hold case advisory lock: %v", err)
	}
	return lock
}

func (lock *caseAdvisoryLock) hasWaiter(t *testing.T) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var waiting bool
	err := lock.conn.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND objsubid=1
		AND classid=((($1::bigint >> 32) & 4294967295)::oid)
		AND objid=(($1::bigint & 4294967295)::oid)
	)`, lock.key).Scan(&waiting)
	if err != nil {
		t.Fatalf("inspect case advisory lock waiters: %v", err)
	}
	return waiting
}

func (lock *caseAdvisoryLock) waitForWaiter(t *testing.T) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if lock.hasWaiter(t) {
			return
		}
		select {
		case <-deadline:
			t.Fatal("HTTP mutation did not reach the held PostgreSQL case lock")
		case <-ticker.C:
		}
	}
}

func (lock *caseAdvisoryLock) release(t *testing.T) {
	t.Helper()
	if lock.released {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var released bool
	if err := lock.conn.QueryRow(ctx, `SELECT pg_advisory_unlock($1)`, lock.key).Scan(&released); err != nil {
		t.Fatalf("release case advisory lock: %v", err)
	}
	if !released {
		t.Fatal("case advisory lock was not held by the fixture connection")
	}
	lock.released = true
}

func (lock *caseAdvisoryLock) close(t *testing.T) {
	t.Helper()
	if lock.conn == nil {
		return
	}
	if !lock.released {
		lock.release(t)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := lock.conn.Close(ctx); err != nil {
		t.Errorf("close advisory lock connection: %v", err)
	}
	lock.conn = nil
}
