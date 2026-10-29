package toolgateway

import (
	"errors"
	"testing"
)

func TestPinnedDocumentIDsRejectsEmptyAndDuplicateBindings(t *testing.T) {
	if _, err := pinnedDocumentIDs([]string{"doc-a", ""}); !errors.Is(err, errFrozenIdentity) {
		t.Fatalf("empty pinned identity error = %v", err)
	}
	if _, err := pinnedDocumentIDs([]string{"doc-a", "doc-a"}); !errors.Is(err, errFrozenIdentity) {
		t.Fatalf("duplicate pinned identity error = %v", err)
	}
	got, err := pinnedDocumentIDs([]string{"doc-b", "doc-a"})
	if err != nil || len(got) != 2 {
		t.Fatalf("valid pinned identities = %v, %v", got, err)
	}
}
