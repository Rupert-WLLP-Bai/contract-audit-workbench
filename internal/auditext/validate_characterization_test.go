package auditext

// Characterization tests for the auditext validation surface. They pin the
// exact error text, the sentinel error, and the first-error precedence of the
// public Validate entry points before the validation bodies are split into
// helpers. Every test calls an exported entry point.

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	invalidRequestText = "auditext: invalid request"
	unauthorizedText   = "auditext: unauthorized"
)

func checkValidateError(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("want error %q, got nil", want)
	}
	if got := err.Error(); got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	if want == unauthorizedText {
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("error %q does not wrap ErrUnauthorized", err)
		}
		return
	}
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error %q does not wrap ErrInvalidRequest", err)
	}
}

func invalidRequest(section string) string {
	return invalidRequestText + ": " + section
}

func ptr[T any](value T) *T {
	return &value
}

func validField() Field {
	return Field{
		Name:          "configured_bandwidth",
		Value:         "80",
		Unit:          "Mbit/s",
		SourcePointer: "/serviceCharacteristic/0/value",
		SemanticKind:  "configured_value",
	}
}

func validEvidenceDocument() EvidenceRef {
	return EvidenceRef{
		Kind:              EvidenceDocument,
		DocumentVersionID: "doc-001",
		ParsedVersionID:   "parse-001",
		Start:             0,
		End:               24,
		Quote:             "本线路最低保障带宽100Mbit/s。",
	}
}

func validEvidenceSnapshot() EvidenceRef {
	return EvidenceRef{
		Kind:         EvidenceExternalSnapshot,
		SnapshotID:   "snapshot-001",
		RecordID:     "line-001",
		FieldPointer: "/serviceCharacteristic/0/value",
		DisplayValue: "80 Mbit/s",
		ObservedAt:   time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC),
	}
}

func validFactRecord() Fact {
	return Fact{
		Name:         "minimum_bandwidth",
		Value:        ExactQuantity{Value: "100", Unit: "Mbit/s"},
		SemanticKind: "configured_value",
		Evidence:     []EvidenceRef{validEvidenceDocument()},
	}
}

func validCheckResult() CheckResult {
	return CheckResult{
		CheckID:       "line.configured_bandwidth",
		Relation:      RelationCompatible,
		ReasonCode:    "within_bandwidth_band",
		MachineStatus: MachineCompleted,
		Evidence:      []EvidenceRef{validEvidenceDocument()},
		Limitations:   []string{"synthetic source"},
	}
}

func validImpactPlan() ImpactPlan {
	return ImpactPlan{
		SchemaVersion:        ImpactPlanSchema,
		BaseEvaluationID:     "eval-001",
		TargetEvaluationID:   "eval-002",
		Mode:                 ImpactExplain,
		DependencyComplete:   true,
		Fallback:             FallbackNone,
		ChangedInputs:        []string{"external.configured_bandwidth"},
		Checks:               []ImpactCheck{{CheckID: "line.configured_bandwidth", Action: ImpactRecompute, Reasons: []string{"dependent_field_changed"}}},
		BusinessResultSource: "full",
		DataClass:            DataClassSynthetic,
	}
}

func validFrozenInput() FrozenInput {
	return FrozenInput{
		Scope:                  validScope(),
		Pack:                   PackRef{ID: "operator.dedicated_line", Version: "1.0.0"},
		ContractVersion:        "doc-001",
		RulePackVersion:        "5",
		BindingStatus:          BindingConfirmed,
		BindingVersion:         "binding-001",
		SnapshotSetID:          "set-001",
		ExternalObjectID:       "line-001",
		SourceSystem:           "fixture-oss-east",
		ModelConfigurationID:   "model-001",
		PlannerConfigurationID: "planner-001",
		ParserConfigurationID:  "parser-001",
		BusinessAt:             time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC),
		Facts:                  []Fact{validFactRecord()},
	}
}

func documentEvidenceSet(n int) []EvidenceRef {
	out := make([]EvidenceRef, n)
	for i := range out {
		out[i] = validEvidenceDocument()
	}
	return out
}

func TestValidateIDPinned(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"simple", "case-001", ""},
		{"all allowed characters", "A0_.:/-", ""},
		{"maximum length", "a" + strings.Repeat("b", 127), ""},
		{"empty", "", invalidRequest("identifier")},
		{"leading space", " case", invalidRequest("identifier")},
		{"leading dash", "-case", invalidRequest("identifier")},
		{"too long", "a" + strings.Repeat("b", 128), invalidRequest("identifier")},
		{"non ascii", "案件-001", invalidRequest("identifier")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkValidateError(t, ValidateID(tc.value), tc.want)
		})
	}
}

func TestValidateVersionPinned(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"three parts", "1.0.0", ""},
		{"large parts", "18.3.42", ""},
		{"two parts", "1.0", invalidRequest("version")},
		{"four parts", "1.0.0.0", invalidRequest("version")},
		{"prefixed", "v1.0.0", invalidRequest("version")},
		{"trailing space", "1.0.0 ", invalidRequest("version")},
		{"empty", "", invalidRequest("version")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkValidateError(t, ValidateVersion(tc.value), tc.want)
		})
	}
}

func TestValidateQuantityPinned(t *testing.T) {
	tests := []struct {
		name     string
		quantity ExactQuantity
		want     string
	}{
		{"integer", ExactQuantity{Value: "80", Unit: "Mbit/s"}, ""},
		{"decimal", ExactQuantity{Value: "80.5", Unit: "Mbit/s"}, ""},
		{"zero", ExactQuantity{Value: "0", Unit: "Mbit/s"}, ""},
		{"value maximum length", ExactQuantity{Value: strings.Repeat("1", 64), Unit: "Mbit/s"}, ""},
		{"unit maximum length", ExactQuantity{Value: "80", Unit: strings.Repeat("u", 64)}, ""},
		{"empty value", ExactQuantity{Value: "", Unit: "Mbit/s"}, invalidRequest("quantity value")},
		{"trailing dot", ExactQuantity{Value: "80.", Unit: "Mbit/s"}, invalidRequest("quantity value")},
		{"leading dot", ExactQuantity{Value: ".5", Unit: "Mbit/s"}, invalidRequest("quantity value")},
		{"negative", ExactQuantity{Value: "-1", Unit: "Mbit/s"}, invalidRequest("quantity value")},
		{"text", ExactQuantity{Value: "eighty", Unit: "Mbit/s"}, invalidRequest("quantity value")},
		{"value too long", ExactQuantity{Value: strings.Repeat("1", 65), Unit: "Mbit/s"}, invalidRequest("quantity value")},
		{"empty unit", ExactQuantity{Value: "80", Unit: ""}, invalidRequest("quantity unit")},
		{"unit too long", ExactQuantity{Value: "80", Unit: strings.Repeat("u", 65)}, invalidRequest("quantity unit")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkValidateError(t, ValidateQuantity(tc.quantity), tc.want)
		})
	}
}

func TestAuthorizedScopeValidatePinned(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AuthorizedScope)
		want   string
	}{
		{"valid", nil, ""},
		{"grant version maximum length", func(s *AuthorizedScope) { s.GrantVersion = strings.Repeat("g", 128) }, ""},
		{"bad tenant", func(s *AuthorizedScope) { s.TenantID = "bad tenant" }, invalidRequest("authorized scope")},
		{"bad case", func(s *AuthorizedScope) { s.CaseID = "" }, invalidRequest("authorized scope")},
		{"bad actor", func(s *AuthorizedScope) { s.ActorID = "-actor" }, invalidRequest("authorized scope")},
		{"empty grant version", func(s *AuthorizedScope) { s.GrantVersion = "" }, invalidRequest("grant version")},
		{"grant version too long", func(s *AuthorizedScope) { s.GrantVersion = strings.Repeat("g", 129) }, invalidRequest("grant version")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scope := validScope()
			if tc.mutate != nil {
				tc.mutate(&scope)
			}
			checkValidateError(t, scope.Validate(), tc.want)
		})
	}
}

func TestPackRefValidatePinned(t *testing.T) {
	tests := []struct {
		name string
		pack PackRef
		want string
	}{
		{"valid", PackRef{ID: "operator.dedicated_line", Version: "1.0.0"}, ""},
		{"bad id", PackRef{ID: "bad id", Version: "1.0.0"}, invalidRequest("pack reference")},
		{"bad version", PackRef{ID: "operator.dedicated_line", Version: "1.0"}, invalidRequest("pack reference")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkValidateError(t, tc.pack.Validate(), tc.want)
		})
	}
}

func TestFieldValidatePinned(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Field)
		want   string
	}{
		{"valid", nil, ""},
		{"gbit unit", func(f *Field) { f.Unit = "Gbit/s" }, ""},
		{"root pointer", func(f *Field) { f.SourcePointer = "/" }, ""},
		{"pointer maximum length", func(f *Field) { f.SourcePointer = "/" + strings.Repeat("p", 511) }, ""},
		{"value maximum length", func(f *Field) { f.Value = strings.Repeat("7", 64) }, ""},
		{"name maximum length", func(f *Field) { f.Name = "n" + strings.Repeat("a", 127) }, ""},
		{"bad name", func(f *Field) { f.Name = "configured bandwidth" }, invalidRequest("field name")},
		{"bad value", func(f *Field) { f.Value = "eighty" }, invalidRequest("field value")},
		{"empty value", func(f *Field) { f.Value = "" }, invalidRequest("field value")},
		{"value too long", func(f *Field) { f.Value = strings.Repeat("7", 65) }, invalidRequest("field value")},
		{"bad unit", func(f *Field) { f.Unit = "Kbit/s" }, invalidRequest("field unit")},
		{"empty unit", func(f *Field) { f.Unit = "" }, invalidRequest("field unit")},
		{"empty pointer", func(f *Field) { f.SourcePointer = "" }, invalidRequest("field pointer")},
		{"pointer without slash", func(f *Field) { f.SourcePointer = "serviceCharacteristic/0/value" }, invalidRequest("field pointer")},
		{"pointer too long", func(f *Field) { f.SourcePointer = "/" + strings.Repeat("p", 512) }, invalidRequest("field pointer")},
		{"bad semantic kind", func(f *Field) { f.SemanticKind = "derived_value" }, invalidRequest("field semantic kind")},
		{"empty semantic kind", func(f *Field) { f.SemanticKind = "" }, invalidRequest("field semantic kind")},
		{"name before value", func(f *Field) { f.Name = "bad name"; f.Value = "eighty" }, invalidRequest("field name")},
		{"value before unit", func(f *Field) { f.Value = "eighty"; f.Unit = "Kbit/s" }, invalidRequest("field value")},
		{"unit before pointer", func(f *Field) { f.Unit = "Kbit/s"; f.SourcePointer = "relative" }, invalidRequest("field unit")},
		{"pointer before semantic kind", func(f *Field) { f.SourcePointer = "relative"; f.SemanticKind = "" }, invalidRequest("field pointer")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			field := validField()
			if tc.mutate != nil {
				tc.mutate(&field)
			}
			checkValidateError(t, field.Validate(), tc.want)
		})
	}
}

func TestObservationValidatePinned(t *testing.T) {
	longPayload := "fixture://" + strings.Repeat("a", 520)
	maximumPayload := "fixture://" + strings.Repeat("a", 502)
	validRecordVersion := "record-v1"
	tests := []struct {
		name   string
		mutate func(*Observation)
		scope  func() AuthorizedScope
		want   string
	}{
		{"valid", nil, nil, ""},
		{"valid record version", func(o *Observation) { o.RecordVersion = &validRecordVersion }, nil, ""},
		{"record version maximum length", func(o *Observation) { o.RecordVersion = ptr(strings.Repeat("v", 128)) }, nil, ""},
		{"payload maximum length", func(o *Observation) { o.PayloadRef = &maximumPayload }, nil, ""},
		{"stale without payload or fields", func(o *Observation) {
			o.Availability = AvailabilityStale
			o.ReasonCode = "source_stale"
			o.PayloadRef = nil
			o.Fields = nil
		}, nil, ""},
		{"unavailable without payload or fields", func(o *Observation) {
			o.Availability = AvailabilityUnavailable
			o.ReasonCode = "source_unavailable"
			o.PayloadRef = nil
			o.Fields = nil
		}, nil, ""},
		{"thirty two fields", func(o *Observation) {
			o.Fields = make([]Field, 32)
			for i := range o.Fields {
				o.Fields[i] = validField()
			}
		}, nil, ""},
		{"bad schema", func(o *Observation) { o.SchemaVersion = "external-observation/v2" }, nil, invalidRequest("observation schema")},
		{"bad scope tenant", func(o *Observation) { o.TenantID = "tenant-b" }, func() AuthorizedScope {
			scope := validScope()
			scope.TenantID = "bad tenant"
			return scope
		}, invalidRequest("authorized scope")},
		{"bad scope grant version", func(o *Observation) {}, func() AuthorizedScope {
			scope := validScope()
			scope.GrantVersion = ""
			return scope
		}, invalidRequest("grant version")},
		{"tenant mismatch", func(o *Observation) { o.TenantID = "tenant-b" }, nil, unauthorizedText},
		{"case mismatch", func(o *Observation) { o.CaseID = "case-002" }, nil, unauthorizedText},
		{"bad snapshot id", func(o *Observation) { o.SnapshotID = "bad id" }, nil, invalidRequest("observation identity")},
		{"bad connector id", func(o *Observation) { o.ConnectorID = "" }, nil, invalidRequest("observation identity")},
		{"bad source system", func(o *Observation) { o.SourceSystem = "bad source" }, nil, invalidRequest("observation identity")},
		{"bad object binding version", func(o *Observation) { o.ObjectBindingVersion = "-binding" }, nil, invalidRequest("observation identity")},
		{"bad external object id", func(o *Observation) { o.ExternalObjectID = "line 001" }, nil, invalidRequest("observation identity")},
		{"bad record id", func(o *Observation) { o.RecordID = "" }, nil, invalidRequest("observation identity")},
		{"empty record version", func(o *Observation) { o.RecordVersion = ptr("") }, nil, invalidRequest("record version")},
		{"record version too long", func(o *Observation) { o.RecordVersion = ptr(strings.Repeat("v", 129)) }, nil, invalidRequest("record version")},
		{"zero observed at", func(o *Observation) { o.ObservedAt = time.Time{} }, nil, invalidRequest("observation time")},
		{"zero effective from", func(o *Observation) { o.EffectiveFrom = time.Time{} }, nil, invalidRequest("observation time")},
		{"zero effective until", func(o *Observation) { o.EffectiveUntil = time.Time{} }, nil, invalidRequest("observation time")},
		{"empty effective interval", func(o *Observation) { o.EffectiveUntil = o.EffectiveFrom }, nil, invalidRequest("effective interval")},
		{"reversed effective interval", func(o *Observation) { o.EffectiveFrom = o.EffectiveUntil.Add(time.Hour) }, nil, invalidRequest("effective interval")},
		{"bad availability", func(o *Observation) { o.Availability = Availability("unknown") }, nil, invalidRequest("availability")},
		{"empty availability", func(o *Observation) { o.Availability = "" }, nil, invalidRequest("availability")},
		{"bad reason code", func(o *Observation) { o.ReasonCode = "read succeeded" }, nil, invalidRequest("reason code")},
		{"payload reference too long", func(o *Observation) { o.PayloadRef = &longPayload }, nil, invalidRequest("payload reference")},
		{"available without payload", func(o *Observation) { o.PayloadRef = nil }, nil, invalidRequest("available observation requires a payload")},
		{"available with empty payload", func(o *Observation) { o.PayloadRef = ptr("") }, nil, invalidRequest("available observation requires a payload")},
		{"available without fields", func(o *Observation) { o.Fields = nil }, nil, invalidRequest("available observation requires fields")},
		{"too many fields", func(o *Observation) {
			o.Fields = make([]Field, 33)
			for i := range o.Fields {
				o.Fields[i] = validField()
			}
		}, nil, invalidRequest("too many fields")},
		{"bad field", func(o *Observation) {
			field := validField()
			field.Name = "bad name"
			o.Fields = []Field{field}
		}, nil, invalidRequest("field name")},
		{"bad data class", func(o *Observation) { o.DataClass = "production" }, nil, invalidRequest("data class")},
		{"schema before scope", func(o *Observation) {
			o.SchemaVersion = "external-observation/v2"
			o.TenantID = "tenant-b"
		}, func() AuthorizedScope {
			scope := validScope()
			scope.TenantID = "bad tenant"
			return scope
		}, invalidRequest("observation schema")},
		{"scope before tenant mismatch", func(o *Observation) { o.TenantID = "tenant-b" }, func() AuthorizedScope {
			scope := validScope()
			scope.GrantVersion = ""
			return scope
		}, invalidRequest("grant version")},
		{"tenant mismatch before identity", func(o *Observation) {
			o.TenantID = "tenant-b"
			o.SnapshotID = "bad id"
		}, nil, unauthorizedText},
		{"identity before record version", func(o *Observation) {
			o.SnapshotID = "bad id"
			o.RecordVersion = ptr("")
		}, nil, invalidRequest("observation identity")},
		{"record version before time", func(o *Observation) {
			o.RecordVersion = ptr("")
			o.ObservedAt = time.Time{}
		}, nil, invalidRequest("record version")},
		{"time before interval", func(o *Observation) {
			o.ObservedAt = time.Time{}
			o.EffectiveUntil = o.EffectiveFrom
		}, nil, invalidRequest("observation time")},
		{"interval before availability", func(o *Observation) {
			o.EffectiveUntil = o.EffectiveFrom
			o.Availability = Availability("unknown")
		}, nil, invalidRequest("effective interval")},
		{"availability before reason", func(o *Observation) {
			o.Availability = Availability("unknown")
			o.ReasonCode = "bad reason"
		}, nil, invalidRequest("availability")},
		{"reason before payload length", func(o *Observation) {
			o.ReasonCode = "bad reason"
			o.PayloadRef = &longPayload
		}, nil, invalidRequest("reason code")},
		{"payload length before available payload", func(o *Observation) {
			o.PayloadRef = &longPayload
			o.Fields = nil
		}, nil, invalidRequest("payload reference")},
		{"available payload before available fields", func(o *Observation) { o.PayloadRef = nil; o.Fields = nil }, nil, invalidRequest("available observation requires a payload")},
		{"field limit before field content", func(o *Observation) {
			o.Fields = make([]Field, 33)
			for i := range o.Fields {
				o.Fields[i] = validField()
			}
			o.Fields[0].Name = "bad name"
		}, nil, invalidRequest("too many fields")},
		{"field content before data class", func(o *Observation) {
			field := validField()
			field.Unit = "Kbit/s"
			o.Fields = []Field{field}
			o.DataClass = "production"
		}, nil, invalidRequest("field unit")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			observation := validObservation(validScope())
			if tc.mutate != nil {
				tc.mutate(&observation)
			}
			scope := validScope()
			if tc.scope != nil {
				scope = tc.scope()
			}
			checkValidateError(t, observation.Validate(scope), tc.want)
		})
	}
}

func TestEvidenceRefValidatePinned(t *testing.T) {
	tests := []struct {
		name     string
		evidence func() EvidenceRef
		want     string
	}{
		{"valid document", validEvidenceDocument, ""},
		{"valid snapshot", validEvidenceSnapshot, ""},
		{"document range boundary", func() EvidenceRef {
			e := validEvidenceDocument()
			e.Start = 0
			e.End = 1
			return e
		}, ""},
		{"document quote maximum length", func() EvidenceRef {
			e := validEvidenceDocument()
			e.Quote = strings.Repeat("q", 4096)
			return e
		}, ""},
		{"document bad document version", func() EvidenceRef { e := validEvidenceDocument(); e.DocumentVersionID = "bad id"; return e }, invalidRequest("document evidence identity")},
		{"document bad parsed version", func() EvidenceRef { e := validEvidenceDocument(); e.ParsedVersionID = ""; return e }, invalidRequest("document evidence identity")},
		{"document negative start", func() EvidenceRef { e := validEvidenceDocument(); e.Start = -1; return e }, invalidRequest("document evidence range")},
		{"document empty range", func() EvidenceRef { e := validEvidenceDocument(); e.End = e.Start; return e }, invalidRequest("document evidence range")},
		{"document reversed range", func() EvidenceRef { e := validEvidenceDocument(); e.End = e.Start - 1; return e }, invalidRequest("document evidence range")},
		{"document empty quote", func() EvidenceRef { e := validEvidenceDocument(); e.Quote = ""; return e }, invalidRequest("document evidence quote")},
		{"document quote too long", func() EvidenceRef { e := validEvidenceDocument(); e.Quote = strings.Repeat("q", 4097); return e }, invalidRequest("document evidence quote")},
		{"document mixed snapshot id", func() EvidenceRef { e := validEvidenceDocument(); e.SnapshotID = "snapshot-001"; return e }, invalidRequest("mixed document evidence")},
		{"document mixed record id", func() EvidenceRef { e := validEvidenceDocument(); e.RecordID = "line-001"; return e }, invalidRequest("mixed document evidence")},
		{"document mixed field pointer", func() EvidenceRef { e := validEvidenceDocument(); e.FieldPointer = "/p"; return e }, invalidRequest("mixed document evidence")},
		{"document mixed display value", func() EvidenceRef { e := validEvidenceDocument(); e.DisplayValue = "80"; return e }, invalidRequest("mixed document evidence")},
		{"document mixed observed at", func() EvidenceRef { e := validEvidenceDocument(); e.ObservedAt = time.Now(); return e }, invalidRequest("mixed document evidence")},
		{"document identity before range", func() EvidenceRef {
			e := validEvidenceDocument()
			e.DocumentVersionID = "bad id"
			e.Start = -1
			return e
		}, invalidRequest("document evidence identity")},
		{"document range before quote", func() EvidenceRef {
			e := validEvidenceDocument()
			e.End = e.Start
			e.Quote = ""
			return e
		}, invalidRequest("document evidence range")},
		{"document quote before mixed", func() EvidenceRef {
			e := validEvidenceDocument()
			e.Quote = ""
			e.SnapshotID = "snapshot-001"
			return e
		}, invalidRequest("document evidence quote")},
		{"snapshot root pointer", func() EvidenceRef {
			e := validEvidenceSnapshot()
			e.FieldPointer = "/"
			return e
		}, ""},
		{"snapshot pointer maximum length", func() EvidenceRef {
			e := validEvidenceSnapshot()
			e.FieldPointer = "/" + strings.Repeat("p", 511)
			return e
		}, ""},
		{"snapshot display maximum length", func() EvidenceRef {
			e := validEvidenceSnapshot()
			e.DisplayValue = strings.Repeat("d", 256)
			return e
		}, ""},
		{"snapshot bad snapshot id", func() EvidenceRef { e := validEvidenceSnapshot(); e.SnapshotID = ""; return e }, invalidRequest("snapshot evidence identity")},
		{"snapshot bad record id", func() EvidenceRef { e := validEvidenceSnapshot(); e.RecordID = "line 001"; return e }, invalidRequest("snapshot evidence identity")},
		{"snapshot empty pointer", func() EvidenceRef { e := validEvidenceSnapshot(); e.FieldPointer = ""; return e }, invalidRequest("snapshot evidence pointer")},
		{"snapshot pointer without slash", func() EvidenceRef { e := validEvidenceSnapshot(); e.FieldPointer = "p"; return e }, invalidRequest("snapshot evidence pointer")},
		{"snapshot pointer too long", func() EvidenceRef {
			e := validEvidenceSnapshot()
			e.FieldPointer = "/" + strings.Repeat("p", 512)
			return e
		}, invalidRequest("snapshot evidence pointer")},
		{"snapshot empty display value", func() EvidenceRef { e := validEvidenceSnapshot(); e.DisplayValue = ""; return e }, invalidRequest("snapshot evidence value")},
		{"snapshot display value too long", func() EvidenceRef { e := validEvidenceSnapshot(); e.DisplayValue = strings.Repeat("d", 257); return e }, invalidRequest("snapshot evidence value")},
		{"snapshot zero observed at", func() EvidenceRef { e := validEvidenceSnapshot(); e.ObservedAt = time.Time{}; return e }, invalidRequest("snapshot evidence time")},
		{"snapshot mixed document version", func() EvidenceRef { e := validEvidenceSnapshot(); e.DocumentVersionID = "doc-001"; return e }, invalidRequest("mixed snapshot evidence")},
		{"snapshot mixed parsed version", func() EvidenceRef { e := validEvidenceSnapshot(); e.ParsedVersionID = "parse-001"; return e }, invalidRequest("mixed snapshot evidence")},
		{"snapshot mixed start", func() EvidenceRef { e := validEvidenceSnapshot(); e.Start = 1; return e }, invalidRequest("mixed snapshot evidence")},
		{"snapshot mixed end", func() EvidenceRef { e := validEvidenceSnapshot(); e.End = 1; return e }, invalidRequest("mixed snapshot evidence")},
		{"snapshot mixed quote", func() EvidenceRef { e := validEvidenceSnapshot(); e.Quote = "q"; return e }, invalidRequest("mixed snapshot evidence")},
		{"snapshot identity before pointer", func() EvidenceRef {
			e := validEvidenceSnapshot()
			e.SnapshotID = ""
			e.FieldPointer = "p"
			return e
		}, invalidRequest("snapshot evidence identity")},
		{"snapshot pointer before value", func() EvidenceRef {
			e := validEvidenceSnapshot()
			e.FieldPointer = "p"
			e.DisplayValue = ""
			return e
		}, invalidRequest("snapshot evidence pointer")},
		{"snapshot value before time", func() EvidenceRef {
			e := validEvidenceSnapshot()
			e.DisplayValue = ""
			e.ObservedAt = time.Time{}
			return e
		}, invalidRequest("snapshot evidence value")},
		{"snapshot time before mixed", func() EvidenceRef {
			e := validEvidenceSnapshot()
			e.ObservedAt = time.Time{}
			e.Quote = "q"
			return e
		}, invalidRequest("snapshot evidence time")},
		{"empty kind", func() EvidenceRef { return EvidenceRef{} }, invalidRequest("evidence kind")},
		{"unknown kind", func() EvidenceRef { e := validEvidenceDocument(); e.Kind = "api_response"; return e }, invalidRequest("evidence kind")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkValidateError(t, tc.evidence().Validate(), tc.want)
		})
	}
}

func TestCheckResultValidatePinned(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CheckResult)
		want   string
	}{
		{"valid", nil, ""},
		{"sixteen evidence", func(r *CheckResult) { r.Evidence = documentEvidenceSet(16) }, ""},
		{"sixteen limitations", func(r *CheckResult) {
			r.Limitations = make([]string, 16)
			for i := range r.Limitations {
				r.Limitations[i] = "limitation"
			}
		}, ""},
		{"limitation maximum length", func(r *CheckResult) { r.Limitations = []string{strings.Repeat("l", 256)} }, ""},
		{"bad check id", func(r *CheckResult) { r.CheckID = "bad id" }, invalidRequest("check id")},
		{"bad relation", func(r *CheckResult) { r.Relation = Relation("approved") }, invalidRequest("relation")},
		{"empty relation", func(r *CheckResult) { r.Relation = "" }, invalidRequest("relation")},
		{"bad reason code", func(r *CheckResult) { r.ReasonCode = "within band" }, invalidRequest("reason code")},
		{"bad machine status", func(r *CheckResult) { r.MachineStatus = MachineStatus("ok") }, invalidRequest("machine status")},
		{"empty machine status", func(r *CheckResult) { r.MachineStatus = "" }, invalidRequest("machine status")},
		{"too much evidence", func(r *CheckResult) { r.Evidence = documentEvidenceSet(17) }, invalidRequest("too much check evidence")},
		{"bad evidence", func(r *CheckResult) { r.Evidence = []EvidenceRef{{Kind: EvidenceKind("api")}} }, invalidRequest("evidence kind")},
		{"too many limitations", func(r *CheckResult) {
			r.Limitations = make([]string, 17)
			for i := range r.Limitations {
				r.Limitations[i] = "limitation"
			}
		}, invalidRequest("too many limitations")},
		{"empty limitation", func(r *CheckResult) { r.Limitations = []string{""} }, invalidRequest("limitation")},
		{"limitation too long", func(r *CheckResult) { r.Limitations = []string{strings.Repeat("l", 257)} }, invalidRequest("limitation")},
		{"check id before relation", func(r *CheckResult) { r.CheckID = "bad id"; r.Relation = "" }, invalidRequest("check id")},
		{"relation before reason", func(r *CheckResult) { r.Relation = ""; r.ReasonCode = "bad reason" }, invalidRequest("relation")},
		{"reason before status", func(r *CheckResult) { r.ReasonCode = "bad reason"; r.MachineStatus = "" }, invalidRequest("reason code")},
		{"status before evidence count", func(r *CheckResult) { r.MachineStatus = ""; r.Evidence = documentEvidenceSet(17) }, invalidRequest("machine status")},
		{"evidence count before limitations", func(r *CheckResult) {
			r.Evidence = documentEvidenceSet(17)
			r.Limitations = make([]string, 17)
		}, invalidRequest("too much check evidence")},
		{"bad evidence before limitations", func(r *CheckResult) {
			r.Evidence = []EvidenceRef{{Kind: EvidenceKind("api")}}
			r.Limitations = []string{""}
		}, invalidRequest("evidence kind")},
		{"limitation count before content", func(r *CheckResult) {
			r.Limitations = make([]string, 17)
			r.Limitations[0] = ""
		}, invalidRequest("too many limitations")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := validCheckResult()
			if tc.mutate != nil {
				tc.mutate(&result)
			}
			checkValidateError(t, result.Validate(), tc.want)
		})
	}
}

func TestImpactPlanValidatePinned(t *testing.T) {
	checks := func(n int) []ImpactCheck {
		out := make([]ImpactCheck, n)
		for i := range out {
			out[i] = ImpactCheck{CheckID: "check-" + string(rune('a'+i%26)), Action: ImpactRecompute, Reasons: []string{"dependent_field_changed"}}
		}
		return out
	}
	reasons := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "reason-" + string(rune('a'+i%26))
		}
		return out
	}
	tests := []struct {
		name   string
		mutate func(*ImpactPlan)
		want   string
	}{
		{"valid explain", nil, ""},
		{"valid shadow with fallback", func(p *ImpactPlan) {
			p.Mode = ImpactShadow
			p.DependencyComplete = false
			p.Fallback = FallbackFull
			p.FallbackReason = "source_changed"
		}, ""},
		{"valid deny fallback", func(p *ImpactPlan) {
			p.DependencyComplete = false
			p.Fallback = FallbackDeny
			p.FallbackReason = "rules_changed"
		}, ""},
		{"valid maximum changed inputs", func(p *ImpactPlan) {
			p.ChangedInputs = nil
			for i := 0; i < 128; i++ {
				p.ChangedInputs = append(p.ChangedInputs, "external.input-"+string(rune('a'+i%26)))
			}
		}, ""},
		{"valid maximum checks", func(p *ImpactPlan) {
			p.Checks = nil
			for i := 0; i < 128; i++ {
				p.Checks = append(p.Checks, ImpactCheck{CheckID: "check-" + string(rune('a'+i%26)), Action: ImpactDeny, Reasons: []string{"reason-a"}})
			}
		}, ""},
		{"valid maximum reasons", func(p *ImpactPlan) {
			p.Checks[0].Reasons = nil
			for i := 0; i < 32; i++ {
				p.Checks[0].Reasons = append(p.Checks[0].Reasons, "reason-"+string(rune('a'+i%26)))
			}
		}, ""},
		{"bad schema", func(p *ImpactPlan) { p.SchemaVersion = "impact-plan/v2" }, invalidRequest("impact plan schema")},
		{"bad base evaluation", func(p *ImpactPlan) { p.BaseEvaluationID = "bad id" }, invalidRequest("impact plan evaluation")},
		{"bad target evaluation", func(p *ImpactPlan) { p.TargetEvaluationID = "" }, invalidRequest("impact plan evaluation")},
		{"bad mode", func(p *ImpactPlan) { p.Mode = ImpactMode("reuse") }, invalidRequest("impact plan mode")},
		{"empty mode", func(p *ImpactPlan) { p.Mode = "" }, invalidRequest("impact plan mode")},
		{"bad fallback", func(p *ImpactPlan) { p.Fallback = Fallback("partial") }, invalidRequest("impact plan mode")},
		{"empty fallback", func(p *ImpactPlan) { p.Fallback = "" }, invalidRequest("impact plan mode")},
		{"complete with fallback", func(p *ImpactPlan) { p.Fallback = FallbackFull; p.FallbackReason = "source_changed" }, invalidRequest("complete dependencies require no fallback")},
		{"incomplete without fallback", func(p *ImpactPlan) { p.DependencyComplete = false }, invalidRequest("incomplete dependencies require a fallback")},
		{"unused fallback reason", func(p *ImpactPlan) { p.FallbackReason = "source_changed" }, invalidRequest("unused fallback reason")},
		{"bad fallback reason", func(p *ImpactPlan) {
			p.DependencyComplete = false
			p.Fallback = FallbackFull
			p.FallbackReason = "source changed"
		}, invalidRequest("fallback reason")},
		{"missing fallback reason", func(p *ImpactPlan) { p.DependencyComplete = false; p.Fallback = FallbackFull }, invalidRequest("fallback reason")},
		{"too many changed inputs", func(p *ImpactPlan) {
			for i := 0; i < 128; i++ {
				p.ChangedInputs = append(p.ChangedInputs, "external.input-"+string(rune('a'+i%26)))
			}
		}, invalidRequest("too many changed inputs")},
		{"bad changed input", func(p *ImpactPlan) { p.ChangedInputs = []string{"external input"} }, invalidRequest("changed input")},
		{"no checks", func(p *ImpactPlan) { p.Checks = nil }, invalidRequest("impact checks")},
		{"too many checks", func(p *ImpactPlan) { p.Checks = checks(129) }, invalidRequest("impact checks")},
		{"bad check id", func(p *ImpactPlan) { p.Checks[0].CheckID = "bad id" }, invalidRequest("impact check")},
		{"bad check action", func(p *ImpactPlan) { p.Checks[0].Action = ImpactAction("approve") }, invalidRequest("impact check")},
		{"no reasons", func(p *ImpactPlan) { p.Checks[0].Reasons = nil }, invalidRequest("impact reasons")},
		{"too many reasons", func(p *ImpactPlan) { p.Checks[0].Reasons = reasons(33) }, invalidRequest("impact reasons")},
		{"bad reason", func(p *ImpactPlan) { p.Checks[0].Reasons = []string{"bad reason"} }, invalidRequest("impact reason")},
		{"bad business result source", func(p *ImpactPlan) { p.BusinessResultSource = "shadow" }, invalidRequest("business result source")},
		{"empty business result source", func(p *ImpactPlan) { p.BusinessResultSource = "" }, invalidRequest("business result source")},
		{"bad data class", func(p *ImpactPlan) { p.DataClass = "production" }, invalidRequest("data class")},
		{"schema before evaluation", func(p *ImpactPlan) { p.SchemaVersion = ""; p.BaseEvaluationID = "bad id" }, invalidRequest("impact plan schema")},
		{"evaluation before mode", func(p *ImpactPlan) { p.BaseEvaluationID = "bad id"; p.Mode = "" }, invalidRequest("impact plan evaluation")},
		{"mode before fallback consistency", func(p *ImpactPlan) { p.Mode = ""; p.Fallback = FallbackFull }, invalidRequest("impact plan mode")},
		{"consistency before fallback reason", func(p *ImpactPlan) { p.Fallback = FallbackFull; p.FallbackReason = "bad reason" }, invalidRequest("complete dependencies require no fallback")},
		{"fallback reason before changed inputs", func(p *ImpactPlan) {
			p.DependencyComplete = false
			p.Fallback = FallbackFull
			p.FallbackReason = "bad reason"
			p.ChangedInputs = []string{"bad input"}
		}, invalidRequest("fallback reason")},
		{"changed input count before content", func(p *ImpactPlan) {
			p.ChangedInputs = make([]string, 129)
			p.ChangedInputs[0] = "bad input"
		}, invalidRequest("too many changed inputs")},
		{"changed input before checks", func(p *ImpactPlan) { p.ChangedInputs = []string{"bad input"}; p.Checks = nil }, invalidRequest("changed input")},
		{"check count before checks", func(p *ImpactPlan) { p.Checks = checks(129); p.Checks[0].CheckID = "bad id" }, invalidRequest("impact checks")},
		{"check before reasons", func(p *ImpactPlan) { p.Checks[0].Action = ""; p.Checks[0].Reasons = nil }, invalidRequest("impact check")},
		{"reasons before reason", func(p *ImpactPlan) { p.Checks[0].Reasons = []string{""} }, invalidRequest("impact reason")},
		{"checks before source", func(p *ImpactPlan) { p.Checks = nil; p.BusinessResultSource = "shadow" }, invalidRequest("impact checks")},
		{"source before data class", func(p *ImpactPlan) { p.BusinessResultSource = "shadow"; p.DataClass = "production" }, invalidRequest("business result source")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := validImpactPlan()
			if tc.mutate != nil {
				tc.mutate(&plan)
			}
			checkValidateError(t, plan.Validate(), tc.want)
		})
	}
}

func TestFrozenInputValidatePinned(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*FrozenInput)
		want   string
	}{
		{"valid", nil, ""},
		{"no facts", func(f *FrozenInput) { f.Facts = nil }, ""},
		{"missing binding without object", func(f *FrozenInput) {
			f.BindingStatus = BindingMissing
			f.BindingVersion = ""
			f.ExternalObjectID = ""
			f.SourceSystem = ""
		}, ""},
		{"ambiguous binding without object", func(f *FrozenInput) {
			f.BindingStatus = BindingAmbiguous
			f.BindingVersion = ""
			f.ExternalObjectID = ""
			f.SourceSystem = ""
		}, ""},
		{"unconfirmed binding with valid optional identities", func(f *FrozenInput) { f.BindingStatus = BindingMissing }, ""},
		{"rule pack version maximum length", func(f *FrozenInput) { f.RulePackVersion = strings.Repeat("1", 128) }, ""},
		{"sixty four facts", func(f *FrozenInput) {
			f.Facts = nil
			for i := 0; i < 64; i++ {
				f.Facts = append(f.Facts, Fact{Name: "fact-" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Value: ExactQuantity{Value: "1", Unit: "Mbit/s"}})
			}
		}, ""},
		{"bad scope", func(f *FrozenInput) { f.Scope.TenantID = "bad tenant" }, invalidRequest("authorized scope")},
		{"bad grant version", func(f *FrozenInput) { f.Scope.GrantVersion = "" }, invalidRequest("grant version")},
		{"bad pack id", func(f *FrozenInput) { f.Pack.ID = "bad pack" }, invalidRequest("pack reference")},
		{"bad pack version", func(f *FrozenInput) { f.Pack.Version = "1.0" }, invalidRequest("pack reference")},
		{"bad contract version", func(f *FrozenInput) { f.ContractVersion = "bad id" }, invalidRequest("frozen input identity")},
		{"bad snapshot set", func(f *FrozenInput) { f.SnapshotSetID = "" }, invalidRequest("frozen input identity")},
		{"bad model configuration", func(f *FrozenInput) { f.ModelConfigurationID = "bad model" }, invalidRequest("frozen input identity")},
		{"bad planner configuration", func(f *FrozenInput) { f.PlannerConfigurationID = "" }, invalidRequest("frozen input identity")},
		{"bad parser configuration", func(f *FrozenInput) { f.ParserConfigurationID = "-parser" }, invalidRequest("frozen input identity")},
		{"bad binding status", func(f *FrozenInput) { f.BindingStatus = BindingStatus("guessed") }, invalidRequest("binding status")},
		{"empty binding status", func(f *FrozenInput) { f.BindingStatus = "" }, invalidRequest("binding status")},
		{"confirmed without binding version", func(f *FrozenInput) { f.BindingVersion = "" }, invalidRequest("confirmed binding")},
		{"confirmed without object", func(f *FrozenInput) { f.ExternalObjectID = "" }, invalidRequest("confirmed binding")},
		{"confirmed without source system", func(f *FrozenInput) { f.SourceSystem = "" }, invalidRequest("confirmed binding")},
		{"missing with bad binding version", func(f *FrozenInput) { f.BindingStatus = BindingMissing; f.BindingVersion = "bad id" }, invalidRequest("unconfirmed binding")},
		{"missing with bad object", func(f *FrozenInput) { f.BindingStatus = BindingMissing; f.ExternalObjectID = "line 001" }, invalidRequest("unconfirmed binding")},
		{"missing with bad source system", func(f *FrozenInput) { f.BindingStatus = BindingMissing; f.SourceSystem = "bad source" }, invalidRequest("unconfirmed binding")},
		{"empty rule pack version", func(f *FrozenInput) { f.RulePackVersion = "" }, invalidRequest("rule pack version")},
		{"rule pack version too long", func(f *FrozenInput) { f.RulePackVersion = strings.Repeat("1", 129) }, invalidRequest("rule pack version")},
		{"zero business time", func(f *FrozenInput) { f.BusinessAt = time.Time{} }, invalidRequest("business time")},
		{"too many facts", func(f *FrozenInput) {
			f.Facts = nil
			for i := 0; i < 65; i++ {
				f.Facts = append(f.Facts, Fact{Name: "fact-" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Value: ExactQuantity{Value: "1", Unit: "Mbit/s"}})
			}
		}, invalidRequest("too many facts")},
		{"duplicate fact", func(f *FrozenInput) { f.Facts = append(f.Facts, validFactRecord()) }, invalidRequest("duplicate fact")},
		{"bad fact name", func(f *FrozenInput) { f.Facts[0].Name = "bad name" }, invalidRequest("fact name")},
		{"bad fact value", func(f *FrozenInput) { f.Facts[0].Value.Value = "eighty" }, invalidRequest("quantity value")},
		{"bad fact unit", func(f *FrozenInput) { f.Facts[0].Value.Unit = "" }, invalidRequest("quantity unit")},
		{"sixteen fact evidence", func(f *FrozenInput) { f.Facts[0].Evidence = documentEvidenceSet(16) }, ""},
		{"too much fact evidence", func(f *FrozenInput) { f.Facts[0].Evidence = documentEvidenceSet(17) }, invalidRequest("too much fact evidence")},
		{"bad fact evidence", func(f *FrozenInput) { f.Facts[0].Evidence = []EvidenceRef{{Kind: EvidenceKind("api")}} }, invalidRequest("evidence kind")},
		{"scope before pack", func(f *FrozenInput) { f.Scope.TenantID = "bad tenant"; f.Pack.ID = "bad pack" }, invalidRequest("authorized scope")},
		{"pack before identity", func(f *FrozenInput) { f.Pack.ID = "bad pack"; f.ContractVersion = "bad id" }, invalidRequest("pack reference")},
		{"identity before binding status", func(f *FrozenInput) { f.ContractVersion = "bad id"; f.BindingStatus = "" }, invalidRequest("frozen input identity")},
		{"binding status before binding identity", func(f *FrozenInput) { f.BindingStatus = BindingStatus("guessed"); f.BindingVersion = "" }, invalidRequest("binding status")},
		{"confirmed binding before rule pack", func(f *FrozenInput) { f.ExternalObjectID = ""; f.RulePackVersion = "" }, invalidRequest("confirmed binding")},
		{"unconfirmed binding before rule pack", func(f *FrozenInput) {
			f.BindingStatus = BindingMissing
			f.BindingVersion = "bad id"
			f.RulePackVersion = ""
		}, invalidRequest("unconfirmed binding")},
		{"rule pack before business time", func(f *FrozenInput) { f.RulePackVersion = ""; f.BusinessAt = time.Time{} }, invalidRequest("rule pack version")},
		{"business time before facts", func(f *FrozenInput) { f.BusinessAt = time.Time{}; f.Facts = append(f.Facts, validFactRecord()) }, invalidRequest("business time")},
		{"fact count before duplicate", func(f *FrozenInput) {
			f.Facts = nil
			for i := 0; i < 65; i++ {
				f.Facts = append(f.Facts, Fact{Name: "fact-" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Value: ExactQuantity{Value: "1", Unit: "Mbit/s"}})
			}
			f.Facts[64] = f.Facts[0]
		}, invalidRequest("too many facts")},
		{"duplicate before fact content", func(f *FrozenInput) {
			bad := validFactRecord()
			bad.Name = "bad name"
			f.Facts = []Fact{bad, bad}
		}, invalidRequest("fact name")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := validFrozenInput()
			if tc.mutate != nil {
				tc.mutate(&input)
			}
			checkValidateError(t, input.Validate(), tc.want)
		})
	}
}

func TestValidateDependencySetPinned(t *testing.T) {
	items := func(n int, kind DependencyKind) []Dependency {
		out := make([]Dependency, n)
		for i := range out {
			out[i] = Dependency{Kind: kind, Ref: "ref-001", Version: "1.0.0", Present: true}
		}
		return out
	}
	tests := []struct {
		name string
		set  DependencySet
		want string
	}{
		{"empty", DependencySet{}, ""},
		{"all kinds", DependencySet{Items: []Dependency{
			{Kind: DependencyField, Ref: "field-1"},
			{Kind: DependencyRecordSet, Ref: "record-set-1"},
			{Kind: DependencyMapping, Ref: "mapping-1"},
			{Kind: DependencyRulePack, Ref: "rule-pack-1"},
			{Kind: DependencyCapabilityPack, Ref: "capability-pack-1"},
			{Kind: DependencyDocument, Ref: "document-1"},
			{Kind: DependencyParser, Ref: "parser-1"},
			{Kind: DependencyModel, Ref: "model-1"},
			{Kind: DependencyPolicy, Ref: "policy-1"},
		}}, ""},
		{"maximum items", DependencySet{Items: items(256, DependencyField)}, ""},
		{"too many dependencies", DependencySet{Items: items(257, DependencyField)}, invalidRequest("too many dependencies")},
		{"bad kind", DependencySet{Items: []Dependency{{Kind: DependencyKind("database"), Ref: "ref-001"}}}, invalidRequest("dependency kind")},
		{"empty kind", DependencySet{Items: []Dependency{{Kind: "", Ref: "ref-001"}}}, invalidRequest("dependency kind")},
		{"bad reference", DependencySet{Items: []Dependency{{Kind: DependencyField, Ref: "bad ref"}}}, invalidRequest("dependency reference")},
		{"count before kind", DependencySet{Items: items(257, DependencyKind("database"))}, invalidRequest("too many dependencies")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkValidateError(t, ValidateDependencySet(tc.set), tc.want)
		})
	}
}
