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
	"net/http"
	"testing"
	"time"
)

// TestNewSetsRequestTimeout pins the client timeout and preserves caller-owned clients.
func TestNewSetsRequestTimeout(t *testing.T) {
	client := New(Config{BaseURL: "http://model.test", ChatModel: "test-model"})
	if client.http.Timeout != 630*time.Second {
		t.Fatalf("timeout = %v, want %v", client.http.Timeout, 630*time.Second)
	}
	custom := &http.Client{Timeout: time.Second, Transport: http.DefaultTransport}
	if got := NewWithHTTPClient(Config{}, custom).http; got != custom || got.Timeout != time.Second || got.Transport != http.DefaultTransport {
		t.Fatal("caller-owned HTTP client was changed")
	}
}
