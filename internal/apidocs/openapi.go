// Package apidocs serves the versioned contract for the implemented HTTP API.
package apidocs

import (
	"bytes"
	_ "embed"
	"net/http"
	"time"
)

//go:embed openapi.json
var document []byte

// Document returns a copy so callers cannot mutate the served contract.
func Document() []byte { return bytes.Clone(document) }

// Serve exposes public schema metadata, never case data or credentials.
func Serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "openapi.json", time.Time{}, bytes.NewReader(document))
}
