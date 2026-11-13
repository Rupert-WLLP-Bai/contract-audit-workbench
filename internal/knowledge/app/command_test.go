package app

import (
	"context"
	"encoding/json"
	"testing"

	"contractaudit/internal/knowledge/model"
)

type commandTestRepo struct {
	CommandRepository
	saved    string
	response []byte
	found    bool
	loads    int
	raw      RawVersion
}

func (r *commandTestRepo) IdempotencyReceipt(context.Context, CreateDraftIdentity, string) (string, []byte, bool, error) {
	return r.saved, r.response, r.found, nil
}
func (r *commandTestRepo) LoadVersion(context.Context, string, string, string) (RawVersion, error) {
	r.loads++
	return r.raw, nil
}

type commandTestFacts struct {
	CommandFacts
	adminChecks, sourceChecks int
}

func (f *commandTestFacts) RecheckAdmin(context.Context) error { f.adminChecks++; return nil }
func (f *commandTestFacts) AuthorizeSource(context.Context, model.GovernedKnowledgeContent) error {
	f.sourceChecks++
	return nil
}
func TestCommandReplayPrecedesSyntaxAndRechecksAuthorization(t *testing.T) {
	input := model.GovernedKnowledgeCommandInput{IdempotencyKey: "key", Action: "previous"}
	raw, _ := json.Marshal(input)
	response, _ := json.Marshal(model.GovernedKnowledgeVersion{Status: "draft"})
	repo := &commandTestRepo{found: true, saved: model.SHA256(raw), response: response}
	facts := &commandTestFacts{}
	out, err := Command(context.Background(), repo, facts, CreateDraftIdentity{}, input)
	if err != nil || out.Status != "draft" || repo.loads != 0 || facts.adminChecks != 1 || facts.sourceChecks != 1 {
		t.Fatalf("out=%+v err=%v repo=%+v facts=%+v", out, err, repo, facts)
	}
}
func TestCommandMissRejectsSyntaxBeforeVersionRead(t *testing.T) {
	repo := &commandTestRepo{}
	facts := &commandTestFacts{}
	_, err := Command(context.Background(), repo, facts, CreateDraftIdentity{}, model.GovernedKnowledgeCommandInput{IdempotencyKey: "key", Action: "invalid"})
	if err == nil || repo.loads != 0 {
		t.Fatalf("err=%v loads=%d", err, repo.loads)
	}
}
func TestExactVersionRejectsDigestBeforeLegalFacts(t *testing.T) {
	repo := &commandTestRepo{raw: RawVersion{Version: model.GovernedKnowledgeVersion{Status: "draft", Ref: model.KnowledgeRef{ContentSHA256: "wrong"}}, ContentBytes: []byte(`{"kind":"legal"}`)}}
	_, err := LoadExactVersion(context.Background(), repo, &commandTestFacts{}, "tenant", "id", "version")
	if err == nil {
		t.Fatal("corrupt content accepted")
	}
}
