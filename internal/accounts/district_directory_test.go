// File metadata (manifest mirror):
// Purpose: Checks district demonstration identity uniqueness, fixed account compatibility and case-scoped grant boundaries.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package accounts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"contractaudit/internal/access"
)

func TestDistrictDirectoryMatchesOrganizationRoster(t *testing.T) {
	if len(districtLogins) != additionalDistrictLogins {
		t.Fatalf("district logins %d, constant %d", len(districtLogins), additionalDistrictLogins)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "domain", "demo-directory.json"))
	if err != nil {
		t.Fatal(err)
	}
	var roster struct {
		Organizations []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"organizations"`
		Users []User `json:"users"`
	}
	if err := json.Unmarshal(raw, &roster); err != nil {
		t.Fatal(err)
	}
	wantNames := []string{"万州区", "黔江区", "涪陵区", "渝中区", "大渡口区", "沙坪坝区", "九龙坡区", "南岸区", "北碚区", "巴南区", "长寿区", "江津区", "合川区", "永川区", "南川区", "綦江区", "大足区", "璧山区", "铜梁区", "潼南区", "荣昌区", "开州区", "梁平区", "武隆区", "两江新区"}
	if len(roster.Organizations) != len(wantNames) {
		t.Fatalf("organizations %d", len(roster.Organizations))
	}
	names := map[string]string{}
	for i, item := range roster.Organizations {
		if item.Name != wantNames[i] || item.ID == "" {
			t.Fatalf("organization %d = %+v", i, item)
		}
		names[item.ID] = item.Name
	}
	listed := List()
	if len(listed) != len(roster.Users) {
		t.Fatalf("directory %d, roster %d", len(listed), len(roster.Users))
	}
	byID := map[string]User{}
	seen := map[string]bool{}
	counts := map[string]int{}
	for _, user := range listed {
		byID[user.ID] = user
		if seen[user.DisplayName] {
			t.Fatalf("duplicate display name %s", user.DisplayName)
		}
		seen[user.DisplayName] = true
		if user.TenantID == LocalDemoTenant {
			counts[user.OrgID]++
			if names[user.OrgID] == "" {
				t.Fatalf("%s organization %s is not a Chongqing district", user.ID, user.OrgID)
			}
		}
	}
	if len(counts) != len(wantNames) {
		t.Fatalf("local organizations %+v", counts)
	}
	for id, count := range counts {
		if count < 2 || count > 4 {
			t.Fatalf("%s has %d users", names[id], count)
		}
	}
	for _, want := range roster.Users {
		got, ok := byID[want.ID]
		if !ok || got.DisplayName != want.DisplayName || got.Role != want.Role || got.TenantID != want.TenantID || got.OrgID != want.OrgID || !got.Enabled {
			t.Fatalf("user %+v, roster %+v", got, want)
		}
		grantRole := map[string]string{"admin": "case_owner", "clerk": "case_owner", "reviewer": "reviewer", "viewer": "viewer"}[want.Role]
		if !reflect.DeepEqual(got.Grants, []access.Grant{{Role: grantRole, Scope: "case"}}) {
			t.Fatalf("grants for %s: %+v", want.ID, got.Grants)
		}
	}
	admin, ok := Authenticate("demo-admin", "admin-demo")
	if !ok || admin.DisplayName != "James Carter" || admin.OrgID != "demo-org" || admin.Role != "admin" {
		t.Fatalf("fixed admin login %+v %v", admin, ok)
	}
	reviewer, ok := Authenticate("yuzhong-reviewer", "yuzhong-reviewer-demo")
	if !ok || reviewer.DisplayName != "Mia Harper" || reviewer.OrgID != "demo-other-org" || reviewer.Role != "reviewer" {
		t.Fatalf("district login %+v %v", reviewer, ok)
	}
	if _, ok := Authenticate("yuzhong-reviewer", "admin-demo"); ok {
		t.Fatal("district login accepted another account password")
	}
}
