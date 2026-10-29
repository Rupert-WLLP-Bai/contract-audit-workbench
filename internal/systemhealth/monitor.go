package systemhealth

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

type dockerContainer struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	Labels map[string]string `json:"Labels"`
}

var readinessPaths = map[string]string{"rustfs": "/health", "grafana": "/api/health", "prometheus": "/-/ready", "tempo": "/ready", "otel-collector": "/"}
var endpointClient = &http.Client{Timeout: time.Second}

type dockerInspection struct {
	State struct {
		Status    string
		StartedAt string
		ExitCode  int
		Health    *struct{ Status string }
	}
	RestartCount int
}

func (m *Monitor) Snapshot(ctx context.Context) Snapshot {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	out := Snapshot{SchemaVersion: SchemaVersion, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Mode: "embedded", Source: "process", Services: catalog()}
	expected, missingIsUnknown := m.deploymentSnapshot(ctx, &out)
	m.setDatabaseTarget(&out)
	m.appendModels(&out)
	out.Services = append(out.Services, jevService(m.observedJevConfig(ctx)), m.minerService(ctx))
	out.Groups = buildGroups(out.Services, mergeExpectations(m.config.ExpectedReplicas, expected), missingIsUnknown)
	out.Architecture = architectureInventory(out.Services, out.Source, out.Cluster)
	return out
}

func (m *Monitor) deploymentSnapshot(ctx context.Context, out *Snapshot) (map[string]replicaExpectation, bool) {
	if m.config.Kubernetes != nil {
		out.Mode, out.Source = "kubernetes", "kubernetes"
		return m.kubernetes(ctx, out)
	}
	if m.config.Project != "" {
		out.Mode, out.Source = "distributed", "docker"
		m.containers(ctx, out)
		return nil, out.Source == "docker_unavailable"
	}
	m.embeddedServices(ctx, out)
	return nil, false
}

func (m *Monitor) embeddedServices(ctx context.Context, out *Snapshot) {
	for i := range out.Services {
		out.Services[i].Detail = "单进程模式未配置独立服务容器。"
	}
	if m.config.DatabaseReady != nil {
		for i := range out.Services {
			if out.Services[i].ID == "db" {
				item := &out.Services[i]
				item.Kind = "integration"
				item.LogicalID = item.ID
				item.ObservedAt = out.ObservedAt
				item.Target = "当前业务数据库"
				started := time.Now()
				err := m.config.DatabaseReady(ctx)
				item.Status = "ready"
				item.Detail = "当前数据库连接探测成功。"
				if err != nil {
					item.Status = "unavailable"
					item.Detail = "当前数据库连接探测失败。"
				}
				item.Probe = &Probe{Kind: "database", Status: item.Status, DurationMS: time.Since(started).Milliseconds()}
			}
		}
	}
}

// A configured database URL is authoritative in every deployment mode.
func (m *Monitor) setDatabaseTarget(out *Snapshot) {
	if target := safeTarget(m.config.DatabaseTarget); target != "" {
		for i := range out.Services {
			if out.Services[i].ID == "db" {
				out.Services[i].Target = target
			}
		}
	}
}

func (m *Monitor) appendModels(out *Snapshot) {
	for _, external := range []struct{ id, name, model string }{{"review-model", "审查模型", m.config.ModelName}, {"ocr-model", "OCR 模型", m.config.OCRName}} {
		service := Service{ID: external.id, Name: external.name, Category: "external", Kind: "integration", Status: "unconfigured", Detail: "未配置模型。", Target: safeTarget(m.config.ModelEndpoint), DependsOn: []string{}, LogicalID: external.id}
		if external.model != "" {
			service.Status = "unknown"
			service.Detail = "已配置 " + external.model + "；健康刷新不发起付费推理。"
		}
		out.Services = append(out.Services, service)
	}
}

// This reads only the credential-free status route; it never starts inference.
func (m *Monitor) observedJevConfig(ctx context.Context) JevConfig {
	jevConfig := m.config.Jev
	if jevConfig.GatewayURL != "" {
		if status, ok := FetchJevGatewayStatus(ctx, jevConfig.GatewayURL); ok {
			jevConfig.GatewayStatus = status.Status
			jevConfig.GatewayReason = status.Reason
			if jevConfig.Name == "" {
				jevConfig.Name = status.Model
			}
			if jevConfig.Egress == "" {
				jevConfig.Egress = status.Egress
			}
			if len(jevConfig.Features) == 0 {
				jevConfig.Features = status.Features
			}
		}
	}
	return jevConfig
}

type observationState struct{ status, detail string }

func jevService(config JevConfig) Service {
	service := Service{ID: "jev-model", Name: "TypeSafe Jev 决策模型", Category: "external", Kind: "integration", Target: safeTarget(config.Endpoint), DependsOn: []string{}, LogicalID: "jev-model"}
	state := jevObservation(config)
	service.Status, service.Detail = state.status, state.detail
	return service
}

func jevObservation(config JevConfig) observationState {
	if config.Mode == "" || config.Mode == "off" {
		return observationState{"unconfigured", "Jev 已关闭；审查使用确定性结果。"}
	}
	if !config.Valid {
		return observationState{"unavailable", "TypeSafe Jev 配置无效（" + config.Reason + "）；审查使用确定性回退。"}
	}
	if config.Provider == "direct" && !config.KeyConfigured {
		return observationState{"unconfigured", "默认开启但未配置 TYPESAFE_API_KEY；审查使用确定性回退。"}
	}
	states := map[string]observationState{
		"unconfigured": {"unconfigured", "默认开启，但 model-gateway 未配置 TYPESAFE_API_KEY；审查使用确定性回退。"},
		"unavailable":  {"unavailable", "model-gateway 报告 Jev 不可用（" + config.GatewayReason + "）；审查使用确定性回退。"},
		"off":          {"unconfigured", "默认开启，但 model-gateway 报告 Jev 已关闭；审查使用确定性回退。"},
	}
	if state, ok := states[config.GatewayStatus]; ok {
		return state
	}
	summary := strings.TrimSpace(strings.Join([]string{config.Name, config.Mode, config.Egress, strings.Join(config.Features, ",")}, "，"))
	if config.Name == "jev-fixture" || config.Mode == "fixture" {
		return observationState{"unknown", "Jev 使用确定性 fixture（" + summary + "）；不发起外部推理。"}
	}
	return observationState{"unknown", "默认启用 TypeSafe Jev（" + summary + "）；健康刷新不发起付费推理。"}
}

func (m *Monitor) minerService(ctx context.Context) Service {
	miner := Service{ID: "mineru", Name: "MinerU 文档解析", Category: "external", Kind: "integration", Status: "unconfigured", Detail: "未配置 MinerU 服务地址。", Target: safeTarget(m.config.MinerUURL), DependsOn: []string{}, LogicalID: "mineru"}
	if miner.Target != "" {
		started := time.Now()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(miner.Target, "/")+"/v1/health", nil)
		var result struct {
			Status string `json:"status"`
		}
		if err == nil {
			err = getJSON(endpointClient, request, &result)
		}
		miner.Status = "unavailable"
		miner.Detail = "MinerU 健康端点不可用。"
		if err == nil && result.Status == "ok" {
			miner.Status = "ready"
			miner.Detail = "MinerU 解析服务健康端点可达；未发起文档推理。"
		}
		miner.Probe = &Probe{Kind: "http", Status: miner.Status, DurationMS: time.Since(started).Milliseconds()}
	}
	return miner
}

type dockerTarget struct {
	container dockerContainer
	serviceID string
}

type dockerInspectionResult struct {
	inspection dockerInspection
	err        error
}

const (
	dockerInspectTimeout = 750 * time.Millisecond
	dockerInspectLimit   = 8
)

func (m *Monitor) containers(ctx context.Context, out *Snapshot) {
	targets, err := m.listDockerTargets(ctx)
	indices := prepareDockerServices(out)
	if err != nil {
		out.Source = "docker_unavailable"
		markDockerUnavailable(out)
		return
	}
	results := m.inspectDockerTargets(ctx, targets)
	observed := map[string]bool{}
	for i, target := range targets {
		item := dockerServiceRow(out, target, indices, observed)
		if results[i].err != nil {
			out.Source = "docker_partial"
			continue
		}
		applyDockerInspection(ctx, item, results[i].inspection)
	}
}

func (m *Monitor) listDockerTargets(ctx context.Context) ([]dockerTarget, error) {
	filters := `{"label":["com.docker.compose.project=` + m.config.Project + `"]}`
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(m.config.DockerURL, "/")+"/containers/json?all=1&filters="+url.QueryEscape(filters), nil)
	var containers []dockerContainer
	if err == nil {
		err = getJSON(m.config.Client, request, &containers)
	}
	if err != nil {
		return nil, err
	}
	var targets []dockerTarget
	for _, container := range containers {
		serviceID := container.Labels["com.docker.compose.service"]
		if container.Labels["com.docker.compose.project"] == m.config.Project && serviceID != "" {
			targets = append(targets, dockerTarget{container: container, serviceID: serviceID})
		}
	}
	return targets, nil
}

func prepareDockerServices(out *Snapshot) map[string]int {
	indices := make(map[string]int, len(out.Services))
	for i := range out.Services {
		item := &out.Services[i]
		indices[item.ID] = i
		item.ObservedAt = out.ObservedAt
		if !optional(item.ID) {
			item.Status = "unavailable"
			item.Detail = "当前项目未发现预期服务容器。"
		}
	}
	return indices
}

func markDockerUnavailable(out *Snapshot) {
	for i := range out.Services {
		if !optional(out.Services[i].ID) {
			out.Services[i].Status = "unknown"
			out.Services[i].Detail = "Docker 状态采集不可用，无法确认服务状态。"
		}
	}
}

// Each target owns one result slot; an inspection failure cannot cancel peers.
func (m *Monitor) inspectDockerTargets(ctx context.Context, targets []dockerTarget) []dockerInspectionResult {
	results := make([]dockerInspectionResult, len(targets))
	var inspections errgroup.Group
	inspections.SetLimit(dockerInspectLimit)
	for i := range targets {
		i := i
		inspections.Go(func() error {
			inspectCtx, cancel := context.WithTimeout(ctx, dockerInspectTimeout)
			defer cancel()
			request, requestErr := http.NewRequestWithContext(inspectCtx, http.MethodGet,
				strings.TrimRight(m.config.DockerURL, "/")+"/containers/"+url.PathEscape(targets[i].container.ID)+"/json", nil)
			if requestErr == nil {
				requestErr = getJSON(m.config.Client, request, &results[i].inspection)
			}
			results[i].err = requestErr
			return nil
		})
	}
	_ = inspections.Wait()
	return results
}

func dockerServiceRow(out *Snapshot, target dockerTarget, indices map[string]int, observed map[string]bool) *Service {
	container := target.container
	logicalID := target.serviceID
	index, exists := indices[logicalID]
	if !exists || observed[logicalID] {
		name := logicalID
		rowID := logicalID
		if observed[logicalID] {
			rowID = logicalID + "/" + container.ID
		}
		index = len(out.Services)
		out.Services = append(out.Services, Service{ID: rowID, Name: name, Category: "application", Kind: "container", DependsOn: []string{}, LogicalID: logicalID})
	}
	observed[logicalID] = true
	item := &out.Services[index]
	name := container.ID
	if len(container.Names) > 0 {
		name = strings.TrimPrefix(container.Names[0], "/")
	}
	item.LogicalID = logicalID
	item.InstanceID = container.ID
	item.Workload = logicalID
	item.ObservedAt = out.ObservedAt
	item.Container = &Container{ID: container.ID, Name: name, Image: container.Image}
	item.Status = "unknown"
	item.Detail = "容器元数据读取失败或超时。"
	return item
}

func applyDockerInspection(ctx context.Context, item *Service, inspection dockerInspection) {
	item.Container.State = inspection.State.Status
	item.Container.StartedAt = inspection.State.StartedAt
	item.Container.Restarts = inspection.RestartCount
	if inspection.State.Health != nil {
		item.Container.Health = inspection.State.Health.Status
	}
	switch inspection.State.Status {
	case "running":
		applyRunningContainer(ctx, item)
	case "created", "restarting":
		item.Status, item.Detail = "starting", "容器尚未就绪。"
	case "exited":
		item.Status, item.Detail = "stopped", "容器已停止。"
		if inspection.State.ExitCode == 0 && (item.LogicalID == "migrate" || strings.HasSuffix(item.LogicalID, "-init")) {
			item.Status, item.Detail = "completed", "一次性初始化任务已成功完成。"
		}
	default:
		item.Status, item.Detail = "stopped", "容器未处于运行状态。"
	}
}

func applyRunningContainer(ctx context.Context, item *Service) {
	states := map[string]observationState{
		"healthy":   {"ready", "容器运行中，Docker 健康检查通过。"},
		"unhealthy": {"unavailable", "容器运行中，Docker 健康检查失败。"},
		"starting":  {"starting", "容器正在等待健康检查。"},
	}
	state, ok := states[item.Container.Health]
	if !ok {
		state = observationState{"unknown", "容器运行中，未配置 Docker 健康检查。"}
	}
	item.Status, item.Detail = state.status, state.detail
	path, hasProbe := readinessPaths[item.LogicalID]
	if item.Container.Health != "" || !hasProbe {
		return
	}
	started := time.Now()
	item.Status = probeReadiness(ctx, strings.TrimRight(item.Target, "/")+path)
	item.Detail = "服务就绪端点探测失败。"
	if item.Status == "ready" {
		item.Detail = "容器运行中，服务就绪端点可达。"
	}
	item.Probe = &Probe{Kind: "http", Status: item.Status, DurationMS: time.Since(started).Milliseconds()}
}

func probeReadiness(ctx context.Context, endpoint string) string {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "unavailable"
	}
	response, err := endpointClient.Do(request)
	if err != nil {
		return "unavailable"
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		return "ready"
	}
	return "unavailable"
}
