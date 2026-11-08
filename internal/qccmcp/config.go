// File metadata (manifest mirror):
// Purpose: Defines the explicit Qichacha MCP enable switch and environment-only credential loading, keeping the integration disabled by default.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-08
// Updated: 2026-11-08
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package qccmcp

import (
	"os"
	"strings"
)

const (
	// EnableEnv is the explicit switch for live Qichacha MCP access. It is false
	// unless the operator sets it to true; saved demo data remains readable.
	EnableEnv = "QCC_MCP_ENABLED"
	// AuthorizationEnv holds the Bearer token. It is never persisted.
	AuthorizationEnv = "QCC_MCP_AUTHORIZATION"
)

// EnabledFromEnvironment reports whether live MCP access is explicitly enabled.
func EnabledFromEnvironment() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(EnableEnv)), "true")
}

// AuthorizationFromEnvironment returns the Bearer token without logging it.
func AuthorizationFromEnvironment() string {
	return strings.TrimSpace(os.Getenv(AuthorizationEnv))
}
