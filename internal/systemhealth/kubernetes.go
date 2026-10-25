package systemhealth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	serviceAccountDir   = "/var/run/secrets/kubernetes.io/serviceaccount"
	kubernetesBodyLimit = 2 << 20
)

type KubernetesConfig struct {
	APIURL    string
	Token     string
	Namespace string
	Client    *http.Client
	tokenPath string
	initErr   error
}

type Cluster struct {
	Status     string        `json:"status"`
	Namespace  string        `json:"namespace"`
	ObservedAt string        `json:"observed_at"`
	Detail     string        `json:"detail"`
	Nodes      []ClusterNode `json:"nodes"`
}

type ClusterNode struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Version string `json:"version"`
}

// KubernetesConfigFromEnv enables observation only when the service environment
// identifies an in-cluster API. It never reads a kubeconfig or external credentials.
func KubernetesConfigFromEnv() *KubernetesConfig {
	host := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST"))
	port := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_PORT"))
	if host == "" || port == "" {
		return nil
	}
	config := &KubernetesConfig{}
	config.tokenPath = filepath.Join(serviceAccountDir, "token")
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 || strings.ContainsAny(host, "/?#@") {
		config.initErr = errors.New("invalid in-cluster API address")
		return config
	}
	config.APIURL = "https://" + net.JoinHostPort(host, port)
	token, err := os.ReadFile(config.tokenPath)
	if err != nil || strings.TrimSpace(string(token)) == "" {
		config.initErr = errors.New("in-cluster service account token unavailable")
		return config
	}
	config.Token = strings.TrimSpace(string(token))
	config.Namespace = strings.TrimSpace(os.Getenv("POD_NAMESPACE"))
	if config.Namespace == "" {
		namespace, readErr := os.ReadFile(filepath.Join(serviceAccountDir, "namespace"))
		if readErr == nil {
			config.Namespace = strings.TrimSpace(string(namespace))
		}
	}
	if config.Namespace == "" {
		config.initErr = errors.New("in-cluster namespace unavailable")
		return config
	}
	caPEM, err := os.ReadFile(filepath.Join(serviceAccountDir, "ca.crt"))
	if err != nil {
		config.initErr = errors.New("in-cluster CA unavailable")
		return config
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		config.initErr = errors.New("invalid in-cluster CA")
		return config
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	config.Client = &http.Client{Timeout: 2 * time.Second, Transport: transport}
	return config
}

func (m *Monitor) kubernetes(ctx context.Context, out *Snapshot) {
	config := m.config.Kubernetes
	out.Cluster = &Cluster{Status: "unavailable", Namespace: config.Namespace, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Detail: "Kubernetes 状态采集不可用。", Nodes: []ClusterNode{}}
	indices := make(map[string]int, len(out.Services))
	for i := range out.Services {
		indices[out.Services[i].ID] = i
		if !optional(out.Services[i].ID) {
			out.Services[i].Status = "unavailable"
			out.Services[i].Detail = "当前命名空间未发现预期服务 Pod。"
		}
	}
	if config.initErr != nil || config.APIURL == "" || config.Namespace == "" {
		out.Source = "kubernetes_unavailable"
		markKubernetesUnavailable(out)
		return
	}
	client := config.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	var nodes kubernetesNodeList
	err := m.kubernetesGet(ctx, client, config, "/api/v1/nodes", &nodes)
	if err != nil {
		out.Source = "kubernetes_unavailable"
		markKubernetesUnavailable(out)
		return
	}
	var pods kubernetesPodList
	path := "/api/v1/namespaces/" + url.PathEscape(config.Namespace) + "/pods"
	if err = m.kubernetesGet(ctx, client, config, path, &pods); err != nil {
		out.Source = "kubernetes_unavailable"
		markKubernetesUnavailable(out)
		return
	}
	out.Cluster.Nodes = make([]ClusterNode, 0, len(nodes.Items))
	readyNodes := 0
	for _, node := range nodes.Items {
		item := ClusterNode{Name: node.Metadata.Name, Status: "unknown", Version: node.Status.NodeInfo.KubeletVersion}
		for _, condition := range node.Status.Conditions {
			if condition.Type == "Ready" {
				switch condition.Status {
				case "True":
					item.Status = "ready"
					readyNodes++
				case "False":
					item.Status = "unavailable"
				}
				break
			}
		}
		out.Cluster.Nodes = append(out.Cluster.Nodes, item)
	}
	if len(nodes.Items) > 0 && readyNodes == len(nodes.Items) {
		out.Cluster.Status = "ready"
		out.Cluster.Detail = fmt.Sprintf("观察到 %d/%d 个 Ready Kubernetes 节点；节点数量不证明物理高可用。", readyNodes, len(nodes.Items))
	} else {
		out.Cluster.Detail = fmt.Sprintf("观察到 %d/%d 个 Ready Kubernetes 节点；集群未完全就绪。", readyNodes, len(nodes.Items))
	}
	seenPods := make(map[string]int)
	for _, pod := range pods.Items {
		name := pod.Metadata.Labels["app.kubernetes.io/name"]
		if name == "" {
			continue
		}
		index, expected := indices[name]
		count := seenPods[name]
		var service Service
		if expected {
			service = out.Services[index]
		} else {
			service = Service{ID: name, Name: name, Category: "application", Kind: "container", Status: "unknown", Detail: "Kubernetes Pod 已发现，尚未确认就绪状态。", DependsOn: []string{}}
		}
		if count > 0 {
			service.ID = name + "/" + pod.Metadata.Name
			service.Name = name + " / " + pod.Metadata.Name
		}
		container := podContainerMetadata(pod)
		service.Container = &container
		service.Kind = "container"
		service.Status, service.Detail = podReadiness(pod)
		if count == 0 && expected {
			out.Services[index] = service
		} else {
			out.Services = append(out.Services, service)
		}
		seenPods[name] = count + 1
	}
}

func markKubernetesUnavailable(out *Snapshot) {
	for i := range out.Services {
		if out.Services[i].Container != nil {
			out.Services[i].Status = "unknown"
			out.Services[i].Detail = "Kubernetes 状态采集不可用，无法确认 Pod 状态。"
		} else if !optional(out.Services[i].ID) {
			out.Services[i].Status = "unknown"
			out.Services[i].Detail = "Kubernetes 状态采集不可用，无法确认服务是否存在。"
		}
	}
}

func (m *Monitor) kubernetesGet(ctx context.Context, client *http.Client, config *KubernetesConfig, path string, result any) error {
	token := config.Token
	if config.tokenPath != "" {
		current, err := os.ReadFile(config.tokenPath)
		if err != nil || strings.TrimSpace(string(current)) == "" {
			return errors.New("Kubernetes credentials unavailable")
		}
		token = strings.TrimSpace(string(current))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(config.APIURL, "/")+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("Kubernetes metadata unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, kubernetesBodyLimit+1))
	if err != nil || len(data) > kubernetesBodyLimit {
		return errors.New("Kubernetes metadata unavailable")
	}
	if err := json.Unmarshal(data, result); err != nil {
		return errors.New("Kubernetes metadata unavailable")
	}
	return nil
}

type kubernetesNodeList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Status struct {
			NodeInfo struct {
				KubeletVersion string `json:"kubeletVersion"`
			} `json:"nodeInfo"`
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	} `json:"items"`
}

type kubernetesPodList struct {
	Items []kubernetesPod `json:"items"`
}

type kubernetesPod struct {
	Metadata struct {
		Name   string            `json:"name"`
		UID    string            `json:"uid"`
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		Containers []struct {
			Name string `json:"name"`
		} `json:"containers"`
	} `json:"spec"`
	Status struct {
		Phase      string `json:"phase"`
		Conditions []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
		ContainerStatuses []struct {
			Name         string `json:"name"`
			Image        string `json:"image"`
			Ready        bool   `json:"ready"`
			RestartCount int    `json:"restartCount"`
			State        struct {
				Running *struct {
					StartedAt string `json:"startedAt"`
				} `json:"running"`
				Waiting *struct {
					Reason string `json:"reason"`
				} `json:"waiting"`
				Terminated *struct {
					Reason string `json:"reason"`
				} `json:"terminated"`
			} `json:"state"`
		} `json:"containerStatuses"`
	} `json:"status"`
}

func podReadiness(pod kubernetesPod) (string, string) {
	switch pod.Status.Phase {
	case "Pending":
		return "starting", "Kubernetes Pod 正在等待调度或启动。"
	case "Failed":
		return "unavailable", "Kubernetes Pod 运行失败。"
	case "Succeeded":
		return "completed", "Kubernetes Pod 已成功完成。"
	case "Unknown":
		return "unknown", "Kubernetes Pod 状态未知。"
	}
	readyKnown, readyCondition := false, false
	for _, condition := range pod.Status.Conditions {
		if condition.Type == "Ready" {
			readyKnown = true
			readyCondition = condition.Status == "True"
			break
		}
	}
	if !readyKnown {
		return "unknown", "Kubernetes Pod 就绪或容器状态信息不完整。"
	}
	if !readyCondition {
		return "unavailable", "Kubernetes Pod Ready 条件未满足。"
	}
	if len(pod.Spec.Containers) == 0 || len(pod.Status.ContainerStatuses) != len(pod.Spec.Containers) {
		return "unknown", "Kubernetes Pod 就绪或容器状态信息不完整。"
	}
	for _, container := range pod.Status.ContainerStatuses {
		if container.State.Waiting != nil || container.State.Terminated != nil {
			return "unavailable", "Kubernetes Pod 存在未就绪或已终止容器。"
		}
		if container.State.Running == nil || !container.Ready {
			return "unavailable", "Kubernetes Pod 容器尚未就绪。"
		}
	}
	if pod.Status.Phase == "Running" {
		return "ready", "Kubernetes Pod 正在运行且所有容器已就绪。"
	}
	return "unknown", "Kubernetes Pod 就绪状态未知。"
}

func podContainerMetadata(pod kubernetesPod) Container {
	item := Container{ID: pod.Metadata.UID, Name: pod.Metadata.Name, State: pod.Status.Phase, Health: "unknown"}
	for _, container := range pod.Status.ContainerStatuses {
		item.Restarts += container.RestartCount
		if item.Image == "" {
			item.Image = container.Image
		}
		if container.State.Running != nil {
			item.StartedAt = container.State.Running.StartedAt
			item.State = "running"
		} else if container.State.Waiting != nil {
			item.State = "waiting"
		} else if container.State.Terminated != nil {
			item.State = "terminated"
		}
	}
	return item
}
