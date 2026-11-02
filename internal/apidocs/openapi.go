// File metadata (manifest mirror):
// Purpose: serves the versioned contract for the implemented HTTP API. Related `apidocs` operations include Document, Serve.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-02
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

// Package apidocs serves the versioned contract for the implemented HTTP API.
package apidocs

//go:generate go run ../../cmd/openapi-gen

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
