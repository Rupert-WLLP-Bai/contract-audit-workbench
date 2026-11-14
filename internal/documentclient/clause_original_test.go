// File metadata (manifest mirror):
// Purpose: Checks original-reader session forwarding, status mapping, redirect refusal and context cancellation over actual HTTP.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package documentclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/fault"
)

func TestClauseOriginalHTTPSessionAndRefusals(t *testing.T) {
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		if err != nil || cookie.Value != "private-session" || r.Header.Get("X-Contract-Service-Key") != strings.Repeat("d", 40) || r.Method != http.MethodGet || r.URL.Path != "/api/cases/case-1/documents/doc-1/original" {
			t.Errorf("wrong original request or session")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write([]byte("合同原文"))
		}
	}))
	defer server.Close()
	client, err := NewClauseOriginalReader(server.URL, strings.Repeat("d", 40), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	body, err := client.ReadClauseOriginal(context.Background(), "case-1", "doc-1", "private-session")
	if err != nil || string(body) != "合同原文" {
		t.Fatalf("original=%q err=%v", body, err)
	}
	for _, tc := range []struct{ upstream, expected int }{{401, 401}, {403, 404}, {404, 404}, {409, 409}, {500, 503}} {
		status = tc.upstream
		_, err = client.ReadClauseOriginal(context.Background(), "case-1", "doc-1", "private-session")
		var problem *fault.Error
		if !errors.As(err, &problem) || problem.Status != tc.expected {
			t.Fatalf("upstream %d: error=%v", tc.upstream, err)
		}
	}
}

func TestClauseOriginalRefusesCrossOriginRedirectAndCancellation(t *testing.T) {
	redirectCalls := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { redirectCalls++; w.WriteHeader(http.StatusOK) }))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer origin.Close()
	client, err := NewClauseOriginalReader(origin.URL, strings.Repeat("d", 40), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ReadClauseOriginal(context.Background(), "case", "doc", "private-session"); err == nil || redirectCalls != 0 {
		t.Fatalf("redirect followed: calls=%d err=%v", redirectCalls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.ReadClauseOriginal(ctx, "case", "doc", "private-session"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}
}
