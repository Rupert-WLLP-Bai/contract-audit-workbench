package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

type mappingBasisTest struct {
	raw json.RawMessage
	err error
}

func (f mappingBasisTest) VerifyBasis(context.Context, json.RawMessage) (json.RawMessage, error) {
	return f.raw, f.err
}
func TestMappingBasisResolverAndExactPins(t *testing.T) {
	association := model.ServiceObjectContractAssociation{MaterialID: "m", MaterialVersion: 2, DocumentID: "d", ParsedDocumentID: "p"}
	valid := mappingBasisTest{raw: json.RawMessage(`[{"material_id":"m","material_version":2,"document_id":"d","parsed_document_id":"p"}]`)}
	if err := ValidateMappingBasis(context.Background(), valid, nil, association); err != nil {
		t.Fatal(err)
	}
	valid.raw = json.RawMessage(`[{"material_id":"m","material_version":2,"document_id":"d","parsed_document_id":"other"}]`)
	err := ValidateMappingBasis(context.Background(), valid, nil, association)
	e, ok := err.(*fault.Error)
	if !ok || e.Message != "更正依据必须固定当前服务对象关联的材料版本" {
		t.Fatalf("wrong parse accepted: %v", err)
	}
	valid.err = errors.New("bad proof")
	err = ValidateMappingBasis(context.Background(), valid, nil, association)
	e, ok = err.(*fault.Error)
	if !ok || e.Message != "更正依据必须引用授权案件中可见的冻结材料" {
		t.Fatalf("proof must fail first: %v", err)
	}
}

// A completed receipt is authoritative before any current tuple or source read.
type proposalReplayRepo struct{ MappingRepository }

func (proposalReplayRepo) ProposalReceipt(context.Context, CreateIdentity, string) ([]byte, string, bool, error) {
	return []byte(`{"request_id":"existing"}`), "same", true, nil
}
func TestProposalReplayPrecedesCurrentReads(t *testing.T) {
	result, replayed, err := ProposeMapping[previewTestDocument](context.Background(), proposalReplayRepo{}, nil, nil, PreviewScope{}, model.TrustedPrincipalProjection{}, nil, PreparedProposal{RequestSHA: "same"})
	if err != nil || !replayed || result.RequestID != "existing" {
		t.Fatalf("replay = %#v, %v, %v", result, replayed, err)
	}
}
