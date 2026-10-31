package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/rulepack"

	"github.com/jackc/pgx/v5"
)

// RulePack test bounds: synthetic sample input only, never case content.
const (
	maxRulePackTestSources       = 16
	maxRulePackTestSourceBytes   = 32 * 1024
	maxRulePackTestTotalBytes    = 256 * 1024
	maxRulePackTestFacts         = 128
	maxRulePackTestCitations     = 256
	maxRulePackTestFactCitations = 8
)

// RulePackTestSource is one caller-supplied synthetic source document.
type RulePackTestSource struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Role    string `json:"role"`
	Text    string `json:"text"`
}

// RulePackTestCitation is the bounded citation shape a sample fact carries. The
// server fills source tenant/case/digest itself.
type RulePackTestCitation struct {
	SourceID      string `json:"source_id"`
	SourceVersion string `json:"source_version"`
	Start         int    `json:"start"`
	End           int    `json:"end"`
	Quote         string `json:"quote"`
}

// RulePackTestFact is the typed fact input. It mirrors rulepack.Fact fields the
// sample executor needs and is not persisted.
type RulePackTestFact struct {
	ID                string                 `json:"id"`
	RuleID            string                 `json:"rule_id"`
	Dimension         string                 `json:"dimension"`
	ObjectRef         string                 `json:"object_ref"`
	Value             string                 `json:"value"`
	Unit              string                 `json:"unit"`
	SourceRole        string                 `json:"source_role"`
	MeasurementWindow string                 `json:"measurement_window,omitempty"`
	TimeZone          string                 `json:"time_zone,omitempty"`
	Calendar          string                 `json:"calendar,omitempty"`
	OutageDefinition  string                 `json:"outage_definition,omitempty"`
	Exclusions        []string               `json:"exclusions,omitempty"`
	Basis             string                 `json:"basis,omitempty"`
	Applicable        *bool                  `json:"applicable,omitempty"`
	Citations         []RulePackTestCitation `json:"citations"`
}

// RulePackTestInput is one bounded synthetic sample execution request.
type RulePackTestInput struct {
	PackID                  string               `json:"-"`
	Version                 string               `json:"-"`
	ExpectedRevision        int64                `json:"expected_revision"`
	RunFixtureSuite         bool                 `json:"run_fixture_suite,omitempty"`
	ExpectedContentHash     string               `json:"expected_content_hash,omitempty"`
	CompareVersion          string               `json:"compare_version,omitempty"`
	CompareExpectedRevision int64                `json:"compare_expected_revision,omitempty"`
	CompareExpectedHash     string               `json:"compare_expected_hash,omitempty"`
	BusinessLine            string               `json:"business_line"`
	Scenario                string               `json:"scenario"`
	ContractType            string               `json:"contract_type"`
	At                      string               `json:"at"`
	Sources                 []RulePackTestSource `json:"sources"`
	Facts                   []RulePackTestFact   `json:"facts"`
}

// RulePackTestResult returns the deterministic report of the sample execution.
type RulePackTestResult struct {
	Revision    int64           `json:"revision"`
	ContentHash string          `json:"content_hash"`
	Report      rulepack.Report `json:"report"`
}

func validateRulePackTestInput(input RulePackTestInput) error {
	if input.RunFixtureSuite || input.ExpectedContentHash != "" || input.CompareVersion != "" || input.CompareExpectedRevision != 0 || input.CompareExpectedHash != "" {
		return errors.New("fixture suite options are only accepted for a suite run")
	}
	if !validIdentifier(input.BusinessLine) || !validIdentifier(input.Scenario) || !validIdentifier(input.ContractType) {
		return errors.New("business_line, scenario and contract_type are required canonical identifiers")
	}
	if strings.TrimSpace(input.At) == "" {
		return errors.New("at is required")
	}
	if _, err := time.Parse(time.RFC3339, strings.TrimSpace(input.At)); err != nil {
		return errors.New("at must be an RFC3339 timestamp")
	}
	if len(input.Sources) == 0 {
		return errors.New("at least one synthetic source is required")
	}
	if len(input.Sources) > maxRulePackTestSources {
		return fmt.Errorf("synthetic source count exceeds %d", maxRulePackTestSources)
	}
	seenSource := map[string]struct{}{}
	total := 0
	for _, source := range input.Sources {
		if !validIdentifier(source.ID) || !validIdentifier(source.Version) || !validIdentifier(source.Role) {
			return errors.New("source id, version and role must be canonical identifiers")
		}
		key := source.ID + "\x00" + source.Version
		if _, exists := seenSource[key]; exists {
			return fmt.Errorf("duplicate synthetic source %q", source.ID)
		}
		seenSource[key] = struct{}{}
		if len(source.Text) > maxRulePackTestSourceBytes {
			return fmt.Errorf("synthetic source %q text exceeds %d bytes", source.ID, maxRulePackTestSourceBytes)
		}
		total += len(source.Text)
		if total > maxRulePackTestTotalBytes {
			return fmt.Errorf("synthetic source text exceeds %d bytes in total", maxRulePackTestTotalBytes)
		}
	}
	if len(input.Facts) > maxRulePackTestFacts {
		return fmt.Errorf("fact count exceeds %d", maxRulePackTestFacts)
	}
	seenFact := map[string]struct{}{}
	citations := 0
	for _, fact := range input.Facts {
		if !validIdentifier(fact.ID) || !validIdentifier(fact.RuleID) {
			return errors.New("fact id and rule_id must be canonical identifiers")
		}
		if _, exists := seenFact[fact.ID]; exists {
			return fmt.Errorf("duplicate fact id %q", fact.ID)
		}
		seenFact[fact.ID] = struct{}{}
		if len(fact.Citations) > maxRulePackTestFactCitations {
			return fmt.Errorf("fact %q citations exceed %d", fact.ID, maxRulePackTestFactCitations)
		}
		citations += len(fact.Citations)
		if citations > maxRulePackTestCitations {
			return fmt.Errorf("citation count exceeds %d", maxRulePackTestCitations)
		}
		for _, citation := range fact.Citations {
			key := citation.SourceID + "\x00" + citation.SourceVersion
			if _, exists := seenSource[key]; !exists {
				return fmt.Errorf("citation references unknown synthetic source %q", citation.SourceID)
			}
			if citation.Start < 0 || citation.End < citation.Start {
				return errors.New("citation code-point span is invalid")
			}
		}
	}
	return nil
}

// buildRulePackTestRequest assembles the rulepack execution request. Tenant,
// case, org and source digests are server-derived; the caller never supplies a
// principal, grant or case identity.
func buildRulePackTestRequest(tenantID string, input RulePackTestInput) (rulepack.Request, error) {
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(input.At))
	if err != nil {
		return rulepack.Request{}, errors.New("at must be an RFC3339 timestamp")
	}
	sources := make([]rulepack.Source, 0, len(input.Sources))
	digests := map[string]string{}
	for _, source := range input.Sources {
		digest := sha256Bytes([]byte(source.Text))
		digests[source.ID+"\x00"+source.Version] = digest
		sources = append(sources, rulepack.Source{
			ID: source.ID, Version: source.Version, TenantID: tenantID,
			CaseID: "rulepack-sample", Role: source.Role, Digest: digest, Text: source.Text,
		})
	}
	facts := make([]rulepack.Fact, 0, len(input.Facts))
	for _, fact := range input.Facts {
		converted := rulepack.Fact{
			ID: fact.ID, RuleID: fact.RuleID, Dimension: fact.Dimension, ObjectRef: fact.ObjectRef,
			Value: fact.Value, Unit: fact.Unit, SourceRole: fact.SourceRole,
			MeasurementWindow: fact.MeasurementWindow, TimeZone: fact.TimeZone, Calendar: fact.Calendar,
			OutageDefinition: fact.OutageDefinition, Basis: fact.Basis, Applicable: fact.Applicable,
		}
		converted.Exclusions = append([]string(nil), fact.Exclusions...)
		for _, citation := range fact.Citations {
			converted.Citations = append(converted.Citations, rulepack.Citation{
				SourceID: citation.SourceID, SourceVersion: citation.SourceVersion,
				TenantID: tenantID, CaseID: "rulepack-sample",
				SourceDigest: digests[citation.SourceID+"\x00"+citation.SourceVersion],
				Start:        citation.Start, End: citation.End, Quote: citation.Quote,
			})
		}
		facts = append(facts, converted)
	}
	return rulepack.Request{
		Principal: access.Principal{
			UserID: "rulepack-sample", TenantID: tenantID, Enabled: true,
			// The sample executor runs under a test-only case-scoped review grant
			// the server creates itself; no caller grant or case identity is used.
			Grants: []access.Grant{{Role: "reviewer", Scope: "case", CaseID: "rulepack-sample"}},
		},
		Resource:     access.Resource{TenantID: tenantID, CaseID: "rulepack-sample", OrgID: "rulepack-sample"},
		BusinessLine: input.BusinessLine, Scenario: input.Scenario, ContractType: input.ContractType,
		At: at, Sources: sources, Facts: facts,
	}, nil
}

// updateAdminAs runs one governance transaction under the trusted admin scope.
// Governance tables are Core-owned; the transaction takes the scheme-wide
// advisory lock exactly like the other writers, with no case key.
func (s *Store) updateAdminAs(admin accounts.User, fn func(pgx.Tx) error) error {
	if s.ReadOnly() {
		return fault.New(503, "read_only", "只读模式下不能修改规则治理")
	}
	scope := governanceScope(admin)
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema(), 0))`); err != nil {
		return err
	}
	if err := setLocalScope(ctx, tx, scope); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

var _ = utf8.RuneCountInString
