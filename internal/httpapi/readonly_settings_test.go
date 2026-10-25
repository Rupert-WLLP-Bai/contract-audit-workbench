package httpapi_test

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"contractaudit/internal/httpapi"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
)

func TestReadOnlySettingsDoesNotProbeConfiguredModels(t *testing.T) {
	var probes atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probes.Add(1)
		_, _ = w.Write([]byte(`{"data":[{"id":"unused-readonly-model"}]}`))
	}))
	defer upstream.Close()
	configured := model.New(model.Config{BaseURL: upstream.URL + "/v1", ChatModel: model.DefaultChatModel, OCRModel: model.DefaultOCRModel})
	st, err := store.OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.UseModels(configured)
	st.UseReadOnly(true)
	server := httptest.NewServer(httpapi.Handler(st, httpapi.Options{Models: configured}))
	defer server.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	api := &testAPI{base: server.URL, client: &http.Client{Jar: jar}}
	api.login(t, "demo-admin", "admin-demo")
	status, settings := api.do(t, http.MethodGet, "/api/settings", nil)
	if status != http.StatusOK {
		t.Fatalf("readonly settings unavailable: %d %+v", status, settings)
	}
	if calls := probes.Load(); calls != 0 {
		t.Fatalf("readonly settings sent %d external model probes", calls)
	}
	view := settings["model"].(map[string]any)
	if view["status"] != "unknown" {
		t.Fatalf("unprobed model was assigned a measured health status: %+v", view)
	}
	label := view["label"].(string)
	if !strings.Contains(label, model.DefaultChatModel) || !strings.Contains(label, model.DefaultOCRModel) {
		t.Fatalf("readonly settings discarded configured model identities: %+v", view)
	}
}
