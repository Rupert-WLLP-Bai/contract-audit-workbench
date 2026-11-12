package app

import (
	"context"
	"testing"

	clmcontract "contractaudit/internal/contract/model"
)

type replayRepository struct{ DraftCommandRepository }

func (replayRepository) LoadContract(context.Context, string, string) (clmcontract.Aggregate, error) {
	return clmcontract.Aggregate{TenantID: "tenant", ContractID: "contract"}, nil
}

type replayAccess struct{}

func (replayAccess) CanRead(context.Context, clmcontract.Aggregate) bool { return true }
func (replayAccess) CanManage(clmcontract.Aggregate) bool                { return false }

func TestDraftReplayRejectsCorruptStoredResponse(t *testing.T) {
	for name, response := range map[string]string{
		"unknown field":        `{"summary":{},"unexpected":true}`,
		"unknown nested field": `{"summary":{"unexpected":true}}`,
		"trailing response":    `{"summary":{}} {"summary":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := replayCreatedDraft(context.Background(), CreateDraftPrincipal{TenantID: "tenant"},
				clmcontract.WriteReceipt{ContractID: "contract", RequestDigest: "digest", Response: []byte(response)},
				"digest", replayRepository{}, nil, replayAccess{})
			if err == nil {
				t.Fatal("corrupt persisted response was accepted")
			}
		})
	}
}
