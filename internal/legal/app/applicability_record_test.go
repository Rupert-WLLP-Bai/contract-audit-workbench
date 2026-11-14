package app

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/legal/model"
)

type recordTestPorts struct {
	RecordRepository
	calls   []string
	binding RecordPartyBinding
	clock   time.Time
}

func (p *recordTestPorts) VerifyBasis(context.Context, json.RawMessage) (json.RawMessage, *time.Time, error) {
	p.calls = append(p.calls, "basis")
	return json.RawMessage("[]"), nil, nil
}
func (p *recordTestPorts) Now() time.Time { p.calls = append(p.calls, "now"); return p.clock }
func (p *recordTestPorts) NewFactID() (string, error) {
	p.calls = append(p.calls, "id")
	return "fact", nil
}
func (p *recordTestPorts) LoadRecordBinding(context.Context, model.LegalApplicabilityPartyPin) (RecordPartyBinding, error) {
	p.calls = append(p.calls, "binding")
	return p.binding, nil
}
func (p *recordTestPorts) CountRecordRole(context.Context, string) (int, error) {
	p.calls = append(p.calls, "count")
	return 1, nil
}
func (p *recordTestPorts) LockHead(context.Context, RecordIdentity, string) (string, int64, error) {
	p.calls = append(p.calls, "lock")
	return "", 0, nil
}
func (p *recordTestPorts) InsertHead(context.Context, RecordIdentity, string, string, time.Time) error {
	p.calls = append(p.calls, "head")
	return nil
}
func (p *recordTestPorts) AppendVersion(context.Context, model.LegalApplicabilityFactVersion) error {
	p.calls = append(p.calls, "append")
	return nil
}
func TestRecordNewFactBasisClockAndInsertOrder(t *testing.T) {
	p := &recordTestPorts{clock: time.Date(2026, 1, 1, 0, 0, 0, 1234, time.UTC)}
	input := model.LegalApplicabilityFactInput{Dimension: "funding_kind", Status: "unknown", UnknownReason: "insufficient_evidence", TemporalBasis: "current_state", BasisNote: "依据不足", IdempotencyKey: "key"}
	fact, err := RecordNewFact(context.Background(), p, p, p, p, RecordIdentity{TenantID: "tenant", OrgID: "org", CaseID: "case", ActorID: "actor"}, input, "digest")
	if err != nil || !reflect.DeepEqual(p.calls, []string{"basis", "now", "lock", "id", "head", "append"}) || fact.Version != 1 || fact.CreatedAt.Nanosecond() != 1000 {
		t.Fatalf("fact=%+v calls=%v err=%v", fact, p.calls, err)
	}
}
func TestRecordSubjectStaleAndRoleKeepDistinctFaults(t *testing.T) {
	pin := model.LegalApplicabilityPartyPin{Side: "buyer", BindingID: "b", BindingVersion: 1, SubjectID: "s", SubjectVersion: 1, ScopeNote: "buyer"}
	input := model.LegalApplicabilityFactInput{Dimension: "buyer_kind", Status: "unknown", UnknownReason: "insufficient_evidence", TemporalBasis: "current_state", BasisNote: "依据不足", Party: pin}
	for _, tc := range []struct {
		name    string
		binding RecordPartyBinding
		status  int
	}{
		{"stale", RecordPartyBinding{Found: true, HeadVersion: 2, SubjectID: "s", SubjectVersion: 1, SubjectHead: 1, Role: "supplier", State: "active"}, 409},
		{"role", RecordPartyBinding{Found: true, HeadVersion: 1, SubjectID: "s", SubjectVersion: 1, SubjectHead: 1, Role: "supplier", State: "active"}, 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &recordTestPorts{clock: time.Now(), binding: tc.binding}
			_, err := RecordNewFact(context.Background(), p, p, p, p, RecordIdentity{}, input, "digest")
			problem, ok := err.(*fault.Error)
			if !ok || problem.Status != tc.status || !reflect.DeepEqual(p.calls, []string{"basis", "now", "binding"}) {
				t.Fatalf("calls=%v err=%v", p.calls, err)
			}
		})
	}
}

type replayRecordRepo struct {
	RecordRepository
	calls []string
	fact  model.LegalApplicabilityFactVersion
}

func (r *replayRecordRepo) FindVersionByActorKey(context.Context, RecordIdentity, string, string) (int64, bool, error) {
	r.calls = append(r.calls, "lookup")
	return 1, true, nil
}
func (r *replayRecordRepo) ReadVersion(context.Context, RecordIdentity, string, int64) (model.LegalApplicabilityFactVersion, error) {
	r.calls = append(r.calls, "version")
	return r.fact, nil
}
func TestRecordReplayValidatesBasisBeforeDigestAndDoesNotResolveDependencies(t *testing.T) {
	input := model.LegalApplicabilityFactInput{Dimension: "transaction_kind", Status: "unknown", UnknownReason: "insufficient_evidence", TemporalBasis: "current_state", BasisNote: "依据不足", BasisRefs: json.RawMessage("[]"), IdempotencyKey: "key"}
	fact, err := model.NormalizeLegalApplicabilityInput(input, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), json.RawMessage("[]"), nil)
	if err != nil {
		t.Fatal(err)
	}
	fact.RequestDigest = "different"
	bad := fact
	bad.BasisSHA256 = "corrupt"
	repo := &replayRecordRepo{fact: bad}
	_, _, found, err := FindRecordReplay(context.Background(), repo, RecordIdentity{}, input)
	if found || err == nil || err.Error() != "法律适用事实材料依据摘要不匹配" || !reflect.DeepEqual(repo.calls, []string{"lookup", "version"}) {
		t.Fatalf("found=%v calls=%v err=%v", found, repo.calls, err)
	}
	repo.fact = fact
	repo.calls = nil
	_, _, found, err = FindRecordReplay(context.Background(), repo, RecordIdentity{}, input)
	if found || err == nil || err.Error() != "同一幂等键不能用于不同法律适用事实" || !reflect.DeepEqual(repo.calls, []string{"lookup", "version"}) {
		t.Fatalf("found=%v calls=%v err=%v", found, repo.calls, err)
	}
	digest, err := model.LegalApplicabilityRequestDigest("", "", "", "", input.IdempotencyKey, input)
	if err != nil {
		t.Fatal(err)
	}
	fact.RequestDigest = digest
	repo.fact, repo.calls = fact, nil
	got, _, found, err := FindRecordReplay(context.Background(), repo, RecordIdentity{}, input)
	if err != nil || !found || got.RequestDigest != digest || !reflect.DeepEqual(repo.calls, []string{"lookup", "version"}) {
		t.Fatalf("replay=%+v found=%v calls=%v err=%v", got, found, repo.calls, err)
	}
}
