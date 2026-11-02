package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

type downloadSessionAuthority struct {
	store *store.Store
	mode  string
	calls int
}

func (a *downloadSessionAuthority) AuthenticateSession(ctx context.Context, token string) (accounts.User, string, error) {
	a.calls++
	user, audience, err := a.store.AuthenticateSession(ctx, token)
	if err != nil || a.calls == 1 {
		return user, audience, err
	}
	switch a.mode {
	case "revoked":
		return accounts.User{}, "", fault.New(http.StatusUnauthorized, "session_invalid", "会话已失效")
	case "switched":
		proof, _ := user.CommandAuthority()
		proof.ContextVersion = "999"
		user = accounts.WithCommandAuthority(user, proof)
	case "other-tenant":
		user.TenantID = "another-tenant"
	case "other-audience":
		audience = "admin"
	}
	return user, audience, nil
}

// The second resolution occurs after the CSV has been rendered; the cached
// first identity must not let a revoked or switched session receive file bytes.
func TestContractDownloadReauthenticatesBeforeBytes(t *testing.T) {
	st, err := store.OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	token, err := st.EstablishSession(admin, "client")
	if err != nil {
		t.Fatal(err)
	}
	user, _, err := st.AuthenticateSession(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateContractDraft(context.Background(), user, store.CreateContractDraftInput{
		AuthorityMode: "native", IdempotencyKey: "download-contract-fixture",
		Draft: store.DraftContent{Contract: &store.ContractBasics{Name: "synthetic download contract", Currency: "CNY", Amount: "1.00"}},
	}); err != nil {
		t.Fatal(err)
	}
	export, err := st.CreateContractExport(context.Background(), user, store.ContractExportInput{Format: "csv", Columns: []string{"contract_number"}, IdempotencyKey: "download-reauth-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"valid", "revoked", "switched", "other-tenant", "other-audience"} {
		t.Run(mode, func(t *testing.T) {
			authority := &downloadSessionAuthority{store: st, mode: mode}
			h := &handler{store: st, sessionAuthority: authority}
			req := httptest.NewRequest(http.MethodGet, "/download", nil)
			req.SetPathValue("exportID", export.ExportID)
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
			rec := httptest.NewRecorder()
			h.downloadContractExport(rec, req)
			if authority.calls != 2 {
				t.Fatalf("authentication calls=%d, want initial and uncached final", authority.calls)
			}
			if mode == "valid" {
				if rec.Code != http.StatusOK || !bytes.HasPrefix(rec.Body.Bytes(), []byte{0xef, 0xbb, 0xbf}) {
					t.Fatalf("valid download status=%d body=%q", rec.Code, rec.Body.String())
				}
			} else if rec.Code == http.StatusOK || rec.Header().Get("Content-Type") == "text/csv; charset=utf-8" || bytes.HasPrefix(rec.Body.Bytes(), []byte{0xef, 0xbb, 0xbf}) {
				t.Fatalf("%s returned CSV bytes: status=%d body=%q", mode, rec.Code, rec.Body.String())
			}
		})
	}
}
