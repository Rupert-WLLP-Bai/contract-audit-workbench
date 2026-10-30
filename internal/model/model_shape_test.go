package model

import (
	"testing"
	"time"
)

// TestNewSetsRequestTimeout pins the client timeout. The mutator changes
// `120 * time.Second` to `120 / time.Second`, which silently disables the
// timeout; this test makes that change fail.
func TestNewSetsRequestTimeout(t *testing.T) {
	client := New(Config{BaseURL: "http://model.test", ChatModel: "test-model"})
	if client.http.Timeout != 120*time.Second {
		t.Fatalf("timeout = %v, want %v", client.http.Timeout, 120*time.Second)
	}
}
