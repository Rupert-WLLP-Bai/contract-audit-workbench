package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	legalApp "contractaudit/internal/legal/app"
	legalModel "contractaudit/internal/legal/model"
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
	var out legalApp.RecordPartyBinding
	err := f.tx.QueryRow(ctx, `SELECT h.current_version,v.subject_id,v.subject_version,v.role,v.binding_state,sv.current_version FROM case_subject_roles h JOIN case_subject_role_versions v ON v.tenant_id=h.tenant_id AND v.org_id=h.org_id AND v.case_id=h.case_id AND v.binding_id=h.binding_id AND v.version=h.current_version JOIN subjects sv ON sv.tenant_id=v.tenant_id AND sv.subject_id=v.subject_id WHERE h.tenant_id=$1 AND h.org_id=$2 AND h.case_id=$3 AND h.binding_id=$4`, accounts.Tenant(f.user), f.item.OwnerOrgID, f.item.ID, pin.BindingID).Scan(&out.HeadVersion, &out.SubjectID, &out.SubjectVersion, &out.Role, &out.State, &out.SubjectHead)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	out.Found = err == nil
	return out, err
}
func (f legalRecordFacts) CountRecordRole(ctx context.Context, role string) (int, error) {
	var count int
	err := f.tx.QueryRow(ctx, `SELECT count(*) FROM case_subject_roles h JOIN case_subject_role_versions v ON v.tenant_id=h.tenant_id AND v.org_id=h.org_id AND v.case_id=h.case_id AND v.binding_id=h.binding_id AND v.version=h.current_version WHERE h.tenant_id=$1 AND h.org_id=$2 AND h.case_id=$3 AND h.current_version=v.version AND v.role=$4 AND v.binding_state='active'`, accounts.Tenant(f.user), f.item.OwnerOrgID, f.item.ID, role).Scan(&count)
	return count, err
}

type legalRecordClock struct{}

func (legalRecordClock) Now() time.Time             { return time.Now() }
func (legalRecordClock) NewFactID() (string, error) { return newID("laf_") }
