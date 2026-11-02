// File metadata (manifest mirror):
// Purpose: 实现 `TestCoreSessionResolverRebuildsMembershipProjectionFromAuthenticatedResponse` 等具体接口与边界逻辑，供 internal/toolgateway 的调用方使用。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package toolgateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCoreSessionResolverRebuildsMembershipProjectionFromAuthenticatedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != coreSessionAuthorityPath || r.Header.Get("Authorization") != "Bearer opaque-session-token" {
			t.Errorf("Core authority request method=%q path=%q authorization=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(coreSessionProjection{
			Audience: "client", MembershipBound: true, ActorID: "actor", DisplayName: "Directory label",
			Role: "reviewer", TenantID: "database-tenant", OrgID: "database-org", Enabled: true,
			Issuer: "https://issuer.example", Subject: "subject", PrincipalID: "principal-id",
			MembershipID: "membership-id",
		})
	}))
	defer server.Close()
	resolver, err := NewCoreSessionResolver(server.URL, "", true, "test", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	user, audience, err := resolver.AuthenticateSession(context.Background(), "opaque-session-token")
	if err != nil {
		t.Fatal(err)
	}
	if audience != "client" || !user.HasResolvedMembership() || user.TenantID != "database-tenant" || user.Role != "reviewer" ||
		user.PrincipalID != "principal-id" || user.MembershipID != "membership-id" {
		t.Fatalf("authenticated Core projection was not reconstructed: audience=%q user=%+v", audience, user)
	}
}
