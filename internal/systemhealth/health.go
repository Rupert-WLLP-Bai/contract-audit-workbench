// Package systemhealth exposes observed deployment metadata without credentials.
package systemhealth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const SchemaVersion = "system-health/v1"

type Container struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Image     string `json:"image"`
	State     string `json:"state"`
	Health    string `json:"health"`
	StartedAt string `json:"started_at"`
	Restarts  int    `json:"restarts"`
}

type Probe struct {
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
}

type Service struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Category  string     `json:"category"`
	Kind      string     `json:"kind"`
	Status    string     `json:"status"`
	Detail    string     `json:"detail"`
	Target    string     `json:"target"`
	DependsOn []string   `json:"depends_on"`
	Container *Container `json:"container"`
	Probe     *Probe     `json:"probe"`
	// LogicalID is the stable service identity. InstanceID is the stable
	// runtime identity (Pod UID or container ID) and is what a client must use
	// when it keeps one instance selected across refreshes. ID remains the
	// unique display key for this row.
	LogicalID  string `json:"logical_id,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	NodeName   string `json:"node_name,omitempty"`
	Workload   string `json:"workload,omitempty"`
	ObservedAt string `json:"observed_at,omitempty"`
}

// ServiceGroup separates a logical service from its observed instances and
// carries controller-derived replica expectations. ExpectedSource is
// "deployment" when Kubernetes reported it, "catalog" for an explicit static
// deployment definition, and "unavailable" when no controller data exists.
type ServiceGroup struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Category          string `json:"category"`
	Status            string `json:"status"`
	Detail            string `json:"detail"`
	ExpectedInstances int    `json:"expected_instances"`
	ObservedInstances int    `json:"observed_instances"`
	ReadyInstances    int    `json:"ready_instances"`
	ExpectedSource    string `json:"expected_source"`
	ObservedAt        string `json:"observed_at,omitempty"`
}

type Snapshot struct {
	SchemaVersion string              `json:"schema_version"`
	ObservedAt    string              `json:"observed_at"`
	Mode          string              `json:"mode"`
	Source        string              `json:"source"`
	Cluster       *Cluster            `json:"cluster"`
	Services      []Service           `json:"services"`
	Groups        []ServiceGroup      `json:"groups"`
	Architecture  []ArchitectureEntry `json:"architecture"`
}

type Reader interface {
	Snapshot(context.Context) Snapshot
}

type Config struct {
	Project          string
	DockerURL        string
	Client           *http.Client
	Kubernetes       *KubernetesConfig
	DatabaseReady    func(context.Context) error
	DatabaseTarget   string
	ModelName        string
	ModelEndpoint    string
	OCRName          string
	MinerUURL        string
	ExpectedReplicas map[string]int
}

type Monitor struct{ config Config }

// ParseExpectedReplicas reads an explicit deployment declaration such as
// "core-api=2,audit-worker=3". It is not derived from observed Pods.
func ParseExpectedReplicas(raw string) (map[string]int, error) {
	result := map[string]int{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return result, nil
	}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		id, count, ok := strings.Cut(item, "=")
		id, count = strings.TrimSpace(id), strings.TrimSpace(count)
		if !ok || id == "" || len(id) > 64 || count == "" {
			return nil, errors.New("SYSTEM_HEALTH_EXPECTED_REPLICAS must be id=count pairs")
		}
		for _, c := range id {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.') {
				return nil, errors.New("SYSTEM_HEALTH_EXPECTED_REPLICAS contains an invalid service id")
			}
		}
		value, err := strconv.Atoi(count)
		if err != nil || value < 0 || value > 99 {
			return nil, errors.New("SYSTEM_HEALTH_EXPECTED_REPLICAS count must be an integer in 0..99")
		}
		result[id] = value
	}
	return result, nil
}

// ExpectedReplicasFromEnv is the deployment-side declaration consumed by the
// standalone health monitor. Invalid values fail startup instead of silently
// producing a misleading health page.
func ExpectedReplicasFromEnv() (map[string]int, error) {
	return ParseExpectedReplicas(os.Getenv("SYSTEM_HEALTH_EXPECTED_REPLICAS"))
}

func New(config Config) *Monitor {
	if config.Client == nil {
		config.Client = &http.Client{Timeout: 2 * time.Second}
	}
	return &Monitor{config: config}
}

func FromEnv(ready func(context.Context) error, databaseTarget string) Reader {
	remote := strings.TrimSpace(os.Getenv("SYSTEM_HEALTH_URL"))
	if remote != "" {
		return &Remote{URL: remote, Client: &http.Client{Timeout: 5 * time.Second}}
	}
	return New(Config{DatabaseReady: ready, DatabaseTarget: databaseTarget, ModelName: os.Getenv("LLM_MODEL"), ModelEndpoint: os.Getenv("LLM_BASE_URL"), OCRName: os.Getenv("OCR_MODEL"), MinerUURL: os.Getenv("MINERU_URL")})
}

// DockerClient is used only by the isolated deployment monitor, never the API.
func DockerClient(socket string) *http.Client {
	return &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
}

func safeTarget(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

type Remote struct {
	URL    string
	Client *http.Client
}

func (r *Remote) Snapshot(ctx context.Context) Snapshot {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(r.URL, "/")+"/snapshot", nil)
	var result Snapshot
	if err == nil {
		err = getJSON(r.Client, request, &result)
	}
	if err == nil && result.SchemaVersion == SchemaVersion && len(result.Services) > 0 {
		if len(result.Architecture) == 0 {
			result.Architecture = architectureInventory(result.Services, result.Source, result.Cluster)
		}
		return result
	}
	services := []Service{{ID: "health-monitor", Name: "健康采集器", Category: "observability", Kind: "service", Status: "unavailable", Detail: "无法读取部署状态，请检查健康采集器。", Target: safeTarget(r.URL), DependsOn: []string{}, Container: nil, Probe: nil}}
	return Snapshot{SchemaVersion: SchemaVersion, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Mode: "distributed", Source: "monitor_unavailable", Services: services, Architecture: architectureInventory(services, "monitor_unavailable", nil)}
}

func getJSON(client *http.Client, request *http.Request, result any) error {
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("health metadata unavailable")
	}
	return json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(result)
}
