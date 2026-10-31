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
