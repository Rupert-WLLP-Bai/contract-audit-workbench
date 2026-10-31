package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/rulepack"
	"github.com/jackc/pgx/v5"
)

// frozenRuleRequest deliberately excludes the principal and resource. Those
// are resolved again from the current directory and case for every replay.
type frozenRuleRequest struct {
	Encoding string `json:"encoding"`
	BusinessLine string `json:"business_line"`
	Scenario string `json:"scenario"`
	ContractType string `json:"contract_type"`
	At time.Time `json:"at"`
	Sources []rulepack.Source `json:"sources"`
	Facts []rulepack.Fact `json:"facts"`
	AuthorityGates []rulepack.AuthorityGate `json:"authority_gates"`
}

func freezeRuleRequest(request rulepack.Request) frozenRuleRequest {
	facts := append([]rulepack.Fact{}, request.Facts...)
	return frozenRuleRequest{Encoding: "rulepack-request/v1", BusinessLine: request.BusinessLine,
		Scenario: request.Scenario, ContractType: request.ContractType, At: request.At,
		Sources: append([]rulepack.Source{}, request.Sources...), Facts: facts,
		AuthorityGates: append([]rulepack.AuthorityGate{}, request.AuthorityGates...)}
}

func (f frozenRuleRequest) bind(request rulepack.Request) rulepack.Request {
	request.BusinessLine, request.Scenario, request.ContractType = f.BusinessLine, f.Scenario, f.ContractType
	request.At, request.Sources, request.Facts, request.AuthorityGates = f.At, f.Sources, f.Facts, f.AuthorityGates
	return request
}

// freezeAttemptRuleRequest captures the actual typed input before evaluation.
// A recovered worker reuses the first captured bytes; it cannot silently change
// facts or the evaluation time after a previous generation got this far.
func (s *Store) freezeAttemptRuleRequest(ctx context.Context, runID, attemptID string, item Case, request rulepack.Request) (rulepack.Request, error) {
	actor, err := trustedUser(accounts.User{ID: request.Principal.UserID})
	if err != nil {
		return rulepack.Request{}, err
	}
	claim, durable := s.claimForAttempt(attemptID)
	if durable && claim.JobID != "" && (claim.RunID != runID || claim.AttemptID != attemptID || claim.CaseID != item.ID || claim.ActorID != actor.ID) {
		return rulepack.Request{}, errors.New("rule input claim identity mismatch")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil { return rulepack.Request{}, err }
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema(),0))`); err != nil { return rulepack.Request{}, err }
	scope := userScope(actor)
	if durable && claim.JobID != "" { scope = claim.scope() }
	if err := setLocalScope(ctx, tx, scope); err != nil { return rulepack.Request{}, err }
	if err := scopedCaseLock(ctx, tx, item.ID); err != nil { return rulepack.Request{}, err }
	if err := scopedAttemptLock(ctx, tx, attemptID); err != nil { return rulepack.Request{}, err }
	var attemptState string
	if err := tx.QueryRow(ctx, `SELECT state FROM review_attempts WHERE tenant_id=$1 AND org_id=$2 AND run_id=$3 AND id=$4 FOR UPDATE`, item.TenantID, item.OwnerOrgID, runID, attemptID).Scan(&attemptState); err != nil { return rulepack.Request{}, err }
	if !attemptActive(attemptState) { return rulepack.Request{}, errAttemptTerminal }
	if durable && claim.JobID != "" {
		if err := s.requireLiveClaim(ctx, tx, claim, runID, attemptID); err != nil { return rulepack.Request{}, err }
	}
	if err := s.reauthorizeAttempt(ctx, tx, runID, attemptID); err != nil { return rulepack.Request{}, err }
	if err := revalidateGovernedAttemptTx(ctx, tx, runID, attemptID, item.ID); err != nil { return rulepack.Request{}, err }
	identity := JobClaim{TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: item.ID}
	st, err := s.loadScopedAttemptState(ctx, tx, identity, runID, attemptID)
	if err != nil { return rulepack.Request{}, err }
	attempt, ok := st.attemptByID(attemptID)
	if !ok || attempt.ActorID != actor.ID || attempt.InputHash == nil || attempt.RulePackCompilerRef != governedCompilerMarker { return rulepack.Request{}, errors.New("rule input lacks governed attempt identity") }
	input, ok := st.attemptInput(attemptID)
	snapshot := st.snapshotOf(runID)
	if !ok || input.InputHash != *attempt.InputHash || !frozenAttemptInputMatches(st, runID, attemptID, input) || s.verifyFrozenDocuments(st, input.Documents) != nil {
		return rulepack.Request{}, errors.New("rule input does not match the frozen attempt")
	}
	if _, err := ParseFrozenGovernedRulePack(attempt, item); err != nil { return rulepack.Request{}, err }
	if snapshot.ComparatorRef == "" || snapshot.GovernedKnowledge == nil { return rulepack.Request{}, errors.New("rule input lacks frozen extractor or knowledge") }
	sources, err := telecomSources(interpretedFromFrozen(input.Documents))
	if err != nil || !reflect.DeepEqual(sources, request.Sources) { return rulepack.Request{}, errors.New("rule input sources do not match the frozen documents") }
	gates, err := rulepackAuthorityGates(snapshot.GovernedKnowledge)
	if err != nil || !reflect.DeepEqual(append([]rulepack.AuthorityGate{}, gates...), append([]rulepack.AuthorityGate{}, request.AuthorityGates...)) {
		return rulepack.Request{}, errors.New("rule input authority gates do not match the frozen knowledge")
	}
	if request.BusinessLine != item.BusinessLine || request.Scenario != item.Scenario || request.ContractType != item.ContractType || request.At.IsZero() || rulepack.ValidateSources(request) != nil {
		return rulepack.Request{}, errors.New("rule input scope or sources are invalid")
	}
	frozen := freezeRuleRequest(request)
	raw, err := json.Marshal(frozen)
	if err != nil { return rulepack.Request{}, err }
	var previous, previousHash, savedCase, savedRun, savedPack, savedVersion, savedPackHash, savedPackDigest, savedExtractor string
	var previousBytes []byte
	err = tx.QueryRow(ctx, `SELECT request_sha256,request_bytes,attempt_input_sha256,case_id,run_id,pack_id,pack_version,pack_content_sha256,pack_snapshot_sha256,extractor_ref FROM review_rulepack_inputs WHERE tenant_id=$1 AND org_id=$2 AND attempt_id=$3`, item.TenantID, item.OwnerOrgID, attemptID).Scan(&previousHash, &previousBytes, &previous, &savedCase, &savedRun, &savedPack, &savedVersion, &savedPackHash, &savedPackDigest, &savedExtractor)
	if err == nil {
		var saved frozenRuleRequest
		if previous != input.InputHash || savedCase != item.ID || savedRun != runID || savedPack != attempt.RulePackID || savedVersion != attempt.RulePackVersion || savedPackHash != attempt.RulePackHash || savedPackDigest != attempt.RulePackSnapshotDigest || savedExtractor != snapshot.ComparatorRef || sha256Bytes(previousBytes) != previousHash || json.Unmarshal(previousBytes, &saved) != nil || saved.Encoding != "rulepack-request/v1" || saved.At.IsZero() || saved.Facts == nil || !reflect.DeepEqual(saved.Sources, frozen.Sources) || saved.BusinessLine != frozen.BusinessLine || saved.Scenario != frozen.Scenario || saved.ContractType != frozen.ContractType || !reflect.DeepEqual(saved.AuthorityGates, frozen.AuthorityGates) {
			return rulepack.Request{}, errors.New("stored rule input does not match the attempt")
		}
		canonical, marshalErr := json.Marshal(saved)
		if marshalErr != nil || !bytes.Equal(canonical, previousBytes) { return rulepack.Request{}, errors.New("stored rule input is not canonical") }
		request = saved.bind(request)
	} else if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `INSERT INTO review_rulepack_inputs (tenant_id,org_id,case_id,run_id,attempt_id,attempt_input_sha256,pack_id,pack_version,pack_content_sha256,pack_snapshot_sha256,extractor_ref,request_sha256,request_bytes) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, item.TenantID, item.OwnerOrgID, item.ID, runID, attemptID, input.InputHash, attempt.RulePackID, attempt.RulePackVersion, attempt.RulePackHash, attempt.RulePackSnapshotDigest, snapshot.ComparatorRef, sha256Bytes(raw), raw)
		if err != nil { return rulepack.Request{}, err }
		request = frozen.bind(request)
	} else { return rulepack.Request{}, err }
	if err := s.reauthorizeAttempt(ctx, tx, runID, attemptID); err != nil { return rulepack.Request{}, err }
	if err := revalidateGovernedAttemptTx(ctx, tx, runID, attemptID, item.ID); err != nil { return rulepack.Request{}, err }
	if durable && claim.JobID != "" {
		if err := s.requireLiveClaim(ctx, tx, claim, runID, attemptID); err != nil { return rulepack.Request{}, err }
	}
	if err := tx.Commit(ctx); err != nil { return rulepack.Request{}, err }
	return request, nil
}
