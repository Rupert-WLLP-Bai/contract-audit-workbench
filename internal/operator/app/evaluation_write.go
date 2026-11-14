// File metadata (manifest mirror):
// Purpose: Constructs owned check and object append records, exact result digests and nullable tuple projections.
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
	"time"

	"contractaudit/internal/auditext"
	"contractaudit/internal/operator/model"
)

type EvaluationWrite struct {
	Identity              CreateIdentity
	Value                 model.OperatorEvaluation
	Key, RequestSHA       string
	InputJSON, ResultJSON []byte
	Aggregate             bool
}
type EvaluationObjectWrite struct {
	Identity     CreateIdentity
	EvaluationID string
	Ordinal      int
	Value        model.OperatorEvaluationObject
	FactsJSON    []byte
	FactsSHA     string
	ResultJSON   []byte
	ResultSHA    string
}
type EvaluationCheckWrite struct {
	Identity                                    CreateIdentity
	EvaluationID                                string
	Value                                       auditext.CheckResult
	Evidence, Limitations, Dependencies, Result []byte
	Now                                         time.Time
}
type EvaluationWriter interface {
	AppendEvaluation(context.Context, EvaluationWrite) error
	AppendCheck(context.Context, EvaluationCheckWrite) error
	AppendObject(context.Context, EvaluationObjectWrite) error
}

func WriteEvaluationChecks(ctx context.Context, repo EvaluationWriter, id CreateIdentity, evaluationID string, checks []auditext.CheckResult, now time.Time) error {
	for _, c := range checks {
		e, _ := json.Marshal(c.Evidence)
		l, _ := json.Marshal(c.Limitations)
		d, _ := json.Marshal(c.Dependencies)
		r, _ := json.Marshal(c)
		if err := repo.AppendCheck(ctx, EvaluationCheckWrite{Identity: id, EvaluationID: evaluationID, Value: c, Evidence: e, Limitations: l, Dependencies: d, Result: r, Now: now}); err != nil {
			return err
		}
	}
	return nil
}
func WriteEvaluationObject(ctx context.Context, repo EvaluationWriter, id CreateIdentity, evaluationID string, ordinal int, v model.OperatorEvaluationObject, facts []byte, factsSHA string) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	return repo.AppendObject(ctx, EvaluationObjectWrite{Identity: id, EvaluationID: evaluationID, Ordinal: ordinal, Value: v, FactsJSON: facts, FactsSHA: factsSHA, ResultJSON: b, ResultSHA: hex.EncodeToString(sum[:])})
}
