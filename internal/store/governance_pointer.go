package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
)

// postgresReturningOldNew reports whether this server can return the
// pre-image and post-image of one rule-pointer statement. PostgreSQL 18
// added RETURNING old/new; earlier servers still record both values, but
// the pre-image is read before the write.
func postgresReturningOldNew(serverVersionNum int) bool {
	return serverVersionNum >= 180000
}

func postgresVersionNum(ctx context.Context, tx pgx.Tx) (int, error) {
	var raw string
	if err := tx.QueryRow(ctx, `SELECT current_setting('server_version_num')`).Scan(&raw); err != nil {
		return 0, err
	}
	version, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("parse server_version_num %q: %w", raw, err)
	}
	return version, nil
}

func rulePackPointerUpsertSQL(useOldNew bool) string {
	statement := `INSERT INTO rule_pack_pointers (tenant_id, pack_id, version, active, pointer_revision, updated_at, updated_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (tenant_id, pack_id) DO UPDATE SET version=EXCLUDED.version, active=EXCLUDED.active, pointer_revision=EXCLUDED.pointer_revision, updated_at=EXCLUDED.updated_at, updated_by=EXCLUDED.updated_by
		WHERE rule_pack_pointers.pointer_revision=$8
		RETURNING `
	if useOldNew {
		return statement + `old.version, old.active, new.version, new.active`
	}
	return statement + `version, active`
}

func rulePackPointerDisableSQL(useOldNew bool) string {
	statement := `UPDATE rule_pack_pointers SET active=false, pointer_revision=$1, updated_at=$2, updated_by=$3
		WHERE tenant_id=$4 AND pack_id=$5 AND pointer_revision=$6 AND version=$7
		RETURNING `
	if useOldNew {
		return statement + `old.version, old.active, new.version, new.active`
	}
	return statement + `version, active`
}

type pointerTransition struct {
	PreviousVersion  *string
	PreviousActive   *bool
	ResultingVersion string
	ResultingActive  bool
}

// moveRulePackPointer applies one compare-and-swap pointer change and
// returns the database's before and after images. On PostgreSQL 18 both
// images come from the same statement. A missed CAS returns a conflict and
// does not invent a transition.
func moveRulePackPointer(ctx context.Context, tx pgx.Tx, tenantID, packID, version string, active bool, pointerRevision int64, updatedAt, updatedBy string, expectedRevision int64, disable bool) (pointerTransition, error) {
	versionNum, err := postgresVersionNum(ctx, tx)
	if err != nil {
		return pointerTransition{}, err
	}
	useOldNew := postgresReturningOldNew(versionNum)
	var previousVersion *string
	var previousActive *bool
	if !useOldNew {
		previousVersion, previousActive, err = loadRulePackPointerImage(ctx, tx, tenantID, packID)
		if err != nil {
			return pointerTransition{}, err
		}
	}
	var transition pointerTransition
	if disable {
		transition, err = scanPointerStatement(ctx, tx, rulePackPointerDisableSQL(useOldNew), useOldNew, previousVersion, previousActive,
			pointerRevision, updatedAt, updatedBy, tenantID, packID, expectedRevision, version)
	} else {
		transition, err = scanPointerStatement(ctx, tx, rulePackPointerUpsertSQL(useOldNew), useOldNew, previousVersion, previousActive,
			tenantID, packID, version, active, pointerRevision, updatedAt, updatedBy, expectedRevision)
	}
	if err != nil {
		return pointerTransition{}, err
	}
	if transition.ResultingVersion != version || transition.ResultingActive != active {
		return pointerTransition{}, fmt.Errorf("rule pointer transition returned %q active=%t, requested %q active=%t", transition.ResultingVersion, transition.ResultingActive, version, active)
	}
	return transition, nil
}

func loadRulePackPointerImage(ctx context.Context, tx pgx.Tx, tenantID, packID string) (*string, *bool, error) {
	var version string
	var active bool
	err := tx.QueryRow(ctx, `SELECT version, active FROM rule_pack_pointers WHERE tenant_id=$1 AND pack_id=$2`, tenantID, packID).Scan(&version, &active)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	return &version, &active, nil
}

func scanPointerStatement(ctx context.Context, tx pgx.Tx, statement string, useOldNew bool, previousVersion *string, previousActive *bool, args ...any) (pointerTransition, error) {
	var resultingVersion string
	var resultingActive bool
	var err error
	if useOldNew {
		var oldVersion *string
		var oldActive *bool
		err = tx.QueryRow(ctx, statement, args...).Scan(&oldVersion, &oldActive, &resultingVersion, &resultingActive)
		if oldVersion != nil {
			previousVersion = oldVersion
			previousActive = oldActive
		}
	} else {
		err = tx.QueryRow(ctx, statement, args...).Scan(&resultingVersion, &resultingActive)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pointerTransition{}, fault.Conflict("规则包启用指针已被其他操作修改，请刷新后重试")
		}
		return pointerTransition{}, err
	}
	if previousVersion != nil && previousActive == nil {
		return pointerTransition{}, errors.New("rule pointer pre-image is missing its active flag")
	}
	return pointerTransition{
		PreviousVersion: previousVersion, PreviousActive: previousActive,
		ResultingVersion: resultingVersion, ResultingActive: resultingActive,
	}, nil
}

func (entry *RulePackHistoryEntry) recordPointerTransition(transition pointerTransition) {
	entry.PreviousVersion = transition.PreviousVersion
	entry.PreviousActive = transition.PreviousActive
	resultingVersion := transition.ResultingVersion
	resultingActive := transition.ResultingActive
	entry.ResultingVersion = &resultingVersion
	entry.ResultingActive = &resultingActive
}
