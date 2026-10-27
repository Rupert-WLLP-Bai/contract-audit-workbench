package systemhealth

import "fmt"

// replicaExpectation is the desired replica count for one logical service and
// where that number came from. A zero desired count with source "unavailable"
// means the collector has no controller or static deployment declaration.
type replicaExpectation struct {
	desired int
	source  string
}

func catalogExpectations() map[string]replicaExpectation {
	result := map[string]replicaExpectation{}
	threshold := 1
	for _, service := range catalog() {
		if optional(service.ID) {
			threshold = 0
		} else {
			threshold = 1
		}
		result[service.ID] = replicaExpectation{desired: threshold, source: "catalog"}
	}
	return result
}

func mergeExpectations(configured map[string]int, deployments map[string]replicaExpectation) map[string]replicaExpectation {
	result := catalogExpectations()
	for id, count := range configured {
		result[id] = replicaExpectation{desired: count, source: "config"}
	}
	for id, expectation := range deployments {
		result[id] = expectation
	}
	return result
}

// buildGroups derives one row per logical service from the instance rows. It
// never infers the desired count from the observed count: when no controller
// or deployment declaration exists, expected_source stays "unavailable".
func buildGroups(services []Service, expected map[string]replicaExpectation, missingIsUnknown bool) []ServiceGroup {
	order := make([]string, 0, len(services))
	groups := make(map[string]*ServiceGroup, len(services))
	statuses := make(map[string][]string, len(services))
	for _, service := range services {
		logical := service.LogicalID
		if logical == "" {
			logical = service.ID
		}
		group, exists := groups[logical]
		if !exists {
			order = append(order, logical)
			group = &ServiceGroup{ID: logical, Name: service.Name, Category: service.Category}
			groups[logical] = group
		}
		// A container is a runtime instance; a configured integration is one
		// observed endpoint. A catalog placeholder that was not found is neither.
		observed := service.Container != nil ||
			(service.Kind == "integration" && service.Status != "unconfigured")
		if observed {
			group.ObservedInstances++
		}
		if service.Status == "ready" {
			group.ReadyInstances++
		}
		statuses[logical] = append(statuses[logical], service.Status)
		if service.ObservedAt > group.ObservedAt {
			group.ObservedAt = service.ObservedAt
		}
	}
	for _, id := range order {
		group := groups[id]
		expectation, declared := expected[id]
		if !declared {
			expectation = replicaExpectation{source: "unavailable"}
		}
		group.ExpectedInstances, group.ExpectedSource = expectation.desired, expectation.source
		group.Status = groupStatus(group, statuses[id], declared, missingIsUnknown)
		group.Detail = groupDetail(group, missingIsUnknown)
	}
	rows := make([]ServiceGroup, 0, len(order))
	for _, id := range order {
		rows = append(rows, *groups[id])
	}
	return rows
}

func groupStatus(group *ServiceGroup, statuses []string, declared, missingIsUnknown bool) string {
	if group.ObservedInstances == 0 {
		if missingIsUnknown {
			return "unknown"
		}
		if group.ExpectedInstances > 0 {
			return "unavailable"
		}
		if !declared && !optional(group.ID) {
			return "unavailable"
		}
		return "unconfigured"
	}
	starting, unavailable, stopped, completed, unknown := 0, 0, 0, 0, 0
	for _, status := range statuses {
		switch status {
		case "starting":
			starting++
		case "unavailable":
			unavailable++
		case "stopped":
			stopped++
		case "completed":
			completed++
		case "unknown":
			unknown++
		}
	}
	if completed == group.ObservedInstances {
		return "completed"
	}
	if group.ReadyInstances == group.ObservedInstances {
		if group.ExpectedInstances > 0 && group.ObservedInstances < group.ExpectedInstances {
			return "degraded"
		}
		return "ready"
	}
	if group.ReadyInstances > 0 {
		return "degraded"
	}
	if starting > 0 {
		return "starting"
	}
	if unavailable > 0 || stopped > 0 {
		return "unavailable"
	}
	if unknown > 0 {
		return "unknown"
	}
	return "unknown"
}

func groupDetail(group *ServiceGroup, missingIsUnknown bool) string {
	if group.ObservedInstances == 0 && missingIsUnknown {
		return "采集器未能读取实例清单；不能把缺少观测判定为服务故障或正常。"
	}
	switch group.ExpectedSource {
	case "deployment":
		return fmt.Sprintf("就绪 %d/%d；已观测 %d 个实例；期望值来自 Kubernetes Deployment。",
			group.ReadyInstances, group.ExpectedInstances, group.ObservedInstances)
	case "config":
		return fmt.Sprintf("就绪 %d/%d；已观测 %d 个实例；期望值来自部署配置。",
			group.ReadyInstances, group.ExpectedInstances, group.ObservedInstances)
	case "catalog":
		return fmt.Sprintf("就绪 %d/%d；已观测 %d 个实例；期望值来自内置服务清单。",
			group.ReadyInstances, group.ExpectedInstances, group.ObservedInstances)
	default:
		return fmt.Sprintf("就绪 %d；已观测 %d 个实例；当前没有控制器或部署配置可提供期望副本数。",
			group.ReadyInstances, group.ObservedInstances)
	}
}
