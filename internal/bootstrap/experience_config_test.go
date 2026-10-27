package bootstrap

import (
	"strings"
	"testing"
)

func TestExperienceResultDeliveryRequiresSeparateCompleteConfiguration(t *testing.T) {
	for _, name := range []string{"CORE_SERVICE_URL", "WORKER_CORE_TOKEN", "WORKER_DOCUMENT_TOKEN", "WORKER_MODEL_TOKEN", "WORKER_TOOL_TOKEN"} {
		t.Setenv(name, "")
	}
	client, err := configuredExperienceResultAcceptor()
	if err != nil || client != nil {
		t.Fatalf("unconfigured delivery: client=%v err=%v", client, err)
	}
	t.Setenv("CORE_SERVICE_URL", "http://core-api:8081")
	if _, err := configuredExperienceResultAcceptor(); err == nil {
		t.Fatal("endpoint without worker credential was accepted")
	}
	token := strings.Repeat("a", 40)
	t.Setenv("WORKER_CORE_TOKEN", token)
	t.Setenv("WORKER_DOCUMENT_TOKEN", token)
	if _, err := configuredExperienceResultAcceptor(); err == nil {
		t.Fatal("shared document and core credentials were accepted")
	}
	t.Setenv("WORKER_DOCUMENT_TOKEN", strings.Repeat("b", 40))
	client, err = configuredExperienceResultAcceptor()
	if err != nil || client == nil {
		t.Fatalf("valid delivery setup: client=%v err=%v", client, err)
	}
}
