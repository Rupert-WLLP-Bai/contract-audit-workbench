package store

import (
	"context"
	"errors"
	"sync"
	"testing"

	"contractaudit/internal/fault"
)

func llmEndpointInput(name string, models []string, expectedRevision *int64) LLMEndpointInput {
	return LLMEndpointInput{
		Name: name, Protocol: "openai-compatible", BaseURL: "https://llm.example.com/v1",
		Models: models, ReviewModel: models[0], OCRModel: models[1],
		ExpectedRevision: expectedRevision,
	}
}

// The first save locks no existing row, so the lock that serializes it must
// come from somewhere else. Without it two creators both write revision 1 and
// the revision-only watcher never notices the second configuration.
func TestConcurrentFirstLLMEndpointSaveSerializesRevisions(t *testing.T) {
	st, admin := openExecutionStore(t)
	zero := int64(0)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for index := range errs {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			name := []string{"primary", "secondary"}[index]
			_, errs[index] = st.SaveLLMEndpoint(context.Background(), admin,
				llmEndpointInput(name, []string{"review-a", "ocr-b"}, &zero))
		}(index)
	}
	wg.Wait()

	accepted := 0
	for _, err := range errs {
		if err == nil {
			accepted++
			continue
		}
		var conflict *fault.Error
		if !errors.As(err, &conflict) || conflict.Status != 409 {
			t.Fatalf("concurrent first save returned %v, want a revision conflict", err)
		}
	}
	if accepted != 1 {
		t.Fatalf("concurrent first save accepted %d writers, want exactly 1", accepted)
	}
	settings, found, err := st.LLMEndpoint(context.Background())
	if err != nil || !found {
		t.Fatalf("read saved endpoint: found=%v err=%v", found, err)
	}
	if settings.Revision != 1 || settings.BaseURL != "https://llm.example.com/v1" {
		t.Fatalf("serialized save produced revision %d at %s", settings.Revision, settings.BaseURL)
	}
}
