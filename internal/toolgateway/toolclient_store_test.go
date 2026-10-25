package toolgateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
	"contractaudit/internal/toolclient"
)

const toolGatewayTestCredential = "tool-gateway-test-credential-with-more-than-32-bytes"

type gatewayConsumerModels struct{}

func (gatewayConsumerModels) Name() string            { return "gateway-consumer-test-model" }
func (gatewayConsumerModels) ConfigurationID() string { return "gateway-consumer-test-model" }
func (gatewayConsumerModels) Warranty(context.Context, []model.Material) (model.Warranty, error) {
	return model.Warranty{Claim: "Fixture proposal", Relation: "compatible", Disposition: "false_positive"}, nil
}
func (gatewayConsumerModels) Transcribe(context.Context, string, []byte) (string, error) {
	return "", model.ErrUnusable
}

type gatewayToolClientReader struct {
	client    *toolclient.Client
	responses []json.RawMessage
	errors    []error
}

func (r *gatewayToolClientReader) FrozenEvidence(ctx context.Context, in store.ToolEvidenceRequest) (json.RawMessage, error) {
	result, err := r.client.FrozenEvidence(ctx, toolclient.Request{
		Claim: toolclient.Claim{
			TenantID: in.TenantID, OrgID: in.OrgID, JobID: in.JobID, WorkerID: in.WorkerID,
			Generation: in.Generation, AttemptID: in.AttemptID,
		},
		CaseID: in.CaseID, RunID: in.RunID, AttemptID: in.AttemptID,
		CapabilityID: in.CapabilityID, Deadline: in.Deadline,
	})
	r.responses = append(r.responses, result)
	r.errors = append(r.errors, err)
	return result, err
}

type gatewayClaimFixture struct {
	caseID     string
	generation store.GenerationRecord
	acceptance store.JobAcceptance
	claim      store.JobClaim
}

func createGatewayClaimFixture(t *testing.T, st *store.Store, user accounts.User, workerID string) gatewayClaimFixture {
	t.Helper()
	caseID, err := st.CreateCaseWithBusiness(user, "MCP冻结证据消费者测试", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatalf("create telecom case: %v", err)
	}
	generation, err := st.Generate(context.Background(), user, caseID, "ent_telecom_dedicated_line_v1", map[string]string{
		"circuit_id": "LINE-001", "bandwidth_mbps": "100", "sla_percent": "99.9",
		"sla_period": "calendar_month", "sla_exclusions": "none", "amount_cny": "100000",
		"tax_basis": "inclusive", "prepayment_percent": "20", "customer": "测试客户", "supplier": "测试供应商",
	})
	if err != nil {
		t.Fatalf("generate telecom draft: %v", err)
	}
	if _, err := st.AddDocument(context.Background(), user, caseID, "procurement", "purchase.txt", "text/plain", []byte("采购文件未提供合同阈值或审批依据。")); err != nil {
		t.Fatalf("add procurement material: %v", err)
	}
	accepted, err := st.StartReviewAsync(context.Background(), user, caseID)
	if err != nil {
		t.Fatalf("accept review: %v", err)
	}
	claim, err := st.ClaimNextJob(context.Background(), workerID, time.Minute)
	if err != nil {
		t.Fatalf("claim review: %v", err)
	}
	if claim.JobID != accepted.JobID {
		t.Fatalf("claimed job %q, want %q", claim.JobID, accepted.JobID)
	}
	return gatewayClaimFixture{caseID: caseID, generation: generation, acceptance: accepted, claim: claim}
}

func callWithGatewayClaim(client *toolclient.Client, claim store.JobClaim) (json.RawMessage, error) {
	return client.FrozenEvidence(context.Background(), toolclient.Request{
		Claim: toolclient.Claim{
			TenantID: claim.TenantID, OrgID: claim.OrgID, JobID: claim.JobID, WorkerID: claim.WorkerID,
			Generation: claim.Generation, AttemptID: claim.AttemptID,
		},
		CaseID: claim.CaseID, RunID: claim.RunID, AttemptID: claim.AttemptID,
		CapabilityID: "dedicated_line.minimum_bandwidth", Deadline: time.Now().UTC().Add(30 * time.Second),
	})
}

func requireGatewayClaimRefusal(t *testing.T, client *toolclient.Client, claim store.JobClaim) {
	t.Helper()
	_, err := callWithGatewayClaim(client, claim)
	if err == nil || !strings.Contains(err.Error(), "HTTP 409") {
		t.Fatalf("inactive worker claim error = %v, want bounded HTTP 409 refusal", err)
	}
	if len(err.Error()) > 128 || strings.Contains(err.Error(), claim.JobID) || strings.Contains(err.Error(), claim.CaseID) {
		t.Fatalf("worker claim refusal exposed scope or an unbounded error: %q", err.Error())
	}
}

func TestToolClientReadsFrozenEvidenceThroughLiveGatewayClaim(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TEST_DATABASE_URL")) == "" {
		t.Skip("TEST_DATABASE_URL is required for isolated PostgreSQL tool gateway checks")
	}
	st, err := store.OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatalf("open isolated tool gateway schema: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close isolated tool gateway schema: %v", err)
		}
	})
	user, found := accounts.ByID("demo-admin")
	if !found {
		t.Fatal("demo admin is unavailable")
	}
	st.UseModels(gatewayConsumerModels{})

	server := httptest.NewServer(NewHandler(st, Options{WorkerCredential: toolGatewayTestCredential}))
	defer server.Close()
	client, err := toolclient.New(server.URL, toolGatewayTestCredential, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	reader := &gatewayToolClientReader{client: client}
	if err := st.UseToolEvidenceReader(reader); err != nil {
		t.Fatal(err)
	}

	live := createGatewayClaimFixture(t, st, user, "tool-gateway-live-worker")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := st.RunClaimedJob(ctx, live.claim); err != nil {
		t.Fatalf("run worker through real tool client and gateway: %v", err)
	}
	if len(reader.responses) != 3 || len(reader.errors) != 3 {
		t.Fatalf("worker received %d tool responses and %d errors, want three capability reads", len(reader.responses), len(reader.errors))
	}
	for index, callErr := range reader.errors {
		if callErr != nil {
			t.Fatalf("MCP capability call %d failed: %v", index, callErr)
		}
	}

	type citation struct {
		DocumentID       string `json:"document_id"`
		Filename         string `json:"filename"`
		OriginalDigest   string `json:"original_digest"`
		ParsedDocumentID string `json:"parsed_document_id"`
		Quote            string `json:"quote"`
		Start            int    `json:"start"`
		End              int    `json:"end"`
	}
	type fact struct {
		Value     string     `json:"value"`
		Unit      string     `json:"unit"`
		Citations []citation `json:"citations"`
	}
	var bandwidthFound bool
	var bandwidthCitation citation
	for _, raw := range reader.responses {
		var evidence struct {
			SchemaVersion string `json:"schema_version"`
			ToolVersion   string `json:"tool_version"`
			CaseID        string `json:"case_id"`
			RunID         string `json:"run_id"`
			AttemptID     string `json:"attempt_id"`
			CapabilityID  string `json:"capability_id"`
			Status        string `json:"status"`
			Facts         []fact `json:"facts"`
		}
		if err := json.Unmarshal(raw, &evidence); err != nil {
			t.Fatalf("decode actual MCP evidence: %v", err)
		}
		if evidence.SchemaVersion != "frozen-evidence/v1" || evidence.ToolVersion != "frozen-evidence/v1" ||
			evidence.CaseID != live.caseID || evidence.RunID != live.acceptance.RunID || evidence.AttemptID != live.acceptance.AttemptID {
			t.Fatalf("MCP evidence was not bound to the frozen attempt: %+v", evidence)
		}
		if evidence.CapabilityID != "dedicated_line.minimum_bandwidth" {
			continue
		}
		if evidence.Status != "found" || len(evidence.Facts) != 1 {
			t.Fatalf("bandwidth evidence = %+v, want one cited frozen fact", evidence)
		}
		cite := evidence.Facts[0].Citations
		if evidence.Facts[0].Value != "100" || evidence.Facts[0].Unit != "Mbit/s" || len(cite) != 1 ||
			cite[0].DocumentID != live.generation.DocumentID {
			t.Fatalf("bandwidth fact lost its frozen document citation: %+v", evidence.Facts[0])
		}
		bandwidthCitation = cite[0]
		bandwidthFound = true
	}
	if !bandwidthFound {
		t.Fatal("the active claim did not return the cited bandwidth evidence")
	}

	workspace, err := st.WorkspaceAttempt(user, live.caseID, live.acceptance.RunID, live.acceptance.AttemptID)
	if err != nil {
		t.Fatalf("read completed worker attempt: %v", err)
	}
	matchedSource := false
	for _, document := range workspace.Documents {
		if document.Document.ID != bandwidthCitation.DocumentID {
			continue
		}
		runes := []rune(document.Text)
		if document.ParsedDocument == nil || document.ParsedDocument.ID != bandwidthCitation.ParsedDocumentID ||
			document.Document.Digest != bandwidthCitation.OriginalDigest || document.Document.Filename != bandwidthCitation.Filename ||
			bandwidthCitation.Start < 0 || bandwidthCitation.End <= bandwidthCitation.Start || bandwidthCitation.End > len(runes) ||
			string(runes[bandwidthCitation.Start:bandwidthCitation.End]) != bandwidthCitation.Quote {
			t.Fatalf("citation does not match its frozen source: %+v", bandwidthCitation)
		}
		matchedSource = true
	}
	if !matchedSource {
		t.Fatal("citation source is absent from the frozen attempt")
	}
	acceptedCapabilities := make(map[string]bool)
	if workspace.Execution != nil {
		for _, event := range workspace.Execution.Events {
			if event.NodeKind == "tool_call" && event.Phase == "evidence_tool" && event.EventType == "tool_evidence" && event.Status == "accepted" && event.ModelRef != nil {
				acceptedCapabilities[*event.ModelRef] = true
			}
		}
	}
	if len(acceptedCapabilities) != 3 {
		t.Fatalf("worker did not admit all returned capability facts: %+v", acceptedCapabilities)
	}

	stale := live.claim
	requireGatewayClaimRefusal(t, client, stale)

	foreign := createGatewayClaimFixture(t, st, user, "tool-gateway-foreign-worker")
	foreignClaim := foreign.claim
	foreignClaim.TenantID = "another-tenant"
	foreignClaim.OrgID = "another-org"
	requireGatewayClaimRefusal(t, client, foreignClaim)
	if err := st.CancelReview(context.Background(), user, foreign.caseID, foreign.acceptance.RunID); err != nil {
		t.Fatalf("cancel foreign-hint test claim: %v", err)
	}

	cancelled := createGatewayClaimFixture(t, st, user, "tool-gateway-cancelled-worker")
	if err := st.CancelReview(context.Background(), user, cancelled.caseID, cancelled.acceptance.RunID); err != nil {
		t.Fatalf("cancel claimed review: %v", err)
	}
	requireGatewayClaimRefusal(t, client, cancelled.claim)
}
