package documentservice_test

import (
	"net/http"
	"testing"
	"time"
)

// A full parser queue must not reveal capacity in place of an authorization
// failure or allow an unauthorized request to consume a parser slot.
func TestFullPublicQueueStillChecksAuthority(t *testing.T) {
	f := newServiceFixture(t, 1, 20*time.Second)
	caseID, imageID := f.newCaseWithDocuments(t)
	objects := newBlockingObjectStore()
	f.document.UseObjectStore(objects)
	t.Cleanup(objects.releaseGet)

	contentType, body := multipartUploadPayload(t, "attachment", "held.txt", "text/plain", []byte("held parser"))
	blocked := make(chan httpResult, 1)
	go func() { blocked <- f.do(http.MethodPost, "/api/cases/"+caseID+"/documents", contentType, body) }()
	select {
	case <-objects.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("authorized upload did not occupy the parser queue")
	}

	viewer, _, err := f.core.Login("demo-viewer", "viewer-demo")
	if err != nil {
		t.Fatal(err)
	}
	clerk, _, err := f.core.Login("demo-clerk", "clerk-demo")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		name, token, path, contentType string
		body                           []byte
		want                           int
	}{
		{"viewer upload", viewer, "/api/cases/" + caseID + "/documents", contentType, body, http.StatusForbidden},
		{"ungranted upload", clerk, "/api/cases/" + caseID + "/documents", contentType, body, http.StatusNotFound},
		{"viewer reparse", viewer, "/api/cases/" + caseID + "/documents/" + imageID + "/parse", "application/json", []byte(`{}`), http.StatusForbidden},
		{"ungranted generation", clerk, "/api/cases/" + caseID + "/generations", "application/json", []byte(`{"template_id":"ent_telecom_dedicated_line_v1","fields":{}}`), http.StatusNotFound},
		{"authorized capacity", f.token, "/api/cases/" + caseID + "/documents", contentType, body, http.StatusServiceUnavailable},
	} {
		t.Run(item.name, func(t *testing.T) {
			response := f.doAs(item.token, http.MethodPost, item.path, item.contentType, item.body)
			if response.err != nil || response.status != item.want {
				t.Fatalf("status=%d body=%s err=%v, want %d while queue remains full", response.status, response.body, response.err, item.want)
			}
		})
	}
	objects.releaseGet()
	if response := waitHTTP(t, blocked); response.err != nil || response.status != http.StatusCreated {
		t.Fatalf("blocked authorized upload: status=%d body=%s err=%v", response.status, response.body, response.err)
	}
}
