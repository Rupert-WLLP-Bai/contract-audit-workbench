// File metadata (manifest mirror):
// Purpose: Exercises confirmed revocation and temporary authority or directory failures through the real Core HTTP handler and independent Execution database role.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-03
// Updated: 2026-11-03
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"contractaudit/internal/accounts"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/operationauthority"
	"contractaudit/internal/store"
)

func TestRevokedFinalizeRealCoreHTTPAndExecutionRole(t *testing.T) {
	for _, scenario := range []string{"confirmed_revocation", "core_unavailable", "directory_unavailable", "directory_mode_change", "directory_default_scope_changed", "expired_lease_with_core_unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			f := store.NewRevokedFinalizeFixture(t)
			workerKey, gatewayKey := strings.Repeat("w", 40), strings.Repeat("g", 40)
			handler, err := httpapi.ServiceHandler(f.Core(), httpapi.Options{Durable: true, ExperienceWorkerCredential: workerKey}, "core", gatewayKey)
			if err != nil {
				t.Fatal(err)
			}
			var unavailable atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if unavailable.Load() {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				handler.ServeHTTP(w, r)
			}))
			t.Cleanup(server.Close)
			client, err := operationauthority.NewOperationClient(server.URL, "worker", workerKey, "", "test", true)
			if err != nil {
				t.Fatal(err)
			}
			f.Execution().UseOperationAuthority(client)
			if scenario == "expired_lease_with_core_unavailable" {
				f.ExpireClaim(t)
				before := f.Snapshot(t)
				unavailable.Store(true)
				if err := f.Finalize(); !errors.Is(err, store.ErrLeaseLost) {
					t.Fatalf("unavailable Core hid the expired lease: %v", err)
				}
				if after := f.Snapshot(t); !reflect.DeepEqual(before, after) {
					t.Fatalf("expired claim changed state while Core was unavailable: before=%+v after=%+v", before, after)
				}
				unavailable.Store(false)
				if err := f.Finalize(); !errors.Is(err, store.ErrLeaseLost) {
					t.Fatalf("recovered Core hid the expired lease: %v", err)
				}
				if after := f.Snapshot(t); !reflect.DeepEqual(before, after) {
					t.Fatalf("expired claim changed state after Core recovered: before=%+v after=%+v", before, after)
				}
				return
			}
			before := f.Snapshot(t)
			initialUsers := accounts.List()
			if scenario == "directory_default_scope_changed" {
				for i := range initialUsers {
					if initialUsers[i].ID == f.Actor().ID {
						initialUsers[i].TenantID = "stale-directory-default"
						initialUsers[i].OrgID = "stale-directory-org"
						initialUsers[i].Role = "viewer"
						initialUsers[i].Grants = nil
					}
				}
			}
			restoreDirectory := func() {
				t.Helper()
				raw, err := json.Marshal(struct {
					Users []accounts.User `json:"users"`
				}{Users: initialUsers})
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "original-directory.json")
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
				if err := accounts.ConfigureDirectory(path); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "directory_default_scope_changed" {
				restoreDirectory()
				if err := f.Finalize(); err != nil {
					t.Fatalf("directory default scope replaced authoritative membership: %v", err)
				}
				return
			}
			if scenario == "directory_mode_change" {
				if err := accounts.ConfigureDirectory(""); err != nil {
					t.Fatal(err)
				}
				if err := f.Finalize(); err == nil {
					t.Fatal("directory mode change allowed terminal transition")
				}
				if after := f.Snapshot(t); !reflect.DeepEqual(before, after) {
					t.Fatalf("directory mode change altered attempt: before=%+v after=%+v", before, after)
				}
				restoreDirectory()
				if err := f.Finalize(); err != nil {
					t.Fatalf("retry after original directory restored: %v", err)
				}
				return
			}
			if scenario == "core_unavailable" {
				unavailable.Store(true)
				if err := f.Finalize(); err == nil || errors.Is(err, store.ErrLeaseLost) {
					t.Fatalf("unavailable Core must preserve the active lease and refuse the transition: %v", err)
				}
				if after := f.Snapshot(t); !reflect.DeepEqual(before, after) {
					t.Fatalf("unavailable Core changed attempt: before=%+v after=%+v", before, after)
				}
				unavailable.Store(false)
				if err := f.Finalize(); err != nil {
					t.Fatalf("retry after Core recovered: %v", err)
				}
				attempt, _, _, _, _ := f.State(t)
				if attempt != "completed" {
					t.Fatalf("recovered attempt state=%s", attempt)
				}
				return
			}
			if scenario == "directory_unavailable" {
				actor := f.Actor()
				raw, err := json.Marshal(struct {
					Users []accounts.User `json:"users"`
				}{Users: []accounts.User{actor}})
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "directory.json")
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
				if err := accounts.ConfigureDirectory(path); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = accounts.ConfigureDirectory("") })
				if err := os.WriteFile(path, []byte(`{"users":`), 0600); err != nil {
					t.Fatal(err)
				}
				if err := accounts.ConfigureDirectory(path); err == nil {
					t.Fatal("invalid current directory accepted")
				}
				if got := accounts.DirectoryStatus().State; got != "unavailable" {
					t.Fatalf("directory state=%s", got)
				}
				if err := f.Finalize(); err == nil {
					t.Fatal("unavailable directory allowed terminal transition")
				}
				if after := f.Snapshot(t); !reflect.DeepEqual(before, after) {
					t.Fatalf("unavailable directory changed attempt: before=%+v after=%+v", before, after)
				}
				restoreDirectory()
				if err := f.Finalize(); err != nil {
					t.Fatalf("retry after directory recovered: %v", err)
				}
				return
			}
			actor := f.Actor()
			actor.Enabled = false
			raw, err := json.Marshal(struct {
				Users []accounts.User `json:"users"`
			}{Users: []accounts.User{actor}})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "directory.json")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if err := accounts.ConfigureDirectory(path); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = accounts.ConfigureDirectory("") })
			if err := f.Finalize(); err != nil {
				t.Fatalf("confirmed revoked finalization: %v", err)
			}
			attempt, run, code, findings, evaluations := f.State(t)
			if attempt != "cancelled" || run != "cancelled" || code != "authorization_revoked" || findings != 0 || evaluations != 0 {
				t.Fatalf("revocation result attempt=%s run=%s code=%s findings=%d evaluations=%d", attempt, run, code, findings, evaluations)
			}
			restoreDirectory()
			if err := f.Finalize(); err == nil {
				t.Fatal("restored identity upgraded a cancelled attempt")
			}
			attempt, run, code, findings, evaluations = f.State(t)
			if attempt != "cancelled" || run != "cancelled" || code != "authorization_revoked" || findings != 0 || evaluations != 0 {
				t.Fatal("terminal cancellation changed after restore")
			}
		})
	}
}
