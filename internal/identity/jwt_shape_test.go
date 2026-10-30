package identity

import (
	"strings"
	"testing"
)

func shapedToken(length int) string {
	if length < 4 {
		return ""
	}
	return "a." + strings.Repeat("b", length-4) + ".c"
}

// TestValidJWTShapeSizeLimit pins the upper size guard on the three-part
// token shape. Mutations of the `||`/`>` guard otherwise let an oversized
// token through or reject one exactly at the limit.
func TestValidJWTShapeSizeLimit(t *testing.T) {
	if !validJWTShape("a.b.c") {
		t.Fatal("normal three-part token rejected")
	}
	if validJWTShape("") {
		t.Fatal("empty token accepted")
	}

	exact := shapedToken(maxProviderJSON)
	if len(exact) != maxProviderJSON {
		t.Fatalf("constructed token length = %d, want %d", len(exact), maxProviderJSON)
	}
	if !validJWTShape(exact) {
		t.Fatal("token exactly at the size limit rejected")
	}

	oversized := shapedToken(maxProviderJSON + 1)
	if len(oversized) != maxProviderJSON+1 {
		t.Fatalf("constructed token length = %d, want %d", len(oversized), maxProviderJSON+1)
	}
	if validJWTShape(oversized) {
		t.Fatal("oversized three-part token accepted")
	}
}
