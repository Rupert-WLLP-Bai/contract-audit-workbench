// File metadata (manifest mirror):
// Purpose: Verifies local OCR polling, submission identity, cancellation and loopback endpoint boundaries.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package localocr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseImagePollsSameRequestWithoutResubmission(t *testing.T) {
	var posts atomic.Int32
	var id string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				return
			}
			id = r.FormValue("request_id")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]string{"request_id": id, "status": "pending"})
			return
		}
		if r.URL.Path != "/v1/file_parse/"+id && r.URL.Path != "/v1/file_parse/"+id+"/result" {
			t.Errorf("unexpected poll path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/result") {
			_, _ = w.Write([]byte("archive"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"request_id": id, "status": "completed"})
	}))
	defer server.Close()
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := client.ParseImage(context.Background(), "image/png", []byte("image"))
	if err != nil || string(body) != "archive" || posts.Load() != 1 || id == "" {
		t.Fatalf("unexpected completion: %q posts=%d err=%v", body, posts.Load(), err)
	}
}

func TestParseImageCancellationAndRemoteFailureIdentifyRequest(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var posts atomic.Int32
			var id string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				_ = r.ParseMultipartForm(1 << 20)
				id = r.FormValue("request_id")
				if cancelled {
					cancel()
					return
				}
				w.WriteHeader(http.StatusAccepted)
				_ = json.NewEncoder(w).Encode(map[string]string{"request_id": id, "status": "submission_unknown"})
			}))
			defer server.Close()
			client, err := NewClient(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.ParseImage(ctx, "image/png", []byte("image"))
			if err == nil || id == "" || !strings.Contains(err.Error(), id) || posts.Load() != 1 {
				t.Fatalf("missing request identity or repeated submit: id=%q posts=%d err=%v", id, posts.Load(), err)
			}
			if cancelled && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}

func TestNewClientRejectsNonLoopback(t *testing.T) {
	for _, endpoint := range []string{"http://example.com:80", "http://localhost:80", "https://127.0.0.1:80", "http://127.0.0.1:80/other"} {
		if _, err := NewClient(endpoint); err == nil {
			t.Fatalf("accepted endpoint %q", endpoint)
		}
	}
}
