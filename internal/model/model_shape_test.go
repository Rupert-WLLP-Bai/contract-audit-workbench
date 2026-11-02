// File metadata (manifest mirror):
// Purpose: Exercises the `model` package behavior through TestNewSetsRequestTimeout; these checks pin observable state, error handling, or wire/database compatibility for this module.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-30
// Updated: 2026-10-30
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

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
