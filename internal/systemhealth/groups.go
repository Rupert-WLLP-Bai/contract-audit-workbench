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
	collected := groupedServices{groups: map[string]*ServiceGroup{}, statuses: map[string][]string{}}
	for _, service := range services {
		collected.add(service)
	}
	rows := make([]ServiceGroup, 0, len(collected.order))
	for _, id := range collected.order {
		group := collected.groups[id]
		expectation, declared := expected[id]
		if !declared {
			expectation = replicaExpectation{source: "unavailable"}
		}
		group.ExpectedInstances, group.ExpectedSource = expectation.desired, expectation.source
		group.Status = groupStatus(group, collected.statuses[id], declared, missingIsUnknown)
		group.Detail = groupDetail(group, missingIsUnknown)
		rows = append(rows, *group)
	}
	return rows
}

type groupedServices struct {
	order    []string
	groups   map[string]*ServiceGroup
	statuses map[string][]string
}

func (g *groupedServices) add(service Service) {
	logical := service.LogicalID
	if logical == "" {
		logical = service.ID
	}
	group, exists := g.groups[logical]
	if !exists {
		g.order = append(g.order, logical)
		group = &ServiceGroup{ID: logical, Name: service.Name, Category: service.Category}
		g.groups[logical] = group
	}
	// Catalog placeholders are not runtime observations.
	if service.Container != nil || (service.Kind == "integration" && service.Status != "unconfigured") {
		group.ObservedInstances++
	}
	if service.Status == "ready" {
		group.ReadyInstances++
	}
	g.statuses[logical] = append(g.statuses[logical], service.Status)
	if service.ObservedAt > group.ObservedAt {
		group.ObservedAt = service.ObservedAt
	}
}

func groupStatus(group *ServiceGroup, statuses []string, declared, missingIsUnknown bool) string {
	if group.ObservedInstances == 0 {
		return unobservedGroupStatus(group, declared, missingIsUnknown)
	}
	counts := map[string]int{}
	for _, status := range statuses {
		counts[status]++
	}
	if counts["completed"] == group.ObservedInstances {
		return "completed"
	}
	if group.ReadyInstances == group.ObservedInstances {
		return readyGroupStatus(group)
	}
	if group.ReadyInstances > 0 {
		return "degraded"
	}
	if counts["starting"] > 0 {
		return "starting"
	}
	if counts["unavailable"] > 0 || counts["stopped"] > 0 {
		return "unavailable"
	}
	return "unknown"
}

func unobservedGroupStatus(group *ServiceGroup, declared, missingIsUnknown bool) string {
	if missingIsUnknown {
		return "unknown"
	}
	if group.ExpectedInstances > 0 || (!declared && !optional(group.ID)) {
		return "unavailable"
	}
	return "unconfigured"
}

func readyGroupStatus(group *ServiceGroup) string {
	if group.ExpectedInstances > 0 && group.ObservedInstances < group.ExpectedInstances {
		return "degraded"
	}
	return "ready"
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
