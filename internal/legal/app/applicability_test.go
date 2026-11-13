package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	legal "contractaudit/internal/legal/model"
)

type subjectFactsStub struct {
	calls        []string
	binding      SubjectBinding
	current      int64
	currentFound bool
	count        int
	subjectErr   error
}

func (s *subjectFactsStub) LoadFactFamilies(context.Context) ([]FactFamily, error) {
	s.calls = append(s.calls, "families")
	return []FactFamily{{Dimension: "buyer_kind", Head: 1, Versions: []legal.LegalApplicabilityFactVersion{{Version: 1, Status: "established", Value: "enterprise", TemporalBasis: "historical_interval", ValidFrom: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), Party: legal.LegalApplicabilityPartyPin{Side: "buyer", BindingID: "b", BindingVersion: 1, SubjectID: "s", SubjectVersion: 1}}}}}, nil
}
func (s *subjectFactsStub) LoadSubjectBinding(context.Context, legal.LegalApplicabilityPartyPin) (SubjectBinding, error) {
	s.calls = append(s.calls, "binding")
	return s.binding, nil
}
func (s *subjectFactsStub) LoadSubjectVersion(context.Context, string) (int64, bool, error) {
	s.calls = append(s.calls, "subject")
	return s.current, s.currentFound, s.subjectErr
}
func (s *subjectFactsStub) CountActiveRole(context.Context, string) (int, error) {
	s.calls = append(s.calls, "count")
	return s.count, nil
}
func TestResolveApplicabilitySubjectLookupPrecedesStaleAndCount(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		facts   subjectFactsStub
		want    []string
		reason  string
		wantErr error
	}{
		{"missing binding", subjectFactsStub{}, []string{"families", "binding"}, "stale_party_context", nil},
		{"stale binding still reads subject", subjectFactsStub{binding: SubjectBinding{Found: true, BindingVersion: 2, SubjectID: "s", SubjectVersion: 1, Role: "customer", State: "active"}, current: 1, currentFound: true}, []string{"families", "binding", "subject"}, "stale_party_context", nil},
		{"fresh checks count", subjectFactsStub{binding: SubjectBinding{Found: true, BindingVersion: 1, SubjectID: "s", SubjectVersion: 1, Role: "customer", State: "active"}, current: 1, currentFound: true, count: 2}, []string{"families", "binding", "subject", "count"}, "party_scope_ambiguous", nil},
		{"subject error before stale", subjectFactsStub{binding: SubjectBinding{Found: true, BindingVersion: 2, SubjectID: "s"}, subjectErr: errors.New("subject failure")}, []string{"families", "binding", "subject"}, "", errors.New("subject failure")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.facts
			got, err := ResolveApplicability(context.Background(), &f, at, at)
			if !reflect.DeepEqual(f.calls, tc.want) {
				t.Fatalf("calls=%v", f.calls)
			}
			if tc.wantErr != nil {
				if err == nil || err.Error() != tc.wantErr.Error() {
					t.Fatalf("err=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range got {
				if v.Dimension == "buyer_kind" {
					if v.UnknownReason != tc.reason {
						t.Fatalf("fact=%+v", v)
					}
					return
				}
			}
			t.Fatal("buyer dimension missing")
		})
	}
}
