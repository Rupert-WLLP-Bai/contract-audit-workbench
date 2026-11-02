// File metadata (manifest mirror):
// Purpose: BrowserOrigins returns the explicitly configured browser origins and, in development or test only, the local Vite origins. Production has no implicit browser origins; an empty or… Related `config` operations include BrowserOrigins.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-10-31
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package config

import "strings"

// BrowserOrigins returns the explicitly configured browser origins and, in
// development or test only, the local Vite origins. Production has no implicit
// browser origins; an empty or invalid configuration must not grant access.
func BrowserOrigins(raw, mode string) []string {
	var origins []string
	if normalized, err := normalizeMode(mode); err == nil && (normalized == ModeDevelopment || normalized == ModeTest) {
		origins = append(origins, "http://localhost:5173", "http://127.0.0.1:5173")
	}
	for _, origin := range strings.Split(raw, ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			origins = append(origins, origin)
		}
	}
	return origins
}
