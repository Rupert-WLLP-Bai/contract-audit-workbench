package store

import (
	"errors"
	"sync"
	"testing"

	"contractaudit/internal/accounts"
)

// TestSessionNarrowWritesKeepAuthorityAndRevocation pins the session contract
// against the durable Core service role.
func TestSessionNarrowWritesKeepAuthorityAndRevocation(t *testing.T) {
	environment, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	st := environment.openRole(t, "core")
	admin, found := accounts.ByID("demo-admin")
	if !found {
		t.Fatal("demo admin missing")
	}
	first, err := st.EstablishSession(admin, "client")
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.EstablishSession(admin, "client")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("two sessions shared one token: %q", first)
	}
	for _, token := range []string{first, second} {
		user, err := st.User(token)
		if err != nil || user.ID != admin.ID {
			t.Fatalf("session %q did not resolve its actor: user=%+v err=%v", token, user, err)
		}
	}
	// Revoking one token must not touch the other, and must be idempotent.
	if err := st.Logout(first); err != nil {
		t.Fatal(err)
	}
	if _, err := st.User(first); err == nil {
		t.Fatal("a revoked session still resolved")
	}
	if _, err := st.User(second); err != nil {
		t.Fatalf("revoking one session revoked another: %v", err)
	}
	if err := st.Logout(first); err != nil {
		t.Fatalf("logout was not idempotent: %v", err)
	}
	// A whole-state write after the narrow writes must not delete the live
	// session: the in-memory projection has to know about it.
	if _, err := st.CreateCase(admin, "会话投影回归", "procurement_ict"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.User(second); err != nil {
		t.Fatalf("a whole-state write deleted a live session: %v", err)
	}
	// A foreign token cannot revoke a session it does not hold.
	stranger, err := st.EstablishSession(admin, "client")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Logout("s_not_a_real_token"); err != nil {
		t.Fatalf("revoking an unknown token must be a no-op: %v", err)
	}
	if _, err := st.User(stranger); err != nil {
		t.Fatalf("an unknown token revoked a live session: %v", err)
	}
	if _, err := st.User(second); err != nil {
		t.Fatalf("an unknown token revoked a live session: %v", err)
	}
	// The narrow path keeps the actor identity exact.
	user, ok := accounts.ByID(admin.ID)
	if !ok {
		t.Fatal("demo admin missing")
	}
	other, err := st.EstablishSession(user, "invalid-audience")
	if err == nil {
		t.Fatalf("an unsupported audience was accepted: %q", other)
	}
	adminSession, err := st.EstablishSession(user, "admin")
	if err != nil {
		t.Fatalf("the trusted admin identity could not open an admin session: %v", err)
	}
	if audience, err := st.SessionAudience(adminSession); err != nil || audience != "admin" {
		t.Fatalf("admin session audience was not retained: %q err=%v", audience, err)
	}
}

// TestConcurrentLogoutCannotBeResurrectedByCaseWrite uses two Core stores on
// one database: one revokes a session while the other commits a generic case
// state write from a process opened before that session existed.
func TestConcurrentLogoutCannotBeResurrectedByCaseWrite(t *testing.T) {
	environment, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	revoker := environment.openRole(t, "core")
	admin, found := accounts.ByID("demo-admin")
	if !found {
		t.Fatal("demo admin missing")
	}
	caseID, err := revoker.CreateCase(admin, "并发会话回归", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	writer := environment.openRole(t, "core")
	token, err := revoker.EstablishSession(admin, "client")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.User(token); err != nil {
		t.Fatalf("the second store did not see the session: %v", err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var logoutErr, writeErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		logoutErr = revoker.Logout(token)
	}()
	go func() {
		defer wg.Done()
		<-start
		writeErr = writer.updateAs(admin, func(st *state) error {
			for i := range st.Cases {
				if st.Cases[i].ID == caseID {
					st.Cases[i].Title = "并发会话回归已更新"
					st.Cases[i].UpdatedAt = stamp()
					return nil
				}
			}
			return errors.New("test case is missing from the writer snapshot")
		})
	}()
	close(start)
	wg.Wait()
	if logoutErr != nil {
		t.Fatalf("concurrent logout failed: %v", logoutErr)
	}
	if writeErr != nil {
		t.Fatalf("concurrent generic case write failed: %v", writeErr)
	}
	for name, store := range map[string]*Store{"revoker": revoker, "writer": writer} {
		if _, err := store.User(token); err == nil {
			t.Fatalf("the revoked token reappeared on the %s store", name)
		}
	}
}
