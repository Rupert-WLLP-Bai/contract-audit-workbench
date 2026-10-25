package systemhealth

import (
	"fmt"
	"strings"
)

// ArchitectureEntry compares a production architecture target with evidence
// from the current deployment monitor. It never represents acceptance.
type ArchitectureEntry struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Category     string   `json:"category"`
	Target       string   `json:"target"`
	Observed     string   `json:"observed"`
	State        string   `json:"state"`
	Verification string   `json:"verification"`
	ServiceIDs   []string `json:"service_ids"`
	Reference    string   `json:"reference"`
}

type architectureTarget struct {
	id, name, category, target, reference string
	serviceIDs                            []string
}

var architectureTargets = []architectureTarget{
	{"kubernetes", "Kubernetes 集群", "platform", "生产工作负载运行于 Kubernetes 集群", "https://kubernetes.io/docs/concepts/overview/", nil},
	{"gateway-api", "Gateway API", "platform", "使用 Kubernetes Gateway API 管理入口路由", "https://gateway-api.sigs.k8s.io/", nil},
	{"istio-ambient", "Istio Ambient 与 ztunnel", "platform", "使用 Istio Ambient 数据面并由 ztunnel 提供节点级 L4 代理", "https://istio.io/latest/docs/ambient/architecture/ztunnel/", nil},
	{"model-waypoint", "模型 Waypoint", "platform", "模型 API 流量经 Istio Ambient 模型 Waypoint 执行 L7 策略路由", "https://istio.io/latest/docs/ambient/usage/waypoint/", []string{"model-waypoint"}},
	{"tool-waypoint", "工具 Waypoint", "platform", "工具 API 流量经 Istio Ambient 工具 Waypoint 执行 L7 策略路由", "https://istio.io/latest/docs/ambient/usage/waypoint/", []string{"tool-waypoint"}},
	{"postgres-ha", "CloudNativePG / PostgreSQL 3 节点", "storage", "CloudNativePG 管理 PostgreSQL 3 节点高可用集群", "https://cloudnative-pg.io/documentation/current/", []string{"db"}},
	{"rustfs-erasure", "RustFS 4 节点 / 16 PVC", "storage", "RustFS 4 节点，每节点 4 个持久卷声明（共 16 PVC）", "https://docs.rustfs.com/", []string{"rustfs"}},
	{"nats-jetstream", "NATS JetStream 3 节点 R=3", "messaging", "NATS JetStream 3 节点集群，stream 副本数 R=3", "https://docs.nats.io/nats-concepts/jetstream", []string{"nats"}},
	{"keda-postgres", "KEDA（PostgreSQL 权威任务状态）", "platform", "KEDA Prometheus scaler 查询由 PostgreSQL 权威任务状态导出的可领取与等待作业指标；不使用 NATS 原始队列深度，也不增加直接 PostgreSQL scaler 查询", "https://keda.sh/docs/latest/scalers/prometheus/", []string{"keda-operator", "keda"}},
	{"hpa", "Horizontal Pod Autoscaler", "platform", "使用 Kubernetes HPA 管理工作负载副本数", "https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/", nil},
	{"oidc-enterprise", "企业 OIDC 身份提供方", "identity", "优先使用企业 OIDC 身份提供方；自托管回退时使用独立 Keycloak 与 identity-db", "https://openid.net/developers/how-connect/", nil},
	{"keycloak-fallback", "Keycloak 自托管回退", "identity", "仅在企业 OIDC 不可用或不适用时可选部署 Keycloak 回退", "https://www.keycloak.org/documentation", []string{"keycloak"}},
	{"identity-db", "Keycloak 独立身份数据库", "identity", "自托管 Keycloak 回退使用与业务库隔离的独立 PostgreSQL identity-db", "https://www.postgresql.org/docs/current/", []string{"identity-db"}},
	{"otel-collector", "OpenTelemetry Collector", "observability", "使用 OpenTelemetry Collector 接收、处理并导出遥测数据", "https://opentelemetry.io/docs/collector/", []string{"otel-collector"}},
	{"prometheus", "Prometheus", "observability", "使用 Prometheus 抓取并存储指标", "https://prometheus.io/docs/introduction/overview/", []string{"prometheus"}},
	{"tempo", "Tempo", "observability", "使用 Grafana Tempo 存储和查询分布式链路", "https://grafana.com/docs/tempo/latest/", []string{"tempo"}},
	{"grafana", "Grafana", "observability", "使用 Grafana 查询并展示指标与链路", "https://grafana.com/docs/grafana/latest/", []string{"grafana"}},
	{"parser-ocr-workers", "Parser / OCR Worker", "application", "由解析与 OCR Worker 执行文档解析任务", "https://docs.nats.io/nats-concepts/jetstream/consumers", []string{"parser-worker", "ocr-worker", "audit-worker", "mineru", "ocr-model"}},
	{"outbox-relay", "Outbox Relay", "application", "由 Outbox Relay 从 PostgreSQL 持久化 Outbox 发布通知", "https://docs.nats.io/using-nats/developer/develop_jetstream/model_deep_dive", []string{"outbox-relay"}},
	{"postgres-recovery", "PostgreSQL PITR / 对象备份恢复", "storage", "提供 PostgreSQL 时间点恢复（PITR）及对象存储备份恢复", "https://cloudnative-pg.io/documentation/current/backup/", []string{"postgres-backup", "object-backup"}},
	{"pgbouncer", "PgBouncer（可选）", "storage", "可选部署 PgBouncer 连接池", "https://www.pgbouncer.org/", []string{"pgbouncer"}},
	{"audit-workers", "审查 Worker 并发", "application", "部署 6–20 个审查 Worker，每个 Worker 并行处理 3–5 个案件", "https://kubernetes.io/docs/concepts/workloads/autoscaling/horizontal-pod-autoscale/", []string{"audit-worker"}},
	{"private-model-api", "私有模型 API 限流", "application", "私有模型 API 统一施加全局并发、RPM、TPM 上限，GPU 目标为 0", "https://kubernetes.io/docs/concepts/policy/resource-quotas/", []string{"model-gateway", "review-model"}},
	{"frontend-edge", "三前端与边缘入口", "application", "部署业务、管理、门户三个前端，并由边缘网关统一入口", "https://gateway-api.sigs.k8s.io/", []string{"client", "admin", "portal", "gateway"}},
}

func architectureInventory(services []Service, source string, cluster *Cluster) []ArchitectureEntry {
	entries := make([]ArchitectureEntry, 0, len(architectureTargets))
	for _, target := range architectureTargets {
		entry := ArchitectureEntry{
			ID: target.id, Name: target.name, Category: target.category,
			Target: target.target, State: "not_connected", Verification: "NOT_RUN",
			ServiceIDs: append([]string{}, target.serviceIDs...), Reference: target.reference,
		}
		if len(target.serviceIDs) == 0 {
			entry.Observed = "当前采集器没有此架构组件的集群或服务观测来源。"
		} else if strings.HasSuffix(source, "_unavailable") {
			entry.Observed = "状态观测源不可用，无法确认相关服务是否存在或其状态。"
			if source == "docker_unavailable" {
				entry.Observed = "Docker 状态源不可用，无法确认相关服务是否存在或其状态。"
			}
		} else {
			count := 0
			states := make([]string, 0, len(target.serviceIDs))
			for _, service := range services {
				matches := false
				for _, id := range target.serviceIDs {
					if service.ID == id || len(service.ID) > len(id) && service.ID[:len(id)] == id && service.ID[len(id)] == '/' {
						matches = true
						break
					}
				}
				if matches && (service.Container != nil || service.Probe != nil) {
					count++
					states = append(states, fmt.Sprintf("%s=%s", service.Name, service.Status))
				}
			}
			if count > 0 {
				entry.State = "observed"
				observedBy := "容器/数据库探测"
				if source == "docker" {
					observedBy = "Docker/数据库探测"
				}
				entry.Observed = fmt.Sprintf("%s观察到 %d 个相关服务实例（%s）；仅为实例状态，无法证明目标集群、复制、备份或验收能力。", observedBy, count, strings.Join(states, "、"))
			} else {
				entry.Observed = "当前服务观测中未发现相关服务实例。"
			}
		}
		if target.id == "kubernetes" {
			if source == "kubernetes" && cluster != nil {
				readyNodes := 0
				for _, node := range cluster.Nodes {
					if node.Status == "ready" {
						readyNodes++
					}
				}
				entry.State = "observed"
				entry.Observed = fmt.Sprintf("Kubernetes API 观察到 %d 个 Ready 节点（共 %d 个节点）；单机集群观测不证明物理高可用，验收未运行。", readyNodes, len(cluster.Nodes))
			} else {
				entry.Observed = "当前采集器没有 Kubernetes 集群观测源；无法判断集群是否存在或连接状态。"
				if source == "kubernetes_unavailable" {
					entry.Observed = "Kubernetes 集群观测源不可用；无法判断集群状态。"
				}
				entry.State = "not_connected"
			}
		}
		entries = append(entries, entry)
	}
	return entries
}
