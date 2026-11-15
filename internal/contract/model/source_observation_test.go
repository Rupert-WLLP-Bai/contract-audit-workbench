// File metadata (manifest mirror):
// Purpose: Exercises external contract source observation preparation: authority and event validation, case scoping, snapshot normalization, unknown amount handling and digest preservation.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package contract

import (
	"errors"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/clmsource"
	"contractaudit/internal/fault"
)

func sourceObservationAuthority() ContractSourceAuthority {
	return ContractSourceAuthority{
		TenantID: "tenant-a", SourceID: "source-a", KeyID: "key-a",
		Kind: string(clmsource.SourceMock), Ordering: clmsource.VersionOpaque,
		AllowedCases: []ContractSourceCaseScope{{OrgID: "org-a", CaseID: "case-a"}},
	}
}

func sourceObservationInput() ContractSourceObservationInput {
	return ContractSourceObservationInput{
		EventID: "event-1", ExternalContractID: "ext-1", SourceVersion: "v1",
		SourceState: "  signed  ", OccurredAt: time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC),
		CaseOrgID: "org-a", CaseID: "case-a",
		Snapshot: DraftContent{Contract: &ContractBasics{
			Name: "外部合同", Amount: "100.00", Currency: "CNY",
			RelatedParties: []PartyRef{{
				PartyID: "party-1", Role: "supplier", NameSnapshot: "供应商", BindingID: "binding-1",
				ReferenceSnapshot: &PartyReferenceSnapshot{BindingID: "binding-1"},
			}},
		}},
		RawBody: []byte(`{"contract":{"name":"外部合同"}}`),
	}
}

func requireSourceFault(t *testing.T, name string, err error, status int) {
	t.Helper()
	var fe *fault.Error
	if err == nil {
		t.Fatalf("%s was accepted", name)
	}
	if !errors.As(err, &fe) || fe.Status != status {
		t.Fatalf("%s returned %v", name, err)
	}
}

func TestPrepareContractSourceObservationNormalizesSemanticSnapshot(t *testing.T) {
	authority := sourceObservationAuthority()
	input := sourceObservationInput()
	prepared, err := PrepareContractSourceObservation(authority, input)
	if err != nil {
		t.Fatalf("prepare observation: %v", err)
	}
	if prepared.Authority.SourceID != "source-a" || !prepared.AmountCurrencyValid ||
		prepared.Input.SourceState != "signed" || prepared.Input.EventID != "event-1" ||
		string(prepared.Input.RawBody) != `{"contract":{"name":"外部合同"}}` {
		t.Fatalf("prepared observation lost identity: %+v", prepared)
	}
	party := prepared.Input.Snapshot.Contract.RelatedParties[0]
	if party.BindingID != "" || party.ReferenceSnapshot != nil {
		t.Fatalf("server-managed party state survived: %+v", party)
	}
	if input.Snapshot.Contract.RelatedParties[0].BindingID != "binding-1" {
		t.Fatal("preparation mutated the caller's snapshot")
	}
	if prepared.Input.Snapshot.Contract.Amount != "100.00" || prepared.DigestSnapshot.Contract.Amount != "100.00" {
		t.Fatalf("known amount changed: %+v", prepared)
	}

	blankState := sourceObservationInput()
	blankState.SourceState = "   "
	prepared, err = PrepareContractSourceObservation(authority, blankState)
	if err != nil || prepared.Input.SourceState != "unknown" {
		t.Fatalf("blank source state became %q (%v)", prepared.Input.SourceState, err)
	}

	monotonic := sourceObservationAuthority()
	monotonic.Ordering = clmsource.VersionMonotonicSequence
	sequence := uint64(7)
	sequenced := sourceObservationInput()
	sequenced.SourceSequence = &sequence
	prepared, err = PrepareContractSourceObservation(monotonic, sequenced)
	if err != nil || prepared.Input.SourceSequence == nil || *prepared.Input.SourceSequence != 7 {
		t.Fatalf("monotonic sequence observation: %+v %v", prepared, err)
	}
}

func TestPrepareContractSourceObservationClearsUnknownAmountsButKeepsDigest(t *testing.T) {
	authority := sourceObservationAuthority()
	for name, mutate := range map[string]func(*ContractSourceObservationInput){
		"malformed amount":   func(in *ContractSourceObservationInput) { in.Snapshot.Contract.Amount = "1.5" },
		"missing currency":   func(in *ContractSourceObservationInput) { in.Snapshot.Contract.Currency = "" },
		"lowercase currency": func(in *ContractSourceObservationInput) { in.Snapshot.Contract.Currency = "cny" },
	} {
		t.Run(name, func(t *testing.T) {
			input := sourceObservationInput()
			mutate(&input)
			prepared, err := PrepareContractSourceObservation(authority, input)
			if err != nil {
				t.Fatalf("unknown amount must stay legal: %v", err)
			}
			if prepared.AmountCurrencyValid {
				t.Fatal("unknown amount was reported valid")
			}
			if prepared.Input.Snapshot.Contract.Amount != "" || prepared.Input.Snapshot.Contract.Currency != "" {
				t.Fatalf("unknown pair reached storage: %+v", prepared.Input.Snapshot.Contract)
			}
			if prepared.DigestSnapshot.Contract.Amount != input.Snapshot.Contract.Amount ||
				prepared.DigestSnapshot.Contract.Currency != input.Snapshot.Contract.Currency {
				t.Fatalf("digest snapshot lost observed values: %+v", prepared.DigestSnapshot.Contract)
			}
		})
	}
}

func TestPrepareContractSourceObservationRejectsInvalidAuthorityAndInput(t *testing.T) {
	validAuthority := sourceObservationAuthority()
	validInput := sourceObservationInput()
	for name, mutate := range map[string]func(*ContractSourceAuthority){
		"missing tenant":   func(a *ContractSourceAuthority) { a.TenantID = " " },
		"missing source":   func(a *ContractSourceAuthority) { a.SourceID = "" },
		"missing key":      func(a *ContractSourceAuthority) { a.KeyID = "" },
		"invalid kind":     func(a *ContractSourceAuthority) { a.Kind = "fixture" },
		"invalid ordering": func(a *ContractSourceAuthority) { a.Ordering = "realtime" },
	} {
		authority := validAuthority
		mutate(&authority)
		_, err := PrepareContractSourceObservation(authority, validInput)
		requireSourceFault(t, name, err, 422)
	}

	for name, mutate := range map[string]func(*ContractSourceObservationInput){
		"empty event":          func(in *ContractSourceObservationInput) { in.EventID = "" },
		"event too long":       func(in *ContractSourceObservationInput) { in.EventID = strings.Repeat("a", 301) },
		"event trailing space": func(in *ContractSourceObservationInput) { in.EventID = "event-1 " },
		"empty external id":    func(in *ContractSourceObservationInput) { in.ExternalContractID = "" },
		"external id long":     func(in *ContractSourceObservationInput) { in.ExternalContractID = strings.Repeat("a", 201) },
		"version empty":        func(in *ContractSourceObservationInput) { in.SourceVersion = "" },
		"version long":         func(in *ContractSourceObservationInput) { in.SourceVersion = strings.Repeat("a", 201) },
		"sequence on opaque": func(in *ContractSourceObservationInput) {
			sequence := uint64(1)
			in.SourceSequence = &sequence
		},
		"empty body":      func(in *ContractSourceObservationInput) { in.RawBody = nil },
		"body too large":  func(in *ContractSourceObservationInput) { in.RawBody = make([]byte, (1<<20)+1) },
		"zero occurred":   func(in *ContractSourceObservationInput) { in.OccurredAt = time.Time{} },
		"state too long":  func(in *ContractSourceObservationInput) { in.SourceState = strings.Repeat("a", 201) },
		"state with CRLF": func(in *ContractSourceObservationInput) { in.SourceState = "signed\r\n" },
		"state with null": func(in *ContractSourceObservationInput) { in.SourceState = "signed\x00" },
	} {
		input := sourceObservationInput()
		mutate(&input)
		_, err := PrepareContractSourceObservation(validAuthority, input)
		requireSourceFault(t, name, err, 422)
	}

	zeroSequence := validAuthority
	zeroSequence.Ordering = clmsource.VersionMonotonicSequence
	sequenced := sourceObservationInput()
	sequence := uint64(0)
	sequenced.SourceSequence = &sequence
	_, err := PrepareContractSourceObservation(zeroSequence, sequenced)
	requireSourceFault(t, "zero monotonic sequence", err, 422)

	unauthorized := sourceObservationInput()
	unauthorized.CaseID = "case-b"
	_, err = PrepareContractSourceObservation(validAuthority, unauthorized)
	requireSourceFault(t, "unauthorized case", err, 404)

	missingContract := sourceObservationInput()
	missingContract.Snapshot = DraftContent{}
	_, err = PrepareContractSourceObservation(validAuthority, missingContract)
	requireSourceFault(t, "snapshot without contract", err, 422)

	withProcurement := sourceObservationInput()
	withProcurement.Snapshot.Contract.ProcurementResultPackage = &ProcurementRef{System: "erp"}
	_, err = PrepareContractSourceObservation(validAuthority, withProcurement)
	requireSourceFault(t, "local procurement reference", err, 422)

	withMaterials := sourceObservationInput()
	withMaterials.Snapshot.Materials = []MaterialRef{{MaterialID: "m1"}}
	_, err = PrepareContractSourceObservation(validAuthority, withMaterials)
	requireSourceFault(t, "case material reference", err, 422)

	invalidContent := sourceObservationInput()
	invalidContent.Snapshot.Contract.Name = strings.Repeat("a", 201)
	_, err = PrepareContractSourceObservation(validAuthority, invalidContent)
	requireSourceFault(t, "invalid normalized snapshot", err, 422)
}

func TestAllowsContractSourceCaseMatchesExactScopes(t *testing.T) {
	scopes := []ContractSourceCaseScope{{OrgID: "org-a", CaseID: "case-a"}}
	if !AllowsContractSourceCase(scopes, "org-a", "case-a") {
		t.Fatal("exact scope was refused")
	}
	for _, scope := range [][2]string{{"org-b", "case-a"}, {"org-a", "case-b"}, {"", ""}} {
		if AllowsContractSourceCase(scopes, scope[0], scope[1]) {
			t.Fatalf("scope %v was accepted", scope)
		}
	}
	if AllowsContractSourceCase(nil, "org-a", "case-a") {
		t.Fatal("missing scopes authorized a case")
	}
}
