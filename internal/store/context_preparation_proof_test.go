// File metadata (manifest mirror):
// Purpose: 实现 `TestPreparationPrivateProofRejectsGenericWrite` 等 Store 数据结构与事务操作，按租户和组织范围读写或校验持久状态。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

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
