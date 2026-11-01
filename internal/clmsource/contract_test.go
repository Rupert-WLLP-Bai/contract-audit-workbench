package clmsource

import "testing"

const digestA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const digestB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestCapabilityDistinguishesMockAndRealDeclarations(t *testing.T) {
	mock := Capability{SourceID: "fixture.procurement", Kind: SourceMock, Authority: AuthorityExternal, ReadOnly: true, Authentication: AuthFixture, CanReadContract: true, VersionOrdering: VersionOpaque}
	if err := mock.Validate(); err != nil {
		t.Fatalf("valid mock declaration: %v", err)
	}
	real := mock
	real.SourceID = "procurement.prod"
	real.Kind = SourceReal
	real.Authentication = AuthMutualTLS
	if err := real.Validate(); err != nil {
		t.Fatalf("valid real configuration declaration: %v", err)
	}
	// Validate describes configuration only. It performs no network or auth check.
	invalidReal := real
	invalidReal.Authentication = AuthFixture
	if err := invalidReal.Validate(); err == nil {
		t.Fatal("real source must not claim fixture authentication")
	}
	writable := mock
	writable.ReadOnly = false
	if err := writable.Validate(); err == nil {
		t.Fatal("external source must remain read-only")
	}
}

func TestExternalAuthorityAllowsSourceFactsButBlocksBusinessMutations(t *testing.T) {
	if !AllowsLocalAction(AuthorityExternal, ActionAppendSourceFact) || !AllowsLocalAction(AuthorityExternal, ActionAuditLink) {
		t.Fatal("external source facts and local audit links should be allowed by authority policy")
	}
	for _, action := range []Action{ActionEditContract, ActionApprove, ActionRegister, ActionRecordDelivery, ActionRecordAcceptance, ActionRecordPayment, ActionRecordAssetHandover} {
		if AllowsLocalAction(AuthorityExternal, action) {
			t.Errorf("external authority allowed local mutation %q", action)
		}
	}
}

func TestCompareReceiptDeduplicatesAndRejectsChangedPayload(t *testing.T) {
	previous := Receipt{SourceID: "finance-a", EventID: "evt-42", Digest: digestA}
	decision, err := CompareReceipt(previous, previous)
	if err != nil || decision != ReceiptReplay {
		t.Fatalf("same event/digest: decision=%q err=%v", decision, err)
	}
	changed := previous
	changed.Digest = digestB
	decision, err = CompareReceipt(previous, changed)
	if err != nil || decision != ReceiptConflict {
		t.Fatalf("same event/different digest: decision=%q err=%v", decision, err)
	}
}

func TestCompareVersionOpaqueAndOutOfOrder(t *testing.T) {
	current := SnapshotVersion{Version: "rev-9", Digest: digestA, Sequence: 9}
	for _, incoming := range []SnapshotVersion{
		{Version: "rev-10", Digest: digestB, Sequence: 10},
		{Version: "rev-2", Digest: digestB, Sequence: 2},
	} {
		decision, err := CompareVersion(current, incoming, VersionOpaque)
		if err != nil || decision != VersionArchiveObservationOnly {
			t.Fatalf("opaque version %q: decision=%q err=%v", incoming.Version, decision, err)
		}
	}
	decision, err := CompareVersion(current, SnapshotVersion{Version: "rev-8", Digest: digestB, Sequence: 8}, VersionMonotonicSequence)
	if err != nil || decision != VersionArchiveObservationOnly {
		t.Fatalf("older monotonic source version: decision=%q err=%v", decision, err)
	}
	decision, err = CompareVersion(current, SnapshotVersion{Version: "rev-10", Digest: digestB, Sequence: 10}, VersionMonotonicSequence)
	if err != nil || decision != VersionAdvanceCurrent {
		t.Fatalf("newer authenticated monotonic version: decision=%q err=%v", decision, err)
	}
	decision, err = CompareVersion(current, SnapshotVersion{Version: "different-version", Digest: digestB, Sequence: 9}, VersionMonotonicSequence)
	if err != nil || decision != VersionConflict {
		t.Fatalf("same monotonic sequence with different content: decision=%q err=%v", decision, err)
	}
	for _, incoming := range []SnapshotVersion{
		{Version: "rev-9", Digest: digestB, Sequence: 8},
		{Version: "rev-9", Digest: digestB, Sequence: 10},
		{Version: "rev-9", Digest: digestA, Sequence: 8},
		{Version: "rev-9", Digest: digestA, Sequence: 10},
	} {
		decision, err := CompareVersion(current, incoming, VersionMonotonicSequence)
		if err != nil || decision != VersionConflict {
			t.Errorf("immutable provider version changed digest/sequence: incoming=%+v decision=%q err=%v", incoming, decision, err)
		}
	}
}

func TestCompareVersionSameVersionReplayOrConflict(t *testing.T) {
	decision, err := CompareVersion(SnapshotVersion{}, SnapshotVersion{Version: "first", Digest: digestA}, VersionOpaque)
	if err != nil || decision != VersionArchiveObservationOnly {
		t.Fatalf("first opaque version must remain observation-only: decision=%q err=%v", decision, err)
	}
	decision, err = CompareVersion(SnapshotVersion{}, SnapshotVersion{Version: "first", Digest: digestA}, VersionMonotonicSequence)
	if err != nil || decision != VersionArchiveObservationOnly {
		t.Fatalf("first monotonic version without sequence must remain observation-only: decision=%q err=%v", decision, err)
	}
	decision, err = CompareVersion(SnapshotVersion{}, SnapshotVersion{Version: "first", Digest: digestA, Sequence: 7}, VersionMonotonicSequence)
	if err != nil || decision != VersionAdvanceCurrent {
		t.Fatalf("first authenticated sequenced version: decision=%q err=%v", decision, err)
	}
	decision, err = CompareVersion(SnapshotVersion{Version: "unsequenced", Digest: digestA}, SnapshotVersion{Version: "first-sequenced", Digest: digestB, Sequence: 2}, VersionMonotonicSequence)
	if err != nil || decision != VersionAdvanceCurrent {
		t.Fatalf("first trusted sequence after unsequenced observations: decision=%q err=%v", decision, err)
	}
	current := SnapshotVersion{Version: "opaque:abc", Digest: digestA}
	decision, err = CompareVersion(current, current, VersionOpaque)
	if err != nil || decision != VersionReplay {
		t.Fatalf("same version/digest: decision=%q err=%v", decision, err)
	}
	changed := current
	changed.Digest = digestB
	decision, err = CompareVersion(current, changed, VersionOpaque)
	if err != nil || decision != VersionConflict {
		t.Fatalf("same version/different digest: decision=%q err=%v", decision, err)
	}
}
