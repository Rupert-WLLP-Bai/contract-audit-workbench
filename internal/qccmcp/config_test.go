// File metadata (manifest mirror):
// Purpose: Tests that live Qichacha MCP access stays disabled unless the operator explicitly enables it and credentials are read only from the environment.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-08
// Updated: 2026-11-08
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package qccmcp

import "testing"

func TestLiveAccessIsDisabledByDefault(t *testing.T) {
	t.Setenv(EnableEnv, "")
	if EnabledFromEnvironment() {
		t.Fatal("QCC MCP must be disabled by default")
	}
	t.Setenv(EnableEnv, "false")
	if EnabledFromEnvironment() {
		t.Fatal("false must keep QCC MCP disabled")
	}
	t.Setenv(EnableEnv, "true")
	if !EnabledFromEnvironment() {
		t.Fatal("true must explicitly enable QCC MCP")
	}
}

func TestAuthorizationComesOnlyFromEnvironment(t *testing.T) {
	t.Setenv(AuthorizationEnv, "  Bearer test-only  ")
	if got := AuthorizationFromEnvironment(); got != "Bearer test-only" {
		t.Fatalf("authorization = %q", got)
	}
}
