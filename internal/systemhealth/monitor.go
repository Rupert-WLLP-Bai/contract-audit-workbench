package systemhealth

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
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
	databaseTarget := safeTarget(m.config.DatabaseTarget)
	if m.config.Kubernetes != nil {
		out.Mode = "kubernetes"
		out.Source = "kubernetes"
		m.kubernetes(ctx, &out)
	} else if m.config.Project != "" {
		out.Mode = "distributed"
		out.Source = "docker"
		m.containers(ctx, &out)
	} else {
		for i := range out.Services {
			out.Services[i].Detail = "单进程模式未配置独立服务容器。"
		}
		if m.config.DatabaseReady != nil {
			for i := range out.Services {
				if out.Services[i].ID == "db" {
					item := &out.Services[i]
					item.Kind = "integration"
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
	// The selected database identity is authoritative in every deployment mode;
	// keep container DNS targets only when no credential-free URL was configured.
	if databaseTarget != "" {
		for i := range out.Services {
			if out.Services[i].ID == "db" {
				out.Services[i].Target = databaseTarget
			}
		}
	}
	for _, external := range []struct{ id, name, model string }{{"review-model", "审查模型", m.config.ModelName}, {"ocr-model", "OCR 模型", m.config.OCRName}} {
		service := Service{ID: external.id, Name: external.name, Category: "external", Kind: "integration", Status: "unconfigured", Detail: "未配置模型。", Target: safeTarget(m.config.ModelEndpoint), DependsOn: []string{}}
		if external.model != "" {
			service.Status = "unknown"
			service.Detail = "已配置 " + external.model + "；健康刷新不发起付费推理。"
		}
		out.Services = append(out.Services, service)
	}
	miner := Service{ID: "mineru", Name: "MinerU 文档解析", Category: "external", Kind: "integration", Status: "unconfigured", Detail: "未配置 MinerU 服务地址。", Target: safeTarget(m.config.MinerUURL), DependsOn: []string{}}
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
	out.Services = append(out.Services, miner)
	out.Architecture = architectureInventory(out.Services, out.Source, out.Cluster)
	return out
}

func (m *Monitor) containers(ctx context.Context, out *Snapshot) {
	filters := `{"label":["com.docker.compose.project=` + m.config.Project + `"]}`
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(m.config.DockerURL, "/")+"/containers/json?all=1&filters="+url.QueryEscape(filters), nil)
	var containers []dockerContainer
	if err == nil {
		err = getJSON(m.config.Client, request, &containers)
	}
	indices := make(map[string]int, len(out.Services))
	for i := range out.Services {
		indices[out.Services[i].ID] = i
	}
	for i := range out.Services {
		if !optional(out.Services[i].ID) {
			out.Services[i].Status = "unavailable"
			out.Services[i].Detail = "当前项目未发现预期服务容器。"
		}
	}
	if err != nil {
		out.Source = "docker_unavailable"
		for i := range out.Services {
			if !optional(out.Services[i].ID) {
				out.Services[i].Status = "unknown"
				out.Services[i].Detail = "Docker 状态采集不可用，无法确认服务状态。"
			}
		}
		return
	}
	observed := map[string]bool{}
	for _, container := range containers {
		if container.Labels["com.docker.compose.project"] != m.config.Project {
			continue
		}
		id := container.Labels["com.docker.compose.service"]
		if id == "" {
			continue
		}
		index, exists := indices[id]
		if !exists || observed[id] {
			name := id
			if observed[id] {
				id += "/" + container.ID
			}
			index = len(out.Services)
			out.Services = append(out.Services, Service{ID: id, Name: name, Category: "application", Kind: "container", DependsOn: []string{}})
		}
		observed[id] = true
		item := &out.Services[index]
		name := container.ID
		if len(container.Names) > 0 {
			name = strings.TrimPrefix(container.Names[0], "/")
		}
		item.Container = &Container{ID: container.ID, Name: name, Image: container.Image}
		item.Status = "unknown"
		item.Detail = "容器元数据无法读取。"
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(m.config.DockerURL, "/")+"/containers/"+url.PathEscape(container.ID)+"/json", nil)
		var inspection dockerInspection
		if err == nil {
			err = getJSON(m.config.Client, request, &inspection)
		}
		if err != nil {
			continue
		}
		item.Container.State = inspection.State.Status
		item.Container.StartedAt = inspection.State.StartedAt
		item.Container.Restarts = inspection.RestartCount
		if inspection.State.Health != nil {
			item.Container.Health = inspection.State.Health.Status
		}
		switch inspection.State.Status {
		case "running":
			switch item.Container.Health {
			case "healthy":
				item.Status = "ready"
				item.Detail = "容器运行中，Docker 健康检查通过。"
			case "unhealthy":
				item.Status = "unavailable"
				item.Detail = "容器运行中，Docker 健康检查失败。"
			case "starting":
				item.Status = "starting"
				item.Detail = "容器正在等待健康检查。"
			default:
				item.Status = "unknown"
				item.Detail = "容器运行中，未配置 Docker 健康检查。"
			}
			if item.Container.Health == "" {
				if path, ok := readinessPaths[item.ID]; ok {
					started := time.Now()
					request, probeErr := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(item.Target, "/")+path, nil)
					status := "unavailable"
					if probeErr == nil {
						response, callErr := endpointClient.Do(request)
						if callErr == nil {
							if response.StatusCode == http.StatusOK {
								status = "ready"
							}
							_ = response.Body.Close()
						}
					}
					item.Status = status
					item.Detail = "服务就绪端点探测失败。"
					if status == "ready" {
						item.Detail = "容器运行中，服务就绪端点可达。"
					}
					item.Probe = &Probe{Kind: "http", Status: status, DurationMS: time.Since(started).Milliseconds()}
				}
			}
		case "created", "restarting":
			item.Status = "starting"
			item.Detail = "容器尚未就绪。"
		case "exited":
			item.Status = "stopped"
			item.Detail = "容器已停止。"
			if inspection.State.ExitCode == 0 && (container.Labels["com.docker.compose.service"] == "migrate" || strings.HasSuffix(container.Labels["com.docker.compose.service"], "-init")) {
				item.Status = "completed"
				item.Detail = "一次性初始化任务已成功完成。"
			}
		default:
			item.Status = "stopped"
			item.Detail = "容器未处于运行状态。"
		}
	}
}
