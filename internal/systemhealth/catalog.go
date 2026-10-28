package systemhealth

// catalog lists expected deployment services, including optional profiles. New
// Compose services discovered from Docker are retained even before being named.
func catalog() []Service {
	type definition struct {
		id, name, category, target string
		dependencies               []string
	}
	definitions := []definition{
		{"client", "业务工作台", "frontend", "http://client:8080", []string{"gateway"}},
		{"admin", "规则管理工作台", "frontend", "http://admin:8080/admin/", []string{"gateway"}},
		{"portal", "API 文档站点", "frontend", "http://portal:8080/portal/", []string{"gateway"}},
		{"gateway", "边缘网关", "application", "http://gateway:8080", []string{"core-api", "document-service", "audit-execution", "tool-gateway"}},
		{"core-api", "核心业务 API", "application", "http://core-api:8081", []string{"db", "nats"}},
		{"document-service", "文档服务", "application", "http://document-service:8082", []string{"db", "rustfs"}},
		{"audit-execution", "审查执行服务", "application", "http://audit-execution:8083", []string{"db", "nats"}},
		{"audit-worker", "审查 Worker", "application", "http://audit-worker:8086", []string{"db", "nats", "document-service", "model-gateway", "tool-gateway"}},
		{"model-gateway", "模型网关", "application", "http://model-gateway:8084", []string{"db", "review-model", "ocr-model"}},
		{"tool-gateway", "工具网关", "application", "http://tool-gateway:8085", []string{"db"}},
		{"db", "PostgreSQL 业务库", "middleware", "db:5432", []string{}},
		{"rustfs", "RustFS 对象存储", "middleware", "http://rustfs:9000", []string{}},
		{"nats", "NATS JetStream", "middleware", "nats://nats:4222", []string{}},
		{"keycloak", "Keycloak 身份服务", "identity", "http://keycloak:8080", []string{"identity-db"}},
		{"identity-db", "PostgreSQL 身份库", "identity", "identity-db:5432", []string{}},
		{"otel-collector", "OpenTelemetry Collector", "observability", "http://otel-collector:13133", []string{"tempo", "prometheus"}},
		{"tempo", "Tempo 链路存储", "observability", "http://tempo:3200", []string{}},
		{"prometheus", "Prometheus 指标存储", "observability", "http://prometheus:9090", []string{"otel-collector"}},
		{"grafana", "Grafana 监控面板", "observability", "http://grafana:3000", []string{"tempo", "prometheus"}},
		{"health-monitor", "健康采集器", "observability", "http://health-monitor:8092", []string{}},
	}
	out := make([]Service, 0, len(definitions)+3)
	for _, d := range definitions {
		out = append(out, Service{ID: d.id, Name: d.name, Category: d.category, Kind: "container", Status: "unconfigured", Detail: "当前部署未发现此服务容器。", Target: d.target, DependsOn: d.dependencies, LogicalID: d.id})
	}
	return out
}

func optional(id string) bool {
	switch id {
	case "keycloak", "identity-db", "otel-collector", "tempo", "prometheus", "grafana", "jev-model":
		return true
	}
	return false
}
