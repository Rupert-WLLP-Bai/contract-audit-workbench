// File metadata (manifest mirror):
// Purpose: Defines versioned Agent step checkpoints and the narrow persistence port used to recover successful planner, knowledge-read, and tool results.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Package steps owns the recovery contract for durable Agent planner,
// knowledge-read, and tool steps. It contains no database or Store dependency.
package steps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"
)

const (
	StatusStarted   = "started"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusAmbiguous = "ambiguous"

	UsageObserved      = "observed"
	UsageUnknown       = "unknown"
	UsageNotApplicable = "not_applicable"

	MaxPayloadBytes = 4 << 20
)

var (
	ErrAmbiguous        = errors.New("Agent step result is ambiguous")
	ErrIdentityMismatch = errors.New("Agent step checkpoint identity does not match")
	ErrCorrupt          = errors.New("Agent step checkpoint is corrupt")
	ErrPersistence      = errors.New("Agent step checkpoint persistence failed")
	ErrTerminal         = errors.New("Agent attempt is terminal")
	ErrPriorFailure     = errors.New("a prior Agent step failed")
)

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var identityPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_./:-]{0,255}$`)
var failureCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,79}$`)

// Identity is the immutable input identity for one logical planner,
// knowledge-read, or tool step. The attempt scope and live generation are
// bound by the Store adapter.
type Identity struct {
	StepID                  string
	Kind                    string
	Round                   int
	RuntimeVersion          string
	PlannerID               string
	PlannerConfigurationID  string
	ModelConfigurationID    string
	ToolCatalogVersion      string
	ToolCatalogSHA256       string
	ToolID                  string
	ToolVersion             string
	ToolConfigurationSHA256 string
	InputSHA256             string
}

func (identity Identity) Validate() error {
	if !identityPattern.MatchString(identity.StepID) || (identity.Kind != "planner" && identity.Kind != "tool" && identity.Kind != "knowledge_read") || identity.Round < 1 ||
		identity.RuntimeVersion == "" || identity.PlannerID == "" || identity.PlannerConfigurationID == "" ||
		identity.ToolCatalogVersion == "" || !digestPattern.MatchString(identity.ToolCatalogSHA256) || !digestPattern.MatchString(identity.InputSHA256) {
		return errors.New("Agent step identity is incomplete")
	}
	if identity.Kind != "tool" {
		if identity.ToolID != "" || identity.ToolVersion != "" || identity.ToolConfigurationSHA256 != "" {
			return errors.New("non-tool step carries tool identity")
		}
		return nil
	}
	if !identityPattern.MatchString(identity.ToolID) || identity.ToolVersion == "" || !digestPattern.MatchString(identity.ToolConfigurationSHA256) {
		return errors.New("tool step identity is incomplete")
	}
	return nil
}

// Scope is supplied only by the trusted Execution Store transaction adapter.
type Scope struct {
	TenantID           string
	OrgID              string
	CaseID             string
	RunID              string
	AttemptID          string
	Generation         int64
	AttemptInputSHA256 string
}

func (scope Scope) Validate() error {
	if scope.TenantID == "" || scope.OrgID == "" || scope.CaseID == "" || scope.RunID == "" || scope.AttemptID == "" ||
		scope.Generation < 1 || !digestPattern.MatchString(scope.AttemptInputSHA256) {
		return errors.New("Agent step scope is incomplete")
	}
	return nil
}

// Usage records provider counters without treating unavailable counters as
// zero. NotApplicable is used by local deterministic steps with no provider.
type Usage struct {
	Status       string
	InputTokens  *int64
	OutputTokens *int64
	TotalTokens  *int64
}

func (usage Usage) Validate() error {
	switch usage.Status {
	case UsageObserved:
		if usage.InputTokens == nil || usage.OutputTokens == nil || usage.TotalTokens == nil ||
			*usage.InputTokens < 0 || *usage.OutputTokens < 0 || *usage.TotalTokens < 0 {
			return errors.New("observed Agent step usage is incomplete")
		}
	case UsageUnknown, UsageNotApplicable:
		if usage.InputTokens != nil || usage.OutputTokens != nil || usage.TotalTokens != nil {
			return errors.New("unknown Agent step usage cannot contain token counters")
		}
	default:
		return errors.New("Agent step usage status is invalid")
	}
	return nil
}

// Outcome is recorded only after a step has returned. Successful payloads are
// complete typed results needed by the next step; failures carry only a bounded
// code and can never masquerade as successful output.
type Outcome struct {
	Status        string
	Payload       []byte
	FailureCode   string
	ObservedAt    time.Time
	ElapsedMS     int64
	CallsConsumed int
	Usage         Usage
}

func (outcome Outcome) Validate() error {
	if outcome.Status != StatusSucceeded && outcome.Status != StatusFailed {
		return errors.New("Agent step outcome status is invalid")
	}
	if outcome.ObservedAt.IsZero() || outcome.ElapsedMS < 0 || outcome.CallsConsumed < 1 {
		return errors.New("Agent step outcome observation is incomplete")
	}
	if err := outcome.Usage.Validate(); err != nil {
		return err
	}
	if outcome.Status == StatusSucceeded {
		if len(outcome.Payload) == 0 || len(outcome.Payload) > MaxPayloadBytes || outcome.FailureCode != "" {
			return errors.New("successful Agent step payload is invalid")
		}
		return nil
	}
	if len(outcome.Payload) != 0 || !failureCodePattern.MatchString(outcome.FailureCode) {
		return errors.New("failed Agent step outcome is invalid")
	}
	return nil
}

// Record is a verified row returned by the persistence adapter.
type Record struct {
	Scope         Scope
	Identity      Identity
	Status        string
	Payload       []byte
	OutputSHA256  string
	FailureCode   string
	StartedAt     time.Time
	ObservedAt    *time.Time
	ElapsedMS     int64
	ElapsedKnown  bool
	CallsConsumed int
	Usage         Usage
}

func (record Record) Validate() error {
	if err := record.Scope.Validate(); err != nil {
		return err
	}
	if err := record.Identity.Validate(); err != nil {
		return err
	}
	if record.Status != StatusStarted && record.Status != StatusSucceeded && record.Status != StatusFailed && record.Status != StatusAmbiguous {
		return ErrCorrupt
	}
	if record.StartedAt.IsZero() || record.ElapsedMS < 0 || record.CallsConsumed < 1 {
		return ErrCorrupt
	}
	if record.Status == StatusStarted || record.Status == StatusAmbiguous {
		if len(record.Payload) != 0 || record.OutputSHA256 != "" || record.ObservedAt != nil || record.ElapsedKnown ||
			record.Usage.Status != UsageUnknown || record.Usage.InputTokens != nil || record.Usage.OutputTokens != nil || record.Usage.TotalTokens != nil {
			return ErrCorrupt
		}
		if record.Status == StatusStarted && record.FailureCode != "" || record.Status == StatusAmbiguous && !failureCodePattern.MatchString(record.FailureCode) {
			return ErrCorrupt
		}
		return nil
	}
	if record.ObservedAt == nil || record.ObservedAt.IsZero() {
		return ErrCorrupt
	}
	if !record.ElapsedKnown {
		return ErrCorrupt
	}
	if err := record.Usage.Validate(); err != nil {
		return ErrCorrupt
	}
	if record.Status == StatusSucceeded {
		if len(record.Payload) == 0 || len(record.Payload) > MaxPayloadBytes || !digestPattern.MatchString(record.OutputSHA256) ||
			sha256Hex(record.Payload) != record.OutputSHA256 || record.FailureCode != "" {
			return ErrCorrupt
		}
		return nil
	}
	if len(record.Payload) != 0 || record.OutputSHA256 != "" || !failureCodePattern.MatchString(record.FailureCode) {
		return ErrCorrupt
	}
	return nil
}

// RecoverySummary counts durable step time only. The caller combines it with
// prior event time using a maximum so model/tool callback durations are not
// counted twice.
type RecoverySummary struct {
	ElapsedMS          int64
	KnowledgeReadCount int
}

// Journal is a transaction-backed step checkpoint port. Begin returns true
// only for a newly inserted started row; every previously stored result or
// unresolved call is returned with false and must be handled without invoking
// the planner, knowledge reader, or tool again.
type Journal interface {
	RecoverySummary(context.Context) (RecoverySummary, error)
	Begin(context.Context, Identity) (Record, bool, error)
	Finish(context.Context, Identity, Outcome) error
}

func OutputDigest(payload []byte) string { return sha256Hex(payload) }

func sha256Hex(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func (record Record) String() string {
	return fmt.Sprintf("%s/%s/%d:%s", record.Identity.Kind, record.Identity.StepID, record.Scope.Generation, record.Status)
}
