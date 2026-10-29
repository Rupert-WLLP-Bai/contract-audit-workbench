package httpapi

import (
	"net/http"

	"contractaudit/internal/accounts"
)

type actorResolver func(http.ResponseWriter, *http.Request) (accounts.User, bool)
type jsonCommand[T any] func(accounts.User, *http.Request, T) (any, error)
type jsonLookup func(accounts.User, *http.Request) (any, error)

type routeBinding struct {
	pattern string
	handler http.HandlerFunc
}

func mountRoutes(mux *http.ServeMux, routes ...routeBinding) {
	for _, route := range routes { mux.HandleFunc(route.pattern, route.handler) }
}

// serveJSONCommand keeps the shared HTTP sequence in one place: authorize,
// decode the strict object body, execute, then apply the common JSON response.
func serveJSONCommand[T any](w http.ResponseWriter, r *http.Request, status int, resolve actorResolver, execute jsonCommand[T]) {
	user, ok := resolve(w, r)
	if !ok {
		return
	}
	var body T
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	item, err := execute(user, r, body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, status, item)
}

func serveClientJSONCommand[T any](h *handler, w http.ResponseWriter, r *http.Request, status int, execute jsonCommand[T]) {
	serveJSONCommand(w, r, status, h.actor, execute)
}

func serveGovernanceJSONCommand[T any](h *handler, w http.ResponseWriter, r *http.Request, status int, execute jsonCommand[T]) {
	serveJSONCommand(w, r, status, func(w http.ResponseWriter, r *http.Request) (accounts.User, bool) {
		return h.actorForAudience(w, r, "admin")
	}, execute)
}

func serveClientJSONLookup(h *handler, w http.ResponseWriter, r *http.Request, execute jsonLookup) {
	user, ok := h.actor(w, r)
	if !ok { return }
	item, err := execute(user, r)
	if err != nil { writeErr(w, err); return }
	writeJSON(w, http.StatusOK, item)
}
