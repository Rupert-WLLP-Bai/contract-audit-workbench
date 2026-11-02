package store

import (
	"context"
	"encoding/json"
	"testing"

	"contractaudit/internal/accounts"
	"contractaudit/internal/model"
	"github.com/jackc/pgx/v5"
)

func TestPreparationPrivateProofRejectsGenericWrite(t *testing.T) {
	raw, _ := json.Marshal(model.DelegatedScope{Kind: "execution_prepare", Action: "embedded_prepare"})
	child, _ := json.Marshal(model.DelegatedScope{Kind: "document", Action: "embedded_prepare_parse"})
	for _, operation := range [][]byte{raw, child, []byte(`{`), []byte(`null`)} {
		user := accounts.WithCommandAuthority(accounts.User{}, accounts.CommandAuthority{OperationScope: operation})
		for _, outer := range []model.DelegatedScope{{}, {Kind: "execution"}, {Kind: "document"}} {
			ctx := commandContext(model.WithDelegatedScope(context.Background(), outer), user)
			if _, err := new(Store).beginContextTx(ctx, pgx.TxOptions{}); err == nil {
				t.Fatal("private preparation proof accepted by generic transaction")
			}
		}
	}
}
