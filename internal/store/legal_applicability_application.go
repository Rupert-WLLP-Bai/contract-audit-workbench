package store

import (
	"context"
	"encoding/json"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	legalApp "contractaudit/internal/legal/app"
	legalModel "contractaudit/internal/legal/model"
	platformpostgres "contractaudit/internal/platform/postgres"
	subjectPostgres "contractaudit/internal/subject/postgres"
	"github.com/jackc/pgx/v5"
)

type legalRecordFacts struct {
	tx   pgx.Tx
	user accounts.User
	item Case
}

func (f legalRecordFacts) VerifyBasis(ctx context.Context, raw json.RawMessage) (json.RawMessage, *time.Time, error) {
	st, err := loadState(ctx, f.tx, true)
	if err != nil {
		return nil, nil, err
	}
	item, ok := st.caseByID(f.item.ID)
	if !ok || item.TenantID != accounts.Tenant(f.user) || item.OwnerOrgID != f.item.OwnerOrgID {
		return nil, nil, fault.NotFound("没有找到对应案件")
	}
	refs := json.RawMessage("[]")
	provided, err := legalModel.LegalApplicabilityBasisProvided(raw)
	if err != nil {
		return nil, nil, fault.Invalid("法律适用事实依据格式无效")
	}
	if provided {
		refs, err = validateSubjectBasisRefs(st, &item, raw)
		if err != nil {
			return nil, nil, fault.Invalid("法律适用事实依据不是本案已冻结的材料原文")
		}
	}
	obtained, err := legalFactBasisObtainedAt(st, item, refs)
	return refs, obtained, err
}
func (f legalRecordFacts) LoadRecordBinding(ctx context.Context, pin legalModel.LegalApplicabilityPartyPin) (legalApp.RecordPartyBinding, error) {
	v, err := subjectPostgres.ReadRecordBinding(ctx, platformpostgres.Bind(f.tx), accounts.Tenant(f.user), f.item.OwnerOrgID, f.item.ID, pin.BindingID)
	return legalApp.RecordPartyBinding{HeadVersion: v.HeadVersion, SubjectID: v.SubjectID, SubjectVersion: v.SubjectVersion, Role: v.Role, State: v.State, SubjectHead: v.SubjectHead, Found: v.Found}, err
}
func (f legalRecordFacts) CountRecordRole(ctx context.Context, role string) (int, error) {
	return subjectPostgres.CountActiveRole(ctx, platformpostgres.Bind(f.tx), accounts.Tenant(f.user), f.item.OwnerOrgID, f.item.ID, role)
}

type legalRecordClock struct{}

func (legalRecordClock) Now() time.Time             { return time.Now() }
func (legalRecordClock) NewFactID() (string, error) { return newID("laf_") }
