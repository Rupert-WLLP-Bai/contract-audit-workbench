// File metadata (manifest mirror):
// Purpose: Decodes exact evaluation records, ordered checks and object rows with original fallback, digest and partial-result semantics.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"contractaudit/internal/auditext"
	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

type EvaluationRow struct {
	Record        model.EvaluationRecord
	Input, Result []byte
}
type CheckRow struct{ Result, Dependencies []byte }
type EvaluationObjectRow struct {
	ObjectRef, FactsSHA, ResultSHA string
	Facts, Result                  []byte
}
type CheckCursor interface {
	Next() bool
	ScanCheck() (CheckRow, error)
	Err() error
	Close()
}
type EvaluationObjectCursor interface {
	Next() bool
	ScanObject() (EvaluationObjectRow, error)
	Err() error
	Close()
}
type EvaluationReader interface {
	EvaluationIDByKey(context.Context, CreateIdentity, string) (string, error)
	EvaluationRaw(context.Context, string, string) (EvaluationRow, error)
	PreviousEvaluationID(context.Context, string, model.OperatorEvaluation) (string, error)
	Checks(context.Context, string, string) (CheckCursor, error)
	EvaluationObjects(context.Context, string, string) (EvaluationObjectCursor, error)
}

func ReadEvaluationByKey(ctx context.Context, repo EvaluationReader, id CreateIdentity, key string) (model.EvaluationRecord, error) {
	v, err := repo.EvaluationIDByKey(ctx, id, key)
	if err != nil {
		return model.EvaluationRecord{}, err
	}
	return ReadEvaluation(ctx, repo, id.CaseID, v)
}
func ReadEvaluation(ctx context.Context, repo EvaluationReader, caseID, evaluationID string) (model.EvaluationRecord, error) {
	row, err := repo.EvaluationRaw(ctx, caseID, evaluationID)
	out := &row.Record.Value
	if err != nil {
		return row.Record, err
	}
	out.BusinessAt = out.BusinessAt.UTC()
	out.CreatedAt = out.CreatedAt.UTC()
	if err = json.Unmarshal(row.Input, &out.Input); err != nil {
		return row.Record, err
	}
	out.EvidenceCutoffAt = out.Input.EvidenceCutoffAt
	if out.Input.EvaluationScope != nil {
		scope := model.OperatorEvaluationScope{Kind: out.Input.EvaluationScope.Kind, ServiceObjectIDs: append([]string(nil), out.Input.EvaluationScope.ServiceObjectIDs...)}
		out.Scope = &scope
	}
	checks, err := readEvaluationChecks(ctx, repo, caseID, evaluationID)
	if err != nil {
		return row.Record, err
	}
	if len(checks) == 0 {
		if err = json.Unmarshal(row.Result, &checks); err != nil {
			return row.Record, err
		}
	}
	out.Checks = checks
	if out.Input.EvaluationScope != nil {
		objects, e := readEvaluationObjects(ctx, repo, caseID, evaluationID)
		if e != nil {
			return row.Record, e
		}
		out.Objects = objects
		coverage := model.SummarizeEvaluationObjects(out.Input.EvaluationScope.Kind, objects)
		out.Coverage = &coverage
		if len(checks) == 0 {
			b, e := json.Marshal(objects)
			if e != nil {
				return row.Record, e
			}
			sum := sha256.Sum256(b)
			if hex.EncodeToString(sum[:]) != out.ResultSHA256 {
				return row.Record, fault.New(500, "integrity_error", "整案核验结果摘要不匹配")
			}
		}
	}
	return row.Record, nil
}
func ReadPreviousEvaluation(ctx context.Context, repo EvaluationReader, caseID string, target model.OperatorEvaluation) (model.OperatorEvaluation, error) {
	id, err := repo.PreviousEvaluationID(ctx, caseID, target)
	if err != nil {
		return model.OperatorEvaluation{}, err
	}
	r, err := ReadEvaluation(ctx, repo, caseID, id)
	return r.Value, err
}
func readEvaluationChecks(ctx context.Context, repo EvaluationReader, caseID, id string) ([]auditext.CheckResult, error) {
	rows, err := repo.Checks(ctx, caseID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []auditext.CheckResult
	for rows.Next() {
		row, e := rows.ScanCheck()
		if e != nil {
			return nil, e
		}
		var check auditext.CheckResult
		if e = json.Unmarshal(row.Result, &check); e != nil {
			return nil, e
		}
		if len(row.Dependencies) > 0 {
			if e = json.Unmarshal(row.Dependencies, &check.Dependencies); e != nil {
				return nil, e
			}
		}
		out = append(out, check)
	}
	return out, rows.Err()
}
func readEvaluationObjects(ctx context.Context, repo EvaluationReader, caseID, id string) ([]model.OperatorEvaluationObject, error) {
	rows, err := repo.EvaluationObjects(ctx, caseID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.OperatorEvaluationObject, 0)
	for rows.Next() {
		r, e := rows.ScanObject()
		if e != nil {
			return nil, e
		}
		a, b := sha256.Sum256(r.Facts), sha256.Sum256(r.Result)
		if hex.EncodeToString(a[:]) != r.FactsSHA || hex.EncodeToString(b[:]) != r.ResultSHA {
			return nil, fault.New(500, "integrity_error", "核验对象冻结事实或结果摘要不匹配")
		}
		var o model.OperatorEvaluationObject
		if json.Unmarshal(r.Result, &o) != nil || o.ObjectRef != r.ObjectRef || o.FactsSHA256 != r.FactsSHA {
			return nil, fault.New(500, "integrity_error", "核验对象冻结结果损坏")
		}
		out = append(out, o)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
