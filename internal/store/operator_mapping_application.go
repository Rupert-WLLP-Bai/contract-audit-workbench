// File metadata (manifest mirror):
// Purpose: Projects trusted principals and frozen preview facts, reuses the sole strict material-anchor resolver and supplies original clock or identifier capabilities.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"encoding/json"
	"time"

	"contractaudit/internal/accounts"
	operatorApp "contractaudit/internal/operator/app"
	operatorModel "contractaudit/internal/operator/model"
	operatorPostgres "contractaudit/internal/operator/postgres"
	platformpostgres "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

type mappingBasisFacts struct {
	st   state
	item Case
}

func (f mappingBasisFacts) VerifyBasis(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return validateSubjectBasisRefs(f.st, &f.item, raw)
}

type mappingClock struct{}

func (mappingClock) Now() time.Time                 { return time.Now() }
func (mappingClock) NewDecisionID() (string, error) { return newID("omcd_") }
func (mappingClock) NewBindingID() (string, error)  { return newID("bind_") }
func (mappingClock) NewRequestID() (string, error)  { return newID("omcr_") }
func mappingScope(item Case) operatorApp.PreviewScope {
	return operatorApp.PreviewScope{TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: item.ID, BusinessLine: item.BusinessLine, Scenario: item.Scenario, ContractType: item.ContractType}
}
func mappingFacts(s *Store, tx pgx.Tx, st state, user accounts.User, item Case) operatorPreviewFacts {
	return operatorPreviewFacts{store: s, tx: tx, state: st, item: item, user: user}
}
func mappingRepo(tx pgx.Tx) *operatorPostgres.Mapping {
	return operatorPostgres.NewMapping(platformpostgres.Bind(tx))
}
func mappingPrincipal(user accounts.User) operatorModel.TrustedPrincipalProjection {
	return operatorModel.TrustedPrincipalProjection{UserID: user.ID, Issuer: user.Issuer, Subject: user.Subject}
}

// collectionWritable evaluates Core's existing policy only at an acquire miss or takeover.
type collectionWritable struct{ item Case }

func (f collectionWritable) CheckWritable() error { return businessWritable(f.item) }
