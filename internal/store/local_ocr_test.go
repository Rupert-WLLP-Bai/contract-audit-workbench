// File metadata (manifest mirror):
// Purpose: Verifies local OCR authorization before and after work and frozen parser identity across engine changes.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"contractaudit/internal/accounts"
	"contractaudit/internal/model"
)

type localOCRAuthority struct {
	calls       int
	revokeAfter int
}

func (a *localOCRAuthority) ResolveOperation(context.Context, model.DelegatedScope) (accounts.User, error) {
	a.calls++
	if a.revokeAfter > 0 && a.calls >= a.revokeAfter {
		return accounts.User{}, errors.New("revoked")
	}
	return accounts.User{}, nil
}

type localOCRFixture struct {
	calls   int
	cancel  context.CancelFunc
	profile string
}

func (o *localOCRFixture) ProfileID() string { return "fixture-profile/" + o.profile }
func (o *localOCRFixture) Transcribe(context.Context, string, []byte) (string, error) {
	o.calls++
	if o.cancel != nil {
		o.cancel()
	}
	return "private output", nil
}

func TestLocalOCRRequiresLiveAuthorityBeforeAndAfterWork(t *testing.T) {
	scope := model.DelegatedScope{Kind: "document", TenantID: "tenant", OrgID: "org", CaseID: "case", ActorID: "actor"}
	for _, tc := range []struct {
		name      string
		withScope bool
		revokeAt  int
		cancel    bool
		wantCalls int
	}{
		{"missing_scope", false, 0, false, 0}, {"revoked_before", true, 1, false, 0}, {"revoked_after", true, 2, false, 1}, {"cancelled_after", true, 0, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.withScope {
				ctx = model.WithDelegatedScope(ctx, scope)
			}
			authority := &localOCRAuthority{revokeAfter: tc.revokeAt}
			engine := &localOCRFixture{}
			if tc.cancel {
				engine.cancel = cancel
			}
			s := &Store{operationAuthority: authority, localOCR: engine}
			text, err := s.transcribe(ctx, nil, Document{MediaType: "image/png"}, []byte("image"))
			if err == nil || text != "" || engine.calls != tc.wantCalls {
				t.Fatalf("untrusted output exposed: text=%q calls=%d err=%v", text, engine.calls, err)
			}
			if tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}

func TestLocalOCRIdentityTracksActualEngineAndPreservesModelHistory(t *testing.T) {
	models := &countingOCRModel{configuration: "model-a"}
	for _, filename := range []string{"scan.png", "scan.pdf", "scan.docx", "text.txt"} {
		doc := Document{Filename: filename}
		id, version, hash := parserConfigurationFor(doc, models)
		plain := &Store{}
		if i, v, h := plain.parserConfigurationWithModels(doc, models); i != id || v != version || h != hash {
			t.Fatal("default model parser identity changed")
		}
		local := &Store{localOCR: &localOCRFixture{profile: "one"}}
		_, localVersion, localHash := local.parserConfigurationWithModels(doc, models)
		if filename == "text.txt" {
			if localVersion != version || localHash != hash {
				t.Fatal("local OCR changed native text identity")
			}
			continue
		}
		if localHash == hash || !strings.Contains(localVersion, "fixture-profile/one") {
			t.Fatal("actual local OCR identity is not frozen")
		}
		_, _, unrelated := local.parserConfigurationWithModels(doc, &countingOCRModel{configuration: "model-b"})
		if localHash != unrelated {
			t.Fatal("unused model configuration changed local OCR identity")
		}
		local.localOCR = &localOCRFixture{profile: "two"}
		_, _, upgraded := local.parserConfigurationWithModels(doc, models)
		if localHash == upgraded {
			t.Fatal("local OCR profile change reused old parsing identity")
		}
	}
}
