// File metadata (manifest mirror):
// Purpose: 实现 `offlineWatcherStore`, `LLMEndpoint`, `UseModels`, `HasDatabase` 等单进程服务装配与集成入口，连接已配置的服务组件和权限边界。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package bootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/llmendpoint"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
)

type offlineWatcherStore struct {
	settings store.LLMEndpointSettings
	reads    chan struct{}
	updates  chan store.Models
}

func (s *offlineWatcherStore) LLMEndpoint(context.Context) (store.LLMEndpointSettings, bool, error) {
	select {
	case s.reads <- struct{}{}:
	default:
	}
	return s.settings, true, nil
}
func (s *offlineWatcherStore) UseModels(m store.Models) { s.updates <- m }
func (*offlineWatcherStore) HasDatabase() bool          { return true }

func TestOfflineWatcherCannotActivateNativeStoredProvider(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer upstream.Close()
	base := model.Config{BaseURL: "http://example.invalid", ChatModel: "chat", OCRModel: "vision"}
	offline := model.New(base)
	st := &offlineWatcherStore{settings: store.LLMEndpointSettings{Protocol: llmendpoint.ProtocolOpenAICompatible, BaseURL: upstream.URL, Models: []string{"chat", "vision"}, ReviewModel: "chat", OCRModel: "vision", Revision: 2}, reads: make(chan struct{}, 1), updates: make(chan store.Models, 1)}
	stop, done := watchStoredLLMEndpoint(context.Background(), st, "", base, 1)
	select {
	case <-st.reads:
	case <-time.After(5 * time.Second):
		t.Fatal("watcher did not observe revision")
	}
	stop()
	<-done
	select {
	case <-st.updates:
		t.Fatal("watcher installed native stored provider")
	default:
	}
	ctx := model.WithDelegatedScope(context.Background(), model.DelegatedScope{Kind: "document", Action: "upload_parse", OperationTicket: "op_real"})
	if _, err := offline.Transcribe(ctx, "image/png", model.SampleWarrantyPNG); err == nil || !strings.Contains(err.Error(), "model request failed") {
		t.Fatalf("ticketed document native call was not rejected: %v", err)
	}
	if calls != 0 {
		t.Fatalf("provider received %d requests", calls)
	}
}
