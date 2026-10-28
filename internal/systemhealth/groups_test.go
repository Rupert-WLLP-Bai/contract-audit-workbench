package systemhealth

import (
	"strings"
	"testing"
)

func TestParseExpectedReplicasIsExplicitAndBounded(t *testing.T) {
	parsed, err := ParseExpectedReplicas("core-api=2, audit-worker=3,nats=3")
	if err != nil || parsed["core-api"] != 2 || parsed["audit-worker"] != 3 || parsed["nats"] != 3 {
		t.Fatalf("valid replica declaration rejected: %v %v", parsed, err)
	}
	for _, raw := range []string{"core-api", "core-api=2,,nats=3", "core api=2", "core-api=100", "core-api=-1"} {
		if _, err := ParseExpectedReplicas(raw); err == nil {
			t.Fatalf("invalid replica declaration accepted: %q", raw)
		}
	}
}

func TestGroupsSeparateExpectedFromObserved(t *testing.T) {
	services := []Service{
		{ID: "core-api/a", LogicalID: "core-api", Name: "核心业务 API", Category: "application", Kind: "container", Status: "ready", Container: &Container{ID: "a"}, ObservedAt: "2026-10-27T00:00:00Z"},
		{ID: "core-api/b", LogicalID: "core-api", Name: "核心业务 API", Category: "application", Kind: "container", Status: "ready", Container: &Container{ID: "b"}, ObservedAt: "2026-10-27T00:00:00Z"},
		{ID: "audit-worker", LogicalID: "audit-worker", Name: "审查 Worker", Category: "application", Kind: "container", Status: "unavailable", Detail: "missing"},
		{ID: "review-model", LogicalID: "review-model", Name: "审查模型", Category: "external", Kind: "integration", Status: "unknown"},
	}
	expected := map[string]replicaExpectation{
		"core-api":     {desired: 3, source: "deployment"},
		"audit-worker": {desired: 1, source: "config"},
	}
	groups := buildGroups(services, expected, false)
	byID := map[string]ServiceGroup{}
	for _, group := range groups {
		byID[group.ID] = group
	}
	core := byID["core-api"]
	if core.ObservedInstances != 2 || core.ReadyInstances != 2 || core.ExpectedInstances != 3 || core.ExpectedSource != "deployment" || core.Status != "degraded" {
		t.Fatalf("expected/observed replica state conflated: %+v", core)
	}
	worker := byID["audit-worker"]
	if worker.ObservedInstances != 0 || worker.ExpectedInstances != 1 || worker.Status != "unavailable" || !strings.Contains(worker.Detail, "部署配置") {
		t.Fatalf("missing expected instance not reported: %+v", worker)
	}
	model := byID["review-model"]
	if model.ObservedInstances != 1 || model.Status != "unknown" || model.ExpectedSource != "unavailable" {
		t.Fatalf("unprobed integration state lost: %+v", model)
	}
}
